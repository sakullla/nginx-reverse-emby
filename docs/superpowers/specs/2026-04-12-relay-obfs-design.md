# Relay 首段混淆设计

日期：2026-04-12

## 背景

当前 relay 隧道以 TLS 作为外层传输，业务流量在 relay 建连成功后以透明 TCP 字节流形式直接透传。对于 HTTP 规则和 L4 TCP 规则，如果后端业务本身也是 TLS，则外层 TLS 负载中的前几段应用数据会高度接近一个完整的内层 TLS 握手起始段，容易形成明显的 `TLS in TLS` 特征。

这次工作目标不是重新设计 relay 协议，也不是引入全会话的重度混淆，而是在尽量小的改动范围内，增加一个默认关闭、按规则启用的首段混淆能力，降低首段特征暴露风险，并为未来可能的全会话模式预留协议扩展位。

## 目标

- 为 HTTP 规则和 L4 TCP 规则增加一个默认关闭的 relay 隐私增强开关
- 开关仅放在对应规则表单的 `Relay 配置` Tab 内
- 开启后，对 relay 隧道内首段 TCP 载荷做有限窗口的分片和 padding，打散首段长度特征
- 采用 fail-closed 语义：规则要求该能力时，全链路任一节点不支持都直接建连失败
- 保持未开启时的现有 relay 行为完全不变
- 为未来扩展到更强模式保留协议结构，但本次不实现全会话分片

## 非目标

- 不修改 Relay Listener 资源模型，不把该能力做成监听器级开关
- 不实现全会话持续分片、节奏扰动或长期 padding
- 不尝试针对 TLS ClientHello 做协议识别，仅按 TCP 首段字节流处理
- 不改变 UDP 规则能力边界，L4 UDP 仍然不支持 relay，也不支持该开关

## 用户体验

### HTTP 规则

在 HTTP 规则表单的 `Relay 配置` Tab 中，Relay 链路配置区域下方新增一个布尔开关：

- 文案：`启用 Relay 隐私增强`
- 默认值：关闭
- 说明：开启后将对 relay 隧道首段流量进行混淆，降低 TLS in TLS 特征暴露风险

交互约束：

- 当 `relay_chain` 为空时，开关显示为禁用态，并提示当前为直连模式，开启无效
- 当 `relay_chain` 非空时，允许操作
- 编辑已有规则时，开关按已保存值回填

### L4 规则

在 L4 规则表单的 `Relay 配置` Tab 中，Relay 链路配置区域下方新增相同的布尔开关：

- 文案：`启用 Relay 隐私增强`
- 默认值：关闭
- 仅在 TCP 规则且 `relay_chain` 非空时允许开启

交互约束：

- UDP 规则沿用现有 relay 不支持逻辑，开关不生效并保持关闭
- 当 `relay_chain` 为空时，开关显示为禁用态，并提示当前为直连模式，开启无效
- 编辑已有规则时，开关按已保存值回填

## 方案对比

### 方案 A：首段有限窗口混淆

在 relay TLS 建连完成后，对隧道内首段 TCP 字节流进行短暂的 framing、分片和 padding，达到阈值后切回普通透明转发。

优点：

- 直接命中当前暴露风险所在的前几段数据
- 对现有 relay 透明转发模型侵入较小
- 性能和延迟成本可控
- 便于为未来全会话模式复用 frame 结构

缺点：

- 不能持续隐藏全会话长度分布特征
- 需要新增 framing 状态机与失败处理

### 方案 B：仅控制报文 padding

只对 `relayRequest` 和 `relayResponse` 做 padding，不改后续透传阶段。

优点：

- 实现最简单

缺点：

- 不能解决内层 TLS 首段暴露问题
- 与目标不匹配

### 方案 C：全会话分片与 padding

整条 relay 会话都通过分帧层传输。

优点：

- 隐匿能力最强

缺点：

- 实现和回归风险显著增加
- 对吞吐、延迟、复杂度和排障都有明显影响
- 超出本次范围

本次采用方案 A，并为未来升级到方案 C 预留协议扩展位。

## 数据模型

### 字段命名

规则级新增布尔字段：`relay_obfs`

命名理由：

- 这是一个针对 relay 隧道的技术性混淆开关
- 比 `relay_privacy` 更不容易被误解为泛化的安全承诺
- 后续若扩展为枚举模式，也可以自然演进为更具体的 relay 传输模式字段

### HTTP 规则

在 control-plane、agent model、前端 API 归一化结构中新增：

```json
{
  "relay_chain": [1, 2],
  "relay_obfs": true
}
```

约束：

- 默认值 `false`
- `relay_chain` 为空时不得为 `true`

### L4 规则

在 control-plane、agent model、前端 API 归一化结构中新增：

```json
{
  "protocol": "tcp",
  "relay_chain": [1, 2],
  "relay_obfs": true
}
```

约束：

