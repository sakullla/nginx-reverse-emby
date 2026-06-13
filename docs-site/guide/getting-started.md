# 从 0 到 HTTP 代理

这篇是给第一次使用的人看的最短路径：你已经有一个能直连国内、港澳台或其他优化线路的 VPS，也已经购买或加入了某个公费服、公益服 Emby/Jellyfin，但源站访问慢、绕路，或者每次观看都必须挂梯子。

Nginx-Reverse-Emby 要做的事很简单：让你的 VPS 作为中转入口。你访问自己的域名，VPS 再去访问公费服/公益服的 Emby 源站，从而把观看入口固定到你的优化线路上。

按这条线走完后，你应该能打开类似 `http://emby.example.com` 或 `https://emby.example.com` 的地址，并访问到原来的 Emby/Jellyfin 服。

## 你需要先准备

- 一台优化线路 VPS，已经安装 Docker 和 Docker Compose。它最好能稳定访问你要反代的 Emby 源站。
- 一个可以解析到这台 VPS 的域名，例如 `emby.example.com`。
- 公费服/公益服给你的 Emby/Jellyfin 原始访问地址，例如 `https://origin.emby.example.net`。
- 一个自己设置的面板登录令牌，也就是 `API_TOKEN`。

先在 VPS 上测试源站能不能通：

```bash
curl -I https://origin.emby.example.net
```

如果 VPS 自己都访问不了源站，反代规则创建成功也不能正常播放。

## 第 1 步：下载 Compose 文件

```bash
mkdir -p nginx-reverse-emby && cd nginx-reverse-emby
curl -O https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/docker-compose.yaml
mkdir -p data
```

## 第 2 步：修改登录令牌

编辑 `docker-compose.yaml`，至少改这三项：

```yaml
environment:
  API_TOKEN: your-secure-token
  MASTER_REGISTER_TOKEN: your-register-token
  NRE_TIMEZONE: Asia/Shanghai
```

`API_TOKEN` 是你登录面板时输入的访问令牌。不要使用示例里的弱密码。

## 第 3 步：启动面板

```bash
docker compose up -d
```

启动后，打开：

```text
http://<服务器 IP>:8080
```

输入刚才设置的 `API_TOKEN` 登录。

## 第 4 步：添加 HTTP 规则

进入面板后，打开 **HTTP 规则** 页面，选择 `local` 节点，然后添加规则：

| 表单项 | 示例 | 说明 |
| --- | --- | --- |
| 前端访问地址 | `emby.example.com` | 你自己访问的加速入口域名，需要解析到这台 VPS。 |
| 后端服务器 | `origin.emby.example.net` | 公费服/公益服给你的 Emby/Jellyfin 原始地址。 |
| 启用此规则 | 开启 | 保存后规则才会生效。 |

详细图文步骤见 [添加 HTTP 规则](./http-rule.md)。

## 第 5 步：验证访问

确认以下条件后，用浏览器访问你的前端域名：

- 你的加速域名 DNS 已解析到这台 VPS。
- VPS 安全组、防火墙已放行你要监听的端口，例如 `80`、`443` 或规则里使用的端口。
- VPS 能访问公费服/公益服的 Emby 源站。
- 播放时浏览器、电视盒子或 Emby 客户端填写的是你的加速域名，不再直接填源站地址。

## 下一步

- 需要更详细的部署说明，读 [Docker Compose 部署](./docker-compose.md)。
- 已经部署完成，直接读 [添加 HTTP 规则](./http-rule.md)。
