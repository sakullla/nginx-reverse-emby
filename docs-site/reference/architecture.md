# 架构与设计

Nginx-Reverse-Emby 是一个纯 Go 实现的反向代理控制面，为 Emby、Jellyfin 以及任意 HTTP/TCP 服务设计。默认通过 Docker Compose 部署，内置一个 `local` Agent。

## 组件关系

```text
控制面
├─ Vue 3 面板
├─ Go API 服务
│  ├─ /api/* 和 /panel-api/* 路由
│  ├─ Agent 注册与管理
│  ├─ 规则、证书、Relay、WireGuard、出口配置存储
│  ├─ 流量统计与额度
│  └─ 版本策略分发
├─ local Agent（内置）
│  ├─ HTTP 代理引擎
│  ├─ TCP/UDP 代理
│  ├─ Relay 隧道
│  └─ WireGuard / 流量采集
└─ SQLite / PostgreSQL / MySQL
```

**远程 Agent** 在其他服务器上运行，通过心跳拉取模式连接控制面。Agent 只需要出站网络访问控制面，控制面从不主动连接 Agent——这让 NAT 和防火墙配置极简。

## 面板布局

```text
仪表盘
  ├─ 节点状态
  ├─ 流量概览
  └─ 热门规则和节点

流量管理
  ├─ HTTP 规则
  └─ L4 规则

基础设施
  ├─ 证书
  ├─ Relay 监听器
  ├─ WireGuard 配置
  └─ 节点管理

设置
  ├─ 常规
  ├─ 出口配置
  ├─ 数据管理
  └─ 关于

版本策略（独立页面）
```

## 请求流程

```text
浏览器 → Go 控制面
  → 认证的 /api/* 路由
  → /panel-api/* 兼容别名
  → 公共 Agent 资源（join-agent.sh、Agent 二进制）
  → 构建好的前端静态文件 / SPA 回退
```

## Agent 同步流程

1. 控制面存储每个 Agent 的期望配置和期望版本
2. Agent 定期向控制面发送心跳 / 同步请求
3. 控制面返回 HTTP 规则、L4 规则、Relay 监听器、证书和版本信息
4. Agent 在本地应用配置，下次心跳报告当前状态

## 数据存储

默认 **SQLite**，数据在 `./data` 目录。不需要额外配置。

切换 PostgreSQL 或 MySQL 时设置 `NRE_DATABASE_DRIVER` 和 `NRE_DATABASE_DSN`。迁移见 [数据迁移](../operations/migration.md)。

| 数据库 | 驱动值 | DSN 示例 |
|--------|--------|----------|
| SQLite（默认） | `sqlite` | 自动检测 |
| PostgreSQL | `postgres` | `postgres://user:pass@host:5432/db?sslmode=disable` |
| MySQL | `mysql` | `user:pass@tcp(host:3306)/db?parseTime=true&charset=utf8mb4` |

## 为什么用 host 网络

控制面在运行时动态创建监听端口（HTTP 规则、L4 规则、Relay 监听器）。Docker bridge 网络无法在容器启动后新增端口映射。host 网络模式让 `local` Agent 直接绑定宿主机网络接口。

::: warning
规则中配置的端口直接占用宿主机端口。检查冲突并放行防火墙。
:::