- 默认值 `false`
- 仅 TCP 规则允许为 `true`
- `relay_chain` 为空时不得为 `true`

### Relay Listener

不新增 `relay_obfs` 字段。

原因：

- 该能力是“某条业务规则是否要求启用”的连接级属性，不是监听器资源的持久配置
- 如果放到监听器层，会产生“监听器支持但规则未要求”和“规则要求但监听器是否必须开启”的双重语义混乱

## 协议设计

### 控制面扩展

在现有 `relayRequest` 中增加一个能力声明块，用于表达本次连接要求的 relay 传输模式。建议形态如下：

```json
{
  "network": "tcp",
  "target": "backend.internal:443",
  "chain": [],
  "transport": {
    "mode": "first_segment_v1"
  }
}
```

语义：

- 未携带 `transport` 或 `mode=off`：走旧行为
- `mode=first_segment_v1`：要求启用首段混淆

这样后续可以平滑扩展为：

- `off`
- `first_segment_v1`
- `full_stream_v1`

本次只实现 `first_segment_v1`。

### fail-closed 语义

当规则开启 `relay_obfs` 后：

- 本地发起侧必须在 relay request 中显式声明 `mode=first_segment_v1`
- 每一跳 relay 节点如果不支持该模式，必须立即返回错误
- 任一跳失败都终止整条链路建立，不允许静默降级为普通 relay

错误需要区分：

- 配置非法：例如 `relay_chain` 为空、UDP 规则开启、字段与规则类型不匹配
- 协议不支持：例如远端 agent 版本过旧或未实现该模式
- 运行时 framing 失败：例如 frame 非法、超时、状态机错误

## 执行路径

### 建连阶段

1. 前端保存 HTTP/L4 规则时提交 `relay_obfs`
2. control-plane 持久化并下发该字段
3. HTTP proxy 和 L4 TCP proxy 在发现 `relay_chain` 非空且 `relay_obfs=true` 时，调用 `relay.Dial` 时附带 `transport.mode=first_segment_v1`
4. 第一跳 relay listener 收到后校验本节点是否支持该模式
5. 如果 request 还包含剩余 `chain`，则向下一跳继续透传该模式要求
6. 只有全链路都接受后，才进入首段混淆数据阶段

这里的“链路起点”需要明确：

- 在范围内的是“发起 relay 链路的本地代理进程到第一跳 relay”
- 以及第一跳 relay 到第二跳 relay、后续各 hop 之间
- 不在范围内的是终端用户到 HTTP/L4 规则前端监听地址这一段接入流量

也就是说，只要字节流已经进入 relay 隧道，那么从第一跳开始的第一段外层 TLS 负载就应当启用同样的首段混淆；否则第一跳仍会保留最明显的 `TLS in TLS` 特征，无法达到本次目标

### 数据阶段

当前 relay TLS 建连成功后会直接进入透明字节流转发。本次改为：

1. 当 `mode=first_segment_v1` 时，连接建立后先进入“首段 framing 阶段”
2. 发送侧缓存首段有限窗口数据
3. 把缓存中的真实字节拆成多个 `data` frame
4. 在 `data` frame 之间插入若干 `pad` frame
5. 当达到结束条件时，发送 `end` frame
6. 双方在收到 `end` 后切回普通透明转发，后续继续现有 `io.Copy`

首段 framing 从 relay 隧道的第一跳就开始生效，而不是只作用于 relay 中间 hop。对于一条 `代理进程 -> relay-1 -> relay-2 -> 后端服务` 的链路：

- `代理进程 -> relay-1` 之间的 outer TLS 负载需要做首段混淆
- `relay-1 -> relay-2` 之间如果继续转发 relay request，也沿用同样的传输模式要求
- 一旦最后一跳完成建连，数据面的首段混淆也同样在对应 hop 的 outer TLS 上传输

### 作用范围

该混淆不识别内层协议，不判断是否为 TLS，只按首段 TCP 字节流统一处理。

原因：

- 与当前需求一致
- 避免实现 fragile 的协议识别逻辑
- 未来即使承载的不是 TLS，也可复用该能力

## 帧格式

本次新增一层极简首段 frame 协议，仅用于 `first_segment_v1` 模式下的首段阶段。

建议 frame 类型：

- `data`：真实字节片段
- `pad`：随机填充，不参与重组
- `end`：标记首段阶段结束，双方恢复普通透传

建议保留统一长度前缀，避免无限读取；具体字段保持最小化即可。frame 只需要满足：

- 可区分类型
- 可携带长度
- 可被严格校验

不需要在本次引入压缩、重放保护、复杂序号或长期流控逻辑。

## 参数策略

首段混淆采用固定上限策略，而不是复杂自适应。

建议原则：

- 以字节上限为主，例如首段最多处理 4KB 到 8KB 量级
- 以短时间窗口为辅，防止低速连接长时间停留在 framing 状态
- 对 padding 总量、frame 数量设硬上限

