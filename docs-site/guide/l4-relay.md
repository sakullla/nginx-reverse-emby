# L4 规则与 Relay

这篇适合已经会用 HTTP 规则的人。HTTP 规则处理浏览器和客户端对 Web 服务的访问；L4 规则处理更底层的 TCP / UDP 端口转发。配合 Relay 后，入口节点可以先把 TCP / UDP 流量送进 Relay 隧道，再由中继节点访问后端。

本文用一个 TCP 示例说明完整路径：

```text
客户端访问: VPS_A:18096
L4 规则入口: local 节点
Relay 首跳: relay-fast-vps, relay.example.com:7443
最终后端: origin.example.net:8096
```

实际使用时，把域名、端口和后端地址换成你自己的。

## 适合什么场景

- 想把某个 TCP / UDP 服务固定到节点端口上访问。
- 后端不是 HTTP，或你明确要按端口转发流量。
- 中间链路需要经过 Relay，而不是让入口节点直连后端。

如果只是反代 Web 入口，优先读 [HTTP 反向代理](./http-rule.md)。L4 不理解 HTTP Host、路径、Cookie、302 跳转和证书自动匹配，只按端口转发流量。

## 你需要先准备

- 控制面已通过 [部署](./deploy.md) 启动。
- 面板能看到 `local` 节点在线，或已接入要承载入口的远程 Agent。
- Relay 公网入口端口已在云防火墙和系统防火墙放行（如 `7443`）。
- L4 对外监听端口已放行（如示例 `18096`）。
- 承载最终出站的节点能访问后端 `origin.example.net:8096`。

## 1. 创建 Relay 监听器

进入 **基础设施 → Relay 监听器**，选择 `local` 或中继节点，点击 **新建监听器**。

| 字段 | 示例 | 说明 |
| --- | --- | --- |
| 名称 | `relay-fast-vps` | 给这条 Relay 入口起一个可读的名字。 |
| 监听证书来源 | 自动签发（Relay CA） | 新手保持默认，系统自动维护 Relay CA 和 Pin。 |
| 绑定地址 | `0.0.0.0` | 监听所有网卡，可每行一个。 |
| 监听端口 | `7443` | Relay 在节点上实际监听的端口。 |
| 公网入口 | `relay.example.com:7443` | 其他节点访问这条 Relay 时使用的公网地址。 |
| Relay Transport | `TLS/TCP` | 新手先用默认值，可用后再考虑 QUIC 或 WireGuard。 |

![创建 Relay 监听器](/screenshots/panel-relay-form.png)

保存后列表应能看到启用状态、监听地址和公网入口。

![Relay 监听器列表](/screenshots/panel-relay-listener.png)

## 2. 创建 L4 规则

进入 **流量管理 → L4 规则**，选择承载入口端口的 Agent，点击 **添加 L4 规则**。表单分为 **基础配置**、**协议与监听**、**Relay 配置** 三个标签页。

### 基础配置

| 字段 | 示例 | 说明 |
| --- | --- | --- |
| 协议 | `TCP` | 选 `TCP` 或 `UDP`。 |
| 监听地址 | `0.0.0.0` | 允许外部访问这个入口端口。 |
| 监听端口 | `18096` | 客户端最终访问的节点端口。 |
| 后端服务器 | `origin.example.net:8096` | 最终要连接的后端 TCP 服务，可添加多个并排序。 |
| 负载均衡策略 | 自适应 | 多后端时的调度策略：自适应、轮询、随机。 |
| 启用规则 | 开启 | 保存后规则才会下发。 |

![L4 基础配置](/screenshots/panel-l4-form-basic.png)

### 协议与监听

| 字段 | 说明 |
| --- | --- |
| 监听模式 | TCP / UDP 转发、SOCKS / HTTP 代理入口，或 WireGuard。 |
| WireGuard 配置 | 监听模式为 WireGuard 时选择 WireGuard Profile。 |
| WireGuard 入站模式 | 透明或内网入口。 |
| 出口 Profile | 出站经过的 Egress Profile。 |
| PROXY Protocol | TCP 模式下可选择接收 / 发送 PROXY Protocol 以透传客户端 IP。 |

### Relay 配置

点击 **添加新层**，选择刚才创建的 `relay-fast-vps`，界面会显示：

```text
客户端 -> 第 1 层 relay-fast-vps -> 后端
```

![L4 Relay 配置](/screenshots/panel-l4-relay-form.png)

这里的「层」就是 `relay_layers`：一层里可放多个 Relay 监听器作为并行候选；多层表示按顺序逐跳转发。新手先只配一层一个节点，确认可用后再扩展。TCP Relay 还可启用 **Relay 隐私增强**（仅首跳）。

保持规则启用，点击 **创建规则**。

## 3. 验证 L4 规则

回到 L4 规则列表，规则卡片带有 `Relay` 标记即说明已带上 Relay 链路。

![L4 Relay 规则](/screenshots/panel-l4-rule.png)

在客户端测试入口端口：

```bash
nc -vz <VPS_A_IP> 18096
```

如果后端是 HTTP 服务，也可直接用浏览器打开：

```text
http://<VPS_A_IP>:18096
```

如果给入口端口绑定了域名或前置代理，访问对应域名和端口即可。

## 和 HTTP 规则怎么选

| 需求 | 推荐 |
| --- | --- |
| 反代 Web 入口，按域名 / 路径 / HTTP 头处理 | HTTP 规则 |
| 只转发一个 TCP / UDP 端口 | L4 规则 |
| 流量需要经过中继链路 | L4 规则 + Relay（或 HTTP 规则 + Relay） |
| 客户端用 SOCKS / HTTP 代理接入 | L4 规则（代理入口模式） |

## 常见问题

| 现象 | 排查方向 |
| --- | --- |
| L4 规则保存了但连不上 | 检查监听端口是否被云安全组和系统防火墙放行。 |
| 规则卡片没有 `Relay` 标记 | 回到 Relay 配置，确认已添加 Relay 层并保存。 |
| Relay 监听器可见但不可用 | 检查 Relay 公网入口域名、端口、防火墙和证书自动签发状态。 |
| 入口能连通但后端无响应 | 检查最终出站节点是否能访问 `origin.example.net:8096`。 |
| HTTPS 客户端访问失败 | L4 只转发 TCP，不会为上游 HTTP 服务自动签发 HTTPS 证书；确认客户端协议与后端协议一致。 |
