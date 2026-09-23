# Nginx-Reverse-Emby v1.5.1

对照 v1.5.0 的补丁版本。当前镜像：`sakullla/nginx-reverse-emby:1.5.1`。

## 修复

- 修复托管证书续签时单张证书异常或 ACME 操作卡住，导致整轮调度停止的问题。
- 单次 ACME 签发或续签默认超时 60 分钟；失败会记录重试时间，并由调度器在到期后唤醒重试。
- 修复本地与远程同时分发的证书未被续签扫描的问题。
- 修复 revision/generation artifact 孤儿引用和外置文件清理，并在满足阈值时回收 SQLite 空闲空间。
- 消除 artifact 外置扫描过程中正常出现的 `record not found` 日志噪声。

## 升级

```sh
docker compose pull
docker compose up -d
```

如需调整 ACME 单次操作超时，可设置 `NRE_MANAGED_CERT_ACME_TIMEOUT`；默认值为 `60m`。
