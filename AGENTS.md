# AGENTS.md - Nginx-Reverse-Emby 项目指南

本文档面向 AI 编程助手，介绍本项目的架构、技术栈和开发约定。

## 项目概述

**Nginx-Reverse-Emby** 是一个功能强大、高度自动化的 Bash 脚本项目，用于一键配置 Nginx 反向代理。项目主要针对 Emby/媒体服务器场景优化，但支持代理任何后端应用。

### 核心功能
- 智能 URL 处理（支持 IPv4/IPv6、域名、端口、路径）
- 完整的 SSL/TLS 方案（自动申请和续期证书）
- 国内网络加速优化（自动检测并使用 GitHub 代理）
- 支持泛域名证书（DNS API 验证）
- Docker 支持（动态反向代理配置）

## 项目结构

```
.
├── deploy.sh                      # 主部署脚本（核心文件）
├── nginx.conf                     # Nginx 主配置文件模板
├── Dockerfile                     # Docker 镜像构建配置
├── README.md                      # 项目文档（中文）
├── AGENTS.md                      # 本文件
├── conf.d/                        # Nginx 站点配置模板
│   ├── p.example.com.conf         # HTTPS 配置模板
│   └── p.example.com.no_tls.conf  # HTTP 配置模板
├── docker/                        # Docker 相关文件
│   ├── nginx.conf                 # Docker 版 Nginx 配置
│   ├── default.conf.template      # Docker 版站点配置模板
│   └── 25-dynamic-reverse-proxy.sh # Docker entrypoint 脚本
└── .github/workflows/
    └── docker-build.yml           # GitHub Actions CI/CD
```

## 技术栈

### 主要技术
- **Bash**: 核心部署脚本（Bash 4.0+）
- **Nginx**: 反向代理服务器（官方 Mainline 版本）
- **acme.sh**: SSL 证书申请和管理工具
- **Docker**: 容器化部署支持
- **GitHub Actions**: CI/CD 自动化

### 支持的 Linux 发行版
- Debian/Ubuntu
- CentOS/RHEL/Fedora/AlmaLinux/Rocky/Amazon Linux
- Arch Linux
- Alpine Linux

## 核心文件详解

### deploy.sh
主部署脚本，支持两种运行模式：

1. **交互模式**: 引导用户输入参数
   ```bash
   bash deploy.sh
   ```

2. **非交互模式**: 通过命令行参数自动化部署
   ```bash
   bash deploy.sh -y <前端URL> -r <后端URL> [其他选项]
   ```

#### 关键函数
- `parse_arguments()`: 解析命令行参数
- `parse_url()`: 解析 URL（支持 IPv6 方括号格式）
- `install_dependencies()`: 安装 Nginx、acme.sh、依赖工具
- `generate_nginx_config()`: 使用 envsubst 渲染配置模板
- `issue_certificate()`: 申请 SSL 证书（Standalone/DNS 模式）
- `remove_domain_config()`: 移除配置和证书

#### 环境变量
- `CONF_HOME`: 配置文件远程基础 URL
- `ACME_INSTALL_URL`: acme.sh 安装脚本 URL
- `BACKUP_DIR`: 配置备份目录（默认 `/etc/nginx/backup`）

### 配置模板（conf.d/）

模板使用 `envsubst` 进行变量替换，支持以下变量：

| 变量名 | 说明 |
|--------|------|
| `${you_domain}` | 前端域名/IP（含方括号的 IPv6） |
| `${you_frontend_port}` | 前端监听端口 |
| `${resolver}` | DNS 解析器配置 |
| `${format_cert_domain}` | 证书域名（纯净的，无方括号） |
| `${you_domain_path}` | 前端路径 |
| `${you_domain_path_rewrite}` | 路径重写规则 |
| `${r_domain_full}` | 后端完整 URL |

### Docker 支持

Docker 镜像使用环境变量动态生成配置：

```bash
# 示例：运行 Docker 容器
docker run -e PROXY_RULE_1="http://frontend.com,http://backend:8080" \
           -e PROXY_RULE_2="http://api.frontend.com,http://api-backend:3000" \
           ghcr.io/sakullla/nginx-reverse-emby:latest
```

#### Docker 环境变量
- `PROXY_RULE_N`: 第 N 条代理规则，格式为 `前端URL,后端URL`
- `NGINX_LOCAL_RESOLVERS`: DNS 解析器（默认 `1.1.1.1`）
- `NGINX_ENTRYPOINT_QUIET_LOGS`: 设置为非空值可抑制日志输出

