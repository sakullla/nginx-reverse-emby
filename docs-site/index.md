---
layout: home

hero:
  name: Nginx-Reverse-Emby
  text: 纯 Go 反向代理控制面
  tagline: 一台优化线路的 VPS、一个可视化面板，把任意 HTTP / HTTPS / TCP / UDP 服务反代到自己的域名。内置 Relay 隧道、WireGuard、ACME 证书、流量额度与多节点管理。
  actions:
    - theme: brand
      text: 快速开始
      link: /guide/quickstart
    - theme: alt
      text: HTTP 反向代理
      link: /guide/http-rule
    - theme: alt
      text: GitHub
      link: https://github.com/sakullla/nginx-reverse-emby

features:
  - icon: 🚀
    title: 纯 Go 运行时
    details: 控制面和执行面都用 Go 实现，不依赖 Nginx。一个 Docker Compose 即可拉起控制面与内嵌 local agent。
    link: /guide/deploy
    linkText: 部署方式
  - icon: 🌐
    title: HTTP / HTTPS 反代
    details: 按域名反代 Web 服务，内置中断流恢复、同 backend 重试、302/307 重定向改写与 ACME 自动证书。
    link: /guide/http-rule
    linkText: 添加 HTTP 规则
  - icon: 🔌
    title: L4 端口转发
    details: 直接转发 TCP / UDP 端口，支持多后端负载均衡、SOCKS/HTTP 入口、PROXY Protocol 与 WireGuard 监听 / 出口。
    link: /guide/l4-relay
    linkText: L4 + Relay
  - icon: 🛰️
    title: Relay 隧道
    details: Agent 到 Agent 的多跳隧道，传输可选 TLS/TCP、QUIC 或 WireGuard，适合把流量经中继节点送抵后端。
    link: /reference/relay
    linkText: Relay 参考
  - icon: 📊
    title: 流量统计与额度
    details: 按网卡采集入站 / 出站 / 双向流量，支持月度额度、超额阻断、计费周期与手工校准。
    link: /reference/traffic
    linkText: 流量与额度
  - icon: 🔒
    title: 证书管理
    details: HTTP-01 与 Cloudflare DNS-01 自动签发，或手动上传证书；Relay 监听器默认使用自动签发的 Relay CA。
    link: /guide/certificates
    linkText: 证书与 HTTPS
---
