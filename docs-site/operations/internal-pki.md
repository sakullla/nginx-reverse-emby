# 内部 PKI 升级与运维

内部 PKI 是 Relay 隧道的独立安全域。它为远程 Agent、内嵌 `local` Agent 和 Relay listener 分配独立 tunnel identity，并用双向 TLS 验证 Relay TLS/TCP 与 QUIC 数据面。

::: warning 边界不能混用
内部 mTLS **不**认证 registration、heartbeat、revision 或 task 控制请求。这些请求继续走现有 panel/control listener，并使用 `X-Agent-Token`。公网 ACME、面板/API 用户、公开 HTTPS/TCP/UDP 客户端也不属于内部 PKI。
:::

镜像只暴露现有 8080 panel/control listener。不要为了内部 PKI 新增第二个控制端口，也不要给控制请求配置客户端证书。

## 部署 master key

默认情况下，控制面在 panel data 目录中管理 `pki/master.key`。如果 secret manager 提供 master key，`NRE_PKI_MASTER_KEY_FILE` 必须指向容器内受限绝对路径，并把宿主 secret 只读挂载到同一路径：

```yaml
environment:
  NRE_PKI_MASTER_KEY_FILE: /run/secrets/nre-pki-master.key
volumes:
  - ./data:/opt/nginx-reverse-emby/panel/data
  - ./secrets/nre-pki-master.key:/run/secrets/nre-pki-master.key:ro
```

宿主文件应仅运行用户可读。不要把 master key 放进镜像、环境变量值、日志、普通配置备份或 Git。设置该变量不会改变 `NRE_MASTER_URL`、控制监听地址或 token 认证。

## 从旧 Relay 认证升级

这是维护窗口操作。旧 pin-only、单向 TLS、自签名放行和 `pin_or_ca` 不是兼容回退；升级完成后的 internal Relay 只接受当前 PKI domain 签发、用途和身份匹配、未过期且未撤销的证书。

### 升级前检查

- [ ] 只运行一个预期的控制面实例；确认没有复制的数据库实例同时签发。
- [ ] 记录当前 Agent ID、规则和 listener 关联，确认远程 Agent 仍能用现有 control token 心跳。
- [ ] 为普通配置做独立备份；如果当前版本已有内部 PKI，再额外导出一份受保护 PKI 备份。
- [ ] 确认所有参与 Relay 的节点在线，并安排 Relay 数据面可能中断的维护窗口。
- [ ] 准备逐节点验证，不批量复用登记 token。

### 升级顺序

1. **先升级控制面。** 保持原有 panel/control URL 和 8080 listener，配置 PKI master key，启动后确认内部 PKI overview 为 healthy，并记录 PKI domain、epoch 和活动 CA generation。
2. **确认 embedded identity。** 内嵌 `local` Agent 由控制面进程内登记，使用独立 tunnel identity 和本地凭据目录；不要为 `local` 创建或传递 enrollment token。
3. **逐个登记远程 Agent。** 在 **证书管理 → 内部 PKI** 为现有 Agent ID 创建“绑定现有节点”的一次性 token。必须绑定原有稳定 Agent ID，不能用“新节点” token 生成替代身份。
4. **在原数据目录执行 re-enrollment。** 例如默认 Linux 安装：

   ```bash
   curl -fsSL https://panel.example.com/panel-api/public/join-agent.sh | sh -s -- \
     --master-url https://panel.example.com \
     --register-token '<bound-one-time-token>' \
     --data-dir /var/lib/nre-agent \
     --force-pki-reenroll \
     --install-systemd
   ```

   token 只使用一次，不要放进 shell history、日志或工单。使用原数据目录让脚本复用稳定 Agent ID；bound re-enrollment 会换 tunnel/control credential，但保留 Agent 及规则/listener 的稳定关联。
5. **核对每个节点。** 内部 PKI 页面应显示 identity active、当前 certificate/generation、正确 owner/purpose，Agent 应回报当前 PKI epoch/security revision 和完整 trust acknowledgement。
6. **重新签发 listener identity 并验证 Relay。** TLS/TCP 与 QUIC 都必须完成双向验证。缺证书、错误 CA/EKU/identity/domain、过期或撤销证书都应被拒绝。
7. **最后结束维护状态。** 只有所有必需节点和 listener 都已确认新 generation，且相关 revision applied、旧会话 drained 后，才执行内部 PKI 的 activation。此时删除旧 Pin Set、pin-only、单向 TLS 和自签名放行配置；不要保留隐藏 fallback。

