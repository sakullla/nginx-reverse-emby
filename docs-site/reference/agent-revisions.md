# Agent revision API

配置写接口在静态校验和持久化成功后返回 `202 Accepted`。返回 202 表示配置已保存并创建了不可变 revision，不表示 runtime 已经生效。Panel 请求使用 `X-Panel-Token`；远端 Agent 协议使用该 Agent 自己的 `X-Agent-Token`。

`/panel-api` 与兼容前缀 `/api` 暴露相同契约。以下示例使用 `/panel-api`。

## 写入与 202 envelope

现有 HTTP、L4、Relay、证书、出口、Agent 设置、版本策略和备份导入写端点原地采用异步契约，没有第二套 v2 API。建议所有可重试写请求携带不超过 256 字节的 `Idempotency-Key`。

```bash
curl -i -X POST http://127.0.0.1:8080/panel-api/agents/local/rules \
  -H 'X-Panel-Token: <panel-token>' \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: rule-create-20260716-01' \
  --data '{"frontend_url":"http://127.0.0.1:18080","backends":[{"url":"http://127.0.0.1:8096"}]}'
```

典型响应：

```json
{
  "ok": true,
  "operation_id": "op-...",
  "agent_id": "local",
  "desired_revision": 42,
  "apply_status": "pending",
  "status_url": "/panel-api/operations/op-...",
  "no_op": false,
  "replayed": false,
  "agents": [
    {
      "agent_id": "local",
      "desired_revision": 42,
      "apply_status": "pending",
      "status_url": "/panel-api/agents/local/revisions/42",
      "snapshot_digest": "...",
      "no_op": false
    }
  ],
  "rule": {}
}
```

响应同时设置 `Location` 为 operation `status_url`。同一个 key 和等价请求返回原 operation/revision，并设置 `Idempotency-Replayed: true`；同 key 配不同 method/path/query/body 返回 `409 Conflict`。未提供 key 时，最终状态与资源等价性仍避免无意义 apply，但调用方不能依赖首次 envelope 被重放。

静态字段、引用、能力或冲突校验失败会返回 4xx，且不会创建 revision。配置已保存后即使 apply 失败，desired revision 仍保留，last-known-good generation 继续服务。

## 查询状态

```text
GET /panel-api/operations/{operation_id}
GET /panel-api/agents/{agent_id}/revisions/{revision}
GET /panel-api/revision-events?after={cursor}&limit={1..500}&operation_id={id}&agent_id={id}
```

operation 响应位于 `operation` 字段，包含 `operation_id`、`kind`、`apply_status`、`primary_agent_id`、`no_op`、`degraded`、`agents`、错误和时间字段。单 Agent revision 响应位于 `revision` 字段，包含：

- `desired_revision`、`applied_revision`、`last_known_good_revision`
- `apply_status`、`drain_status`、`blocked_by`、`no_op`
- `retry_cycle`、`attempt_count`、`next_attempt_at`
- `generation_id`、`attempts`、`generations`
- `error_code`、`error_message` 和创建/更新/生效/失败时间

`apply_status` 使用 `pending`、`applying`、`applied`、`failed`、`superseded`；跨 Agent 依赖部分失败时 operation 可为 `degraded`。`drain_status` 独立使用 `draining`、`drained`、`forced`。因此 `applied + draining` 是正常状态：新连接已经进入新 generation，旧会话仍在排空；不能把它显示为未生效。

事件接口按递增 `id` 返回 `events`、`next_cursor`、`has_more`。客户端应先消费事件或 panel monitor stream，再用 operation `status_url` 轮询兜底；所有状态响应都带 `Cache-Control: no-store`。

## 重试与回退

```text
POST /panel-api/agents/{agent_id}/revisions/{failed_revision}/retry
POST /panel-api/agents/{agent_id}/revisions/rollback
```

两者都接受 `Idempotency-Key` 并返回 202 envelope。retry 仅重新调度 failed revision；每个 retry cycle 的 attempt 重新从 1 开始。rollback 不改写历史，而是把 last-known-good snapshot 复制成新的最高 desired revision。对 applied revision 调用 retry 返回冲突。

自动 apply 每次 attempt 默认 60 秒，最多 5 次，采用 1 秒基数、30 秒上限的 full-jitter 指数退避。Agent 离线等待和尚未确认 `start` 的 lease 不消耗 attempt；更高 revision 可将旧 pending/applying 工作标记为 `superseded`。

## 远端 Agent 协议

```text
POST /panel-api/agent-revisions/pull
POST /panel-api/agent-revisions/{revision}/start
POST /panel-api/agent-revisions/{revision}/report
```

pull 返回最高可执行 desired revision、不可变 snapshot、digest、目标 version，以及绑定 `agent_id/revision/retry_cycle/attempt/lease_id` 的 lease。lease 还包含已固化的 `apply_timeout_seconds`、`drain_timeout_seconds` 和 deadline。Agent 完成 prepare 后用 start 提交 generation ID，随后只按单调状态报告 applied/failed/drained/forced。旧 lease、旧 attempt、Agent ID 不匹配或倒退报告被拒绝且不改变事实。

## Retention

Control plane 启动后立即执行一次 revision retention，并每 24 小时重跑。每个 Agent 保留最近 30 天且最多 500 个 revision；当前 desired/applied、last-known-good、active/draining generation 引用永久 pin。`Idempotency-Key` 记录保留 24 小时；无引用 generation artifact 同批清理。单次清理失败只记录日志，不会停止 HTTP 服务，后续周期继续重试。
