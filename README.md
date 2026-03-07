# Nginx-Reverse-Emby 🚀

[![Docker Build](https://github.com/sakullla/nginx-reverse-emby/actions/workflows/docker-build.yml/badge.svg)](https://github.com/sakullla/nginx-reverse-emby/actions/workflows/docker-build.yml)
![Docker Pulls](https://img.shields.io/docker/pulls/sakullla/nginx-reverse-emby?color=blue)

一个专为 Emby、Jellyfin 及各种 HTTP 服务设计的自动化反向代理解决方案。支持可视化面板管理、证书自动续期及 IPv4/IPv6 双栈。

---

## ✨ 核心特性

- 🛠 **双模式部署**：支持 Docker 容器化部署（推荐）和宿主机脚本直接部署。
- 🖥 **可视化面板**：轻量级管理后端，支持规则的增删改查、即时应用及流量统计。
- 🔒 **自动化 SSL**：集成 `acme.sh`，支持 HTTP / DNS API（如 Cloudflare）自动申请并续期证书。
- 🌐 **全栈协议支持**：完美支持 IPv4 / IPv6，适配各种复杂的网络环境。
- ⚡ **动态响应**：基于模板的 Nginx 配置生成，修改规则后自动执行 `nginx -t` 与 `reload`，平滑无感。
- 📦 **开箱即用**：预置最优化的 Nginx 配置，特别针对媒体流服务进行了调优。

---

## 🚀 快速开始 (Docker 模式)

这是最推荐的部署方式，只需一个文件即可接管你的反代服务。

### 1. 准备工作
```bash
mkdir -p nginx-reverse-emby && cd nginx-reverse-emby
curl -O https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/docker-compose.yaml
mkdir -p data
```

### 2. 配置环境变量
编辑 `docker-compose.yaml`，重点修改以下几项：
- `API_TOKEN`: **务必修改**，用于面板接口鉴权。
- `PROXY_RULE_1`: (可选) 预置第一条规则，格式为 `前端地址,后端地址`。

### 3. 启动
```bash
docker compose up -d
```

### 4. 访问面板
打开浏览器访问：`http://<服务器IP>:8080`
> **注意**：首次登录请使用你在环境变量中设置的 `API_TOKEN`。

---

## ⚙️ 配置指南

### 规则格式
无论是在面板添加还是通过环境变量预置，规则统一遵循：
`frontend_url,backend_url`

**示例：**
- **标准 HTTPS**：`https://emby.example.com,http://192.168.1.10:8096` (会自动触发 SSL 申请)
- **特定端口**：`http://files.example.com:81,http://10.0.0.5:8080`
- **IPv6 后端**：`https://jellyfin.me.com,http://[2001:db8::1]:8096`

### 部署模式 (PROXY_DEPLOY_MODE)

| 模式 | 说明 | 适用场景 |
| :--- | :--- | :--- |
| `direct` (默认) | 容器直接接管 80/443 端口，处理 SSL 握手。 | **最推荐**。服务器无其他 Nginx，想一站式解决。 |
| `front_proxy` | 容器仅做内部转发，SSL 由外层代理（如大内网前置机）处理。 | 已有上游 Nginx 或使用 CF 隧道。 |

---

## 🔒 证书与域名验证

本镜像默认使用 `acme.sh` 管理证书。
当上一次失败残留了域名 key 或 ACME 状态时，Docker `direct` 和宿主机脚本都会在首次申请失败后清理状态，并带 `--force` 自动重试一次。

### DNS API 验证 (以 Cloudflare 为例)
如果你希望在不暴露 80 端口的情况下申请证书，建议使用 DNS 验证：
```yaml
environment:
  - ACME_DNS_PROVIDER=cf
  - CF_Token=你的Cloudflare_Token
  - CF_Account_ID=你的账号ID
```

---

## 🛠 主机模式 (非 Docker)

如果你希望直接在宿主机运行，可以使用我们提供的交互式脚本：

```bash
# 交互式安装/添加规则
bash <(curl -sSL https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/deploy.sh)

# 非交互式快捷添加
curl -sSL https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/deploy.sh | bash -s -- -y https://emby.abc.com -r http://127.0.0.1:8096
```

---

## ❓ 常见问题 (FAQ)

<details>
<summary>为什么新增 HTTPS 规则后生效较慢？</summary>
系统需要按序执行：生成配置 -> 申请证书 (ACME 验证) -> 安装证书 -> Nginx 测试 -> Reload。域名验证过程通常需要 10-30 秒。
</details>

<details>
<summary>为什么推荐使用 host 网络模式？</summary>
`network_mode: host` 可以让容器直接高效地监听宿主机端口，避免了繁琐的 Docker 端口映射，尤其是在处理 IPv6 和动态增加多端口规则时更具优势。
</details>

<details>
<summary>如何备份我的规则和证书？</summary>
只需备份挂载到容器 `/opt/nginx-reverse-emby/panel/data` 的宿主机目录即可。
</details>

---
⭐ 如果这个项目对你有帮助，请给一个 Star！
