# 安全最佳实践

本页汇总了让 nginx-reverse-emby 更安全的关键措施。每一条都值得花几分钟确认。

## 密码和令牌

- `API_TOKEN` 用 32 位以上随机字符串，包含大小写字母和数字。不要用简单密码。
- `MASTER_REGISTER_TOKEN` 单独设置一个不同的值，不要和 `API_TOKEN` 共用。
- 这两个 Token 都不要提交到 Git 仓库，不要在公开渠道（聊天记录、论坛、截图）中暴露。

## data 目录保护

`./data` 目录包含 SQLite 数据库、证书私钥、Agent Token 等敏感信息：

- 不要提交到 Git 仓库（确保 `.gitignore` 包含 `data/`）
- 不要上传到公共网盘
- 备份文件加密后再存储到远程

## 面板访问控制

- 面板端口（默认 8080）如果暴露在公网，建议在云防火墙中限制来源 IP。
- 或者在面板前面加一层反向代理，配上 HTTPS 和 HTTP Basic Auth。

## 防火墙

只放行真正需要的端口，不要一把全开：

- 面板端口：限制来源 IP
- HTTP 入口：80
- HTTPS 入口：443
- 自定义监听端口：按实际规则逐个放行

## 证书安全

- 证书私钥不要复制传播，不要提交到仓库
- Cloudflare API Token 只授予必要权限（区域读取 + DNS 读取/编辑），定期轮换
- 详情见 [证书与 HTTPS](../guides/certificates.md)

## Agent 注册安全

- `MASTER_REGISTER_TOKEN` 是 Agent 注册的唯一凭证，泄露后任何人都可以注册 Agent 接入你的控制面
- 定期检查 **节点管理** 页面，移除不认识的 Agent

## 数据库安全

- 如果使用 PostgreSQL 或 MySQL，数据库密码独立设置，不要复用其他密码
- 数据库端口不要暴露在公网

## 及时升级

定期更新控制面和 Agent 到最新版本以获取安全修复。更新前先备份 data 目录，以防万一。
