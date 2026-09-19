# Telegram 发布稿（直接复制下面正文）

---

🚀 **Nginx-Reverse-Emby v1.5.0 发布**

相对 v1.4.1 的一次大更新。登录方式没变，还是 `API_TOKEN`。

**这次主要多了什么**

• **插件市场**：面板里直接安装 / 升级官方签名插件。加速源装好后，HTTP 规则后端可以选「插件提供商」，不用自己填插件端口；Cloudflare Token 也可以按域名映射；WAF 可以挂到 HTTP 规则上做观察。

• **内部 Relay PKI**：生产 Relay 走双向 TLS。证书中心现在分两块——公网 HTTPS，和内部 PKI（轮转、撤销、重登记、加密备份）。

• **更稳的反代和热升级**：上游 HTTP/2 不行会回退 HTTP/1.1；独立 Linux Agent 下更新包时心跳不会卡住，旧连接继续排空。

文档：https://sakullla.github.io/nginx-reverse-emby/
Release：https://github.com/sakullla/nginx-reverse-emby/releases/tag/v1.5.0

**怎么升级**

```
cd nginx-reverse-emby
docker compose pull
docker compose up -d
```

镜像：`sakullla/nginx-reverse-emby:1.5.0` 或 `:latest`

用了 Relay 的先看这一页，激活内部 PKI 之后不会再回到旧的 pin / 单向 TLS：
https://sakullla.github.io/nginx-reverse-emby/operations/internal-pki

要用官方插件的话，控制面得出得了 GitHub（或配 HTTP 代理）。

---

另外推荐两个同作者的轻量项目，和本面板很搭：

📝 **LightInk** — Markdown 优先的轻量博客 / 知识库，静态生成，想挂文档站或个人笔记很合适。
https://github.com/sakullla/LightInk

🎬 **Rillight** — 轻量视频分享，Docker 一键拉起，可以对接 Emby / Jellyfin，给家人朋友传片子不用上完整媒体库。
https://github.com/sakullla/Rillight

域名解析到 VPS 后，用 nginx-reverse-emby 给它们套 HTTPS 即可。
