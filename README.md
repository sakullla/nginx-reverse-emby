# Nginx-Reverse-Emby

一个面向 Emby、Jellyfin 和其他 HTTP 服务的反向代理项目。

它提供两种使用方式：

- 主机模式：直接在宿主机写入 Nginx 配置
- Docker 模式：容器启动后按规则动态生成反代配置，并提供管理面板

支持：

- IPv4 / IPv6 前端和后端 URL
- `acme.sh` 自动申请和安装证书
- DNS API 验证（含 Cloudflare）
- Docker 环境变量规则 `PROXY_RULE_N`
- 面板动态新增、编辑、删除、应用规则

## 推荐用法

大多数场景直接使用仓库内置的 `docker-compose.yaml` 即可。

当前示例默认：

- 使用镜像 `ghcr.io/sakullla/nginx-reverse-emby:latest`
- 使用 `network_mode: host`
- 使用 `PROXY_DEPLOY_MODE=direct`
- 面板默认监听宿主机 `8080`
- 反代规则直接占用宿主机 `80/443` 或规则中声明的监听端口

启动：

```bash
docker compose up -d
```

面板地址：

```text
http://<你的服务器IP>:8080/
```

首次启动前，至少检查这几项：

- 把 `API_TOKEN` 改成你自己的随机长字符串
- 确认宿主机上的 `8080` 没被占用
- 如果要让容器直接签发 HTTPS 证书，确认 `80` 和 `443` 可用

## docker-compose.yaml 说明

仓库内置示例的思路是“开箱即用，而不是最小配置”。

你通常只需要改这几项：

- `API_TOKEN`
- `PROXY_RULE_1`（如果你想预置规则）
- `ACME_EMAIL`（如果你希望显式填写）
- `ACME_DNS_PROVIDER` 和对应 DNS 凭据（如果你要用 DNS 验证）

规则格式固定为：

```text
frontend_url,backend_url
```

例如：

```text
https://emby.example.com,http://192.168.1.10:8096
```

注意：

- `PROXY_RULE_1`、`PROXY_RULE_2`、`PROXY_RULE_3` 必须连续编号
- 如果缺了某个编号，后面的规则不会继续读取
- 仓库示例里默认把 `PROXY_RULE_1` 注释掉，避免你启动后立刻生成错误规则

## Docker 快速开始

### 方式一：直接使用 compose

```bash
docker compose up -d
```

默认数据目录：

```text
./data
```

其中会保存：

- 面板规则文件
- 已签发证书
- `acme.sh` 工作目录

### 方式二：使用 docker run

```bash
docker run \
  -d \
  --name nginx-reverse-emby \
  --restart unless-stopped \
  --network host \
  -e PROXY_DEPLOY_MODE=direct \
  -e API_TOKEN=change-this-token \
  -v ${PWD}/data:/opt/nginx-reverse-emby/panel/data \
  ghcr.io/sakullla/nginx-reverse-emby:latest
```

## 面板使用

默认面板入口：

```text
http://<你的服务器IP>:8080/
```

面板支持：

- 查看规则
- 新增规则
- 编辑规则
- 删除规则
- 手动应用配置

默认开启自动应用，也就是新增、修改、删除规则后会自动执行：

```text
generate -> nginx -t -> nginx -s reload
```

如果你不想自动应用，可以设置：

```bash
PANEL_AUTO_APPLY=0
```

`API_TOKEN` 说明：

- 留空时，面板接口不做鉴权
- 仓库示例默认要求你显式设置它
- 生产环境不要使用 `change-this-token` 这种示例值

## Docker 两种模式

### `direct`

推荐给大多数用户，也是当前 `docker-compose.yaml` 默认值。

特点：

- 容器自己处理前端 HTTP / HTTPS
- `https://` 规则会生成 TLS 配置
- 可以直接在容器内申请和续期证书
- 适合没有外层 Nginx / Caddy / Traefik 的场景

### `front_proxy`

适合已经有上游反向代理的场景。

特点：

- 容器内部只做 HTTP 反代
- 外层代理负责 TLS
- 实际监听端口由 `FRONT_PROXY_PORT` 控制，默认 `3000`

示例：

```yaml
environment:
  PROXY_DEPLOY_MODE: front_proxy
  FRONT_PROXY_PORT: "3000"
```

说明：

- 镜像内部实现默认值仍然是 `front_proxy`
- 但仓库提供的 `docker-compose.yaml` 已经显式改成 `direct`

## Docker 证书说明

`direct` 模式下默认使用：

```text
DIRECT_CERT_MODE=acme
```

