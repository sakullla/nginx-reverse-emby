# Docker Compose

Docker Compose 是大多数部署场景的推荐运行方式。

## 数据目录

Compose 文件会把面板状态、SQLite 数据、证书和运行时文件持久化到宿主机的 `./data`。

不要提交这个目录里的文件。

## 必要环境变量

```yaml
environment:
  API_TOKEN: your-secure-token
  MASTER_REGISTER_TOKEN: your-register-token
  NRE_TIMEZONE: Asia/Shanghai
```

`API_TOKEN` 用于面板访问。`MASTER_REGISTER_TOKEN` 用于 Agent 向 Master 注册。

## Host 网络模式

默认 Compose 文件使用 host 网络，因为代理监听端口由面板动态配置。Docker bridge 网络无法在容器启动后自动发布新的监听端口。
