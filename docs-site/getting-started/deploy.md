# 部署指南

推荐用 Docker Compose 部署，它会自动拉起控制面容器，并内置一个 `local` Agent。你不需要额外安装 Nginx。

## 新手一键部署

在 VPS 上执行：

```bash
curl -fsSL https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/scripts/deploy-compose.sh | sh
```

脚本会创建 `nginx-reverse-emby/` 目录，下载 `docker-compose.yaml`，生成 `.env` 随机 token，创建 `data/` 并启动服务。如果系统还没有 Docker Compose，脚本会询问后自动安装。

交互尽量压成两步：

1. **面板域名**：DNS 已指向本机则填入；直接回车 = 临时 HTTP（生成随机 `NRE_PANEL_PUBLIC_PATH`）。
2. **Cloudflare API Token**（可选）：粘贴后启用 DNS-01 并在线校验；直接回车跳过，改用 HTTP-01（需 80/443 公网可达）。

有域名时脚本会写入 `NRE_PUBLIC_URL=https://面板域名`，并尽量创建 `https://面板域名 -> http://127.0.0.1:8080` 的自代理规则。Cloudflare Token 权限需要：`区域 / 区域 / 读取`、`区域 / DNS / 读取`、`区域 / DNS / 编辑`；不要用 Global API Key。随机路径不能替代 token 和 HTTPS，只适合临时部署。

可选参数：

```bash
curl -fsSL https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/scripts/deploy-compose.sh | sh -s -- \
  --dir nginx-reverse-emby \
  --public-url https://panel.example.com \
  --cf-token YOUR_CF_TOKEN \
  --yes
```

完整选项：`--dir`、`--image`、`--timezone`、`--public-url`、`--cf-token`、`--non-interactive`（关闭所有提问，用于 CI/cron）、`--yes`（跳过部署前确认）。等价环境变量：`API_TOKEN`、`MASTER_REGISTER_TOKEN`、`CF_TOKEN`、`ACME_DNS_PROVIDER`、`NRE_NONINTERACTIVE=1`。交互提问优先从 `/dev/tty` 读取，所以 `curl ... | sh` 仍能在 SSH 终端里正常弹出提问。

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
  PANEL_VAULT_MASTER_KEY: ${PANEL_VAULT_MASTER_KEY:-}
  PANEL_VAULT_KEY_ID: ${PANEL_VAULT_KEY_ID:-primary}
  NRE_TIMEZONE: Asia/Shanghai            # 时区，国内用户建议填这个
  NRE_PANEL_PUBLIC_PATH: /panel-a1b2c3d4  # 无域名 HTTP 临时部署时可选
  NRE_PKI_MASTER_KEY_FILE: /run/nre-pki/master.key  # 使用独立 PKI key 目录时可选
```

| 变量 | 必填 | 说明 |
| --- | --- | --- |
| `API_TOKEN` | 是 | 登录面板和调用 API 的访问令牌。用 32 位以上随机字符串，包含大小写字母和数字 |
| `MASTER_REGISTER_TOKEN` | 是 | 远程 Agent 注册时用的独立令牌。仓库附带的 Compose 强制要求设置，且不能与 `API_TOKEN` 相同 |
| `PANEL_VAULT_MASTER_KEY` | 否 | 通用 secret vault 的 32-byte envelope key；留空时从 `API_TOKEN` 确定性派生。已有 ciphertext 后不能单独更换 key 来源 |
| `PANEL_VAULT_KEY_ID` | 否 | 当前 vault key 的稳定 ID；Compose 默认 `primary`。轮换 key 时新旧 ID 必须不同 |
| `NRE_TIMEZONE` | 否 | 面板时区，影响流量统计和计费周期的分界点。Compose 默认 `Asia/Shanghai`；直接运行二进制默认 `UTC` |
| `NRE_PANEL_PUBLIC_PATH` | 否 | 无域名 HTTP 临时部署时的随机面板入口路径，例如 `/panel-a1b2c3d4` |
| `NRE_PKI_MASTER_KEY_FILE` | 否 | 内部 tunnel-PKI master key 的**容器内绝对路径**。使用时挂载私有且可写的父目录；受保护恢复需要在其中原子轮换 key。留空则使用 data 目录内的默认 key。 |

更多配置项见 [环境变量速查](../reference/environment-variables.md)。

手动部署建议先用 `openssl rand -hex 32` 生成并持久保存 `PANEL_VAULT_MASTER_KEY`。若使用 `.env` 保存 token 和 key，请执行 `chmod 600 .env`，并把实际 secret 配置纳入受控、加密的恢复材料。若现有部署使用从 `API_TOKEN` 派生的 key，迁移到显式 key 时必须同时设置新的 key/新 ID，以及 `PANEL_VAULT_PREVIOUS_API_TOKEN`/旧 ID；不能只填新 key。控制面成功启动并确认既有 secret 可读取后，才删除 `PANEL_VAULT_PREVIOUS_*`。完整变量见[环境变量速查](../reference/environment-variables.md#通用-secret-vault)。

外部 secret 的 Compose 示例：

```yaml
environment:
  NRE_PKI_MASTER_KEY_FILE: /run/nre-pki/master.key
