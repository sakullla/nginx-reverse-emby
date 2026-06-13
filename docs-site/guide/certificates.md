# 证书与 HTTPS

控制面可以为代理规则统一管理证书：ACME 自动签发（HTTP-01 / Cloudflare DNS-01）、手动上传，以及 Relay 监听器专用的自动签发 CA。证书材料随面板数据持久化。

::: warning 不要提交敏感材料
不要把证书私钥、DNS API Token 或 `panel/data/` 里的内容提交到仓库。
:::

## 证书管理页面

在 **基础设施 → 证书管理** 中可以创建和管理证书。创建时可选择用途模板：

- **HTTPS 入口** —— 给 HTTP 规则的 HTTPS 前端使用。
- **Relay 监听** —— 给 Relay 监听器使用。
- **混合用途** —— 同一份证书既用于 HTTPS 又用于 Relay。
- **IP 证书** —— 给 IP 地址签发证书。

高级设置里可以选择：

| 字段 | 说明 |
| --- | --- |
| 证书类型 | 域名证书或 IP 证书。 |
| 签发模式 | Master 统一签发，或节点本地签发。 |
| 用途 | HTTPS、Relay 隧道、系统 Relay CA 或混合用途。 |
| 证书来源 | 自动签发、手动上传、内部自签。 |

对于普通 HTTPS 前端，使用默认的自动签发即可。

## HTTP-01 验证（推荐入门）

HTTP-01 适合普通单域名证书，例如：

```text
https://app.example.com
```

使用 HTTP-01 需要满足：

- 域名已解析到承载规则的节点。
- 节点公网可访问 `80` 端口（验证阶段临时占用）。
- HTTPS 访问还需放行 `443` 端口。

规则保存后，Agent 在同步配置时自动申请证书；手动重试签发时，也是让 Agent 重新进入待签发状态。HTTP-01 不需要额外配置 DNS Token。

## DNS-01 验证（Cloudflare）

当节点无法暴露 `80` 端口，或需要通配符证书时，使用 DNS-01。目前内置支持 Cloudflare：

```ini
ACME_DNS_PROVIDER=cf
CF_TOKEN=your-cloudflare-api-token
```

DNS-01 仅在 `ACME_DNS_PROVIDER=cf` 且 Token 非空时启用。

Cloudflare API Token 需要配置这些权限：

| 选择项 | 用途 |
| --- | --- |
| `区域 / 区域 / 读取` | 读取域名所属区域。 |
| `区域 / DNS / 读取` | 读取 DNS 记录。 |
| `区域 / DNS / 编辑` | 创建和删除证书验证用的 DNS 记录。 |

区域资源选择 **包括 / 特定区域 / 你的域名**；客户端 IP 地址筛选保持默认即可。

![Cloudflare API Token 权限配置](/screenshots/cloudflare-token-permissions.png)

除 `CF_TOKEN` 外，控制面也接受 `CLOUDFLARE_DNS_API_TOKEN`、`CF_DNS_API_TOKEN`、`CF_Token`；可选的 Zone Token 为 `CLOUDFLARE_ZONE_API_TOKEN` / `CF_ZONE_API_TOKEN`。

## 手动上传证书

如果已有证书（自签、商业证书或内网 CA 签发），在证书表单把 **证书来源** 选为 **手动上传**，填入：

- 证书 PEM
- 私钥 PEM
- CA 链 PEM（可选）

并把 **标记为自签名证书** 按需开启。手动上传的证书不会自动续期，到期前需自行更新。

## Relay 证书

Relay 监听器默认使用系统自动签发的 Relay CA 和监听证书，普通用户不需要手动创建 Relay 证书，也不需要维护 Pin Set。

只有需要完全自定义 Relay TLS 信任材料时，才在 Relay 监听器里把证书来源切到 **绑定已有证书**，并在高级信任策略里手动配置 Pin 或 CA。详见 [Relay 参考](../reference/relay.md)。

## 自动续期

控制面按 `NRE_MANAGED_CERT_RENEW_INTERVAL`（默认 `24h`）定期检查托管证书，对临近过期的证书自动续期。DNS-01 模式下续期需要保持 Cloudflare Token 可用。

## HTTP/3

设置以下变量后，HTTPS 入口可同时启用 HTTP/3：

```ini
NRE_HTTP3_ENABLED=true
```
