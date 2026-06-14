# 部署指南

推荐用 Docker Compose 部署，它会自动拉起控制面容器，并内置一个 `local` Agent。你不需要额外安装 Nginx。

## 创建目录和下载配置

```bash
mkdir -p nginx-reverse-emby && cd nginx-reverse-emby
curl -O https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/docker-compose.yaml
mkdir -p data
```

最终目录结构：

```text
nginx-reverse-emby/
├── docker-compose.yaml
└── data/                  # 数据持久化目录
```

## 配置环境变量

编辑 `docker-compose.yaml` 的 `environment` 部分：

```yaml
environment:
  API_TOKEN: your-secure-token           # 必填，面板登录密码
  MASTER_REGISTER_TOKEN: your-register-token  # Agent 注册令牌
  NRE_TIMEZONE: Asia/Shanghai            # 时区，国内用户建议填这个
```

| 变量 | 必填 | 说明 |
| --- | --- | --- |
| `API_TOKEN` | 是 | 登录面板和调用 API 的密码。用 32 位以上随机字符串，包含大小写字母和数字 |
| `MASTER_REGISTER_TOKEN` | 否 | 远程 Agent 注册时用的令牌。不上多台机器可以不填，会自动回退到 `API_TOKEN` |
| `NRE_TIMEZONE` | 否 | 面板时区，影响流量统计和计费周期的分界点。默认 UTC |

更多配置项见 [环境变量速查](../reference/environment-variables.md)。

## 启动服务

```bash
docker compose up -d      # 启动
docker compose ps          # 看运行状态
docker compose logs -f     # 实时日志（Ctrl+C 退出）
```

浏览器访问 `http://<VPS IP>:8080`，输入 `API_TOKEN` 登录。

健康检查接口：`http://<VPS IP>:8080/panel-api/health`

## 为什么用 host 网络？

默认 Compose 文件用 `network_mode: host`。原因是：你在面板里创建规则后，Agent 需要动态监听对应的端口。Docker bridge 网络在容器启动后无法再动态映射新端口。host 模式让容器直接共享宿主机网络栈，端口管理完全灵活。

::: warning
规则里填的端口会直接占用宿主机端口。确认这些端口没被其他程序占用，并在防火墙中放行。
:::

## 数据目录

Compose 把所有数据保存在宿主机的 `./data` 目录（容器内路径是 `/opt/nginx-reverse-emby/panel/data`）。

::: warning 不要提交 data 目录
`./data` 包含 Agent Token、证书私钥等敏感信息。不要提交到 Git 仓库，不要上传到公共网盘。
:::

## 切换数据库（可选）

默认 SQLite，零配置开箱即用。需要 PostgreSQL 或 MySQL 时：

**PostgreSQL：**

```yaml
environment:
  NRE_DATABASE_DRIVER: postgres
  NRE_DATABASE_DSN: "postgres://nre:nre@postgres:5432/nre?sslmode=disable"
```

**MySQL：**

```yaml
environment:
  NRE_DATABASE_DRIVER: mysql
  NRE_DATABASE_DSN: "nre:nre@tcp(mysql:3306)/nre?parseTime=true&charset=utf8mb4"
```

已有 SQLite 数据需要迁移？见 [数据迁移](../operations/migration.md)。

## 关闭不需要的模块（可选）

```yaml
environment:
  NRE_WIREGUARD_ENABLED: "false"
  NRE_TRAFFIC_STATS_ENABLED: "false"
```

## 下一步

- [HTTP 反向代理](../guides/http-rules.md)
- [L4 端口转发](../guides/l4-rules.md)
- [证书与 HTTPS](../guides/certificates.md)
- [Agent 节点管理](../guides/agents.md)
