# 证书与 HTTPS

要让访客通过 HTTPS 安全访问你的服务，你需要 TLS 证书。控制面可以自动申请、续期和管理证书，不需要手动操作命令行。

## 三种证书来源

| 方式 | 适合场景 |
| --- | --- |
| **自动签发（HTTP-01）** | 有域名、能开放 80/443 端口。推荐首选 |
| **DNS-01 验证** | 无法开放 80 端口，或需要通配符证书（`*.example.com`） |
| **手动上传** | 已有商业证书或自签证书 |

## 证书管理页面

进入 **基础设施 → 证书管理**。这里管理公网 HTTPS/ACME 与手动上传的业务证书；创建时选一个模板，系统会帮你填好大部分设置：

| 模板 | 说明 |
| --- | --- |
| 网站 HTTPS | 给 HTTP 代理规则使用 |
| 手动上传证书 | 粘贴已有 PEM 证书、私钥和可选 CA 链 |

Relay 的 tunnel identity、内部 CA、轮转和撤销属于另一安全域。证书管理页右上角提供 **内部 PKI** 入口；侧栏不会再增加一个平行 PKI 菜单。

![证书管理列表](/screenshots/panel-certificates.png)

![创建证书表单](/screenshots/panel-certificate-form.png)

## HTTP-01 自动签发（推荐）

最简单的方式。创建 HTTPS 入口的 HTTP 规则后，Agent 自动完成申请，无需额外配置。

前置条件：
1. 域名已解析到运行规则的 Agent 节点
2. Agent 节点 80 端口可访问（用于验证域名所有权）
3. 443 端口放行（提供 HTTPS 服务）

如果申请失败，去证书页面点击重试。

## DNS-01 验证（Cloudflare）

以下情况需要用 DNS-01：
- 节点无法开放 80 端口（内网、端口被占用）
- 需要通配符证书（`*.example.com`）
- 给内网域名签发证书

在控制面的环境变量中配置：

```ini
ACME_DNS_PROVIDER=cf
CF_TOKEN=your-cloudflare-api-token
```

两个变量都配置且非空时，DNS-01 才会启用。

### 获取 Cloudflare API Token

1. 登录 [Cloudflare Dashboard](https://dash.cloudflare.com)
2. 进入 **My Profile → API Tokens → Create Token**，选 Custom Token
3. 配置权限：

| 权限 | 用途 |
| --- | --- |
| 区域 / 区域 / 读取 | 读取域名区域信息 |
| 区域 / DNS / 读取 | 读取现有 DNS 记录 |
| 区域 / DNS / 编辑 | 创建和删除验证用的临时记录 |

4. 区域资源选 **特定区域 / 你的域名**
5. 不限制客户端 IP

![Cloudflare API Token 权限](/screenshots/cloudflare-token-permissions.png)

Token 支持多种环境变量名，按优先级依次尝试：`CLOUDFLARE_DNS_API_TOKEN` > `CF_DNS_API_TOKEN` > `CF_TOKEN` > `CF_Token`。填任意一个即可，同时设置了多个时优先级高的生效。

::: warning Token 安全
CF_TOKEN 能操作 DNS 记录，不要提交到仓库。定期轮换 Token。更多安全建议见 [安全最佳实践](../reference/security.md)。
:::

## 手动上传证书

已有证书时直接上传：

1. 创建证书时把「证书来源」选为 **手动上传**
2. 填入证书 PEM、私钥 PEM，可选填 CA 链 PEM
3. 自签证书需勾选「标记为自签名证书」

::: tip 手动上传的证书不会自动续期
到期前需要手动更新。优先使用自动签发。
:::

## 内部 Relay PKI

生产 Relay mTLS 使用内部 PKI 签发的 Agent/listener tunnel identity，不从本页创建，也不能用公网 ACME 或普通手动上传证书替代。请从本页的 **内部 PKI** 入口查看 identity、CA generation、有效期、轮转、撤销和受保护备份。既有 Relay CA/Pin 配置仅在维护升级激活前保留；完成激活后不能作为降级或恢复路径。详见[内部 PKI 升级与运维](../operations/internal-pki.md)和 [Relay 协议内幕](../reference/relay-internals.md)。

## 自动续期

Let's Encrypt 证书有效期 90 天。控制面每 24 小时检查一次，对临近到期的证书自动续期。

DNS-01 续期需要 CF_TOKEN 持续有效。Token 过期会导致续期失败。

## HTTP/3

```ini
NRE_HTTP3_ENABLED=true
```

开启后 HTTPS 入口同时支持 HTTP/3（QUIC），不影响 HTTP/1.1 和 HTTP/2。需要客户端和浏览器支持。
