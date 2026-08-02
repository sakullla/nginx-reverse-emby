# Task pki-production-runtime

## Execution

```yaml
format: task_run
task_id: pki-production-runtime
execution:
  outcome: done_with_concerns
  summary: "生产 PKI runtime 已完整接线：正常/紧急 CA 生命周期、精确 relay revision barrier、远端强制重新登记、租约 fencing、加密备份与跨平台原子恢复均落到真实 Gorm/SQLite 路径；Windows 跨卷敏感 staging 采用跨 lifecycle cleanup registration，按字面目录扫描并覆盖 tombstone 重现。mTLS 仍只保护复用现有入口的隧道 TLS/TCP 与 QUIC 数据面，控制监听器、NRE_MASTER_URL、注册/心跳/revision/task 协议及 X-Agent-Token 均未调整。"
  verification_refs:
    - "cd panel/backend-go && go test ./... -count=1 — exit 0"
    - "cd panel/backend-go && go test -tags=integration ./... -count=1 — exit 0"
    - "cd panel/backend-go && go vet ./... — exit 0"
    - "cd go-agent && go test -short ./... -count=1 — exit 0"
    - "GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c ./internal/controlplane/storage — exit 0"
    - "高风险 service 与 storage 测试各 -count=10 — exit 0"
    - "git diff --check 与 git diff --cached --check — exit 0"
    - "control integration、PKI lifecycle、PKI storage 三路独立复审 — approved"
  concerns:
    - "当前 Windows Go 环境未配置 CGO/C 编译器，未执行 go test -race；已用 integration tier、Linux 交叉编译、高风险用例十次重复及三路独立复审补强。"
dispatch_binding:
  workflow_id: 2026-07-31-内部证书自动轮转与隧道双向mtls
  task_id: pki-production-runtime
  attempt_id: pki-production-runtime-A1-f77ad5b5e5619cf3
  message_id: pki-production-runtime-A1-f77ad5b5e5619cf3-M1
integration:
  paths:
    - panel/backend-go/cmd/nre-control-plane/main.go
    - panel/backend-go/internal/controlplane/config/config_test.go
    - panel/backend-go/internal/controlplane/service/agents_pki.go
    - panel/backend-go/internal/controlplane/service/certs_pki.go
    - panel/backend-go/internal/controlplane/service/control_pki_integration_test.go
    - panel/backend-go/internal/controlplane/service/pki_authority.go
    - panel/backend-go/internal/controlplane/service/pki_authority_runtime.go
    - panel/backend-go/internal/controlplane/service/pki_authority_runtime_test.go
    - panel/backend-go/internal/controlplane/service/pki_authority_test.go
    - panel/backend-go/internal/controlplane/service/pki_backup.go
    - panel/backend-go/internal/controlplane/service/pki_backup_restore_target.go
    - panel/backend-go/internal/controlplane/service/pki_backup_restore_target_test.go
    - panel/backend-go/internal/controlplane/service/pki_backup_sqlite.go
    - panel/backend-go/internal/controlplane/service/pki_backup_test.go
    - panel/backend-go/internal/controlplane/service/pki_crypto.go
    - panel/backend-go/internal/controlplane/service/pki_emergency_authority_runtime.go
    - panel/backend-go/internal/controlplane/service/pki_enrollment.go
    - panel/backend-go/internal/controlplane/service/pki_enrollment_test.go
    - panel/backend-go/internal/controlplane/service/relay.go
    - panel/backend-go/internal/controlplane/service/relay_pki_repository.go
    - panel/backend-go/internal/controlplane/storage/gorm_lifecycle.go
    - panel/backend-go/internal/controlplane/storage/gorm_store.go
    - panel/backend-go/internal/controlplane/storage/pki_backup_repository.go
    - panel/backend-go/internal/controlplane/storage/pki_models.go
    - panel/backend-go/internal/controlplane/storage/pki_relay_failclosed_test.go
    - panel/backend-go/internal/controlplane/storage/pki_repository.go
    - panel/backend-go/internal/controlplane/storage/pki_restore.go
    - panel/backend-go/internal/controlplane/storage/pki_restore_platform_unix.go
    - panel/backend-go/internal/controlplane/storage/pki_restore_platform_windows.go
    - panel/backend-go/internal/controlplane/storage/pki_restore_test.go
    - panel/backend-go/internal/controlplane/storage/revision_coordinator.go
    - panel/backend-go/internal/controlplane/storage/sqlite_store.go
  subject: "feat(backend): complete production PKI lifecycle recovery"
```
