# 部署

Docker Compose 是推荐的部署方式。默认 Compose 会拉起一个纯 Go 控制面容器，并启用内嵌的 `local` agent，不需要另外安装 Nginx。

## 一键准备目录

```bash
mkdir -p nginx-reverse-emby && cd nginx-reverse-emby
curl -O https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/docker-compose.yaml
mkdir -p data
```

## 必要环境变量

启动前编辑 `docker-compose.yaml`，至少修改：

```yaml
environment:
  API_TOKEN: your-secure-token
  MASTER_REGISTER_TOKEN: your-register-token
  NRE_TIMEZONE: Asia/Shanghai
```

| 变量 | 必填 | 说明 |
| --- | --- | --- |
| `API_TOKEN` | 是 | 面板访问令牌 / API 凭证。请使用强随机值。 |
| `MASTER_REGISTER_TOKEN` | 否 | Agent 注册令牌。不设置时回退到 `API_TOKEN`；只用 `local` 节点时可忽略。 |
| `NRE_TIMEZONE` | 否 | 面板时区，影响日 / 月汇总和流量计费周期边界，默认 `UTC`。 |

完整变量清单见 [环境变量](../reference/environment.md)。

## 数据目录

Compose 把面板状态、SQLite 数据库、证书材料和运行时文件持久化到宿主机的 `./data`，对应容器内：

```text
/opt/nginx-reverse-emby/panel/data
```

::: warning 不要提交 data 目录
`./data` 中可能包含节点 token、证书私钥等敏感材料，不要提交到代码仓库，也不要在不受信任的环境间随意传输。
:::

## 启动与查看状态

```bash
docker compose up -d
docker compose ps
docker compose logs -f
```

面板默认监听：

```text
http://<服务器 IP>:8080
```

健康检查端点：

```text
http://<服务器 IP>:8080/panel-api/health
```

## Host 网络模式

默认 Compose 使用 `network_mode: host`。原因是 HTTP、L4、Relay 监听端口都是控制面在运行时动态创建的，Docker bridge 网络无法在容器启动后自动发布新增端口映射，host 网络让 `local` agent 直接绑定宿主机端口。

这也意味着：你在规则里填写的监听端口会直接占用宿主机端口。请提前确认端口未被其他程序占用，并在防火墙放行。

## 切换数据库

默认使用 SQLite，无需额外配置。如需 PostgreSQL 或 MySQL：

```yaml
environment:
  NRE_DATABASE_DRIVER: postgres
  NRE_DATABASE_DSN: "postgres://nre:nre@postgres:5432/nre?sslmode=disable"
```

```yaml
environment:
  NRE_DATABASE_DRIVER: mysql
  NRE_DATABASE_DSN: "nre:nre@tcp(mysql:3306)/nre?parseTime=true&charset=utf8mb4"
```

从已有 SQLite 迁移到外部数据库，使用 `migrate-storage` 命令，见 [迁移](../operations/migration.md)。

## 关闭可选模块

如果不需要 WireGuard 或流量统计，可以关掉以降低资源占用：

```yaml
environment:
  NRE_WIREGUARD_ENABLED: "false"
  NRE_TRAFFIC_STATS_ENABLED: "false"
```

## 下一步

- 添加规则：[HTTP 反向代理](./http-rule.md)、[L4 规则与 Relay](./l4-relay.md)。
- 启用 HTTPS：[证书与 HTTPS](./certificates.md)。
- 接入远程节点：[Agent 接入](./agent.md)。
