# Execution Plan

<!-- Duplicate task/command entries as needed; use `inspect contract` for every constraint. -->

```yaml
format: execution_plan
tasks:
  - id: pki-storage
    goal: "建立与公网 managed certificates 分离的内部 PKI canonical schema、策略校验和控制面 CA vault。"
    depends_on: []
    covers: [R1, R2, R3, R4, R5, R9, R13]
    scope:
      - "panel/backend-go/internal/controlplane/storage/schema.go"
      - "panel/backend-go/internal/controlplane/storage/pki_*.go"
      - "panel/backend-go/internal/controlplane/service/pki_crypto*.go"
      - "panel/backend-go/internal/controlplane/service/pki_policy*.go"
      - "panel/backend-go/go.mod"
      - "panel/backend-go/go.sum"
    outcomes:
      - "PKI domain、CA generation、identity、certificate、enrollment token、lifecycle job、event 和 instance lease 具有唯一约束与事务化 repository。"
      - "CA 私钥使用 AES-256-GCM vault 与受限 master-key file，内部 PKI 私钥不会落入 generic certificate material。"
      - "ECDSA/TLS profile、CA/endpoint lifetime、renewal threshold、backoff、alert threshold 和 retention 边界由单一 validator 拒绝非法值。"
      - "现有 internal_ca/relay 元数据可被识别为迁移来源，公网 ACME schema 与行为不被内部 PKI 状态污染。"
    verify:
      - "cd panel/backend-go && go test -short ./internal/controlplane/storage ./internal/controlplane/service -run 'Test(PKI|InternalPKI|Vault|Policy)'"
    test: new

  - id: pki-enrollment-core
    goal: "实现一次性短时登记、稳定 agent/listener identity、CSR 签发与重登记的原子业务核心。"
    depends_on: [pki-storage]
    covers: [R3, R5, R7, R8]
    scope:
      - "panel/backend-go/internal/controlplane/service/pki_enrollment*.go"
      - "panel/backend-go/internal/controlplane/service/pki_identity*.go"
      - "panel/backend-go/internal/controlplane/service/pki_token*.go"
      - "panel/backend-go/internal/controlplane/storage/pki_repository.go"
    outcomes:
      - "256-bit register token 只存 digest、默认十分钟过期，并发消费只有一个事务成功。"
      - "新登记和 bound re-enrollment 都以 stable agent ID 为 owner，CSR 算法、purpose、public-key change 和 subject binding 被校验。"
      - "签发、token 消费、identity/certificate 状态与 audit event 同事务提交，失败不留下半完成 agent 或 certificate。"
      - "本地嵌入式 agent 可通过内部调用绑定 LocalAgentID，但不会暴露 bootstrap token。"
    verify:
      - "cd panel/backend-go && go test -short ./internal/controlplane/service -run 'TestPKI(Enrollment|Identity|Token)'"
    test: new

  - id: pki-backup-lease
    goal: "实现协作式单活 PKI lease、PKI epoch fencing 和不包含 enrollment token 的受保护备份/恢复。"
    depends_on: [pki-storage]
    covers: [R9, R10, R11]
    scope:
      - "panel/backend-go/internal/controlplane/service/backup.go"
      - "panel/backend-go/internal/controlplane/service/backup_types.go"
      - "panel/backend-go/internal/controlplane/service/backup_test.go"
      - "panel/backend-go/internal/controlplane/service/pki_backup*.go"
      - "panel/backend-go/internal/controlplane/service/pki_lease*.go"
      - "panel/backend-go/internal/controlplane/storage/pki_backup_repository*.go"
      - "panel/backend-go/internal/controlplane/storage/pki_lease_repository*.go"
      - "panel/backend-go/internal/controlplane/storage/pki_models.go"
      - "panel/backend-go/internal/controlplane/storage/pki_repository.go"
      - "panel/backend-go/internal/controlplane/storage/pki_repository_test.go"
      - "panel/backend-go/go.mod"
    outcomes:
      - "只有当前 lease holder 能解密 CA、签发、轮转、发布安全快照或导出可恢复备份，失去 lease 后 PKI mutation 失败关闭。"
      - "Argon2id + AES-256-GCM envelope 包含 sanitized SQLite snapshot；全部 enrollment-token rows 被排除且 manifest 证明 row count 为零。"
      - "restore 在 staging 中完成 AEAD/hash/schema/foreign-key/key-match 校验后原子生效，错误口令、篡改或失败不改变 active state。"
      - "安全快照按 (pki_epoch, security_revision) 字典序 fencing；force activation 以 higher epoch/revision 0完整快照恢复。"
    verify:
      - "cd panel/backend-go && go test -short ./internal/controlplane/service -run 'Test(PKIBackup|PKILease|Backup.*PKI|PKIEpoch)'"
    test: extend

  - id: pki-lifecycle
    goal: "实现端点续签、CA 日常换代、撤销、强制轮转、紧急轮转、审计与派生告警的可重入状态机。"
    depends_on: [pki-storage, pki-enrollment-core, pki-backup-lease]
    covers: [R3, R4, R5, R8, R9, R11]
    scope:
      - "panel/backend-go/internal/controlplane/service/pki_lifecycle*.go"
      - "panel/backend-go/internal/controlplane/service/pki_authority*.go"
      - "panel/backend-go/internal/controlplane/service/pki_scheduler*.go"
      - "panel/backend-go/internal/controlplane/service/pki_revocation*.go"
      - "panel/backend-go/internal/controlplane/service/pki_audit*.go"
      - "panel/backend-go/internal/controlplane/service/pki_alert*.go"
    outcomes:
      - "endpoint 在剩余三分之一阈值后稳定错峰换钥，失败按退避重试并保留 active generation，过期后失败关闭。"
      - "CA prepare/distribute/reissue/cutover/overlap/retire 可重入；持续在线未 ack 节点在 deadline 后阻塞切换并告警，离线节点不阻塞且受 30 天 overlap 约束。"
      - "revoke 原子递增 security revision、禁用 identity/control token、发布完整或增量签名安全快照，并产生结构化 event。"
      - "online peer 在五秒内关闭 revoked relay；隔离 peer 可用最后可信快照，并在控制恢复时先应用安全更新。"
      - "force endpoint rotation 只影响目标；emergency CA 立即废弃旧 trust并要求重新登记，失败不回退旧 CA。"
    verify:
      - "cd panel/backend-go && go test -short ./internal/controlplane/service -run 'TestPKI(Lifecycle|Authority|Rotation|Revocation|Audit|Alert)'"
    test: new

  - id: control-integration
    goal: "把 PKI 生命周期接入现有控制入口、agent registration/revision/task、relay/listener、应用启动与维护期迁移，同时保持控制协议非 mTLS。"
    depends_on: [pki-lifecycle]
    covers: [R1, R2, R7, R8, R9, R11, R13]
    scope:
      - "README.md"
      - "panel/backend-go/internal/controlplane/config/config.go"
      - "panel/backend-go/internal/controlplane/config/config_test.go"
      - "panel/backend-go/internal/controlplane/storage/pki_repository.go"
      - "panel/backend-go/internal/controlplane/storage/snapshot_types.go"
      - "panel/backend-go/internal/controlplane/storage/sqlite_models.go"
      - "panel/backend-go/internal/controlplane/service/agents*.go"
      - "panel/backend-go/internal/controlplane/service/tasks*.go"
      - "panel/backend-go/internal/controlplane/service/relay*.go"
      - "panel/backend-go/internal/controlplane/service/certs*.go"
      - "panel/backend-go/internal/controlplane/service/cert_renewal*.go"
      - "panel/backend-go/internal/controlplane/service/pki_crypto.go"
      - "panel/backend-go/internal/controlplane/service/pki_enrollment.go"
      - "panel/backend-go/internal/controlplane/service/pki_lease.go"
      - "panel/backend-go/internal/controlplane/service/pki_revocation*.go"
      - "panel/backend-go/internal/controlplane/http/**"
      - "panel/backend-go/internal/controlplane/app/app.go"
      - "panel/backend-go/cmd/nre-control-plane/**"
    outcomes:
      - "现有 PANEL_BACKEND listener、NRE_MASTER_URL、heartbeat/revision/task URL 与 X-Agent-Token control authentication 保持不变且不要求客户端证书。"
      - "现有 registration payload/response 和 authenticated control sync 承载 CSR、tunnel certificate、trust/safety snapshot 与 ack，不新增监听器或端口。"
      - "relay CA/listener 从 generic internal_ca 迁移到 PKI identity，公网 ACME/update loop 继续只处理其原有候选。"
      - "agent revoke 会禁用 control token并关闭已有 task session；PKI API/operation envelope 暴露登记、轮转、撤销、审计、告警、备份和 activation 动作。"
      - "维护期保留 agent ID/name/tags/rule/listener 关联，完成后只关闭 relay 旧认证，不关闭 token control routes。"
    verify:
      - "cd panel/backend-go && go test -short ./internal/controlplane/http ./internal/controlplane/service ./cmd/nre-control-plane -run 'Test(PKI|Agent.*Enroll|Relay.*PKI|.*TunnelMTLSUpgrade)'"
    test: extend

  - id: pki-production-runtime
    goal: "补齐内部 PKI 的 production CA lifecycle coordinator 与受保护恢复 target，使管理动作由持久执行器真实推进并可故障恢复。"
    depends_on: [control-integration, pki-backup-lease, pki-lifecycle]
    covers: [R4, R8, R9, R10, R11]
    scope:
      - "panel/backend-go/internal/controlplane/storage/pki_authority_runtime*.go"
      - "panel/backend-go/internal/controlplane/storage/pki_restore*.go"
      - "panel/backend-go/internal/controlplane/storage/pki_repository.go"
      - "panel/backend-go/internal/controlplane/storage/pki_models.go"
      - "panel/backend-go/internal/controlplane/storage/schema.go"
      - "panel/backend-go/internal/controlplane/storage/sqlite_store.go"
      - "panel/backend-go/internal/controlplane/storage/gorm_store.go"
      - "panel/backend-go/internal/controlplane/storage/gorm_lifecycle.go"
      - "panel/backend-go/internal/controlplane/service/pki_authority_runtime*.go"
      - "panel/backend-go/internal/controlplane/service/pki_backup_restore*.go"
      - "panel/backend-go/internal/controlplane/service/pki_authority*.go"
      - "panel/backend-go/internal/controlplane/service/pki_backup*.go"
      - "panel/backend-go/internal/controlplane/service/agents_pki.go"
      - "panel/backend-go/internal/controlplane/app/**"
      - "panel/backend-go/cmd/nre-control-plane/**"
    outcomes:
      - "normal CA 与 emergency CA operation 由 production coordinator 生成 vault authority、以 canonical DB-time lease fence 原子推进持久 job/security snapshot/event，并由维护循环重入至 blocked 或 succeeded；不返回永久 pending 或 unavailable 占位结果。"
      - "日常 CA 的 dual-trust、reissue、cutover、最长 30 天 overlap 与 retire 使用 agent security/certificate ack 作为 canonical 条件；持续在线未 ack 节点在 deadline 后 blocked 并保持旧 CA active，离线节点不阻塞。"
      - "紧急 CA rotation 先持久置 relay fail-closed latch并停止旧 CA签发，再通过既有 revision/task 通道对远程 agent与合成 LocalAgentID 建立可重放的精确 revision apply+drain disable barrier；tokenless revoked目标只有在安全快照/session teardown convergence成功后才退出。missing/superseded/pointer-advanced barrier按 attempt重发，replacement最终事务重验当前目标并固化需重登记的远程 agent后才废弃旧 trust、递增 security revision、禁用受影响 token。replacement 清 token不等于退出：目标必须取得新 CA credential再收敛受限 enable revision；最终事务锁定 revision pointer并要求 desired/applied精确等于 barrier，才原子清除 latch并成功。"
      - "protected import 按 SQLite DSN、vault、外置 master key 的各自 active 卷 staging；canonical path归并 symlink/junction并拒绝 hardlink别名，同路径 store/pool 共享 lifecycle gate和跨进程 restore lock；独占锁等待与旧 cleanup 完成后均再次 checkpoint/lease-fence。敏感内容写入前先持久登记 staging cleanup manifest，主 journal落盘后独占全部 cleanup ownership并在多路径部分 rollback失败时保留恢复材料；原子切换、整体 reopen及故障 rollback/roll-forward均由启动恢复完成。敏感删除使用 durable tombstone+cleanup manifest重试；跨卷 registration 在删除后的下一次 lifecycle open发现残留时只重试并继续保留，直到再一次 clean open才移除，commit marker后的清理失败作为 cleanup-pending成功结果而非 activation failure。"
    verify:
      - "cd panel/backend-go && go test -short ./internal/controlplane/storage ./internal/controlplane/service ./cmd/nre-control-plane -run 'Test(PKI.*(AuthorityCoordinator|EmergencyAuthorityRuntime|EmergencyRelay|BackupRestoreTarget|RestoreJournal|EnrollmentStopsOldAuthority)|SQLiteRestoreLifecycle)'"
      - "cd panel/backend-go && go test -tags=integration ./internal/controlplane/storage ./internal/controlplane/service ./cmd/nre-control-plane -run 'TestIntegrationPKI(AuthorityCoordinator|EmergencyAuthorityRuntime|BackupRestoreTarget|RestoreJournal)'"
    test: new

  - id: agent-credentials
    goal: "实现 agent 侧 tunnel key/CSR、跨平台 generation 激活、安全快照持久化和嵌入式独立身份，并更新 join/upgrade 凭据流程。"
    depends_on: [control-integration, pki-production-runtime]
    covers: [R3, R5, R7, R10, R11]
    scope:
      - "go-agent/internal/model/pki*.go"
      - "go-agent/internal/modules/pki/**"
      - "go-agent/embedded/**"
      - "go-agent/go.mod"
      - "go-agent/go.sum"
      - "scripts/join-agent.sh"
      - "scripts/join-agent.test.sh"
    outcomes:
      - "remote/embedded agent 的 tunnel 私钥只在所属 data dir 生成并以受限权限保存，控制面只收到 CSR。"
      - "immutable generation 经完整验证后用 Unix rename 或 Windows ReplaceFileW/MoveFileExW 原子替换 active pointer，崩溃只留下旧或新完整状态。"
      - "agent 原子持久化 PKI domain、(epoch, revision)、trust/revocation snapshot并拒绝 downgrade；higher epoch/revision 0完整快照可恢复。"
      - "join/upgrade 使用一次性 register token取得 tunnel credential，同时保留 NRE_AGENT_TOKEN 作为现有控制协议凭据且以 0600 保存。"
      - "本地嵌入式 agent 保留进程内 control bridge并使用独立 tunnel credential store。"
    verify:
      - "cd go-agent && go test -short ./internal/modules/pki ./embedded"
      - "sh scripts/join-agent.test.sh"
    test: new

  - id: relay-mtls
    goal: "将 relay TLS/TCP 与 QUIC 数据隧道改为严格双向 mTLS，并让 transport fallback 复用同一身份、撤销和 generation 规则。"
    depends_on: [agent-credentials, control-integration]
    covers: [R3, R4, R5, R6, R8, R11, R13]
    scope:
      - "go-agent/internal/model/relay.go"
      - "go-agent/internal/control/**"
      - "go-agent/internal/modules/relay/**"
      - "go-agent/internal/app/**"
    outcomes:
      - "relay client/server 同时验证 CA、时间、EKU、DNS/IP、PKI-domain/agent/listener URI identity、cached revocation 和 CA generation。"
      - "无证书、非受信、过期、撤销、错误用途或身份不匹配全部拒绝；pin-only、单向 TLS 和非认证 fallback 从内部 relay 删除。"
      - "QUIC 到 TLS/TCP fallback 只改变 transport，不改变 mTLS verifier或 credential generation。"
      - "在线 revoke/CA security update 在五秒内关闭相关 session；控制面短时不可用时 relay 继续使用最后可信 snapshot，重连后安全更新优先。"
      - "heartbeat/revision/task control requests 继续使用原 transport/token，并有反向测试证明不会被要求客户端证书。"
    verify:
      - "cd go-agent && go test -short ./internal/modules/relay ./internal/control ./internal/app -run 'Test(Relay.*MTLS|TunnelIdentity|SecuritySnapshot|ControlProtocolUnchanged)'"
    test: extend

  - id: panel-pki
    goal: "提供与公网证书分域的内部 PKI 生命周期、告警、审计、处置、登记和受保护备份界面。"
    depends_on: [control-integration, pki-backup-lease, pki-production-runtime]
    covers: [R2, R7, R8, R9, R10, R13]
    scope:
      - "panel/frontend/src/pages/CertsPage.vue"
      - "panel/frontend/src/pages/Pki*.vue"
      - "panel/frontend/src/components/CertificateForm.vue"
      - "panel/frontend/src/components/certs/**"
      - "panel/frontend/src/components/pki/**"
      - "panel/frontend/src/components/settings/SettingsDataMgmt.vue"
      - "panel/frontend/src/components/settings/data-mgmt/**"
      - "panel/frontend/src/hooks/useCertificates*.js"
      - "panel/frontend/src/hooks/usePki*.js"
      - "panel/frontend/src/api/runtime.js"
      - "panel/frontend/src/router/**"
    outcomes:
      - "公网证书与内部 PKI 使用明确分区；内部页展示 identity、owner、purpose、chain/generation、serial/fingerprint、有效期、next action、rotation phase和错误。"
      - "管理员可一次查看 register token，并通过明确确认执行 revoke、force endpoint rotate、normal/emergency CA rotate、迁移 activation。"
      - "derived warning/critical 和 audit 查询覆盖签发、续签、轮转、撤销、拒绝、备份/恢复的对象、来源、结果与原因。"
      - "加密备份导出/导入口令只驻留单次请求内，UI 不再宣称备份未加密或持久化口令。"
      - "页面行为测试覆盖 operation polling、错误恢复、高风险确认与内部/公网边界。"
    verify:
      - "cd panel/frontend && npm test"
    test: extend

  - id: upgrade-docs
    goal: "固化 tunnel-only mTLS 的部署、升级、备份迁移与安全运维契约，不引入第二控制入口。"
    depends_on: [relay-mtls, panel-pki]
    covers: [R1, R7, R10, R11, R13]
    scope:
      - "README.md"
      - ".env.example"
      - "docker-compose.yaml"
      - "Dockerfile"
      - "docs-site/**"
    outcomes:
      - "部署材料继续只暴露现有 panel/control listener，并说明 mTLS 只覆盖 relay TLS/TCP/QUIC。"
      - "升级手册说明维护窗口、bound enrollment、稳定 agent 关联、embedded tunnel identity 和旧 relay authentication 删除点。"
      - "备份/计划迁移/force activation 文档说明口令、PKI epoch、NRE_MASTER_URL 地址更新、单活边界与离线撤销延迟。"
      - "公网 ACME、面板/API 用户和公开业务客户端不被描述为内部 mTLS consumer。"
    verify:
      - "docker compose --env-file .env.example config"
    test: not_applicable

  - id: pki-e2e
    goal: "建立多进程内部 PKI/relay mTLS 端到端夹具和 CI 契约，机械证明正常、失败、攻击、恢复与迁移行为。"
    depends_on: [relay-mtls, panel-pki, upgrade-docs]
    covers: [R2, R3, R4, R5, R6, R7, R8, R10, R11, R12, R13]
    scope:
      - "scripts/test-internal-pki-e2e.sh"
      - "tests/internal-pki/**"
      - ".github/workflows/tests.yml"
      - "TESTING.md"
    outcomes:
      - "隔离夹具启动控制面、远程 agent、embedded agent 和 relay listener，并以可注入时钟验证三分之一续签、换钥和 CA 最长 30 天换代。"
      - "攻击矩阵覆盖无证书、非受信、过期、撤销、错误 EKU/agent/listener/PKI domain及安全快照 downgrade，且 control protocol 保持 token。"
      - "处置/恢复矩阵覆盖在线五秒撤销、离线快照可用与重连收敛、ack timeout blocked、force/emergency rotate、crash recovery和跨平台 active pointer。"
      - "加密备份篡改/错误口令失败不改变状态，sanitized snapshot 无 enrollment token；跨目录/地址恢复保持 PKI/agent/关联并验证 epoch fencing与单活。"
      - "CI 区分快速与 integration 层，并保留公网 Pebble ACME 与容器构建回归。"
    verify:
      - "sh scripts/test-internal-pki-e2e.sh"
    test: new
delivery_verification:
  backend-fast:
    command: "cd panel/backend-go && go test -short ./..."
  agent-fast:
    command: "cd go-agent && go test -short ./..."
  frontend-behavior:
    command: "cd panel/frontend && npm test"
  backend-full:
    command: "cd panel/backend-go && go test ./..."
  agent-full:
    command: "cd go-agent && go test ./..."
  backend-integration:
    command: "cd panel/backend-go && go test -tags=integration -run '^TestIntegration' ./..."
  agent-integration:
    command: "cd go-agent && go test -tags=integration -run '^TestIntegration' ./..."
  internal-pki-e2e:
    command: "sh scripts/test-internal-pki-e2e.sh"
  frontend-build:
    command: "cd panel/frontend && npm run build"
  compose-config:
    command: "docker compose --env-file .env.example config"
  image-build:
    command: "docker build -t nginx-reverse-emby ."
```
