# Technical Solution

```yaml
format: solution
summary: "将内部 PKI 从公网证书模型中独立出来，以持久 CA 代次、隧道端点本地生钥/CSR、现有控制入口承载证书生命周期、relay 数据面双向 mTLS、原子凭据 generation、加密备份和可恢复轮转状态机覆盖节点隧道身份。"
```

## 目标与边界

本方案把“内部隧道身份与节点信任”设为独立安全域：控制面是唯一内部 CA 与生命周期事实 owner；每个远程 agent、本地嵌入式 agent 和内部 relay listener 是独立隧道端点身份及私钥 owner。现有控制协议、`PANEL_BACKEND_HOST` / `PANEL_BACKEND_PORT` 监听、`NRE_MASTER_URL`、heartbeat、revision、task stream 和 `X-Agent-Token` 认证保持现状；公网 HTTPS/ACME、面板/API 用户认证和公开业务客户端 TLS 也不进入内部 PKI 状态机。

方案必须同时做到：

- 内部 CA 和端点证书按 R3-R5 自动换钥、可恢复轮转，失败不破坏仍有效 generation，过期后失败关闭。
- 只有 `go-agent/internal/modules/relay` 的 TLS/TCP 与 QUIC 隧道数据面强制严格身份匹配的 mTLS；transport 可以在 QUIC 与 TLS/TCP 之间回退，隧道认证强度不可回退。
- 远程与本地嵌入式 agent 在参与 relay 隧道时使用同一证书身份与校验路径；隧道端点私钥不经过控制面 snapshot、API、日志或常规备份。
- 管理员可观察、撤销、强制轮转、紧急换 CA、查询审计，并可用受保护备份保持 PKI domain 与节点隧道身份连续。
- 首次升级允许维护窗口中断，但升级完成后 relay 隧道删除 pin-only/单向 TLS 等旧认证路径；控制协议继续使用现有 agent token，不建立第二套控制入口。

明确不改变 R13：公网 ACME、公开 Relay/HTTPS 客户端、面板/API 用户 mTLS、多活控制面、外部 KMS/HSM 和外部告警不在本期。

## Requirement anchor coverage

| Anchor | 方案落点 |
|---|---|
| R1 | 以 01 的实现证据为基线；迁移和验收分别证明旧行为、复用点与差距闭合。 |
| R2 | 内部 PKI 使用独立存储/API/页面；现有 `managed_certificates` 只保留公网 ACME、上传及公开业务证书。 |
| R3 | 持久 scheduler、剩余三分之一阈值、确定性错峰、指数退避、派生告警与过期失败关闭。 |
| R4 | 新 CA generation、双根信任、在线换证、最长 30 天 retiring deadline、逾期旧根拒绝。 |
| R5 | 所属 agent 生成 ECDSA 隧道私钥与 CSR，generation 目录 + 单一 active manifest 原子切换，私钥不传输。 |
| R6 | relay TLS/TCP 与 QUIC 使用证书链/EKU/URI 身份/有效期/撤销校验；heartbeat/revision/task 控制协议不改为 mTLS。 |
| R7 | 256-bit 一次性短时登记 token、原子消费、稳定 agent ID 绑定；嵌入式 agent 通过本地 PKI service取得独立隧道身份，控制 bridge 不改。 |
| R8 | 隧道身份撤销事务、安全 revision、既有 relay session 关闭、端点 force rotate 与 emergency CA 状态机。 |
| R9 | 内部 PKI overview、生命周期字段、派生告警、结构化持久事件与高风险确认操作。 |
| R10 | 本地隔离 vault、口令加密备份、事务化恢复、PKI domain/信任/撤销/agent 关联连续；控制地址仍由 `NRE_MASTER_URL` 更新。 |
| R11 | 协作式单活 lease + PKI epoch、计划迁移 relinquish、灾难恢复显式 force activation、隧道 mTLS 升级状态机。 |
| R12 | 快速/全量/集成/UI/镜像构建加顶层多进程 E2E，覆盖正常、失败、攻击与迁移矩阵。 |
| R13 | 内部 agent PKI 路由和模型不扩展到公网证书、面板用户或公开业务客户端。 |

## 现状约束与复用基线

- 当前 relay CA/listener 只有缺失或 host 改变时重签，没有内部续签或 CA 换代；公网 ACME renewal/backoff 不能直接改变候选条件，但其时钟注入、错误持久化和退避算法可以抽取复用。证据：`01-exploration.md#内部证书与公网证书边界`。
- 现有 managed generation 已有 stage、hash gate、ack、promotion、rollback 与重启修复；目标方案保留事务模式，但为内部 PKI 建立独立 canonical model，不继续把 `internal_ca` 叶证书塞进公网证书分类。证据：`01-exploration.md#材料持久化、投递与运行时切换`。
- heartbeat/revision/task 当前是 HTTP(S) + agent token，本方案保留其 listener、transport、认证和本地 agent 进程内 bridge；relay 当前只验证服务端，才是改为双向 mTLS 的目标。证据：`01-exploration.md#节点控制链路、隧道与身份`。
- 稳定 agent ID、长期 control token、规则/listener 关联、revision generation、operation UI 和备份 round-trip 是可复用事实；明文隧道私钥下发、明文备份和 generic internal certificate API 是删除或迁移边界。

## 目标架构

