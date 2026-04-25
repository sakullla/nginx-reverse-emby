# Nginx-Reverse-Emby

[![Docker Build](https://github.com/sakullla/nginx-reverse-emby/actions/workflows/docker-build.yml/badge.svg)](https://github.com/sakullla/nginx-reverse-emby/actions/workflows/docker-build.yml)
![Docker Pulls](https://img.shields.io/docker/pulls/sakullla/nginx-reverse-emby?color=blue)

专为 Emby、Jellyfin 及各种 HTTP/TCP 服务设计的自动化反向代理解决方案。支持可视化面板管理、多节点集中管控、证书自动续期、L4 代理、Relay 隧道与 IPv4/IPv6 双栈。

## 核心特性

- **纯 Go 运行时**：控制面 (Go + Vue 3) 与执行面 (Go agent) 完全不依赖 Nginx，单一二进制即可运行
- **可视化面板**：轻量管理界面，支持 HTTP/L4 规则增删改查、证书统一管理、Agent 状态监控
- **自动化 SSL**：集成 ACME (lego)，支持 HTTP/DNS API 自动申请并续期证书
- **Master/Agent 架构**：集中管理多个代理节点，Agent 通过心跳拉取配置，NAT 环境无需入站端口
- **全栈协议支持**：HTTP/HTTPS 代理、L4 TCP 代理、Relay 隧道 (tls_tcp/quic)、HTTP/3、IPv4/IPv6 双栈
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
│                    数据: SQLite (panel/data/)    │
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
- **数据存储**：SQLite，数据目录 `panel/data/`

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
  API_TOKEN: your-secure-token              # 面板访问令牌（必填）
  MASTER_REGISTER_TOKEN: your-register-token # Agent 注册令牌（不使用 Agent 可忽略）
```

启动服务：

```bash
docker compose up -d
```

访问面板 `http://<服务器IP>:8080`，使用 `API_TOKEN` 登录。

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
| `NRE_HEARTBEAT_INTERVAL` | `30s` | 心跳同步间隔（Go duration 格式） |
| `NRE_MANAGED_CERT_RENEW_INTERVAL` | `24h` | 托管证书续期检查间隔 |
| `ACME_DNS_PROVIDER` | - | DNS 验证提供商（如 `cf`） |
| `CF_Token` / `CF_TOKEN` | - | Cloudflare API Token |

> 所有 `NRE_` 前缀的环境变量同时作用于 Master 内嵌 local agent 和独立部署的 `go-agent`。
> 时间类变量使用 Go `time.ParseDuration` 格式（如 `500ms`、`5s`、`2m`）。

### HTTP 传输与流式恢复

| 变量 | 默认值 | 说明 |
| :--- | :--- | :--- |
| `NRE_HTTP_DIAL_TIMEOUT` | `30s` | HTTP upstream 连接建立超时 |
| `NRE_HTTP_TLS_HANDSHAKE_TIMEOUT` | `10s` | HTTPS upstream TLS 握手超时 |
| `NRE_HTTP_RESPONSE_HEADER_TIMEOUT` | `30s` | 等待 upstream 响应头超时 |
| `NRE_HTTP_IDLE_CONN_TIMEOUT` | `90s` | upstream 空闲连接回收超时 |
| `NRE_HTTP_KEEP_ALIVE` | `30s` | upstream TCP keepalive 间隔 |
| `NRE_HTTP_MAX_CONNS_PER_HOST` | `32` | 每个 upstream 主机允许的最大并发连接数，避免海报墙等突发图片请求把单个 Emby 后端打满 |
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

也可直接备份挂载目录 `./data`（即容器内 `/opt/nginx-reverse-emby/panel/data`）。

## 版本更新

`desired_version` 由控制面下发，驱动 Go agent 版本升级：

1. 在控制面为 agent 或版本策略设置 `desired_version`
2. 准备安装包来源：Linux/macOS 可使用控制面公开的 agent 资产，或在版本策略中配置自托管 URL 与 `sha256`；Windows 客户端包从 GitHub Release 获取
3. Agent 在心跳同步时收到 `desired_version`、`version_package` 与 `version_sha256`
4. 平台匹配且包信息完整时，agent 下载、校验并执行更新，后续心跳上报新版本

未匹配到当前平台的版本包时，控制面保留 `desired_version` 但 agent 不执行更新。

## 高级特性

### Relay 隧道

- 支持 `transport_mode=tls_tcp|quic`，默认 `tls_tcp`
- `tls_tcp` 为单外层 TLS 连接承载多逻辑流的复用隧道，支持长连接复用和多路复用
- 支持 `obfs_mode=off|early_window_v2`（仅对 `tls_tcp` 生效）
- UDP relay：`quic` 走流内包帧，`tls_tcp` 走 UoT
- 真实 TLS 0-RTT 仅在 `quic` 路径可用（Go `crypto/tls` 限制）

### HTTP/3

设置 `NRE_HTTP3_ENABLED=true` 可让 HTTPS 入口同时启用 HTTP/3 (QUIC)。

### 证书管理

- **HTTP 验证**：需开放 80 端口
- **DNS 验证**（推荐）：通过 DNS API 自动验证，无需开放端口
- 设置 `ACME_DNS_PROVIDER=cf` 并配置 `CF_Token` 即可启用 Cloudflare DNS 验证

## 开发

### 前置要求

- Go 1.26+
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
go test ./...
```

### Docker 构建

```bash
docker build -t nginx-reverse-emby .
docker compose up -d
```

Dockerfile 使用多阶段构建，自动交叉编译 Go agent 到 Linux/macOS 的 AMD64/ARM64 平台。Windows 客户端包不随控制面镜像构建或公开，后续通过 GitHub Release 分发。

## 常见问题

<details>
<summary>为什么推荐使用 host 网络模式？</summary>

`network_mode: host` 让容器直接监听宿主机端口，避免 Docker 端口映射的复杂性，特别适合 IPv6、动态多端口和 L4 代理场景。

</details>

<details>
<summary>如何备份规则和证书？</summary>

方式一：使用面板内置的 `导出备份` 功能。方式二：直接备份 `docker-compose.yaml` 中挂载的 `./data` 目录。

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

---

如果这个项目对你有帮助，请给一个 Star！
