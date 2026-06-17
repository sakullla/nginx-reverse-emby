# 部署指南

推荐用 Docker Compose 部署，它会自动拉起控制面容器，并内置一个 `local` Agent。你不需要额外安装 Nginx。

## 新手一键部署

在 VPS 上执行：

```bash
curl -fsSL https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/scripts/deploy-compose.sh | sh
```

脚本会创建 `nginx-reverse-emby/` 目录，下载 `docker-compose.yaml`，生成 `.env` 随机 token，创建 `data/` 并启动服务。如果系统还没有 Docker Compose，脚本会询问后自动安装。

脚本会优先推荐你配置域名和 Cloudflare API Token：

- 已有域名且 DNS 已指向 VPS：输入面板域名，脚本会写入 `NRE_PUBLIC_URL=https://面板域名`，并自动创建 `https://面板域名 -> http://127.0.0.1:8080` 的面板自代理规则。
- 使用 Cloudflare：建议创建 API Token，不要使用 Global API Key；权限给 `Zone:Read` 和 `DNS:Edit`，Zone Resources 只选择你的域名。
- 暂时没有域名：脚本会提示 HTTP 风险，并生成 `NRE_PANEL_PUBLIC_PATH=/panel-随机字符串` 作为临时面板入口。随机路径不能替代 token 和 HTTPS，只适合临时部署。

可选参数：

```bash
curl -fsSL https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/scripts/deploy-compose.sh | sh -s -- \
  --dir nginx-reverse-emby \
  --public-url https://panel.example.com
```

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
  API_TOKEN: ${API_TOKEN:?set API_TOKEN to a random 32+ character token}
  MASTER_REGISTER_TOKEN: ${MASTER_REGISTER_TOKEN:?set MASTER_REGISTER_TOKEN to a random 32+ character token}
  NRE_TIMEZONE: Asia/Shanghai            # 时区，国内用户建议填这个
  NRE_PANEL_PUBLIC_PATH: /panel-a1b2c3d4  # 无域名 HTTP 临时部署时可选
```

| 变量 | 必填 | 说明 |
| --- | --- | --- |
| `API_TOKEN` | 是 | 登录面板和调用 API 的密码。用 32 位以上随机字符串，包含大小写字母和数字 |
| `MASTER_REGISTER_TOKEN` | 否 | 远程 Agent 注册时用的令牌。不上多台机器可以不填，会自动回退到 `API_TOKEN` |
| `NRE_TIMEZONE` | 否 | 面板时区，影响流量统计和计费周期的分界点。默认 UTC |
| `NRE_PANEL_PUBLIC_PATH` | 否 | 无域名 HTTP 临时部署时的随机面板入口路径，例如 `/panel-a1b2c3d4` |

更多配置项见 [环境变量速查](../reference/environment-variables.md)。

## 启动服务

```bash
docker compose up -d      # 启动
docker compose ps          # 看运行状态
docker compose logs -f     # 实时日志（Ctrl+C 退出）
```

默认 Compose 只监听 `127.0.0.1:8080`。首次登录从本机开一条 SSH 隧道：

```bash
ssh -L 8080:127.0.0.1:8080 root@<VPS IP>
```

然后浏览器访问 `http://127.0.0.1:8080`，输入 `API_TOKEN` 登录。

健康检查接口：`http://127.0.0.1:8080/panel-api/health`

## 给面板自身启用 HTTPS

面板可以给自己提供 HTTPS。首次通过 SSH 隧道登录后，在 **流量管理 → HTTP 规则** 添加一条规则：

| 字段 | 示例 |
| --- | --- |
| Agent | `local` |
| 入口域名 | `https://panel.example.com` |
| 后端地址 | `http://127.0.0.1:8080` |
| 启用规则 | 开 |

确认 DNS 已把 `panel.example.com` 指向 VPS，且防火墙放行 80/443。规则同步后，local Agent 会申请证书并把公网 HTTPS 流量转回本机控制面。

完成后建议在 `docker-compose.yaml` 中设置：

```yaml
environment:
  NRE_PUBLIC_URL: https://panel.example.com
```

这样 join script 和 Agent 更新包 URL 会使用 HTTPS 面板地址。只有在你额外使用上游反代，并且上游会清洗并重写 `X-Forwarded-*` 头时，才开启 `NRE_TRUST_FORWARDED_HEADERS: "true"`。

如果你配置了 Cloudflare API Token：

```yaml
environment:
  ACME_DNS_PROVIDER: cf
  CF_TOKEN: <Cloudflare API Token>
```

创建 HTTPS 规则时会优先使用 DNS-01 申请证书，不要求 80 端口先能完成 HTTP-01 校验。Token 建议只授予 `Zone:Read` 和 `DNS:Edit`，并限制到当前域名。

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
