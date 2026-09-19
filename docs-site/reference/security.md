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

通用 secret vault 的 `PANEL_VAULT_MASTER_KEY`（或用于派生它的 panel token）必须与数据库作为同一个恢复点保存。已有 ciphertext 后不要单独更换 token、key 或 key ID；按[环境变量速查](./environment-variables.md#通用-secret-vault)提供 previous key 输入完成事务性轮换。

内部 PKI 使用独立的 32-byte binary master key。默认 key 位于 `data/pki/master.key`；若设置 `NRE_PKI_MASTER_KEY_FILE`，还要把其私有父目录纳入同一备份。目录保持 `0700`、文件保持 `0600`，并允许控制面在受保护恢复时原子替换文件；只读单文件或不可变 secret projection 不受支持。

## 面板访问控制

- 默认 `docker-compose.yaml` 只监听 `127.0.0.1:8080`。首次登录建议通过 SSH 隧道访问 `http://127.0.0.1:8080`。
- 面板可以给自己提供 HTTPS：登录后创建 `https://panel.example.com -> http://127.0.0.1:8080` 的 HTTP 规则，使用 `local` Agent 自动申请证书并代理回控制面。
- 面板自身 HTTPS 可用后，设置 `NRE_PUBLIC_URL=https://你的面板域名`，让 join script 和 Agent 更新 URL 使用固定可信地址。
- 不要把面板 8080 端口直接暴露到公网 HTTP。如果需要公网监听，必须配合防火墙限制来源 IP。
- 一键部署创建的 bundled local-Agent 自代理会清洗并重写 `X-Forwarded-*`，脚本会为它启用 `NRE_TRUST_FORWARDED_HEADERS`。其它上游代理只有具备相同行为时才能开启，直连保持关闭。

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
