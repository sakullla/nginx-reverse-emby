# Task pki-enrollment-core

## Attempt History

```yaml
format: task_attempt_history
task_id: pki-enrollment-core
attempts:
  - attempt_id: pki-enrollment-core-A1-de2ed9976e7460f6
    baseline_ref: 781b3613ca2bda3877717ff41d99ef3c184eef96
    observation_base_ref: 781b3613ca2bda3877717ff41d99ef3c184eef96
    checkpoint_ref: a04bc6f41c7303f1cbb09135f52899644c64c315
    execution:
      outcome: done
      summary: 已实现一次性短时登记核心：256-bit digest-only token、stable agent/listener identity、严格 CSR 与 purpose/owner/key-change 绑定、同事务签发和审计，以及 LocalAgentID tokenless 内部登记；并补齐并发与失败回滚测试。
      verification_refs: [cd panel/backend-go && go test -count=1 -short ./internal/controlplane/service -run 'TestPKI(Enrollment|Identity|Token)' — exit 0, cd panel/backend-go && go test -count=1 -short ./internal/controlplane/storage -run 'TestPKI' — exit 0, cd panel/backend-go && go vet ./internal/controlplane/storage ./internal/controlplane/service — exit 0, git diff --check — exit 0]
      concerns: []
      checkpoint_ref: a04bc6f41c7303f1cbb09135f52899644c64c315
    review: null
  - attempt_id: pki-enrollment-core-A2-27a4358c0ab68486
    baseline_ref: a04bc6f41c7303f1cbb09135f52899644c64c315
    observation_base_ref: 781b3613ca2bda3877717ff41d99ef3c184eef96
    checkpoint_ref: a04bc6f41c7303f1cbb09135f52899644c64c315
    execution:
      outcome: done
      summary: A2 完成受控 scope 修正；沿用 A1 的原子登记实现与 checkpoint，精确验证再次通过，无需新增源码变更。
      verification_refs: [cd panel/backend-go && go test -count=1 -short ./internal/controlplane/service -run 'TestPKI(Enrollment|Identity|Token)' — exit 0, cd panel/backend-go && go test -count=1 -short ./internal/controlplane/storage -run 'TestPKI' — exit 0, cd panel/backend-go && go vet ./internal/controlplane/storage ./internal/controlplane/service — exit 0, git diff --check — exit 0]
      concerns: []
      checkpoint_ref: a04bc6f41c7303f1cbb09135f52899644c64c315
    review:
      decision: changes_requested
      summary: Token、CSR 和事务并发实现总体完整，但仍有两项阻断性身份与审计契约缺口。
      findings: ["P1: panel/backend-go/internal/controlplane/service/pki_enrollment.go:323-325 与 pki_enrollment_test.go:228-270——复用密钥、owner 不匹配、签名失败等登记失败仅回滚并返回错误，未追加不含 token 的失败审计事件；测试还固定断言事件数不变，导致登记攻击与故障不可审计。最小修复是在主事务回滚后通过独立事务记录脱敏的失败事件，保持 token 未消费及 identity/certificate 无残留，并补充复用、过期、scope/CSR/owner 失败断言。", "P1: panel/backend-go/internal/controlplane/service/pki_token.go:104-115、pki_enrollment.go:174-181——bound re-enrollment 只要求非空 AgentID，未确认其对应现有 canonical agent；因此可为不存在的 owner 签发 active identity/certificate。应在 token 创建及消费登记事务中校验稳定 AgentRow，并补充不存在或已删除 owner 的拒绝测试。", "P1: panel/backend-go/internal/controlplane/service/pki_enrollment.go:323-325、pki_enrollment_test.go:228-270——登记失败直接回滚返回，复用、过期、scope/CSR/owner 或签名失败均无脱敏失败审计，且测试固定事件数不变。应在保持 token 未消费及无 identity/certificate 残留的前提下独立追加失败事件并覆盖这些错误路径。"]
      observation_base_ref: 781b3613ca2bda3877717ff41d99ef3c184eef96
      checkpoint_ref: a04bc6f41c7303f1cbb09135f52899644c64c315
  - attempt_id: pki-enrollment-core-A3-cac33d86fdb287c1
    baseline_ref: a04bc6f41c7303f1cbb09135f52899644c64c315
    observation_base_ref: 781b3613ca2bda3877717ff41d99ef3c184eef96
    checkpoint_ref: 76c997c0c607e1a79b90a2e892bcf892b1922041
    execution:
      outcome: done
      summary: 已完成 A3 修正：bound token 创建及登记均校验现存稳定 AgentRow；LocalAgentID 保持内部无 token 路径；所有指定拒绝类别均在主事务回滚后追加独立脱敏失败审计。
      verification_refs: [cd panel/backend-go && go test -count=1 -short ./internal/controlplane/service -run 'TestPKI(Enrollment|Identity|Token)' — exit 0, cd panel/backend-go && go test -count=1 -short ./internal/controlplane/storage -run 'TestPKI' — exit 0, cd panel/backend-go && go vet ./internal/controlplane/storage ./internal/controlplane/service — exit 0, git diff --check — exit 0]
      concerns: []
      checkpoint_ref: 76c997c0c607e1a79b90a2e892bcf892b1922041
    review:
      decision: passed
      summary: A3 已在 token 创建与登记消费事务中复核并锁定 stable AgentRow，同时拒绝为 LocalAgentID 创建远程 bound token；原有 CSR、签发与存储不变量未受回归影响。此前 P1 身份与失败审计问题已解决，累计的测试证据缺口仅为非阻断 P2。
      findings: ["P2: panel/backend-go/internal/controlplane/service/pki_enrollment_test.go:339-450、764-770——现有失败都发生在 identity/certificate 写入之前，计数断言未证明写入后失败会回滚 supersede/current-certificate/token 等状态。应注入成功事件 ID/AppendPKIEvent 等后置失败，并断言 token 未消费、identity/current certificate 与旧证书状态完全不变且仅保留独立失败审计。", "P2: panel/backend-go/internal/controlplane/service/pki_enrollment_test.go:772-790——脱敏助手用原始 PEM 对 JSON 文本做 substring 检查；若 CSR 作为 JSON 字符串泄漏，换行会被转义而测试仍通过，也未约束允许字段集合。应解析 DetailsJSON、校验精确字段白名单，并逐值检查 token、digest、CSR/PEM 与内部错误内容。"]
      observation_base_ref: 781b3613ca2bda3877717ff41d99ef3c184eef96
      checkpoint_ref: 76c997c0c607e1a79b90a2e892bcf892b1922041
```

## Execution

```yaml
format: task_run
task_id: pki-enrollment-core
execution:
  outcome: done
  summary: 已完成 A3 修正：bound token 创建及登记均校验现存稳定 AgentRow；LocalAgentID 保持内部无 token 路径；所有指定拒绝类别均在主事务回滚后追加独立脱敏失败审计。
  verification_refs: [cd panel/backend-go && go test -count=1 -short ./internal/controlplane/service -run 'TestPKI(Enrollment|Identity|Token)' — exit 0, cd panel/backend-go && go test -count=1 -short ./internal/controlplane/storage -run 'TestPKI' — exit 0, cd panel/backend-go && go vet ./internal/controlplane/storage ./internal/controlplane/service — exit 0, git diff --check — exit 0]
  concerns: []
  checkpoint_ref: 76c997c0c607e1a79b90a2e892bcf892b1922041
```
