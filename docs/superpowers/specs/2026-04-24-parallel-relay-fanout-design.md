# Relay 分层并行扇出与逐跳诊断设计

## 背景

当前 HTTP/L4 规则使用 `relay_chain: []int` 表示一条线性 Relay 链路。运行时按该顺序建立单一路径，诊断报告只返回整体 backend/sample 延迟，不返回 Relay 每一跳延迟。

本设计将 Relay 链路升级为“分层并行扇出”：每一层可配置多个 Relay 节点，运行时结合现有观测优选模型并行竞争多条候选路径，最终只选择最快成功路径承载本次业务流量，同时诊断返回每条候选路径及每一跳延迟。

## 目标

- 支持规则配置 `relay_layers`，每一层包含一个或多个 Relay listener ID。
- 保持旧 `relay_chain` 兼容，旧配置自动视为每层一个节点。
- 业务流量采用 `race-first-success`：多路径并行建连，实际只使用最快成功路径，避免重复请求后端。
- 将并行路径选择接入现有观测优选模型，优先尝试稳定、低延迟、高吞吐路径，同时保留冷启动探索。
- 诊断结果返回每条路径、每一跳延迟、观测状态、选择结果和失败原因。

## 非目标

- 不实现对同一 HTTP 请求或 TCP 流的多路复制与多响应汇聚。
- 不在 Relay 服务端协议中实现真正的多播 fan-in/fan-out。
- 不移除旧 `relay_chain` 字段。
- 不改变现有 backend 负载均衡语义。

## 配置模型

新增字段：

```json
{
  "relay_layers": [[1, 2, 3], [4, 5]]
}
```

语义：

- 外层数组表示 Relay 层级。
- 内层数组表示该层可并行参与竞争的 Relay 节点。
- 上例会展开为 `1->4`、`1->5`、`2->4`、`2->5`、`3->4`、`3->5` 六条候选路径。
- `relay_chain: [1, 4]` 等价于 `relay_layers: [[1], [4]]`。
- 当 `relay_layers` 存在且非空时优先使用 `relay_layers`；否则回退 `relay_chain`。

校验规则：

- 每层至少一个 ID。
- ID 必须对应当前 snapshot 中存在且启用的 Relay listener。
- 同一路径内不允许重复 listener ID，避免环路。
- 限制最大展开路径数，防止组合爆炸；默认建议 32 条，可通过配置调整。

## 路径选择

运行时将 `relay_layers` 展开为候选路径列表。候选路径不是全部无差别同时拨号，而是先经过观测优选排序，再按限并发分批竞争。

排序输入：

- 路径级观测：整条 Relay path 到目标 backend 的成功率、延迟、吞吐、退避状态。
- 跳级观测：单个 Relay hop 到下一跳的握手延迟和失败状态。
- backend 观测：沿用现有 `backends.Cache` 对目标 backend 的排序能力。

选择流程：

1. 展开所有路径组合。
2. 为每条路径生成稳定 observation key。
3. 使用现有 `backends.Cache` 的优选信息排序路径。
4. 启动第一批高分路径并行拨号。
5. 首个成功路径成为 selected path，立即取消并关闭其他路径。
6. 如果一批全部失败或超时，启动下一批候选路径。
7. 所有路径失败时返回聚合错误，并记录失败观测。

推荐默认：

- `relay_path_race_concurrency`: 3。
- `relay_path_race_timeout`: 继承现有 Relay dial timeout。
- 冷启动探索比例复用现有 adaptive/cold exploration 逻辑。

## 观测模型集成

新增或扩展 observation key：

- 路径级：`relay_path|1-4|backend.example:443`。
- 跳级：`relay_hop|1|next=4`、`relay_hop|4|next=backend.example:443`。
- 兼容旧 key：旧 `relay|1-4|backend` 仍可读取或迁移到路径级 key。

成功观测：

