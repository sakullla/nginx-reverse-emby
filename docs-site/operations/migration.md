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

**迁移内容：** Agent 信息、HTTP/L4 规则、证书材料、WireGuard 配置、Relay 监听器、版本策略、流量策略与校准基线。

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