volumes:
  - ./data:/opt/nginx-reverse-emby/panel/data
  - ./secrets/nre-pki:/run/nre-pki
```

先执行 `mkdir -p ./secrets/nre-pki && chmod 700 ./secrets/nre-pki`；若已有 key，再执行 `chmod 600 ./secrets/nre-pki/master.key`。目录必须对容器进程可写，不能改成 `:ro` 单文件挂载或不可变 secret projection，因为 protected restore 会暂存并原子替换 key。恢复后不要让外部同步器回写旧 key。内部 PKI 不新增控制端口；registration、heartbeat、revision 和 task 仍走现有 8080 panel/control listener 与 token 认证。升级或迁移既有 Relay 前，请先阅读[内部 PKI 升级与运维](../operations/internal-pki.md)。

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

然后浏览器访问 `http://127.0.0.1:8080`，输入 `API_TOKEN` 登录后即可使用面板。

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
  NRE_TRUST_FORWARDED_HEADERS: "true"
```

这样 join script 和 Agent 更新包 URL 会使用 HTTPS 面板地址。bundled local Agent 会清洗并重写 `X-Forwarded-*` 头，因此该自代理可以开启信任；若改用其它上游反代，只有在它会丢弃客户端伪造值并写入自身值时才开启。

## 出站代理（可选）

市场仓库刷新和其它控制面 HTTP 客户端遵循 `HTTP_PROXY`、`HTTPS_PROXY` 与 `NO_PROXY`。host 网络不会让容器里的 `127.0.0.1` 自动指向 Docker Desktop 宿主代理；可按实际环境使用 `http://192.168.65.254:<端口>`，Linux 服务器则填写宿主机可达地址。把控制面、数据库、Agent 和其它内网目标加入 `NO_PROXY`，避免本地控制流量绕到出站代理。

```yaml
environment:
  HTTP_PROXY: http://192.168.65.254:7890
  HTTPS_PROXY: http://192.168.65.254:7890
  NO_PROXY: localhost,127.0.0.1,::1,10.0.0.0/8
```

如果你配置了 Cloudflare API Token：

```yaml
environment:
  ACME_DNS_PROVIDER: cf
  CF_TOKEN: <Cloudflare API Token>
```

创建 HTTPS 规则时会优先使用 DNS-01 申请证书，不要求 80 端口先能完成 HTTP-01 校验。Token 建议只授予 `区域 / 区域 / 读取`、`区域 / DNS / 读取`、`区域 / DNS / 编辑`，并限制到当前域名。

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
  NRE_TRAFFIC_STATS_ENABLED: "false"
```

## 下一步

- [HTTP 反向代理](../guides/http-rules.md)
- [L4 端口转发](../guides/l4-rules.md)
- [证书与 HTTPS](../guides/certificates.md)
- [Agent 节点管理](../guides/agents.md)
