# 环境变量

本页列出控制面和 Agent 使用的核心配置变量。时间类变量使用 Go duration 格式，例如 `500ms`、`5s`、`2m`。

同名变量存在多个别名时，按列出的先后顺序生效（前者优先）。大多数 `NRE_*` 变量都有等价的 legacy 别名，方便旧配置继续工作。

## 控制面 / 通用

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `NRE_PANEL_TOKEN`（别名 `API_TOKEN`） | 必填 | 面板登录令牌和 API 凭证。 |
| `NRE_REGISTER_TOKEN`（别名 `MASTER_REGISTER_TOKEN`、`PANEL_REGISTER_TOKEN`、`API_TOKEN`） | 回退到 `NRE_PANEL_TOKEN` | Agent 注册时使用的令牌。 |
| `NRE_CONTROL_PLANE_ADDR`（别名 `PANEL_BACKEND_HOST` + `PANEL_BACKEND_PORT`） | `0.0.0.0:8080` | 控制面监听地址。 |
| `NRE_CONTROL_PLANE_DATA_DIR`（别名 `PANEL_DATA_ROOT`） | `/opt/nginx-reverse-emby/panel/data` | 数据目录。 |
| `NRE_FRONTEND_DIST_DIR`（别名 `PANEL_FRONTEND_DIST_DIR`） | `/opt/nginx-reverse-emby/panel/frontend/dist` | 前端构建产物目录。 |
| `NRE_PUBLIC_AGENT_ASSETS_DIR`（别名 `PANEL_PUBLIC_AGENT_ASSETS_DIR`） | `/opt/.../public/agent-assets` | 公开 Agent 资产目录。 |
| `NRE_ENABLE_LOCAL_AGENT`（别名 `MASTER_LOCAL_AGENT_ENABLED`） | `true` | 是否启用内嵌 local agent。 |
| `NRE_LOCAL_AGENT_ID`（别名 `MASTER_LOCAL_AGENT_ID`） | `local` | 内嵌 local agent 的标识。 |
| `NRE_LOCAL_AGENT_NAME`（别名 `MASTER_LOCAL_AGENT_NAME`） | `local` | 内嵌 local agent 的显示名称。 |
| `NRE_TIMEZONE` | `UTC` | 面板时区（IANA），用于日 / 月汇总和流量计费周期边界。 |
| `NRE_HEARTBEAT_INTERVAL` | `30s` | 控制面侧心跳间隔口径（Agent 自身默认 `10s`，见下表）。 |
| `NRE_PROJECT_URL` | 空 | 项目主页 URL，用于版本信息展示。 |

## 数据库

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `NRE_DATABASE_DRIVER` | `sqlite` | 数据库驱动：`sqlite`、`postgres`、`mysql`。 |
| `NRE_DATABASE_DSN` | 空 | 数据库 DSN。SQLite 未设置时默认使用 `NRE_CONTROL_PLANE_DATA_DIR/panel.db`。 |

PostgreSQL：

```ini
NRE_DATABASE_DRIVER=postgres
NRE_DATABASE_DSN=postgres://nre:nre@postgres:5432/nre?sslmode=disable
```

MySQL：

```ini
NRE_DATABASE_DRIVER=mysql
NRE_DATABASE_DSN=nre:nre@tcp(mysql:3306)/nre?parseTime=true&charset=utf8mb4
```

## 证书 / ACME

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `ACME_DNS_PROVIDER` | 空 | DNS 验证提供商，目前支持 `cf`（Cloudflare）。设为 `cf` 且 Token 非空时启用 DNS-01。 |
| `CLOUDFLARE_DNS_API_TOKEN`（别名 `CF_DNS_API_TOKEN`、`CF_TOKEN`、`CF_Token`） | 空 | Cloudflare API Token。 |
| `CLOUDFLARE_ZONE_API_TOKEN`（别名 `CF_ZONE_API_TOKEN`） | 空 | 可选的 Cloudflare Zone Token。 |
| `NRE_ACME_EMAIL` | 空 | ACME 账户注册邮箱。 |
| `NRE_ACME_DIRECTORY_URL` | Let's Encrypt 生产目录 | ACME 目录 URL。 |
| `NRE_MANAGED_CERT_RENEW_INTERVAL` | `24h` | 托管证书续期检查间隔。 |