```text
Panel / public HTTPS / ACME clients
  -> existing panel listener and public certificate domain

Control-plane ↔ remote/embedded agent management traffic
  -> existing PANEL_BACKEND_HOST / PANEL_BACKEND_PORT listener
       existing registration, heartbeat, revision and task protocols
       existing X-Agent-Token control authentication
       CSR / trust / revocation payloads carried by existing authenticated flows

InternalPKIService
  -> policy + stable PKI domain identity
  -> CA generations + tunnel identities + certificates
  -> enrollment + lifecycle jobs + revocation security revision
  -> audit events + derived alerts

Internal relay agent <-> relay agent
  -> TLS/TCP or QUIC transport
       strict peer certificate identity and revocation checks
       no pin-only or unauthenticated fallback

InternalPKIService
  -> SQLite canonical facts
  -> encrypted control-plane vault
  -> public-key-only snapshot / signed trust and revocation manifests
```

### 现有入口与认证边界

- 不新增监听地址、端口、SNI、环境变量或服务端 TLS 入口。控制面继续只使用现有 `PANEL_BACKEND_HOST` / `PANEL_BACKEND_PORT`，agent 继续通过 `NRE_MASTER_URL` 访问。
- registration、heartbeat、revision、task NDJSON/SSE 的 URL、连接方式和 `X-Agent-Token` control authentication 保持现状。PKI 只在现有注册 payload、authenticated API 或 revision/snapshot 中增加 CSR、证书、trust set、security revision 和 revocation manifest 数据，不把这些控制请求改成 mTLS。
- 一次性短时 register token 只收紧现有首次注册语义；注册完成后既有 per-agent token 继续作为控制协议凭据。撤销 agent 时同时禁用该 token，但这不是隧道 TLS 握手的一部分。
- 内部 relay TLS/TCP 与 QUIC 均使用同一严格 peer verifier。QUIC 到 TLS/TCP 的 transport fallback 可以保留，但 `pin_only`、单向 TLS 或跳过有效期/EKU/identity/revocation 检查不得用于内部 relay。
- 本地嵌入式 agent 保留现有进程内 `SyncSource/StateSink` 控制 bridge；当它作为 relay client/server 时，由本地 `InternalPKIService` 自动登记独立 tunnel identity，并使用与远程 agent 相同的 credential store 与 relay peer verifier。
- 公网 HTTPS/ACME、面板/API、公开 agent asset 和公开业务 listener 继续使用现有入口与 TLS 策略，不要求内部客户端证书。

## Canonical 数据模型与 owner

内部 PKI 表由 `panel/backend-go/internal/controlplane/storage` 单一拥有，`InternalPKIService` 是唯一写入者；现有 `managed_certificates` 继续由公网证书服务拥有。

| Canonical fact | 最小字段与约束 | 生成者 / consumer |
|---|---|---|
| `pki_settings` singleton | `pki_domain_id`、CA/endpoint lifetime、audit retention、`security_revision`、`pki_epoch`、canonical `relay_fail_closed` latch、upgrade state | 首次启动事务生成；PKI service、relay verifier、backup consumer |
| `pki_authorities` | UUID、单调 generation、status、certificate PEM、encrypted key ref、fingerprint、not-before/after、retire deadline、created/retired reason | CA lifecycle job；issuer、trust bundle、UI、backup |
| `pki_identities` | UUID、kind (`agent|listener`)、stable agent/listener ref、state (`enrollment_required|active|revoked`)、current certificate ref | enrollment/migration；relay verifier、关联查询、UI |
| `pki_certificates` | random serial、identity、purpose、CA generation、public-key fingerprint、not-before/after、status、revoked reason/time、superseded ref | issuer/rotation；verifier、audit、UI、signed manifest |
| `pki_enrollment_tokens` | SHA-256 token digest、scope/new-or-bound agent ID、expires-at、consumed-at、created-by | admin/local bootstrap；enrollment transaction；原始 token 只返回一次 |
| `pki_lifecycle_jobs` | target、kind、phase、attempt、next-attempt、deadline、last error、operation ref | scheduler/admin action；worker、operation envelope、UI |
| `pki_events` | immutable event ID/type/time/source/operator/object/certificate/CA generation/result/reason/security revision | 所有安全事务同事务追加；audit API/UI/retention worker |
| `pki_instance_lease` singleton | PKI domain ID、instance ID、lease deadline、PKI epoch、state | app startup/renew/relinquish；signing、rotation、backup gate |

约束：

- `pki_authorities.generation`、certificate serial、identity+purpose active certificate 均唯一；同一 identity/purpose 最多一个 active generation。
- revoked/superseded/expired 证书和 retired CA 证书在审计保留期内不可硬删除；retired CA 私钥在 overlap 完成且不存在回滚需求后销毁，CA 公钥证书保留。
- alerts 不建立第二份事实表；由 certificate/job/event 与当前时间派生 `renewal_failed|near_expiry|identity_anomaly|revoked_peer`，API 返回稳定 alert ID。
- 通用 `operations` 只包装用户可见异步动作并引用 `pki_lifecycle_jobs`；job phase 是生命周期进度的 canonical owner。
- agent snapshot 不再携带内部 relay `key_pem`。现有控制同步只增加 CA trust set、tunnel certificate PEM、公钥指纹、security revision 和签名 revocation manifest；公网证书 snapshot 与 control authentication 保持现有行为。

## 密钥、证书与 TLS 安全基线

### 算法与身份