离线或未完成节点会保持 `enrollment_required`，涉及它们的 Relay 路径不可用；这不应促使你恢复旧认证。control token 通道仍可用于节点重新上线后的 bound enrollment 和安全状态下发。

## 日常生命周期

- endpoint 证书由 Agent 本地生成私钥和 CSR，私钥不会进入控制面 snapshot 或普通备份。
- 日常续签和普通轮转由 lifecycle job 驱动；需要人工强制换证时，操作必须绑定明确 identity、reason 和服务端 confirmation nonce。
- revoke、endpoint force rotate、normal/emergency CA rotate 与 activation 都是高风险操作。提交前核对对象和 reason，并持续跟踪独立 PKI operation 的 `accepted/running/blocked/succeeded/failed` 状态。
- emergency CA rotation 会 fail closed。不要把旧 CA、Pin 或单向认证重新启用来绕过 blocked 状态。
- 只有重登记现有稳定 Agent 时才使用 `bound_reenrollment` token；每次生成新 token，并在关闭一次性显示窗口前安全保存。

## 受保护备份

在 **证书管理 → 内部 PKI → 受保护备份** 操作。它与 **设置 → 数据管理** 的普通 `.tar.gz` 配置备份不同：

- passphrase 只存在于本次请求内；控制面和浏览器不保存副本，丢失后无法恢复。
- archive 是带版本 manifest 的加密 envelope，保留 PKI domain、epoch、CA/trust、撤销、安全状态和稳定 Agent 关联。
- sanitized snapshot 会删除所有 enrollment token；manifest 中 token 行数必须为 0。恢复后需要登记时重新生成一次性 token。
- 错误 passphrase、篡改或不完整 archive 必须失败，并且不得改变当前 active state。

普通未加密配置 tar、数据库 dump 或直接打包 data 目录都不能替代内部 PKI 受保护备份：它们不能单独证明 vault/master key、PKI epoch、撤销和 Agent identity 连续。

## 计划迁移

1. 安排维护窗口，等待正在运行的 PKI operation 完成，确认所有可达 Agent 已同步当前 epoch/security revision。
2. 在源实例导出受保护 PKI 备份，并记录 manifest 中的 PKI domain/epoch。普通配置另行备份。
3. 停止并隔离源实例，确保它不能继续取得 lease、签发或发布安全状态。不要让复制的 source/target 同时运行。
4. 在目标实例挂载预期的 data/secret 路径，导入受保护备份并完成计划迁移 activation；核对 PKI domain 保持不变，epoch 不回退。
5. 在每台 Agent 更新 `NRE_MASTER_URL` 指向新控制面。地址变化不应重建 Agent ID、tunnel identity 或证书；既有 control token 和关联保持连续。
6. 验证 Agent 接受目标实例发布的当前 epoch、安全 snapshot 和 CA generation，再恢复 Relay 流量。

## 灾难恢复与 force activation

只有旧实例确实不可用时才使用 force activation：

1. 先从网络、调度器和存储层永久隔离旧实例。
2. 在新实例导入最后一份已验证的受保护备份，输入单次 passphrase，并通过明确 reason/高风险确认执行 force activation。
3. 新实例必须发布更高 PKI epoch 的完整安全 snapshot；Agent 会拒绝旧 epoch，即使旧 snapshot 的 security revision 数字更大。
4. 更新 Agent 的 `NRE_MASTER_URL` 并逐个确认收敛。不要重新运行被隔离的旧实例。

instance lease 提供共享 canonical state 上的协作式单活，不是分布式共识。两个完全隔离、各持一份数据库副本的实例仍可能各自运行；管理员必须保证旧实例隔离。PKI epoch 让 Agent 拒绝 stale 控制面安全状态，但不能代替外部单活编排。

离线 Agent 在断开期间只能使用最后一次已验证的安全 snapshot；它不会实时得知新撤销。节点重新连接后会优先同步当前 epoch/revocation 状态并关闭失效会话，因此撤销对离线节点存在直到重连的延迟。安全敏感场景应同时在网络层隔离该节点。

