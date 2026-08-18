---
# Runtime 只读取这一处文件头机器区；不要在正文重复机器字段。
format: exploration
# AI 在此填写非空摘要；详细内容写入下方预建章节。
summary: 公开 SDK 已增加 NodeAddresses/NodeAddressSource 与 DualStackListenBinding/DualStackListener；控制面已有 agent.ddns_domain。Host 尚未把句柄注入 Agent 插件进程。HTTP backend-provider 合同未改。
---
# Exploration

## 证据与直接消费者

- 实现入口在 worktree 的 `plugin-sdk/go/node_addresses.go`：`NodeAddresses`（DDNS/IPv4/IPv6）、`NodeAddressSource`、`SelectShareHost`（DDNS>IPv4>IPv6）、`ShareableHost`（拒绝未指定、回环、localhost）。
- 双栈监听入口在 `plugin-sdk/go/dual_stack_listener.go`：`DualStackListenBinding`（必须同时 TCP+UDP，端口 1–65535）、`DualStackListener.Binding(ctx, listenerRef)`、`JoinShareHostPort`、`ValidL4BackendHost`。
- 测试：`plugin-sdk/go/node_addresses_test.go`、`plugin-sdk/go/dual_stack_listener_test.go`。`go test ./go -run SelectShareHost|DualStack|JoinShareHostPort|ValidL4` 在 `plugin-sdk` 模块下通过。
- 控制面已有 `panel/backend-go/internal/controlplane/service/agents.go` 的 `DdnsDomain` 与面板 `AgentDetailPage.vue` 的 `agent.ddns_domain`。这是 DDNS 来源证据，不是插件进程可读的 SDK 句柄实现。
- `plugin-sdk/go/typed_handles.go` 声明 Docker/HTTP/UI 效果必须经 Host 句柄；已注明节点地址与双栈监听同样由 Host 拥有。
- `plugin-sdk/go/http_backend_provider.go` 仍是本机 Agent 私有 socket 的 HTTP 供应商合同，未被本需求改成跨主机模型。
- 直接消费者：`sakullla-plugins` 的 shadowsocks-server（`share.go` 目前自有平行类型），通过 `go.mod` replace 指向本 worktree 的 `plugin-sdk`。

## 复用点

- 分享主机选择与 `ShareableHost` 规则与插件仓 `plugins/shadowsocks-server/share.go` 的 `SelectHost`/`shareableHost` 同序、同拒绝集。
- `ValidL4BackendHost` 对照 reverse-l4 的 `backend_host` 边界（无 scheme、无路径）。
- 句柄风格对照既有 `service.revocable-resource-handle`：插件不持有套接字或探测权。
- 控制面 `ddns_domain` 是 Host 实现 `NodeAddressSource` 时的 DDNS 字段来源。

## 风险

- SDK 类型已在，但 Agent 运行时仍要 Host 实现并注入 `NodeAddressSource` 与 `DualStackListener`；未注入时插件分享仍会缺对外地址。
- 插件仓 `share.go` 仍有一套同名本地类型，未改为消费 `pluginsdk` 符号，存在两套投影漂移风险。
- 未新增 manifest permission 枚举，避免改 `plugin-manifest-v1.schema.json` 与 `sdk.lock.json`；句柄走既有 revocable-resource-handle。若 Host 要用独立 permission 名，需要后续改 schema。
- `sdk.lock.json` 仍钉在旧 tag `plugin-sdk/v0.6.1` / 模块 v0.7.0；replace 仅开发期有效，发布前要打新 SDK 版本并刷新 lock。

## Unknowns

- Host Agent 从哪条路径把 `agent.ddns_domain` 与节点 IPv4/IPv6 注入插件进程，本 worktree 尚未接线。
- `DualStackListener.Binding` 与现有插件 `Listener.Register(listener_ref, *Service)` 如何在 Activate 后衔接（先 Register 再 Binding，还是 Binding 包含 Register）未在 Host 侧落地。
- 节点同时有公网 IPv4 与仅内网 IPv4 时，Host 填入 `NodeAddresses.IPv4` 的是哪一个，SDK 不区分。

## 验证焦点

- R1：有 DDNS 选域名；无 DDNS 有 IPv4 选 IPv4；否则 IPv6；`0.0.0.0`/`::`/`127.0.0.1`/`localhost` 不可选。
- R1：双栈绑定缺 TCP 或 UDP、端口越界则 Validate 失败。
- R2：`JoinShareHostPort` 产出 `203.0.113.10:8388` 与 `[2001:db8::1]:8388`；通配不可格式化。
- R3：`http_backend_provider.go` 合同保持本机供应商，不出现跨主机完成项。
