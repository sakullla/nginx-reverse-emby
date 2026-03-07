# Nginx-Reverse-Emby

一个以 Bash 为核心的一键反向代理部署项目，面向 Nginx 主机部署和 Docker 运行时动态配置两种场景。

- 支持 IPv4 / IPv6 前后端 URL
- 支持 acme.sh 自动申请和安装证书（含 DNS API / Cloudflare）
- 支持 Docker 动态规则：`PROXY_RULE_N` + 面板规则文件
- 支持 Docker 双模式：`front_proxy` / `direct`

---

## 1. 项目结构与职责

核心实现在 `deploy.sh`，Docker 相关逻辑在 `docker/`，面板在 `panel/`。

- `deploy.sh`：主机模式部署与移除主流程
- `conf.d/p.example.com.conf`：TLS 模板（HTTP/3 + QUIC）
- `conf.d/p.example.com.no_tls.conf`：HTTP 模板
- `docker/25-dynamic-reverse-proxy.sh`：Docker 动态反代配置生成
- `docker/15-panel-config.sh`：面板 nginx 配置渲染
- `docker/20-panel-backend.sh`：面板后端启动
- `panel/backend/server.js`：面板 API（`/api/rules`、`/api/apply`）

---

## 2. 主机模式（deploy.sh）

### 2.1 执行流程

`main()` 顺序：
1. `parse_arguments`
2. 若有 `--remove`，走 `remove_domain_config`
3. `setup_env`
4. `prompt_interactive_mode`
5. `display_summary`
6. `install_dependencies`
7. `generate_nginx_config`
8. `issue_certificate`
9. `test_and_reload_nginx`

安全模型：
- `set -e`
- `set -o pipefail`
- `trap 'handle_error $LINENO' ERR`
- 非 root 自动走 sudo

### 2.2 快速开始

交互模式：
```bash
bash <(curl -sSL https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/deploy.sh)
```

非交互模式（HTTPS 示例）：
```bash
curl -sSL https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/deploy.sh \
  | bash -s -- -y https://proxy.example.com -r http://127.0.0.1:8096
```

移除规则（建议始终带 scheme）：
```bash
curl -sSL https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/deploy.sh \
  | bash -s -- --remove https://proxy.example.com:443 --yes
```

### 2.3 参数（以实现为准）

| 短参数 | 长参数 | 说明 |
|---|---|---|
| `-y` | `--you-domain <URL>` | 前端访问 URL |
| `-r` | `--r-domain <URL>` | 后端 URL |
| `-m` | `--cert-domain <domain>` | 手动指定证书域名 |
| `-d` | `--parse-cert-domain` | 按规则自动提取根域名（仅匹配 `*.*.*`） |
| `-D` | `--dns <provider>` | DNS API 证书模式（如 `cf`） |
| `-R` | `--resolver <dns list>` | 自定义解析器 |
| `-c` | `--template-domain-config <path\|url>` | 自定义站点模板 |
|  | `--gh-proxy <url>` | GitHub 代理地址 |
|  | `--cf-token <token>` | Cloudflare Token |
|  | `--cf-account-id <id>` | Cloudflare Account ID |
|  | `--remove <URL>` | 按 `域名+端口` 精确移除 |
| `-Y` | `--yes` | 非交互删除确认 |
| `-h` | `--help` | 帮助 |

> 注意：真实长参数是 `--template-domain-config`，不是 `--template`。

### 2.4 关键行为

- URL 解析契约：`proto|domain|port|path`，支持带中括号 IPv6。
- 配置文件输出：`/etc/nginx/conf.d/{clean_domain}.{port}.conf`。
- 证书目录：`/etc/nginx/certs/{format_cert_domain}/`。
- 前端非根路径会自动生成 rewrite。
- 后端地址由协议 + 主机 + 可选端口拼接得到。

### 2.5 证书行为

- `no_tls=yes`（即前端 URL 为 `http://`）时跳过证书申请。
- 前端为 IP（IPv4/IPv6）时使用短期证书参数：
  `--certificate-profile shortlived --days 6`。
- DNS 模式：`issue_certificate_dns()`。
- Standalone 模式：`issue_certificate_standalone()`。
- Cloudflare 变量：`CF_Token`、`CF_Account_ID`。

### 2.6 移除行为

- 按 `domain + port` 定位配置文件。
- 非交互模式必须显式 `--yes`。
- 若证书被其他站点共用，会避免危险删除。
- 删除后会执行 `nginx -t` 再 reload/restart。

---

## 3. Docker 模式

### 3.1 启动

仓库自带 `docker-compose.yaml`：
```bash
docker compose up -d --build
```

默认暴露：
- `8080:8080`（面板）
- `80:80`（反代入口）

若使用 `direct` 且有 HTTPS 规则，通常还需要映射 `443:443`。

### 3.2 规则来源与格式

`docker/25-dynamic-reverse-proxy.sh` 会合并两类规则：
1. 环境变量：`PROXY_RULE_1`, `PROXY_RULE_2`, ...
2. 面板规则文件：`PANEL_RULES_FILE`（默认 `/opt/nginx-reverse-emby/panel/data/proxy_rules.csv`）

