# Task pki-backup-lease

## Attempt History

```yaml
format: task_attempt_history
task_id: pki-backup-lease
attempts:
  - attempt_id: pki-backup-lease-A1-a3769e0bbbe6c777
    baseline_ref: 76c997c0c607e1a79b90a2e892bcf892b1922041
    observation_base_ref: 76c997c0c607e1a79b90a2e892bcf892b1922041
    checkpoint_ref: 667a3c1d73df99c1a8dfc9eb0eeec3c1ca9e88ae
    execution:
      outcome: done_with_concerns
      summary: "已实现协作式单活 PKI lease、敏感签名操作的实时 lease fencing、(pki_epoch, security_revision) 字典序防回退，以及 Argon2id + AES-256-GCM 受保护备份；导出快照会删除全部 enrollment token，恢复会在 staging 中验证 AEAD、hash、schema、foreign key 与 CA key/certificate 匹配，并支持 higher epoch/revision 0 的 force activation。"
      verification_refs: [cd panel/backend-go && go test -count=1 -short ./internal/controlplane/service -run 'Test(PKIBackup|PKILease|Backup.*PKI|PKIEpoch)' — exit 0, cd panel/backend-go && go test -count=1 -short ./internal/controlplane/service — exit 0, cd panel/backend-go && go vet ./internal/controlplane/service — exit 0, cd panel/backend-go && git diff --check && gofmt -d internal/controlplane/service/pki_backup.go internal/controlplane/service/pki_backup_sqlite.go internal/controlplane/service/pki_backup_test.go internal/controlplane/service/pki_lease.go internal/controlplane/service/pki_lease_signer.go internal/controlplane/service/pki_lease_signer_test.go internal/controlplane/service/pki_lease_test.go — exit 0]
      concerns: [PKIBackupRestoreTarget 与 PKILeaseRepository 的进程级 production storage adapter/启动接线由后续 control-integration task 提供；本 task 已固定 fail-closed CAS、consistent snapshot 与原子激活契约并以确定性 fake 覆盖。]
      checkpoint_ref: 667a3c1d73df99c1a8dfc9eb0eeec3c1ca9e88ae
    review: null
  - attempt_id: pki-backup-lease-A2-0bf5cf12a5f51c81
    baseline_ref: 667a3c1d73df99c1a8dfc9eb0eeec3c1ca9e88ae
    observation_base_ref: 76c997c0c607e1a79b90a2e892bcf892b1922041
    checkpoint_ref: 667a3c1d73df99c1a8dfc9eb0eeec3c1ca9e88ae
    execution:
      outcome: done_with_concerns
      summary: A2 已完成受控 scope 修正；沿用 A1 的 lease、backup/restore 与 epoch fencing 实现及 checkpoint，关键测试与完整 service 快速回归再次通过，无需新增源码变更。
      verification_refs: [cd panel/backend-go && go test -count=1 -short ./internal/controlplane/service -run 'Test(PKIBackup|PKILease|Backup.*PKI|PKIEpoch)' — exit 0, cd panel/backend-go && go test -count=1 -short ./internal/controlplane/service — exit 0, cd panel/backend-go && go vet ./internal/controlplane/service — exit 0, git diff --check — exit 0]
      concerns: [PKIBackupRestoreTarget 与 PKILeaseRepository 的进程级 production storage adapter/启动接线由后续 control-integration task 提供；本 task 已固定 fail-closed CAS、consistent snapshot 与原子激活契约并以确定性 fake 覆盖。]
      checkpoint_ref: 667a3c1d73df99c1a8dfc9eb0eeec3c1ca9e88ae
    review:
      decision: changes_requested
      summary: 累计审查发现 sanitized SQLite 残页泄密、整库 schema 兼容性缺失、lease term/CAS 与 deadline 校验缺陷，以及关键 repository/原子激活测试仅由 fake 自证；需修改后复审。
      findings: ["P1: panel/backend-go/internal/controlplane/service/pki_backup_sqlite.go:115 — 仅 DELETE enrollment-token rows；未启用 secure_delete 或在事务后 VACUUM/VACUUM INTO，digest、scope、绑定对象等记录内容可残留在 freeblocks/free pages，违反全部 token 被排除的 sanitized-backup 契约。最小修复是在删除前启用 secure_delete，并在提交后重建数据库或 VACUUM，再验证无残留自由页。", "P1: panel/backend-go/internal/controlplane/service/pki_backup.go:554 — schema version/hash 只与同一备份 manifest 自证一致，而 pki_backup_sqlite.go:215 仅要求 PKI 表/列存在；合法加密但旧版或缺失其他应用表/约束的整库快照可通过并在激活时替换 active DB，造成非 PKI 数据丢失或启动失败。最小修复是恢复前对照当前二进制持有的完整应用 schema/version 基线验证兼容性，或只将 PKI 数据导入当前 schema。", "P1: panel/backend-go/internal/controlplane/service/pki_lease.go:43 — CAS 契约仅携带 instance_id 与 epoch，没有每次 acquisition 唯一的 lease term/fencing token，且本批只有 interface、没有 production CAS repository；同一 instance 失租后重获时，旧 renew/relinquish 及 pki_lease_signer.go:61 的旧 signer 仍会匹配并操作新租约。最小修复是持久化 acquisition term，所有 grant、renew、relinquish 和 signer 前后检查都精确匹配该 term，并提供事务化 production repository 与并发边界测试。", "P1: panel/backend-go/internal/controlplane/service/pki_lease.go:166 — RequirePKILease 在可能阻塞的 canonical read 之前取时钟，读取完成后仍用旧 now 校验 deadline；调用可在租约已过期后返回成功，Acquire/Renew 也有同类返回窗口。最小修复是在 repository 调用返回后重新取时钟并校验返回 grant 仍存活，必要时采用 repository/数据库权威时间。", "P2: panel/backend-go/internal/controlplane/service/pki_backup_test.go:409 — 所谓失败不改变 active state 的测试仅让 fake target 在任何写入前直接返回 activationErr，因此不能证明 SQLite/vault 已部分 staging/swap/reopen 失败时仍保持旧 active。最小修复是对 production restore target 注入各激活阶段故障并核对数据库、vault 与 reopen 状态均未改变。", "P2: panel/backend-go/internal/controlplane/service/pki_lease_test.go:172 — 多实例并发仅覆盖进程内 mutex fake，且没有同 instance 失租后重获时旧 grant/signer 的 acquisition-term 用例，也没有让 ReadPKILease 阻塞跨过 deadline；因此既不能验证 production repository 的事务 CAS，也无法捕获前批确认的 fencing/过期窗口。最小修复是对真实 SQLite repository 做多连接并发测试，并增加可阻塞读取及旧 term 重获场景。"]
      observation_base_ref: 76c997c0c607e1a79b90a2e892bcf892b1922041
      checkpoint_ref: 667a3c1d73df99c1a8dfc9eb0eeec3c1ca9e88ae
  - attempt_id: pki-backup-lease-A3-d7c7e2b0c7e1a6da
    baseline_ref: 667a3c1d73df99c1a8dfc9eb0eeec3c1ca9e88ae
    observation_base_ref: 76c997c0c607e1a79b90a2e892bcf892b1922041
    checkpoint_ref: 5a9440b049be74f843772d5885a63358db50b1f0
    execution:
      outcome: done_with_concerns
      summary: A3 已修复全部阻断项：sanitized snapshot 启用 secure_delete、VACUUM、freelist 与原始 digest 反查；restore 对照目标可信完整 schema；lease 使用每次 acquisition 唯一的 256-bit term 并在 repository 返回后重查 deadline；新增真实 Gorm/SQLite CAS repository、VACUUM INTO 一致快照 source、同实例重获 fencing 与多连接竞争测试。
      verification_refs: [cd panel/backend-go && go test -count=1 -short ./internal/controlplane/service -run 'Test(PKIBackup|PKILease|Backup.*PKI|PKIEpoch)' — exit 0, cd panel/backend-go && go test -count=1 -short ./internal/controlplane/service — exit 0, cd panel/backend-go && go test -count=1 -short ./internal/controlplane/storage -run 'TestPKI' — exit 0, cd panel/backend-go && go test -count=1 ./internal/controlplane/storage -run 'Test(CaptureConsistentPKISQLite|PKIInstanceLeaseRepository)' — exit 0, cd panel/backend-go && go test -count=1 ./internal/controlplane/service -run 'TestPKILeaseGormRepositoryCoordinatesSharedSQLiteStores' — exit 0, cd panel/backend-go && go vet ./internal/controlplane/storage ./internal/controlplane/service — exit 0, git diff --check and gofmt -d scoped files — exit 0]
      concerns: [SQLite+vault staging/swap/reopen 各阶段的 production fault-injection test 仍需随 control-integration 的进程级 PKIBackupRestoreTarget 一并落地；当前 service 已在任何 pre-activation 验证失败时保证不调用 target，并固定 CAS/原子失败契约。]
      checkpoint_ref: 5a9440b049be74f843772d5885a63358db50b1f0
    review: null
  - attempt_id: pki-backup-lease-A4-f1686edb3e902486
    baseline_ref: 5a9440b049be74f843772d5885a63358db50b1f0
    observation_base_ref: 76c997c0c607e1a79b90a2e892bcf892b1922041
    checkpoint_ref: 5a9440b049be74f843772d5885a63358db50b1f0
    execution:
      outcome: done_with_concerns
      summary: A4 已完成受控 scope 修正；沿用 A3 的 hardened backup/lease 实现与 checkpoint 5a9440b0，所有 recipe、service/storage 回归、真实 SQLite CAS/snapshot 专项和 vet 均已通过，无需新增源码变更。
      verification_refs: [cd panel/backend-go && go test -count=1 -short ./internal/controlplane/service -run 'Test(PKIBackup|PKILease|Backup.*PKI|PKIEpoch)' — exit 0, cd panel/backend-go && go test -count=1 -short ./internal/controlplane/service — exit 0, cd panel/backend-go && go test -count=1 -short ./internal/controlplane/storage -run 'TestPKI' — exit 0, cd panel/backend-go && go test -count=1 ./internal/controlplane/storage -run 'Test(CaptureConsistentPKISQLite|PKIInstanceLeaseRepository)' — exit 0, cd panel/backend-go && go test -count=1 ./internal/controlplane/service -run 'TestPKILeaseGormRepositoryCoordinatesSharedSQLiteStores' — exit 0, cd panel/backend-go && go vet ./internal/controlplane/storage ./internal/controlplane/service — exit 0, git diff --check and gofmt -d scoped files — exit 0]
      concerns: [SQLite+vault staging/swap/reopen 各阶段的 production fault-injection test 仍需随 control-integration 的进程级 PKIBackupRestoreTarget 一并落地；当前 service 已在任何 pre-activation 验证失败时保证不调用 target，并固定 CAS/原子失败契约。]
      checkpoint_ref: 5a9440b049be74f843772d5885a63358db50b1f0
    review:
      decision: changes_requested
      summary: 最终批的 LeaseTerm 创建、canonical relationship 校验及测试夹具更新正确，未发现新增问题；但同一 attempt 累计的 lease 本地状态并发竞态仍是阻断项。
      findings: ["P1: panel/backend-go/internal/controlplane/service/pki_lease.go:225 — Require 在 repository read 后无条件 setGrant；若其读取旧 term 后，同一 service 的并发 Acquire 已写入并缓存新 term，旧 Require 会重新覆盖本地状态并返回已被 canonical DB fencing 的 grant。最小修复是串行化 lease 状态转换，或为本地状态增加 generation/CAS，只有调用期间本地 grant 未变化时才能提交读取结果，并增加 barrier 驱动的 Require/Acquire 竞态测试。", "P2: panel/backend-go/internal/controlplane/service/pki_lease_repository_test.go:53 — 两个真实 SQLite store 虽共享同一文件，但 Acquire 均顺序执行，没有同时进入数据库 CAS，也未验证 takeover 后旧 term 的 Renew/Relinquish 被拒绝。最小修复是用 barrier 并发启动两个 store 的 Acquire 并断言唯一赢家，再以旧 term 调用 renew/relinquish 验证 canonical 新租约不变。", "P2: panel/backend-go/internal/controlplane/storage/pki_backup_repository.go:40 — os.ReadFile 会先把完整快照无界载入内存，service 层的 512 MiB 限制只能在返回后检查；大型或异常膨胀数据库可先造成显著内存压力甚至 OOM，绕过限制的保护目的。最小修复是在读取前 stat 输出并按共享上限拒绝，或改为有界流式读取/返回文件句柄。", "P2: panel/backend-go/internal/controlplane/storage/pki_lease_repository_test.go:70 — 并发竞争前只是将现有 singleton 行标记 relinquished，因此两个 contender 仅覆盖 UPDATE 分支，没有覆盖真正 absent-row 的 INSERT/ON CONFLICT 竞争；且测试只运行 SQLite，PostgreSQL/MySQL 的行锁、upsert 与 RowsAffected 语义未被验证。最小修复是在无 lease row 的新库上并发首获，并将同一 CAS 场景纳入三种真实数据库方言测试。", "P2: panel/backend-go/internal/controlplane/storage/pki_backup_repository_test.go:40 — 测试顺序写入后立即从同一 store 捕获，未断言 WAL 模式、未确保 committed 数据仍只存在于未 checkpoint 的 WAL，也没有独立连接/进程写入重叠，因此无法机械证明其名称宣称的 WAL 与跨进程一致性。最小修复是显式启用 WAL、禁止/验证 checkpoint，使用独立 writer 提交后确认 WAL frames 存在，再执行捕获并校验完整快照。", "P1: panel/backend-go/internal/controlplane/service/pki_lease.go:225 — Require 的旧 repository 读取结果可在并发 Acquire 已安装新 term 后无条件覆盖本地 grant，并返回已被 canonical DB fencing 的旧 grant。最小修复是串行化 lease 状态转换，或以本地 generation/CAS 拒绝提交调用期间已失效的读取结果，并增加确定性竞态测试。"]
      observation_base_ref: 76c997c0c607e1a79b90a2e892bcf892b1922041
      checkpoint_ref: 5a9440b049be74f843772d5885a63358db50b1f0
  - attempt_id: pki-backup-lease-A5-bb735102f73103c3
    baseline_ref: 5a9440b049be74f843772d5885a63358db50b1f0
    observation_base_ref: 76c997c0c607e1a79b90a2e892bcf892b1922041
    checkpoint_ref: 802f0a181c870ea40f006862ebc90ea82ac1f4d4
    execution:
      outcome: done_with_concerns
      summary: A5 已串行化同一 PKILeaseService 的全部 lease transition，确定性阻止旧 Require 结果覆盖并发重获的新 term；snapshot repository 在分配前执行共享 512 MiB stat/有界读取；真实 SQLite 测试覆盖 absent-row 双连接首获、takeover 后旧 term renew/relinquish fencing，以及独立 writer 的未 checkpoint WAL 捕获。
      verification_refs: [cd panel/backend-go && go test -count=1 -short ./internal/controlplane/service -run 'Test(PKIBackup|PKILease|Backup.*PKI|PKIEpoch)' — exit 0, cd panel/backend-go && go test -count=1 -short ./internal/controlplane/service — exit 0, cd panel/backend-go && go test -count=1 -short ./internal/controlplane/storage -run 'TestPKI' — exit 0, cd panel/backend-go && go test -count=1 ./internal/controlplane/storage -run 'Test(CaptureConsistentPKISQLiteIncludesCommittedWALState|ReadBoundedPKISQLiteSnapshotRejectsOversizedFile|PKIInstanceLeaseRepositoryConcurrentFirstAcquireHasOneWinner|PKIInstanceLeaseRepositoryFencesTermsAndSerializesContenders)' — exit 0, cd panel/backend-go && go test -count=1 ./internal/controlplane/service -run 'TestPKILeaseGormRepositoryCoordinatesSharedSQLiteStores' — exit 0, cd panel/backend-go && go test -count=20 -short ./internal/controlplane/service -run '^TestPKILeaseRequireSerializesConcurrentReacquire$' — exit 0, cd panel/backend-go && go test -count=10 ./internal/controlplane/storage -run '^TestPKIInstanceLeaseRepositoryConcurrentFirstAcquireHasOneWinner$' — exit 0, cd panel/backend-go && go test -count=10 ./internal/controlplane/service -run '^TestPKILeaseGormRepositoryCoordinatesSharedSQLiteStores$' — exit 0, cd panel/backend-go && go vet ./internal/controlplane/storage ./internal/controlplane/service — exit 0, cd panel/backend-go && git diff --check and gofmt scoped files — exit 0]
      concerns: [当前工作区没有 PostgreSQL/MySQL 实例，真实数据库并发 CAS 矩阵未在本机执行；SQLite 双连接 production repository 路径已重复通过，其他方言需由相应 CI 环境验证。, SQLite+vault staging/swap/reopen 各阶段的 production fault-injection test 仍随 control-integration 的进程级 PKIBackupRestoreTarget 落地；当前 pre-activation 验证失败保持不调用 target。, 当前 Windows Go 环境 CGO_ENABLED=0 且无 C 编译器，go test -race 未执行；确定性 barrier 与真实双连接并发测试均已重复通过。]
      checkpoint_ref: 802f0a181c870ea40f006862ebc90ea82ac1f4d4
    review:
      decision: passed
      summary: 最终 repository 文件保持 LeaseTerm 必填及 canonical domain/epoch/term 校验；A5 已解决此前全部 P1。累计两项 P2 分别属于数据库停滞时的可取消性与测试确定性，不破坏 lease fencing、备份完整性或失败关闭语义，作为非阻断改进记录。
      findings: ["P2: panel/backend-go/internal/controlplane/service/pki_lease_signer.go:60 — Sign 的两次 gate 检查使用 context.Background；A5 后 Require 会持有 transitionMutex 跨 repository I/O，因此数据库停滞可无限占锁，阻塞 Maintain 取消清理及全部 lease transition。最小修复是为 crypto.Signer 的 lease 检查使用明确的短超时上下文，并测试超时后锁可释放、grant 失败关闭。", "P2: panel/backend-go/internal/controlplane/service/pki_lease_test.go:165 — 竞态测试仅以 100ms 内未收到 acquireStarted 推断 Acquire 被 transition 锁阻塞，但没有先证明 Acquire goroutine 已实际尝试进入；旧实现可能因调度延迟而通过，故不能确定性捕获回归。最小修复是使用显式 transition-entry hook，或同步断言 transitionMutex.TryLock 失败后再释放 read barrier，移除基于时间的负向判断。", "P2: panel/backend-go/internal/controlplane/service/pki_lease_signer.go:60 — Sign 的 gate 检查使用 context.Background，数据库停滞时可长期占用 transitionMutex 并延迟 Maintain 取消及其他 transition。建议使用明确的 lease-check 超时上下文，并验证超时后释放锁且失败关闭。", "P2: panel/backend-go/internal/controlplane/service/pki_lease_test.go:165 — 并发回归测试以 100ms 未收到信号推断 Acquire 被阻塞，未机械证明 goroutine 已尝试进入 transition，旧实现理论上可因调度延迟通过。建议改用显式 transition-entry hook 或同步 TryLock 断言。"]
      observation_base_ref: 76c997c0c607e1a79b90a2e892bcf892b1922041
      checkpoint_ref: 802f0a181c870ea40f006862ebc90ea82ac1f4d4
```

