# Nginx-Reverse-Emby

[![Docker Build](https://github.com/sakullla/nginx-reverse-emby/actions/workflows/docker-build.yml/badge.svg)](https://github.com/sakullla/nginx-reverse-emby/actions/workflows/docker-build.yml)
![Docker Pulls](https://img.shields.io/docker/pulls/sakullla/nginx-reverse-emby?color=blue)

面向 Emby、Jellyfin 以及常见 HTTP/TCP 服务的反向代理控制面。  
典型场景：你有一台线路较好的 VPS，想把公费服 / 公益服 Emby、Jellyfin 或其它服务反代到自己的域名，减少观看时必须挂代理的问题。

完整中文文档：

- [文档首页](https://sakullla.github.io/nginx-reverse-emby/)
- [快速开始](https://sakullla.github.io/nginx-reverse-emby/getting-started/quickstart)
- [部署指南](https://sakullla.github.io/nginx-reverse-emby/getting-started/deploy)

## 它能做什么

- **HTTP / HTTPS 反代**：按域名转发 Web 服务，支持 ACME 自动证书
- **L4 端口转发**：转发 TCP / UDP 端口
- **多节点 Agent**：本机 `local` 节点可直接代理；也可把远端机器加入面板统一管理
- **Relay 隧道**：需要时再启用节点间中继（见文档站）

默认运行时是 **纯 Go 控制面容器**，不再依赖 Nginx。一个 Compose 即可拉起控制面和内置 local Agent。

## 5 分钟上手

### 1. 一键部署（推荐）

在 VPS 上执行：

```bash
curl -fsSL https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/scripts/deploy-compose.sh | sh
```

脚本会创建目录、生成随机 token、启动服务，并在结束时打印**访问地址**和 **Panel token**（登录密码）。

交互通常只需两步：

1. **面板域名**：DNS 已指向本机则填入；直接回车 = 临时 HTTP
2. **Cloudflare Token**（可选）：粘贴后自动 DNS-01 申请证书；回车跳过则用 HTTP-01

把脚本输出的地址和 token 保存好。临时 HTTP 的随机路径只能降低被扫到的概率，**不能替代强密码和 HTTPS**。

### 2. 登录并添加第一条规则

用脚本输出的地址打开面板，输入 Panel token 登录。

进入 **流量管理 → HTTP 规则**，选择 `local` 节点，添加：

| 字段 | 示例 | 说明 |
| --- | --- | --- |
| 入口域名 | `http://app.example.com` | 你访问用的域名，DNS 需先指向 VPS |
| 后端地址 | `https://origin.example.net` | 真实服务地址 |
| 启用规则 | 开 | 关闭不会生效 |

保存后，`local` Agent 会自动拉取并应用配置。更完整的图文步骤见 [快速开始](https://sakullla.github.io/nginx-reverse-emby/getting-started/quickstart)。

## 手动部署

适合不想跑交互脚本、或要自己改 Compose 的场景。

```bash
mkdir -p nginx-reverse-emby && cd nginx-reverse-emby
curl -O https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/docker-compose.yaml
mkdir -p data
```

配置两个访问 token 和通用 secret vault 的 envelope key。可先生成 vault key：

```bash
openssl rand -hex 32
```

将输出的 64 位 hex 字符串写入 `PANEL_VAULT_MASTER_KEY`：

```yaml
environment:
  API_TOKEN: <面板登录密码>
  MASTER_REGISTER_TOKEN: <远程节点注册令牌>
  PANEL_VAULT_MASTER_KEY: <64 位随机 hex>
  PANEL_VAULT_KEY_ID: primary
  NRE_TIMEZONE: Asia/Shanghai
```

`API_TOKEN` 和 `MASTER_REGISTER_TOKEN` 都要用 32 位以上随机字符串，且互不相同。`PANEL_VAULT_MASTER_KEY` 不会写入数据库，必须在每次重启时提供同一值并单独安全备份。已存储 secret 后，丢失或直接替换 master key 会使既有 ciphertext 永久不可解；在提供受控重加密流程前，不要手动轮换 key 或 key ID。一键部署脚本会在 `.env` 中自动生成并持久保留该 key，且将文件权限收紧为 `0600`。

官方插件市场默认读取镜像内的 `/opt/nginx-reverse-emby/official-market.lock`。需要使用其它锁文件时，可设置 `PANEL_OFFICIAL_MARKET_LOCK_FILE`；该值必须是容器内的绝对路径，并指向普通文件而非符号链接。文件缺失会阻止控制面初始化，锁内容、固定提交、市场摘要或签名校验失败会拒绝官方市场刷新并保留当前快照，不会回退到可移动分支。

启动：

```bash
docker compose up -d
```

默认只监听本机 `127.0.0.1:8080`。首次访问可先开 SSH 隧道：

```bash
ssh -L 8080:127.0.0.1:8080 root@<服务器 IP>
```

浏览器打开 `http://127.0.0.1:8080`，用 `API_TOKEN` 登录。

也可用 `.env` 管理配置，参考 [`.env.example`](.env.example)。**不要把真实 token、证书或私钥提交到仓库。**

### 给面板自身上 HTTPS（手动部署时推荐）

一键脚本在填写域名后会尽量自动完成；手动部署时可以自己加一条自代理规则：

| 字段 | 示例 |
| --- | --- |
| 入口域名 | `https://panel.example.com` |
| 后端地址 | `http://127.0.0.1:8080` |

确认防火墙放行 `80/443`。证书申请成功后，在 Compose 中设置：

```yaml
environment:
  NRE_PUBLIC_URL: https://panel.example.com
```

然后 `docker compose up -d`。

### 非交互部署

CI / 已知全部参数时：

```bash
curl -fsSL https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/scripts/deploy-compose.sh | \
  sh -s -- --public-url https://panel.example.com --cf-token YOUR_CF_TOKEN --yes --non-interactive
```

## 加入更多节点

面板所在机器默认已有 `local` 节点。如果还要在其它服务器上跑代理：

1. 打开面板 **节点管理**
2. 点击 **加入节点**
3. 选择 Linux / macOS，复制一键命令到目标机执行

默认会签发**一次性登记令牌**（约 10 分钟有效）。节点上线后会出现在列表中。  
Windows 目前需要手工安装 Go agent，详见 [Agent 指南](https://sakullla.github.io/nginx-reverse-emby/guides/agents)。

## 接下来看什么

| 目标 | 文档 |
| --- | --- |
| 跑通第一条反代 | [快速开始](https://sakullla.github.io/nginx-reverse-emby/getting-started/quickstart) |
| 理解部署细节与环境变量 | [部署指南](https://sakullla.github.io/nginx-reverse-emby/getting-started/deploy) |
| 配置 HTTP 规则 | [HTTP 反向代理](https://sakullla.github.io/nginx-reverse-emby/guides/http-rules) |
| 配置端口转发 | [L4 端口转发](https://sakullla.github.io/nginx-reverse-emby/guides/l4-rules) |
| 申请 / 管理公网证书 | [证书与 HTTPS](https://sakullla.github.io/nginx-reverse-emby/guides/certificates) |
| 多节点与加入 Agent | [Agent 指南](https://sakullla.github.io/nginx-reverse-emby/guides/agents) |
| Relay 中继隧道 | [Relay 指南](https://sakullla.github.io/nginx-reverse-emby/guides/relay) |
| 备份与恢复 | [备份恢复](https://sakullla.github.io/nginx-reverse-emby/operations/backup-restore) |
| 故障排查 | [故障排查](https://sakullla.github.io/nginx-reverse-emby/operations/troubleshooting) |

更偏运维与内部机制的内容（内部 PKI、revision 异步生效、热升级等）已放在文档站的运维章节，不在本 README 展开。

## 本地开发

```bash
# 前端
cd panel/frontend && npm ci && npm run dev

# 控制面
cd panel/backend-go && go test ./... && go run ./cmd/nre-control-plane

# Agent
cd go-agent && go test ./...

# 镜像
docker build -t nginx-reverse-emby .
```

文档站源码在 `docs-site/`：

```bash
cd docs-site
npm ci
npm run dev
```

## 许可证

本项目基于 GNU General Public License v3.0 授权发布，详见 [LICENSE](./LICENSE)。
