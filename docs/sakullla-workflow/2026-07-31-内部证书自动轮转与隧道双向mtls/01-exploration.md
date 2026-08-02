# Exploration

```yaml
format: exploration
summary: "现有内部 PKI 仅覆盖 relay CA 与 listener 服务端证书，没有内部自动续签、CA 换代或节点双向 mTLS；可复用基础包括 ACME 退避、revision/generation 原子投递、listener 平滑切换、稳定节点关联和现有集成测试分层。"
```

## 探索覆盖

- `control-plane-pki`：`panel/backend-go/`，覆盖内部证书、agent 注册与控制链路、持久化、备份、审计和测试入口。
- `execution-plane`：`go-agent/`、`scripts/`、`docker-compose.yaml`、`Dockerfile`，覆盖 agent 控制链路、relay、证书运行时、本地嵌入式 agent、安装升级与执行面测试。
- `panel-and-verification`：`panel/frontend/`、`TESTING.md`、`.github/workflows/`、`README.md`、`docs/`，覆盖管理界面、API consumer、测试分层与当前公开运维契约。

## 当前实现事实

### 内部证书与公网证书边界

- 当前真正由控制面内部 CA 签发的对象只有全局 `__relay-ca.internal` 和 relay listener 服务端叶证书。relay CA 使用 RSA-2048 自签并固定有效 10 年；listener 叶证书使用 RSA-2048、固定有效 825 天且只有 `ServerAuth`。证据：`panel/backend-go/internal/controlplane/service/relay_material.go:24`、`:94`，`panel/backend-go/internal/controlplane/service/relay.go:136`、`:1307`、`:1364`。
- bootstrap 和手工 issue 只在材料缺失或 PEM/key 无效时生成；已有有效 key pair 会被复用。listener 自动重签只由 `public_host` 改变触发。未发现内部证书到期阈值、定时续签、日常换钥、旧 CA 淘汰或并信截止处理。证据：`panel/backend-go/internal/controlplane/service/relay.go:984`，`panel/backend-go/internal/controlplane/service/certs.go:1101`、`:1163`。
- listener 叶证书虽然不是 CA，元数据仍使用 `certificate_type=internal_ca`；内部 CA 与内部叶证书的生命周期语义当前混在同一分类中。
- 公网 ACME 自动续签只处理 `master_cf_dns + certificate_type=acme + local agent`，并持久化错误、退避与重试。内部 relay CA/listener 使用 `local_http01`、`relay_ca|relay_tunnel`，不会进入该续签循环。证据：`panel/backend-go/internal/controlplane/service/cert_renewal.go:28`、`:316`，`panel/backend-go/internal/controlplane/service/certs.go:2125`。
- agent 本地 `internal_ca` 缺少材料时会生成 RSA-2048、自签 10 年证书并以 0600 保存，但没有自动轮转、并信或撤销。公网 ACME 本地续签默认提前 30 天，只有短期 IP 证书使用生命周期三分之一阈值，而且续签明确复用旧私钥。证据：`go-agent/internal/modules/certs/runtime.go:322`、`:1453`、`:1762`，`go-agent/internal/modules/certs/renewal.go:20`、`:176`，`go-agent/internal/modules/certs/acme_integration_test.go:175`。

### 材料持久化、投递与运行时切换

- 证书元数据在 SQLite `managed_certificates` 中；材料写入 data root 下的 generation 目录，目录/文件权限为 0700/0600。现有 generation 状态机包含 pending/active/superseded、材料 hash gate、agent 投递确认后 promotion、失败补偿和重启修复。证据：`panel/backend-go/internal/controlplane/storage/sqlite_models.go:132`、`:169`，`panel/backend-go/internal/controlplane/storage/managed_certificate_generation.go:23`、`:110`、`:230`、`:800`。
- generation/promotion 当前硬限定 `issuer_mode=master_cf_dns`，内部 `local_http01/internal_ca` 不进入该机制。证据：`panel/backend-go/internal/controlplane/service/cert_generation_promotion.go:33`、`:356`、`:399`、`:439`。
- snapshot 中的 `ManagedCertificateBundle` 同时包含 `cert_pem` 和 `key_pem`；uploaded、带材料的 `internal_ca` 和 `master_cf_dns` 都把控制面保存的私钥下发给 agent。当前 listener 私钥不是在所属 agent 本地生成。证据：`panel/backend-go/internal/controlplane/storage/snapshot_types.go:8`、`:378`，`go-agent/internal/model/certificates.go:5`，`go-agent/internal/modules/certs/runtime.go:322`。
- agent 对本地证书与密钥分别采用原子文件替换，但两者不是一个原子事务。HTTP listener 每次握手经 `GetCertificate` 读取当前证书；relay listener 把证书复制进新 generation 的 `tls.Config`，再借助 publish/drain 切换。证据：`go-agent/internal/modules/certs/runtime.go:1762`，`go-agent/internal/modules/http/runtime.go:416`，`go-agent/internal/modules/relay/generation_module_test.go:53`、`:193`。

