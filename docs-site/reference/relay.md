# Relay 参考

Relay 隧道通过 Agent 到 Agent 的路径承载流量。它通常和 HTTP 规则或 L4 规则组合使用：规则负责监听入口和定义最终后端，Relay 监听器负责把中间路径串起来。

支持的传输模式：

- `tls_tcp`
- `quic`
- `wireguard`

当传输路径需要经过托管的 WireGuard 接口时，Relay 可与 WireGuard Profile 组合使用。

## 监听器字段

| 字段 | 说明 |
| --- | --- |
| 名称 | 监听器在面板中的显示名称。 |
| 监听证书来源 | 默认自动签发（Relay CA），也可绑定已有证书。 |
| 绑定地址 | Agent 本机监听地址，可每行一个。 |
| 监听端口 | Agent 本机监听端口。 |
| 公网入口 | 其他节点访问这条 Relay 时使用的地址，可填 `host` 或 `host:port`。 |
| Relay Transport | `TLS/TCP`、`QUIC` 或 `WireGuard`。 |
| 信任策略 | 默认自动使用 Relay CA + Pin，也可高级自定义。 |
| 标签 | 节点标签，便于筛选。 |

## 传输模式

| 模式 | 适用场景 |
| --- | --- |
| `tls_tcp` | 默认模式，最容易先跑通。 |
| `quic` | 需要更低握手耗时，且 UDP / QUIC 链路可用；可配置在 QUIC 不可用时回退到 TLS/TCP。 |
| `wireguard` | Relay 传输路径需要走托管 WireGuard Profile。 |

`wireguard` 只改变 Relay 传输路径，Relay 原有的 TLS、mux、认证仍然生效。

## TLS 与信任策略

默认使用自动签发的 Relay CA + Pin。需要完全自定义时，在高级设置里选择 TLS 模式：

| TLS 模式 | 含义 |
| --- | --- |
| Pin + CA | 同时校验证书 Pin 和 CA 信任链（默认）。 |
| 仅证书 Pin | 只校验 Pin。 |
| 仅 CA 信任链 | 只校验 CA。 |
| 证书 Pin 或 CA | 满足其一即可。 |

并按需填入 Pin Set（每行一个，格式 `type:value`）和可信 CA 证书。

对 TLS/TCP 模式，可选择 **TLS 隐匿策略**（如 `early_window_v2`），仅在首跳生效。

## Relay 层

HTTP 规则和 L4 规则使用 `relay_layers` 表示链路。每一层可以包含多个 Relay 监听器，代表同一层的并行候选；多层会按顺序转发：

```text
客户端 -> 第 1 层 -> 第 2 层 -> 后端
```

旧的 `relay_chain` 只作为兼容字段保留，新配置应使用 `relay_layers`。

## 超时变量

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `NRE_RELAY_DIAL_TIMEOUT` | `5s` | Relay 上游拨号超时。 |
| `NRE_RELAY_HANDSHAKE_TIMEOUT` | `5s` | Relay 握手超时。 |
| `NRE_RELAY_FRAME_TIMEOUT` | `5s` | Relay 单帧读写超时。 |
| `NRE_RELAY_IDLE_TIMEOUT` | `2m` | Relay 空闲连接超时。 |
