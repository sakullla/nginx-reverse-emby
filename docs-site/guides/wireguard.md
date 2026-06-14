# WireGuard 隧道

WireGuard 是一种快速、现代的 VPN 协议。在 nginx-reverse-emby 中，WireGuard 有三种用法：作为 Relay 的传输方式、作为 L4/HTTP 规则的入口、作为 L4/HTTP 规则的出口。

如果不需要 WireGuard，可以在控制面关闭：

```ini
NRE_WIREGUARD_ENABLED=false
```

## WireGuard Profile

Profile 由控制面统一管理，包含私钥、监听端口、接口地址、Peer 配置等信息。Agent 会在节点上自动创建对应的 WireGuard 接口。私钥在面板中脱敏显示，编辑时保持脱敏值不变则保留已有密钥。

### 创建 Profile

进入 **基础设施 → WireGuard 配置**，点击 **新建**：

| 字段 | 示例 | 说明 |
| --- | --- | --- |
| 名称 | `edge-wg` | Profile 名称 |
| 监听端口 | `51820` | WireGuard UDP 监听端口 |
| Private Key | 留空 | 留空自动生成 |
| MTU | `1420` | 576–9000 |
| Addresses | `192.168.0.109` | Agent 主机上 WireGuard socket 的物理监听地址。留空监听 `0.0.0.0` |
| WG 分配地址 | `10.8.0.1/24` | WireGuard 虚拟接口地址/地址池，支持 IPv4/IPv6，可填多行 |
| DNS | `1.1.1.1` | 接口 DNS |
| 公网入口 | `vpn.example.com:51820` | 生成客户端配置用的公网地址 |

![WireGuard Profile 创建表单](/screenshots/panel-wireguard-form.png)

**Addresses** 是 Agent 主机上 UDP socket 的物理监听地址，**WG 分配地址** 是 WireGuard 虚拟接口地址，两者不同。

WG 分配地址留空时，控制面从地址池自动分配。地址池配置：

```ini
NRE_WIREGUARD_AUTO_ADDRESS_POOLS=10.8.x.1/24,fd10:8:x::1/64
```

模板中的 `x` 会被替换为递增序号。

### 客户端

Profile 下可以创建客户端。控制面为每个客户端自动生成密钥对、分配地址，并导出标准客户端配置或 `wireguard://` URI，方便导入到手机或桌面客户端。

## 三种用法

### 1. Relay 传输

创建 Relay 监听器时，把传输方式设为 `wireguard`。Relay 的 TLS、mux、认证等功能仍然在 WireGuard 链路上生效。

### 2. L4 监听入口

创建 L4 规则时，把监听模式设为 WireGuard。客户端需要先连接 Agent 的 WireGuard 端点，进入隧道后再访问规则配置的虚拟服务地址。

### 3. HTTP/L4 出口

把 HTTP 或 L4 规则的出口设为 WireGuard Profile，出站连接通过 WireGuard 发起。
