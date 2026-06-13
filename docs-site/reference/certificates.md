# 证书管理

控制面可以为代理规则管理 ACME 证书和证书材料。

证书材料会随面板数据一起持久化。不要把证书私钥、DNS API Token 或 `panel/data/` 里的内容提交到仓库。

## HTTP 验证

HTTP 验证要求公网可以访问 `80` 端口。

## DNS 验证

当代理无法暴露 `80` 端口，或需要通配符证书时，推荐使用 DNS 验证。

Cloudflare 可通过环境变量配置 DNS 提供商和令牌：

```ini
ACME_DNS_PROVIDER=cf
CF_TOKEN=your-cloudflare-api-token
```

Cloudflare API Token 需要选择 `区域 / 区域 / 读取`、`区域 / DNS / 读取` 和 `区域 / DNS / 编辑` 权限。

不要提交 DNS 令牌或私钥。

## Relay 证书

Relay 监听器默认使用系统自动签发的 Relay CA 和监听证书。普通用户不需要手动创建 Relay 证书，也不需要维护 Pin Set。

只有你需要完全自定义 Relay TLS 信任材料时，才在 Relay 监听器里把证书来源切到“绑定已有证书”，并在高级信任策略里手动配置 Pin 或 CA。

## HTTP/3

设置下面的变量后，HTTPS 入口可以同时启用 HTTP/3：

```ini
NRE_HTTP3_ENABLED=true
```