推荐策略：

- 首段窗口有固定最大字节数
- 到达字节阈值后立即发送 `end`
- 若迟迟达不到阈值，则在短超时到达后也发送 `end`

这样可以控制额外延迟和状态复杂度，同时避免 framing 阶段无限持续。

## 前端设计

### HTTP RuleForm

在 `Relay 配置` Tab 的链路配置卡片下方新增一张设置卡片，包含：

- 一个布尔开关：`启用 Relay 隐私增强`
- 一段简短说明：用于降低 relay over TLS 时首段特征暴露
- 当 `relay_chain` 为空时的禁用提示

### L4 RuleForm

与 HTTP 规则保持相同布局，放在 `Relay 配置` Tab 的链路配置卡片下方。

约束：

- TCP 且存在 `relay_chain` 时可开启
- UDP 或无链路时禁用并给出说明

### 默认行为

- 创建新规则时默认关闭
- 编辑已有规则时回填历史值
- 未开启时前端不展示额外高级参数

## Control-Plane 校验

### HTTP 规则校验

- `relay_obfs` 默认 `false`
- 当 `relay_obfs=true` 且 `relay_chain` 为空时，返回 `invalid argument`

### L4 规则校验

- `relay_obfs` 默认 `false`
- 当协议不是 `tcp` 且 `relay_obfs=true` 时，返回 `invalid argument`
- 当 `relay_obfs=true` 且 `relay_chain` 为空时，返回 `invalid argument`

### 兼容性

- 旧数据未包含该字段时，按 `false` 处理
- 旧 agent 收到新规则前，control-plane 不负责自动探测链路支持性
- 支持性判断放在运行时建连阶段，由 relay request 协商结果决定

## Go-Agent 改动边界

### 模型层

- `go-agent/internal/model/http.go` 新增 `RelayObfs bool`
- `go-agent/internal/model/l4.go` 新增 `RelayObfs bool`

### relay 包

- `relayRequest` 新增传输模式字段
- `Dial` 支持把规则要求编码到 request
- 服务端在 `handleConn` 中校验模式并决定是否进入首段 framing 阶段
- 新增首段 frame 编解码与状态切换逻辑

### HTTP / L4 执行层

- HTTP proxy 在构建 relay transport 时把 `rule.RelayObfs` 传给 relay
- L4 TCP 在 dial relay upstream 时把 `rule.RelayObfs` 传给 relay
- 未开启时仍走当前透明 relay 路径

## 错误处理

以下情况必须明确失败并断开连接：

- 请求了 `first_segment_v1`，但本地或远端节点不支持
- 首段 frame 类型非法或长度非法
- framing 状态中超时未完成
- 收到非法状态转换，例如重复 `end` 或 `end` 后继续 frame

错误信息建议包含明确上下文，例如：

- `relay obfs requires non-empty relay_chain`
- `relay obfs is only supported for tcp rules`
- `relay transport mode first_segment_v1 is not supported by hop 31`
- `invalid relay obfs frame`

## 测试策略

### Control-Plane

- HTTP 规则默认值、序列化与反序列化
- L4 规则默认值、序列化与反序列化
- HTTP 开启 `relay_obfs` 但无 `relay_chain` 时拒绝保存
- L4 UDP 开启 `relay_obfs` 时拒绝保存
- L4 TCP 开启 `relay_obfs` 但无 `relay_chain` 时拒绝保存

### Frontend

- HTTP `RuleForm.vue` 的 `Relay 配置` Tab 出现开关
- L4 `L4RuleForm.vue` 的 `Relay 配置` Tab 出现开关
- 默认值为关闭
- `relay_chain` 为空时禁用并显示提示
- 编辑已有规则时正确回填
- 提交 payload 时正确携带 `relay_obfs`

### Go-Agent

- relay request 正确携带传输模式
- 未启用时完全兼容旧路径
- 不支持模式时 fail-closed
- 首段 frame 编码、解码、重组正确
- 添加 padding 后端到端字节流与原始输入一致
- 多 hop 场景下模式要求逐跳透传
- 收到非法 frame、超时、异常状态时连接被正确关闭

## 风险与取舍

- 首段混淆只能降低最前面一段特征暴露，不能掩盖全会话模式
- framing 阶段引入短暂缓存和额外状态，需要严格限制窗口与超时
- fail-closed 会让旧节点在新规则启用时直接失败，但这是明确要求的语义，也是避免“误以为已开启”的必要代价

## 实施建议

建议分三步实现：

1. 先打通数据模型、前端表单和 control-plane 校验
2. 再实现 relay request 的模式协商与 fail-closed 错误路径
3. 最后实现首段 frame 编解码、端到端测试和回归验证

这样可以把“规则层正确表达能力”和“传输层真正启用能力”分开验证，降低联调复杂度。
