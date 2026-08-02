# Technical Solution

```yaml
format: solution
summary: "控制面内部 PKI 契约已全部就绪；本方案只在消费侧落地：go-agent 新增 PKI 模块消费既有 register/heartbeat/revision 三通道完成本地生钥/CSR/证书接收/签名安全快照持久化，relay 数据面以 pki_mtls 模式实现严格双向校验并删除 pin-only/单向 TLS，前端接入既有 /pki/* API 与 operation 轮询，最后以多进程 e2e 夹具机械证明正常/攻击/恢复/迁移矩阵。"
```

## 目标与边界

本方案覆盖新需求的剩余实现面，前提是旧工作流已交付的控制面内部 PKI（canonical storage、一次性登记、CA vault/lease、生命周期状态机、控制契约、production runtime）是既有事实，不重做、不复审（R9）。目标：

- go-agent 作为隧道端点身份及私钥唯一 owner：本地生成 ECDSA P-256 tunnel key 与 CSR，经现有注册/心跳通道完成一次性登记与续签，私钥不离开所属 agent data dir（R2）。
- agent 原子持久化签名安全快照，按 `(pki_epoch, security_revision)` 字典序拒绝 downgrade；relay TLS/TCP 与 QUIC 数据面强制严格 mTLS，transport 可回退、认证强度不可回退（R2、R3）。
- 面板提供与公网证书分域的内部 PKI 生命周期、告警、审计、处置、登记 token 与受保护备份界面（R4）。
- 部署/升级/备份迁移文档固化 tunnel-only mTLS 运维契约（R5）；多进程 e2e 与 CI 机械证明正常、攻击、恢复、迁移矩阵（R6）。

明确不改变（R7）：`PANEL_BACKEND_HOST`/`PANEL_BACKEND_PORT` 监听、`NRE_MASTER_URL`、registration/heartbeat/revision/task URL 与 `X-Agent-Token` 控制认证保持现状，不新增监听地址/端口/服务端 TLS 入口；公网 ACME、面板/API 用户与公开业务客户端不进入内部 PKI。控制面侧实现不在本期范围（R9），不补 `go test -race`（R10），不修复旧工作流 state（R11）。

## Requirement anchor coverage

| Anchor | 方案落点 |
|---|---|
| R1 | 以 HEAD `1ca08656` 为基线；控制面 PKI 契约作为既有输入，01 证据为现状基线。 |
| R2 | go-agent `internal/modules/pki` + control sync 扩展 + embedded 桥 PKI 搬运 + join/upgrade 一次性 token 流程。 |
| R3 | relay `pki_mtls` 模式：server RequireAndVerifyClientCert、client 持证书、统一严格 verifier、快照驱动会话收敛。 |
| R4 | 前端 PKI 分区页面、hooks/api consumer、`/pki/operations` 轮询接入、nonce/passphrase 高风险确认。 |
| R5 | README/.env.example/compose/Dockerfile/docs-site 的 tunnel-only mTLS 契约文档。 |
| R6 | `scripts/test-internal-pki-e2e.sh` + `tests/internal-pki/**` 多进程夹具 + CI 分层。 |
| R7 | 控制协议反向测试证明不要求客户端证书；公网 ACME 路径零改动。 |
| R8 | 03 `delivery_verification` 唯一固化全量命令。 |
| R9–R11 | non-goals；验收确认不混入交付。 |

## 现状约束与复用基线