### 节点控制链路、隧道与身份

- heartbeat、revision pull/start/report 都是 HTTP JSON POST，以 `X-Agent-Token` 鉴权；控制面自身通过 `ListenAndServe` 提供普通 HTTP。`MasterURL` 明确允许 `http://`，默认 transport 没有客户端证书、专用 Root CA、逻辑控制面 `ServerName` 或 pin。证据：`panel/backend-go/internal/controlplane/app/app.go:24`、`:39`，`panel/backend-go/internal/controlplane/http/handlers_public.go:14`，`go-agent/internal/control/http.go:14`，`go-agent/internal/control/sync_client.go:77`、`:192`、`:283`。
- task 长连接优先 HTTP streaming POST + NDJSON；特定 HEAD 探测结果会降级为 SSE GET，二者仍只使用 agent token。断线固定等待 1 秒重连。证据：`panel/backend-go/internal/controlplane/service/tasks.go:100`、`:211`，`panel/backend-go/internal/controlplane/http/handlers_tasks.go:223`、`:261`、`:349`，`go-agent/internal/control/task_client.go:75`、`:97`、`:215`、`:300`、`:436`。
- relay 数据隧道支持 TLS/TCP 与 QUIC；服务端 TLS 未配置 `ClientAuth`，客户端只验证服务端。QUIC 可显式回退到已验证的 TLS/TCP；`pin_only` 模式不检查证书有效期、用途或节点身份。证据：`go-agent/internal/model/relay.go:8`，`go-agent/internal/modules/relay/tls.go:14`、`:116`，`go-agent/internal/modules/relay/dial_runtime.go:104`。
- 稳定 agent ID 在控制面首次注册时生成随机 128-bit hex，并被规则、listener、证书目标与 revision 关联引用。执行面从启动环境读取 `NRE_AGENT_ID`，默认值为 `linux-agent`，自身不生成或持久化 ID；控制面地址也只在启动时读取。证据：`panel/backend-go/internal/controlplane/service/agents.go:415`、`:1923`，`go-agent/internal/model/config.go:25`、`:120`。
- 远程注册使用可选的单一长期 master register token，没有过期或消费记录；agent 自带长期 token 并以明文持久化。join 脚本把注册 token 与 agent token 同时放入 header 和 JSON，保存 master URL、agent token、agent ID 的 `agent.env` 时未显式设置 0600。证据：`panel/backend-go/internal/controlplane/http/auth.go:29`，`panel/backend-go/internal/controlplane/service/agents.go:1309`，`scripts/join-agent.sh:386`、`:546`、`:823`、`:902`。
- 重复安装和 `migrate-from-main` 会复用旧 agent token/name/tags，但注册 payload 不携带旧 agent ID；控制面按同名/旧 token 复用行为与关联保留存在测试基础，但脚本自身没有显式迁移 listener/rule 关联。
- 本地嵌入式 agent 使用固定 `LocalAgentID`，通过进程内 `SyncSource/StateSink` bridge 运行，无 `MasterURL`、agent token 或网络隧道；它没有独立 PKI 身份与私钥边界。证据：`panel/backend-go/internal/controlplane/localagent/runtime.go:38`，`panel/backend-go/internal/controlplane/localagent/sync_source.go:75`，`go-agent/embedded/runtime.go:53`、`:105`、`:264`，`go-agent/internal/app/embedded.go:11`，`docker-compose.yaml:27`。

### 撤销、审计、告警与单活

- agent 删除会移除 agent 行及关联规则/listener/证书目标，但当前没有证书序列、撤销状态或重登记凭据模型。task session 只在内存中保存且连接建立时鉴权一次；`AgentService.Delete` 未连接 `TaskService`，未发现按 agent 立即断开既有 stream 的入口。
- 证书 API 只有 CRUD 和 `issue`，未发现单节点证书 revoke、端点 force-rotate 或 emergency-CA 操作。执行面也未发现对应消费协议。
- 证书行可持久化 status、`last_error`、retry、`not_after`、material hash 和 per-agent report；通用 `operations`/`revision_events` 能记录部分 `certificate.*` mutation，但没有 PKI 专用审计/告警模型，也缺操作者或触发来源、证书序列/指纹、轮转阶段和 mTLS 拒绝对象字段。证据：`panel/backend-go/internal/controlplane/storage/sqlite_models.go:265`、`:338`，`panel/backend-go/internal/controlplane/storage/schema.go:32`。
- 已检查的配置和 schema 没有逻辑控制面身份、PKI owner 或 instance lease；端口绑定只能阻止同地址冲突，不能防止不同地址的两个实例使用同一份 PKI。