- 内部 CA、agent tunnel client 和内部 relay listener 新密钥统一使用 ECDSA P-256 / SHA-256；随机序列号至少 128 bit；`NotBefore` 允许 5 分钟时钟偏差。
- 内部 relay mTLS 最低 TLS 1.3，使用 Go TLS 1.3 默认 cipher suites；不提供弱 cipher、自定义跳过验证或 TLS 1.2 降级开关。该限制不改变现有 control HTTP(S) transport。
- 公网 HTTPS/ACME 与公开业务 listener 的 TLS 版本和证书算法不因本方案改变。
- agent tunnel client 证书用途为 `ClientAuth`，URI SAN 为 `spiffe://<pki-domain-id>/agent/<agent-id>`；内部 relay server 证书用途为 `ServerAuth`，DNS/IP SAN 匹配 listener endpoint，URI SAN 为 `spiffe://<pki-domain-id>/agent/<agent-id>/listener/<listener-id>`。PKI domain ID 因而是证书身份的一部分，client/server purpose 不复用证书。
- relay verifier 必须同时检查链、时间、EKU、DNS/IP（server 方向）、URI SAN 中的 PKI domain/agent/listener identity、serial/identity 在本地最后可信 revocation snapshot 中的状态，以及证书所属 CA generation。PKI epoch 和 security revision 属于签名安全快照版本，不是证书字段；control request 不调用 relay verifier。

### 有效期与调度参数

- CA 默认 10 年，可配置范围 1–20 年；endpoint 默认 90 天，可配置范围 24 小时–397 天。越界配置在 API/config validator 层拒绝，不能生成证书。
- renewal due time 固定为 `not_after - total_lifetime/3`。每个 endpoint 以 identity+certificate fingerprint 计算稳定错峰，落在 due time 后 `0..min(24h, total_lifetime/30)`；测试使用注入 clock 与 deterministic seed。
- 首次失败 1 分钟后重试，指数退避上限 6 小时并带 ±20% 稳定 jitter；成功清零。失败立刻形成 warning；连续 3 次失败或剩余有效期小于等于 `min(24h, lifetime/20)` 形成 critical（24 小时证书在最后 72 分钟进入临期 critical，而不是签发即 critical）；过期形成 failed-closed critical。
- audit 默认保留 365 天，可配置 90–3650 天；删除只清理超过保留期且不再被 active lifecycle/incident 引用的 event，当前安全状态不随事件清理。

### 私钥隔离

- 控制面只持有内部 CA 私钥；它以 AES-256-GCM 加密后存入 `<data>/pki/vault/`，每条记录使用随机 nonce，并把 PKI domain ID、generation、purpose 作为 AAD。控制面不持有远程或嵌入式 agent 的 tunnel client/listener 私钥。
- 首次启动生成 32-byte vault master key 到 `<data>/pki/master.key`，目录 0700、文件 0600；可通过 `NRE_PKI_MASTER_KEY_FILE` 指向等价的本地受限文件。master key 不进 SQLite、日志、API 或普通 tar。
- agent 与 listener 私钥只在所属 agent 的 `<data>/pki/identities/<identity>/generations/<generation>/` 生成并以 0600 保存。新 generation 先完整写入、fsync、解析验证，再由单一 `active.json` 指针切换；切换必须走跨平台 `atomicReplaceFile`：Unix 使用同目录 rename，Windows 使用 `ReplaceFileW`（目标存在）或 `MoveFileExW(MOVEFILE_REPLACE_EXISTING|MOVEFILE_WRITE_THROUGH)`（首次/替换），并在各平台完成可用的 file/directory flush。不得直接依赖 Windows 上不能覆盖目标的 `os.Rename`，也不得分别更新 active cert/key 指针。
- CSR、certificate PEM、trust set 和 public-key fingerprint 可以传输；私钥字段从内部 API/model/snapshot/backup schema 中删除，并增加结构与日志泄漏测试。

## 登记、续签与重登记事务

### 一次性登记

1. 管理员创建 32-byte 随机 token，默认 10 分钟过期；新节点 token 可不绑定 ID，升级/撤销恢复 token 必须绑定现有 agent ID。数据库只保存 digest，原始值只在创建响应显示一次。
2. agent 在本地创建 tunnel client key 和 CSR，通过现有注册 URL 与 transport 提交 token、CSR、name/tags 与可选 bound agent ID；不建立新的 TLS listener 或控制协议。
3. SQLite 单事务锁定 token，校验 scope/expiry/unused；创建或加载稳定 agent row与既有 per-agent control token，校验 CSR purpose/algorithm/identity，签发 tunnel certificate、写 identity/certificate/event，并原子标记 register token consumed。任何一步失败都不消费 token、不产生半 agent/certificate。
4. 现有注册响应扩展返回 agent ID、per-agent control token、tunnel certificate PEM、active/retiring CA trust set、PKI domain ID、PKI epoch 和 security revision。agent 验证公钥匹配后写入候选 generation并原子激活。
5. 相同 token 的并发请求只能一个成功；复用、过期、scope 错误、CSR key/identity 错误都返回稳定错误码并记录不含 token 的审计事件。

本地嵌入式 agent 由进程内 PKI service 使用一次性 bootstrap 绑定现有 `LocalAgentID`，不经过面板/API 输出，也不改变 `SyncSource/StateSink`；其 tunnel certificate 和私钥落在独立 embedded agent data dir。

### 正常续签与强制轮转