- selected path 记录总拨号耗时、首包延迟和传输吞吐。
- 每个成功 hop 记录该跳握手延迟。
- 未被选中的已成功连接关闭后不记录为失败，只记录为 race loser 或轻量探索样本。

失败观测：

- 建连失败路径记录失败类型和退避。
- 可定位到具体 hop 的失败同步记录 hop 级失败。
- 被取消的路径不作为网络失败，避免污染优选模型。

## Relay 协议与运行时

现有 Relay 协议仍承载单一路径 `[]Hop`。并行扇出在客户端/agent 运行时层完成：

- 每条候选路径仍调用现有 `relay.DialWithResult(ctx, network, target, chain, provider, opts...)`。
- 不改变 Relay 服务端帧协议。
- race 管理器负责上下文取消、连接关闭、结果聚合和观测写入。
- 后续如果需要服务端原生多播，可在新协议版本中独立设计。

## 诊断结果

在现有 `Report` 结构中新增可选字段：

```json
{
  "relay_paths": [
    {
      "path": [1, 4],
      "selected": true,
      "success": true,
      "latency_ms": 42.7,
      "adaptive": { "preferred": true, "state": "warm" },
      "hops": [
        { "from": "client", "to_listener_id": 1, "latency_ms": 12.1, "success": true },
        { "from_listener_id": 1, "to_listener_id": 4, "latency_ms": 15.4, "success": true },
        { "from_listener_id": 4, "to": "backend.example:443", "latency_ms": 15.2, "success": true }
      ]
    }
  ],
  "selected_relay_path": [1, 4]
}
```

诊断行为：

- 对所有展开路径执行受限并发探测。
- 每条路径返回成功/失败、总延迟、每跳延迟和错误。
- 汇总最快成功路径、失败路径数、平均路径延迟。
- HTTP/L4 诊断 Modal 展示路径矩阵与逐跳瀑布图。

## API 与存储兼容

- 数据库规则表新增 `relay_layers` JSON 字段。
- API 响应同时返回 `relay_chain` 和 `relay_layers`。
- 创建/更新规则时接受两者；`relay_layers` 优先。
- 旧客户端只传 `relay_chain` 时行为不变。
- 前端保存新配置时写入 `relay_layers`，同时可派生单节点层到 `relay_chain` 以兼容旧 agent。

## 前端交互

规则表单新增“Relay 层级”编辑器：

- 每层可添加多个节点。
- 支持新增/删除层。
- 显示展开路径数和最大路径数限制提示。
- 兼容旧单链 UI，旧规则自动显示为每层一个节点。

诊断 Modal：

- 展示 selected path。
- 展示每条路径的状态、总延迟和 adaptive 状态。
- 展示每跳延迟，失败 hop 高亮错误。
- 支持按“优选顺序 / 延迟 / 成功状态”排序。

## 测试策略

Go control-plane：

- 存储 `relay_layers` 往返测试。
- API 创建/更新/兼容 `relay_chain` 测试。
- snapshot 下发包含 `relay_layers` 测试。

Go agent：

- `relay_chain` 兼容为单节点层测试。
- 多层路径展开和最大路径数校验测试。
- race-first-success 选择最快成功路径测试。
- 取消 loser 路径不记录失败测试。
- 结合 `backends.Cache` 优选排序测试。
- 诊断返回 `relay_paths[].hops[]` 测试。

Frontend：

- 表单可编辑多层多节点测试。
- 保存 payload 包含 `relay_layers` 测试。
- 诊断 Modal 展示每跳延迟测试。

## 风险与缓解

- 组合爆炸：限制最大路径数并按限并发分批 race。
- 连接风暴：默认并发 3，并接入退避状态跳过明显失败路径。
- 观测污染：取消路径不记失败，只有真实网络失败写入失败观测。
- 兼容风险：保留 `relay_chain`，旧配置和旧客户端不受影响。
- 延迟口径不一致：诊断字段明确区分路径总延迟、hop 握手延迟和业务 sample 延迟。

