# HTTP 反向代理

HTTP 规则按域名把请求转发到后端服务，支持路径匹配、请求头注入、重定向改写和自动 HTTPS。适合 Emby、Jellyfin、网站等所有 Web 服务。

## 前置条件

- 已 [部署控制面](../getting-started/deploy.md) 并能正常打开面板
- 入口域名已解析到运行规则的 Agent 节点
- 了解 [核心概念](../getting-started/core-concepts.md) 中的域名、端口和证书

## 创建 HTTP 规则

进入 **流量管理 → HTTP 规则**，选好 Agent 节点（单机选 `local`），点击 **添加规则**。

![HTTP 规则页面](/screenshots/panel-http-rules.png)

### 基础配置

| 字段 | 示例 | 说明 |
| --- | --- | --- |
| 入口域名 | `app.example.com` | 选 `http://` 或 `https://`，填用户访问的域名 |
| 后端地址 | `http://192.168.1.100:8096` | 后端实际地址，带协议和端口 |
| 标签 | `emby` | 可选，方便在面板中筛选 |
| 启用规则 | 开 | 只有开启才会生效 |

![添加 HTTP 规则](/screenshots/panel-http-rule-form.png)

Agent 会在下次心跳同步时开始监听入口域名。如果你选了 HTTPS 入口，Agent 会自动申请证书（前提是 80 和 443 端口已放行）。

### 高级选项

| 选项 | 说明 |
| --- | --- |
| 代理 302/307 重定向 | 默认开启，把后端返回的跳转地址改写为入口域名。CDN 回源或 OAuth 回调场景需要关闭 |
| 出口 Profile | 出站流量经过的代理，默认 `Direct` 直连。可选 SOCKS/HTTP 代理或 WireGuard |
| 请求头 | 自定义发送给后端的请求头 |
| Relay 配置 | 添加 Relay 隧道层，流量先经过中继节点再到后端。详见 [Relay 隧道](./relay.md) |

## 验证规则

浏览器打开入口域名。能正常看到后端页面就说明规则生效了。

打不开？按顺序查：
1. DNS 是否指向 Agent 节点
2. 节点端口是否放行（HTTP 80，HTTPS 443）
3. 节点能否访问后端（`curl -I <后端地址>`）
4. 规则是否选了正确的 Agent 且已启用

更多排查见 [排障指南](../operations/troubleshooting.md)。

## 启用 HTTPS

1. 把入口域名改成 `https://app.example.com`
2. 确认 80 和 443 端口已在防火墙放行
3. 保存规则，Agent 会在同步时自动申请证书

需要通配符证书或 80 端口不可用？见 [证书与 HTTPS](./certificates.md)。

## 给面板自身代理 HTTPS

默认控制面只监听 `127.0.0.1:8080`。首次通过 SSH 隧道登录后，可以创建一条 HTTP 规则让面板给自己提供 HTTPS：

| 字段 | 示例 |
| --- | --- |
| Agent | `local` |
| 入口域名 | `https://panel.example.com` |
| 后端地址 | `http://127.0.0.1:8080` |
| 启用规则 | 开 |

确认 `panel.example.com` 已解析到 VPS，并放行 80/443。规则生效后，访问 `https://panel.example.com` 就会由 local Agent 代理回控制面。

自代理 HTTPS 可用后，在 `docker-compose.yaml` 设置：

```yaml
environment:
  NRE_PUBLIC_URL: https://panel.example.com
```

## 流式恢复

HTTP 代理内置中断恢复机制：下载或播放中途断流时自动续传。后端需要支持 `Accept-Ranges: bytes` 并返回稳定的 `ETag` 或 `Last-Modified`。

相关环境变量见 [环境变量速查](../reference/environment-variables.md)。