### 面板与管理员 consumer

- `CertsPage` 当前展示节点归属、ID/域名、用途、来源、启用/签发状态、签发/到期时间、剩余时间、标签、`last_error`、下次重试与次数，支持创建、编辑、删除和对 pending/error 的手工“签发”。证据：`panel/frontend/src/pages/CertsPage.vue:5`、`:342`、`:489`，`panel/frontend/src/components/certs/CertTable.vue:6`、`:101`，`panel/frontend/src/components/certs/CertCard.vue:23`、`:152`。
- 表单以 `scope/issuer_mode/usage/certificate_type/self_signed` 区分网站 ACME、上传证书、relay 内部证书和内部自签；上传模式接受证书、私钥和 CA 链 PEM。页面没有内部 CA 代次/信任链、序列号/指纹、下一自动动作、轮转阶段或审计事件。证据：`panel/frontend/src/components/CertificateForm.vue:4`、`:213`。
- 证书 mutation 使用 operation envelope 跟踪 revision；issuing 状态每 4 秒轮询。现有删除、revision retry/rollback 已有高风险确认模式。未命中节点证书撤销、端点强制轮转、CA 紧急轮转或重新登记的 API consumer。证据：`panel/frontend/src/hooks/useCertificates.js:7`、`:105`，`panel/frontend/src/api/runtime.js:283`、`:490`，`panel/frontend/src/components/operations/OperationStatusList.vue:45`。
- 节点列表/详情已有在线、同步、revision、错误和关联展示；join 弹窗从 `/info` 读取 `master_register_token` 并直接拼入安装命令。证据：`panel/frontend/src/pages/AgentsPage.vue:1`、`:267`、`:371`，`panel/frontend/src/pages/AgentDetailPage.vue:10`、`:325`、`:1026`。

### 备份恢复与迁移

- 备份 API 可 round-trip agent ID/token、规则/listener 关联、证书元数据及 relay CA/listener 材料。现有产物只是 gzip tar：`agents.json` 包含长期 token，`materials/.../key.pem` 包含可直接读取的私钥，没有保护封装。证据：`panel/backend-go/internal/controlplane/service/backup_types.go:51`、`:177`，`panel/backend-go/internal/controlplane/service/backup.go:2273`。
- 前端把备份定位为迁移/灾备，并在常量中声明当前备份未加密且包含节点 token、证书私钥；实际 Export/Import 界面未消费该警告。证据：`panel/frontend/src/components/settings/SettingsDataMgmt.vue:43`，`panel/frontend/src/components/settings/data-mgmt/ExportPanel.vue:56`，`panel/frontend/src/components/settings/data-mgmt/ImportWizard.vue:20`，`panel/frontend/src/utils/backupImport.js:1`。
- 现有备份/恢复保留或重映射 agent ID 与关联，为身份连续性提供数据基础；但没有逻辑控制面身份、受保护私钥封装、跨地址服务身份或单活所有权事实。

## 可复用机制

| 机制 | 当前 owner | 当前 consumer | 可复用事实与边界 |
|---|---|---|---|
| ACME renewal/backoff | 控制面 `CertificateService` 与 agent cert runtime | 公网 ACME 证书 | 已有周期检查、错误状态、退避和 retry；当前不覆盖内部证书，且执行面续签会复用私钥。 |
| generation stage/promote/rollback | 控制面 managed generation storage/service | snapshot、agent cert runtime | 已有 hash gate、投递确认、失败补偿和重启恢复；控制面路径当前限定 `master_cf_dns`。 |
| revision publish/drain | 控制面 revision/operation 服务 | agent HTTP/relay runtime、面板 operation UI | 可表达 saved/applying/applied/draining 并平滑切换 listener generation；不能直接证明 mTLS 凭据轮转。 |
| 多 CA trust input | listener 数据契约与 agent `TrustedCAPool` | relay TLS client | 可以同时装载多个 CA ID；当前没有代次、并信截止、撤销或旧 CA 淘汰语义。 |
| 稳定节点与关联 | 控制面 agent/storage/backup | 规则、listener、证书目标、revision、面板 | agent ID 与关联可持久化和备份恢复；登记协议仍使用长期 token，脚本升级不显式携带旧 ID。 |
| 通用 operation/event | 控制面 storage/service | 面板 operation 组件 | 可承载 mutation 状态与 retry/rollback UI；当前字段不足以充当完整 PKI 审计。 |
| 证书/节点管理界面 | Vue cert/agent pages 与 hooks | 面板管理员 | 已有到期、错误、重试、关联展示和确认交互；缺生命周期代次、三类安全处置及审计查询。 |
| 测试分层 | 两个 Go module、Vitest、Pebble fixture、CI | 开发与交付核验 | 已有 fast/full/integration 与真实证书/进程基础；没有内部 mTLS 多节点顶层 E2E 契约。 |

