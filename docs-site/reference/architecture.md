# 架构与特性

Nginx-Reverse-Emby 是面向 Emby、Jellyfin 以及任意 HTTP / TCP 服务的纯 Go 反向代理控制面。默认部署路径是 Docker Compose 启动控制面，并启用内嵌 `local` agent 执行代理规则。

## 核心特性

- **纯 Go 运行时**：控制面和执行面都不依赖 Nginx。
- **可视化面板**：管理 HTTP 规则、L4 规则、Relay 监听器、WireGuard Profile、证书、Egress Profile 和 Agent。
- **自动化 SSL**：支持 ACME HTTP-01 与 Cloudflare DNS-01 验证和证书续期，也支持手动上传。
- **Master / Agent 架构**：集中管理多节点，Agent 通过心跳拉取配置。
- **全栈协议**：HTTP / HTTPS、L4 TCP / UDP、Relay、WireGuard、HTTP/3、IPv4 / IPv6。
- **流式恢复**：HTTP 代理内置中断流恢复和 backend 重试机制。
- **出口策略**：HTTP / L4 规则可选择 Egress Profile（直连、SOCKS / HTTP 代理、WireGuard 出口）。
- **流量额度**：按网卡统计入站 / 出站，支持月度额度、超额阻断、计费周期与手工校准。
- **版本管理**：控制面向 Agent 下发 `desired_version`，支持自托管安装包与 SHA256 校验。

## 运行结构

```text
Master 控制面
├─ Vue 3 SPA（面板）
├─ Go Control Plane
│  ├─ REST API（/api、/panel-api 别名）
│  ├─ Agent 注册与管理
│  ├─ 规则、证书、Relay、WireGuard、Egress 存储
│  ├─ 流量统计与额度
│  └─ 版本策略下发
├─ local agent（内嵌）
│  ├─ HTTP 代理引擎
│  ├─ L4 代理（TCP / UDP）
│  ├─ Relay
│  └─ WireGuard / 流量采集
└─ SQLite / PostgreSQL / MySQL
```

远程 Agent 通过 heartbeat pull 主动向 Master 同步期望状态。NAT 节点只需要能访问 Master，不要求 Master 主动连进 Agent。

## 面板结构

```text
首页（仪表盘：节点状态 / 流量概览 / Top 规则 / Top 节点）
流量管理
├─ HTTP 规则
└─ L4 规则
基础设施
├─ 证书管理
├─ Relay 监听器
├─ WireGuard 配置
└─ 节点管理
设置
├─ 通用
├─ Egress Profiles
├─ 数据管理（备份导出 / 导入）
└─ 关于
版本策略（独立页面）
```

## 控制面请求流

```text
浏览器
  -> Go 控制面
    -> 鉴权后的 /api/* 路由
    -> /panel-api/* 兼容别名
    -> 公开 Agent 资产路由（join-agent.sh、二进制）
    -> 构建好的前端静态文件 / SPA fallback
```

## Agent 同步流（pull 模型）

1. Master 存储期望状态和期望版本。
2. 注册过的 Agent 向 Master 发送心跳 / 同步请求。
3. Master 返回 HTTP 规则、L4 规则、Relay 监听器、证书和版本信息。
4. Agent 在本地应用配置，并在后续心跳里回报当前状态和版本。

## 数据存储

Docker Compose 默认使用 SQLite，数据目录挂载到宿主机 `./data`。要切换到 PostgreSQL 或 MySQL，通过 `NRE_DATABASE_DRIVER` 和 `NRE_DATABASE_DSN` 配置；从已有数据迁移使用 `migrate-storage`，见 [迁移](../operations/migration.md)。

普通部署无需改数据库，保持默认 SQLite 即可。

## 为什么默认使用 host 网络

面板中的 HTTP、L4、Relay 监听端口是运行时动态创建的。Docker bridge 网络无法在容器启动后自动发布新增端口，所以默认 Compose 使用 host 网络，让 `local` agent 直接绑定宿主机端口。这意味着规则里填写的监听端口会直接占用 VPS 端口，需要提前检查端口冲突并放行防火墙。

## legacy deploy.sh

`deploy.sh`、`conf.d/` 和仓库根目录 `nginx.conf` 是历史独立 Nginx 工作流，不是当前默认控制面路径。

仍需使用主机 Nginx 模式时，可以执行：

```bash
bash <(curl -sSL https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/deploy.sh)
```

非交互添加规则示例：

```bash
curl -sSL https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/deploy.sh | bash -s -- \
  -y https://app.example.com -r http://127.0.0.1:8096
```

常用参数：

| 参数 | 说明 |
| --- | --- |
| `-y, --you-domain` | 前端访问地址。 |
| `-r, --r-domain` | 后端目标地址。 |
| `-m, --cert-domain` | 手动指定证书主域名。 |
| `-d, --parse-cert-domain` | 自动提取根域名作为证书域名。 |
| `-D, --dns` | 使用 DNS API 模式申请证书。 |
| `--no-proxy-redirect` | 禁用 302/307 重定向代理。 |
| `--remove` | 移除指定域名配置。 |
| `-Y, --yes` | 非交互模式自动确认。 |
