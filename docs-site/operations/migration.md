# 迁移

控制面提供两类迁移：存储后端迁移（如 SQLite → PostgreSQL / MySQL），以及从旧版轻量 Agent 迁移到当前 Go agent。

## 存储后端迁移

使用 `migrate-storage` 子命令。请在既能读取源 SQLite 文件、又能连接目标数据库的环境中执行：

```bash
nre-control-plane migrate-storage \
  --from-driver sqlite \
  --from-dsn /opt/nginx-reverse-emby/panel/data/panel.db \
  --to-driver postgres \
  --to-dsn 'postgres://nre:nre@127.0.0.1:5432/nre?sslmode=disable'
```

支持的标志：

| 标志 | 说明 |
| --- | --- |
| `--from-driver` | 源数据库驱动（`sqlite` / `postgres` / `mysql`）。 |
| `--from-dsn` | 源数据库 DSN。 |
| `--from-data-root` | 源证书材料数据根目录（不在自动推断位置时显式指定）。 |
| `--to-driver` | 目标数据库驱动。 |
| `--to-dsn` | 目标数据库 DSN。 |
| `--to-data-root` | 目标证书材料数据根目录。 |

迁移内容包括 Agent、规则、证书、WireGuard、Relay、版本策略、流量策略和托管证书材料。普通迁移默认跳过高容量流量历史。

## 从旧版 Agent 迁移

从旧 `main` 版本轻量 Agent 迁移到当前 Go agent：

1. 在旧控制面执行 **导出备份**。
2. 升级控制面并 **导入备份**。
3. 在每台旧 Agent 机器执行迁移命令。

```bash
curl -fsSL http://master.example.com:8080/panel-api/public/join-agent.sh | sh -s -- \
  migrate-from-main \
  --register-token your-register-token \
  --install-systemd
```

脚本会尝试从 `/opt/nginx-reverse-emby-agent`（可用 `--source-dir` 覆盖）读取旧配置，复用原 `agent_token`，切换到 `/var/lib/nre-agent`，验证通过后清理旧 runtime 和 nginx 服务。

## 注意事项

- 生产迁移前，先复制一份旧数据做影子验证。
- SQLite source DSN：在容器内执行用容器内路径，在宿主机直接执行用宿主机路径。
- source 和 target 的 `driver+dsn` 完全相同时会被拒绝。
- 普通迁移默认跳过高容量 traffic history。
- 证书材料目录不在 SQLite 同级目录下时，传入 `--from-data-root` 和 `--to-data-root`。
