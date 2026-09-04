# 环境变量速查

控制面和 Agent 通过环境变量配置。时间值使用 Go duration 格式（`500ms`、`5s`、`2m`）。大多数 `NRE_*` 变量有旧版别名，旧配置可以继续使用。

## 快速索引

| 你想做的事 | 对应变量 |
|---|---|
| 设置面板密码 | `API_TOKEN`（必填） |
| 设置时区 | `NRE_TIMEZONE`（国内填 `Asia/Shanghai`） |
| 设置公网访问地址 | `NRE_PUBLIC_URL` |
| 设置 HTTP 临时随机面板入口 | `NRE_PANEL_PUBLIC_PATH` |
| 切换数据库 | `NRE_DATABASE_DRIVER` + `NRE_DATABASE_DSN` |
| 开启 Cloudflare DNS 验证 | `ACME_DNS_PROVIDER=cf` +（`CF_TOKEN` 或按域名映射） |
| 使用外部内部-PKI master key | `NRE_PKI_MASTER_KEY_FILE` |
| 关闭流量统计 | `NRE_TRAFFIC_STATS_ENABLED=false` |
| Agent 注册令牌 | `MASTER_REGISTER_TOKEN` |

---

## 控制面

| 变量 | 默认值 | 作用 |
|------|--------|------|
| `NRE_PANEL_TOKEN`（别名 `API_TOKEN`） | **必填** | 网页界面和 API 认证的登录令牌。示例占位值会被拒绝启动。 |
| `NRE_REGISTER_TOKEN`（别名 `MASTER_REGISTER_TOKEN`、`PANEL_REGISTER_TOKEN`、`API_TOKEN`） | 未设置时依次尝试 `MASTER_REGISTER_TOKEN`、`PANEL_REGISTER_TOKEN`、`API_TOKEN` | Agent 向控制面注册时使用的令牌。示例占位值会被拒绝启动。 |
| `NRE_CONTROL_PLANE_ADDR`（别名 `PANEL_BACKEND_HOST` + `PANEL_BACKEND_PORT`） | `0.0.0.0:8080` | 控制面监听的地址。 |
| `NRE_PUBLIC_URL` | 空 | 控制面公网 URL，例如 `https://panel.example.com`。设置后 join script 和 Agent 更新包 URL 会优先使用该值。 |
| `NRE_PANEL_PUBLIC_PATH` | 空 | 无域名 HTTP 临时部署时的面板入口路径，例如 `/panel-a1b2c3d4`。设置后根路径不再直接服务面板；它不能替代 token 和 HTTPS。 |
| `NRE_TRUST_FORWARDED_HEADERS` | `false` | 是否信任上游反代设置的 `X-Forwarded-*` 头。仅在反代会清洗并重写这些头时开启。 |
| `NRE_CONTROL_PLANE_DATA_DIR`（别名 `PANEL_DATA_ROOT`） | `/opt/nginx-reverse-emby/panel/data` | SQLite 数据库和运行时数据的目录。 |
| `NRE_PKI_MASTER_KEY_FILE` | 空 | 可选的内部 tunnel-PKI master key **容器内绝对路径**。挂载的父目录须私有且可写，以便 protected restore 在同目录原子轮换 key；只读文件/不可变 secret projection 不受支持。空值使用 `NRE_CONTROL_PLANE_DATA_DIR/pki/master.key`。它不改变控制面地址、`NRE_MASTER_URL` 或 token 认证。 |
| `NRE_FRONTEND_DIST_DIR`（别名 `PANEL_FRONTEND_DIST_DIR`） | `/opt/nginx-reverse-emby/panel/frontend/dist` | 存放构建好的前端文件的目录。 |
| `NRE_PUBLIC_AGENT_ASSETS_DIR`（别名 `PANEL_PUBLIC_AGENT_ASSETS_DIR`） | `/opt/.../public/agent-assets` | 公共代理资源的目录（加入脚本、二进制文件）。 |
| `NRE_ENABLE_LOCAL_AGENT`（别名 `MASTER_LOCAL_AGENT_ENABLED`） | `true` | 是否在控制面节点上运行内置的 `local` Agent。 |
| `NRE_LOCAL_AGENT_ID`（别名 `MASTER_LOCAL_AGENT_ID`） | `local` | 内置本地代理的标识符。 |
| `NRE_LOCAL_AGENT_NAME`（别名 `MASTER_LOCAL_AGENT_NAME`） | `local` | 内置本地代理的显示名称。 |
| `NRE_TIMEZONE` | 二进制为 `UTC`；Compose 为 `Asia/Shanghai` | 面板使用的时区（IANA 格式），用于每日/每月流量汇总和计费周期边界。Compose 同时把该值传给容器的 `TZ`。 |
| `NRE_HEARTBEAT_INTERVAL` | `30s` | 从控制面角度的心跳间隔。（Agent 默认是 `10s`；见下面的 Agent 部分。） |
| `NRE_DDNS_IP_PROBE_INTERVAL` | `5m` | 内置 `local` Agent 探测 DDNS 公网 IP 的最小间隔；独立于心跳和 Cloudflare DNS 对账间隔。 |
| `NRE_MARKETPLACE_REFRESH_TIMEOUT` | `30m` | 单次插件市场刷新（含 Git 拉取与验证）的容忍上限；同时约束手动刷新（手动刷新不受客户端断开影响）。 |
| `HTTP_PROXY` / `HTTPS_PROXY` / `NO_PROXY` | 代理为空；Compose 的 `NO_PROXY` 为 `localhost,127.0.0.1,::1` | 可选出站代理，供市场刷新拉取 Git 仓库等控制面出站请求使用。Docker Desktop host 网络容器内可用 `http://192.168.65.254:<端口>` 访问宿主机回环代理；Linux 上应使用容器实际可达地址。把控制面、数据库、Agent 和其它内网目标加入 `NO_PROXY`。 |
| `NRE_PROJECT_URL` | 空 | 项目主页 URL，显示在版本信息中。 |

