# Nginx-Reverse-Emby

[![Docker Build](https://github.com/sakullla/nginx-reverse-emby/actions/workflows/docker-build.yml/badge.svg)](https://github.com/sakullla/nginx-reverse-emby/actions/workflows/docker-build.yml)
[![Docker Pulls](https://img.shields.io/docker/pulls/sakullla/nginx-reverse-emby?color=blue)](https://hub.docker.com/r/sakullla/nginx-reverse-emby)
[![Docs](https://img.shields.io/badge/docs-GitHub%20Pages-brightgreen)](https://sakullla.github.io/nginx-reverse-emby/)
[![License: GPL v3](https://img.shields.io/badge/license-GPLv3-blue)](./LICENSE)
[![GitHub Stars](https://img.shields.io/github/stars/sakullla/nginx-reverse-emby?style=social)](https://github.com/sakullla/nginx-reverse-emby/stargazers)

**一台线路好的 VPS + 一个面板，把 Emby / Jellyfin 以及常见 HTTP·TCP 服务反代到自己的域名——不用手写 Nginx 配置。**

* **懒得写反代配置？** → 一个 Docker Compose 拉起纯 Go 控制面，本机自带 `local` 节点负责转发
* **观看要挂代理才流畅？** → 用优质线路 VPS 做入口，把后端服务挂到自己的域名
* **要 HTTPS 又怕证书折腾？** → 规则选 HTTPS 即可 ACME 自动签发（HTTP-01 / Cloudflare DNS-01）
* **不止 Web，还要端口转发？** → 同一面板管理 HTTP/HTTPS 反代与 L4 TCP/UDP 转发
* **单机不够、入口到后端不通？** → 多节点 Agent 统一管理，需要时启用 Relay 隧道

### 一键安装

```bash
curl -fsSL https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/scripts/deploy-compose.sh | sh
```

📖 [完整文档](https://sakullla.github.io/nginx-reverse-emby/) · 🚀 [快速开始](https://sakullla.github.io/nginx-reverse-emby/getting-started/quickstart) · 🐳 [Docker Hub](https://hub.docker.com/r/sakullla/nginx-reverse-emby) · ⭐ 觉得有用请 [给仓库点个 Star](https://github.com/sakullla/nginx-reverse-emby)

***

## 目录

* [它能做什么](#它能做什么)
* [5 分钟上手](#5-分钟上手)
* [手动部署](#手动部署)
* [加入更多节点](#加入更多节点)
* [安全须知](#安全须知)
* [文档导航](#文档导航)
* [本地开发](#本地开发)
* [参与贡献](#参与贡献)
* [许可证](#许可证)

## 它能做什么

| 能力                  | 说明                                                                                            |
| ------------------- | --------------------------------------------------------------------------------------------- |
| **HTTP / HTTPS 反代** | 按域名转发 Web 服务，支持 ACME 自动证书                                                                     |
| **L4 端口转发**         | 直接转发 TCP / UDP 端口                                                                             |
| **多节点 Agent**       | 本机 `local` 节点开箱即用；也可把远端机器加入面板统一管理（支持内网 / NAT 后的节点）                                            |
| **Relay 隧道**        | 入口节点到后端不通时，启用节点间中继                                                                            |
| **证书管理**            | HTTP-01 与 Cloudflare DNS-01 自动签发，或手动上传公网证书                                                    |
| **流量统计与额度**         | 按网卡统计流量，支持月度额度等（见 [流量额度](https://sakullla.github.io/nginx-reverse-emby/guides/traffic-quota)） |

默认运行时是 **纯 Go 控制面容器**，控制面和 Agent 都由 Go 实现，**不再依赖 Nginx**（项目名沿用历史名称）。

## 5 分钟上手

### 准备

1. 一台能装 Docker 的 Linux VPS
2. （推荐）一个域名，DNS 已解析到这台 VPS
3. 后端服务地址，例如 `https://origin.example.net` 或 `http://192.168.1.100:8096`

先确认 VPS 自己能访问后端——这一步不通，后面的反代也一定不通：

```bash
curl -I https://origin.example.net
```

### 第 1 步：一键部署（推荐）

```bash
curl -fsSL https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/scripts/deploy-compose.sh | sh
```

脚本会创建目录、生成随机 token 并启动服务；如果系统还没有 Docker Compose，会询问后自动安装。交互通常只有两步：

1. **面板域名**：DNS 已指向本机则填入；直接回车 = 临时 HTTP
2. **Cloudflare Token**（可选）：粘贴后自动用 DNS-01 申请证书；回车跳过则用 HTTP-01

> Cloudflare Token 权限需包含：`区域 / 区域 / 读取`、`区域 / DNS / 读取`、`区域 / DNS / 编辑`。**不要使用 Global API Key。**

结束时会打印 **访问地址** 和 **Panel token**（登录用的访问令牌），请妥善保存。

### 第 2 步：用访问令牌登录

用脚本输出的地址打开面板，输入 **Panel token** 登录。全新部署没有用户名和密码，只用这一条令牌。

暂时没有公网域名？先在你的电脑上开 SSH 隧道，再访问 `http://127.0.0.1:8080`：

```bash
ssh -L 8080:127.0.0.1:8080 root@<服务器 IP>
```

### 第 3 步：添加第一条规则

进入 **流量管理 → HTTP 规则**，节点选 `local`，添加规则：

| 字段   | 示例                           | 说明                       |
| ---- | ---------------------------- | ------------------------ |
| 入口域名 | `https://emby.example.com`   | 你访问用的域名；选 HTTPS 时会自动申请证书 |
| 后端地址 | `https://origin.example.net` | 真正的服务地址，带协议和端口           |
| 启用规则 | 开                            | 只有开启才会生效                 |

确认 DNS 已指向 VPS、防火墙放行 `80` / `443`。保存后 `local` 节点会自动同步配置。浏览器打开入口域名能看到后端页面，就说明跑通了。

**打不开？按顺序检查：**

1. DNS 是否解析到 VPS
2. 防火墙是否放行了 80 / 443
3. VPS 能否访问后端（上面的 `curl -I`）
4. 规则是否选了 `local` 并且已启用

更多见 [故障排查](https://sakullla.github.io/nginx-reverse-emby/operations/troubleshooting)。

> 💡 节点上如果已安装加速源等插件，后端里可直接选「插件提供商」，不必手填插件端口。第一次上手建议先用普通后端地址把链路跑通。图文步骤见 [快速开始](https://sakullla.github.io/nginx-reverse-emby/getting-started/quickstart)。

## 手动部署

适合不想跑交互脚本、或需要自己改 Compose 的场景。

```bash
mkdir -p nginx-reverse-emby && cd nginx-reverse-emby
curl -O https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/docker-compose.yaml
mkdir -p data
```

编辑 `docker-compose.yaml`（或写入 `.env`，参考 [`.env.example`](.env.example)），至少设置以下两个令牌——用 32 位以上随机字符串，且**互不相同**：

```yaml
environment:
  API_TOKEN: <面板登录令牌>
  MASTER_REGISTER_TOKEN: <远程节点注册令牌>
  NRE_TIMEZONE: Asia/Shanghai
```

启动：

```bash
docker compose up -d
```

面板默认只监听本机 `127.0.0.1:8080`。首次访问用上面的 SSH 隧道打开，用 `API_TOKEN` 登录。

<details>
<summary><b>可选：Secret Vault 主密钥</b></summary>

Compose 中的 `PANEL_VAULT_MASTER_KEY` 留空时会从 `API_TOKEN` 派生。若之后可能更换 `API_TOKEN`，建议提前固定一个显式主密钥：

```yaml
environment:
  PANEL_VAULT_MASTER_KEY: <openssl rand -hex 32 的输出>
  PANEL_VAULT_KEY_ID: primary
```

把 token / key 放在 `.env` 时，请将文件权限设为 `0600`。细节见 [部署指南](https://sakullla.github.io/nginx-reverse-emby/getting-started/deploy)。

</details>

<details>
<summary><b>托管证书续签参数</b></summary>

托管证书默认每 24 小时扫描一次；单张 ACME 签发或续签默认最多运行 60 分钟，超时会记录失败并继续处理其他证书。可通过 `NRE_MANAGED_CERT_RENEW_INTERVAL` 和 `NRE_MANAGED_CERT_ACME_TIMEOUT` 调整。

</details>

### 给面板自身上 HTTPS

一键脚本填写域名后会尽量自动完成。手动部署时，登录后加一条自代理规则：

| 字段   | 示例                          |
| ---- | --------------------------- |
| 入口域名 | `https://panel.example.com` |
| 后端地址 | `http://127.0.0.1:8080`     |

确认防火墙放行 `80` / `443`。证书申请成功后，在 Compose 中设置并重新 `docker compose up -d`：

```yaml
environment:
  NRE_PUBLIC_URL: https://panel.example.com
  NRE_TRUST_FORWARDED_HEADERS: "true"
```

这样加入节点的命令和 Agent 更新地址都会走 HTTPS。

> `NRE_TRUST_FORWARDED_HEADERS` 仅在上游代理（如这里的 local Agent）会清洗并重写 `X-Forwarded-*` 时开启。

### 非交互部署

CI 或已知全部参数时：

```bash
curl -fsSL https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/scripts/deploy-compose.sh | \
  sh -s -- --public-url https://panel.example.com --cf-token YOUR_CF_TOKEN --yes --non-interactive
```

也可用环境变量 `API_TOKEN`、`MASTER_REGISTER_TOKEN`、`CF_TOKEN`、`NRE_NONINTERACTIVE=1` 达到同样效果。

## 加入更多节点

面板所在机器默认已有 `local` 节点，单机使用时所有规则选 `local` 就够了。要在其它服务器上跑代理：

1. 打开面板 **节点管理**
2. 点击 **加入节点**
3. 选择 Linux / macOS，复制一键命令到目标机执行

Agent 会主动连接面板拉取配置，因此即使节点在内网或 NAT 后面也能工作。Windows 安装方式见 [Agent 指南](https://sakullla.github.io/nginx-reverse-emby/guides/agents)。

## 安全须知

* **令牌要足够长且随机**：`API_TOKEN` 与 `MASTER_REGISTER_TOKEN` 用 32 位以上随机字符串，两者不能相同。
* **尽快启用 HTTPS**：临时 HTTP 的随机路径只能降低被扫到的概率，**不能替代 HTTPS 和足够长的随机令牌**。
* **不要提交敏感信息**：真实 token、证书、私钥以及 `./data` 运行数据目录，都不要上传到 Git 或网盘。
* **Cloudflare 用最小权限 API Token**，不要用 Global API Key。

## 文档导航

完整中文文档：**<https://sakullla.github.io/nginx-reverse-emby/>**

| 目标           | 文档                                                                               |
| ------------ | -------------------------------------------------------------------------------- |
| 跑通第一条反代      | [快速开始](https://sakullla.github.io/nginx-reverse-emby/getting-started/quickstart) |
| 部署细节与环境变量    | [部署指南](https://sakullla.github.io/nginx-reverse-emby/getting-started/deploy)     |
| 配置 HTTP 规则   | [HTTP 反向代理](https://sakullla.github.io/nginx-reverse-emby/guides/http-rules)     |
| 配置端口转发       | [L4 端口转发](https://sakullla.github.io/nginx-reverse-emby/guides/l4-rules)         |
| 申请 / 管理公网证书  | [证书与 HTTPS](https://sakullla.github.io/nginx-reverse-emby/guides/certificates)   |
| 多节点与加入 Agent | [Agent 指南](https://sakullla.github.io/nginx-reverse-emby/guides/agents)          |
| Relay 中继隧道   | [Relay 指南](https://sakullla.github.io/nginx-reverse-emby/guides/relay)           |
| 流量统计与额度      | [流量额度](https://sakullla.github.io/nginx-reverse-emby/guides/traffic-quota)       |
| 备份与恢复        | [备份恢复](https://sakullla.github.io/nginx-reverse-emby/operations/backup-restore)  |
| 故障排查         | [故障排查](https://sakullla.github.io/nginx-reverse-emby/operations/troubleshooting) |

环境变量全表、内部 PKI、revision 异步生效、热升级等运维与内部机制内容均在文档站，本 README 不展开。

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

# 文档站（源码在 docs-site/）
cd docs-site && npm ci && npm run dev
```

## 参与贡献

* 遇到问题或有功能建议：请提交 [Issue](https://github.com/sakullla/nginx-reverse-emby/issues)（附上部署方式与相关日志，注意脱敏 token）
* 欢迎提交 [Pull Request](https://github.com/sakullla/nginx-reverse-emby/pulls) 改进代码或文档
* ⭐ 如果这个项目帮到了你，[点个 Star](https://github.com/sakullla/nginx-reverse-emby) 就是最好的支持，也能让更多人发现它

## 许可证

本项目基于 GNU General Public License v3.0 授权发布，详见 [LICENSE](./LICENSE)。
