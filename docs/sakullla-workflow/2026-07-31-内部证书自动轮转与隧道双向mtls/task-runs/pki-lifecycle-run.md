# Task pki-lifecycle

## Attempt History

```yaml
format: task_attempt_history
task_id: pki-lifecycle
attempts:
  - attempt_id: pki-lifecycle-A1-938a6933c79a5e2b
    baseline_ref: 802f0a181c870ea40f006862ebc90ea82ac1f4d4
    observation_base_ref: 802f0a181c870ea40f006862ebc90ea82ac1f4d4
    checkpoint_ref: 8ae389bb9e46eeb9602d74e30cc3be1cdb6c3728
    execution:
      outcome: done_with_concerns
      summary: 已实现内部 PKI 生命周期领域层：端点按 lifetime/3 稳定错峰、指数退避、失败保留 active 与过期失败关闭，force rotate 仅作用目标；CA prepare/distribute/reissue/cutover/overlap/retire reducer 具备 lease、CAS、idempotency key、在线 ack deadline/blocked alert、离线豁免和 30 天 retire；紧急 CA 先禁用 relay 且失败不回退；撤销通过单事务 callback 绑定 identity/certificate/control-token/security revision/签名 snapshot/event/close intents，并在五秒硬期限内发布与断连；恢复同步强制安全 snapshot 先于普通 revision；审计和告警保持 repository-backed canonical facts 与纯派生投影。
      verification_refs: [cd panel/backend-go && go test -count=1 -short ./internal/controlplane/service -run 'TestPKI(Lifecycle|Authority|Rotation|Revocation|Audit|Alert)' — exit 0, cd panel/backend-go && go test -count=1 -short ./internal/controlplane/service — exit 0, cd panel/backend-go && go test -count=20 -short ./internal/controlplane/service -run 'TestPKI(Lifecycle|Authority|Rotation|Revocation|Audit|Alert)' — exit 0, cd panel/backend-go && go vet ./internal/controlplane/service — exit 0, cd panel/backend-go && git diff --check and gofmt scoped files — exit 0]
      concerns: [本 task 按计划只固定 service 层 transaction/repository/runtime contracts；Gorm production adapters、control token/session 关闭、revision publisher 与应用启动接线由后续 control-integration task 落地。, 端点本地 generation 原子替换与安全 snapshot 的 agent 持久化 consumer 由 agent-credentials/relay-mtls task 实现；当前 service 已固定验证、CAS、顺序与失败关闭契约。]
      checkpoint_ref: 8ae389bb9e46eeb9602d74e30cc3be1cdb6c3728
    review:
      decision: changes_requested
      summary: scheduler 的 lifetime/3 稳定错峰、backoff、force bypass 与 critical/failed-closed 派生正确；revocation 测试覆盖签名失败回滚、快照/事件/token/session intent、收敛 deadline 与恢复顺序，但未消除累计的 lease fencing 与 future-dated activation 问题。
      findings: ["P1: panel/backend-go/internal/controlplane/service/pki_authority.go:226 — 在第二次 RequirePKILease 后才分别调用 SavePKICARotationTransition/CommitPKIEmergencyAuthorityRotation，但 transition/commit 不携带 expected lease owner/epoch，repository 契约也只声明 job CAS；lease 可在检查与提交之间丢失，旧 holder 仍能推进或紧急替换 CA。应把 lease authority/epoch fence 纳入提交对象，并在同一 repository 事务中原子校验 lease 与 phase/state CAS。", "P1: panel/backend-go/internal/controlplane/service/pki_lifecycle.go:139 — endpoint activation/failure 与 atomic revocation 仅在 repository mutation 前后检查 lease；mutation 均不携带 owner/term/epoch fence，且 endpoint 在确认 lease 已丢失后仍调用 RecordPKIEndpointRotationFailure，revocation 的事后检查发生在提交完成后。旧 holder 因此仍可激活证书、修改 retry 状态或撤销 identity/token。应把 lease grant/fence 纳入 repository mutation，并在同一事务中原子校验未过期 lease 与 certificate/revision CAS。", "P2: panel/backend-go/internal/controlplane/service/pki_lifecycle.go:216 — 只要求 candidate.NotBefore 非零及 NotAfter 晚于当前时间，未拒绝 NotBefore 晚于当前时间的候选；错误 rotator 可使尚未生效的证书替换可用 active generation，造成端点中断。应按允许的 skew 明确校验 candidate 已生效，并增加 future-dated candidate 回归测试。", "P1: panel/backend-go/internal/controlplane/service/pki_authority.go:226 — authority、endpoint 与 revocation mutation 均未在 repository 事务内原子校验 lease owner/term/epoch；revocation 的复查发生在提交后，endpoint 甚至在确认 lease 丢失后记录 failure。旧 holder 可推进/紧急替换 CA、激活证书、修改 retry 或撤销 identity/token。应把 lease fence 纳入每个 commit/CAS 并在同一事务中验证，补充检查与提交之间失去 lease的竞态测试。", "P2: panel/backend-go/internal/controlplane/service/pki_lifecycle.go:216 — 未拒绝 NotBefore 晚于当前时间的 rotation candidate，错误 rotator 可用尚未生效的证书替换 active generation。应按允许的时钟偏差验证 candidate 当前有效，并增加 future-dated candidate 测试。"]
      observation_base_ref: 802f0a181c870ea40f006862ebc90ea82ac1f4d4
      checkpoint_ref: 8ae389bb9e46eeb9602d74e30cc3be1cdb6c3728
  - attempt_id: pki-lifecycle-A2-5c142ebbf5597097
    baseline_ref: 8ae389bb9e46eeb9602d74e30cc3be1cdb6c3728
    observation_base_ref: 802f0a181c870ea40f006862ebc90ea82ac1f4d4
    checkpoint_ref: 0fdfcd299580a0dc74ffdb2a20a80151ad963f7d
    execution:
      outcome: done_with_concerns
      summary: A2 已关闭全部 review finding：endpoint failure/activation、正常 CA transition、紧急 CA commit 与 revoke mutation 都携带完整 lease grant，repository 契约要求在同一事务把 live domain/epoch/instance/term/deadline fence 与 certificate/job/security-revision CAS 一并校验；确认失租后不再写 retry；新增 check/commit 间失租竞态测试证明无 mutation；endpoint 与 emergency CA 均拒绝 future-dated candidate。
      verification_refs: [cd panel/backend-go && go test -count=1 -short ./internal/controlplane/service -run 'TestPKI(Lifecycle|Authority|Rotation|Revocation|Audit|Alert)' — exit 0, cd panel/backend-go && go test -count=1 -short ./internal/controlplane/service — exit 0, cd panel/backend-go && go test -count=20 -short ./internal/controlplane/service -run 'TestPKI(Lifecycle|Authority|Rotation|Revocation|Audit|Alert)' — exit 0, cd panel/backend-go && go vet ./internal/controlplane/service — exit 0, cd panel/backend-go && git diff --check and gofmt scoped files — exit 0]
      concerns: [Gorm production adapters must implement the documented same-transaction lease fence with canonical database time; their wiring remains owned by control-integration and must not degrade to a preflight-only lease check., 端点本地 generation 原子替换与安全 snapshot 的 agent 持久化 consumer 仍由 agent-credentials/relay-mtls task 实现。]
      checkpoint_ref: 0fdfcd299580a0dc74ffdb2a20a80151ad963f7d
    review:
      decision: passed
      summary: A1 的 blocking lease-fence 与 future-dated candidate findings 已由 A2 关闭；scheduler 的 lifetime/3 稳定错峰、backoff、expiry flag 与 force 语义保持正确，revocation 覆盖原子回滚、提交后发布/断连及 deadline。仅剩非阻断的测试 fixture epoch 不一致。
      findings: ["P2: pki_revocation_test.go:11-34,150-180 的成功路径通过 pkiStaticLeaseGate 取得 epoch 1 lease，却构造并发布 epoch 2 revocation facts/snapshot；fake repository 只验证 fence 形状，未执行契约要求的 canonical domain/epoch 比较，因此测试会把跨 epoch mutation 当作成功。应让 fixture lease/facts epoch 一致，并让 fake 比较完整 live lease，同时增加 epoch mismatch 时不 commit/publish/close 的用例；服务提交校验也可显式绑定 Facts.PKIDomainID/PKIEpoch 与 Lease。"]
      observation_base_ref: 802f0a181c870ea40f006862ebc90ea82ac1f4d4
      checkpoint_ref: 0fdfcd299580a0dc74ffdb2a20a80151ad963f7d
```

