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

另外推荐我另外两款独立软件（和本面板不是配套，各管各的）：

📖 **LightInk** — 本地电子书阅读器 + Typora 风格 Markdown 编辑器。EPUB / PDF / TXT / Markdown / 漫画都能看，所见即所得写笔记，书库、OPDS、WebDAV 都有。Win / macOS / Linux。
https://github.com/sakullla/LightInk

🎬 **Rillight** — 独立的跨平台 Emby 桌面客户端。连你现有的 Emby 服务器看电影追剧，播放用 libmpv，不是再搭一套媒体库。
https://github.com/sakullla/Rillight
