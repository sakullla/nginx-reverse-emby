# 备份与恢复

数据安全很重要。控制面支持两种备份方式：

| 方式 | 适合 | 优点 | 缺点 |
| --- | --- | --- | --- |
| 面板导出/导入 | 普通配置迁移、跨架构迁移、版本升级 | 可预览、有冲突处理 | 不含流量历史或内部 PKI 恢复保证 |
| 直接备份 data 目录 | 普通 SQLite 同机维护 | 简单、快速 | 需停机；不是内部 PKI 恢复入口 |

使用 PostgreSQL 或 MySQL 时，用该数据库的备份工具（`pg_dump`、`mysqldump`），并按下文把数据库、PKI vault 与 master key 作为同一个恢复点保存。

::: danger 先确认存储后端
**证书管理 → 内部 PKI → 受保护备份** 当前只支持 file-backed SQLite。它不是 PKI-only 导出，而是“清除 enrollment token 后的完整 SQLite 数据库快照 + CA 私钥”的加密 archive；恢复会替换目标的整个 SQLite 数据库。PostgreSQL/MySQL 调用该接口会返回不支持，必须使用下文的协同冷恢复。
:::

## 内部 PKI 的恢复边界

### File-backed SQLite

从 **证书管理 → 内部 PKI → 受保护备份** 使用单次 passphrase 导出加密 archive。除 PKI domain/epoch、CA/trust、撤销状态和稳定 Agent 关联外，archive 还包含导出时的 Agent、规则、证书及其他面板数据库状态。导出时一并记录控制面版本和镜像 digest；导入要求目标具有完全相同的 SQLite schema fingerprint，因此应先部署与源端相同的 release/image，再执行恢复。

恢复前先为目标数据库做可回滚备份。受保护恢复必须先执行，因为它会覆盖目标 SQLite 数据库；如果确实要用更新的普通配置包叠加导出后的变更，只能在受保护恢复成功并核对完整状态后，再预览和导入普通配置包。详见[内部 PKI 升级与运维](./internal-pki.md#受保护备份)。

### PostgreSQL/MySQL 协同冷恢复

PostgreSQL/MySQL 当前没有可移植的受保护 PKI archive。可执行的灾难恢复路径是整实例的协同冷恢复：

1. 停止并从网络、调度器隔离所有控制面实例，确认没有数据库 writer、签发者或 lease owner。
2. 在同一个停机恢复点创建数据库原生一致性备份（`pg_dump`、`mysqldump` 或托管数据库快照），同时保存 panel data 下的 PKI vault/data root 和 `NRE_PKI_MASTER_KEY_FILE` 所在私有目录。
3. 将这组材料加密并限制访问；随备份记录控制面 release、镜像 digest、数据库类型/版本以及 data/key 的目标路径。单独的数据库 dump、单独的 vault 或单独的 master key 都不可恢复。
4. 保持旧源永久隔离，在目标部署完全相同的 release/schema，按原路径恢复数据库、vault/data root 与 master-key 目录，并恢复目录 `0700`、key/vault 文件 `0600` 权限。
5. 只启动一个目标实例，核对 PKI domain、epoch/security revision、CA generation、Agent/规则和 Relay 状态；确认恢复成功后才能升级版本或恢复流量。

这是同版本整实例冷恢复，不是普通配置 tar、SQLite `force=true` 恢复或跨 domain 激活。需要切换数据库后端时，只使用经过影子验证的 [`migrate-storage` 维护流程](./migration.md#存储后端迁移)，不要把灾难恢复与跨后端迁移合并成一步。

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