- 续签请求通过现有 per-agent token 控制认证提交；控制面从 token 对应的 stable agent row 决定 owner，body 中的 agent ID 不作为授权事实。被 revoked/disabled 的 agent token不得续签。
- agent 在 due time 或通过现有 revision/task 通道收到 force-rotate command 后生成新 key/CSR。控制面校验 control identity、old tunnel certificate、CSR purpose、public-key change，签发新证书并把旧证书标为 superseded only-after agent ack。
- agent 候选 generation 验证完成后先让新连接使用新证书，旧连接继续排空；ack 后控制面更新 active ref。失败时删除候选并保留仍有效旧 generation，job 按退避重试。
- listener key 每次都由所属 agent 重新生成 CSR；agent tunnel client key同样换钥。所有 force rotate 都走同一 lifecycle job，不提供直接覆盖 active 文件的旁路。

### 撤销与重登记

- 单节点 revoke 是一个事务：将 tunnel identity 及其 active certificates 标为 revoked、禁用对应 per-agent control token、递增 `security_revision`、追加 event、创建签名 revocation manifest，并登记当前 control/task session 与 relay session 的关闭目标。
- 现有控制服务在事务提交后拒绝该 token 并关闭其 heartbeat/task stream；当前与控制面连通的 peer 通过现有 revision/task 通道获取新 security snapshot，内部 relay runtime 在 5 秒内关闭与 revoked identity 的 session。数据面不以 snapshot 年龄作为每次握手的在线门槛：与控制面暂时失联的 peer 可继续使用最后一个签名且校验通过的完整 snapshot；恢复控制同步时必须先原子应用较新的安全 snapshot并关闭已撤销 relay，再应用普通 revision。因网络隔离而尚未收到撤销的 peer存在明确的延迟窗口，但不会为刷新 revocation 状态而把每次 relay 握手硬耦合到控制面存活。
- revoke 不删除 agent row、名称、标签、规则或 listener 关联。管理员创建绑定相同 agent ID 的新 token 后可重新登记；旧 serial 永久保持 revoked，新证书使用新 key/serial。
- “删除节点”与 revoke 分离：删除前必须先 revoke；硬删除仍按现有产品语义清理关联，但不得删除审计期内的 identity/certificate/event。UI 必须明确两种操作的不同后果。

## CA 日常轮转与紧急轮转

### 日常换代状态机

`pki_lifecycle_jobs(kind=ca_rotate)` 持久化以下单向 phase，进程重启后从最后已提交 phase 重入：

1. `prepare`：生成新 CA key/certificate 和下一 generation，验证算法/有效期，状态为 prepared；当前 active CA 不变。
2. `distribute_trust`：通过现有 heartbeat/revision 控制同步把 old+new 根作为带 security revision 的 trust set 下发；在线 agent ack 已安装两根后才进入下一 phase。“在线”按最近两个 heartbeat interval 内持续上报且能领取当前 revision 判定。每个在线 agent 默认有 1 小时 `trust_ack_deadline` 并按 lifecycle backoff 重推；持续在线但逾期未 ack 的 agent 会让 job 进入 `blocked`、产生 critical alert并列出对象，旧 CA 继续 active，不得自动跳过或 cutover。管理员只能修复后 resume，或显式 quarantine/revoke 该 agent 后将其移出在线资格集合。
3. `reissue`：agent tunnel client 和内部 relay listener 各自在所属 agent 生成新 key/CSR并取得 new-CA certificate；个体失败保留旧证书并重试。
4. `cutover`：agent relay runtime 原子切换新 tunnel generation；新 relay 连接使用 new CA，已有 old-CA relay 连接可排空。确认在线节点已切换后，新 CA 成为 active，旧 CA 成为 retiring。
5. `overlap`：old+new 继续受信，`retire_deadline` 不得晚于 cutover 后 30 天。离线节点在截止前上线仍可经 old certificate 完成一次换证。
6. `retire`：从 trust set 移除 old CA、递增 security revision、关闭仍使用 old generation 的连接并把其 certificates 标为 expired-by-rotation；销毁 old CA encrypted key，保留 CA certificate/fingerprint/event。之后旧节点只能重新登记。

`prepare` 或 trust 分发前失败可以安全丢弃新 generation；一旦发出 new-CA endpoint certificate，不允许把 old CA 恢复为唯一 active，必须继续修复当前 job或启动新的 CA generation。每个 phase 的数据库更新、文件 staging 和 ack 条件都有幂等 key，避免重启重复签发。`reissue/cutover` 对持续在线节点采用同样的 deadline/blocked/quarantine 语义；离线节点不阻塞 phase，而是在最长 30 天 overlap 内回归换证。

### 紧急 CA 轮转

