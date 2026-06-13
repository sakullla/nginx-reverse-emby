# 常见问题

## 为什么默认 Compose 文件使用 host 网络？

内嵌 local agent 会动态创建代理监听端口。host 网络允许这些监听器直接绑定到宿主机，而不需要修改容器端口映射（Docker bridge 无法在容器运行后自动发布新增端口）。

## 面板数据存储在哪里？

默认 Docker Compose 部署中，面板数据存储在宿主机 `./data`，挂载到容器内 `/opt/nginx-reverse-emby/panel/data`。

## 数据库可以切换吗？

可以。默认 SQLite，通过 `NRE_DATABASE_DRIVER` 和 `NRE_DATABASE_DSN` 切换到 PostgreSQL 或 MySQL；已有数据使用 `migrate-storage` 迁移，详见 [迁移](./migration.md)。

## `local` 是什么？

`local` 是 Docker Compose 默认启用的内嵌 local agent。它和控制面跑在同一个容器里，但监听端口绑定到宿主机网络。只在一台 VPS 上反代时，所有规则都选 `local` 即可。

## 远程 Agent 必须开放入站端口吗？

不需要。Agent 通过心跳主动拉取配置（heartbeat pull），NAT 环境下只要 Agent 能访问控制面即可。控制面不向 Agent 发起入站连接。代理规则本身的监听端口仍需按规则放行。

## 为什么我访问还是要挂代理？

先确认客户端里填的是你自己的入口域名，而不是后端原始地址。本项目的做法是让你的节点去访问后端，客户端访问你的节点。

再确认节点自己能访问后端：

```bash
curl -I https://origin.example.net
```

如果节点到后端不通，规则保存成功也无法解决访问问题。

## HTTP 规则和 L4 规则怎么选？

反代 Web 入口、需要按域名 / 路径 / HTTP 头处理流量，优先用 HTTP 规则——它理解前端 URL、后端 URL、重定向和代理头，适合浏览器、电视盒子和 Emby / Jellyfin 客户端。

只需要转发 TCP / UDP 端口，或要把流量接入 Relay 链路时，再使用 L4 规则。

## 何时关闭 302/307 改写？

以下场景可在 HTTP 规则的 **代理 302/307 重定向** 处关闭：

- CDN 回源需要保留原始重定向地址。
- 多跳转链接需要客户端直接访问。
- OAuth 回调需要重定向地址原样传递。

## 证书需要手动申请吗？

不需要。HTTP 规则保存后，Agent 会在同步时自动用 HTTP-01 申请证书。无法开放 `80` 端口或需要通配符证书时，配置 Cloudflare DNS-01，详见 [证书与 HTTPS](../guide/certificates.md)。

## 流量额度超了会怎样？

开启 **超额阻断** 后，节点在计费周期内累计达到月度额度会被阻断，并在节点列表和仪表盘标记阻断状态。详见 [流量统计与额度](../reference/traffic.md)。

## 如何升级 Agent？

在 **版本策略** 页面创建策略，设置 `desired_version` 和各平台安装包（URL + SHA256）。Agent 心跳时收到策略后会下载、校验并原子更新。详见 [开发与构建](../reference/development.md#版本更新)。

## 根目录 docs 还能继续放临时文档吗？

可以。公开文档站源码位于 `docs-site/`，根目录 `docs/` 可继续放临时设计、计划或内部文档。
