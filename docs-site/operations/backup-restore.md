# 备份与恢复

控制面提供两种备份方式：面板的导出 / 导入（适合跨机器、跨架构迁移），以及直接备份 Docker Compose 挂载的 `./data` 目录（适合同机快速恢复）。使用 PostgreSQL 或 MySQL 时，请使用对应数据库工具备份数据。

## 面板导出 / 导入

在 **设置 → 数据管理** 中操作。

### 导出

勾选要导出的资源（支持全选）：

- 节点（Agent）
- HTTP 规则
- L4 规则
- 中继监听器（Relay）
- 证书（含证书材料）
- 版本策略

点击 **导出选中备份** 生成 `.tar.gz` 包。

备份包包含配置类的实体：Agent、HTTP 规则、L4 规则、WireGuard Profile 与客户端、Egress Profile、Relay 监听器、证书记录与证书材料、版本策略、流量策略与校准基线，以及一个 `manifest.json`。普通备份**不包含**高容量流量历史（小时 / 日 / 月明细）。

### 导入

导入是三步向导：

1. **选择文件**：拖入或选择 `.tar.gz` / `.tgz` 备份包。
2. **预览确认**：展示来源架构、导出时间和各资源将新增 / 跳过的数量。
3. **导入结果**：汇总已导入、冲突跳过、无效跳过、缺少证书材料跳过。

冲突处理：同名或同 ID 的 Agent、同域名的证书会被跳过；系统 Relay CA 有专门处理；导入的 `local` 引用会重映射到目标系统的 local agent；导入后会刷新 Agent 的期望版本。

::: warning 备份包含敏感材料
备份包未加密，可能包含节点 token、证书私钥等敏感材料，只应在受信任环境中保存和传输。
:::

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

::: tip 先停机再备份
对 SQLite 直接打包前应先 `docker compose down`，避免备份到正在写入的数据库文件。
:::
