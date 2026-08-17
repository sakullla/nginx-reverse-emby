---
# Runtime 只读取这一处文件头机器区；不要在正文重复机器字段。
format: solution
# AI 在此填写非空摘要；详细内容写入下方预建章节。
summary: 在公开 plugin-sdk 增加 NodeAddressSource 与 DualStackListener，由 Host 注入；分享主机按 DDNS>IPv4>IPv6，插件不探测 IP、不开公网套接字。不改 HTTP 供应商。
---
# Technical Solution

## 目标与 Non-goals

目标（R1、R2）：

- 公开 SDK 提供节点对外地址快照与只读来源接口。
- 公开专用 TCP+UDP 同端口绑定，以及按 `listener_ref` 读取实际绑定的句柄。
- 分享主机选择与 `host:port` 格式化在 SDK 内可单测。

Non-goals（R3）：

- 不改 HTTP backend-provider 本机模型。
- 不新增 L4 插件供应商。
- 本阶段不改 manifest permission 枚举与 `sdk.lock.json`；句柄沿用既有 revocable-resource-handle。

## 事实 owner/consumer

| 事实 | Owner | Consumer |
| --- | --- | --- |
| 节点地址快照与选择规则 | `plugin-sdk/go/node_addresses.go` | 插件分享投影、Host 注入实现 |
| 双栈绑定与 Binding 句柄 | `plugin-sdk/go/dual_stack_listener.go` | 插件 Activate 后读端口 |
| `agent.ddns_domain` 与节点 IP 的采集 | Host 控制面 / Agent | `NodeAddressSource` 实现 |
| 公网套接字绑定 | Host | `DualStackListener` |
| HTTP 供应商合同 | `http_backend_provider.go` | 不在本方案修改 |

## 设计与状态变化

- `NodeAddresses` 三字段：DDNS、IPv4、IPv6。`SelectShareHost` 按该顺序取第一个 `ShareableHost` 通过的值。
- `ShareableHost` 拒绝未指定地址、回环、localhost / `*.localhost`、scheme、路径与非法字符。
- `DualStackListenBinding` 要求同一端口同时 TCP 与 UDP。`DualStackListener.Binding` 只读已绑定结果，插件不 `Listen`。
- `JoinShareHostPort` / `ValidL4BackendHost` 给 SIP002 与现有 L4 填地址用。
- 开发期 `sakullla-plugins` 用 replace 指向本 worktree 的 `plugin-sdk`。

## 失败行为

- 三者都不可分享：`SelectShareHost` 返回 ok=false，插件不得编造主机。
- 端口越界或非双栈：`Validate` 失败。
- 通配/回环：`JoinShareHostPort` 失败。
- Host 未注入句柄：插件保持既有 fail-closed / 分享不可用，不自己探测。

## 删除面

- 不在 SDK 再保留「插件自己探测公网 IP」或「分享写绑定通配地址」的分支。
- 不把节点地址做成新的 HTTP 供应商变体。

## 机械验收

- `SelectShareHost`：同时有三者时选 DDNS；无 DDNS 选 IPv4；否则 IPv6。
- 仅通配/回环/空：ok=false。
- 双栈 Validate：8388+TCP+UDP 通过；缺 UDP 或端口 0 失败。
- `JoinShareHostPort("203.0.113.10", 8388)` = `203.0.113.10:8388`；IPv6 带括号；`0.0.0.0` 失败。
- `http_backend_provider.go` 无跨主机语义变更。

## 实施 Unknowns

- Host 如何把控制面 `ddns_domain` 与节点地址注入 Agent 插件进程。
- `Binding` 与插件现有 `Listener.Register` 的调用顺序。
- 发布前需要新的 plugin-sdk 版本并刷新 `sdk.lock.json`。