## Requirement 差距与风险

- **R1-R2**：内部与公网证书在行为上有 issuer/usage 边界，但共享表与泛化 UI；`internal_ca` 同时表示 CA 与叶证书，后续生命周期状态容易误分类。
- **R3**：只有公网 ACME 具备自动续签和退避；内部三分之一阈值、跨节点错峰、失败至过期关闭均无证据。`pin_only` 还能绕过有效期检查。
- **R4**：没有 CA 换密钥、代次、最多 30 天并信或离线逾期淘汰；现有多 CA pool 只是输入能力。
- **R5**：当前控制面生成并下发 listener 私钥，且 snapshot/备份都包含私钥；这与端点本地生钥及私钥不传输直接冲突。
- **R6**：控制链路为 HTTP + agent token，task stream 为 token + NDJSON/SSE，relay 只验证服务端；模型没有 client certificate、EKU、节点 ID、逻辑控制面身份或撤销校验。
- **R7**：注册 token 是可选静态值，缺过期和消费记录；远程 agent token 长期有效，本地 agent 走进程内旁路且无独立 PKI 身份。升级脚本的稳定 ID/关联保留依赖隐式服务端行为。
- **R8**：删除不等于证书撤销；没有既有 stream 强制断连、端点强制轮转或紧急 CA 废弃协议。
- **R9**：已有状态/到期/错误呈现，但缺要求的身份/链/指纹/下一动作/轮转阶段、PKI 审计字段、面板告警模型和三类处置入口。
- **R10**：现有备份明文包含长期 token 与私钥；没有逻辑控制面身份、受保护封装或跨地址服务身份连续性。
- **R11**：没有共享 PKI 的单活 lease/owner；首次升级维护窗口与升级完成后仅 mTLS 也没有持久状态契约。
- **R12**：局部测试基础充分，但完整攻击拒绝矩阵、轮转不断流、撤销重登、升级关联、受保护恢复/迁移和单活冲突没有顶层可重复命令。
- **R13**：公网 ACME 与公开业务流量是现有能力，后续必须维持边界；泛化证书模型/UI 增加误把内部 PKI 语义外推到公网证书的风险。

## 需要技术方案明确的 Unknowns

- 逻辑控制面身份及其与新机器、新地址服务证书的绑定表达。
- CA/端点证书分类、身份编码、EKU、TLS 最低版本、密钥与签名算法，以及可配置有效期的安全上下限。
- CA 代次、并信截止、撤销状态、离线逾期、紧急废弃和当前连接断开的持久化语义。
- 所属端本地生钥、CSR/领证、材料验证与原子切换协议，以及 listener 与本地嵌入式 agent 的私钥边界。
- 自动调度的错峰分布、重试退避、临期/失败告警阈值和审计保留期。
- 备份私钥静态保护、跨机器导入与恢复授权封装，同时保证常规 UI/API 不可明文导出。
- 共享 PKI 的单活所有权、防双实例使用和故障恢复边界。
- 首次升级如何保留稳定节点 ID/名称/标签/规则/listener 关联并只重建 mTLS 凭据。

## 后续验证焦点

- 受影响 Go 模块的快速层：`cd panel/backend-go && go test -short ./...`、`cd go-agent && go test -short ./...`。
- 前端行为层：`cd panel/frontend && npm test`；现有覆盖集中在 AgentDetail、monitor stream、operation 与 certificate hook，需注意 CertsPage、AgentsPage 和 CertificateForm 缺直接页面行为测试。
- full/integration 基础由 `TESTING.md:3`、`:27` 与 `.github/workflows/tests.yml:24`、`:140` 定义；Pebble `v2.10.1` fixture 可验证真实公网 ACME，但不能替代内部 CA/mTLS 拓扑。
- 现有可复用测试入口包括 `panel/backend-go/internal/controlplane/service/cert_generation_promotion_test.go:15`、`:404`、`:499`，`panel/backend-go/internal/controlplane/storage/managed_certificate_generation_integration_test.go:21`、`:137`，`go-agent/internal/modules/certs/generation_recovery_integration_test.go:17`，`go-agent/internal/app/hotrestart_packet_integration_test.go:26`。
- 正式交付仍需用顶层可重复命令分别证明：端点自动换钥、日常 CA 无中断换代、严格 mTLS 正反例、一次性登记、撤销与立即断连、离线逾期、失败至过期关闭、升级关联保留、重启/受保护恢复、跨地址迁移、紧急 CA 立即废弃与单活冲突拒绝。
