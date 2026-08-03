# 数据迁移

控制面支持两类迁移：换数据库（SQLite → PostgreSQL/MySQL）、从旧版 Agent 升级。

## 存储后端迁移

用 `migrate-storage` 命令把数据从一个数据库迁到另一个：

```bash
nre-control-plane migrate-storage \
  --from-driver sqlite \
  --from-dsn /opt/nginx-reverse-emby/panel/data/panel.db \
  --to-driver postgres \
  --to-dsn 'postgres://nre:nre@127.0.0.1:5432/nre?sslmode=disable'
```

| 参数 | 说明 |
| --- | --- |
| `--from-driver` | 源数据库驱动（`sqlite`、`postgres`、`mysql`） |
| `--from-dsn` | 源数据库连接串 |
| `--from-data-root` | 源证书材料目录（非标准位置时指定） |
| `--to-driver` | 目标数据库驱动 |
| `--to-dsn` | 目标数据库连接串 |
| `--to-data-root` | 目标证书材料目录 |

**迁移内容：** Agent 信息、HTTP/L4 规则、证书材料、Relay 监听器、版本策略、流量策略与校准基线。

**不迁移：** 高容量流量历史（小时/日/月明细）。

### 执行环境

迁移命令需要在能同时访问源和目标数据库的环境中执行。容器内执行用容器内路径，宿主机执行用宿主机路径。

### 迁移前 checklist

- [ ] 备份源数据（先导出面板备份或直接备份 data 目录）
- [ ] 确保目标数据库已创建且可连接
- [ ] 证书材料路径确认正确
- [ ] 生产环境先做影子验证（复制一份旧数据测试迁移）

### 常见问题

**源和目标相同会怎样？** 命令拒绝执行。

**迁移会删除源数据吗？** 不会，是复制操作。

**迁移后要重启吗？** 需要。修改控制面的数据库配置指向新数据库后重启。

## 搬迁已启用内部 PKI 的控制面

数据库迁移或普通配置导入不会自行完成内部 PKI 的安全交接。应用不设置独立 PKI 侧栏菜单，相关操作从 **证书管理 → 内部 PKI** 进入。

### File-backed SQLite

受保护 archive、页面普通恢复和 panel-token `force=true` API **只支持 file-backed SQLite**。archive 内是 sanitized 的完整 SQLite 数据库快照和 CA 私钥，恢复会覆盖目标的 Agent、规则及其他面板状态，并要求目标 schema fingerprint 与源端完全相同。按[内部 PKI 升级与运维](./internal-pki.md#计划迁移)执行时：

- 源实例和目标实例不能同时签发；迁移前停止并隔离源实例，协作式 instance lease 不能替代外部单活编排。
- 记录源端 release/image digest，在目标先部署相同版本并做好回滚备份；受保护恢复必须早于任何普通配置导入，恢复确认后再升级。
- 已初始化且同 domain 的目标可普通导入；空白/不同 domain 目标必须使用文档中的 panel-token 认证 multipart API 并显式发送 `force=true`，页面普通导入会失败。
- 普通恢复采用 manifest 版本，epoch 不保证前进；force restore 才会保持 archive 的 PKI domain，并设置 `epoch=max(目标 epoch, archive epoch)+1, security_revision=0` 来 fence 旧实例。

### PostgreSQL/MySQL

PostgreSQL/MySQL 调用受保护导出/导入会返回不支持，不能使用上述 archive 或 `force=true` API。灾难恢复和同后端搬迁必须停止、隔离所有控制面 writer，在同一恢复点协同保存/恢复数据库原生一致性备份、PKI vault/data root 与 `NRE_PKI_MASTER_KEY_FILE` 私有目录，并使用相同 release/schema 和原路径启动一个目标实例；完整步骤见[备份与恢复](./backup-restore.md#postgresqlmysql-协同冷恢复)。

如果是有计划地切换数据库后端，应在源控制面停机、目标 PKI 状态为空且版本一致时运行本页前述 `migrate-storage`，并显式提供可访问的 `--from-data-root`/`--to-data-root`。该命令复制 canonical PKI 图和默认 data root 下的 vault；若 master key 通过 `NRE_PKI_MASTER_KEY_FILE` 位于 data root 外，仍须把其私有目录作为同一次迁移操作安全复制或在目标挂载同一受控目录。先用生产副本做影子验证，不要把跨后端迁移与灾难恢复合并执行。

完成任一路径后，把每个 Agent 的 `NRE_MASTER_URL` 改到新控制面地址，但保留稳定 Agent ID、tunnel identity、control token 和规则/listener 关联。离线 Agent 只能继续使用最后验证的安全状态，撤销会在重连同步后收敛；安全敏感时同时从网络层隔离离线节点。

## 从旧版 Agent 迁移

旧版轻量 Agent 升级到当前 Go Agent：

**1. 导出旧控制面备份** — 登录旧面板，全选资源导出

**2. 部署新控制面并导入** — 部署新版后，在数据管理页面导入备份包

**3. 每台 Agent 机器执行迁移：**

```bash
curl -fsSL http://<面板地址>:8080/panel-api/public/join-agent.sh | sh -s -- \
  migrate-from-main \
  --register-token your-register-token \
  --install-systemd
```

脚本做的事：读取旧配置 → 复用 Agent Token → 切换新目录 → 验证连接 → 清理旧文件。

### 注意事项

- 生产迁移前先做影子验证
- 确保新控制面已正常运行且可访问
- 迁移期间旧 Agent 会短暂中断，建议低峰期操作
- 迁移失败时旧配置不会被删除，可以手动回滚
