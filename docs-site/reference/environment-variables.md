# 环境变量速查

控制面和 Agent 通过环境变量配置。时间值使用 Go duration 格式（`500ms`、`5s`、`2m`）。大多数 `NRE_*` 变量有旧版别名，旧配置可以继续使用。

## 快速索引

| 你想做的事 | 对应变量 |
|---|---|
| 设置面板密码 | `API_TOKEN`（必填） |
| 设置时区 | `NRE_TIMEZONE`（国内填 `Asia/Shanghai`） |
| 切换数据库 | `NRE_DATABASE_DRIVER` + `NRE_DATABASE_DSN` |
| 开启 Cloudflare DNS 验证 | `ACME_DNS_PROVIDER=cf` + `CF_TOKEN` |
| 关闭 WireGuard | `NRE_WIREGUARD_ENABLED=false` |
| 关闭流量统计 | `NRE_TRAFFIC_STATS_ENABLED=false` |
| Agent 注册令牌 | `MASTER_REGISTER_TOKEN` |

---

## 控制面

| 变量 | 默认值 | 作用 |
|------|--------|------|
| `NRE_PANEL_TOKEN`（别名 `API_TOKEN`） | **必填** | 网页界面和 API 认证的登录令牌。 |
| `NRE_REGISTER_TOKEN`（别名 `MASTER_REGISTER_TOKEN`、`PANEL_REGISTER_TOKEN`、`API_TOKEN`） | 未设置时依次尝试 `MASTER_REGISTER_TOKEN`、`PANEL_REGISTER_TOKEN`、`API_TOKEN` | Agent 向控制面注册时使用的令牌。 |
| `NRE_CONTROL_PLANE_ADDR`（别名 `PANEL_BACKEND_HOST` + `PANEL_BACKEND_PORT`） | `0.0.0.0:8080` | 控制面监听的地址。 |
| `NRE_CONTROL_PLANE_DATA_DIR`（别名 `PANEL_DATA_ROOT`） | `/opt/nginx-reverse-emby/panel/data` | SQLite 数据库和运行时数据的目录。 |
| `NRE_FRONTEND_DIST_DIR`（别名 `PANEL_FRONTEND_DIST_DIR`） | `/opt/nginx-reverse-emby/panel/frontend/dist` | 存放构建好的前端文件的目录。 |
| `NRE_PUBLIC_AGENT_ASSETS_DIR`（别名 `PANEL_PUBLIC_AGENT_ASSETS_DIR`） | `/opt/.../public/agent-assets` | 公共代理资源的目录（加入脚本、二进制文件）。 |
| `NRE_ENABLE_LOCAL_AGENT`（别名 `MASTER_LOCAL_AGENT_ENABLED`） | `true` | 是否在控制面节点上运行内置的 `local` Agent。 |
| `NRE_LOCAL_AGENT_ID`（别名 `MASTER_LOCAL_AGENT_ID`） | `local` | 内置本地代理的标识符。 |
| `NRE_LOCAL_AGENT_NAME`（别名 `MASTER_LOCAL_AGENT_NAME`） | `local` | 内置本地代理的显示名称。 |
| `NRE_TIMEZONE` | `UTC` | 面板使用的时区（IANA 格式），用于每日/每月流量汇总和计费周期边界。 |
| `NRE_HEARTBEAT_INTERVAL` | `30s` | 从控制面角度的心跳间隔。（Agent 默认是 `10s`；见下面的 Agent 部分。） |
| `NRE_PROJECT_URL` | 空 | 项目主页 URL，显示在版本信息中。 |

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
| `ACME_DNS_PROVIDER` | 空 | DNS 验证提供商。目前支持 `cf`（Cloudflare）。设置为 `cf` 并提供有效令牌以启用 DNS-01。 |
| `CLOUDFLARE_DNS_API_TOKEN`（别名 `CF_DNS_API_TOKEN`、`CF_TOKEN`、`CF_Token`） | 空 | 用于 DNS-01 验证的 Cloudflare API 令牌。 |
| `CLOUDFLARE_ZONE_API_TOKEN`（别名 `CF_ZONE_API_TOKEN`） | 空 | 可选的 Cloudflare 区域令牌。 |
| `NRE_ACME_EMAIL` | 空 | ACME 账户注册用的电子邮件地址。 |
| `NRE_ACME_DIRECTORY_URL` | Let's Encrypt 生产环境 | ACME 目录 URL。 |
| `NRE_MANAGED_CERT_RENEW_INTERVAL` | `24h` | 检查证书续期的频率。 |

只有当 `ACME_DNS_PROVIDER=cf` **且** 令牌非空时，DNS-01 才会启用。详情请参阅 [证书与 HTTPS](../guides/certificates.md)。

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

## WireGuard

| 变量 | 默认值 | 作用 |
|------|--------|------|
| `NRE_WIREGUARD_ENABLED` | `true` | 启用或禁用 WireGuard 模块和 API。 |
| `NRE_WIREGUARD_AUTO_ADDRESS_POOLS` | `10.8.x.1/24,fd10:8:x::1/64` | WireGuard 配置文件的自动 IP 地址池。`x` 被替换为顺序编号。 |

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
| `NRE_HTTP3_ENABLED` | `false` | 启用 HTTP/3（QUIC）作为入站协议。 |
| `NRE_TRAFFIC_STATS_ENABLED` | `true` | 在 Agent 端启用流量采集。 |
| `NRE_WIREGUARD_ENABLED` | `true` | 在 Agent 端启用 WireGuard。 |
| `NRE_PPROF_ADDR` | 空 | pprof 调试端点的地址。需要调试构建。 |
