# Relay 协议内幕

Relay 隧道在 Agent 之间传输流量。配合 HTTP 或 L4 规则使用：规则定义入口和后端，Relay 监听器定义 Agent 之间的中间路径。

## 传输方式

| 方式 | 说明 |
|------|------|
| `tls_tcp` | TLS over TCP。默认模式，配置最简单，大多数网络都能用 |
| `quic` | QUIC over UDP。握手延迟更低。防火墙需允许 UDP/QUIC |

## 监听器字段

| 字段 | 说明 |
|------|------|
| 名称 | 面板显示名称 |
| 证书来源 | 激活后由内部 PKI 为 listener identity 自动签发；旧 Relay CA/手动证书只在维护升级前保留 |
| 绑定地址 | Agent 本地监听地址，每行一个 |
| 监听端口 | Agent 本地监听端口 |
| 公网入口 | 其他节点连接此中继的地址，格式 `host` 或 `host:port` |
| 传输方式 | TLS/TCP 或 QUIC |
| 信任策略 | 激活后的内部 Relay 固定为当前 PKI domain 的双向验证（`pki_mtls`） |

## TLS 与信任策略

内部 PKI 激活后，TLS/TCP 和 QUIC 都要求双向证书认证。服务端和客户端会校验当前 PKI domain/epoch、CA generation、Agent/listener identity、EKU/purpose、有效期与撤销状态；任一不匹配都拒绝连接。registration、heartbeat、revision 和 task 不走这条 mTLS 数据面，仍使用现有 panel/control listener 与 token。

旧版本和维护升级前可能仍显示以下兼容模式：

| 模式 | 含义 |
|------|------|
| Pin + CA | 同时验证固定值和 CA 链 |
| 仅 Pin | 只验证固定值 |
| 仅 CA | 只验证 CA 链 |
| Pin 或 CA | 任一通过即接受 |

这些模式不是内部 PKI 的恢复或降级方案。激活 `tunnel_mtls_only` 后，旧 Pin Set、单向 TLS、`pin_or_ca` 与自签名放行必须删除；PKI degraded 时应修复 canonical 状态或恢复受保护 PKI 备份，不能重新启用旧信任。维护升级前的高级设置仍可提供「固定集」和「受信任的 CA 证书」；TLS/TCP 模式还可选 TLS 混淆策略（仅首跳有效）。

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
