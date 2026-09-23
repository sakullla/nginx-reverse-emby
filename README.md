# Nginx-Reverse-Emby

[![Docker Build](https://github.com/sakullla/nginx-reverse-emby/actions/workflows/docker-build.yml/badge.svg)](https://github.com/sakullla/nginx-reverse-emby/actions/workflows/docker-build.yml)
![Docker Pulls](https://img.shields.io/docker/pulls/sakullla/nginx-reverse-emby?color=blue)

面向 Emby、Jellyfin 以及常见 HTTP / TCP 服务的反向代理控制面。  
典型场景：你有一台线路较好的 VPS，想把公费服 / 公益服 Emby、Jellyfin 或其它服务反代到自己的域名，减少观看时必须挂代理的问题。

不需要自己写 Nginx 配置。一个 Docker Compose 就能拉起面板，并在本机自带一个 `local` 节点负责真正转发流量。

完整中文文档：

- [文档首页](https://sakullla.github.io/nginx-reverse-emby/)
- [快速开始](https://sakullla.github.io/nginx-reverse-emby/getting-started/quickstart)
- [部署指南](https://sakullla.github.io/nginx-reverse-emby/getting-started/deploy)

## 它能做什么

- **HTTP / HTTPS 反代**：按域名转发 Web 服务，支持 ACME 自动证书
- **L4 端口转发**：转发 TCP / UDP 端口
- **多节点 Agent**：本机 `local` 节点可直接代理；也可把远端机器加入面板统一管理
- **Relay 隧道**：入口节点到后端不通时，再启用节点间中继（见文档站）

默认运行时是 **纯 Go 控制面容器**，不再依赖 Nginx。

## 5 分钟上手

### 你需要准备什么

1. 一台能装 Docker 的 Linux VPS
2. （推荐）一个域名，DNS 已解析到这台 VPS
3. 后端服务地址，例如 `https://origin.example.net` 或 `http://192.168.1.100:8096`

先确认 VPS 自己能访问后端：

```bash
curl -I https://origin.example.net
```

这一步不通，后面的反代也一定不通。

### 1. 一键部署（推荐）

在 VPS 上执行：

```bash
curl -fsSL https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/scripts/deploy-compose.sh | sh
```

脚本会创建目录、生成随机 token、启动服务。如果系统还没有 Docker Compose，会询问后自动安装。结束时会打印**访问地址**和 **Panel token**（登录用的访问令牌）。

交互通常只需两步：

1. **面板域名**：DNS 已指向本机则填入；直接回车 = 临时 HTTP
2. **Cloudflare Token**（可选）：粘贴后自动用 DNS-01 申请证书；回车跳过则用 HTTP-01

Cloudflare Token 权限需要包含：`区域 / 区域 / 读取`、`区域 / DNS / 读取`、`区域 / DNS / 编辑`。不要用 Global API Key。

把脚本输出的地址和 token 保存好。临时 HTTP 的随机路径只能降低被扫到的概率，**不能替代 HTTPS 和足够长的随机令牌**。

### 2. 用访问令牌登录

用脚本输出的地址打开面板，输入 **Panel token** 登录。全新部署没有用户名和密码，只用这一条令牌。

如果暂时没有公网域名，先在你的电脑上开 SSH 隧道：

```bash
ssh -L 8080:127.0.0.1:8080 root@<服务器 IP>
```

然后浏览器打开 `http://127.0.0.1:8080`。

### 3. 添加第一条规则

进入 **流量管理 → HTTP 规则**，节点选 `local`，添加规则：

| 字段 | 示例 | 说明 |
| --- | --- | --- |
| 入口域名 | `https://emby.example.com` | 你访问用的域名；选 HTTPS 时会自动申请证书 |
| 后端地址 | `https://origin.example.net` | 真正的服务地址，带协议和端口 |
| 启用规则 | 开 | 只有开启才会生效 |

确认 DNS 已指向 VPS，防火墙放行 `80` / `443`。保存后，`local` 节点会自动同步配置。

浏览器打开入口域名，能看到后端页面就说明跑通了。打不开时按顺序检查：

1. DNS 是否解析到 VPS
2. 防火墙是否放行了 80 / 443
3. VPS 能不能访问后端（上面的 `curl -I`）
4. 规则是否选了 `local` 并且已启用

节点上如果已经安装了加速源等插件，也可以在后端里选择「插件提供商」，不必自己填插件端口。第一次上手建议先用普通后端地址把链路跑通。更完整的图文步骤见 [快速开始](https://sakullla.github.io/nginx-reverse-emby/getting-started/quickstart)。

## 手动部署

适合不想跑交互脚本、或要自己改 Compose 的场景。

```bash
mkdir -p nginx-reverse-emby && cd nginx-reverse-emby
curl -O https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/docker-compose.yaml
mkdir -p data
```

编辑 `docker-compose.yaml`，至少改这两个值（用 32 位以上随机字符串，且互不相同）：

```yaml
environment:
  API_TOKEN: <面板登录令牌>
  MASTER_REGISTER_TOKEN: <远程节点注册令牌>
  NRE_TIMEZONE: Asia/Shanghai
```

也可以把配置写在 `.env` 里，参考 [`.env.example`](.env.example)。**不要把真实 token、证书或私钥提交到仓库。** `./data` 目录是运行数据，同样不要上传到 Git 或网盘。

托管证书默认每 24 小时扫描一次；单张 ACME 签发或续签默认最多运行 60 分钟，超时会记录失败并继续处理其他证书。可通过 `NRE_MANAGED_CERT_RENEW_INTERVAL` 和 `NRE_MANAGED_CERT_ACME_TIMEOUT` 调整。

启动：

```bash
docker compose up -d
```

默认只监听本机 `127.0.0.1:8080`。首次访问用上面的 SSH 隧道打开面板，用 `API_TOKEN` 登录。

### 给面板自身上 HTTPS

一键脚本在填写域名后会尽量自动完成。手动部署时，登录后加一条自代理规则即可：

| 字段 | 示例 |
| --- | --- |
| 入口域名 | `https://panel.example.com` |
| 后端地址 | `http://127.0.0.1:8080` |

确认防火墙放行 `80/443`。证书申请成功后，在 Compose 中设置：

```yaml
environment:
  NRE_PUBLIC_URL: https://panel.example.com
  NRE_TRUST_FORWARDED_HEADERS: "true"
```

然后 `docker compose up -d`。这样加入节点的命令和 Agent 更新地址都会走 HTTPS。

### 非交互部署

CI / 已知全部参数时：

```bash
curl -fsSL https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/scripts/deploy-compose.sh | \
  sh -s -- --public-url https://panel.example.com --cf-token YOUR_CF_TOKEN --yes --non-interactive
```

也可用环境变量 `API_TOKEN`、`MASTER_REGISTER_TOKEN`、`CF_TOKEN`、`NRE_NONINTERACTIVE=1` 达到同样效果。

## 加入更多节点

面板所在机器默认已有 `local` 节点。单机使用时，所有规则选 `local` 就够了。

如果还要在其它服务器上跑代理：

1. 打开面板 **节点管理**
2. 点击 **加入节点**
3. 选择 Linux / macOS，复制一键命令到目标机执行

Agent 会主动连接面板拉取配置，所以即使节点在内网或 NAT 后面也能工作。  
Windows 安装方式见 [Agent 指南](https://sakullla.github.io/nginx-reverse-emby/guides/agents)。

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

更偏运维与内部机制的内容（环境变量全表、内部 PKI、revision 异步生效、热升级等）已放在文档站，不在本 README 展开。

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
