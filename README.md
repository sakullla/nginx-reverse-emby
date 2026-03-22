# Nginx-Reverse-Emby

[![Docker Build](https://github.com/sakullla/nginx-reverse-emby/actions/workflows/docker-build.yml/badge.svg)](https://github.com/sakullla/nginx-reverse-emby/actions/workflows/docker-build.yml)
![Docker Pulls](https://img.shields.io/docker/pulls/sakullla/nginx-reverse-emby?color=blue)

专为 Emby、Jellyfin 及各种 HTTP/TCP 服务设计的自动化反向代理解决方案，支持可视化面板管理、证书自动续期及 IPv4/IPv6 双栈。

## 核心特性

- **双模式部署**：Docker 容器化部署（推荐）或宿主机脚本直接部署
- **可视化面板**：轻量级管理界面，支持 HTTP/L4 规则增删改查、证书统一管理
- **自动化 SSL**：集成 `acme.sh`，支持 HTTP/DNS API（如 Cloudflare）自动申请并续期证书
- **全栈协议支持**：完美支持 IPv4/IPv6，适配各种网络环境
- **动态响应**：基于模板的 Nginx 配置生成，修改规则后自动测试并 reload
- **Master/Agent 架构**：支持集中管理多个 Nginx 节点，含 NAT Agent 支持

## 快速开始

### Docker 模式（推荐）

```bash
mkdir -p nginx-reverse-emby && cd nginx-reverse-emby
curl -O https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/docker-compose.yaml
mkdir -p data
```

编辑 `docker-compose.yaml`，修改 `API_TOKEN` 为你的访问令牌：

```yaml
environment:
  API_TOKEN: your-secure-token
  MASTER_REGISTER_TOKEN: your-register-token   # Agent 注册令牌，不使用 Agent 可忽略
```

启动服务：

```bash
docker compose up -d
```

访问面板：`http://<服务器IP>:8080`，使用 `API_TOKEN` 登录。

### 主机模式（deploy.sh）

```bash
# 交互式安装
bash <(curl -sSL https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/deploy.sh)

# 非交互式添加规则
curl -sSL https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/deploy.sh | bash -s -- \
  -y https://emby.example.com -r http://127.0.0.1:8096
```

#### deploy.sh 参数

| 参数 | 说明 | 示例 |
| :--- | :--- | :--- |
| `-y, --you-domain` | 前端访问地址 | `-y https://emby.example.com` |
| `-r, --r-domain` | 后端目标地址 | `-r http://192.168.1.10:8096` |
| `-m, --cert-domain` | 手动指定证书主域名 | `-m example.com` |
| `-d, --parse-cert-domain` | 自动提取根域名作为证书域名 | `-d` |
| `-D, --dns` | 使用 DNS API 模式申请证书 | `-D cf` |
| `-R, --resolver` | 手动指定 DNS 解析服务器 | `-R 8.8.8.8` |
| `--no-proxy-redirect` | 禁用 302/307 重定向代理 | `--no-proxy-redirect` |
| `--gh-proxy` | 指定 GitHub 加速代理 | `--gh-proxy https://gh.example.com` |
| `--cf-token` | Cloudflare API Token | `--cf-token xxxx` |
| `--cf-account-id` | Cloudflare Account ID | `--cf-account-id xxxx` |
| `--remove` | 移除指定域名的配置 | `--remove https://emby.example.com` |
| `-Y, --yes` | 非交互模式自动确认 | `-Y` |

## 配置说明

### Docker 环境变量

| 变量 | 说明 | 默认值 |
| :--- | :--- | :--- |
| `API_TOKEN` | 面板访问令牌 | 必填 |
| `PANEL_PORT` | 面板端口 | `8080` |
| `PANEL_ROLE` | 节点角色 `master` / `agent` | `master` |
| `PROXY_DEPLOY_MODE` | 部署模式 `direct` / `front_proxy` | `direct` |
| `ACME_EMAIL` | Let's Encrypt 注册邮箱 | - |
| `ACME_CA` | 证书颁发机构 | `letsencrypt` |
| `ACME_DNS_PROVIDER` | DNS 验证提供商（如 `cf`） | - |
| `CF_Token` | Cloudflare API Token | - |
| `CF_Account_ID` | Cloudflare 账号 ID | - |
| `MASTER_REGISTER_TOKEN` | Agent 注册令牌 | 与 `API_TOKEN` 相同 |
| `MASTER_LOCAL_AGENT_NAME` | Master 本机节点显示名称 | - |
| `MASTER_LOCAL_AGENT_TAGS` | Master 本机节点标签 | - |
| `PANEL_AUTO_APPLY` | 规则变更后自动应用 | `1` |

### 部署模式

- **`direct`**（默认）：容器直接监听 80/443 端口并处理 SSL 终止
- **`front_proxy`**：容器仅做内部转发，由外部代理（如 Nginx/Caddy）处理 SSL

### 302/307 重定向代理

默认代理后端的 302/307 重定向。如需禁用（如 CDN 回源、多跳转链路等场景）：

- **Docker 模式**：面板编辑规则，关闭「代理 302/307 重定向」开关
- **主机模式**：使用 `--no-proxy-redirect` 参数

## 证书管理

本项目默认使用 `acme.sh` 管理证书：

- **HTTP 验证**：需开放 80 端口
- **DNS 验证**（推荐）：通过 DNS API 自动验证，无需开放端口
- **IP 证书**：支持 Let's Encrypt short-lived 证书

首次申请失败后会自动清理残留状态并重试。

## Master/Agent 架构

支持在 Master 上集中管理多个 Nginx 节点：

- **Master**：运行完整面板与后端服务，负责规则管理与配置下发
- **Agent**：运行在目标主机，轻量级，仅需 Node.js 18+ 与 Nginx
- **NAT Agent**：位于内网后方，通过心跳轮询主动拉取配置，无需入站端口

### 加入 Agent 节点

```bash
curl -fsSL http://master.example.com:8080/panel-api/public/join-agent.sh | bash -s -- \
  --register-token your-register-token \
  --install-systemd
```

脚本会尽量自动安装缺失依赖（如 Node.js、curl、nginx、openssl、socat），并生成 `agent.env`、下载内置 apply/runtime 资源，最后注册 systemd 服务。

常见可选参数：

- `--agent-name edge-01`
- `--tags edge,emby`
- `--apply-command '/usr/local/bin/custom-apply.sh'`
- `--local-node --deploy-mode front_proxy`

### NAT Agent 示例

```bash
curl -fsSL http://master.example.com:8080/panel-api/public/join-agent.sh | bash -s -- \
  --register-token your-register-token \
  --agent-name nat-edge-01 \
  --tags nat,edge \
  --install-systemd
```

NAT Agent 只要能主动访问 Master 即可，无需 Master 能访问 Agent。

更多手工部署变量与示例文件见：

- `AGENT_EXAMPLES.md`
- `examples/light-agent.env.example`
- `examples/light-agent.service.example`

## 常见问题

<details>
<summary>为什么推荐使用 host 网络模式？</summary>

`network_mode: host` 让容器直接监听宿主机端口，避免 Docker 端口映射的复杂性，特别是在处理 IPv6 和动态多端口时更具优势。

</details>

<details>
<summary>如何备份规则和证书？</summary>

备份挂载到容器 `/opt/nginx-reverse-emby/panel/data` 的宿主机目录即可（`docker-compose.yaml` 中的 `./data`）。

</details>

<details>
<summary>何时需要禁用 302/307 代理？</summary>

以下场景建议禁用：
- **CDN 回源**：需要保留原始重定向地址
- **多跳转链接**：后端返回的链接需客户端直接访问
- **OAuth 回调**：重定向地址需保持原样传递给客户端

</details>

---

如果这个项目对你有帮助，请给一个 Star！
