# WireGuard 隧道

WireGuard Profile 由控制面统一管理，可用于 Relay 传输、L4 规则的监听 / 出口，以及标准的客户端 VPN。Agent 会在节点上把它实例化为 WireGuard 接口。

Profile 包含私钥、监听端口、接口地址、Peer、DNS、MTU、启用状态和标签。敏感字段在 API 和面板中脱敏显示；编辑时保持脱敏值不变即可保留已存储的密钥。

不需要 WireGuard 时，可以在控制面关闭模块：

```ini
NRE_WIREGUARD_ENABLED=false
```

## 创建 Profile

进入 **基础设施 → WireGuard 配置**，点击新建：

| 字段 | 示例 | 说明 |
| --- | --- | --- |
| 名称 | `edge-wg` | Profile 显示名称。 |
| 监听端口 | `51820` | WireGuard UDP 监听端口。 |
| Private Key | 留空 | 留空时自动生成。 |
| MTU | `1420` | 取值范围 576–9000。 |
| Addresses | `192.168.0.109` | 本机 WireGuard UDP socket 的实际监听地址，留空时监听 `0.0.0.0`。 |
| WG 分配地址 | `10.8.0.1/24` | WireGuard 接口地址 / 地址池，可填多行，支持 IPv4 / IPv6。 |
| DNS | `1.1.1.1` | 接口使用的 DNS。 |
| Public Endpoint | `vpn.example.com:51820` | 用于生成客户端配置的 Endpoint。 |

`Addresses` 表示 Agent 主机上 WireGuard UDP socket 的实际监听地址（例如 `192.168.0.109`）；`WG 分配地址` 表示 WireGuard 接口地址或地址池，例如：

```text
10.8.0.1/24
fd10:8::1/64
```

创建时可留空让系统自动分配，之后在面板中调整。

## 自动地址池

`WG 分配地址` 留空时，控制面从地址池自动分配。地址池由 `NRE_WIREGUARD_AUTO_ADDRESS_POOLS` 配置，逗号分隔，同时支持 IPv4 / IPv6：

```ini
NRE_WIREGUARD_AUTO_ADDRESS_POOLS=10.8.x.1/24,fd10:8:x::1/64
```

模板里的 `x` 会被替换为每个 Profile 的递增序号。

## 客户端

Profile 下可以创建 WireGuard 客户端，控制面为其生成私钥 / 公钥和分配地址，并导出标准客户端配置或 `wireguard://` URI，方便导入到手机、桌面客户端或其他节点。

## 三种使用方式

### 1. 用于 Relay 传输

Relay 监听器把 **Relay Transport** 设为 `wireguard` 时，会使用所选 WireGuard Profile 作为 Relay 传输路径。Relay 原有的 TLS、mux、认证仍然在 WireGuard 链路上生效。

### 2. 用于 L4 监听

L4 规则把 **监听模式** 设为 WireGuard 时，客户端先连接目标 Agent 的 WireGuard UDP endpoint，进入隧道后再访问规则配置的虚拟服务 IP / 端口。入站模式可选 **透明** 或 **内网入口**。

### 3. 用于 L4 出口

L4 规则把 **出口** 设为 WireGuard Profile（`proxy_egress_mode=wireguard`）时，TCP 代理入口的出站连接会通过所选 WireGuard Profile 发起。HTTP 规则的 **出口 Profile** 同样可以选择基于 WireGuard 的 Egress Profile。

## 配合 Cloudflare WARP

如需配合 Cloudflare WARP，可使用可导出的标准 WireGuard profile，或在 Agent 主机外部运行 WARP 客户端并自行配置路由。内置 WireGuard 功能不负责 WARP 注册和自动轮换。
