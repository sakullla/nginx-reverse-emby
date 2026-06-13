# Relay

Relay 隧道通过 Agent 到 Agent 的路径承载流量。

它通常和 HTTP 规则或 L4 规则组合使用：规则负责监听入口和定义最终后端，Relay 监听器负责把中间路径串起来。

支持的传输模式包括：

- `tls_tcp`
- `quic`
- `wireguard`

当传输路径需要经过托管的 WireGuard 接口时，Relay 可以与 WireGuard Profile 组合使用。

## 监听器字段

| 字段 | 说明 |
| --- | --- |
| 名称 | 监听器在面板中的显示名称。 |
| 绑定地址 | Agent 本机监听地址，可一行一个。 |
| 监听端口 | Agent 本机监听端口。 |
| 公网入口 | 其他节点访问这条 Relay 时使用的地址，可填 `host` 或 `host:port`。 |
| 监听证书来源 | 默认使用自动签发的 Relay CA。 |
| 信任策略 | 默认自动使用 Relay CA + Pin。 |
| Relay Transport | `tls_tcp`、`quic` 或 `wireguard`。 |

## 传输模式

| 模式 | 适用场景 |
| --- | --- |
| `tls_tcp` | 默认模式，最容易先跑通。 |
| `quic` | 需要更低握手耗时，且 UDP/QUIC 链路可用。 |
| `wireguard` | Relay 传输路径需要走托管 WireGuard Profile。 |

`wireguard` 只改变 Relay 传输路径，Relay 原有 TLS、mux、认证仍然生效。

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
