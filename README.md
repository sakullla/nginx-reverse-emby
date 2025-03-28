# nginx-reverse-emby

## 项目简介

`nginx-reverse-emby` 是一个用于配置 Nginx 反向代理 Emby 服务器的脚本，适用于 Emby 公费服或机场服。它支持 HTTP 和 HTTPS 连接，并允许自定义前端和后端端口。

### 功能特性

- 支持单个域名的反代，并可实现 307 重定向。对于 301 和 302 的重定向。
- 代理后的 Emby 服务器兼容 HTTP/1.1、HTTP/2、HTTP/3，支持 IPv4 和 IPv6 访问。
- 允许代理多个 Emby 实例。

## 使用方法

运行 `deploy.sh` 脚本时，可以提供以下参数来自定义配置：

### 必填参数

| 参数   | 长格式参数               | 说明              | 示例            |
| ---- | ------------------- | --------------- | ------------- |
| `-y` | `--you-domain <域名>` | 你的域名或 IP 地址（必填） | `example.com` |
| `-r` | `--r-domain <域名>`   | 反代 Emby 的域名（必填） | `backend.com` |

### 可选参数

| 参数   | 长格式参数                      | 说明                              | 示例                 |
| ---- | -------------------------- | ------------------------------- | ------------------ |
| `-P` | `--you-frontend-port <端口>` | 你的前端访问端口（默认: 443）               | `443`              |
| `-p` | `--r-frontend-port <端口>`   | 反代 Emby 的前端端口（默认: 空）            | `8096`             |
| `-f` | `--r-http-frontend` |  使用 HTTP 访问反代 Emby前端（默认: 使用 HTTPS），提供该参数则改为 HTTP | `提供该参数即生效` |
| `-s` | `--no-tls` | 默认使用 HTTPS 进行反代（需要域名或自签证书），提供该参数则改为使用 HTTP | `提供该参数即生效` |
| `-h` | `--help`                   | 显示帮助信息                          | `./deploy.sh -h`   |

### 交互模式

如果未提供 `-y`（你的域名）或 `-r`（反代 Emby 域名），脚本将进入交互模式，并引导用户完成配置。

### 示例命令

#### 通过 `curl` 在线执行（直接传参，推荐）

```bash
curl -sSL https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/deploy.sh | bash -s -- -y yourdomain.com -r backend.com
```

#### 通过 `curl` 在线执行（交互模式）

```bash
bash <(curl -sSL https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/deploy.sh)
```

#### 本地运行（需下载并授予权限）

```bash
curl -O https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/main/deploy.sh
chmod +x deploy.sh
./deploy.sh -y example.com -r backend.com -P 443 -p 8096 -f -s
```

## 依赖项

- 需要一台 VPS 服务器（建议选择带有公网 IP 的 VPS，以确保可以正常访问和配置反向代理）。

- 当使用 HTTPS 反代时，需要拥有一个有效的域名，并确保该域名的 DNS 解析正确配置到服务器的公网 IP 地址。此外，建议提前检查 DNS 解析是否生效，以避免部署过程中出现访问异常。

- 你可以在以下平台购买域名，并进行 DNS 配置（以下仅为参考）：

    - [Porkbun](https://porkbun.com/)（价格实惠，隐私保护较好）
    - [Namecheap](https://www.namecheap.com/)（价格较低，适合个人用户）
    - [Cloudflare Registrar](https://www.cloudflare.com/products/registrar/)（无额外溢价，适合已有 Cloudflare 用户）

- 推荐使用以下 DNS 解析服务进行域名解析（以下仅为参考）：

    - [Cloudflare DNS](https://developers.cloudflare.com/dns/)（快速、免费，提供 DDoS 保护）


---

如果你在使用过程中遇到问题，欢迎提交 issue 进行反馈！

