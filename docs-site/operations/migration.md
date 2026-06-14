# 迁移

迁移是指把数据从一个环境转移到另一个环境。控制面支持两类迁移：

1. **存储后端迁移** — 换数据库，比如从 SQLite 升级到 PostgreSQL
2. **Agent 版本迁移** — 从旧版轻量 Agent 升级到当前 Go agent

## 存储后端迁移

随着数据量增长，你可能需要从 SQLite 切换到 PostgreSQL 或 MySQL。控制面提供了专门的迁移命令。

### 迁移命令

使用 `migrate-storage` 子命令：

```bash
nre-control-plane migrate-storage \
  --from-driver sqlite \
  --from-dsn /opt/nginx-reverse-emby/panel/data/panel.db \
  --to-driver postgres \
  --to-dsn 'postgres://nre:nre@127.0.0.1:5432/nre?sslmode=disable'
```

### 命令参数说明

| 标志 | 说明 | 示例 |
| --- | --- | --- |
| `--from-driver` | 源数据库驱动 | `sqlite`、`postgres`、`mysql` |
| `--from-dsn` | 源数据库连接串 | SQLite 是文件路径，PostgreSQL/MySQL 是连接 URL |
| `--from-data-root` | 源证书材料目录 | 当证书材料不在自动推断的位置时指定 |
| `--to-driver` | 目标数据库驱动 | `sqlite`、`postgres`、`mysql` |
| `--to-dsn` | 目标数据库连接串 | 目标数据库的连接信息 |
| `--to-data-root` | 目标证书材料目录 | 迁移证书文件的目标位置 |

### 迁移内容包括

- Agent 节点信息
- HTTP 规则和 L4 规则
- 证书和证书材料
- WireGuard 配置
- Relay 监听器
- 版本策略
- 流量策略和校准基线

**不包含：** 高容量流量历史（小时/日/月明细）— 默认跳过以节省时间和空间。

### 执行环境要求

迁移命令需要在一个**能同时访问源数据库和目标数据库**的环境中执行：

- **SQLite → PostgreSQL**：在能读取 SQLite 文件、且能连接 PostgreSQL 的宿主机或容器内执行
- **容器内执行**：DSN 路径使用容器内路径
- **宿主机直接执行**：DSN 路径使用宿主机路径

### 迁移前 checklist

- [ ] 备份源数据（先导出面板备份或直接备份 data 目录）
- [ ] 确保目标数据库已创建且可连接
- [ ] 如果证书材料目录不在 SQLite 同级目录，确认 `--from-data-root` 和 `--to-data-root`
- [ ] 生产环境先做影子验证（复制一份旧数据测试迁移）

### 常见问题

**Q: 源和目标数据库相同会怎样？**
A: 命令会拒绝执行，防止误操作。

**Q: 迁移会删除源数据吗？**
A: 不会。迁移是复制操作，源数据保持不变。

**Q: 迁移后需要重启服务吗？**
A: 需要。修改控制面的数据库配置指向新数据库，然后重启。

## 从旧版 Agent 迁移

如果你之前使用的是旧 `main` 版本的轻量 Agent，可以按以下步骤迁移到当前 Go agent：

### 迁移步骤

**第一步：在旧控制面导出备份**
- 登录旧控制面
- 进入 **设置 → 数据管理**
- 全选并导出备份包

**第二步：升级控制面并导入备份**
- 部署新版控制面
- 在新控制面导入备份包
- 确认规则、证书等数据完整

**第三步：在每台 Agent 机器执行迁移命令**

```bash
curl -fsSL http://master.example.com:8080/panel-api/public/join-agent.sh | sh -s -- \
  migrate-from-main \
  --register-token your-register-token \
  --install-systemd
```

### 迁移脚本会做什么

1. **读取旧配置** — 从 `/opt/nginx-reverse-emby-agent` 读取旧 Agent 的配置（可用 `--source-dir` 指定其他路径）
2. **复用身份** — 保留原来的 `agent_token`，无需重新注册
3. **切换目录** — 新 Agent 使用 `/var/lib/nre-agent` 作为工作目录
4. **验证** — 检查新 Agent 能正常连接控制面
5. **清理** — 验证通过后，自动清理旧 runtime 和 nginx 服务

### 迁移参数

| 参数 | 说明 |
| --- | --- |
| `migrate-from-main` | 子命令，表示从旧版迁移 |
| `--register-token` | 控制面的注册 token |
| `--install-systemd` | 安装 systemd 服务，开机自启 |
| `--source-dir` | 旧 Agent 配置目录，默认 `/opt/nginx-reverse-emby-agent` |

### 注意事项

- 生产迁移前，先复制一份旧数据做影子验证
- 确保新控制面已正常运行且能访问
- 迁移过程中旧 Agent 会短暂中断，建议在低峰期执行
- 如果迁移失败，旧配置不会被删除，可以手动回滚
