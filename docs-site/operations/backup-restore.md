# 备份与恢复

数据安全很重要。控制面支持两种备份方式：

| 方式 | 适合 | 优点 | 缺点 |
| --- | --- | --- | --- |
| 面板导出/导入 | 普通配置迁移、跨架构迁移、版本升级 | 可预览、有冲突处理 | 不含流量历史或内部 PKI 恢复保证 |
| 直接备份 data 目录 | 普通 SQLite 同机维护 | 简单、快速 | 需停机；不是内部 PKI 恢复入口 |

使用 PostgreSQL 或 MySQL 时，用该数据库的备份工具（`pg_dump`、`mysqldump`）。

::: danger 内部 PKI 必须单独受保护备份
本页的普通 `.tar.gz`、数据库 dump 和直接 data 目录打包都不能作为内部 PKI 的恢复或迁移凭据。内部 PKI 必须从 **证书管理 → 内部 PKI → 受保护备份** 使用单次 passphrase 的加密 archive，保留并校验 PKI domain/epoch、CA/trust、撤销状态和稳定 Agent 关联；详见[内部 PKI 升级与运维](./internal-pki.md)。
:::

## 面板导出/导入（推荐）

在 **设置 → 数据管理** 中操作。

![数据管理](/screenshots/panel-backup-export.png)

### 导出

勾选要导出的资源（支持全选）：Agent 节点、HTTP 规则、L4 规则、Relay 监听器、证书（含私钥）、版本策略。点击 **导出选中备份**，系统生成 `.tar.gz` 压缩包。

**包含：** Agent、HTTP/L4 规则、Egress Profile、Relay 监听器、证书材料、版本策略、流量策略与校准基线、`manifest.json`

**不包含：** 高容量流量历史（小时/日/月明细）

::: warning 备份包含敏感材料
备份包未加密，可能含 Agent Token、证书私钥。妥善保管，不要上传到公共网盘或代码仓库。建议加密后再存储到远程。
:::

它不会替代内部 PKI 的加密 archive，也不能用于 force restore/activation。

### 导入

三步向导：
1. 选择 `.tar.gz` 备份文件，系统自动解析
2. 预览各资源的新增/跳过数量，确认后执行
3. 查看导入结果（已导入、冲突跳过、无效跳过）

冲突规则：同名 Agent 跳过；同域名证书跳过；系统 Relay CA 不重复创建；`local` 引用自动重映射到目标系统。

## 直接备份 data 目录

SQLite 用户的简单备份方式。数据在 `./data` 目录（Compose 文件同级）。

以下命令只用于普通同机维护。即使目录中碰巧包含 PKI 文件，也不要把该未加密 tar 当作可移植的内部 PKI 备份或跨实例激活材料。

### 备份

```bash
docker compose down
tar -czf nre-data-backup-$(date +%Y%m%d).tgz data
docker compose up -d
```

::: tip 为什么要先停机？
SQLite 运行中可能有未写入的缓存。不停机打包可能导致数据不一致。
:::

### 恢复

```bash
docker compose down
tar -xzf nre-data-backup-20240101.tgz
docker compose up -d
```

### 定时备份

```bash
# 每天凌晨 3 点
0 3 * * * cd /path/to/project && docker compose down && tar -czf backups/nre-$(date +\%Y\%m\%d).tgz data && docker compose up -d
```

### 在线备份（无需停机）

`./data` 目录挂载自宿主机，直接对宿主机上的数据库文件执行备份即可：

```bash
sqlite3 ./data/panel.db ".backup ./panel-backup.db"
```
