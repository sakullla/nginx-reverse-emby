# 常见问题

## 为什么默认 Compose 文件使用 host 网络？

内嵌 local agent 可以动态创建代理监听端口。host 网络允许这些监听器直接绑定到宿主机，而不需要修改容器端口映射。

## 面板数据存储在哪里？

默认 Docker Compose 部署中，面板数据存储在宿主机 `./data`，并挂载到容器内。

## 根目录 docs 还能继续放临时文档吗？

可以。公开文档站源码位于 `docs-site/`，不会占用根目录 `docs/`。

## 为什么我看 Emby 还是要挂代理？

先确认播放器里填的是你自己的加速入口域名，而不是公费服/公益服给你的源站域名。Nginx-Reverse-Emby 的作用是让你的 VPS 去访问源站，客户端访问你的 VPS。

还要确认这台 VPS 自己能访问源站：

```bash
curl -I https://origin.emby.example.net
```

如果 VPS 到源站也不通，HTTP 规则保存成功也无法解决播放问题。

## HTTP 规则和 L4 规则怎么选？

反代 Emby/Jellyfin Web 入口，优先用 HTTP 规则。HTTP 规则知道前端 URL、后端 URL、重定向和代理头，适合浏览器、电视盒子和 Emby 客户端。

只需要转发 TCP/UDP 端口，或者要把 L4 流量接入 Relay 链路时，再使用 L4 规则。

## 面板显示 local 是什么？

`local` 是 Docker Compose 默认启用的内嵌 local agent。它和控制面跑在同一个容器里，但监听端口会绑定到宿主机网络上。

## 何时需要关闭 302/307 代理？

以下场景可以在 HTTP 规则中关闭“代理 302/307 重定向”：

- CDN 回源需要保留原始重定向地址。
- 多跳转链接需要客户端直接访问。
- OAuth 回调需要保持重定向地址原样传递。
