---
layout: home

hero:
  name: Nginx-Reverse-Emby
  text: 用自己的 VPS 反代 Emby 源站
  tagline: 面向已经有优化线路 VPS、购买或加入公费服/公益服 Emby/Jellyfin 的用户，按教程部署后把观看入口固定到自己的域名。
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
  - title: 先部署到 VPS
    details: 准备能访问源站的优化线路 VPS，改好 API_TOKEN，用 Docker Compose 启动控制面和 local 节点。
  - title: 再添加 HTTP 规则
    details: 在真实面板里选择 local，填写自己的加速入口域名和公费服/公益服给出的 Emby 源站地址。
  - title: 最后换入口观看
    details: 配好 DNS 和端口后，播放器访问你的域名，由 VPS 连接源站，减少观看时必须挂代理的麻烦。
---