统一格式：
```text
frontend_url,backend_url
```

**重要：环境变量扫描是连续下标模式。** 一旦缺失某个编号，会停止后续扫描。

### 3.3 部署模式

`PROXY_DEPLOY_MODE`：

- `front_proxy`（默认）
  - 容器内仅 HTTP 反代
  - 适合上游已终止 TLS 的场景（如外层 Nginx/Caddy/Traefik）

- `direct`
  - 根据前端 URL 协议生成 HTTP/HTTPS server
  - `https://` 规则需要证书
  - `http://` 规则走明文模板

### 3.4 Direct 模式证书

核心变量：
- `DIRECT_CERT_MODE=acme|manual`（默认 `acme`）
- `DIRECT_CERT_DIR`（默认 `/etc/nginx/certs`）
- `DIRECT_CERT_CLEANUP`（默认启用）
- `ACME_EMAIL`
- `ACME_DNS_PROVIDER`
- `ACME_HOME`（默认 `/opt/acme.sh`）
- `ACME_CA`
- `ACME_STANDALONE_STOP_NGINX`
- `ACME_AUTO_RENEW`（默认启用）
- `ACME_RENEW_INTERVAL`（秒）

行为要点：
- 先检查现有 acme.sh 记录，存在则跳过签发、直接安装证书文件。
- DNS/Standalone 首次失败会清理残留后自动重试一次。
- 若配置了 `ACME_DNS_PROVIDER` 但前端主机是 IP，会自动回退到 Standalone。
- 删除 HTTPS 规则后，若域名不再被任何规则使用，可清理陈旧证书目录与 ACME 记录。

### 3.5 面板模式

默认面板入口：`http://<host>:8080/`。

- nginx 代理到后端 `PANEL_BACKEND_PORT`（默认 `18081`）
- API 路由：
  - `GET /api/rules`
  - `POST /api/rules`
  - `PUT /api/rules/:id`
  - `DELETE /api/rules/:id`
  - `POST /api/apply`
- `PANEL_AUTO_APPLY` 默认开启：增删改规则后自动执行 `generate -> nginx -t -> nginx -s reload`

---

## 4. 常见配置示例

### 4.1 front_proxy（默认）

```yaml
services:
  nginx-reverse-emby:
    image: ghcr.io/sakullla/nginx-reverse-emby:latest
    restart: unless-stopped
    ports:
      - "8080:8080"
      - "80:80"
    environment:
      - PROXY_DEPLOY_MODE=front_proxy
      - PROXY_RULE_1=https://proxy.example.com,http://emby:8096
    volumes:
      - nre_panel_data:/opt/nginx-reverse-emby/panel/data
```

### 4.2 direct（容器内自管 HTTPS）

```yaml
services:
  nginx-reverse-emby:
    image: ghcr.io/sakullla/nginx-reverse-emby:latest
    restart: unless-stopped
    ports:
      - "8080:8080"
      - "80:80"
      - "443:443"
    environment:
      - PROXY_DEPLOY_MODE=direct
      - DIRECT_CERT_MODE=acme
      - ACME_EMAIL=admin@example.com
      - ACME_DNS_PROVIDER=cf
      - CF_Token=xxxxxxxx
      - CF_Account_ID=xxxxxxxx
      - PROXY_RULE_1=https://media.example.com,http://emby:8096
    volumes:
      - nre_panel_data:/opt/nginx-reverse-emby/panel/data
      - nre_certs:/etc/nginx/certs
      - nre_acme:/opt/acme.sh
```

---

## 5. 验证清单

主机模式建议至少验证：
1. `nginx -t`
2. 部署一次 HTTPS 域名
3. 部署一次 IPv6 URL
4. 部署一次 HTTP（无 TLS）
5. 执行一次 `--remove https://domain:port --yes`

Docker 模式建议至少验证：
1. `docker build -t nginx-reverse-emby .`
2. 连续下标 `PROXY_RULE_1..N` 正常生效
3. 面板可通过 `PANEL_PORT` 访问（默认 `8080`）
4. `dynamic/` 下配置文件数量与命名正确
5. 路径转发与重写符合预期
6. `front_proxy` 在上游 TLS 终止场景可用
7. `direct` 可完成 HTTPS 规则证书签发和安装
8. 删规则后可清理陈旧证书目录/记录
9. `DIRECT_CERT_MODE=acme` 时自动续期循环正常

---

## 6. 已知文档偏差（历史）

- 旧文档可能写成 `--template`，实现实际为 `--template-domain-config`。
- `--remove` 示例若不带 scheme，容易造成匹配歧义；建议始终使用完整 URL。
- `--parse-cert-domain` 不是通用“根域名提取”，只在匹配 `*.*.*` 时触发。
- Docker 环境变量规则是“连续索引”策略，不支持跳号。

---

## 7. 贡献与同步要求

当实现行为发生变化时，请在同一补丁中同步更新：
- `README.md`
- `AGENTS.md`
- `CLAUDE.md`

并优先以 `deploy.sh` 与 `docker/25-dynamic-reverse-proxy.sh` 的实际实现作为事实依据。
