# 备份与恢复

数据安全很重要。控制面提供两种备份方式，适合不同场景：

| 方式 | 适合场景 | 优点 | 缺点 |
| --- | --- | --- | --- |
| **面板导出/导入** | 跨机器迁移、跨架构迁移、版本升级 | 完整、可预览、有冲突处理 | 不包含流量历史 |
| **直接备份 data 目录** | 同机快速恢复、定期自动备份 | 简单、快速 | 需要停机、仅限 SQLite |

如果你使用 PostgreSQL 或 MySQL，请使用对应数据库的备份工具（如 `pg_dump`、`mysqldump`）。

## 面板导出 / 导入（推荐）

在 **设置 → 数据管理** 中操作。

![数据管理](/screenshots/panel-backup-export.png)

### 导出

勾选要导出的资源（支持全选）：

- **节点（Agent）** — 所有注册的 Agent 及其配置
- **HTTP 规则** — 所有 HTTP 代理规则
- **L4 规则** — 所有 TCP/UDP 转发规则
- **中继监听器（Relay）** — Relay 隧道配置
- **证书** — 证书记录和证书材料（含私钥）
- **版本策略** — Agent 升级策略

点击 **导出选中备份**，系统会生成一个 `.tar.gz` 压缩包。

**备份包里包含什么？**
- Agent、HTTP 规则、L4 规则
- WireGuard Profile 与客户端配置
- Egress Profile
- Relay 监听器配置
- 证书记录与证书材料
- 版本策略
- 流量策略与校准基线
- `manifest.json`（备份清单）

**不包含什么？**
- 高容量流量历史（小时/日/月明细）— 这些数据量大，普通备份跳过

::: warning 备份包含敏感材料
备份包未加密，可能包含节点 token、证书私钥等敏感信息。请妥善保管：
- 只在受信任的环境中保存和传输
- 不要上传到公共网盘或代码仓库
- 考虑加密后再存储到远程
:::

### 导入

导入是一个三步向导，操作简单且安全：

**第一步：选择文件**
- 拖入或选择 `.tar.gz` / `.tgz` 备份包
- 系统会自动解析备份内容

**第二步：预览确认**
- 显示来源系统的架构和导出时间
- 列出各资源将要 **新增** 或 **跳过** 的数量
- 你可以确认无误后再执行导入

**第三步：导入结果**
- 汇总已导入、冲突跳过、无效跳过、缺少证书材料跳过的情况

**冲突处理规则：**
- 同名或同 ID 的 Agent → 跳过（保留现有）
- 同域名的证书 → 跳过（保留现有）
- 系统 Relay CA → 有特殊处理逻辑，不会重复创建
- `local` 引用 → 自动重映射到目标系统的 local agent
- 导入后 → 自动刷新 Agent 的期望版本

## 直接备份 data 目录

如果你使用 SQLite（默认），且只需要在同机快速恢复，可以直接备份数据目录。

### 目录位置

Docker Compose 默认把数据挂载到：

| 位置 | 路径 |
| --- | --- |
| 宿主机 | `./data`（Compose 文件同级目录） |
| 容器内 | `/opt/nginx-reverse-emby/panel/data` |

### 备份步骤

```bash
# 1. 停止服务（确保数据库文件不再被写入）
docker compose down

# 2. 打包数据目录
tar -czf nre-data-backup-$(date +%Y%m%d).tgz data

# 3. 重新启动服务
docker compose up -d
```

::: tip 为什么一定要先停机？
SQLite 是文件型数据库，运行中可能有未写入的缓存。直接打包可能备份到不一致的状态，导致恢复后数据损坏。
:::

### 恢复步骤

```bash
# 1. 停止服务
docker compose down

# 2. 恢复数据目录（从备份包解压）
tar -xzf nre-data-backup-20240101.tgz

# 3. 确保目录权限正确
# 如果恢复后启动失败，检查 data 目录的所有者是否与容器内一致

# 4. 重新启动服务
docker compose up -d
```

### 自动化备份建议

你可以把备份命令加入定时任务（cron）：

```bash
# 每天凌晨 3 点备份
0 3 * * * cd /path/to/your/project && docker compose down && tar -czf backups/nre-$(date +\%Y\%m\%d).tgz data && docker compose up -d
```

或者使用更安全的方案：利用 SQLite 的在线备份功能，无需停机：

```bash
# 在线备份（无需停机）
docker compose exec control-plane sqlite3 /opt/nginx-reverse-emby/panel/data/panel.db ".backup to /tmp/panel-backup.db"
docker compose cp control-plane:/tmp/panel-backup.db ./panel-backup.db
```
