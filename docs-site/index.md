---
layout: home

hero:
  name: Nginx-Reverse-Emby
  text: 纯 Go 反向代理控制面
  tagline: 一台 VPS + 一个面板 = 任意 HTTP/HTTPS/TCP/UDP 服务反代到你的域名。内置 Relay 隧道、WireGuard、ACME 自动证书、流量额度与多节点管理。
  actions:
    - theme: brand
      text: 快速开始
      link: /getting-started/quickstart
    - theme: alt
      text: HTTP 反向代理
      link: /guides/http-rules
    - theme: alt
      text: GitHub
      link: https://github.com/sakullla/nginx-reverse-emby

features:
  - icon: 🚀
    title: 纯 Go 运行时
    details: 控制面和 Agent 都用 Go 实现，不依赖 Nginx。一个 Docker Compose 拉起控制面与 local Agent。
    link: /getting-started/deploy
    linkText: 部署方式
  - icon: 🌐
    title: HTTP / HTTPS 反代
    details: 按域名反代 Web 服务，内置断流续传、同后端重试、302/307 重定向改写与 ACME 自动证书。
    link: /guides/http-rules
    linkText: 添加 HTTP 规则
  - icon: 🔌
    title: L4 端口转发
    details: 直接转发 TCP/UDP 端口，支持多后端负载均衡、SOCKS/HTTP 入口、PROXY Protocol 与 WireGuard 监听。
    link: /guides/l4-rules
    linkText: L4 端口转发
  - icon: 🛰️
    title: Relay 隧道
    details: Agent 到 Agent 的多跳加密隧道，传输可选 TLS/TCP、QUIC 或 WireGuard，流量经中继节点送达后端。
    link: /guides/relay
    linkText: Relay 隧道
  - icon: 📊
    title: 流量统计与额度
    details: 按网卡统计入站/出站/双向流量，支持月度额度、超额阻断、计费周期与手动校准。
    link: /guides/traffic-quota
    linkText: 流量额度
  - icon: 🔒
    title: 证书管理
    details: HTTP-01 与 Cloudflare DNS-01 自动签发，或手动上传证书。Relay 监听器默认使用自动签发的 Relay CA。
    link: /guides/certificates
    linkText: 证书与 HTTPS
---
