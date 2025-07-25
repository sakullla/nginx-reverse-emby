# Nginx-Reverse-Emby: 一站式 Emby 反向代理部署脚本

`Nginx-Reverse-Emby` 是一个功能强大、高度自动化的 Shell 脚本，旨在为您一键配置 Nginx 作为 Emby 服务器的反向代理。无论您是为 Emby 公费服还是机场服进行配置，它都能提供稳定、高效的解决方案。

脚本的核心优势在于其智能的重定向处理、对现代网络协议的全面支持以及完善的部署与卸载管理功能。

## ✨ 功能特性

* **🔄 智能重定向处理**: 透明拦截并处理来自后端 Emby 服务器的 `301`, `302`, `307` 等重定向请求，确保媒体播放无缝衔接，永不中断。

* **⚡️ 现代协议支持**: 代理后的服务完全兼容 HTTP/1.1, HTTP/2, 和 **HTTP/3 (QUIC)**，并同时支持 IPv4 和 IPv6 访问，为用户提供最低延迟的连接。

* **🚀 一键自动化部署**: 自动检测操作系统、从官方源安装最新版 Nginx、配置 SSL 证书 (acme.sh)，并完成所有必要的系统设置。

* **🛡️ 灵活的 SSL 方案**:

  * **单域名证书**: 通过 HTTP-01 验证，自动为您的指定域名申请和续期证书。

  * **泛域名证书**: 通过 DNS-01 验证，可全自动申请和续期泛域名证书，轻松管理多个子域名。

* **🗑️ 完整的生命周期管理**: 不仅支持一键部署，还提供安全的 `--remove` 选项，可以彻底、干净地卸载指定域名的所有相关配置和证书。

* **🌍 全面的系统兼容**: 支持主流 Linux 发行版 (Debian, Ubuntu, CentOS, Fedora, Arch, Alpine 等)。

## 🚀 使用方法

### 依赖项

1. 一台拥有公网 IP 的 VPS 或服务器。

2. 一个域名，并已将其 DNS A/AAAA 记录指向您的服务器 IP。

3. (可选) 如果您希望申请泛域名证书，需要准备好您的 DNS 服务商的 API 密钥 (例如 Cloudflare 的 API Token)。

### 一、在线执行

#### 方式 A: 交互模式 (推荐新手使用)

如果您不确定如何配置，或者希望通过向导一步步操作，请使用此命令。

```bash
bash <(curl -sSL https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/deploy.sh)
```

#### 方式 B: 非交互模式 (自动化部署)

通过命令行参数传入所有配置，实现全自动部署。

```bash
curl -sSL https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/deploy.sh | bash -s -- [选项]
```

> **注意**: 在非交互模式下，如果使用 DNS 验证或卸载操作，必须通过命令行参数提供 API 密钥或确认选项。

### 二、实用示例

#### 示例 1: 部署一个单域名 HTTPS 服务

这是最常见的场景。此命令将为 `my-media.your-domain.com` 申请一个单域名证书，并反代到一个公网 Emby 服务。

```bash
curl -sSL https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/deploy.sh | bash -s -- -y https://my-media.your-domain.com -r https://shared-emby.server.com
```

#### 示例 2: 部署一个 HTTP 服务 (使用 IP 和自定义端口)

如果您没有域名，或者想在非标准端口上提供服务，可以使用此方法。脚本会自动配置一个纯 HTTP 的反向代理。

```bash
curl -sSL https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/deploy.sh | bash -s -- -y http://123.45.67.89:8080 -r https://shared-emby.server.com
```

#### 示例 3: 部署一个使用泛域名证书的服务 (DNS 验证)

假设您希望使用 `*.my-cloud.net` 的泛域名证书来访问 `media-stream.my-cloud.net`。

* `-d` 参数会自动从 `media-stream.my-cloud.net` 解析出 `my-cloud.net` 作为证书域名。

* `--dns cf` 告诉脚本使用 Cloudflare 的 DNS API 来申请泛域名证书。

* `--cf-token` 和 `--cf-account-id` 用于在非交互模式下提供 API 凭据。

```bash
curl -sSL https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/deploy.sh | bash -s -- \
  -y https://media-stream.my-cloud.net \
  -r https://another-emby-provider.com \
  -d \
  --dns cf \
  --cf-token "您的Cloudflare_API_Token" \
  --cf-account-id "您的Cloudflare_Account_ID"
```

#### 示例 4: 移除一个已部署的域名配置

此命令将安全地查找并移除与 `my-media.your-domain.com` 相关的所有 Nginx 配置和证书。在非交互模式下，必须加上 `-Y` 进行确认。

```bash
curl -sSL https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/deploy.sh | bash -s -- --remove https://my-media.your-domain.com --yes
```

### 三、参数参考

| 短选项 | 长选项 | 说明 |
| :--- | :--- | :--- |
| `-y` | `--you-domain <域名或URL>` | **(部署)** 您的访问地址。 |
| `-r` | `--r-domain <域名或URL>` | **(部署)** 您要代理的 Emby 服务器地址。 |
| `-m` | `--cert-domain <域名>` | **(部署)** 手动指定证书的根域名，适合泛域名场景。 |
| `-d` | `--parse-cert-domain` | **(部署)** 自动从 `-y` 提供的域名中解析出根域名。 |
| `-D` | `--dns <服务商>` | **(部署)** 使用 DNS API 模式申请证书 (例如: `cf`)。 |
|      | `--cf-token <TOKEN>` | **(部署)** 提供 Cloudflare API Token。 |
|      | `--cf-account-id <ID>` | **(部署)** 提供 Cloudflare Account ID。 |
|      | `--remove <域名或URL>` | **(管理)** 移除指定域名或 URL 的所有相关配置和证书。 |
| `-Y` | `--yes` | **(管理)** 在非交互模式下，自动确认移除操作。 |
| `-h` | `--help` | 显示帮助信息。 |

## 💬 反馈与贡献

遇到问题或有改进建议？欢迎在项目的 Issues 页面中提出。