## Execution

```yaml
format: task_run
task_id: pki-lifecycle
execution:
  outcome: done_with_concerns
  summary: A2 已关闭全部 review finding：endpoint failure/activation、正常 CA transition、紧急 CA commit 与 revoke mutation 都携带完整 lease grant，repository 契约要求在同一事务把 live domain/epoch/instance/term/deadline fence 与 certificate/job/security-revision CAS 一并校验；确认失租后不再写 retry；新增 check/commit 间失租竞态测试证明无 mutation；endpoint 与 emergency CA 均拒绝 future-dated candidate。
  verification_refs: [cd panel/backend-go && go test -count=1 -short ./internal/controlplane/service -run 'TestPKI(Lifecycle|Authority|Rotation|Revocation|Audit|Alert)' — exit 0, cd panel/backend-go && go test -count=1 -short ./internal/controlplane/service — exit 0, cd panel/backend-go && go test -count=20 -short ./internal/controlplane/service -run 'TestPKI(Lifecycle|Authority|Rotation|Revocation|Audit|Alert)' — exit 0, cd panel/backend-go && go vet ./internal/controlplane/service — exit 0, cd panel/backend-go && git diff --check and gofmt scoped files — exit 0]
  concerns: [Gorm production adapters must implement the documented same-transaction lease fence with canonical database time; their wiring remains owned by control-integration and must not degrade to a preflight-only lease check., 端点本地 generation 原子替换与安全 snapshot 的 agent 持久化 consumer 仍由 agent-credentials/relay-mtls task 实现。]
  checkpoint_ref: 0fdfcd299580a0dc74ffdb2a20a80151ad963f7d
```