---

## 通用 secret vault

| 变量 | 默认值 | 作用 |
|------|--------|------|
| `PANEL_VAULT_MASTER_KEY` | 空 | 32-byte envelope key，可使用 64 位 hex、base64 或 32-byte literal。空值时从 `API_TOKEN`（或 `NRE_PANEL_TOKEN`）确定性派生。 |
| `PANEL_VAULT_KEY_ID` | key digest 派生值；Compose 为 `primary` | 当前 key 的稳定 ID。只要仍使用同一个 key 就不要改变；轮换时新旧 ID 必须不同。 |
| `PANEL_VAULT_PREVIOUS_MASTER_KEY` | 空 | 一次性轮换输入：旧的显式 master key。不能与 `PANEL_VAULT_PREVIOUS_API_TOKEN` 同时设置。 |
| `PANEL_VAULT_PREVIOUS_API_TOKEN` | 空 | 一次性轮换输入：旧 key 由 panel token 派生时使用的旧 token。 |
| `PANEL_VAULT_PREVIOUS_KEY_ID` | 旧 key digest 派生值 | 旧部署记录 ciphertext 时使用的 key ID；仓库 Compose 的默认值是 `primary`。 |

已有 ciphertext 后不能只更换 token、key 或 key ID。轮换时同时设置新 `PANEL_VAULT_MASTER_KEY`、不同的新 `PANEL_VAULT_KEY_ID`，以及一种 previous key 来源和原 key ID。启动会事务性重加密 active secret；确认启动及既有 secret 读取成功后，才移除 `PANEL_VAULT_PREVIOUS_*` 并备份新 key。`.env` 应为 `0600`，且 key 必须与数据库作为同一个恢复点保存。

---

## 数据库

| 变量 | 默认值 | 作用 |
|------|--------|------|
| `NRE_DATABASE_DRIVER` | `sqlite` | 数据库驱动：`sqlite`、`postgres` 或 `mysql`。 |
| `NRE_DATABASE_DSN` | 空 | 数据库连接字符串。对于 SQLite，如果为空，默认为 `NRE_CONTROL_PLANE_DATA_DIR/panel.db`。 |

**PostgreSQL 示例：**

```ini
NRE_DATABASE_DRIVER=postgres
NRE_DATABASE_DSN=postgres://nre:nre@postgres:5432/nre?sslmode=disable
```

**MySQL 示例：**

```ini
NRE_DATABASE_DRIVER=mysql
NRE_DATABASE_DSN=nre:nre@tcp(mysql:3306)/nre?parseTime=true&charset=utf8mb4
```

---

## 证书 / ACME

| 变量 | 默认值 | 作用 |
|------|--------|------|
| `ACME_DNS_PROVIDER` | 空 | DNS 验证提供商。目前支持 `cf`（Cloudflare）。设置为 `cf`，并提供环境变量令牌或 `cloudflare-dns` 域名映射以启用 DNS-01。 |
| `CLOUDFLARE_DNS_API_TOKEN`（别名 `CF_DNS_API_TOKEN`、`CF_TOKEN`、`CF_Token`） | 空 | 全局 Cloudflare API 令牌，只在按域名解析未命中映射时作为 DNS-01 / DDNS 兜底。 |
| `CLOUDFLARE_ZONE_API_TOKEN`（别名 `CF_ZONE_API_TOKEN`） | 空 | 可选的 Cloudflare 区域令牌。 |
| `NRE_ACME_EMAIL` | 空 | ACME 账户注册用的电子邮件地址。 |
| `NRE_ACME_DIRECTORY_URL` | Let's Encrypt 生产环境 | ACME 目录 URL。 |
| `NRE_MANAGED_CERT_RENEW_INTERVAL` | `24h` | 检查证书续期的频率。 |

