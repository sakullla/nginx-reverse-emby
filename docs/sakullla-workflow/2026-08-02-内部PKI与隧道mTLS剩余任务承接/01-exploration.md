# Exploration

```yaml
format: exploration
summary: "控制面内部 PKI 已完整就绪（登记/CSR 签发/签名安全快照/五秒撤销收敛/fail-closed latch/受保护备份/PKI HTTP API），缺口集中在消费侧：go-agent 完全没有 PKI 字段消费与本地生钥、relay 仍是 pin/CA 单向校验且不认识 pki_mtls、前端无 PKI API consumer 且现有 operation 轮询不吃 /pki/operations 路径、e2e 缺多进程 mTLS 夹具。"
```

## 探索覆盖

- `agent-control-sync`：`go-agent/internal/control/**`、`go-agent/internal/model/**` 与控制面 `agents*.go`、`agents_pki.go`、`pki_enrollment.go`，覆盖控制协议 PKI 字段与 agent 消费现状。
- `relay-runtime`：`go-agent/internal/modules/relay/**`、`go-agent/internal/model/relay.go`、控制面 `relay.go`、`relay_pki_repository.go`，覆盖数据面 TLS 现状与控制面 PKI relay 逻辑。
- `agent-certs-and-join`：`go-agent/internal/modules/certs/**`、`go-agent/embedded/**`、`go-agent/internal/app/**`、`scripts/join-agent*.sh`，覆盖 agent 凭据落地与安装升级流程。
- `panel-frontend`：`panel/frontend/src/**` 与 `panel/backend-go/internal/controlplane/http/**`，覆盖 PKI HTTP API 与前端 consumer 现状。
- `e2e-and-ci`：`TESTING.md`、`.github/workflows/**`、`scripts/**`、`Dockerfile`、`docker-compose.yaml` 与既有 integration 测试入口，覆盖 e2e 夹具基础与 CI 契约。

## 当前实现事实

### 控制协议 PKI 契约（控制面已就绪，R2/R7 直接依赖）

- 注册：`POST /agents/register`（`http/router.go:305`），`RegisterRequest` 已含 `register_token`、`pki_enrollment_request_id`、`tunnel_csr_pem`、`pki_security_ack`（`service/agents.go:243-263`）；带 CSR 时走 `InternalPKIService.RegisterAgent`（`agents_pki.go:91-125`），一次性 token 消费与 AgentRow 同事务（`pki_enrollment.go:282`），响应 `PKIRegistrationReply{agent_id, agent_token, tunnel_credential, security_snapshot}`（`agents.go:131-135`）。
- 心跳/control sync：`HeartbeatRequest` 已含 `pki_security_ack`、`pki_enrollment_requests[]`（`agents.go:190-191,267-276`）；`HeartbeatReply` 已含 `pki_security`、`pki_credentials[]`、`pki_status{status,code,recovery_hint}`（`agents.go:211-226`）。degraded 时 `PKIStatus=degraded` 且清空 RelayListeners（`agents.go:1318-1322,1368-1371`）。
- 安全快照：`storage.PKISecuritySnapshot{pki_domain_id,pki_epoch,security_revision,full,trust_roots[],revoked_identity_ids[],revoked_serials[],signer_generation,signature}`（`storage/snapshot_types.go:52-63`），ECDSA 签名，持久化前 `ValidateCanonicalPKISecuritySnapshot` 验签（`storage/pki_repository.go:1232,1314`）；下发通道为 register 响应、heartbeat `pki_security`、revision 快照 `pki_security`（`revision_api.go:518`、`sqlite_store.go:359`）三处；ack 经注册路径与 heartbeat `recordSecurityAcknowledgement`（`agents_pki.go:250-292`），epoch/revision 超前被拒绝（ErrPKIEpochStale）。
- agent 侧消费**全部缺失**：`SyncRequest`/heartbeat payload 无 PKI 字段（`go-agent/internal/control/sync_client.go:51-61,87-109`），响应中 `pki_security`/`pki_credentials`/`pki_status` 被 JSON unmarshal 静默丢弃；`model.Snapshot`/`model.RelayListener` 无 PKI 字段（`model/types.go:59-70`、`model/relay.go:8-28`）；无 CSR 生成、tunnel key、快照验签/撤销消费代码。
- 鉴权/传输：HTTP header `x-agent-token`（`sync_client.go:133,297`），端点 `{MasterURL}/api/agents/heartbeat`、`/api/agent-revisions/pull|start|report`（`sync_client.go:127,201,276-280`），默认 http.Transport 无 mTLS（`http.go:46-58`）。
- 嵌入式 agent：进程内 `SyncSource/StateSink` 桥（`localagent/runtime.go:38-41,145-163`）**不搬运任何 PKI 字段**（`runtime.go:165-385`）；embedded 证书走 tokenless `EnrollLocal`（`pki_enrollment.go:171-181`），owner 固定 LocalAgentID。