- emergency action 要求输入明确风险确认和 reason。第一个 lease-fenced SQLite 事务只递增 security revision、置 canonical `relay_fail_closed=true`、创建持久 job/event 并立即停止所有旧 CA 签发；此时不得先替换或撤销 CA。replacement 提交前 enrollment、authenticated renewal 与本地签发全部拒绝且一次性 token 不得被消费；replacement 完成后仅允许显式 one-time re-enrollment 和本地嵌入式登记，authenticated renewal 仍拒绝。
- coordinator 随后通过既有 revision/task 通道，为每个仍持有可用 control token 的远程 agent，以及即使数据库尚无 agent row 也必须包含的 `LocalAgentID` 合成节点发布强制 relay-disable revision。job payload 持久化 operation/idempotency key、目标集合 digest、attempt 和逐 agent 的**精确** revision map；只有每个目标的对应 revision 本身已 `applied`、revision pointer 的 desired/applied 仍精确指向它，且 predecessor drain 到 `drained|forced`，才越过 disable barrier，更高 revision不能替代。精确 revision 若 missing/superseded或当前 pointer 已前进则以新 attempt/idempotency key 重发；failed revision走原 retry cycle。离线或未确认节点使 job 保持可重入等待。显式 quarantine/revoke 虽会原子清空 control token，但在签名安全快照发布和 session teardown 的持久 convergence job 成功前仍留在 disable 目标；只有该收敛完成后才可退出，不能用空 token 冒充排空事实。
- 越过 disable barrier 后，一个新的 lease-fenced 事务才把所有旧 CA 标为 revoked、递增 security revision、禁用受影响 agent control tokens并激活全新 CA generation；canonical `relay_fail_closed` **继续保持 true**，不进入双信任 phase。提交 replacement 的同一事务重新锁定当前 agent 目标集合、复核精确 disable revision，并把当时仍持有 token 的远程目标固化为 `required_reenrollment_agent_ids`；若出现迟到/重新启用节点则退回 `relay_disable_pending`，不得提交 replacement。任何生成或提交失败都保持 fail-closed latch，不恢复旧根。
- 旧 trust 不用于分发新根。replacement 清空 token 不会让上述固化目标自动退出：每个远程目标必须使用新的一次性 token 重新登记，取得 replacement generation 的 active certificate 和新 control token；本地嵌入式 agent自动安全登记。只有完成新 generation 重登记后又被显式撤销、且该次撤销 convergence 已成功的目标，才可作为明确隔离退出。所有其余新身份就绪后通过同一持久 revision barrier 发布 relay-enable；该 revision 的 snapshot 使用受限的 coordinator context 投影 relay 可用，但 canonical latch仍为 true，普通 snapshot/rollback 继续 fail closed。最终 succeeded 事务再次锁定当前目标集合与每个 revision pointer，复核 desired/applied 精确等于 enable barrier revision且 apply+drain完成，随后才清除 latch并把 job标记 completed；中途新增 agent或更高普通 revision会重发 enable barrier，不能提前恢复 relay。
- 紧急事件、操作者、reason、旧/新 fingerprint、受影响 identity 数量、disable/enable barrier 和最终结果持久审计。进程崩溃后按同一幂等 key 重放，不能重复生成 revision、跳过迟到节点或提前恢复 relay。

## PKI domain、单活与控制地址迁移

- `pki_domain_id` 首次初始化后只由受保护备份恢复，不从控制面 hostname/IP 派生。控制面地址变化仍只更新 agent 的 `NRE_MASTER_URL`；隧道 CA、agent/listener identity 和 relay certificate 不因控制 API 地址改变而重建。
- 同一 SQLite state 内，进程启动以 compare-and-swap 获取 `pki_instance_lease`：TTL 30 秒、每 10 秒续约。非 holder 不得解密 CA、签发、发布 trust/revocation manifest、轮转或导出可恢复备份；失去 lease 时立即关闭这些 PKI 能力，现有 panel/control listener 本身不因此改成 mTLS。
- agent 通过现有控制同步原子持久化安全快照版本元组 `(pki_epoch, security_revision)`，按字典序比较：同一 epoch 内 revision 必须单调不降；更高 epoch 无条件胜过任何旧 epoch并允许 revision 从 0 重新开始；任何更低 epoch 都被拒绝。每个新 epoch 的第一条消息必须是包含全部 trust roots、revoked serial/identity 和 policy 的完整签名 snapshot，不能是 delta，应用完成后才接收该 epoch 的增量 revision。
- 计划迁移使用单一 `quiesce-and-export` 操作：停止新签发和 PKI mutation、发布最终 revision、递增 PKI epoch、把 source 标为 relinquished，再生成受保护备份。新实例恢复后获取新 lease；agent 只更新 `NRE_MASTER_URL`，既有 control token 与 tunnel credential 保持有效，无需重新登记。
- 灾难恢复在旧实例不可用时要求高风险 `force activate`：以备份中的 epoch 加一、把 security revision 归零并发布该新 epoch 的完整签名 snapshot；因此备份内较高旧 revision 不会阻止恢复。恢复文档必须要求停止或隔离旧实例。没有外部共识/KMS时无法防御管理员恶意复制并同时运行两个完全隔离的数据库副本；本期保证协作式单活、共享 state 互斥和 agent 端 stale-epoch PKI fencing，不宣称分布式多活安全。

## 受保护备份与事务化恢复

