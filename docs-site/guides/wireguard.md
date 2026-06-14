# WireGuard 隧道

本文介绍如何在 nginx-reverse-emby 中创建和管理 WireGuard 隧道。WireGuard 是一种快速、现代的 VPN 协议，可以用来加密节点之间的通信，或作为客户端 VPN 使用。

## 什么是 WireGuard Profile？

WireGuard Profile 由控制面统一管理，包含私钥、监听端口、接口地址、Peer 配置等信息。Agent 会在节点上自动把它实例化为 WireGuard 接口。

Profile 中的敏感字段（如私钥）在 API 和面板中脱敏显示；编辑时如果保持脱敏值不变，系统会保留已存储的密钥。

如果不需要 WireGuard 功能，可以在控制面关闭该模块：

```ini
NRE_WIREGUARD_ENABLED=false
```

---

## 创建 Profile

进入 **基础设施 → WireGuard 配置**，点击 **新建**：

| 字段 | 示例 | 说明 |
| --- | --- | --- |
| 名称 | `edge-wg` | Profile 的显示名称。 |
| 监听端口 | `51820` | WireGuard UDP 监听端口。 |
| Private Key | 留空 | 留空时系统自动生成。 |
| MTU | `1420` | 取值范围 576–9000。 |
| Addresses | `192.168.0.109` | Agent 主机上 WireGuard UDP socket 的实际监听地址，留空时监听 `0.0.0.0`。 |
| WG 分配地址 | `10.8.0.1/24` | WireGuard 接口地址 / 地址池，可填多行，支持 IPv4 / IPv6。 |
| DNS | `1.1.1.1` | 接口使用的 DNS 服务器。 |
| Public Endpoint | `vpn.example.com:51820` | 用于生成客户端配置的公网地址。 |

![WireGuard Profile 创建表单](/screenshots/panel-wireguard-form.png)

### 两个地址的区别

- **Addresses**：Agent 主机上 WireGuard UDP socket 的实际监听地址（例如 `192.168.0.109`）
- **WG 分配地址**：WireGuard 接口地址或地址池，例如：

```text
10.8.0.1/24
fd10:8::1/64
```

创建时 **WG 分配地址** 可以留空让系统自动分配，创建后在面板中调整即可。

---

## 自动地址池

**WG 分配地址** 留空时，控制面会从地址池自动分配。地址池由 `NRE_WIREGUARD_AUTO_ADDRESS_POOLS` 配置，逗号分隔，同时支持 IPv4 / IPv6：

```ini
NRE_WIREGUARD_AUTO_ADDRESS_POOLS=10.8.x.1/24,fd10:8:x::1/64
```

模板里的 `x` 会被替换为每个 Profile 的递增序号。

---

## 客户端管理

Profile 下可以创建 WireGuard 客户端。控制面会为每个客户端自动生成私钥 / 公钥、分配地址，并导出标准客户端配置或 `wireguard://` URI，方便导入到手机、桌面客户端或其他节点。

---

## 三种使用方式

### 1. 用于 Relay 传输

创建 Relay 监听器时，把 **Relay Transport** 设为 `wireguard`，系统会使用所选 WireGuard Profile 作为 Relay 的传输路径。Relay 原有的 TLS、mux、认证等功能仍然在 WireGuard 链路上生效。

### 2. 用于 L4 监听

创建 L4 规则时，把 **监听模式** 设为 WireGuard，客户端需要先连接目标 Agent 的 WireGuard UDP endpoint，进入隧道后再访问规则配置的虚拟服务 IP / 端口。入站模式可选 **透明** 或 **内网入口**。

### 3. 用于 L4 / HTTP 出口

- L4 规则把 **出口** 设为 WireGuard Profile（`proxy_egress_mode=wireguard`）时，TCP 代理入口的出站连接会通过所选 WireGuard Profile 发起。
- HTTP 规则的 **出口 Profile** 同样可以选择基于 WireGuard 的 Egress Profile。

---

## 配合 Cloudflare WARP

如需配合 Cloudflare WARP，可以使用可导出的标准 WireGuard profile，或在 Agent 主机外部运行 WARP 客户端并自行配置路由。内置的 WireGuard 功能不负责 WARP 注册和密钥自动轮换。