### relay 数据面现状（R3 改造对象）

- 服务端：`relay/tls.go:14-35` `serverTLSConfig` 仅设 `Certificates` + `MinVersion=TLS1.2`，**无 ClientAuth/ClientCAs（NoClientCert）**；QUIC 复用后 Clone 强制 TLS1.3 + ALPN `nre-relay-quic/1`（`quic_runtime.go:484-505`）。
- 客户端：`tls.go:37-95` `InsecureSkipVerify=true` + 自定义 `VerifyConnection`，按 `listener.TLSMode` 分派 `pin_only|ca_only|pin_or_ca|pin_and_ca`（`validation.go:9-14,160-173`）；RootCAs 来自 `provider.TrustedCAPool(ctx, listener.TrustedCACertificateIDs)`；**`pki_mtls` 不在合法模式集合中，agent 收到会报 unsupported tls_mode**。
- 控制面 PKI relay 逻辑已就绪：fail-closed latch（`storage/pki_models.go:48`，置位 `pki_emergency_authority_runtime.go:86,184,697,752`，消费 `sqlite_store.go:283-295` 清空 relay 快照）；`pki_mtls` 投影（`relay.go:175-182,275-403`，FinalizeTunnelMTLSUpgrade 持久化并删除旧 relay_ca/relay_tunnel 材料）；emergency revision barrier 状态机（`relay.go:409-715`）。
- 撤销收敛：`PKIOnlineRevocationConvergence = 5s`（`pki_revocation.go:15`），`CompleteCommittedRevocation` 在 5s ctx 内并行 PublishPKISecuritySnapshot + CloseRevokedPKISessions（`pki_revocation.go:232-260`），失败 job 10s 重试重放（`relay_pki_repository.go:253-258`、`pki_revocation.go:265-312`）；关闭的是 agent 控制任务流（`tasks.go:317-358`），relay 数据面会话靠 revision 替换 + generation drain（`generation_runtime.go:782-793`）。
- QUIC→TLS/TCP fallback：`dial_runtime.go:143-179`，与 QUIC 共用同一份 listener 凭据（同一 clientTLSConfig/pin/CA 集），无凭据区分；两池同属 `relayPoolScope` 按 generationID 引用计数（`generation_pool.go:25-34`）。
- generation 切换：`generation_runtime.go:364-448` 每次 generation 重建 Server，稳定 ingress 经 `relayIngressManager` 复用 broker + SetSelector 路由（`:104-237`），发布 `relayGenerationTransaction.publish`（`:536-554`），旧 runtime drain（`:629-631`）；QUIC 会话池 key 含 `listener.Revision`（`session_pool.go:97-109`），TLS/TCP 池 key 含 PinSet/TrustedCA/CertificateID JSON（`tls_tcp_session_pool.go:491-513`）。

### agent 凭据落地与安装升级（R2 改造对象）

- cert runtime：`<dataDir>/certs/managed/<certID>/`（`certs/runtime.go:1449-1451`）；local internal_ca 为 RSA-2048 自签 10 年平铺写 `cert.pem/key.pem`（`runtime.go:1453-1485,377-407`），**无 generation 两阶段**；ACME 路径有完整两阶段 `stageACMEGeneration`/`promoteACMEGenerationLocked` + 双槽 current 指针（`runtime.go:873-916,999-1069,1246-1264`）。
- 原子替换原语已统一：`writeFileAtomically`（`runtime.go:1776-1812`）→ Unix `os.Rename`（`replace_file_default.go:7-9`）、Windows `MoveFileEx(REPLACE_EXISTING|WRITE_THROUGH)`（`replace_file_windows.go:16-20`）；材料文件 0600；崩溃恢复有 `Reconcile` + pending 恢复（`runtime.go:550-583,794`）。
- **agent 无任何 PKI domain/(epoch,revision)/trust snapshot 持久化结构，也无 downgrade 拒绝**；本地状态仅 `managed_state.json` + `local_metadata.json`。
- 嵌入式 agent：`embedded.New` Config 仅 `AgentID/AgentName/DataDir`，**无 AgentToken**（`embedded/runtime.go:53-68`）；持久桥 `persistentBridgeStore` 用 `<DataDir>/embedded-agent-state`（`runtime.go:285-328`）。
- join-agent.sh：注册 POST `$MASTER_URL/panel-api/agents/register`，header `X-Register-Token` + `X-Agent-Token`（`:416-433`）；`write_agent_env` 写 8 个 NRE_* 变量到 agent.env，**无 chmod/umask 处理**（`:402-414`）；重复 join 幂等复用旧 env（`:546-570`）；migrate-from-main 沿用旧 agent token 重新注册（`:902-999`），legacy 证书数据不迁移到 go-agent certs/managed 结构。

