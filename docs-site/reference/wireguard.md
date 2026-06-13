# WireGuard

WireGuard Profile 由控制面板管理，可用于 Relay 或 L4 规则。

Profile 包含密钥、监听地址、接口地址、Peer、DNS、MTU、启用状态和标签。

敏感字段会在 API 和面板中脱敏显示。编辑时保持脱敏值不变，即可保留已存储的密钥。

## 地址字段

创建通用 WireGuard Profile 时，`Addresses` 表示 Agent 主机上 WireGuard UDP socket 的实际监听地址，例如 `192.168.0.109`；留空时默认监听 `0.0.0.0`。

`WG 分配地址` 表示 WireGuard 接口地址或地址池，例如：

```text
10.8.0.1/24
fd10:8::1/64
```

创建时可留空自动分配，之后可在面板中调整。

## 自动地址池

自动分配地址池可通过 `NRE_WIREGUARD_AUTO_ADDRESS_POOLS` 配置，逗号分隔，同时支持 IPv4/IPv6：

```ini
NRE_WIREGUARD_AUTO_ADDRESS_POOLS=10.8.x.1/24,fd10:8:x::1/64
```

## 用于 Relay

Relay 监听器设置 `transport_mode=wireguard` 时，会使用所选 WireGuard Profile 作为 Relay 传输路径。Relay TLS、mux、认证仍然在 WireGuard 链路上生效。

## 用于 L4

L4 规则设置 `listen_mode=wireguard` 时，客户端先连接目标 Agent 的 WireGuard UDP endpoint，进入隧道后再访问规则配置的虚拟服务 IP/端口。

L4 规则设置 `proxy_egress_mode=wireguard` 或选择 WireGuard 出口 Profile 时，TCP 代理入口的出站连接会通过所选 WireGuard Profile 发起。

如果需要配合 Cloudflare WARP，可使用可导出的标准 WireGuard profile，或在 Agent 主机外部运行 WARP 客户端并自行配置路由。内置 WireGuard 功能不负责 WARP 注册和自动轮换。
