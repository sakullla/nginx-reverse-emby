# Nginx-Reverse-Emby: 一站式 Nginx 反向代理部署脚本

`Nginx-Reverse-Emby` 是一个功能强大、高度自动化的 Bash 脚本，旨在为您一键配置 Nginx 反向代理。无论是代理 Emby、媒体服务器还是其他应用，都能提供稳定、高效的解决方案。

脚本的核心优势在于智能的 URL 处理、全面的 SSL 证书支持、国内加速优化，以及完善的部署与管理功能。

## ✨ 核心特性

### 🔧 灵活的部署配置
* **域名和 IP 混合支持**: 既支持标准域名部署，也支持直接使用 IP 地址
* **自定义端口映射**: 前后端端口完全独立配置，精确到端口的版本管理
* **协议自动识别**: 从 URL 自动判断 HTTP/HTTPS，支持混合代理

### 🛡️ 完整的 SSL/TLS 方案
* **Standalone 模式**: HTTP-01 验证，单域名证书自动申请和续期
* **DNS 验证模式**: DNS-01 验证，支持泛域名证书，包括：
  * **Cloudflare**: 内置 Cloudflare API 支持
  * **其他 DNS 提供商**: 通过 acme.sh 支持 100+ DNS 服务商
* **IP 证书支持**: 检测到 IP 地址时自动申请短期证书 (Let's Encrypt short-lived)

### 🚀 一键自动化部署
* **系统自动检测**: 自动识别 Linux 发行版（Debian/Ubuntu/CentOS/Fedora/Arch/Alpine）
* **官方源安装**: 从 Nginx 官方源安装最新 Mainline 版本
* **完整环境配置**: 自动安装 acme.sh、socat、cron 等依赖工具
* **国内加速优化**: 国内自动使用 GitHub 代理（gh.llkk.cc），无需手动配置

### 🌍 全球网络优化
* **国内代理支持**: 自动检测并使用国内加速代理下载配置和工具
* **灵活的 DNS 解析**: 支持手动指定 DNS 服务器，国内默认阿里云/腾讯云 DNS
* **IPv6 智能适配**: 自动检测并优化 IPv6 支持

### 📋 完整的生命周期管理
* **精确的配置管理**: 配置文件采用 `domain.port.conf` 命名，支持精确到端口的移除
* **安全的卸载**: `--remove` 选项安全删除指定域名的配置和证书
* **完整的备份**: 所有修改前自动备份至 `/etc/nginx/backup/`

### 🔄 高级代理功能
* **路径重写**: 支持前后端路径不同时的自动重写
* **完整 URL 构造**: 根据协议和端口自动拼接后端地址
* **非交互和交互模式**: 支持向导式交互和完全自动化部署

## 📋 系统要求

* 一台拥有公网 IP 的 Linux 服务器（VPS/云服务器）
* Root 权限或配置好的 sudo
* 域名配置（如使用 HTTPS）并指向服务器 IP
* (可选) DNS API 密钥（如使用泛域名证书）

## 🚀 快速开始

### 方式 A：交互模式（推荐新手）

脚本自动进行所有配置，只需运行一条命令：

**使用 curl（推荐）：**
```bash
bash <(curl -sSL https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/deploy.sh)
```

**使用 wget：**
```bash
bash <(wget -qO - https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/deploy.sh)
```

脚本会自动：
1. 检测操作系统和网络环境
2. 安装 Nginx（从官方源）
3. 安装和配置 acme.sh（SSL 证书管理）
4. 引导您输入访问地址和后端地址
5. 自动申请 SSL 证书（如需要）
6. 生成和部署 Nginx 配置
7. 重新加载 Nginx 服务

### 方式 B：非交互模式（自动化部署）

提供所有参数，实现完全自动化：

**使用 curl：**
```bash
curl -sSL https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/deploy.sh | bash -s -- [选项]
```

**使用 wget：**
```bash
wget -qO - https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/deploy.sh | bash -s -- [选项]
```

## 📚 使用示例

### 示例 1：一键部署（HTTPS 单域名）

最简单的方式，一条命令完成所有配置：

```bash
curl -sSL https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/deploy.sh | bash -s -- -y https://proxy.example.com -r https://backend-service.com
```

或使用 wget：
```bash
wget -qO - https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/deploy.sh | bash -s -- -y https://proxy.example.com -r https://backend-service.com
```

### 示例 2：一键部署（HTTP 服务，使用 IP）

```bash
wget -qO - https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/deploy.sh | bash -s -- -y http://192.168.1.100:8080 -r http://internal-service.local:8096
```

### 示例 3：一键泛域名证书部署（Cloudflare DNS 验证）

```bash
wget -qO - https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/deploy.sh | bash -s -- -y https://emby.media.com -r https://emby-backend.server.com -d --dns cf --cf-token "your_cloudflare_api_token" --cf-account-id "your_cloudflare_account_id"
```

### 示例 4：一键部署（自定义后端路径）

```bash
wget -qO - https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/deploy.sh | bash -s -- -y https://my-proxy.com/app -r https://backend.com/streaming
```

### 示例 5：一键部署（指定特定端口）

```bash
wget -qO - https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/deploy.sh | bash -s -- -y https://proxy.example.com:9443 -r http://192.168.1.100:8096
```

### 示例 6：一键部署（自定义 Nginx 模板）

```bash
wget -qO - https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/deploy.sh | bash -s -- -y https://proxy.example.com -r https://backend.com -c https://example.com/my-nginx.conf
```

### 示例 7：一键移除配置（精确到端口）

```bash
wget -qO - https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/deploy.sh | bash -s -- --remove https://proxy.example.com:9443 --yes
```

或移除该域名的所有端口配置：

```bash
wget -qO - https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/deploy.sh | bash -s -- --remove https://proxy.example.com --yes
```

### 示例 8：泛域名多子域部署

首次部署申请泛域名证书：
```bash
wget -qO - https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/deploy.sh | bash -s -- -y https://emby.media.com -r https://backend.com -d --dns cf --cf-token "token" --cf-account-id "id"
```

后续为同一泛域名的其他子域部署（自动使用已有证书）：
```bash
wget -qO - https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/deploy.sh | bash -s -- -y https://files.media.com -r https://another-backend.com -d
```

### 示例 9：HTTPS 前端 + HTTP 后端混合

```bash
wget -qO - https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/deploy.sh | bash -s -- -y https://public-proxy.example.com -r http://192.168.1.100:8080
```

## 📖 参数完整参考

### 部署参数

| 短选项 | 长选项 | 说明 |
|--------|--------|------|
| `-y` | `--you-domain <URL>` | **[必需]** 您的访问地址（支持 `https://domain:port/path` 格式） |
| `-r` | `--r-domain <URL>` | **[必需]** 被代理的后端地址（同样支持完整 URL 格式） |
| `-m` | `--cert-domain <域名>` | 手动指定证书域名，用于泛域名场景 |
| `-d` | `--parse-cert-domain` | 从 `-y` 自动提取根域名作为证书域名 |
| `-D` | `--dns <provider>` | 使用 DNS API 验证申请证书（如 `cf` 表示 Cloudflare） |
| `-R` | `--resolver <DNS>` | 手动指定 DNS 解析服务器（如 `8.8.8.8 1.1.1.1`） |
| `-c` | `--template <path/url>` | 指定自定义 Nginx 配置模板文件或 URL |
|  | `--gh-proxy <URL>` | 指定 GitHub 代理地址（如 `https://gh.llkk.cc/`） |
|  | `--cf-token <TOKEN>` | Cloudflare API Token（配合 `--dns cf`） |
|  | `--cf-account-id <ID>` | Cloudflare Account ID（配合 `--dns cf`） |

### 管理参数

| 短选项 | 长选项 | 说明 |
|--------|--------|------|
|  | `--remove <URL>` | 移除指定域名/端口的配置和证书 |
| `-Y` | `--yes` | 非交互模式下自动确认删除 |

### 其他参数

| 短选项 | 长选项 | 说明 |
|--------|--------|------|
| `-h` | `--help` | 显示帮助信息 |

## 🔍 工作流程

### 部署流程

```
1. 参数解析 → 2. 环境设置 → 3. 交互模式（可选）→ 4. 显示摘要
    ↓
5. 依赖安装 → 6. 生成配置 → 7. 申请证书 → 8. 重载 Nginx
```

### 关键配置位置

* **Nginx 配置**: `/etc/nginx/conf.d/{domain}.{port}.conf`
* **证书位置**: `/etc/nginx/certs/{cert_domain}/`
* **备份目录**: `/etc/nginx/backup/`
* **acme.sh**: `$HOME/.acme.sh/acme.sh`

## 🛠️ 常见问题

### Q: 国内用户无法访问 GitHub？

**A**: 脚本自动检测国内环境并使用 `gh.llkk.cc` 代理。也可手动指定：

```bash
bash deploy.sh -y ... -r ... --gh-proxy https://gh.llkk.cc/
```

### Q: 如何为多个子域名使用同一个泛域名证书？

**A**: 首次部署时申请泛域名证书，后续部署只需用 `-d` 自动识别即可：

```bash
# 首次：申请 *.media.com 证书
bash deploy.sh -y https://emby.media.com -r ... -d --dns cf --cf-token ... --cf-account-id ...

# 后续：自动识别使用已有证书
bash deploy.sh -y https://files.media.com -r ... -d
```

### Q: 可以在 IP 地址上使用吗？

**A**: 可以！IP 地址会自动申请 Let's Encrypt 短期证书（有效期 6 天，自动续期）：

```bash
bash deploy.sh -y https://123.45.67.89 -r https://backend.com
```

### Q: 如何修改已部署的配置？

**A**: 先移除再重新部署：

```bash
bash deploy.sh --remove https://proxy.example.com:443 --yes
bash deploy.sh -y https://proxy.example.com -r https://new-backend.com
```

### Q: 可以同一个域名的不同端口反代不同后端吗？

**A**: 可以！每个配置会精确到端口：

```bash
# 端口 443（HTTPS）
bash deploy.sh -y https://proxy.example.com:443 -r https://backend1.com

# 端口 8443（HTTPS）
bash deploy.sh -y https://proxy.example.com:8443 -r https://backend2.com
```

## 📝 配置示例

### 完整的 Cloudflare DNS 泛域名部署

```bash
bash deploy.sh \
  -y https://stream.mycloud.net \
  -r https://private-emby.internal.net:8096 \
  --parse-cert-domain \
  --dns cf \
  --cf-token "dnstok_xxxxxxx" \
  --cf-account-id "account_xxxxxxx"
```

### 混合场景：HTTPS 前端 + HTTP 后端

```bash
bash deploy.sh \
  -y https://public-proxy.example.com \
  -r http://192.168.1.100:8080
```

## 🔐 安全建议

1. **证书续期**: acme.sh 会自动配置 cron 任务进行续期，无需手动干预
2. **备份配置**: 所有修改前都会备份至 `/etc/nginx/backup/`，修改失败时可恢复
3. **日志检查**: 部署后检查 Nginx 日志确保无错误：
   ```bash
   nginx -t
   journalctl -u nginx -n 50
   ```
4. **防火墙**: 确保开放 80（HTTP 验证）和 443（HTTPS）端口

## 📞 支持与反馈

* **问题报告**: [GitHub Issues](https://github.com/sakullla/nginx-reverse-emby/issues)
* **功能建议**: 欢迎提交 Pull Request
* **许可证**: 本项目遵循相关开源许可

## 📄 更新日志

### v2.0（当前版本）
- ✨ 精确到端口的配置管理
- ✨ IP 地址短期证书支持
- ✨ 国内 GitHub 代理自动优化
- ✨ 完整的路径重写支持
- 🐛 优化了 acme.sh 下载验证机制
- 🐛 移除了端口 80 占用检测逻辑

### v1.0
- 🎉 初始版本发布
- 基础的域名反代和证书申请
- Cloudflare DNS 验证支持
| `-m` | `--cert-domain <域名>` | **(部署)** 手动指定证书的根域名，适合泛域名场景。 |
| `-d` | `--parse-cert-domain` | **(部署)** 自动从 `-y` 提供的域名中解析出根域名。 |
| `-D` | `--dns <服务商>` | **(部署)** 使用 DNS API 模式申请证书 (例如: `cf`)。 |
| `-c` | `--template <路径或URL>` | **(部署)** 指定一个自定义的 Nginx 配置文件模板。 |
|      | `--cf-token <TOKEN>` | **(部署)** 提供 Cloudflare API Token。 |
|      | `--cf-account-id <ID>` | **(部署)** 提供 Cloudflare Account ID。 |
|      | `--remove <域名或URL>` | **(管理)** 移除指定域名或 URL 的所有相关配置和证书。 |
| `-Y` | `--yes` | **(管理)** 在非交互模式下，自动确认移除操作。 |
| `-h` | `--help` | 显示帮助信息。 |

## 💬 反馈与贡献

遇到问题或有改进建议？欢迎在 [GitHub Issues](https://github.com/sakullla/nginx-reverse-emby/issues) 中提出。
