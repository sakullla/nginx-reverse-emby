---
description: 在 Linux VPS 上部署 NRE，创建第一条 HTTP/HTTPS 反向代理规则，并检查后端与入口连通性。
---

# 快速开始：跑通第一条反代

目标是让浏览器通过你的域名打开后端服务。单机使用只需要内置的 `local` 节点；先完成这一条链路，再配置多节点、Relay 或插件。

如果主要用来播放媒体，可以配合 [Emby / Jellyfin 反代指南](../guides/emby-jellyfin.md) 验证登录、播放和拖动进度。

## 准备三个东西

| 准备项 | 示例与要求 |
| --- | --- |
| Linux VPS | 可运行 Docker，能够访问后端；安装脚本可按需安装 Docker / Compose |
| 业务入口域名 | `app.example.com`，DNS 指向 VPS；放行 TCP 80 / 443，确认没有其它服务占用 |
| 后端地址 | `https://origin.example.net` 或 VPS **确实能访问到的** `http://192.168.1.100:8096` |

家里的 `192.168.x.x` 地址通常不能从公网 VPS 直接访问。需要先打通网络，或按 [Relay 指南](../guides/relay.md) 接入内网节点。

在 VPS 上先检查后端：

```bash
curl -I --connect-timeout 5 --max-time 10 https://origin.example.net
```

收到 HTTP 响应后再继续。`401/403` 表示服务限制访问；`405` 可能只是服务不支持 HEAD，需用浏览器验证。解析失败、连接失败或超时则先排查后端网络。

## 第一步：部署并登录

在 VPS 上执行：

```bash
curl -fsSL https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/scripts/deploy-compose.sh | sh
```

脚本会创建目录、生成随机 token 并启动服务。提示填写的域名是 **面板域名**（例如 `panel.example.com`），业务入口域名在下一步添加，两者请使用不同域名。

- 有面板域名：先将 DNS 指向 VPS，填入域名；Cloudflare Token 可选，跳过时使用 HTTP-01 验证。
- 暂时没有面板域名：可回车跳过，按脚本输出的临时访问方式登录，之后再为面板配置 HTTPS。

保存脚本输出的 **访问地址** 和 **Panel token**。打开该地址，用 Panel token 登录；全新部署不需要用户名和密码。临时 HTTP 不加密令牌，公网长期使用前请启用 HTTPS。

![登录面板](/screenshots/panel-login.png)

![仪表盘首页](/screenshots/panel-dashboard.png)

需要自行维护 Compose 或已有外部反代？使用 [部署指南](./deploy.md)。手动部署默认只监听 `127.0.0.1:8080`，需要通过 SSH 隧道首次访问。

## 第二步：创建第一条规则

进入 **流量管理 → HTTP 规则**，节点选择 `local`，点击 **添加规则**：

| 字段 | 填什么 |
| --- | --- |
| 入口域名 | 选择 `https://`，填写 `app.example.com` |
| 后端地址 | `https://origin.example.net`，填写真实协议与端口 |
| 启用规则 | 开启 |
| 其它设置 | 先保留默认值 |

![添加 HTTP 规则](/screenshots/panel-http-rule-form.png)

点击 **创建规则**。`local` 节点同步配置后，自动申请 HTTPS 证书。默认 HTTP-01 要求域名解析正确且外部能访问 TCP 80；访问 HTTPS 还需要 TCP 443。证书未就绪时到 **基础设施 → 证书管理** 查看状态。

如果暂时无法完成证书验证，可以先用 `http://` 验证页面连通性，确认后改为 HTTPS；不要通过未加密的公网入口提交账号密码。80 端口不可用、需要通配符或 DNS-01 时，见 [证书与 HTTPS](../guides/certificates.md)。

## 第三步：从客户端验证

在你实际使用的电脑或手机上打开 `https://app.example.com`：

- 页面显示的是预期的后端服务。
- 浏览器证书有效，登录和主要功能正常。
- 媒体服务再验证播放与拖动进度，见 [媒体反代验收](../guides/emby-jellyfin.md#验证是否真正可用)。

**打不开时**，在运行规则的 VPS 上执行只读检查：

```bash
curl -fsSL https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/scripts/check-proxy.sh -o check-proxy.sh
sh check-proxy.sh \
  --upstream https://origin.example.net \
  --frontend https://app.example.com
```

请把示例换成自己的服务首页地址，**不要带 token、用户名密码或带签名的播放链接**。工具只发送 HEAD，不修改配置，也不下载视频。

| 检查结果 | 下一步 |
| --- | --- |
| 后端 DNS / 连接失败 | 先解决 VPS 到后端的网络或地址问题 |
| 后端通过，入口连接失败 | 检查入口 DNS、80/443、防火墙、规则启用状态及节点同步 |
| 入口证书校验失败 | 查看证书签发状态、域名和证书链 |
| 401 / 403 / 405 | 根据工具提示检查访问限制或用浏览器验证 |
| 两边通过，客户端仍异常 | 从客户端检查 DNS、网络与播放；服务器检查不代表公网可达 |

使用出口代理、Relay 或插件后端时，脚本的直连结果不能代表规则的实际转发路径。更多说明见 [排障指南](../operations/troubleshooting.md#只读连通性检查)。

## 跑通以后

- [为面板自身启用 HTTPS](../guides/http-rules.md#给面板自身代理-https)，与业务入口分开管理。
- [备份数据](../operations/backup-restore.md)，保存配置与密钥。
- 按需增加 [L4 端口转发](../guides/l4-rules.md)、[远程节点](../guides/agents.md) 或 [Relay](../guides/relay.md)。
