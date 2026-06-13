---
layout: home

hero:
  name: Nginx-Reverse-Emby
  text: 新手也能照着部署的反向代理面板
  tagline: 从 Docker Compose 启动，到在面板里添加 HTTP 规则，把 Emby、Jellyfin 或任意 Web 服务代理出去。
  actions:
    - theme: brand
      text: 从 0 到 HTTP 代理
      link: /guide/getting-started
    - theme: alt
      text: 添加 HTTP 规则
      link: /guide/http-rule
    - theme: alt
      text: GitHub
      link: https://github.com/sakullla/nginx-reverse-emby

features:
  - title: 先跑起来
    details: 准备服务器、改好 API_TOKEN，然后用 docker compose up -d 启动面板。
  - title: 再添加规则
    details: 在真实面板里选择本机 Agent，填写前端访问域名和后端服务地址。
  - title: 最后验证访问
    details: 配好 DNS 和端口后，浏览器访问你的域名，确认已经代理到 Emby 或 Jellyfin。
---