- 控制面三通道契约已就绪：register 请求/响应携带 `tunnel_csr_pem`/`tunnel_credential`/`security_snapshot`；heartbeat 携带 `pki_enrollment_requests[]`、`pki_security_ack` 与 `pki_security`/`pki_credentials[]`/`pki_status`；revision 快照含 `pki_security`（01 `agent-control-sync`）。agent 侧这些字段当前被 JSON unmarshal 静默丢弃——本方案的消费点明确。
- go-agent certs runtime 已有可复用原语：两阶段 stage/promote、`writeFileAtomically`（Unix rename / Windows MoveFileEx WRITE_THROUGH）、0600 材料权限、启动 Reconcile 恢复（01 `agent-certs-and-join`）。tunnel credential store 复用同一目录/原子语义，但独立于 ACME generation。
- relay generation 切换已有 publish/drain + ingress selector 平滑切换（01 `relay-runtime`）；mTLS verifier 挂入同一 generation 重建路径，凭据变化经 revision 反映到会话池 key。
- 控制面 relay 侧已可下发 `pki_mtls` 投影、fail-closed latch 清空 relay 快照、五秒撤销收敛与 emergency revision barrier；agent 当前收到 `pki_mtls` 会报 unsupported tls_mode（`relay/validation.go:160-173`）。
- 前端已有 mutationEnvelope、operations store、useOperationStatus、DeleteConfirmDialog 与列表基建；`/pki/*` API 全部就绪但零 consumer，且 `api/operations.js:9-12` 的 statusURL 白名单不含 `/pki/operations`。
- e2e 可复用：cutover in-process harness、hotrestart 真实子进程清理模式、relay-perf 多进程 docker 拓扑、PKI 服务可注入时钟。

## 目标架构

```text
Control plane (已实现，不重做)
  register resp / heartbeat resp / revision snapshot
    -> tunnel_credential + signed PKISecuritySnapshot + pki_status

go-agent
  internal/modules/pki           <- 新模块：credential store + security store + enrollment client
    identities/<id>/generations/<gen>/ + active.json   (本地生钥，0600，原子切换)
    security/<domain>/snapshot.json                    (epoch/revision fencing，拒绝 downgrade)
  internal/control               <- 扩展：发送 CSR/ack，解码 pki_security/credentials/status
  internal/modules/relay         <- pki_mtls：双向严格 verifier，快照驱动
  embedded                       <- 桥搬运 PKI 字段，独立 credential store

panel/frontend
  PKI 分区页面 + usePki hooks + /pki/operations 轮询 + nonce/passphrase 确认

tests/internal-pki + scripts/test-internal-pki-e2e.sh
  控制面 + 远程 agent + embedded agent + relay listener 多进程矩阵
```

## go-agent PKI 模块与登记续签（R2）

### 模块边界与 owner

- 新增 `go-agent/internal/modules/pki` 是 agent 侧 tunnel credential、安全快照与登记状态的唯一 owner。`internal/control` 只负责传输编解码；`internal/modules/relay` 只消费 pki 模块暴露的只读 provider 接口（server certificate、client certificate、trusted roots、revocation 判定）；certs 模块与公网证书路径不引用 pki 模块。
- `go-agent/internal/model` 扩展：`Snapshot` 增加 `pki_security`，`RelayListener` 增加 PKI identity/certificate 引用与 `pki_mtls` TLSMode 合法值；心跳请求模型增加 `pki_enrollment_requests[]` 与 `pki_security_ack`，响应模型增加 `pki_security`/`pki_credentials`/`pki_status`。字段名与控制面契约逐字对齐。

### 凭据 generation 存储

- 目录：`<dataDir>/pki/identities/<identity-id>/generations/<generation>/`（key.pem/cert.pem/chain.pem，0600，目录 0700）+ 单一 `active.json` 指针。新 generation 先完整写入、fsync、解析验证（公钥匹配、有效期、EKU、URI SAN），再原子切换 `active.json`——复用 `writeFileAtomically` 同款原语：Unix 同目录 rename，Windows `MoveFileExW(MOVEFILE_REPLACE_EXISTING|MOVEFILE_WRITE_THROUGH)`；崩溃只留旧或新完整状态，启动 Reconcile 清理未完成候选。
- agent tunnel client 证书用途 `ClientAuth`，URI SAN `spiffe://<pki-domain-id>/agent/<agent-id>`；listener 证书 `ServerAuth` + endpoint DNS/IP SAN + URI SAN `.../listener/<listener-id>`。CSR 在 agent 本地生成，控制面只收到 CSR；任何路径不出现私钥传输（结构/日志泄漏测试证明）。

