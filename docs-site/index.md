---
layout: home

hero:
  name: Nginx-Reverse-Emby
  text: 纯 Go 反向代理控制面
  tagline: 用一个轻量面板管理 Emby、Jellyfin、HTTP、L4、Relay、WireGuard、证书和多节点代理规则。
  actions:
    - theme: brand
      text: 快速开始
      link: /guide/getting-started
    - theme: alt
      text: Docker Compose
      link: /guide/docker-compose
    - theme: alt
      text: GitHub
      link: https://github.com/sakullla/nginx-reverse-emby

features:
  - title: 纯 Go 运行时
    details: 默认打包运行时中的控制面和执行面不依赖 Nginx。
  - title: 多节点 Agent
    details: 远程 Agent 通过心跳从 Master 拉取期望状态，NAT 节点无需开放入站访问。
  - title: 证书与 Relay
    details: 在面板中统一管理 ACME 证书、HTTP/L4 代理规则、Relay 传输和 WireGuard Profile。
---