### PKI HTTP API（控制面已就绪）与前端 consumer（R4 改造对象）

- 路由 `http/router.go:319-334`（requirePanelToken，`/panel-api` 与 `/api` 双前缀）：GET `/pki/overview|authorities|identities|certificates|events|alerts`；POST `/pki/enrollment-tokens`（201 返回一次性 token，无 GET 列表）；POST `/pki/confirmations`（nonce）；POST `/pki/identities/{id}/revoke|force-rotate`、`/pki/authorities/rotate|emergency-rotate`、`/pki/backups/export|import`（passphrase+confirmation_nonce，import multipart ≤32MiB）、`/pki/activation`；GET `/pki/operations/{id}`（`handlers_pki.go:30-357`）。
- action 请求体 `PKIActionRequest{target_id,reason,confirmation_nonce,passphrase,archive,force}`（`relay_pki_api.go:93-100`）；action 响应 202 `{operation_id,status_url:"{prefix}/pki/operations/{id}"}`（`handlers_pki.go:312-326`）；epoch 冲突 409 `pki_security_version_conflict`（`router.go:547-569`）。
- 前端**无任何 PKI API consumer**；现有 operation 轮询链路 `fetchOperationStatus` 只接受 `^/(panel-api|api)/operations/…`（`api/operations.js:9-12,70-73`），**按现状无法消费 `/pki/operations/{id}`**。
- 可复用基建：`mutationEnvelope`/`recordAcceptedOperation`（`api/runtime.js:8-18`）、operations store + `useOperationStatus`（2s 轮询）+ `OperationStatusList`（CertsPage 已挂）、`DeleteConfirmDialog` 高风险确认（无 passphrase/nonce/输入名称确认模式）、useResourceListQuery/ResourceListFilterBar/ViewToggle 等列表基建。
- 备份 UI：ExportPanel/ImportWizard 走 `/system/backup/*`（FormData `file`），**无口令/加密概念**；PKI 受保护备份后端已就绪、前端零实现。
- 前端测试：vitest 4 + @vue/test-utils + jsdom，`npm test`；CertsPage/AgentsPage/SettingsPage 无页面级测试，hook 与 operations 组件有测试。

### 测试分层与 e2e 基础（R6 改造对象）

- 分层契约（TESTING.md）：fast `go test -short ./...` + `npm test`；full 去掉 `-short`；integration `-tags=integration -run '^TestIntegration'` 枚举包清单；禁 sleep、用 channel/fake clock。
- CI：`tests.yml` go-fast/frontend-fast（PR）+ go-full/frontend-full（schedule/dispatch，go-full 含 Pebble fixture + integration matrix）；`docker-build.yml` 镜像构建即回归（PR 只 build 不 push）。
- 可复用夹具：cutover harness（`cutover/runtime_harness.go:42-81`，in-process master + embedded agent + httptest，端口隔离重试）；真实子进程模式（`go-agent/internal/app/hotrestart_packet_integration_test.go:50-115`，进程组 + subreaper + 组级 SIGKILL）；PKI 服务层集成（`control_pki_integration_test.go:22-57`，TempDir + OpenPKIVault + bootstrapInternalPKI）；**可注入时钟已存在**（`agents_pki.go:48,63,72-73`、`certs_pki.go:46,63-64`）。
- `scripts/relay-perf/` 是现有唯一"多 agent 真实进程 + mock 控制面"docker-compose 夹具，可作 e2e 拓扑参考；`join-agent.test.sh`/`deploy-compose.test.sh` 为 POSIX sh 函数抽取 + mock-bin 单元式测试。
- Pebble 公网 ACME 仅挂 tests.yml go-full（`scripts/acme-integration/`，v2.10.1）。

## 可复用机制

