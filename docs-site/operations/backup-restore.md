# 备份与恢复

使用面板的备份导出功能，可以生成包含规则、Agent、Relay 设置、证书和版本策略的可移植备份。

你也可以直接备份 Docker Compose 部署挂载的 `./data` 目录。如果使用 PostgreSQL 或 MySQL，请使用对应数据库工具备份数据。

普通备份默认不包含高容量 traffic history。
