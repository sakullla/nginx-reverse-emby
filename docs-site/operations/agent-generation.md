# Agent generation 运维

## 状态语义

配置保存、runtime 生效和旧会话排空是三个独立时点：

- `saved / pending`：数据库和 immutable snapshot 已提交，API 已返回 202，runtime 尚未 cutover。
- `applying`：Agent 已确认开始 prepare；一次真实 attempt 从此时计时。
- `applied + draining`：新 generation 已接管新连接，`applied_revision` 已更新；旧 generation 仅服务已有会话。
- `applied + drained`：旧 generation 已自然归零并释放。
- `applied + forced`：旧 generation 达到 drain timeout 或 generation 上限后被强制释放。
- `failed`：desired 配置保留，last-known-good generation 继续运行，可显式 retry 或 rollback。
- `superseded`：更高 desired revision 已取代旧 pending/applying 工作；旧 revision 不得覆盖新 revision。

普通修改和新增会优雅排空旧会话；delete 或 `enabled=false` 在新 generation ready 后立即关闭受影响旧会话。HTTP/1.1、HTTP/2、HTTP/3、WebSocket、L4 TCP/UDP 与 Relay 都遵守 generation ownership；UDP 按旧五元组/association 继续旧路径，新五元组进入 active generation。

同一 Agent 串行 apply，不同 Agent 可并行。跨 Agent dependency DAG 只保证依赖顺序和每个 Agent 原子性；部分失败保留已成功节点，并把 operation 标记为 `degraded`，不会自动补偿回滚。

## Timeout 配置

全局默认值：

```env
NRE_REVISION_APPLY_TIMEOUT=1m
NRE_REVISION_DRAIN_TIMEOUT=10m
```

按 Agent 覆盖使用单行 JSON；每个缺失字段回落到全局值：

```env
NRE_REVISION_AGENT_TIMEOUT_OVERRIDES={"edge-a":{"apply_timeout":"45s"},"edge-b":{"drain_timeout":"15m"},"local":{"apply_timeout":"2m","drain_timeout":"8m"}}
```

键必须是非空 Agent ID，值只能包含 `apply_timeout`、`drain_timeout`，duration 必须为正的 Go duration 字符串。malformed JSON、未知字段、null、非字符串或非正 duration 会阻止 control plane 启动，不会静默使用默认值。

Timeout 在 revision 创建时按“Agent 字段覆盖 -> global”解析并以秒固化。之后修改环境变量只影响新 revision，不会改变正在 applying/draining 的 generation。同一 Agent/module 最多保留 2 个 draining generation；创建第三个时强制释放最老一代并记录 `generation_limit`。

## 监控与排障

Panel token 保护的 Prometheus 文本端点：

```bash
curl -H 'X-Panel-Token: <panel-token>' http://127.0.0.1:8080/panel-api/metrics
```

Control plane 指标族：

- `nre_revision_queue_total` / `nre_revision_queue_duration_seconds_sum`
- `nre_revision_apply_total` / `nre_revision_apply_duration_seconds_sum`
- `nre_generation_drain_total` / `nre_generation_drain_duration_seconds_sum`
- `nre_generation_cutover_total` / `nre_generation_cutover_duration_seconds_sum`
- `nre_hot_restart_upgrade_total` / `nre_hot_restart_upgrade_duration_seconds_sum`

Embedded Agent 还暴露对应的 `nre_agent_revision_apply`、`nre_agent_generation_drain`、`nre_agent_generation_cutover`、`nre_agent_hot_restart_upgrade` 指标族。唯一 label 是有限枚举 `outcome`；`agent_id`、revision、generation、operation、attempt 和 lease 不作为 metric label，避免高基数。

详细关联写入结构化日志字段 `operation_id`、`agent_id`、`revision`、`generation_id`、`attempt`、`duration_ms`。token、secret、private key、certificate material、payload 和 `lease_id` 会被过滤。排障顺序：

1. 从 202 envelope 记录 `operation_id`、Agent revision 和 `status_url`。
2. 查询 operation，确认是否 `blocked_by` dependency、`failed`、`superseded` 或 `degraded`。
3. 查询 Agent revision 的 attempts/generations，区分 apply timeout 与 drain timeout。
4. 用 `/revision-events?operation_id=...` 从最后 cursor 续读，再按相同 correlation 字段检索日志。
5. failed 时先修复 `error_code/error_message` 指向的问题，再 retry；需要恢复配置内容时用 rollback 创建新 revision。

## Retention 与恢复

Revision ledger 持久化 pending/applying/applied/failed、attempt、generation 和事件。Control plane 重启后只恢复每个 Agent 的最新 desired 工作，遗留 applying 会按 lease/attempt 规则重新协调；Agent 离线不消耗 attempt。

Retention 启动即运行、之后每 24 小时运行：保留每 Agent 最近 30 天且最多 500 版，并 pin 当前 desired/applied、last-known-good 和 active/draining 引用；幂等键保留 24 小时。日志前缀 `[revision-retention]` 表示本轮维护失败，HTTP 和 apply 服务继续运行，下一周期重试。

## 迁移检查

- 升级前备份 `panel/data/` 或外部数据库，并保留当前 Agent 二进制。
- 先确认所有远端 Agent 能用 `X-Agent-Token` 调用 revision pull/start/report；旧同步写响应不能再作为“已生效”证据。
- 升级 control plane 后检查现有 Agent 已生成 baseline desired/applied pointer，再提交一个低风险修改。
- 验证写请求返回 202、operation 最终 applied、`applied_revision` 前进；有长连接时允许暂时显示 draining。
- 独立 Agent 自更新前按 [热升级运维](./agent-hot-upgrade.md) 核对平台、package digest 和回退指针。

发布验证命令：

```bash
cd panel/backend-go && go test ./...
cd go-agent && go test ./...
cd scripts/generation-soak && go test -run TestGenerationMatrix -count=1 ./...
cd scripts/generation-soak && go test -run TestGenerationSoak100 -count=1 ./...
```
