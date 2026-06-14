# 部署指南

本章介绍如何在你的服务器上安装并运行 nginx-reverse-emby。我们使用 **Docker Compose** 作为推荐部署方式——它会自动拉起面板容器，并内置一个 `local` agent，你**不需要**额外安装 Nginx。

## 准备工作：创建目录并下载配置文件

首先，在你的服务器上创建一个目录，下载官方提供的 `docker-compose.yaml`，并创建一个用于存放数据的文件夹：

```bash
mkdir -p nginx-reverse-emby && cd nginx-reverse-emby
curl -O https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/docker-compose.yaml
mkdir -p data
```

执行完这三行命令后，你的目录结构应该是这样：

```text
nginx-reverse-emby/
├── docker-compose.yaml    # 容器编排文件
└── data/                  # 数据持久化目录（后续会存放数据库、证书等）
```

## 配置必要的环境变量

在启动之前，你需要编辑 `docker-compose.yaml`，设置几个关键的环境变量。打开文件，找到 `environment` 部分，至少修改以下三项：

```yaml
environment:
  API_TOKEN: your-secure-token           # 换成你自己的强密码
  MASTER_REGISTER_TOKEN: your-register-token  # 节点注册用的令牌
  NRE_TIMEZONE: Asia/Shanghai            # 时区，建议改成你所在地的时区
```

下面是每个变量的详细说明：

| 变量 | 是否必填 | 说明 |
| --- | --- | --- |
| `API_TOKEN` | **是** | 这是登录 Web 面板和调用 API 的密码。请使用随机生成的强密码（例如 32 位以上、包含大小写字母和数字），**不要**用简单密码。 |
| `MASTER_REGISTER_TOKEN` | 否 | 其他服务器（Agent）接入面板时需要用这个令牌验证身份。如果你只在一台服务器上使用，可以暂时不设置，它会自动回退成 `API_TOKEN` 的值。 |
| `NRE_TIMEZONE` | 否 | 面板的时区设置，影响每日/每月流量统计和计费周期的分界点。默认是 `UTC`，国内用户建议改成 `Asia/Shanghai`。 |

如果你想了解更多可配置项，可以查看 [环境变量参考文档](../reference/environment.md)。

## 数据目录说明

Compose 会把面板的状态、SQLite 数据库、证书文件以及运行时数据都保存在宿主机的 `./data` 目录中。在容器内部，这个目录对应的路径是：

```text
/opt/nginx-reverse-emby/panel/data
```

::: warning 注意：不要把这个目录提交到 Git 仓库
`./data` 里可能存放着节点认证令牌、证书私钥等敏感信息。请**不要**把它提交到代码仓库，也不要在不安全的网络环境中随意传输这个文件夹。
:::

## 启动服务并查看运行状态

配置完成后，用以下命令启动服务：

```bash
# 启动容器（-d 表示后台运行）
docker compose up -d

# 查看容器运行状态
docker compose ps

# 实时查看日志（按 Ctrl+C 退出）
docker compose logs -f
```

如果一切正常，你可以通过浏览器访问面板：

```text
http://<你的服务器 IP>:8080
```

系统还提供了一个健康检查接口，方便你确认服务是否正常运行：

```text
http://<你的服务器 IP>:8080/panel-api/health
```

## 为什么使用 Host 网络模式？

默认的 `docker-compose.yaml` 使用了 `network_mode: host`（即宿主机的网络模式）。这里解释一下原因：

nginx-reverse-emby 的监听端口（HTTP、L4、Relay）是**动态创建**的——你在 Web 面板里添加一条规则，系统就会立刻去监听对应的端口。如果使用 Docker 默认的 bridge 网络，容器启动后无法再动态映射新的端口到宿主机。而 host 模式让容器直接共享宿主机的网络栈，所以 `local` agent 可以直接绑定宿主机的端口。

**这意味着：** 你在规则里填写的端口号会直接占用宿主机的端口。请提前确认这些端口没有被其他程序（比如系统自带的 Nginx、Apache）占用，同时记得在防火墙/安全组中放行这些端口。

## 切换数据库（可选）

默认使用 **SQLite**，开箱即用，不需要额外配置。如果你需要把数据存到 PostgreSQL 或 MySQL 中，可以这样配置：

### PostgreSQL

```yaml
environment:
  NRE_DATABASE_DRIVER: postgres
  NRE_DATABASE_DSN: "postgres://nre:nre@postgres:5432/nre?sslmode=disable"
```

### MySQL

```yaml
environment:
  NRE_DATABASE_DRIVER: mysql
  NRE_DATABASE_DSN: "nre:nre@tcp(mysql:3306)/nre?parseTime=true&charset=utf8mb4"
```

如果你已经用 SQLite 运行了一段时间，想把数据迁移到外部数据库，可以使用 `migrate-storage` 命令。具体操作请参考 [迁移文档](../operations/migration.md)。

## 关闭不需要的模块（可选）

如果你不需要 WireGuard 功能或流量统计，可以关掉它们来节省系统资源：

```yaml
environment:
  NRE_WIREGUARD_ENABLED: "false"
  NRE_TRAFFIC_STATS_ENABLED: "false"
```

## 下一步

服务跑起来之后，你可以继续学习：

- **添加代理规则**：[HTTP 反向代理](./http-rule.md) | [L4 规则与 Relay](./l4-relay.md)
- **启用 HTTPS**：[证书与 HTTPS](./certificates.md)
- **接入其他服务器**：[Agent 接入](./agent.md)
