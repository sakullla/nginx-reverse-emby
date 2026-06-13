# 从 0 到 HTTP 代理

这篇是给第一次使用的人看的最短路径：先用 Docker Compose 启动面板，再在面板里添加一条 HTTP 规则，把内网里的 Emby、Jellyfin 或其他 Web 服务代理到公网域名。

按这条线走完后，你应该能打开类似 `http://emby.example.com` 或 `https://emby.example.com` 的地址，并访问到后端服务。

## 你需要先准备

- 一台 VPS 或家用服务器，已经安装 Docker 和 Docker Compose。
- 一个可以解析到这台服务器的域名，例如 `emby.example.com`。
- 后端服务地址，例如 Emby 的 `http://192.168.1.100:8096`。
- 一个自己设置的面板登录令牌，也就是 `API_TOKEN`。

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

进入面板后，打开 **HTTP 规则** 页面，选择本机 Agent，然后添加规则：

| 表单项 | 示例 | 说明 |
| --- | --- | --- |
| 前端访问地址 | `emby.example.com` | 用户最终访问的域名，需要解析到这台服务器。 |
| 后端服务器 | `192.168.1.100:8096` | Emby、Jellyfin 或其他 Web 服务的真实地址。 |
| 启用此规则 | 开启 | 保存后规则才会生效。 |

详细图文步骤见 [添加 HTTP 规则](./http-rule.md)。

## 第 5 步：验证访问

确认以下条件后，用浏览器访问你的前端域名：

- 域名 DNS 已解析到运行面板和 local agent 的服务器。
- 服务器安全组、防火墙已放行你要监听的端口，例如 `80`、`443` 或规则里使用的端口。
- 后端服务地址能从这台服务器访问。

## 下一步

- 需要更详细的部署说明，读 [Docker Compose 部署](./docker-compose.md)。
- 已经部署完成，直接读 [添加 HTTP 规则](./http-rule.md)。