## Execution

```yaml
format: task_run
task_id: pki-backup-lease
execution:
  outcome: done_with_concerns
  summary: A5 已串行化同一 PKILeaseService 的全部 lease transition，确定性阻止旧 Require 结果覆盖并发重获的新 term；snapshot repository 在分配前执行共享 512 MiB stat/有界读取；真实 SQLite 测试覆盖 absent-row 双连接首获、takeover 后旧 term renew/relinquish fencing，以及独立 writer 的未 checkpoint WAL 捕获。
  verification_refs: [cd panel/backend-go && go test -count=1 -short ./internal/controlplane/service -run 'Test(PKIBackup|PKILease|Backup.*PKI|PKIEpoch)' — exit 0, cd panel/backend-go && go test -count=1 -short ./internal/controlplane/service — exit 0, cd panel/backend-go && go test -count=1 -short ./internal/controlplane/storage -run 'TestPKI' — exit 0, cd panel/backend-go && go test -count=1 ./internal/controlplane/storage -run 'Test(CaptureConsistentPKISQLiteIncludesCommittedWALState|ReadBoundedPKISQLiteSnapshotRejectsOversizedFile|PKIInstanceLeaseRepositoryConcurrentFirstAcquireHasOneWinner|PKIInstanceLeaseRepositoryFencesTermsAndSerializesContenders)' — exit 0, cd panel/backend-go && go test -count=1 ./internal/controlplane/service -run 'TestPKILeaseGormRepositoryCoordinatesSharedSQLiteStores' — exit 0, cd panel/backend-go && go test -count=20 -short ./internal/controlplane/service -run '^TestPKILeaseRequireSerializesConcurrentReacquire$' — exit 0, cd panel/backend-go && go test -count=10 ./internal/controlplane/storage -run '^TestPKIInstanceLeaseRepositoryConcurrentFirstAcquireHasOneWinner$' — exit 0, cd panel/backend-go && go test -count=10 ./internal/controlplane/service -run '^TestPKILeaseGormRepositoryCoordinatesSharedSQLiteStores$' — exit 0, cd panel/backend-go && go vet ./internal/controlplane/storage ./internal/controlplane/service — exit 0, cd panel/backend-go && git diff --check and gofmt scoped files — exit 0]
  concerns: [当前工作区没有 PostgreSQL/MySQL 实例，真实数据库并发 CAS 矩阵未在本机执行；SQLite 双连接 production repository 路径已重复通过，其他方言需由相应 CI 环境验证。, SQLite+vault staging/swap/reopen 各阶段的 production fault-injection test 仍随 control-integration 的进程级 PKIBackupRestoreTarget 落地；当前 pre-activation 验证失败保持不调用 target。, 当前 Windows Go 环境 CGO_ENABLED=0 且无 C 编译器，go test -race 未执行；确定性 barrier 与真实双连接并发测试均已重复通过。]
  checkpoint_ref: 802f0a181c870ea40f006862ebc90ea82ac1f4d4
```
