# Relay

Relay 隧道通过 Agent 到 Agent 的路径承载流量。

支持的传输模式包括：

- `tls_tcp`
- `quic`
- `wireguard`

当传输路径需要经过托管的 WireGuard 接口时，Relay 可以与 WireGuard Profile 组合使用。