## 代码风格指南

### Bash 脚本规范

1. **严格模式**: 脚本开头启用严格模式
   ```bash
   set -e
   set -o pipefail
   ```

2. **错误处理**: 使用 trap 捕获错误
   ```bash
   trap 'handle_error $LINENO' ERR
   ```

3. **日志输出**: 使用统一的日志函数
   ```bash
   log_info "信息消息"
   log_success "成功消息"
   log_warn "警告消息"
   log_error "错误消息"
   ```

4. **颜色定义**: 使用标准颜色变量
   ```bash
   RED='\033[0;31m'
   GREEN='\033[0;32m'
   YELLOW='\033[1;33m'
   BLUE='\033[0;34m'
   NC='\033[0m'
   ```

5. **权限处理**: 支持 sudo 和 root 运行
   ```bash
   SUDO=''
   if [ "$(id -u)" -ne 0 ]; then
       SUDO='sudo'
   fi
   ```

### Nginx 配置规范

1. 使用 `${variable}` 格式表示模板变量
2. HTTP/3 和 QUIC 支持（端口模板变量需正确处理）
3. 缓冲区优化配置（媒体流传输场景）
4. 路径重写和重定向处理

## 部署流程

### 标准部署流程
```
1. 参数解析 → 2. 环境设置 → 3. 交互模式（可选）→ 4. 显示摘要
    ↓
5. 依赖安装 → 6. 生成配置 → 7. 申请证书 → 8. 重载 Nginx
```

### 关键配置路径
| 路径 | 说明 |
|------|------|
| `/etc/nginx/conf.d/{domain}.{port}.conf` | 站点配置文件 |
| `/etc/nginx/certs/{cert_domain}/` | SSL 证书目录 |
| `/etc/nginx/backup/` | 配置备份目录 |
| `$HOME/.acme.sh/acme.sh` | acme.sh 安装路径 |

## 证书管理

### 证书模式
1. **Standalone 模式**: HTTP-01 验证，适用于单域名或 IP 地址
2. **DNS 模式**: DNS-01 验证，支持泛域名证书

### IP 地址证书
- 自动申请 Let's Encrypt short-lived 证书（6 天有效期）
- 自动续期（acme.sh cron 任务）

### 泛域名证书
- 首次部署需要 DNS API 凭据（如 Cloudflare Token）
- 同一根域名的后续部署自动复用已有证书

## 测试与验证

### 手动测试命令
```bash
# 测试 Nginx 配置
nginx -t

# 检查证书信息
ls -la /etc/nginx/certs/

# 查看 Nginx 日志
journalctl -u nginx -n 50

# 测试 IPv6 连接
curl -6 https://[2400:db8::1]
```

### Docker 本地测试
```bash
# 构建镜像
docker build -t nginx-reverse-emby .

# 运行测试
docker run -e PROXY_RULE_1="http://localhost:8080,http://backend:8096" \
           -p 8080:80 nginx-reverse-emby
```

## CI/CD

GitHub Actions 工作流（`.github/workflows/docker-build.yml`）：
- 触发条件: `main` 分支的 push
- 构建并推送镜像到 GitHub Container Registry (ghcr.io)
- 标签: `latest` 和短 commit SHA

## 安全注意事项

1. **备份机制**: 所有配置修改前自动备份到 `/etc/nginx/backup/`
2. **证书续期**: acme.sh 自动配置 cron 任务，无需手动干预
3. **防火墙**: 确保开放 80（HTTP 验证）和 443（HTTPS）端口
4. **权限**: 脚本需要 root 或 sudo 权限执行
5. **泛域名证书**: 移除配置时智能检测共享证书，避免误删

## 修改建议

### 添加新功能时的注意事项

1. **URL 解析**: 如需修改 URL 解析逻辑，确保兼容 IPv6 方括号格式
2. **模板变量**: 新增模板变量时，需在 `generate_nginx_config()` 中 export
3. **依赖安装**: 新增系统依赖时，需为所有支持的发行版添加安装逻辑
4. **错误处理**: 新增功能应包含适当的错误处理和日志输出

### 常见问题排查

1. **国内网络问题**: 自动检测并代理 GitHub 资源，可通过 `--gh-proxy` 手动指定
2. **证书申请失败**: 检查防火墙、DNS 解析、80 端口占用
3. **Nginx 配置错误**: 使用 `nginx -t` 测试配置
4. **IPv6 问题**: 确保地址使用方括号包裹（如 `[2400:db8::1]`）
