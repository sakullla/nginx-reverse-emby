# 证书管理

控制面可以为代理规则管理 ACME 证书和证书材料。

证书材料会随面板数据一起持久化。不要把证书私钥、DNS API Token 或 `panel/data/` 里的内容提交到仓库。

## HTTP 验证

HTTP 验证也就是 HTTP-01。它适合普通单域名证书，例如：

```text
https://emby.example.com
```

使用 HTTP-01 时，需要满足：

- 域名已经解析到承载规则的 VPS。
- VPS 公网可以访问 `80` 端口。
- HTTPS 访问还需要放行 `443` 端口。

HTTP 规则保存后，Agent 会在同步配置时自动申请证书；手动重试签发时，也是让 Agent 重新进入待签发状态。

## DNS 验证

当代理无法暴露 `80` 端口，或需要通配符证书时，推荐使用 DNS 验证。

Cloudflare 可通过环境变量配置 DNS 提供商和令牌：

```ini
ACME_DNS_PROVIDER=cf
CF_TOKEN=your-cloudflare-api-token
```

Cloudflare API Token 需要配置这些权限：

| 选择项 | 用途 |
| --- | --- |
| `区域 / 区域 / 读取` | 读取域名所属区域。 |
| `区域 / DNS / 读取` | 读取 DNS 记录。 |
| `区域 / DNS / 编辑` | 创建和删除证书验证用的 DNS 记录。 |

区域资源选择 **包括 / 特定区域 / 你的域名**。客户端 IP 地址筛选保持默认即可。

![Cloudflare API Token 权限配置](/screenshots/cloudflare-token-permissions.png)

不要提交 DNS 令牌或私钥。

## Relay 证书

Relay 监听器默认使用系统自动签发的 Relay CA 和监听证书。普通用户不需要手动创建 Relay 证书，也不需要维护 Pin Set。

只有你需要完全自定义 Relay TLS 信任材料时，才在 Relay 监听器里把证书来源切到“绑定已有证书”，并在高级信任策略里手动配置 Pin 或 CA。

## HTTP/3

设置下面的变量后，HTTPS 入口可以同时启用 HTTP/3：

```ini
NRE_HTTP3_ENABLED=true
```
