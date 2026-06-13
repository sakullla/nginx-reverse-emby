# 证书管理

控制面可以为代理规则管理 ACME 证书和证书材料。

## HTTP 验证

HTTP 验证要求公网可以访问 `80` 端口。

## DNS 验证

当代理无法暴露 `80` 端口，或需要通配符证书时，推荐使用 DNS 验证。

Cloudflare 可通过环境变量配置 DNS 提供商和令牌：

```ini
ACME_DNS_PROVIDER=cf
CF_TOKEN=your-cloudflare-api-token
```

不要提交 DNS 令牌或私钥。
