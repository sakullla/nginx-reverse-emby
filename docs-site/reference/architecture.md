# 架构与功能

Nginx-Reverse-Emby 是一个完全用 Go 编写的反向代理控制面板。它为 Emby、Jellyfin 以及任何 HTTP/TCP 服务而设计。默认部署在 Docker Compose 中运行，并内置一个 `local` 代理，在同一台机器上处理代理规则。

## 它能做什么

| 功能 | 对你意味着什么 |
|------|--------------|
| **纯 Go 运行时** | 不需要 Nginx。控制面板和代理引擎都是用 Go 编写的。 |
| **Web UI** | 在浏览器中管理 HTTP 规则、TCP/UDP 规则、中继监听器、证书和代理节点。 |
| **自动 SSL** | 通过 Let's Encrypt（HTTP-01 或 Cloudflare DNS-01）获取免费证书，或上传你自己的证书。 |
| **主控 / 代理** | 从一个面板管理多台服务器。代理节点向主控注册并自动下载配置。 |
| **多协议** | 支持 HTTP/HTTPS、TCP/UDP、中继隧道、WireGuard、HTTP/3、IPv4 和 IPv6。 |
| **断流续传** | 如果下载或流媒体中断，代理可以自动恢复。 |
| **出口路由** | 通过 SOCKS/HTTP 代理或 WireGuard 隧道发送流量，而不是直接连接。 |
| **流量配额** | 按服务器跟踪带宽，设置月度限制，并在超限时阻止流量。 |
| **版本管理** | 从控制面板远程推送代理更新。 |

## 各组件如何协同工作

```text
主控（控制面板）
├─ Vue 3 网页界面
├─ Go 控制平面
│  ├─ REST API（/api/* 和 /panel-api/* 别名）
│  ├─ 代理注册与管理
│  ├─ 规则、证书、中继、WireGuard、出口配置文件的存储
│  ├─ 流量统计与配额
│  └─ 版本策略分发
├─ 本地代理（内置）
│  ├─ HTTP 代理引擎
│  ├─ TCP/UDP 代理
│  ├─ 中继隧道
│  └─ WireGuard / 流量采集
└─ SQLite / PostgreSQL / MySQL
```

**远程代理**（在其他服务器上）使用心跳拉取模式连接到主控。它们只需要出站互联网访问即可连接到主控。主控永远不需要直接连接到代理，这使得 NAT 和防火墙设置变得非常简单。

## Web UI 布局

```text
仪表盘（首页）
  ├─ 节点状态
  ├─ 流量概览
  ├─ 热门规则
  └─ 热门节点

流量管理
  ├─ HTTP 规则
  └─ L4 规则（TCP/UDP）

基础设施
  ├─ 证书
  ├─ 中继监听器
  ├─ WireGuard 配置
  └─ 节点（代理）

设置
  ├─ 常规
  ├─ 出口配置文件
  ├─ 数据管理（备份 / 导入）
  └─ 关于

版本策略（独立页面）
```

## 请求流程

```text
浏览器
  -> Go 控制平面
    -> 经过认证的 /api/* 路由
    -> /panel-api/* 兼容别名
    -> 公共代理资源（join-agent.sh、代理二进制文件）
    -> 构建好的前端静态文件 / SPA 回退
```

## 代理如何保持同步

1. 主控存储每个代理的期望配置和期望版本。
2. 已注册的代理定期向主控发送心跳/同步请求。
3. 主控回复 HTTP 规则、L4 规则、中继监听器、证书和版本信息。
4. 代理在本地应用配置，并在下一次心跳时报告其当前状态。

## 数据存储

默认情况下，Docker Compose 使用 **SQLite**，数据存储在挂载的主机目录（`./data`）中。对于正常使用，你不需要更改此设置。

要切换到 PostgreSQL 或 MySQL，请设置 `NRE_DATABASE_DRIVER` 和 `NRE_DATABASE_DSN`。有关迁移现有数据，请参阅 [迁移](../operations/migration.md)。

| 数据库 | 驱动值 | 典型 DSN |
|--------|--------|----------|
| SQLite（默认） | `sqlite` | 从数据目录自动检测 |
| PostgreSQL | `postgres` | `postgres://user:pass@host:5432/db?sslmode=disable` |
| MySQL | `mysql` | `user:pass@tcp(host:3306)/db?parseTime=true&charset=utf8mb4` |

## 为什么默认使用 Host 网络

控制面板在运行时动态创建监听端口（用于 HTTP 规则、L4 规则和中继监听器）。Docker 的桥接网络无法在容器启动后发布新端口，因此默认的 Compose 文件使用 **host 网络模式**。这意味着 `local` 代理直接绑定到主机的网络接口。

**重要提示：** 你在规则中配置的端口将直接占用 VPS 上的端口。请检查冲突并打开相应的防火墙规则。

## 旧版 deploy.sh

`deploy.sh`、`conf.d/` 和根目录下的 `nginx.conf` 是用于独立 Nginx 工作流的旧版文件。它们不是默认控制面板路径的一部分。

如果你仍然需要主机 Nginx 模式，请运行：

```bash
bash <(curl -sSL https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/deploy.sh)
```

非交互式示例：

```bash
curl -sSL https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/deploy.sh | bash -s -- \
  -y https://app.example.com -r http://127.0.0.1:8096
```

| 参数 | 含义 |
|------|------|
| `-y, --you-domain` | 前端使用的公共域名。 |
| `-r, --r-domain` | 后端目标地址。 |
| `-m, --cert-domain` | 手动指定证书域名。 |
| `-d, --parse-cert-domain` | 自动提取证书的根域名。 |
| `-D, --dns` | 使用 DNS API 模式签发证书。 |
| `--no-proxy-redirect` | 禁用 302/307 重定向代理。 |
| `--remove` | 移除指定域名的配置。 |
| `-Y, --yes` | 非交互模式（自动确认）。 |