`ACME_DNS_PROVIDER=cf` 且存在环境变量令牌 **或** 已安装的 `cloudflare-dns` 映射时，DNS-01 才会启用。全局令牌只在解析未命中时作为兜底；某个域名既无映射又无环境变量令牌时该次操作失败。新手建议使用 Cloudflare API Token，不要使用 Global API Key；权限给 `区域 / 区域 / 读取`、`区域 / DNS / 读取`、`区域 / DNS / 编辑`，Zone Resources 只选择你的域名。详情请参阅 [证书与 HTTPS](../guides/certificates.md)。

---

## HTTP 传输（控制面和 Agent 共享）

这些设置控制 Agent 如何连接到后端服务器。

| 变量 | 默认值 | 作用 |
|------|--------|------|
| `NRE_HTTP_DIAL_TIMEOUT` | `30s` | 与上游服务器建立连接的超时时间。 |
| `NRE_HTTP_TLS_HANDSHAKE_TIMEOUT` | `10s` | 与上游服务器进行 TLS 握手的超时时间。 |
| `NRE_HTTP_RESPONSE_HEADER_TIMEOUT` | `30s` | 等待上游服务器响应头的超时时间。 |
| `NRE_HTTP_IDLE_CONN_TIMEOUT` | `90s` | 空闲连接保持打开的时间，之后关闭。 |
| `NRE_HTTP_KEEP_ALIVE` | `30s` | 上游连接的 TCP keep-alive 间隔。 |
| `NRE_HTTP_MAX_CONNS_PER_HOST` | `64` | 每个上游主机的最大并发连接数（代理端）。 |
| `NRE_HTTP_STREAM_RESUME_ENABLED` | `true` | 启用中断流/下载的自动恢复。 |
| `NRE_HTTP_STREAM_RESUME_MAX_ATTEMPTS` | `2` | 单个请求的最大恢复尝试次数。 |
| `NRE_HTTP_SAME_BACKEND_RETRY_ATTEMPTS` | `1` | 对同一后端的额外重试次数，仅适用于可重试的 HTTP 方法（GET、HEAD 等）。 |
| `NRE_BACKEND_FAILURE_BACKOFF_BASE` | `1s` | 后端故障后的起始退避时长。必须 ≤ 上限。 |
| `NRE_BACKEND_FAILURE_BACKOFF_LIMIT` | `15s` | 重复后端故障后的最大退避时长。 |

---

## Relay

| 变量 | 默认值 | 作用 |
|------|--------|------|
| `NRE_RELAY_DIAL_TIMEOUT` | `5s` | 连接到上游中继节点的超时时间。 |
| `NRE_RELAY_HANDSHAKE_TIMEOUT` | `5s` | 中继握手的超时时间。 |
| `NRE_RELAY_FRAME_TIMEOUT` | `5s` | 读取或写入单个中继帧的超时时间。 |
| `NRE_RELAY_IDLE_TIMEOUT` | `2m` | 空闲中继连接保持打开的时间。 |

---

## 流量统计

| 变量 | 默认值 | 作用 |
|------|--------|------|
| `NRE_TRAFFIC_STATS_ENABLED` | `true` | 启用或禁用流量统计和配额执行。 |
| `NRE_TRAFFIC_CLEANUP_INTERVAL` | `24h` | 清理旧流量历史数据的频率。设置为 `0`、`off` 或 `disabled` 以禁用清理。 |
| `NRE_TRAFFIC_INTERFACES` | 空 | 要监控的网络接口列表，逗号分隔。空表示所有接口。 |

流量配额模型请参阅 [流量统计原理](./traffic-accounting.md)。

---

## Agent

| 变量 | 默认值 | 作用 |
|------|--------|------|
| `NRE_AGENT_ID` | `linux-agent` | Agent 的唯一标识符。 |
| `NRE_AGENT_NAME` | `linux-agent` | Agent 的显示名称。 |
| `NRE_AGENT_TOKEN` | **必填** | Agent 心跳认证令牌（注册时生成）。 |
| `NRE_AGENT_VERSION` | `0.0.0` | 当前 Agent 版本，用于自更新比较。 |
| `NRE_MASTER_URL` | **必填** | Agent 连接的控制面 URL。 |
| `NRE_DATA_DIR` | `/var/lib/nre-agent` | Agent 存储本地数据的目录。 |
| `NRE_HEARTBEAT_INTERVAL` | `10s` | Agent 向控制面发送心跳/同步请求的频率。 |
| `NRE_DDNS_IP_PROBE_INTERVAL` | `5m` | 探测 DDNS 公网 IP 的最小间隔；实际探测在心跳同步时触发。 |
| `NRE_HTTP3_ENABLED` | `false` | 启用 HTTP/3（QUIC）作为入站协议。 |
| `NRE_TRAFFIC_STATS_ENABLED` | `true` | 在 Agent 端启用流量采集。 |
| `NRE_PPROF_ADDR` | 空 | pprof 调试端点的地址。需要调试构建。 |