| 机制 | 当前 owner | 当前 consumer | 可复用事实与边界 |
|---|---|---|---|
| 控制面 PKI 控制契约 | `agents_pki.go`/`pki_enrollment.go` | register/heartbeat/revision 三通道 | CSR 提交、证书下发、签名快照、ack 均已实现；agent 侧只需消费，不需新增端点。 |
| 签名安全快照 + fencing | storage/service PKI | 快照构建与验签 | ECDSA 签名、(epoch,security_revision) 字典序、revision=0 完整快照恢复；agent 侧需新增持久化与 downgrade 拒绝。 |
| ACME 两阶段 generation | go-agent certs runtime | ACME 证书 | stage/promote/rollback/双槽 current/原子替换原语可直接为 tunnel credential 复用；internal_ca 平铺路径无此结构。 |
| relay generation 切换 | go-agent relay generation_runtime | relay listener | publish/drain + ingress selector 平滑切换；mTLS verifier 可挂入同一 generation 重建路径。 |
| 五秒撤销收敛 | 控制面 pki_revocation | 控制任务流 | 快照发布 + session 关闭已有时限契约；relay 数据面会话收敛依赖 revision 替换 + drain。 |
| PKI HTTP API + operation envelope | 控制面 http 层 | 面板（待接入） | action/confirmation/operations 契约完整；前端需扩展 statusURL 白名单以消费 `/pki/operations`。 |
| 列表/确认/轮询前端基建 | panel/frontend | 现有页面 | mutationEnvelope、useOperationStatus、DeleteConfirmDialog、列表基建可复用；缺 passphrase/nonce 确认模式。 |
| cutover/子进程/relay-perf 夹具 | 两 Go module + scripts | 现有集成测试 | 进程内 harness、真实子进程清理、多进程 docker 拓扑三类模式齐备；可注入时钟已存在于 PKI 服务。 |

## Requirement 差距与风险

- **R2（agent-credentials）**：控制面契约完整但 agent 侧零基础——无 PKI 字段消费、无本地生钥/CSR、无快照持久化与 downgrade 拒绝、embedded 桥不搬运 PKI、join 脚本无一次性 token 流程且 agent.env 无权限收紧。工作量集中在 go-agent 新增模块而非修改控制面。
- **R3（relay-mtls）**：agent 收到控制面已可下发的 `pki_mtls` TLSMode 会直接报 unsupported；服务端无 ClientAuth、客户端无证书提供；pin_only 删除会影响现有 listener 配置兼容（需确认迁移路径由控制面投影承担）。
- **R4（panel-pki）**：后端 API 完整，前端全缺；现有 operation 轮询路径白名单不含 `/pki/operations`，需先扩展；缺 passphrase/nonce 高风险确认交互模式。
- **R5（upgrade-docs）**：部署材料当前无 PKI/mTLS 描述（旧探索已证）；docs-site 构建独立于主 CI。
- **R6（pki-e2e）**：三类夹具模式齐备但无现成"控制面+远程 agent+relay mTLS"拓扑；go-agent 侧 mTLS 消费点需 R2/R3 先落地，e2e 依赖顺序正确。
- **R7（约束继承）**：控制协议保持 token/HTTP 有既有反向测试基础；relay fail-closed 清空快照已影响 relay 数据形状，agent 侧兼容需验证。
- **风险**：`pki_production-runtime` 遗留 concern（未跑 `go test -race`）按 R10 接受现状，e2e 高并发场景不复查该缺口。

## 需要技术方案明确的 Unknowns

- agent 侧 PKI 模块边界：新增 `internal/modules/pki` 与 certs/control/relay 模块的依赖方向与安全快照存储格式。
- `pki_mtls` listener 投影到达 agent 后的凭据解析路径：tunnel credential 如何经 snapshot/generation 进入 relay provider（ServerCertificate/TrustedCAPool 的 PKI 替代实现）。
- 在线撤销五秒关闭在 agent 数据面的执行机制：snapshot 应用与既有 relay session 拆除的触发点。
- embedded agent 独立 tunnel credential store 与进程内桥的 PKI 字段搬运方式。
- join/upgrade 流程中一次性 register token 的获取入口（面板创建 → 安装命令拼接）与旧 agent token 保留策略。
- 前端 PKI 页信息架构：独立页面/分区与 `/pki/operations` 轮询接入方式、passphrase 单次驻留的交互设计。
- e2e 夹具形态：Go integration 多进程 vs docker-compose 拓扑的选择与时钟注入边界。

## 后续验证焦点

- 受影响快速层：`cd go-agent && go test -short ./...`、`cd panel/frontend && npm test`；回归层 `cd panel/backend-go && go test -short ./...`。
- 任务级 verify（沿旧计划）：`go-agent -run 'Test(Relay.*MTLS|TunnelIdentity|SecuritySnapshot|ControlProtocolUnchanged)'`、`sh scripts/join-agent.test.sh`、`sh scripts/test-internal-pki-e2e.sh`。
- 顶层 delivery（沿旧计划）：两模块 fast/full、frontend build、backend/agent integration、internal-pki-e2e、`docker compose --env-file .env.example config`、`docker build -t nginx-reverse-emby .`。
- 既有可复用入口：`cutover/cutover_integration_test.go`、`go-agent/internal/app/hotrestart_packet_integration_test.go`、`service/control_pki_integration_test.go`、`scripts/relay-perf/`。
