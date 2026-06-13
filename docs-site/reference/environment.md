# 环境变量

本页列出控制面和 Agent 使用的核心配置变量。

## 控制面

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `API_TOKEN` | 必填 | 面板登录令牌和 API 凭证。 |
| `MASTER_REGISTER_TOKEN` | 与 `API_TOKEN` 相同 | Agent 注册时使用的令牌。 |
| `PANEL_BACKEND_HOST` | `0.0.0.0` | 控制面监听地址。 |
| `PANEL_BACKEND_PORT` | `8080` | 控制面监听端口。 |
| `NRE_ENABLE_LOCAL_AGENT` | `1` | 是否启用内嵌 local agent。 |
| `NRE_LOCAL_AGENT_ID` | `local` | 内嵌 local agent 的标识。 |
| `NRE_LOCAL_AGENT_NAME` | `local` | 内嵌 local agent 的显示名称。 |
| `NRE_DATABASE_DRIVER` | `sqlite` | 数据库驱动：`sqlite`、`postgres` 或 `mysql`。 |
| `NRE_DATABASE_DSN` | 空 | 数据库 DSN。SQLite 默认使用 `NRE_DATA_DIR/panel.db`。 |
| `NRE_TIMEZONE` | `UTC` | 面板聚合周期边界使用的 IANA 时区。 |
| `NRE_HEARTBEAT_INTERVAL` | `30s` | Agent 心跳同步间隔。 |
| `NRE_TRAFFIC_STATS_ENABLED` | `true` | 是否启用流量统计和额度阻断相关持久化。 |
| `NRE_WIREGUARD_ENABLED` | `true` | 是否启用 WireGuard 模块和 API。 |
| `NRE_TRAFFIC_INTERFACES` | 空 | 网卡采集白名单，逗号分隔。 |
| `NRE_TRAFFIC_CLEANUP_INTERVAL` | `24h` | 流量历史主动清理周期，设为 `0`、`off` 或 `disabled` 可关闭。 |
| `NRE_MANAGED_CERT_RENEW_INTERVAL` | `24h` | 托管证书续期检查间隔。 |
| `ACME_DNS_PROVIDER` | 空 | DNS 验证提供商，例如 `cf`。 |
| `CF_Token` / `CF_TOKEN` | 空 | Cloudflare API Token。 |

时间类变量使用 Go duration 格式，例如 `500ms`、`5s`、`2m`。

## HTTP 传输

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `NRE_HTTP_DIAL_TIMEOUT` | `30s` | HTTP upstream 连接建立超时。 |
| `NRE_HTTP_TLS_HANDSHAKE_TIMEOUT` | `10s` | HTTPS upstream TLS 握手超时。 |
| `NRE_HTTP_RESPONSE_HEADER_TIMEOUT` | `30s` | 等待 upstream 响应头超时。 |
| `NRE_HTTP_IDLE_CONN_TIMEOUT` | `90s` | upstream 空闲连接回收超时。 |
| `NRE_HTTP_KEEP_ALIVE` | `30s` | upstream TCP keepalive 间隔。 |
| `NRE_HTTP_MAX_CONNS_PER_HOST` | `64` | 每个 upstream 主机最大并发连接数。 |
| `NRE_HTTP_STREAM_RESUME_ENABLED` | `true` | 是否启用中断流恢复。 |
| `NRE_HTTP_STREAM_RESUME_MAX_ATTEMPTS` | `2` | 单次请求最多追加恢复次数。 |
| `NRE_HTTP_SAME_BACKEND_RETRY_ATTEMPTS` | `1` | 同一 backend 额外重试次数，仅对 retry-safe 方法生效。 |
| `NRE_BACKEND_FAILURE_BACKOFF_BASE` | `1s` | backend 连续失败退避起始值。 |
| `NRE_BACKEND_FAILURE_BACKOFF_LIMIT` | `15s` | backend 连续失败退避上限。 |

## Agent

| 变量 | 说明 |
| --- | --- |
| `NRE_AGENT_ID` | Agent 标识。 |
| `NRE_AGENT_NAME` | Agent 显示名称。 |
| `NRE_AGENT_TOKEN` | Agent 认证令牌。 |
| `NRE_MASTER_URL` | Master 控制面 URL。 |
| `NRE_DATA_DIR` | Agent 数据目录。 |
| `NRE_HTTP3_ENABLED` | 是否启用 HTTP/3 入口。 |

## 数据库示例

```ini
NRE_DATABASE_DRIVER=mysql
NRE_DATABASE_DSN=nre:nre@tcp(mysql:3306)/nre?parseTime=true&charset=utf8mb4
```

```ini
NRE_DATABASE_DRIVER=sqlite
NRE_DATABASE_DSN=/opt/nginx-reverse-emby/panel/data/panel.db
```

## 流量统计

流量统计以 Agent 所在主机或网络命名空间的网卡累计计数作为周期总量和额度阻断口径。HTTP 规则、L4 规则和 Relay 监听器的代理侧统计保留为分项分析。

Linux Agent 优先通过 netlink 读取内核网卡计数，失败时回退到 `/proc/net/dev`。如果 Agent 运行在 Docker bridge 网络中，采到的是容器网络命名空间流量；需要 VPS 主机网卡总量时，建议使用 host network 或主机部署，并用 `NRE_TRAFFIC_INTERFACES` 限定计入网卡。

如不需要流量统计或希望降低数据库写入量：

```ini
NRE_TRAFFIC_STATS_ENABLED=false
```

服务端会持久化 raw cursor、小时桶、日汇总、月汇总、节点策略、校准基线和事件。系统重启、网卡重建或计数回退时，会记录 counter reset，并把当前值作为新基线，避免产生负数或误扣历史周期。