### 安全快照持久化与 fencing

- `<dataDir>/pki/security/<domain-id>/snapshot.json` 原子持久化签名快照（trust roots、revoked identity/serial、signer generation、signature、full 标记）与版本元组 `(pki_epoch, security_revision)`。
- 接受规则：同 epoch 内 revision 单调不降；更高 epoch 无条件胜出且首条必须是 full 快照，应用后才接受该 epoch 增量；更低 epoch 拒绝。应用前以当前 trust roots 验签；写入与生效是同一原子步骤。
- snapshot age 不作为 relay 握手门槛：与控制面失联时继续使用最后可信快照；控制恢复时先应用较新安全快照（并触发撤销会话收敛），再应用普通 revision。

### 登记、续签与重登记流程

- 首次登记：join/upgrade 获得一次性 register token → pki 模块本地生 key/CSR → 现有注册 URL 提交 `register_token`+`tunnel_csr_pem` → 响应取 `agent_id`、`agent_token`、`tunnel_credential`、`security_snapshot` → 验证公钥匹配后写候选 generation 并原子激活 → 持久化安全快照 → 后续心跳带 `pki_security_ack`。
- 续签/force-rotate：心跳响应 `pki_credentials` 或 due time（not_after − lifetime/3 + 稳定错峰，与控制面策略一致）触发本地换 key/CSR，经 heartbeat `pki_enrollment_requests[]` 提交；收新证后先让新连接用新证书、旧连接排空，再 ack；失败保留仍有效旧 generation 并按退避重试，过期后失败关闭。
- `pki_status=degraded` 时保留普通控制载荷、清空 relay 配置（与控制面语义对齐）并把本地状态标为 degraded 供诊断。
- 重登记（revoke 恢复/紧急 CA 后）：使用 bound 一次性 token 走同一登记路径，稳定 agent ID 与产品关联保留，旧 serial 永久 revoked。

### 嵌入式 agent

- embedded 保留进程内 `SyncSource/StateSink` 控制 bridge 与无 AgentToken 边界；桥（控制面 localagent runtime ↔ embedded）新增搬运 `pki_security`/`pki_credentials`/ack 字段。embedded 的 tunnel credential store 落在自身 `<DataDir>/pki/` 下，与远程 agent 同代码同语义；登记由控制面 `EnrollLocal` tokenless 完成，agent 侧只是接收与激活。

### join/upgrade 脚本

- join-agent.sh 增加：接受管理员在面板创建的一次性 register token（新参数或 env），注册 payload 携带；`agent.env` 显式 chmod 0600（修复现状无权限处理）；`NRE_AGENT_TOKEN` 保留为控制凭据。upgrade/migrate-from-main 路径保留 ID/name/tags/关联，走 bound token 重登记取得 tunnel credential。

## relay 数据面双向 mTLS（R3）

### pki_mtls 模式

- `validation.go` 合法 TLSMode 集合新增 `pki_mtls`；控制面投影下发后 agent 不再报 unsupported。`pin_only` 在内部 relay 删除：升级完成后（控制面 `FinalizeTunnelMTLSUpgrade` 已投影 pki_mtls 并删除旧 relay_ca/relay_tunnel 材料）pin-only/单向 TLS/无客户端证书路径不再可用，不留隐藏兼容开关；公网/非 PKI listener 的既有模式语义不变。
- 服务端（TLS/TCP 与 QUIC 共用构造，`tls.go:14-35` 扩展）：`ClientAuth=RequireAndVerifyClientCert`，`ClientCAs` 来自 pki 模块当前可信 roots，`MinVersion=TLS1.3`，证书来自 pki provider 的 listener generation。
- 客户端：`InsecureSkipVerify` + 手工 pin 的旧路径对 pki_mtls 整体弃用，改为标准链校验 + `GetClientCertificate` 提供 agent tunnel client 证书；TLS1.3 + ALPN 保持 QUIC 现状。

