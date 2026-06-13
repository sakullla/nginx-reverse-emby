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

## 6. 如果要自动签 HTTPS 证书

只用 `http://emby.example.com` 测试代理时，不需要 `CF_TOKEN`。

`CF_TOKEN` 只在你要让面板通过 Cloudflare DNS 自动申请或续期 HTTPS 证书时使用。它的作用是让 ACME 客户端临时创建并删除 `_acme-challenge` TXT 记录，完成 DNS-01 验证。它不是面板登录令牌，也不会影响 HTTP 规则转发流量。

使用 Cloudflare 时，在 `docker-compose.yaml` 里配置：

```yaml
environment:
  ACME_DNS_PROVIDER: cf
  CF_TOKEN: your-cloudflare-api-token
```

Cloudflare API Token 建议使用最小权限。新手可以创建一个 token，同时授予：

| 权限 | 用途 |
| --- | --- |
| `Zone / Zone / Read` | 让 ACME 客户端查到域名属于哪个 Cloudflare Zone。 |
| `Zone / DNS / Edit` | 创建和删除 `_acme-challenge` TXT 记录。 |

资源范围限制到要签证书的域名所在 Zone，例如只给 `example.com` 这个 Zone 授权。申请 `emby.example.com` 或 `*.example.com` 证书时，token 必须能读到 `example.com` 这个 Zone，并能编辑这个 Zone 的 DNS 记录。

如果你想把权限拆得更细，也可以使用两个 token：

| 环境变量 | 权限 |
| --- | --- |
| `CF_TOKEN` | `Zone / DNS / Edit` |
| `CLOUDFLARE_ZONE_API_TOKEN` | `Zone / Zone / Read` |

没有单独配置 `CLOUDFLARE_ZONE_API_TOKEN` 时，程序会用 `CF_TOKEN` 同时做 Zone 查询和 DNS 记录修改，所以单 token 必须同时具备上面两项权限。

`CF_TOKEN` 也可以写成 `CF_DNS_API_TOKEN` 或 `CLOUDFLARE_DNS_API_TOKEN`，含义相同。文档里统一使用 `CF_TOKEN`，只是为了让 Docker Compose 配置更短。

如果申请证书时报 `failed to find zone`、`403` 或提示无法 list zones，通常是缺少 `Zone / Zone / Read`，或 token 的 Zone 资源范围没有包含这个域名。

不要使用 Cloudflare Global API Key，也不要把 `CF_TOKEN` 提交到仓库。它只应该放在服务器上的 Compose 环境变量或安全的 secret 管理里。

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
