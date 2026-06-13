# 备份与恢复

使用面板的备份导出功能，可以生成包含规则、Agent、Relay 设置、证书和版本策略的可移植备份。

你也可以直接备份 Docker Compose 部署挂载的 `./data` 目录。如果使用 PostgreSQL 或 MySQL，请使用对应数据库工具备份数据。

普通备份默认不包含高容量 traffic history。

## 直接备份 data 目录

Docker Compose 默认把数据挂载到宿主机 `./data`，对应容器内：

```text
/opt/nginx-reverse-emby/panel/data
```

只使用 SQLite 时，停机后备份 `./data` 是最直接的恢复方式：

```bash
docker compose down
tar -czf nre-data-backup.tgz data
docker compose up -d
```

恢复时把 `data` 目录放回原位置，再启动 Compose。

## 面板导出

面板导出的备份包适合跨机器、跨架构迁移。普通备份包含：

- HTTP 规则
- L4 规则
- Agent
- Relay 监听器
- 证书记录和证书材料
- 版本策略

备份包未加密，可能包含节点 token、证书私钥等敏感材料，只应在受信任环境中保存和传输。