### 统一严格 verifier

- 双向都校验：链（当前 trust roots，含 old+new 并信期多根）、时间、EKU（client 方向 ClientAuth、server 方向 ServerAuth）、server 方向 DNS/IP SAN、URI SAN 中 PKI domain/agent/listener identity 精确匹配、证书 serial/identity 不在本地最后可信撤销快照中、CA generation 属于可信集合。
- QUIC→TLS/TCP fallback 只换 transport：同一 verifier、同一 credential generation，不允许认证强度回退。
- 会话收敛：安全快照应用（撤销/CA 更新）后，relay runtime 按撤销 identity/serial 关闭匹配既有会话，配合控制面五秒收敛目标；fail-closed 快照（relay 清空）经既有 revision 替换 + generation drain 生效。会话池 key 增加 credential generation 维度，凭据切换即隔离旧会话。

## 面板（R4）

### 信息架构与 API consumer

- 证书区分为“公网证书”（现有 `/certs` 语义不变）与“内部 PKI”两个明确分区；新增 PKI 页面/组件（`pages/Pki*.vue`、`components/pki/**`、`hooks/usePki*.js`），router 注册新路由。
- 新 api consumer 对齐既有后端：GET overview/authorities/identities/certificates/events/alerts；POST enrollment-tokens（返回后一次展示）、confirmations（nonce）、revoke、force-rotate、authorities rotate/emergency-rotate、backups export/import（passphrase+nonce，import multipart `archive`）、activation；GET `/pki/operations/{id}`。
- `api/operations.js` statusURL 白名单扩展接受 `^/(panel-api|api)/pki/operations/`，复用 operations store + useOperationStatus 轮询链路，PKI action 的 202 envelope 进入同一 OperationStatusList。

### 交互与边界

- 内部页展示 identity、owner、purpose/EKU、chain/generation、serial/fingerprint、有效期、next action、rotation phase、revocation state、last error；alerts 列表展示 warning/critical 与可行动原因；audit 查询支持 type/identity/serial/CA generation/operator/result/时间范围。
- 高风险确认新模式：revoke/force-rotate 需对象确认；emergency rotate/force activate/受保护备份需 reason + confirmation nonce（先 POST /pki/confirmations 取 nonce）；passphrase 只驻留单次请求内存，不写 store/localStorage/日志。
- 现有备份 UI 删除“未加密”旧暗示，明确口令丢失不可恢复；不再宣称备份未加密或持久化口令。
- 页面行为测试覆盖 operation polling、错误恢复、高风险确认、公网/内部分域与 token 一次展示。

## 文档（R5）

- README/.env.example/docker-compose.yaml/Dockerfile 注释与 docs-site：部署只暴露现有 panel/control listener；mTLS 仅覆盖 relay TLS/TCP/QUIC；升级手册说明维护窗口、bound enrollment、稳定 agent 关联、embedded tunnel identity 与旧 relay 认证删除点；备份/迁移/force activation 说明口令、PKI epoch、`NRE_MASTER_URL` 更新、单活边界与离线撤销延迟；公网 ACME/面板用户/公开客户端不被描述为内部 mTLS consumer。

## 端到端验证（R6）

