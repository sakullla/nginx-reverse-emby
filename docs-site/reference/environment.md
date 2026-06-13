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
| `NRE_DATABASE_DRIVER` | `sqlite` | 数据库驱动：`sqlite`、`postgres` 或 `mysql`。 |
| `NRE_DATABASE_DSN` | 空 | 数据库 DSN。SQLite 默认使用 `NRE_DATA_DIR/panel.db`。 |
| `NRE_TIMEZONE` | `UTC` | 面板聚合周期边界使用的 IANA 时区。 |

## Agent

| 变量 | 说明 |
| --- | --- |
| `NRE_AGENT_ID` | Agent 标识。 |
| `NRE_AGENT_NAME` | Agent 显示名称。 |
| `NRE_AGENT_TOKEN` | Agent 认证令牌。 |
| `NRE_MASTER_URL` | Master 控制面 URL。 |
| `NRE_DATA_DIR` | Agent 数据目录。 |
