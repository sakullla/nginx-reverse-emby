# Task pki-storage

## Attempt History

```yaml
format: task_attempt_history
task_id: pki-storage
attempts:
  - attempt_id: pki-storage-A1-417f4433e4768fdc
    baseline_ref: 251e5fa109d06467faee1c25b976030effe5aee0
    observation_base_ref: 251e5fa109d06467faee1c25b976030effe5aee0
    checkpoint_ref: b3467db9f8ef4f886821864369e4f53cfe955826
    execution:
      outcome: done
      summary: 已完成独立内部 PKI schema、事务化 repository、旧 relay/internal_ca 迁移来源识别、统一策略校验及 AES-256-GCM CA vault，并新增聚焦不变量测试。
      verification_refs: [cd panel/backend-go && go test -short ./internal/controlplane/storage ./internal/controlplane/service -run 'Test(PKI|InternalPKI|Vault|Policy)' — exit 0]
      concerns: []
      checkpoint_ref: b3467db9f8ef4f886821864369e4f53cfe955826
    review:
      decision: changes_requested
      summary: 最终存储批次的非缓存聚焦测试与 go vet 通过，但 canonical repository 可写入悬空或相互矛盾的安全记录，完整状态读取也不是一致快照。结合上一批既有 P1，任务需要返工。
      findings: ["P1: panel/backend-go/internal/controlplane/service/pki_crypto.go:225-249 直接以 canonical 路径创建并写入 master key/vault record；进程终止或掉电可能留下零长/截断文件，之后 OpenPKIVault 会拒绝 master key，SealCAKey 则因 EEXIST 永久无法重试，破坏 CA 可用性和生命周期可重入性。最小修复：在同目录受限临时文件中完整写入、fsync、关闭后原子且 no-clobber 地发布，并同步父目录、处理并发发布；增加截断文件及失败后重试测试。", "P2: panel/backend-go/internal/controlplane/service/pki_policy.go:107-151 接受空 stableIdentity；调用方漏传 identity+certificate fingerprint 时，所有证书会得到相同 renewal stagger 和 retry jitter，失去方案要求的逐端点稳定错峰。最小修复：拒绝空白稳定种子，最好显式接收/校验 identity 与 certificate fingerprint，并补空输入及不同证书产生稳定差异的测试。", "P1: panel/backend-go/internal/controlplane/storage/pki_models.go:49-109、panel/backend-go/internal/controlplane/storage/pki_repository.go:57-128 未声明跨表外键，repository 也不验证 identity/current-certificate、certificate/identity/authority 及 CA generation/domain 的对应关系；现有测试甚至先写入指向尚不存在证书的 identity。因此公开 mutation boundary 可以成功提交悬空证书或错配 CA 的 canonical state，后续 restore 的 foreign-key 校验也无约束可检查。最小修复：增加可跨支持数据库执行的外键/延迟约束或事务提交前完整关联校验，并补不存在引用、跨 domain、错 generation 与回滚测试。", "P1: panel/backend-go/internal/controlplane/storage/pki_repository.go:184-222 的 LoadPKICanonicalState 以多个独立 autocommit 查询拼装安全状态；并发生命周期事务可在查询之间提交，使返回值混合不同 security revision/epoch 的 settings、证书、事件和 lease，进而污染安全快照或备份。最小修复：在同一数据库 read transaction/snapshot 中完成全部读取，或强制调用者提供 transaction-scoped store，并增加并发提交下的一致性测试。", "P2: panel/backend-go/internal/controlplane/storage/pki_repository.go:275-278 的 validCertificateOnlyPEM 仅搜索 PEM 标记文本，任意不可解析内容都能作为 certificate 写入，且 serial/fingerprint 未与证书核对；当前测试使用的伪 PEM 也掩盖了该问题。最小修复：完整 pem.Decode 并 x509.ParseCertificate、拒绝额外非证书 block，并核对 serial、fingerprint及必要 CA 属性，补畸形和字段不匹配测试。"]
      observation_base_ref: 251e5fa109d06467faee1c25b976030effe5aee0
      checkpoint_ref: b3467db9f8ef4f886821864369e4f53cfe955826
  - attempt_id: pki-storage-A2-4fd6ce35931cf660
    baseline_ref: b3467db9f8ef4f886821864369e4f53cfe955826
    observation_base_ref: 251e5fa109d06467faee1c25b976030effe5aee0
    checkpoint_ref: 155b2e95aee1a5f32cddf11306e5900c09a4f5a7
    execution:
      outcome: done
      summary: 已修复全部 A2 审查项：原子 no-clobber vault 发布与重试、显式 jitter seed、提交前关系校验与回滚、单一读取快照，以及严格 X.509 PEM/元数据校验；补齐相应并发和负向测试。
      verification_refs: [cd panel/backend-go && go test -short ./internal/controlplane/storage ./internal/controlplane/service -run 'Test(PKI|InternalPKI|Vault|Policy)' — exit 0, cd panel/backend-go && go vet ./internal/controlplane/storage ./internal/controlplane/service — exit 0]
      concerns: []
      checkpoint_ref: 155b2e95aee1a5f32cddf11306e5900c09a4f5a7
    review:
      decision: changes_requested
      summary: 非缓存完整 storage 快层及任务聚焦测试均通过；新增测试机械覆盖关系校验回滚、错域/错 generation/错 signer、PEM 元数据和并发一致读，schema bootstrap 未出现现有测试回归。但前两批仍有未解决 P1/P2。
      findings: ["P1: panel/backend-go/internal/controlplane/service/pki_crypto.go:249-296、185-207 在临时文件 fsync 或 hard-link 发布后若进程终止，不会清理 `.master.key.tmp-*`；若终止发生在 link 后，该路径还是 active master key 的永久硬链接。正常路径的 remove 失败还会被调用方读取 canonical 后当作幂等成功，可能让原始 master key 以未被备份排除的临时名称长期存在并泄入普通归档。最小修复：采用带进程间协调的可恢复 staging/publish 协议，启动时安全回收 unpublished temp 和 canonical 同 inode 的残留链接，且 cleanup 未确认成功时不得静默成功；增加重启前预置完整 temp、active hard-link alias 与 remove 失败测试。", "P2: panel/backend-go/internal/controlplane/service/pki_crypto.go:185-188、245-309 把 `os.Link` 当作 portable no-clobber primitive，且读取既有外部 master key 也强制 fsync 其父目录；FAT/ReFS 部分版本、部分 FUSE/网络卷或 secret-manager 挂载可能不支持 hard link 或 directory fsync，导致有效配置无法打开或首次初始化失败。最小修复：提供按平台/文件系统可靠的原子 no-clobber 发布实现或明确 fallback，并仅同步本次实际修改的目录；补不支持 link/fsync 的错误注入测试。", "P2: panel/backend-go/internal/controlplane/storage/pki_repository.go:304、validatePKILeafCertificate 仅用 CheckSignatureFrom 验证签名，并只检查所需 EKU 是否存在；它不会校验 leaf 的 issuer DN/AKID 与 authority 匹配，也会接受同时含 ClientAuth 和 ServerAuth 的证书。此类记录可通过 canonical commit，却可能在真实 TLS chain verification 时失败或突破 client/server purpose 最小权限。最小修复：用仅含目标 authority 的 roots 执行 x509.Verify 或显式校验 issuer/AKID，并拒绝 Any/相反认证 EKU；补错误 issuer 和双 EKU 负向测试。", "P2: panel/backend-go/internal/controlplane/storage/pki_repository.go:346-350 对 SupersededByID 只验证存在且 identity/purpose 相同，允许自引用、A→B→A 循环以及 active certificate 携带 superseding reference。该状态会破坏证书 lineage、retention 和 lifecycle 遍历。最小修复：要求只有 superseded 状态可设置引用，拒绝 self-reference，并在 commit 校验整张 supersession 图无环且 replacement 时间顺序有效；补自环和多节点环回滚测试。", "P1: panel/backend-go/internal/controlplane/service/pki_crypto.go:249-296、185-207 硬崩溃或持久 remove 失败仍会留下 `.master.key.tmp-*`；link 后残留是 active master key 的额外硬链接，且调用方可能读取 canonical 后静默成功，使密钥落入未排除该临时名称的普通归档。最小修复：采用可恢复且有进程间协调的 staging/publish 协议，安全回收 unpublished temp 与 canonical 同 inode 的别名，并补重启残留和 remove 失败测试。", "P2: panel/backend-go/internal/controlplane/service/pki_crypto.go:185-188、245-309 依赖并非所有支持文件系统都提供的 hard link，并在仅仅读取既有外部 master key 时强制 directory fsync；部分 FUSE、网络卷、secret-manager 挂载及 Windows 文件系统会拒绝有效配置。最小修复：提供平台可靠的 no-clobber publish/fallback，只同步本次实际修改的目录，并注入 link/fsync 不支持测试。", "P2: panel/backend-go/internal/controlplane/storage/pki_repository.go:304、validatePKILeafCertificate 未绑定 issuer DN/AKID，且允许同时具有 ClientAuth 与 ServerAuth 的证书；canonical commit 可接受真实 TLS 验证失败或 purpose 过宽的证书。最小修复：针对唯一 authority 执行 chain verification 或显式校验 issuer/AKID，并拒绝 Any/相反认证 EKU，补负向测试。", "P2: panel/backend-go/internal/controlplane/storage/pki_repository.go:346-350 允许 SupersededByID 自引用、循环和 active certificate 携带替代引用，会破坏 lineage 与 retention 遍历。最小修复：校验状态语义、禁止自引用并验证整张 supersession 图无环及时间顺序，补回滚测试。"]
      observation_base_ref: 251e5fa109d06467faee1c25b976030effe5aee0
      checkpoint_ref: 155b2e95aee1a5f32cddf11306e5900c09a4f5a7
  - attempt_id: pki-storage-A3-fde8d77d72ff217e
    baseline_ref: 155b2e95aee1a5f32cddf11306e5900c09a4f5a7
    observation_base_ref: 251e5fa109d06467faee1c25b976030effe5aee0
    checkpoint_ref: 8728eedb62f3fec75508053866cb039534fc1715
    execution:
      outcome: done
      summary: 完成 A3：可恢复的跨进程锁定原子发布、staging 清理与故障注入；强化 X.509 issuer/AKID/EKU 和 supersession 图约束，并补齐负向测试。
      verification_refs: ["Windows: cd panel/backend-go && go test -count=1 -short ./internal/controlplane/storage ./internal/controlplane/service -run 'Test(PKI|InternalPKI|Vault|Policy)' — exit 0", "Windows: cd panel/backend-go && go vet ./internal/controlplane/storage ./internal/controlplane/service — exit 0", "Linux/amd64 cross-compile: cd panel/backend-go && go test -c ./internal/controlplane/service — exit 0", git diff --check — exit 0]
      concerns: []
      checkpoint_ref: 8728eedb62f3fec75508053866cb039534fc1715
    review:
      decision: changes_requested
      summary: 最终批次的负向用例与 schema 回归检查完成；两组非缓存 Go 测试均通过，但此前批次发现的原子发布与错误路径问题仍未解决。
      findings: ["P1: panel/backend-go/internal/controlplane/service/pki_crypto.go:44-51、350-367、479-483 以 `Lstat` 后调用 `os.Rename` 实现发布；advisory lock 无法约束 A2/外部 writer，Unix 上竞争者在检查后创建的 canonical 会被 rename 覆盖，并非原子 no-clobber。Windows 路径又不使用 WRITE_THROUGH 且目录 sync 直接成功返回，掉电后已报告成功的 master-key rename 可能丢失。最小修复：实现平台专用 atomic no-replace publish（冲突返回 ErrExist），Windows 使用具备 write-through 语义的 API，Unix 成功后 fsync 目录；补忽略 lock 的竞争 writer 与发布后故障测试。", "P2: panel/backend-go/internal/controlplane/service/pki_crypto.go:87-103、213-225 对已有外部 MasterKeyFile 走无锁快速返回，不扫描 A2 遗留的 `.master.key.tmp-*`；若旧进程在 hard-link 后崩溃，active master key 的额外别名会永久保留并可能进入普通归档。最小修复：对可写、由本程序管理过 staging 的外部目录执行协调清理；无法安全清理时检测残留并显式失败，补外部 canonical 与同 inode 旧 alias 的升级测试。", "P2: panel/backend-go/internal/controlplane/service/pki_crypto.go:377-384 的 lock.Close 错误未标记为 cleanup failure；当 canonical 已存在且内容相同，SealCAKey 会因复合错误仍满足 `errors.Is(err, os.ErrExist)` 而返回成功，吞掉 unlock/close 失败，潜在遗留进程内锁。最小修复：将 lock release 和 staging-directory sync 错误包装为 errPKIVaultCleanup，或使用只代表纯冲突的 typed error，幂等成功前拒绝任何附加错误；补 ErrExist 与 Close 错误同时发生的注入测试。", "P2: panel/backend-go/internal/controlplane/service/pki_crypto_test.go:115-214 的并发测试仅在同一测试进程内启动 goroutine，所谓 restart/crash 也只是手工预置文件；没有子进程持锁后被终止且不调用 Close 的场景，也未在 Linux 运行锁测试。因此 Unix flock 或 Windows LockFileEx 的跨进程崩溃释放、随后清理与 canonical 保留发生回归时测试仍会通过。最小修复：增加 helper subprocess 获取真实锁、通知父进程后被 kill/直接退出，再由第二进程限时获取锁并验证恢复；在 Windows 与 Linux 实际执行。", "P2: panel/backend-go/internal/controlplane/service/pki_crypto_test.go:216-289 的 cleanup 注入只覆盖 remove 失败，未覆盖“canonical 已存在 + lock.Close 失败”的复合错误；当前 SealCAKey 可因错误仍匹配 os.ErrExist 而返回成功。最小修复：注入 Close 返回错误的 pkiDirectoryLock，在相同 canonical 的幂等 Seal 中断言错误不得被吞且后续锁仍可获取。", "P2: panel/backend-go/internal/controlplane/service/pki_crypto_test.go:256-289 的 rename 测试只让 rename 直接返回 Unsupported，没有在 Lstat 与 rename 之间创建竞争 canonical；因此 `os.Rename` 覆盖竞争 winner 的非 no-clobber 行为是假阴性。最小修复：在 rename hook 中先发布不同 winner 再执行真实 rename，断言 winner 永不被替换，并针对平台专用 publish primitive 验证冲突精确返回 os.ErrExist。", "P2: panel/backend-go/internal/controlplane/storage/pki_repository.go:489-493、536-539 对 SupersededByID=nil 直接跳过，因此 status=superseded 但没有 replacement 的证书仍可提交；lineage 与 retention 无法确定其后继，状态/引用约束只实现了单向校验。最小修复：要求每个 superseded certificate 必须具有 SupersededByID，而非 superseded 状态必须没有该引用，并补“superseded without replacement”回滚测试。", "P1: panel/backend-go/internal/controlplane/service/pki_crypto.go:350-367,479-483 采用先 Lstat、后 os.Rename 的发布流程；在旧版或非协作写入者竞态下，Unix rename 可覆盖已发布密钥，Windows 路径也缺少 write-through/目录持久化保证，可能导致 CA 密钥丢失或崩溃后状态不一致。改用平台级原子 no-replace 发布，并在 Windows 使用 write-through、Unix fsync；补充真实跨进程竞态和崩溃测试。", "P2: panel/backend-go/internal/controlplane/service/pki_crypto.go:87-103,213-225 使用外部已有 master key 时提前返回，绕过 A2 遗留 staging/hardlink 清理，敏感别名可长期残留。进入该快速路径前仍应在目录锁内执行恢复清理，并添加外部密钥重启回归测试。", "P2: panel/backend-go/internal/controlplane/service/pki_crypto.go:377-384 将 lock.Close 错误与 os.ErrExist 合并后，幂等密钥比较路径可能返回成功并吞掉解锁/关闭失败。应区分纯发布冲突与锁释放错误，后者必须传播，并注入 Close 失败测试。", "P2: panel/backend-go/internal/controlplane/storage/pki_repository.go:489-493,536-539 仍允许 status=superseded 且 SupersededByID=nil，留下无替代节点的断裂 lineage；本批新增测试也未覆盖此反向约束。要求 superseded 状态必须引用有效替代证书，并增加事务回滚用例。"]
      observation_base_ref: 251e5fa109d06467faee1c25b976030effe5aee0
      checkpoint_ref: 8728eedb62f3fec75508053866cb039534fc1715
  - attempt_id: pki-storage-A4-8d48a1287b0961c6
    baseline_ref: 8728eedb62f3fec75508053866cb039534fc1715
    observation_base_ref: 251e5fa109d06467faee1c25b976030effe5aee0
    checkpoint_ref: 781b3613ca2bda3877717ff41d99ef3c184eef96
    execution:
      outcome: done
      summary: A4 精确收口完成：平台原子 no-replace 发布、外部 master staging 恢复、纯冲突幂等语义、崩溃释放锁测试及完整 supersession 引用约束均已实现并验证。
      verification_refs: ["Windows actual: cd panel/backend-go && go test -count=1 -short ./internal/controlplane/storage ./internal/controlplane/service -run 'Test(PKI|InternalPKI|Vault|Policy)' — exit 0", "Windows actual: cd panel/backend-go && go vet ./internal/controlplane/storage ./internal/controlplane/service — exit 0", "Linux/amd64 WSL actual: service test binary -test.run=TestVault -test.short -test.count=1 — PASS", "Linux/amd64 WSL actual: storage test binary -test.run=TestPKI -test.short -test.count=1 — PASS", "Darwin/amd64: service test binary cross-compile — exit 0", git diff --check — exit 0]
      concerns: []
      checkpoint_ref: 781b3613ca2bda3877717ff41d99ef3c184eef96
    review:
      decision: passed
      summary: 最终反向 supersession 用例命中 commit 前关系校验并验证全事务回滚，schema 仅注册独立内部 PKI canonical 模型。结合前序 vault、跨平台原子发布、锁与 repository 批次，A4 已闭环既有 correctness/security 问题；非缓存 recipe 与 vet 复核通过，跨平台证据完整。
      findings: []
      observation_base_ref: 251e5fa109d06467faee1c25b976030effe5aee0
      checkpoint_ref: 781b3613ca2bda3877717ff41d99ef3c184eef96
```

## Execution

```yaml
format: task_run
task_id: pki-storage
execution:
  outcome: done
  summary: A4 精确收口完成：平台原子 no-replace 发布、外部 master staging 恢复、纯冲突幂等语义、崩溃释放锁测试及完整 supersession 引用约束均已实现并验证。
  verification_refs: ["Windows actual: cd panel/backend-go && go test -count=1 -short ./internal/controlplane/storage ./internal/controlplane/service -run 'Test(PKI|InternalPKI|Vault|Policy)' — exit 0", "Windows actual: cd panel/backend-go && go vet ./internal/controlplane/storage ./internal/controlplane/service — exit 0", "Linux/amd64 WSL actual: service test binary -test.run=TestVault -test.short -test.count=1 — PASS", "Linux/amd64 WSL actual: storage test binary -test.run=TestPKI -test.short -test.count=1 — PASS", "Darwin/amd64: service test binary cross-compile — exit 0", git diff --check — exit 0]
  concerns: []
  checkpoint_ref: 781b3613ca2bda3877717ff41d99ef3c184eef96
```
