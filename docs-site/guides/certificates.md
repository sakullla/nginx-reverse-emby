# 证书与 HTTPS

要让访客通过 HTTPS 安全访问你的网站，你需要 TLS 证书。本项目内置了完整的证书管理功能，无需手动去命令行操作。

简单来说，证书就是一把「电子钥匙」：
- **公钥（证书）** 发给浏览器，用来加密数据
- **私钥** 留在服务器上，用来解密数据

控制面可以帮你自动申请、续期和管理这些证书，支持三种方式：
- **自动签发** — 通过 ACME 协议（Let's Encrypt 等）自动申请和续期
- **手动上传** — 你自己有证书时直接上传
- **内部自签** — 系统内部使用的证书

::: warning 不要提交敏感材料
不要把证书私钥、DNS API Token 或 `panel/data/` 里的内容提交到仓库。
:::

## 证书管理页面

打开 **基础设施 → 证书管理**，这里就是管理所有证书的地方。

创建证书时，先选一个用途模板，系统会帮你填好大部分设置：

| 模板 | 用途 |
| --- | --- |
| **HTTPS 入口** | 给普通的 HTTP 代理规则使用，例如 `https://app.example.com` |
| **Relay 监听** | 给 Relay 隧道监听器使用 |
| **混合用途** | 同一份证书既用于 HTTPS 又用于 Relay |
| **IP 证书** | 给 IP 地址（而非域名）签发证书 |

高级设置里可以进一步调整：

| 字段 | 说明 |
| --- | --- |
| 证书类型 | 域名证书（最常见）或 IP 证书 |
| 签发模式 | Master 统一签发，或节点本地各自签发 |
| 用途 | HTTPS、Relay 隧道、系统 Relay CA 或混合用途 |
| 证书来源 | 自动签发（推荐）、手动上传、内部自签 |

证书管理页面如下：

![证书管理列表](/screenshots/panel-certificates.png)

点击 **新建证书** 后，表单如下：

![创建证书表单](/screenshots/panel-certificate-form.png)

## HTTP-01 验证（推荐新手使用）

HTTP-01 是最简单的方式，适合普通单域名证书，比如：

```text
https://app.example.com
```

使用 HTTP-01 需要满足以下条件：

1. **域名已解析** — 你的域名要指向运行代理规则的节点（A 记录或 CNAME）
2. **80 端口开放** — 验证阶段需要临时占用 80 端口，让证书颁发机构能访问到验证文件
3. **443 端口开放** — 实际提供 HTTPS 服务时需要

**工作流程**：你创建规则并保存后，Agent 在下次同步配置时会自动申请证书。如果申请失败，在证书页面点击手动重试，Agent 会重新进入申请流程。

HTTP-01 不需要额外配置 DNS Token，是最省心的方式。

## DNS-01 验证（Cloudflare）

当你遇到以下情况时，需要使用 DNS-01：
- 节点无法暴露 80 端口（比如在内网、或有其他服务占用）
- 需要通配符证书（如 `*.example.com`）
- 需要给内网域名签发证书

目前内置支持 Cloudflare DNS，配置如下：

```ini
ACME_DNS_PROVIDER=cf
CF_TOKEN=your-cloudflare-api-token
```

只有同时满足 `ACME_DNS_PROVIDER=cf` 且 `CF_TOKEN` 非空时，DNS-01 才会启用。

### 如何获取 Cloudflare API Token

1. 登录 [Cloudflare Dashboard](https://dash.cloudflare.com)
2. 进入 **My Profile → API Tokens → Create Token**
3. 使用 **Custom Token** 模板，配置以下权限：

| 权限项 | 用途 |
| --- | --- |
| `区域 / 区域 / 读取` | 读取域名所属的区域信息 |
| `区域 / DNS / 读取` | 读取现有的 DNS 记录 |
| `区域 / DNS / 编辑` | 创建和删除证书验证用的临时 DNS 记录 |

4. **区域资源** 选择 **包括 / 特定区域 / 你的域名**
5. **客户端 IP 地址筛选** 保持默认（不限制）

![Cloudflare API Token 权限配置](/screenshots/cloudflare-token-permissions.png)

### Token 环境变量名

控制面接受多种环境变量名来读取 Token，按优先级依次尝试：

- `CF_TOKEN`
- `CLOUDFLARE_DNS_API_TOKEN`
- `CF_DNS_API_TOKEN`
- `CF_Token`

可选的 Zone Token（用于读取区域信息）：
- `CLOUDFLARE_ZONE_API_TOKEN`
- `CF_ZONE_API_TOKEN`

## 手动上传证书

如果你已经有证书了（比如自签证书、商业证书、或公司内网 CA 签发的），可以直接上传：

1. 在证书表单把 **证书来源** 选为 **手动上传**
2. 填入以下内容：
   - **证书 PEM** — 证书内容（通常以 `-----BEGIN CERTIFICATE-----` 开头）
   - **私钥 PEM** — 私钥内容（通常以 `-----BEGIN PRIVATE KEY-----` 或 `-----BEGIN RSA PRIVATE KEY-----` 开头）
   - **CA 链 PEM**（可选）— 中间证书或根证书
3. 如果是自签证书，开启 **标记为自签名证书**

::: tip 手动上传的证书不会自动续期
到期前你需要手动更新。建议优先使用自动签发。
:::

## Relay 证书

Relay 监听器默认使用系统自动签发的 Relay CA 和监听证书。**普通用户不需要手动创建 Relay 证书，也不需要维护 Pin Set。**

只有以下特殊情况才需要手动配置：
- 你需要完全自定义 Relay 隧道的 TLS 信任链
- 你有特殊的合规或安全要求

如需自定义，在 Relay 监听器里把证书来源切到 **绑定已有证书**，并在高级信任策略里手动配置 Pin 或 CA。详见 [Relay 参考](../reference/relay.md)。

## 自动续期

证书不是永久有效的，Let's Encrypt 的证书有效期为 90 天。控制面会自动帮你续期：

- 检查间隔由 `NRE_MANAGED_CERT_RENEW_INTERVAL` 控制，默认每 **24 小时** 检查一次
- 对临近过期的证书自动发起续期申请
- 续期成功后自动下发到各 Agent

::: tip DNS-01 续期需要保持 Token 可用
如果你使用 Cloudflare DNS-01，续期时仍需要 `CF_TOKEN` 有效。如果 Token 过期或失效，自动续期会失败。
:::

## HTTP/3

HTTP/3 是新一代 HTTP 协议，基于 QUIC，可以减少连接建立延迟。开启后，HTTPS 入口会同时支持 HTTP/3：

```ini
NRE_HTTP3_ENABLED=true
```

::: tip 兼容性说明
HTTP/3 需要客户端和浏览器支持。开启后不会影响 HTTP/1.1 和 HTTP/2 的访问，三者会共存。
:::