- 新备份格式是 versioned encrypted envelope。导出时管理员必须输入口令；使用 Argon2id（默认 64 MiB、3 iterations、parallelism 1、随机 salt）派生 256-bit key，以 AES-256-GCM 加密 archive。数据库部分是事务一致的 sanitized SQLite snapshot：保留 schema、CA/trust/issuance/revocation/audit、agent control token、tunnel identity与产品关联，但在 staging copy 中清空 `pki_enrollment_tokens` 后重新执行 foreign-key/integrity 校验，再进入 envelope；它不是原数据库文件的无过滤副本。
- vault master key不直接复制到目标。导出内容在 envelope 内携带可由口令解开的 key material；恢复时生成新的本地 vault master key并重加密 CA key。
- passphrase 只存在于单次 request memory，不写 operation payload、日志、事件或浏览器持久状态。备份 manifest 含 format version、PKI domain ID、PKI epoch、条目 hash 和 schema version；AEAD/AAD 覆盖 manifest。
- restore staging 不使用任意系统临时盘：SQLite staging 放在实际 active DSN 数据库文件同目录，vault staging 放在 active vault 同一卷，外置 master key staging 放在其 active 文件同一卷，确保最终切换不退化为跨卷复制。完成 AEAD、manifest、hash、SQLite foreign key、identity/certificate/key-match 与 schema validation 后才进入 maintenance mode。SQLite DSN 先解析现有祖先的 symlink/junction 得到 canonical path；同一实体的 hardlink 入口直接拒绝，避免按字符串分裂 lifecycle group 和跨进程锁。
- 相同 canonical SQLite 路径的所有 store/pool 共享进程内 lifecycle gate；切换前停止新 transaction/open/recovery、checkpoint 全部 pool并校验 lease，随后取得跨进程独占 restore lock。等待独占锁期间其他进程可能写入，因此获得锁后再次 checkpoint并从 active DB 重验同一 lease；恢复旧 cleanup 也可能耗时，所以 cleanup 完成后还必须紧邻 close/swap 再做一次 checkpoint 与 DB-time lease fence，才关闭全部 service/runtime/read/write pool。Unix 使用同卷 rename + file/directory fsync，Windows 使用 write-through replace/move；提交后在稳定 store pointer 上重开全部 pool。
- restore target 创建受限 staging 根后、写入解密 SQLite/vault/master key 之前，先在 active DB 同目录持久登记唯一 staging cleanup manifest；pre-journal 失败只通过该 manifest 的 durable tombstone 协议清理，删除失败或崩溃由下一次 lifecycle open按目录字面 basename 前缀扫描重试（不得把 DSN 目录中的 `[]*?` 当 glob）。切换前再持久化只含受控 staging/backup 路径的主 restore journal，随后逐步交换 SQLite、vault 和 master key；主 journal 落盘即接管全部 cleanup ownership，即使多路径 promotion 后 rollback 中途失败，调用方也不得抢删 staging，启动恢复必须先完成确定性 rollback/roll-forward再处理 staging manifest。敏感 staging/旧备份删除先以 durable rename 移到确定性 `.pki-delete` tombstone，再删除 tombstone；journal完成后转为非敏感 cleanup manifest并至少保留到后续 lifecycle open。跨卷 staging registration 即使本次删除成功也不在同一进程生命周期移除：后续 open 若发现任一路径或 tombstone仍在，只清理并继续保留 registration；只有再一次 open 在入口即确认全部缺失，才删除 registration，防止 Windows 不支持目录 flush 时敏感 tombstone在清单消失后重现。commit marker 已落盘后的 reopen 状态是成功激活；后续 hook、backup/staging/tombstone或锁降级清理失败返回 `cleanup_pending=true` 的成功结果并由启动恢复继续清理，不得降格成普通 activation failure或诱导调用方重试切换。校验或 commit marker 前失败才 rollback，现有 active state 不变。
- 当前未加密 tar 不再作为新导出格式；首次升级可读取本机现有数据完成一次维护期 vault migration，但升级完成后的 API 拒绝导入未加密备份。UI 删除“未加密可迁移”的旧暗示，明确口令丢失不可恢复。
- 备份包含 active 与 retiring CA 所需密钥、证书、撤销及 lifecycle job；retired CA 只保留公钥与审计。所有 enrollment token（无论未消费、已消费或过期）都从 sanitized snapshot 排除，恢复后由管理员重新创建；manifest 记录该排除策略和 token row count=0。

## 面板、API、告警与审计

### API 分域

- 新增 `/api/pki/overview|authorities|identities|certificates|events|alerts` 只读资源，以及 enrollment-token、revoke、force-rotate、normal-CA-rotate、emergency-CA-rotate、protected-export/import 与 activation actions。
- 所有 mutation 复用现有 `202 Accepted + operation_id + status_url` envelope；operation 引用 canonical lifecycle job，不复制 phase。高风险 API 要求 reason；emergency rotate、force activate、node delete 使用显式确认 nonce，不能靠前端文案单独保护。
- 现有 `/certificates` 仅管理公网 ACME/uploaded/公开业务证书；升级迁移完成后拒绝创建 generic `certificate_type=internal_ca`。内部 listener identity 通过 PKI API 管理。

### 页面与可观察状态

- 证书页面分为“公网证书”和“内部 PKI”两个明确区域。内部页展示 logical identity、agent/listener owner、purpose/EKU、CA generation与链、serial/fingerprint、not-before/after、剩余时间、renew due/next attempt、active generation、rotation phase、revocation state 和 latest error。
- 提供创建一次性登记 token、单节点 revoke、端点 force rotate、日常 CA rotate、emergency CA rotate、受保护备份和迁移激活入口；revoke/force rotate 需要对象确认，emergency/force activate 需要输入 reason 和高风险确认。
- alert 列表展示 warning/critical、首次/最近时间、对象与可行动原因；没有邮件/Webhook/即时通讯 consumer。
- audit 查询支持 event type、identity、serial、CA generation、operator/source、result 和时间范围；签发、续签、换代、撤销、紧急操作、登记失败、mTLS 拒绝和备份/恢复均必须在结果事务中追加 event。

## 首次升级与删除边界

### 维护期升级