- 新增 `scripts/test-internal-pki-e2e.sh` + `tests/internal-pki/**`：隔离临时目录构建并启动一个控制面、一个远程 agent、一个本地嵌入式 agent 与内部 relay listener；进程管理复用 hotrestart 子进程模式（进程组 + 清理契约），拓扑可参考 relay-perf harness；时钟经控制面既有注入点缩短 lifetime，不等待真实 30 天。
- 矩阵：正常（一次性登记、token 并发单消费、三分之一换钥、listener 原子切换、CA old+new 分发/换证/retire、relay 流量无感、控制协议保持 token）；攻击（无证书/非受信/过期/撤销/错误 EKU/错误 agent/listener/PKI domain/快照 downgrade/无效签名，均拒绝；控制请求不被要求客户端证书）；处置恢复（在线五秒撤销、隔离 peer 快照可用与重连收敛、force/emergency rotate、crash 后只留完整 generation、跨平台 active pointer）；迁移（错误口令/篡改备份不改状态、sanitized 无 token、跨目录/地址恢复保持关联与 epoch fencing）。
- CI：`tests.yml` 快速层保持现状，integration 层加入 internal-pki e2e 入口；保留 Pebble 公网 ACME 与容器构建回归。

## 失败、恢复与并发语义

- agent 写路径一律先 staging 后原子发布：候选 generation 验证失败删除候选、保留旧 active；安全快照验签/版本检查失败不落地；active 指针、磁盘材料与内存 provider 三者一致才向控制面 ack。
- ack 丢失可用 certificate/public-key fingerprint 重放，不重新生钥或重复签发；控制面已实现的 token 单消费/幂等语义不被 agent 重试破坏（重试携带同一 request_id）。
- 进程重启先 Reconcile：DB 无关，agent 侧核对 active.json、generation 材料完整性与安全快照版本；不一致时 relay PKI provider fail closed，控制协议继续可用并上报 degraded。
- 时钟倒退不延长已过期证书、不降低已持久化 `(epoch, revision)`。

## 机械验收与负向矩阵

精确命令在 03 `delivery_verification` 唯一固化；实现完成后必须可观察：

- `cd go-agent && go test -short ./...`（含 pki/relay/control/embedded 新测试）与 `sh scripts/join-agent.test.sh` 通过；`cd panel/frontend && npm test`（含 PKI 页面行为）通过；`cd panel/backend-go && go test -short ./...` 无回归。
- `sh scripts/test-internal-pki-e2e.sh` 一次跑通正常/攻击/处置/迁移矩阵。
- 负向：relay mTLS 八类攻击连接全部拒绝且产生结构化事件；lower epoch、同 epoch lower revision、非 full 新 epoch 首条、无效签名快照全部拒绝；`(higher epoch, revision 0)` full 快照可恢复。
- 泄漏检查：agent 日志/API/snapshot/文件列表无 PEM 私钥与原始 register token；凭据目录 0700/文件 0600。
- 反向：heartbeat/revision/task 控制请求不要求客户端证书；公网 ACME 测试无回归。

## 风险与明确接受边界

- **agent 侧新增面大**：pki 模块、control 扩展、relay verifier、embedded 桥、脚本同时落地；以 pki 模块单一 owner + provider 只读接口限制跨模块共享状态。
- **pki_mtls 切换兼容**：依赖控制面投影与升级状态机；agent 不接受手工配置 pki_mtls 之外的内部 relay 降级模式。
- **隔离期间撤销延迟**：与控制面失联的 peer 使用最后可信快照，撤销在控制恢复后优先收敛；在线节点满足五秒目标，不宣称无通信全局即时撤销。
- **e2e 环境敏感**：多进程夹具需端口隔离与确定性清理；时钟注入只走控制面既有注入点，不改生产语义。
- **已接受现状**：控制面未跑 `go test -race`（R10）、`pki-production-runtime` 未补审查收口（R9），本期不再复查。

## 已收敛的 Unknowns

- agent 侧模块边界、安全快照存储格式、credential generation 目录与原子切换原语、`pki_mtls` verifier 挂载点、embedded 桥搬运方式、join token 入口、`/pki/operations` 轮询接入、e2e 夹具形态均已给出单一选择。
- 实现期以实际 package API 校准字段/函数命名；不得改变私钥不传输、控制协议非 mTLS、fencing 规则、删除边界或机械验收结果。仓库约束迫使改变这些语义时必须回到方案 Gate。
