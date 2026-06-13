# L4 规则 + Relay 隧道从 0 到可用

这篇教程适合已经会用 HTTP 规则的人。HTTP 规则解决的是浏览器或 Emby 客户端访问 Web 服务的问题；L4 规则处理的是更底层的 TCP/UDP 端口转发。配合 Relay 后，入口节点可以先把 TCP/UDP 流量送进 Relay 隧道，再由中继节点访问后端。

本文用一个 TCP 示例说明完整路径：

```text
客户端访问: VPS_A:18096
L4 规则入口: local 节点
Relay 首跳: relay-fast-vps, relay.example.com:7443
最终后端: origin.emby.example.net:8096
```

实际使用时，把域名、端口和后端地址换成你自己的。

## 适合什么场景

- 你有一台优化线路 VPS，想把某个 TCP 服务固定到这台 VPS 的端口上访问。
- 后端服务不一定是 HTTP，或者你明确要按 TCP/UDP 端口转发。
- 中间链路需要经过 Relay 监听器，而不是让入口节点直接连后端。

如果你的目标只是反代 Emby Web 页面，优先读 [添加 HTTP 规则](./http-rule.md)。L4 不会理解 HTTP Host、路径、Cookie、302 跳转和证书自动匹配，它只是按端口转发流量。

## 你需要先准备

- 控制面已经通过 [Docker Compose 部署](./docker-compose.md) 启动。
- 面板里能看到 `local` 节点在线，或者你已经接入了要承载入口的远程 Agent。
- Relay 公网入口端口已经在云防火墙和系统防火墙放行，例如 `7443`。
- L4 对外监听端口已经放行，例如本文示例的 `18096`。
- 承载最终出站的节点能访问后端 `origin.emby.example.net:8096`。

## 1. 创建 Relay 监听器

进入 **Relay 监听器** 页面，选择 `local` 或你要作为中继节点的 Agent，然后点击 **新建监听器**。

核心字段按下面填写：

| 字段 | 示例 | 说明 |
| --- | --- | --- |
| 名称 | `relay-fast-vps` | 给这条 Relay 入口起一个能看懂的名字。 |
| 监听证书来源 | 自动签发（Relay CA） | 新手保持默认，系统会自动维护 Relay CA 和 Pin。 |
| 绑定地址 | `0.0.0.0` | 监听所有网卡。 |
| 监听端口 | `7443` | Relay 在这台节点上实际监听的端口。 |
| 公网入口 | `relay.example.com:7443` | 其他节点访问这条 Relay 时使用的公网地址。 |
| Relay Transport | `TLS/TCP` | 新手先用默认值，确认可用后再考虑 QUIC 或 WireGuard。 |

![创建 Relay 监听器](/screenshots/panel-relay-form.png)

保存后，列表里应该能看到启用状态、监听地址和公网入口。

![Relay 监听器列表](/screenshots/panel-relay-listener.png)

## 2. 创建 L4 规则

进入 **L4 规则** 页面，选择承载入口端口的 Agent，然后点击 **添加 L4 规则**。

在 **基础配置** 里填写：

| 字段 | 示例 | 说明 |
| --- | --- | --- |
| 协议 | `TCP` | Emby/Jellyfin 的普通 Web 端口是 TCP。 |
| 监听地址 | `0.0.0.0` | 允许外部访问这个入口端口。 |
| 监听端口 | `18096` | 客户端最终访问的 VPS 端口。 |
| 后端服务器 | `origin.emby.example.net:8096` | 最终要连接的后端 TCP 服务。 |
| 启用规则 | 开启 | 保存后规则才会下发。 |

![L4 基础配置](/screenshots/panel-l4-form-basic.png)

## 3. 给 L4 规则添加 Relay 链路

切到 **Relay 配置**，点击 **添加新层**，再选择刚才创建的 `relay-fast-vps`。界面里会显示：

```text
客户端 -> 第 1 层 relay-fast-vps -> 后端
```

![L4 Relay 配置](/screenshots/panel-l4-relay-form.png)

这里的“层”就是 `relay_layers`。一层里可以放多个 Relay 监听器作为并行候选；多层表示按顺序逐跳转发。新手先只配一层一个节点，确认可用后再扩展。

保持规则启用，点击 **创建规则**。

## 4. 验证 L4 规则

回到 L4 规则列表，能看到规则卡片包含 `Relay` 标记，说明这条规则已经带上 Relay 链路。

![L4 Relay 规则](/screenshots/panel-l4-rule.png)

在客户端测试入口端口：

```bash
nc -vz <VPS_A_IP> 18096
```

如果后端是 HTTP 服务，也可以直接用浏览器打开：

```text
http://<VPS_A_IP>:18096
```

如果你给入口端口绑定了域名或前置代理，也可以访问对应域名和端口。

## 常见问题

| 现象 | 排查方向 |
| --- | --- |
| L4 规则保存了但连不上 | 检查 L4 监听端口是否被云安全组和系统防火墙放行。 |
| 规则卡片没有 `Relay` 标记 | 回到 L4 规则的 Relay 配置，确认已经添加 Relay 层并保存。 |
| Relay 监听器可见但不可用 | 检查 Relay 的公网入口域名、端口、防火墙和证书自动签发状态。 |
| 入口能连通但后端无响应 | 检查最终出站节点是否能访问 `origin.emby.example.net:8096`。 |
| 用 HTTPS 客户端访问失败 | L4 只转发 TCP，不自动为上游 HTTP 服务签发 HTTPS 证书；确认客户端协议和后端协议一致。 |

## 和 HTTP 规则怎么选

| 需求 | 推荐 |
| --- | --- |
| 反代公费服/公益服 Emby Web 入口，解决观看必须挂代理 | HTTP 规则 |
| 按域名、路径、HTTP 头处理 Web 流量 | HTTP 规则 |
| 只想转发一个 TCP/UDP 端口 | L4 规则 |
| L4 流量需要经过中继链路 | L4 规则 + Relay |