1. schema migration 保留 agent ID、名称、标签、per-agent control token、规则/listener 关联，生成 PKI domain ID、vault 和 CA generation 1；不新增或替换控制面 listener。
2. 现有 relay listener 转为 PKI listener identity，但不复用控制面生成的旧 listener 私钥；所属 agent 登记后生成新 key/CSR并重新签发。旧 relay CA/material 在迁移验证完成后移出 generic certificate domain。
3. 所有远程 agent 标记 tunnel `enrollment_required`。管理员为现有 agent 创建 bound one-time register token并运行更新后的 join/upgrade script；脚本保留 ID/name/tags 与 control endpoint，写 0600 control env/config 和独立 tunnel credential generation。per-agent `NRE_AGENT_TOKEN` 继续用于现有控制协议，不可用作 relay 隧道认证。
4. 本地嵌入式 agent 通过进程内 PKI service 自动取得 tunnel identity。维护页逐项显示 agent tunnel enrollment 与 listener reissue 结果；离线节点可留在 `enrollment_required`，日后用 bound token恢复。
5. 管理员完成维护 Gate 后，relay 的 pin-only/单向 TLS/无客户端证书路径永久关闭，generic internal certificate mutation关闭。heartbeat/revision/task token routes保持现状；升级完成事实写入 `pki_settings.upgrade_state=tunnel_mtls_only`。

维护期间允许 relay 隧道流量中断，但控制协议继续通过现有入口工作并承载 one-time enrollment/CSR；relay 不接受旧认证隧道。日常 renewal/rotation 不复用该中断例外。

### 删除与保留规则

- revoke 保留 agent 与产品关联；re-enroll 恢复相同稳定 ID。
- agent hard delete 是独立不可逆业务操作，先撤销并关闭连接，再按现有规则删除产品关联；PKI identity/certificate/event 在审计保留期内保留 tombstone。
- active/retiring CA、被 active certificate 引用的 CA、active identity certificate 和进行中 job 不可手工删除。
- endpoint 候选 generation 失败可清理；active pointer、last known-good材料和 canonical DB fact必须保持一致。
- relay pin-only/单向 TLS 配置、内部 relay snapshot `key_pem`、未加密备份导出和 generic internal certificate API 在升级完成时完整删除，不保留隐藏兼容开关。per-agent control token不是删除项。

## 失败、恢复与并发语义

- 证书签发、token 消费、revoke/security revision、event 追加均在一个 SQLite transaction 内；文件先 staging，数据库只引用已验证的 immutable generation。
- scheduler 通过 job lease + durable idempotency key 防止同一 identity/CA phase 并发执行；管理员重复点击返回同一 active operation或稳定 conflict，不创建双 active generation。紧急 relay disable/enable 的目标集合 digest、attempt、逐 agent精确 revision map、replacement 时固化的远程重登记集合、apply/drain barrier 和 next-attempt 均随 job payload 持久化；相同有效 barrier 重启重放不得重复分配 revision，missing/superseded或 revision pointer 已前进的 barrier必须使用递增 attempt重发。replacement 与最终 latch-clear 事务都从锁定后的当前 agent rows重新计算目标并锁定 revision pointer，迟到节点、更高普通 revision或尚未取得新 generation credential 的远程目标都会让 phase回到 pending，不能被旧 payload或空 token遗漏。
- signing 或文件写入失败不改变 active ref；agent ack 丢失时可以用 certificate/public-key fingerprint 重放确认，不重新生成私钥或重复签发。
- 进程重启先核对 DB active ref、vault key match、filesystem manifest 和 lifecycle phase；不一致时 PKI signing 与 relay runtime fail closed，panel/control protocol 保持可用并显示 recovery blocker。
- trust/revocation snapshot 使用 PKI domain ID、版本元组 `(pki_epoch, security_revision)`、issued time、完整/增量标记和 CA 签名；agent 按字典序版本规则接受并持久化，snapshot age 只产生控制面离线/安全状态可能陈旧的告警，不阻止基于最后可信 snapshot 的 relay 握手。控制恢复时安全 snapshot 优先于普通 revision；新 epoch 必须先应用完整 snapshot。
- 时钟倒退不会延长已过期证书或降低 security revision；时钟异常形成 critical alert，签发/轮转暂停但已过期连接仍失败关闭。

## 机械验收与负向矩阵

实现完成后，以下结果必须可由自动化命令观察，精确命令在 03 的 `delivery_verification` 中唯一固化：

