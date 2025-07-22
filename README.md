# Nginx-Reverse-Emby: 一站式 Emby 反向代理部署脚本

一站式 Emby 反向代理部署脚本，智能处理重定向，全面支持现代网络协议，让您的影音服务配置从未如此简单。

## ✨ 核心功能

* **🔄 智能重定向处理**: 透明拦截并处理 `301`, `302`, `307` 等重定向，确保媒体播放无缝衔接，永不中断。

* **⚡️ 现代协议支持**: 完全兼容 HTTP/2 和 HTTP/3 (QUIC)，同时支持 IPv4/IPv6，提供最低延迟的连接体验。

* **🚀 一键自动化部署**: 自动检测系统、安装 Nginx 和 acme.sh，从零到一，全程自动化，无需手动干预。

* **🛡️ 灵活的 SSL 方案**: 无论是单个域名还是泛域名证书，脚本都能智能处理，自动完成申请、配置和续期。

* **🌍 全面的系统兼容**: 支持 Debian, Ubuntu, CentOS, Fedora, Arch, Alpine 等所有主流 Linux 发行版。

## 🚀 快速开始：使用方法

### 在线执行

#### 方式 A: 交互模式 (推荐新手使用)

如果您不确定如何配置，或者希望通过向导一步步操作，请使用此命令。它会以交互模式启动，并询问您所有必要的信息。

```
bash <(curl -sSL [https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/deploy.sh](https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/deploy.sh))
```

#### 方式 B: 非交互模式 (自动化部署)

通过命令行参数传入所有配置，实现全自动部署。

```
curl -sSL [https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/deploy.sh](https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/deploy.sh) | bash -s -- [选项]
```

### 实用示例

#### 示例 1: 为单个域名启用 HTTPS

这是最常见的场景。脚本将为 `emby.yourdomain.com` 申请一个单域名证书，并反代到本地的 Emby 服务。

```
curl -sSL [https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/deploy.sh](https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/deploy.sh) | bash -s -- -y [https://emby.yourdomain.com](https://emby.yourdomain.com) -r [http://127.0.0.1:8096](http://127.0.0.1:8096)
```

## 📋 参数参考

| 短选项 | 长选项 | 说明 | 
| :--- | :--- | :--- |
| `-y` | `--you-domain <域名或URL>` | **(必填)** 您的访问地址，可以是纯域名或完整 URL。 | 
| `-r` | `--r-domain <域名或URL>` | **(必填)** 您要代理的 Emby 服务器地址。 | 
| `-m` | `--cert-domain <域名>` | 手动指定证书的根域名。用于 Nginx 配置的证书路径，尤其适合泛域名场景。 | 
| `-d` | `--parse-cert-domain` | 自动从 `-y` 提供的域名中解析出根域名作为证书域名。 | 
| `-D` | `--dns <服务商>` | **(高级)** 使用 DNS API 模式申请证书 (例如: `cf`)。这是申请泛域名证书的**必须**选项。 | 
| `-h` | `--help` | 显示帮助信息。 | 

## 💬 反馈与贡献

遇到问题或有改进建议？欢迎在 [GitHub Issues](https://github.com/sakullla/nginx-reverse-emby/issues) 中提出。
