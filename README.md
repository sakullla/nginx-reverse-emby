# Nginx-Reverse-Emby

[![Docker Build](https://github.com/sakullla/nginx-reverse-emby/actions/workflows/docker-build.yml/badge.svg)](https://github.com/sakullla/nginx-reverse-emby/actions/workflows/docker-build.yml)
![Docker Pulls](https://img.shields.io/docker/pulls/sakullla/nginx-reverse-emby?color=blue)

专为 Emby、Jellyfin 及各种 HTTP/TCP 服务设计的自动化反向代理解决方案。支持可视化面板管理、多节点集中管控、证书自动续期、L4 代理、Relay 隧道与 IPv4/IPv6 双栈。

## 核心特性

- **纯 Go 运行时**：控制面 (Go + Vue 3) 与执行面 (Go agent) 完全不依赖 Nginx，单一二进制即可运行
- **可视化面板**：轻量管理界面，支持 HTTP/L4 规则增删改查、证书统一管理、Agent 状态监控
- **自动化 SSL**：集成 ACME (lego)，支持 HTTP/DNS API 自动申请并续期证书
- **Master/Agent 架构**：集中管理多个代理节点，Agent 通过心跳拉取配置，NAT 环境无需入站端口
- **全栈协议支持**：HTTP/HTTPS 代理、L4 TCP 代理、Relay 隧道 (tls_tcp/quic/wireguard)、HTTP/3、IPv4/IPv6 双栈
- **流式恢复**：内置中断流恢复与 backend 重试机制，保障大文件传输稳定性
- **版本管理**：支持通过 `desired_version` 从控制面向 Agent 推送版本升级

## 架构概览

```
┌─────────────────────────────────────────────────┐
│                  Master (控制面)                  │
│  ┌──────────────┐  ┌──────────────────────────┐ │
│  │  Vue 3 SPA   │  │  Go Control Plane        │ │
│  │  (前端面板)   │  │  - REST API              │ │
│  │              │  │  - Agent 注册/管理        │ │
│  │              │  │  - 规则/证书/Relay 存储    │ │
│  │              │  │  - 版本策略下发           │ │
│  └──────────────┘  └──────────────────────────┘ │
│                    ┌──────────────────────────┐ │
│                    │  Local Agent (内嵌)       │ │
│                    │  - HTTP 代理引擎          │ │
│                    │  - L4 代理 / Relay        │ │
│                    └──────────────────────────┘ │
│                    数据: PostgreSQL / SQLite     │
└─────────────────────────────────────────────────┘
         │                    │
    Heartbeat Pull      Heartbeat Pull
         │                    │
┌────────▼──────┐  ┌─────────▼─────────┐
│  Remote Agent │  │  Client Agent     │
│  (go-agent)   │  │  (Windows client) │
│  Linux/macOS  │  │  GitHub download  │
└───────────────┘  └───────────────────┘
```

- **同步模型**：Agent 通过心跳轮询 (Heartbeat Pull) 主动拉取期望状态
- **本地节点**：Master 容器默认内嵌 local agent，无需额外部署即可承担代理工作
- **数据存储**：Docker Compose 默认使用内置 SQLite；也可按需切换到 PostgreSQL/MySQL

## 快速开始

### Docker Compose（推荐）

```bash
mkdir -p nginx-reverse-emby && cd nginx-reverse-emby
curl -O https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/docker-compose.yaml
mkdir -p data
```

编辑 `docker-compose.yaml`，修改以下配置：

```yaml
environment:
  API_TOKEN: your-secure-token                # 面板访问令牌（必填）
  MASTER_REGISTER_TOKEN: your-register-token  # Agent 注册令牌（不使用 Agent 可忽略）
  NRE_TIMEZONE: Asia/Shanghai                 # 控制面板统一时区，按需修改
```

启动服务：

```bash
docker compose up -d
```

访问面板 `http://<服务器IP>:8080`，使用 `API_TOKEN` 登录。

默认 Compose 只启动 `nginx-reverse-emby` 一个容器，并使用 `network_mode: host`。内嵌 local agent 创建的 HTTP/L4/Relay 监听端口会直接暴露在宿主机上；面板数据、SQLite 数据库、证书材料等持久化到宿主机 `./data`，对应容器内 `/opt/nginx-reverse-emby/panel/data`。

### 主机模式（deploy.sh）

适用于不使用 Docker 的独立 Nginx 节点（历史兼容路径）：

```bash
# 交互式安装
bash <(curl -sSL https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/deploy.sh)

# 非交互式添加规则
curl -sSL https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/deploy.sh | bash -s -- \
  -y https://emby.example.com -r http://127.0.0.1:8096
```

<details>
<summary>deploy.sh 参数列表</summary>

| 参数 | 说明 | 示例 |
| :--- | :--- | :--- |
| `-y, --you-domain` | 前端访问地址 | `-y https://emby.example.com` |
| `-r, --r-domain` | 后端目标地址 | `-r http://192.168.1.10:8096` |
| `-m, --cert-domain` | 手动指定证书主域名 | `-m example.com` |
| `-d, --parse-cert-domain` | 自动提取根域名作为证书域名 | `-d` |
| `-D, --dns` | 使用 DNS API 模式申请证书 | `-D cf` |
| `-R, --resolver` | 手动指定 DNS 解析服务器 | `-R <DNS_IP>` |
| `--no-proxy-redirect` | 禁用 302/307 重定向代理 | `--no-proxy-redirect` |
| `--gh-proxy` | 指定 GitHub 加速代理 | `--gh-proxy https://gh.example.com` |
| `--cf-token` | Cloudflare API Token | `--cf-token xxxx` |
| `--cf-account-id` | Cloudflare Account ID | `--cf-account-id xxxx` |
| `--remove` | 移除指定域名的配置 | `--remove https://emby.example.com` |
| `-Y, --yes` | 非交互模式自动确认 | `-Y` |

</details>

## 环境变量配置

### 控制面核心变量

| 变量 | 默认值 | 说明 |
| :--- | :--- | :--- |
| `API_TOKEN` | 必填 | 面板访问令牌，同时作为 API 认证凭证 |
| `MASTER_REGISTER_TOKEN` | 与 `API_TOKEN` 相同 | Agent 注册令牌 |
| `PANEL_BACKEND_HOST` | `0.0.0.0` | 控制面监听地址 |
| `PANEL_BACKEND_PORT` | `8080` | 控制面监听端口 |
| `NRE_ENABLE_LOCAL_AGENT` | `1` | 是否启用内嵌 local agent |
| `NRE_LOCAL_AGENT_ID` | `local` | Local agent 标识 |
| `NRE_LOCAL_AGENT_NAME` | `local` | Local agent 显示名称 |
| `NRE_DATABASE_DRIVER` | `sqlite` | 控制面数据库驱动；Docker Compose 默认使用 `sqlite`，可选 `sqlite`、`postgres`、`mysql` |
| `NRE_DATABASE_DSN` | - | 控制面数据库 DSN；SQLite 为空时使用 `NRE_DATA_DIR/panel.db` |
| `NRE_TIMEZONE` | `UTC` | 控制面板统一时区（IANA 名称，如 `Asia/Shanghai`），用于流量日/月汇总、周期边界和清理口径 |
| `NRE_HEARTBEAT_INTERVAL` | `30s` | 心跳同步间隔（Go duration 格式） |
| `NRE_TRAFFIC_STATS_ENABLED` | `true` | 是否启用流量统计模块；关闭后控制面不迁移 traffic history 表、不持久化 stats、不执行额度阻断 |
| `NRE_WIREGUARD_ENABLED` | `true` | 是否启用 WireGuard 模块；关闭后控制面不迁移 WireGuard Profile/Client 表、不提供 WireGuard API，Agent 不声明 WireGuard 能力 |
| `NRE_TRAFFIC_INTERFACES` | - | Agent 主机网卡采集白名单，逗号分隔（如 `eth0,ens3`）；为空时自动排除 loopback/docker/veth/bridge/tun/tap 等虚拟接口 |
| `NRE_TRAFFIC_CLEANUP_INTERVAL` | `24h` | 主动清理 traffic history 的周期；设为 `0`、`off` 或 `disabled` 可关闭，仅在 `NRE_TRAFFIC_STATS_ENABLED=true` 时生效 |
| `NRE_MANAGED_CERT_RENEW_INTERVAL` | `24h` | 托管证书续期检查间隔 |
| `ACME_DNS_PROVIDER` | - | DNS 验证提供商（如 `cf`） |
| `CF_Token` / `CF_TOKEN` | - | Cloudflare API Token |

> `NRE_TIMEZONE` 是控制面板全局配置，不是 Agent 级配置；独立 `go-agent` 不需要配置它。
> 除控制面板专用变量外，部分 `NRE_` 前缀的 Agent 运行参数同时作用于 Master 内嵌 local agent 和独立部署的 `go-agent`。
> 时间类变量使用 Go `time.ParseDuration` 格式（如 `500ms`、`5s`、`2m`）。

流量统计以 Agent 所在主机/网络命名空间的网卡累计计数作为周期总量和额度阻断口径；HTTP 规则、L4 规则和 Relay 监听器的代理侧统计保留为分项分析。Linux Agent 优先通过 netlink 读取内核 `rtnl_link_stats64` 网卡计数，失败时回退到 `/proc/net/dev`。如果运行在 Docker bridge 网络中，采到的是容器网络命名空间流量；需要 VPS 主机网卡总量时请使用 host network 或显式挂载/部署到主机环境，并可用 `NRE_TRAFFIC_INTERFACES` 限定计入的网卡。

服务端会持久化 raw cursor、小时桶、日汇总、月汇总、节点策略、校准基线和事件。网卡计数器是系统累计值，服务端按本次累计值减上次累计值入桶；如果遇到系统重启（boot id 变化）、网卡重建或计数回退，会记录为 counter reset，并把当前值作为新基线，避免产生负数或误扣历史周期。远程节点可在面板中配置 `traffic_stats_interval`，取值为 Go duration 格式（如 `30s`、`1m`、`5m`）。该周期只控制心跳 stats 上报频率，不会重置计数器。

节点详情的流量策略支持配置计费方向、每月起始日期、月额度、超额阻断、保留周期、校准当前周期初始值，以及手动清理。月额度在数据库中按 bytes 保存，面板支持以 KiB/MiB/GiB/TiB 等单位录入和展示。主动清理按每个节点自己的保留策略执行，不删除当前 raw cursor、当前周期校准基线或节点策略。

如不需要流量统计或希望降低数据库写入量，可设置：

```env
NRE_TRAFFIC_STATS_ENABLED=false
```

数据库示例：

```env
# MySQL
NRE_DATABASE_DRIVER=mysql
NRE_DATABASE_DSN=nre:nre@tcp(mysql:3306)/nre?parseTime=true&charset=utf8mb4

# SQLite legacy/dev
NRE_DATABASE_DRIVER=sqlite
NRE_DATABASE_DSN=/opt/nginx-reverse-emby/panel/data/panel.db
```

### HTTP 传输与流式恢复

| 变量 | 默认值 | 说明 |
| :--- | :--- | :--- |
| `NRE_HTTP_DIAL_TIMEOUT` | `30s` | HTTP upstream 连接建立超时 |
| `NRE_HTTP_TLS_HANDSHAKE_TIMEOUT` | `10s` | HTTPS upstream TLS 握手超时 |
| `NRE_HTTP_RESPONSE_HEADER_TIMEOUT` | `30s` | 等待 upstream 响应头超时 |
| `NRE_HTTP_IDLE_CONN_TIMEOUT` | `90s` | upstream 空闲连接回收超时 |
| `NRE_HTTP_KEEP_ALIVE` | `30s` | upstream TCP keepalive 间隔 |
| `NRE_HTTP_MAX_CONNS_PER_HOST` | `64` | 每个 upstream 主机允许的最大并发连接数，避免海报墙等突发图片请求把单个 Emby 后端打满 |
| `NRE_HTTP_STREAM_RESUME_ENABLED` | `true` | 是否启用中断流恢复 |
| `NRE_HTTP_STREAM_RESUME_MAX_ATTEMPTS` | `2` | 单次请求最多追加恢复次数（正整数） |
| `NRE_HTTP_SAME_BACKEND_RETRY_ATTEMPTS` | `1` | 同一 backend 额外重试次数。仅对 retry-safe 方法生效，且只在上游返回响应前的可重试 transport/read 错误上触发 |
| `NRE_BACKEND_FAILURE_BACKOFF_BASE` | `1s` | backend 连续失败退避起始值 |
| `NRE_BACKEND_FAILURE_BACKOFF_LIMIT` | `15s` | backend 连续失败退避上限（指数退避封顶） |

> `NRE_HTTP_SAME_BACKEND_RETRY_ATTEMPTS` 不是通用 POST/5xx 重试开关：不会对 POST 生效，也不会在已收到 HTTP 5xx 响应后重放。Master 内嵌 local agent 要求正整数；独立 `go-agent` 允许设为 `0`。

<details>
<summary>中断流恢复覆盖范围</summary>

- `GET` 全量响应（`200`，且原请求无 `Range`）
- `GET` 单区间响应（`206`，且请求是单个 byte-range）

upstream 必须持续满足 `Accept-Ranges: bytes`，且返回稳定校验器（强 `ETag` 或 `Last-Modified`）。恢复请求携带 `Range` / `If-Range`；若校验器或 `Content-Range` 不一致或变成 multipart ranges，则停止拼接并返回错误。

</details>

### Relay 隧道超时

| 变量 | 默认值 | 说明 |
| :--- | :--- | :--- |
| `NRE_RELAY_DIAL_TIMEOUT` | `5s` | relay 上游拨号超时 |
| `NRE_RELAY_HANDSHAKE_TIMEOUT` | `5s` | relay 握手超时 |
| `NRE_RELAY_FRAME_TIMEOUT` | `5s` | relay 单帧读写超时 |
| `NRE_RELAY_IDLE_TIMEOUT` | `2m` | relay 空闲连接超时 |

### Agent 独立部署变量

| 变量 | 说明 |
| :--- | :--- |
| `NRE_AGENT_ID` | Agent 标识 |
| `NRE_AGENT_NAME` | Agent 显示名称 |
| `NRE_AGENT_TOKEN` | Agent 认证令牌 |
| `NRE_MASTER_URL` | Master 控制面地址（必填） |
| `NRE_DATA_DIR` | 数据目录，默认 `/var/lib/nre-agent` |
| `NRE_HTTP3_ENABLED` | 是否启用 HTTP/3 入口 |

## Agent 管理

独立 Agent 默认数据目录为 `/var/lib/nre-agent`。控制面默认数据目录为 `/opt/nginx-reverse-emby/panel/data`。

### 加入 Agent 节点

**Linux：**

```bash
curl -fsSL http://master.example.com:8080/panel-api/public/join-agent.sh | sh -s -- \
  --register-token your-register-token \
  --install-systemd
```

**macOS：**

```bash
curl -fsSL http://master.example.com:8080/panel-api/public/join-agent.sh | sh -s -- \
  --register-token your-register-token \
  --install-launchd
```

**Windows：**

当前控制面镜像不构建、不公开 Windows 原生 `nre-agent.exe` 资产。Windows 节点后续通过客户端方式接入，客户端安装包从 GitHub Release 下载，不再随控制面镜像发布。

1. 在控制面获取 `register token`
2. 从 GitHub Release 下载 Windows 客户端安装包
3. 在客户端中配置控制面 URL 和注册令牌
4. 由客户端完成注册、运行和后续连接管理

常见可选参数：

| 参数 | 说明 |
| :--- | :--- |
| `--agent-name` | 自定义 agent 名称 |
| `--tags` | 标签（逗号分隔） |
| `--agent-url` | Agent 外部访问地址 |
| `--binary-url` | 自定义 agent 下载地址 |
| `--data-dir` | 自定义数据目录 |

### NAT Agent

NAT Agent 只需能主动访问 Master 即可，无需 Master 能访问 Agent：

```bash
curl -fsSL http://master.example.com:8080/panel-api/public/join-agent.sh | sh -s -- \
  --register-token your-register-token \
  --agent-name nat-edge-01 \
  --tags nat,edge \
  --install-systemd
```

更多手工部署示例见 `AGENT_EXAMPLES.md`。

### 从旧版 Agent 迁移

旧 `main` 版本轻量 Agent 迁移到当前 Go agent：

1. 在旧控制面执行 `导出备份`
2. 升级控制面并 `导入备份`
3. 在每台旧 Agent 机器执行：

```bash
curl -fsSL http://master.example.com:8080/panel-api/public/join-agent.sh | sh -s -- \
  migrate-from-main \
  --register-token your-register-token \
  --install-systemd
```

脚本会自动从 `/opt/nginx-reverse-emby-agent` 读取旧配置，复用原 `agent_token`，切换到 `/var/lib/nre-agent`，验证通过后清理旧 runtime 和 nginx 服务。

### 卸载 Agent

```bash
# 本地卸载入口（安装时自动部署）
/usr/local/bin/nginx-reverse-emby-agent-uninstall.sh

# 或在线卸载
curl -fsSL http://master.example.com:8080/panel-api/public/join-agent.sh | sh -s -- uninstall-agent
```

> 卸载只清理本机运行时和数据；控制面中的 agent 记录需手动删除。

## 备份与恢复

系统设置中提供 `导出备份` 与 `导入备份` 功能：

- 旧版控制面与当前纯 Go 控制面使用同一个可移植备份包格式，支持跨架构导入导出
- 导入时自动跳过冲突项并返回详细报告
- 证书 PEM 和私钥材料一并包含，可从旧版本无感迁移

也可直接备份面板挂载目录 `./data`（即容器内 `/opt/nginx-reverse-emby/panel/data`）。如果你自行切换到 PostgreSQL/MySQL，请同时使用对应数据库的备份方式导出数据库数据。

普通备份默认包含规则、Agent、Relay、证书、版本策略和证书材料，不包含高容量 traffic history（raw cursors、hourly/daily/monthly summaries、events）。

### 数据库迁移

从旧 SQLite 数据库迁移到 PostgreSQL 时，先确保目标数据库已启动，然后在控制面容器内执行：

```bash
nre-control-plane migrate-storage \
  --from-driver sqlite \
  --from-dsn /opt/nginx-reverse-emby/panel/data/panel.db \
  --to-driver postgres \
  --to-dsn 'postgres://nre:nre@127.0.0.1:5432/nre?sslmode=disable'
```

该命令会复制核心配置 rows、traffic policy rows 和 traffic baseline rows，并默认跳过高容量 traffic history 表。在控制面容器内执行时，SQLite source DSN 使用容器内路径 `/opt/nginx-reverse-emby/panel/data/panel.db`；在宿主机直接执行才使用宿主机路径（例如 `./data/panel.db`）。source 与 target 的 driver+dsn 完全相同时会被拒绝。

迁移会从 SQLite DSN 自动推断面板数据目录，用于复制 `managed_certificates/` 下的证书 PEM 和私钥材料。若 source 或 target 使用 PostgreSQL/MySQL，或证书材料目录不在 SQLite 文件同级目录下，请同时传入 `--from-data-root <old panel data dir>` 和/或 `--to-data-root <new panel data dir>`。

## 版本更新

`desired_version` 由控制面下发，驱动 Go agent 版本升级：

1. 在控制面为 agent 或版本策略设置 `desired_version`
2. 准备安装包来源：Linux/macOS 可使用控制面公开的 agent 资产，或在版本策略中配置自托管 URL 与 `sha256`；Windows 客户端包从 GitHub Release 获取
3. Agent 在心跳同步时收到 `desired_version`、`version_package` 与 `version_sha256`
4. 平台匹配且包信息完整时，agent 下载、校验并执行更新，后续心跳上报新版本

未匹配到当前平台的版本包时，控制面保留 `desired_version` 但 agent 不执行更新。

## 高级特性

### Relay 隧道

- 支持 `transport_mode=tls_tcp|quic|wireguard`，默认 `tls_tcp`
- `tls_tcp` 为单外层 TLS 连接承载多逻辑流的复用隧道，支持长连接复用和多路复用
- `wireguard` 使用所选 WireGuard Profile 作为传输路径，Relay 原有 TLS、mux、认证仍在 WireGuard 链路上生效，不是认证绕过
- 支持 `obfs_mode=off|early_window_v2`（仅对 `tls_tcp` 生效）
- UDP relay：`quic` 走流内包帧，`tls_tcp` 走 UoT，`wireguard` 复用所选 Profile 的隧道路径
- 真实 TLS 0-RTT 仅在 `quic` 路径可用（Go `crypto/tls` 限制）

### WireGuard Profile

控制面提供 WireGuard Profiles 页面，可按 Agent 管理标准 WireGuard 配置。Profile 包含 private key、listen port、addresses、interface addresses、peers、DNS、MTU、enabled 和 tags，可用于 Relay transport 或 L4 规则。密钥等敏感字段在接口和面板中会显示为 `xxxxx`；编辑时保持 redacted 值不变即可保留已存储密钥。

创建通用 WireGuard Profile 时，`Addresses` 表示 Agent 主机上 WireGuard UDP socket 的实际监听地址（例如 `192.168.0.109`，留空默认 `0.0.0.0`），`WG 分配地址` 表示 WireGuard 接口地址/地址池（例如 `10.8.0.1/24`、`fd10:8::1/64`）。创建时可留空自动分配，之后可在面板中调整。自动分配地址池可通过 `NRE_WIREGUARD_AUTO_ADDRESS_POOLS` 配置，逗号分隔同时支持 IPv4/IPv6，默认 `10.8.x.1/24,fd10:8:x::1/64`。该功能只管理标准 WireGuard profile，不内置 Cloudflare WARP 注册、MASQUE 或密钥轮换。

### L4 与 WireGuard

L4 规则设置 `listen_mode=wireguard` 时，客户端先连接目标 Agent 的 WireGuard UDP endpoint，进入隧道后再访问规则配置的虚拟服务 IP/端口。未使用 WireGuard 的普通客户端仍访问常规公开 L4 监听端口。

L4 规则设置 `proxy_egress_mode=wireguard` 时，TCP 代理入口的出站连接会通过所选 WireGuard Profile 发起，语义类似现有代理出站模式，只是 egress 路径改为 WireGuard。

如需配合 Cloudflare WARP，可使用可导出的标准 WireGuard profile（如果账号/客户端支持），或在 Agent 主机外部运行 Cloudflare WARP 客户端并自行配置路由；内置 WireGuard 功能不负责 WARP 的注册和自动轮换。

### HTTP/3

设置 `NRE_HTTP3_ENABLED=true` 可让 HTTPS 入口同时启用 HTTP/3 (QUIC)。

### 证书管理

- **HTTP 验证**：需开放 80 端口
- **DNS 验证**（推荐）：通过 DNS API 自动验证，无需开放端口
- 设置 `ACME_DNS_PROVIDER=cf` 并配置 `CF_Token` 即可启用 Cloudflare DNS 验证

## 开发

### 前置要求

- Go 1.26.4+
- Node.js 24+（前端开发）
- Docker（容器构建）

### 控制面 (Go)

```bash
cd panel/backend-go
go run ./cmd/nre-control-plane
go test ./...
```

### 前端 (Vue 3 / Vite)

```bash
cd panel/frontend
npm ci
npm run dev      # 开发服务器（自动代理 /panel-api 到控制面）
npm run build    # 生产构建
npm run test     # 运行测试
```

### Go Agent

```bash
cd go-agent
go run ./cmd/nre-agent
make build       # 生成裁剪后的发布二进制
go test ./...
```

默认发布构建不包含 pprof；需要 `NRE_PPROF_ADDR` 时，用 `go run -tags debug ./cmd/nre-agent` 或 `go build -tags debug ./cmd/nre-agent` 启用。

### Docker 构建

```bash
docker build -t nginx-reverse-emby .
docker compose up -d
```

Dockerfile 使用多阶段构建，自动交叉编译 Go agent 到 Linux/macOS 的 AMD64/ARM64 平台。Windows 客户端包不随控制面镜像构建或公开，后续通过 GitHub Release 分发。

### HTTP/Relay 吞吐测试

```powershell
./scripts/http-relay-perf/run.ps1
```

这个 harness 用 Docker Compose 跑真实 `nre-agent`，对比 HTTP 直连和 relay 入口的下载吞吐，默认会把 `NRE_HTTP_MAX_CONNS_PER_HOST` 和 `NRE_TRAFFIC_STATS_ENABLED` 一并带进去。
默认延迟模型和 `relay-perf` 一致，`CLI -> HTTP` 以及 `HTTP -> backend` 两段都会走 `tc netem`，可用 `HARNESS_DELAY_CLI_TO_HTTP_MS`、`HARNESS_DELAY_HTTP_TO_BACKEND_MS` 和 `HARNESS_NETEM_DELAY_MS` 覆盖。

## 常见问题

<details>
<summary>为什么默认 Compose 使用 host 网络模式？</summary>

默认 Compose 会启用内嵌 local agent。代理规则的监听端口由面板动态配置，Docker bridge 网络无法在容器运行后自动发布新端口，因此应用服务默认使用 host 网络，确保除 `8080` 之外的 HTTP/L4/Relay 监听端口也能从宿主机访问。

</details>

<details>
<summary>如何备份规则和证书？</summary>

面板文件、规则和证书等可使用面板内置的 `导出备份` 功能，或直接备份宿主机 `./data`。如果你自行切换到 PostgreSQL，数据库数据应使用 `pg_dump` 导出，或在停库/确保一致性的前提下备份对应数据库目录。普通备份默认不包含 traffic history。

</details>

<details>
<summary>何时需要禁用 302/307 代理？</summary>

以下场景建议在面板中关闭「代理 302/307 重定向」开关：
- CDN 回源需要保留原始重定向地址
- 多跳转链接需要客户端直接访问
- OAuth 回调需保持重定向地址原样传递

</details>

<details>
<summary>如何验证纯 Go 控制面？</summary>

替换生产镜像前，先用复制的 `panel/data` 做影子验证：

```bash
cd panel/backend-go && go test ./...
cd ../../go-agent && go test ./...
docker build -t nginx-reverse-emby:pure-go --target control-plane-runtime .
sh scripts/verify-pure-go-master.sh /path/to/copied-panel-data
```

验证要点：
- `/panel-api/health`、`/panel-api/info`、`/panel-api/agents` 等端点全部返回 2xx
- `/panel-api/agents` 中能看到 `id=local` 且 `is_local=true`
- `join-agent.sh` 正常下载 agent 二进制

</details>

## 许可证

本项目基于 GNU General Public License v3.0 授权发布，详见 [LICENSE](./LICENSE)。

---

如果这个项目对你有帮助，请给一个 Star！