- 两个 Go module fast/full、前端 behavior suite 与容器构建通过；新增内部 PKI storage/service/HTTP/agent runtime 测试进入相应模块。
- 新增顶层 `scripts/test-internal-pki-e2e.sh` 使用隔离临时目录构建并启动一个控制面、一个远程 agent、一个本地嵌入式 agent和内部 relay listener；测试可注入时钟/缩短 lifetime，不等待真实 30 天。
- 正常矩阵证明：一次性登记、token 并发单消费、隧道端点三分之一自动换钥、listener 原子切换、日常 CA old+new 分发/换证/retire、持续 relay 流量无可感知失败，同时 heartbeat/revision/task 请求仍沿用原入口和 token；短时断开控制面后 relay 可用最后可信 snapshot继续握手，恢复控制连接时先应用新安全 snapshot。
- mTLS 负向矩阵仅作用于 relay TLS/TCP/QUIC：分别构造无证书、非受信 CA、过期、revoked、错误 EKU、错误 agent ID、错误 listener ID和证书 URI 中错误 PKI domain；每项均拒绝且生成结构化 event。安全同步另行验证 lower epoch、同 epoch lower revision、缺少完整新-epoch snapshot和无效签名均被拒绝，而 `(higher epoch, revision 0)` 可用于灾难恢复。对应控制请求不得被错误要求客户端证书。
- 恢复矩阵证明：候选写入/签发/ack/promotion 各阶段故障保留旧 generation，重启可重入；Windows 与 Unix 的 `atomicReplaceFile` 都能替换既有 active pointer且崩溃后只出现旧或新完整 generation；失败持续至过期后关闭且无 pin-only/单向 TLS relay 回退。
- 处置矩阵证明：在线 peer 收到 revoke 后 5 秒内既有连接断开且旧 serial 无法重连；pending revocation convergence 即使 token 已清空也不能越过 disable barrier；隔离 peer 保持数据面可用并在恢复控制同步后先应用撤销、随即关闭旧 relay；bound token 重登记保留产品关联；force rotate 只改变目标；emergency CA 在全部当前目标应用并 drain 各自精确 relay-disable revision 前绝不替换旧 CA，高 revision不会冒充屏障且 superseded/pointer-advanced revision会重发；replacement/finalize 事务注入迟到 agent时均保持 latch并回到 pending，空 agents 表中的嵌入式 `LocalAgentID` 仍进入 barrier；replacement 前旧 CA签发全部停止且 token不消费，替换后旧根凭据全部拒绝且 replacement 清空的远程 token不会自动退出，必须先用新 CA重登记再应用精确 enable revision；enable applied 后插入并应用更高 relay-disabled revision仍保持 latch并重发 enable barrier，同一最终事务精确复核当前 pointer后才清除 latch。
- 迁移矩阵证明：sanitized SQLite snapshot 的 enrollment-token row count 恒为 0；加密备份错误口令/篡改/不完整均不改变当前状态；正确恢复到新目录/地址后 PKI domain、CA、撤销、agent ID/control token/关联连续，agent 只改 `NRE_MASTER_URL`；自定义 DSN/vault/master-key 路径均在各自 active 卷 staging；同 SQLite 路径的多个 store/pool 同步关闭重开，新 open/recovery 在切换期间阻塞，跨进程 restore lock 互斥；独占锁及旧 cleanup 等待后租约均会再次复核，敏感 staging 在写入前登记 cleanup manifest，注入 tombstone 删除失败或模拟跨卷断电后 tombstone重现时，首次重启只清理并保留 registration、下一次 clean open才移除清单；含 glob 元字符的 DSN 目录仍按字面枚举 registration；多路径部分 promotion/rollback 失败时主 journal持续拥有 staging并在重启后恢复；journal 可在崩溃后 rollback/roll-forward；共享 state 双实例与 stale PKI epoch 实例被拒绝，higher epoch/revision 0完整快照可覆盖旧 epoch 的更高 revision。
- CA 换代测试注入持续在线但不 ack 的 agent，证明 deadline 后 job 进入 blocked、旧 CA仍 active且产生 critical；修复 ack 或显式 quarantine/revoke 后才可恢复推进，离线 agent 不阻塞且可在 overlap 内回归。
- 泄漏检查对 API JSON、snapshot、operation/event、日志、普通文件列表和 backup envelope 扫描，确认不存在原始 token 或 PEM private key；凭据目录权限在受支持平台满足 0700/0600。
- UI 行为测试覆盖生命周期字段、derived alert、登记 token 一次显示、高风险确认、operation polling、错误恢复及公网/内部证书分域。

## 风险与明确接受边界

- **改造跨度大**：relay 认证/transport、证书、备份和 UI 同时受影响；以独立 PKI model 和可重入 lifecycle job 限制跨模块共享状态，任务计划必须按 canonical owner 先后拆分。
- **首次升级中断**：所有远程 agent 需要 bound token取得 tunnel credential；这是 R11 已接受的维护成本，升级页必须在关闭旧 relay authentication 前展示未完成节点。控制协议不随之切换。
- **证书时间敏感**：错误时钟可能造成集群失败关闭；加入 clock anomaly alert 与测试时钟，绝不通过忽略有效期缓解。
- **隔离期间撤销延迟**：同时失去控制连接的 relay peer无法得知新的撤销事实；为避免控制面故障让数据面立即停摆，它继续使用最后可信 snapshot，撤销在控制恢复后优先收敛。在线节点仍满足 5 秒断连目标；本期不宣称在无通信条件下实现全局即时撤销。
- **备份口令丢失**：没有外部 KMS 时无法恢复；UI/文档明确不可恢复，系统不保存口令副本。
- **离线数据库克隆**：无外部共识时不能阻止恶意管理员同时运行两个完全隔离的克隆；本期用共享-state lease、迁移 relinquish 和 agent PKI-epoch fencing满足协作式单活，不把它描述为多活或拜占庭防护。
- **公网兼容**：内部 TLS 1.3、ECDSA 与 URI identity 只用于 agent/internal relay；公网 ACME 和公开客户端 TLS 保留现有策略，避免 R13 回归。

## 已收敛的 Unknowns

- 有效期范围、算法/TLS 基线、错峰/退避、告警阈值、审计保留期、私钥静态保护和备份封装已在本方案给出单一默认与 validator 边界。
- PKI domain、CA 并信/撤销、隧道端点 CSR、既有 relay 连接断开、升级登记和协作式单活已有 canonical owner 与失败语义；control protocol 明确保持现状。
- 仍需实现期以实际 package API 校准字段/函数命名；不得改变上述事实 owner、认证边界、密钥不传输、升级删除边界或机械验收结果。如仓库约束迫使改变这些语义，必须回到方案 Gate，而不是在任务实现中静默分叉。
