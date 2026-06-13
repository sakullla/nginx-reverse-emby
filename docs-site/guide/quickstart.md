# 快速开始

这篇是第一次使用的最短路径。你有一台能访问目标后端的 VPS（优化线路、海外落地、内网穿透节点都可以），想把任意 HTTP / HTTPS 服务反代到自己的域名。

最常见的场景是反代 Emby / Jellyfin：源站线路慢、绕路，或每次观看都要挂代理。让 VPS 作为中转入口，播放器访问你的域名，VPS 再去访问源站。HTTP 规则、L4 规则、Web 服务、API 网关同理。

走完这条线，你应该能打开类似 `http://app.example.com` 或 `https://app.example.com` 的地址，访问到原来的服务。

## 你需要先准备

- 一台已安装 Docker 和 Docker Compose 的 VPS，且能稳定访问你要反代的后端。
- 一个可解析到这台 VPS 的域名，例如 `app.example.com`。
- 后端服务的原始访问地址，例如 `https://origin.example.net`。
- 一个自己设置的访问令牌（`API_TOKEN`）。

先在 VPS 上确认能访问后端：

```bash
curl -I https://origin.example.net
```

如果 VPS 自己都访问不了后端，反代规则创建成功也无法正常工作。

## 第 1 步：下载 Compose 文件

```bash
mkdir -p nginx-reverse-emby && cd nginx-reverse-emby
curl -O https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/docker-compose.yaml
mkdir -p data
```

## 第 2 步：修改访问令牌

编辑 `docker-compose.yaml`，至少改这几项：

```yaml
environment:
  API_TOKEN: your-secure-token
  MASTER_REGISTER_TOKEN: your-register-token
  NRE_TIMEZONE: Asia/Shanghai
```

`API_TOKEN` 是你登录面板时输入的访问令牌，请使用强随机字符串，不要用示例值。

`MASTER_REGISTER_TOKEN` 是远程 Agent 向控制面注册时使用的令牌。如果你只用内嵌的 `local` 节点，可以暂时设成另一个强随机字符串；不显式设置时它会回退到 `API_TOKEN`。

## 第 3 步：启动面板

```bash
docker compose up -d
```

启动后打开：

```text
http://<服务器 IP>:8080
```

输入刚才设置的 `API_TOKEN` 登录。登录后，左侧导航的结构大致如下：

```text
首页（仪表盘 / 流量概览）
流量管理
├─ HTTP 规则
└─ L4 规则
基础设施
├─ 证书管理
├─ Relay 监听器
├─ WireGuard 配置
└─ 节点管理
设置（通用 / 出口 Profile / 数据管理 / 关于）
```

## 第 4 步：添加 HTTP 规则

进入 **HTTP 规则**，选择 `local` 节点，点击 **添加规则**：

| 字段 | 示例 | 说明 |
| --- | --- | --- |
| 前端访问地址 | `app.example.com` | 你自己访问的入口域名，需解析到这台 VPS。 |
| 后端地址 | `https://origin.example.net` | 后端服务的完整地址（含协议、域名、端口）。 |
| 启用规则 | 开启 | 保存后规则才会下发并生效。 |

保存后 `local` agent 会按心跳同步配置并开始监听。详细字段（302/307 改写、出口 Profile、Relay 链路等）见 [HTTP 反向代理](./http-rule.md)。

## 第 5 步：验证访问

确认以下条件后，用浏览器访问你的入口域名：

- 入口域名 DNS 已解析到这台 VPS。
- VPS 安全组、防火墙已放行监听端口（HTTP 通常是 `80`，HTTPS 通常是 `443`）。
- VPS 能访问后端服务。
- 客户端填写的是你的入口域名，而不是后端原始地址。

## 下一步

- 想用 HTTPS：先读 [证书与 HTTPS](./certificates.md)，HTTP-01 自动签发无需额外配置。
- 需要更完整的部署说明（数据目录、host 网络、多数据库）：读 [部署](./deploy.md)。
- 要转发 TCP/UDP 端口或使用中继隧道：读 [L4 规则与 Relay](./l4-relay.md)。
- 接入多台节点：读 [Agent 接入](./agent.md)。