常用变量：

- `ACME_EMAIL`
- `ACME_CA`，默认 `letsencrypt`
- `ACME_DNS_PROVIDER`
- `CF_Token`
- `CF_Account_ID`
- `ACME_HOME`，默认 `/opt/nginx-reverse-emby/panel/data/.acme.sh`
- `DIRECT_CERT_DIR`，默认 `/opt/nginx-reverse-emby/panel/data/certs`

当前行为：

- 容器内已经带 `cron` / `crontab`
- `direct + acme` 下会自动启动后台续期循环
- `acme.sh` 的工作目录固定在 `ACME_HOME`
- 证书安装后会尝试执行 reload

### 什么时候建议用 DNS 验证

以下情况建议优先使用 DNS 验证：

- 你通过面板在容器运行中热添加 HTTPS 规则
- 你不想依赖 `80` 端口做 standalone 验证
- 你使用 Cloudflare 之类的 DNS 提供商

Cloudflare 示例：

```yaml
environment:
  PROXY_DEPLOY_MODE: direct
  ACME_DNS_PROVIDER: cf
  CF_Token: xxxxxxxx
  CF_Account_ID: xxxxxxxx
```

## 主机模式

如果你不是想跑 Docker，而是想直接在宿主机写入 Nginx 配置，可以使用 `deploy.sh`。

交互模式：

```bash
bash <(curl -sSL https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/deploy.sh)
```

非交互模式：

```bash
curl -sSL https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/deploy.sh \
  | bash -s -- -y https://proxy.example.com -r http://127.0.0.1:8096
```

删除规则：

```bash
curl -sSL https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/deploy.sh \
  | bash -s -- --remove https://proxy.example.com:443 --yes
```

常用参数：

| 参数 | 说明 |
|---|---|
| `-y`, `--you-domain <URL>` | 前端访问地址 |
| `-r`, `--r-domain <URL>` | 后端地址 |
| `-m`, `--cert-domain <domain>` | 手动指定证书域名 |
| `-d`, `--parse-cert-domain` | 自动提取根域名，仅在匹配 `*.*.*` 时生效 |
| `-D`, `--dns <provider>` | 使用 DNS API 验证 |
| `-R`, `--resolver <dns list>` | 自定义解析器 |
| `-c`, `--template-domain-config <path\|url>` | 自定义模板 |
| `--remove <URL>` | 删除指定规则 |
| `-Y`, `--yes` | 非交互删除确认 |
| `-h`, `--help` | 显示帮助 |

主机模式补充说明：

- `http://` 前端不会申请证书
- 前端是 IP 时会使用短期证书参数
- 没配 `--dns` 时默认走 standalone
- Cloudflare 需要 `CF_Token` 和 `CF_Account_ID`
- `--template-domain-config` 才是真实参数名，不是 `--template`

## 常见问题

### 为什么新增或删除 HTTPS 规则会比较慢

因为后端可能还要顺序完成这些步骤：

- 生成配置
- 申请证书
- 安装证书
- `nginx -t`
- `nginx -s reload`

域名签证书本身就可能需要十几秒甚至更久。

### 为什么面板统计里的“总请求数”偏大

当前统计来自 nginx 全局请求，不只包含代理流量，也包含：

- 面板静态资源
- `/panel-api/*`
- 其他进入 nginx 的请求

所以它不是纯代理请求数。

### 为什么推荐 compose 示例使用 host 网络

因为这个项目在 `direct` 模式下本来就是要让容器直接监听前端端口。

使用 `network_mode: host` 的好处是：

- 配置更简单
- 不需要为每条规则再单独考虑端口映射
- 更符合“容器直接接管 80/443”的使用方式

代价也很明确：

- 会直接占用宿主机端口
- 不适合你还要在宿主机上跑另一套前端 Nginx 的场景

### 为什么 `PROXY_RULE_1` 在 compose 里默认是注释的

因为仓库示例应该优先保证“直接启动不出错”。

如果默认塞入一条示例规则，很容易出现：

- 域名不是你的
- 后端地址不是你的
- 启动后立刻触发证书申请失败
- 面板自动应用时报错

所以默认留空更稳妥。

## 最低检查项

建议至少检查：

1. `docker compose up -d` 后容器正常启动
2. `http://<你的服务器IP>:8080/` 可以打开面板
3. 新增一条 HTTP 规则可以立即生效
4. 新增一条 HTTPS 规则时证书可以正常签发和安装
5. 删除规则后 nginx 仍能正常 `reload`
6. 如果使用 `PROXY_RULE_N`，确认编号连续
