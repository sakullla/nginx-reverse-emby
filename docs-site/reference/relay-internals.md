# Relay 协议内幕

Relay 隧道在 Agent 之间传输流量。配合 HTTP 或 L4 规则使用：规则定义入口和后端，Relay 监听器定义 Agent 之间的中间路径。

## 传输方式

| 方式 | 说明 |
|------|------|
| `tls_tcp` | TLS over TCP。默认模式，配置最简单，大多数网络都能用 |
| `quic` | QUIC over UDP。握手延迟更低。防火墙需允许 UDP/QUIC |
| `wireguard` | 通过 WireGuard 接口传输。Relay 的 TLS、多路复用和认证仍在 WireGuard 上生效 |

## 监听器字段

| 字段 | 说明 |
|------|------|
| 名称 | 面板显示名称 |
| 证书来源 | 默认自动签发（Relay CA）。也可绑定已有证书 |
| 绑定地址 | Agent 本地监听地址，每行一个 |
| 监听端口 | Agent 本地监听端口 |
| 公网入口 | 其他节点连接此中继的地址，格式 `host` 或 `host:port` |
| 传输方式 | TLS/TCP、QUIC 或 WireGuard |
| 信任策略 | 默认自动 Relay CA + Pin。可在高级设置中自定义 |

## TLS 与信任策略

默认使用自动签发的 Relay CA 证书加证书固定（Pin），安全且无需手动配置。

手动控制时的 TLS 模式：

| 模式 | 含义 |
|------|------|
| Pin + CA | 同时验证固定值和 CA 链（默认） |
| 仅 Pin | 只验证固定值 |
| 仅 CA | 只验证 CA 链 |
| Pin 或 CA | 任一通过即接受 |

高级设置中可提供「固定集」（每行一个，格式 `type:value`）和「受信任的 CA 证书」。TLS/TCP 模式还可选 TLS 混淆策略（仅首跳有效）。

## Relay 层

HTTP 和 L4 规则用 `relay_layers` 描述隧道路径。每层可包含多个 Relay 监听器作为并行候选，层按顺序处理：

```text
客户端 → 第 1 层 → 第 2 层 → 后端
```

旧的 `relay_chain` 字段仅用于向后兼容，新配置用 `relay_layers`。

## 超时变量

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `NRE_RELAY_DIAL_TIMEOUT` | `5s` | 连接上游中继超时 |
| `NRE_RELAY_HANDSHAKE_TIMEOUT` | `5s` | 中继握手超时 |
| `NRE_RELAY_FRAME_TIMEOUT` | `5s` | 单帧读写超时 |
| `NRE_RELAY_IDLE_TIMEOUT` | `2m` | 空闲连接保持时间 |

## 如何使用

操作指南见 [Relay 隧道](../guides/relay.md)。
