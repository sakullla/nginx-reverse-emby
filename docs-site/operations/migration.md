# 迁移

控制面提供存储迁移能力，可从 SQLite 迁移到 PostgreSQL。

请在既能读取源 SQLite 文件、又能连接目标数据库的环境中执行迁移命令：

```bash
nre-control-plane migrate-storage \
  --from-driver sqlite \
  --from-dsn /opt/nginx-reverse-emby/panel/data/panel.db \
  --to-driver postgres \
  --to-dsn 'postgres://nre:nre@127.0.0.1:5432/nre?sslmode=disable'
```

如果证书材料不在自动推断的 SQLite 数据根目录下，请显式传入数据根目录参数。
