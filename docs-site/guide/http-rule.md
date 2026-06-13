# 添加 HTTP 规则代理 Emby/Jellyfin

这篇教程假设你已经完成 Docker Compose 部署，并能打开面板。

目标是给公费服/公益服 Emby 做一个自己的加速入口：

```text
你自己访问: http://emby.example.com
VPS 代理到: https://origin.emby.example.net
```

其中 `emby.example.com` 换成解析到你 VPS 的域名，`origin.emby.example.net` 换成公费服/公益服给你的原始 Emby/Jellyfin 地址。

这个配置解决的是“源站访问慢、绕路、必须挂梯子才能看”的问题。你的播放器以后访问自己的 VPS 域名，由 VPS 去连接源站。

## 1. 登录面板

打开：

```text
http://<服务器 IP>:8080
```

输入你在 `docker-compose.yaml` 里设置的 `API_TOKEN`。

![登录面板](/screenshots/panel-login.png)

## 2. 进入 HTTP 规则页面

登录后，在左侧导航中进入 **HTTP 规则**。如果页面顶部有节点选择，先选择 `local` 或你要承载代理规则的 VPS 节点。

![HTTP 规则页面](/screenshots/panel-http-rules.png)

## 3. 点击添加规则

点击右上角 **添加规则**。

在弹窗的 **基础配置** 里，只需要先填两个核心字段：

| 字段 | 示例 | 怎么填 |
| --- | --- | --- |
| 前端访问地址 | `emby.example.com` | 选择 `http://` 或 `https://`，右侧填你自己的加速入口域名。 |
| 后端服务器 | `origin.emby.example.net` | 选择源站实际协议，右侧填公费服/公益服给你的 Emby 原始域名和端口。 |

![添加 HTTP 规则](/screenshots/panel-http-rule-form.png)

## 4. 保持规则启用并保存

保持 **启用此规则** 开启，然后点击 **创建规则**。

保存后回到 HTTP 规则列表，能看到新规则就表示面板已经记录成功。`local` agent 会按心跳同步配置并开始监听。

## 5. 配好 DNS 和端口

规则保存后，还需要确认外部访问条件：

- `emby.example.com` 已解析到运行 Nginx-Reverse-Emby 的服务器公网 IP。
- 服务器安全组和防火墙已放行访问端口。
- 如果使用 `http://`，通常需要放行 `80` 或你填写的端口。
- 如果使用 `https://`，通常需要放行 `443`，并先配置证书。
- 源站地址 `origin.emby.example.net` 必须能从这台 VPS 访问。
- 如果源站本身需要登录、邀请码或 Emby 账号，这些仍然按源站规则处理，反代不会绕过权限。

## 6. 如果要用 HTTPS

新手优先使用 HTTP-01 自动签证书，不需要配置 Cloudflare Token。

把前端访问地址改成：

```text
https://emby.example.com
```

然后确认这三件事：

- `emby.example.com` 已解析到这台 VPS。
- VPS 的 `80` 和 `443` 端口都已放行。
- HTTP 规则保存后，等待本地 Agent 同步并自动申请证书。

HTTP-01 会临时占用 `80` 端口完成域名验证。验证通过后，你就可以用 `https://emby.example.com` 访问自己的 Emby/Jellyfin 入口。

如果你的 VPS 不能开放 `80` 端口，或者要申请通配符证书，再去看 [证书管理](../reference/certificates.md) 里的 DNS 验证。

## 7. 浏览器验证

在浏览器打开你的前端访问地址：

```text
http://emby.example.com
```

如果能看到 Emby/Jellyfin 页面，就说明 HTTP 规则已经生效。

## 流式恢复说明

HTTP 规则内置中断流恢复和同 backend 的安全重试，用来改善大文件播放时的偶发断流。它不会绕过源站权限，也不会把不稳定或不可达的源站变成可用源站。

中断恢复主要覆盖：

- `GET` 全量响应。
- `GET` 单区间 `Range` 响应。

源站需要持续支持 `Accept-Ranges: bytes`，并返回稳定的强 `ETag` 或 `Last-Modified`。如果源站校验器或 `Content-Range` 不一致，恢复会停止并返回错误。

如果打不开，按这个顺序排查：

1. 域名是否解析到正确服务器。
2. 服务器端口是否放行。
3. VPS 是否能访问公费服/公益服源站。
4. HTTP 规则是否选择了正确 Agent。
5. 规则是否保持启用。
6. 源站是否限制 Host、地区、IP 或需要特殊路径。

## 常见填写错误

| 现象 | 常见原因 |
| --- | --- |
| 打开域名没反应 | DNS 没生效，或端口没放行。 |
| 面板里规则保存了，但访问不到源站 | VPS 无法访问公费服/公益服源站，或源站协议、端口填错。 |
| 用 HTTPS 访问失败 | 还没有配置证书，先用 HTTP 验证规则，确认代理链路正常后再启用 HTTPS。 |
| 登录页能打开但播放失败 | 源站线路到 VPS 不稳定、源站限制反代，或播放器仍在请求源站直链。 |
| 代理到错误服务 | 前端访问地址或后端服务器填错，检查协议、域名、端口。 |
