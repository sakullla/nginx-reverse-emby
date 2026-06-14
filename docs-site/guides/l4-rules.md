# L4 端口转发

L4 规则直接转发 TCP 或 UDP 端口流量，不关心协议内容。适合游戏服务器、数据库、非 Web 服务等不需要按域名路由的场景。

## 和 HTTP 规则的区别

| | HTTP 规则 | L4 规则 |
|---|---|---|
| 处理方式 | 按域名、路径、HTTP 头 | 按 TCP/UDP 端口 |
| 适合 | 浏览器访问的 Web 服务 | 非 Web 服务的端口转发 |
| 自动 HTTPS | 支持 | 不支持，L4 只转发 TCP |

如果只是反代 Emby 或网站，用 [HTTP 规则](./http-rules.md) 更合适。

## 前置条件

- 控制面已 [部署](../getting-started/deploy.md) 并正常运行
- 面板中能看到 `local` 节点在线，或已接入远程 Agent
- L4 监听端口已在云防火墙和系统防火墙中放行

## 创建 L4 规则

进入 **流量管理 → L4 规则**，选好 Agent 节点，点击 **添加 L4 规则**。

### 基础配置

| 字段 | 示例 | 说明 |
| --- | --- | --- |
| 协议 | `TCP` | 选 `TCP` 或 `UDP` |
| 监听地址 | `0.0.0.0` | 对外监听的地址 |
| 监听端口 | `18096` | 客户端连接的端口 |
| 后端地址 | `origin.example.net:8096` | 最终要连接的后端服务，可添加多个并排序 |
| 负载均衡 | 自适应 | 多后端时的调度策略：自适应、轮询、随机 |
| 启用规则 | 开 | 保存后下发到 Agent |

![L4 基础配置](/screenshots/panel-l4-form-basic.png)

### 协议与监听

| 字段 | 说明 |
| --- | --- |
| 监听模式 | TCP/UDP 转发、SOCKS/HTTP 代理入口，或 WireGuard |
| 出口 Profile | 出站经过的 Egress Profile |
| PROXY Protocol | TCP 模式下可选接收/发送 PROXY Protocol，透传客户端真实 IP |

### Relay 配置

如果需要流量经过中继节点再到后端，点击 **添加 Relay 层**。新手先配一层一个节点，跑通后再扩展。详见 [Relay 隧道](./relay.md)。

![L4 Relay 配置](/screenshots/panel-l4-relay-form.png)

## 验证规则

```bash
# TCP 端口测试
nc -vz <VPS_IP> 18096

# 如果后端是 HTTP 服务，直接浏览器打开
http://<VPS_IP>:18096
```

规则列表里能看到规则卡片即为保存成功。如果卡片带 `Relay` 标记，说明已关联 Relay 隧道。

![L4 Relay 规则](/screenshots/panel-l4-rule.png)

打不开？见 [排障指南](../operations/troubleshooting.md)。
