# Execution Plan

<!-- Duplicate task/command entries as needed; use `inspect contract` for every constraint. -->

```yaml
format: execution_plan
tasks:
  - id: agent-credentials
    goal: "实现 agent 侧 PKI 模块：本地 tunnel key/CSR、generation 原子激活、签名安全快照持久化与 fencing、control sync 消费、嵌入式桥 PKI 搬运和 join/upgrade 一次性 token 流程。"
    depends_on: []
    covers: [R1, R2, R9, R11]
    scope:
      - "go-agent/internal/model/pki*.go"
      - "go-agent/internal/model/types.go"
      - "go-agent/internal/model/relay.go"
      - "go-agent/internal/modules/pki/**"
      - "go-agent/internal/control/**"
      - "go-agent/embedded/**"
      - "go-agent/go.mod"
      - "go-agent/go.sum"
      - "panel/backend-go/internal/controlplane/localagent/**"
      - "scripts/join-agent.sh"
      - "scripts/join-agent.test.sh"
    outcomes:
      - "remote/embedded agent 的 tunnel 私钥只在所属 data dir 生成并以 0600 保存，控制面只收到 CSR；任何 API/日志/snapshot/文件路径不出现私钥或原始 register token。"
      - "immutable generation 经完整验证后以 Unix rename 或 Windows MoveFileExW(REPLACE_EXISTING|WRITE_THROUGH) 原子切换 active.json 指针，崩溃只留下旧或新完整状态，启动 Reconcile 清理未完成候选。"
      - "agent 原子持久化 PKI domain、(epoch, security_revision)、trust/revocation 快照并拒绝 downgrade；更高 epoch 首条必须为 full 快照，(higher epoch, revision 0) 可恢复。"
      - "心跳/注册消费 pki_enrollment_requests、pki_security_ack、pki_security、pki_credentials、pki_status；degraded 时保留普通控制载荷并清空 relay 配置。"
      - "嵌入式桥搬运 PKI 字段且 embedded 使用独立 tunnel credential store；join/upgrade 使用一次性 register token 取得 tunnel credential，NRE_AGENT_TOKEN 保留为控制凭据，agent.env 显式 0600。"
    verify:
      - "cd go-agent && go test -short ./internal/modules/pki ./internal/control ./embedded"
      - "sh scripts/join-agent.test.sh"
    test: new

  - id: relay-mtls
    goal: "将 relay TLS/TCP 与 QUIC 数据面改为 pki_mtls 严格双向 mTLS，统一 verifier 覆盖链/时间/EKU/URI 身份/撤销/CA generation，transport fallback 不降认证强度。"
    depends_on: [agent-credentials]
    covers: [R3, R7]
    scope:
      - "go-agent/internal/modules/relay/**"
      - "go-agent/internal/app/**"
    outcomes:
      - "pki_mtls 进入合法 TLSMode 集合；服务端 RequireAndVerifyClientCert + 可信 roots，客户端提供 tunnel client 证书；TLS 最低 1.3。"
      - "无证书、非受信、过期、撤销、错误 EKU、错误 agent/listener/PKI domain 全部拒绝；pin-only、单向 TLS 和非认证 fallback 从内部 relay 删除。"
      - "QUIC 到 TLS/TCP fallback 只改变 transport，复用同一 verifier 与 credential generation；会话池 key 含 credential generation 维度。"
      - "安全快照应用后按撤销 identity/serial 关闭匹配既有会话；控制面不可用时 relay 使用最后可信快照，重连后安全快照优先于普通 revision。"
      - "heartbeat/revision/task 控制请求继续使用原 transport/token，反向测试证明不要求客户端证书。"
    verify:
      - "cd go-agent && go test -short ./internal/modules/relay ./internal/app -run 'Test(Relay.*MTLS|TunnelIdentity|SecuritySnapshot|ControlProtocolUnchanged)'"
    test: extend

  - id: panel-pki
    goal: "提供与公网证书分域的内部 PKI 生命周期、告警、审计、处置、登记 token 和受保护备份界面，接入既有 /pki/* API 与 operation 轮询。"
    depends_on: []
    covers: [R4]
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
      - "panel/frontend/src/api/operations.js"
      - "panel/frontend/src/router/**"
    outcomes:
      - "公网证书与内部 PKI 明确分区；内部页展示 identity、owner、purpose、chain/generation、serial/fingerprint、有效期、next action、rotation phase 和错误。"
      - "管理员可一次查看 register token，并通过 nonce 确认执行 revoke、force endpoint rotate、normal/emergency CA rotate、受保护备份导出导入与迁移 activation。"
      - "operation 轮询白名单扩展消费 /pki/operations/{id}，PKI action 的 202 envelope 进入既有 OperationStatusList。"
      - "derived warning/critical 与 audit 查询覆盖签发、续签、轮转、撤销、拒绝、备份/恢复的对象、来源、结果与原因；passphrase 只驻留单次请求内，UI 不再宣称备份未加密或持久化口令。"
      - "页面行为测试覆盖 operation polling、错误恢复、高风险确认与内部/公网边界。"
    verify:
      - "cd panel/frontend && npm test"
    test: extend

  - id: upgrade-docs
    goal: "固化 tunnel-only mTLS 的部署、升级、备份迁移与安全运维契约，不引入第二控制入口。"
    depends_on: [relay-mtls, panel-pki]
    covers: [R5]
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
    goal: "建立多进程内部 PKI/relay mTLS 端到端夹具和 CI 契约，机械证明正常、攻击、处置、恢复与迁移行为。"
    depends_on: [relay-mtls, panel-pki, upgrade-docs]
    covers: [R6, R8, R10]
    scope:
      - "scripts/test-internal-pki-e2e.sh"
      - "tests/internal-pki/**"
      - ".github/workflows/tests.yml"
      - "TESTING.md"
    outcomes:
      - "隔离夹具启动控制面、远程 agent、embedded agent 和 relay listener，并以注入时钟验证三分之一续签、换钥和 CA 最长 30 天换代。"
      - "攻击矩阵覆盖无证书、非受信、过期、撤销、错误 EKU/agent/listener/PKI domain 及安全快照 downgrade 与无效签名，且 control protocol 保持 token。"
      - "处置/恢复矩阵覆盖在线五秒撤销、离线快照可用与重连收敛、force/emergency rotate、crash recovery 和跨平台 active pointer。"
      - "加密备份篡改/错误口令失败不改变状态，sanitized snapshot 无 enrollment token；跨目录/地址恢复保持 PKI/agent/关联并验证 epoch fencing 与单活。"
      - "CI 区分快速与 integration 层，并保留公网 Pebble ACME 与容器构建回归；交付命令不含 go test -race。"
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