DNS-01 仅在 `ACME_DNS_PROVIDER=cf` 且 Token 非空时启用，详见 [证书与 HTTPS](../guide/certificates.md)。

## HTTP 传输（控制面与 Agent 共享）

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `NRE_HTTP_DIAL_TIMEOUT` | `30s` | HTTP upstream 连接建立超时。 |
| `NRE_HTTP_TLS_HANDSHAKE_TIMEOUT` | `10s` | HTTPS upstream TLS 握手超时。 |
| `NRE_HTTP_RESPONSE_HEADER_TIMEOUT` | `30s` | 等待 upstream 响应头超时。 |
| `NRE_HTTP_IDLE_CONN_TIMEOUT` | `90s` | upstream 空闲连接回收超时。 |
| `NRE_HTTP_KEEP_ALIVE` | `30s` | upstream TCP keepalive 间隔。 |
| `NRE_HTTP_MAX_CONNS_PER_HOST` | `64` | 每个 upstream 主机最大并发连接数（Agent 侧）。 |
| `NRE_HTTP_STREAM_RESUME_ENABLED` | `true` | 是否启用中断流恢复。 |
| `NRE_HTTP_STREAM_RESUME_MAX_ATTEMPTS` | `2` | 单次请求最多追加恢复次数。 |
| `NRE_HTTP_SAME_BACKEND_RETRY_ATTEMPTS` | `1` | 同一 backend 额外重试次数，仅对 retry-safe 方法生效。 |
| `NRE_BACKEND_FAILURE_BACKOFF_BASE` | `1s` | backend 连续失败退避起始值（须 ≤ limit）。 |
| `NRE_BACKEND_FAILURE_BACKOFF_LIMIT` | `15s` | backend 连续失败退避上限。 |

## Relay

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `NRE_RELAY_DIAL_TIMEOUT` | `5s` | Relay 上游拨号超时。 |
| `NRE_RELAY_HANDSHAKE_TIMEOUT` | `5s` | Relay 握手超时。 |
| `NRE_RELAY_FRAME_TIMEOUT` | `5s` | Relay 单帧读写超时。 |
| `NRE_RELAY_IDLE_TIMEOUT` | `2m` | Relay 空闲连接超时。 |

## WireGuard

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `NRE_WIREGUARD_ENABLED` | `true` | 是否启用 WireGuard 模块和 API。 |
| `NRE_WIREGUARD_AUTO_ADDRESS_POOLS` | `10.8.x.1/24,fd10:8:x::1/64` | WireGuard Profile 自动地址池，`x` 替换为序号。 |

## 流量统计

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `NRE_TRAFFIC_STATS_ENABLED` | `true` | 是否启用流量统计和额度阻断相关持久化。 |
| `NRE_TRAFFIC_CLEANUP_INTERVAL` | `24h` | 流量历史主动清理周期，设为 `0` / `off` / `disabled` 可关闭。 |
| `NRE_TRAFFIC_INTERFACES` | 空 | 网卡采集白名单，逗号分隔，留空表示全部。 |

流量额度模型见 [流量统计与额度](./traffic.md)。

## Agent

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `NRE_AGENT_ID` | `linux-agent` | Agent 标识。 |
| `NRE_AGENT_NAME` | `linux-agent` | Agent 显示名称。 |
| `NRE_AGENT_TOKEN` | 必填 | Agent 心跳认证令牌（加入时生成）。 |
| `NRE_AGENT_VERSION` | `0.0.0` | Agent 当前版本，用于自更新比对。 |
| `NRE_MASTER_URL` | 必填 | Master 控制面 URL。 |
| `NRE_DATA_DIR` | `/var/lib/nre-agent` | Agent 数据目录。 |
| `NRE_HEARTBEAT_INTERVAL` | `10s` | Agent 心跳同步间隔。 |
| `NRE_HTTP3_ENABLED` | `false` | 是否启用 HTTP/3 入口。 |
| `NRE_TRAFFIC_STATS_ENABLED` | `true` | Agent 侧是否启用流量采集。 |
| `NRE_WIREGUARD_ENABLED` | `true` | Agent 侧是否启用 WireGuard。 |
| `NRE_PPROF_ADDR` | 空 | pprof 监听地址，需使用 debug tags 构建。 |
