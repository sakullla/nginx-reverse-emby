# 内部 PKI 升级与运维

内部 PKI 是 Relay 隧道的独立安全域。它为远程 Agent、内嵌 `local` Agent 和 Relay listener 分配独立 tunnel identity，并用双向 TLS 验证 Relay TLS/TCP 与 QUIC 数据面。

::: warning 边界不能混用
内部 mTLS **不**认证 registration、heartbeat、revision 或 task 控制请求。这些请求继续走现有 panel/control listener，并使用 `X-Agent-Token`。公网 ACME、面板/API 用户、公开 HTTPS/TCP/UDP 客户端也不属于内部 PKI。
:::

镜像只暴露现有 8080 panel/control listener。不要为了内部 PKI 新增第二个控制端口，也不要给控制请求配置客户端证书。

## 部署 master key

默认情况下，控制面在 panel data 目录中管理 `pki/master.key`。如果 key 需要独立持久化，`NRE_PKI_MASTER_KEY_FILE` 必须指向容器内受限绝对路径，并挂载它的私有父目录：

```yaml
environment:
  NRE_PKI_MASTER_KEY_FILE: /run/nre-pki/master.key
volumes:
  - ./data:/opt/nginx-reverse-emby/panel/data
  - ./secrets/nre-pki:/run/nre-pki
```

创建宿主目录后设置 `0700`，已有 `master.key` 设置 `0600`。目录必须对容器进程可写：protected restore 会在父目录生成新 key，并在激活时原子替换配置文件，所以只读单文件 bind mount 和不可变 secret projection 会令恢复失败。恢复完成后，新 key 才是 canonical key；不要让外部同步器覆盖回旧值。不要把 master key 放进镜像、环境变量值、日志、普通配置备份或 Git。设置该变量不会改变 `NRE_MASTER_URL`、控制监听地址或 token 认证。

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
7. **最后结束维护状态。** 只有所有必需节点和 listener 都已确认新 generation，且相关 revision applied、旧会话 drained 后，才在内部 PKI 页执行“迁移激活”。页面会先取得绑定 `activate/domain` 的服务端一次性 confirmation nonce，再提交 reason；此时删除旧 Pin Set、pin-only、单向 TLS 和自签名放行配置，不保留隐藏 fallback。

离线或未完成节点会保持 `enrollment_required`，涉及它们的 Relay 路径不可用；这不应促使你恢复旧认证。control token 通道仍可用于节点重新上线后的 bound enrollment 和安全状态下发。

## 日常生命周期

- endpoint 证书由 Agent 本地生成私钥和 CSR，私钥不会进入控制面 snapshot 或普通备份。
- 日常续签和普通轮转由 lifecycle job 驱动；需要人工强制换证时，操作必须绑定明确 identity、reason 和服务端 confirmation nonce。
- revoke、endpoint force rotate、normal/emergency CA rotate 与 activation 都是高风险操作。提交前核对对象和 reason，并持续跟踪独立 PKI operation 的 `accepted/running/blocked/succeeded/failed` 状态。
- emergency CA rotation 会 fail closed。不要把旧 CA、Pin 或单向认证重新启用来绕过 blocked 状态。
- 只有重登记现有稳定 Agent 时才使用 `bound_reenrollment` token；每次生成新 token，并在关闭一次性显示窗口前安全保存。

## 受保护备份

在 **证书管理 → 内部 PKI → 受保护备份** 操作；应用侧栏不设置平行的 PKI 菜单。该入口当前**只支持 file-backed SQLite**，PostgreSQL/MySQL 会返回不支持，必须改用[数据库、vault 与 master key 的协同冷恢复](./backup-restore.md#postgresql-mysql-协同冷恢复)。它与 **设置 → 数据管理** 的普通 `.tar.gz` 配置备份不同：

- passphrase 只存在于本次请求内；控制面和浏览器不保存副本，丢失后无法恢复。
- archive 是带版本 manifest 的加密 envelope，载荷为**清除 enrollment token 后的完整 SQLite 数据库快照和可恢复 CA 私钥**，不是 PKI-only 行导出。它同时保留导出时的 Agent、规则、证书及其他面板数据库状态。
- manifest 校验完整 SQLite schema fingerprint。导出时必须在 archive 旁记录控制面 release 和镜像 digest；恢复前先部署完全相同的 release/schema，不能先用新版本迁移目标 schema。
- sanitized snapshot 会删除所有 enrollment token；manifest 中 token 行数必须为 0。恢复后需要登记时重新生成一次性 token。
- 错误 passphrase、篡改或不完整 archive 必须失败，并且不得改变当前 active state。

普通未加密配置 tar、单独的数据库 dump 或直接打包 data 目录都不能替代 SQLite 受保护备份：它们不能单独证明 vault/master key、PKI epoch、撤销和 Agent identity 连续。PostgreSQL/MySQL 的例外是停机后在同一恢复点协同保存数据库、PKI vault/data root 和 master-key 私有目录；缺少其中任一项都不能恢复。

### SQLite 恢复前的回滚点

Protected restore 会在一次激活中替换完整 SQLite 数据库、`dataRoot/pki` 目录，并在配置了外部 key 时替换 `NRE_PKI_MASTER_KEY_FILE`。导入前按以下顺序建立人工回滚点：

1. 停止并从网络、调度器隔离目标的所有控制面实例，确认没有数据库 writer、PKI lease owner 或 vault 写入。
2. 在这个停机时点同时保存目标 SQLite 主数据库及仍存在的 `-wal`/`-shm` sidecar、整个 `dataRoot/pki`（包括默认 `master.key` 与 `vault/`）以及外置 master-key 文件所在的私有目录。记录原路径和权限，加密保存回滚包。
3. 只重启一个目标实例，再执行普通或 force 导入。不要在建立回滚点后先升级 schema、导入普通配置或启动第二个实例。

如果成功导入后需要人工回滚，必须先确认初始响应的 `cleanup_pending=false`，或已按下文完成启动恢复清理；不要在 cleanup 未决时移动 journal 管理的文件。随后再次停止并隔离目标，把旧 SQLite 数据库、旧 `dataRoot/pki` 和旧外置 master key **整组恢复到同一时点**，恢复目录 `0700`、key/vault 文件 `0600` 权限，最后只启动一个实例并核对 PKI domain、版本和 CA key 可解密。只恢复旧数据库会让旧 canonical rows 配上新 vault/key，只恢复旧 key/vault 也会产生反向错配；两种做法都会令 PKI degraded。若 operation 自身返回 `failed`/`blocked`，先保留现场并检查恢复 journal/错误，不要把不同时间点的材料手工拼接。

### 导入与 force restore 的实际入口

内部 PKI 页的导入按钮固定执行普通恢复，只适用于**已经初始化且属于同一 PKI domain** 的 file-backed SQLite 目标。空白目标或不同 domain 的灾难接管会拒绝普通恢复，必须在源实例已隔离后显式调用 `force=true` API。两条路径都会替换整个目标 SQLite 数据库和匹配的 vault/key，所以应先按上节建立一致性回滚点，并先部署与 archive 完全相同的 release/schema。force restore 会原子激活 archive、保留 archive 的 PKI domain，并把版本设为 `epoch=max(目标 epoch, archive epoch)+1, security_revision=0`；它不是 UI 的“迁移激活”按钮。

当前 API 由 `X-Panel-Token` 认证，并以 multipart 表单接收 archive、单次 passphrase 和显式 `force=true`。当前服务端不会持久化或审计 multipart `reason`，所以示例不发送该字段；把恢复原因记录在受控的外部变更单中，不要把它当作服务端 Gate。在受信任终端执行，先做本地二次确认：

```bash
PANEL_URL='https://panel.example.com'
ARCHIVE='./internal-pki-backup.nre-pki'
umask 077
secret_dir="$(mktemp -d)" || exit 1
cleanup_secrets() {
  rm -f -- "$secret_dir/panel-header" "$secret_dir/passphrase"
  rmdir -- "$secret_dir" 2>/dev/null || true
}
trap cleanup_secrets EXIT
trap 'exit 130' HUP INT TERM

read -rsp 'Panel API token: ' PANEL_TOKEN && printf '\n'
printf 'X-Panel-Token: %s\n' "$PANEL_TOKEN" > "$secret_dir/panel-header"
read -rsp 'Protected-backup passphrase: ' PKI_PASSPHRASE && printf '\n'
printf '%s' "$PKI_PASSPHRASE" > "$secret_dir/passphrase"
unset PANEL_TOKEN PKI_PASSPHRASE
read -rp 'Type FORCE RESTORE to continue: ' FORCE_CONFIRM
[ "$FORCE_CONFIRM" = 'FORCE RESTORE' ] || { echo 'cancelled' >&2; exit 1; }
RESPONSE_FILE="$(mktemp "${TMPDIR:-/tmp}/nre-pki-restore-response.XXXXXX")" || exit 1

curl --fail-with-body -X POST \
  --header "@$secret_dir/panel-header" \
  --form "archive=@$ARCHIVE" \
  --form "passphrase=<$secret_dir/passphrase" \
  --form-string 'force=true' \
  --output "$RESPONSE_FILE" \
  "$PANEL_URL/panel-api/pki/backups/import" || {
    cat "$RESPONSE_FILE" >&2
    exit 1
  }

cat "$RESPONSE_FILE"
if grep -Eq '"cleanup_pending"[[:space:]]*:[[:space:]]*true' "$RESPONSE_FILE"; then
  CLEANUP_PENDING=true
elif grep -Eq '"cleanup_pending"[[:space:]]*:[[:space:]]*false' "$RESPONSE_FILE"; then
  CLEANUP_PENDING=false
else
  echo 'cleanup_pending missing: keep the target isolated' >&2
  exit 1
fi
printf 'cleanup_pending=%s; initial response=%s\n' "$CLEANUP_PENDING" "$RESPONSE_FILE"

# 响应会给出 status_url；仍在本 shell 中查询，退出时 trap 会删除凭据文件。
STATUS_PATH='/panel-api/pki/operations/<operation-id-from-response>'
curl --fail-with-body \
  --header "@$secret_dir/panel-header" \
  "$PANEL_URL$STATUS_PATH"
```

示例把 token header 和 passphrase 写入 `umask 077` 创建的临时文件；`curl` 参数只出现文件路径，秘密不会展开到进程 argv。`RESPONSE_FILE` 保存初始响应且不由 trap 删除，应移入受控变更记录并在不再需要时销毁。把响应中的实际 `status_url` 填入 `STATUS_PATH`，继续查询直到 operation 为 `succeeded`；轮询响应不会再次给出 `cleanup_pending`，不能代替已保存的初始响应。页面普通导入目前也不显示该字段；使用页面时把 cleanup 状态视为未知，按下节重启并检查 artifact。`failed` 或 `blocked` 时不要启动旧实例或重试普通导入。当前 force-restore 接口以 panel token、本地明确确认和 `force=true` 作为操作门槛，不接受 `/pki/confirmations` nonce；`activate/domain` nonce 只用于 tunnel-mTLS 迁移激活，不要混用两条流程。

#### 已提交但 cleanup pending

`succeeded` 只说明新数据库/vault/key 已提交。初始响应若为 `cleanup_pending=true`，旧数据库/vault/key backup、staging 目录或 restore journal 仍可能存在；字段缺失也按未决处理。此时保持旧源和目标 Relay 流量隔离，不执行人工回滚或删除任何 `.pki-restore-*`/`.pki-delete` 文件：

1. 使用完全相同的 SQLite、data root 和 master-key 路径，重启**同一个且唯一一个**目标控制面实例。存储在打开 SQLite 前会按 commit marker roll forward，并重试 journal、旧 backup、staging 和 tombstone 清理。
2. 等待控制面正常启动，确认内部 PKI overview healthy、PKI domain/epoch 与初始响应一致且 CA key 可解密。
3. 在标准 Compose 的宿主挂载路径执行以下只读检查；自定义部署还要检查 SQLite 文件父目录和外置 master-key 父目录。命令必须没有输出：

   ```bash
   find ./data -maxdepth 1 \
     \( -name '*.pki-restore-*' -o -name '.pki-restore-*' -o -name '*.pki-delete' \) -print
   [ ! -d ./secrets/nre-pki ] || find ./secrets/nre-pki -maxdepth 1 \
     \( -name '*.pki-restore-*' -o -name '.pki-restore-*' -o -name '*.pki-delete' \) -print
   ```

4. 只有启动成功且上述 artifact 已全部消失，才恢复 Relay 流量、升级版本或使用人工回滚包。若启动失败或仍有 artifact，继续隔离并保留日志和现场；不要手动删除或拼接 journal 管理的材料。

## 计划迁移

1. 安排维护窗口，等待正在运行的 PKI operation 完成，确认所有可达 Agent 已同步当前 epoch/security revision；记录源控制面的 release 和镜像 digest。
2. 在 file-backed SQLite 源实例的最终一致状态导出受保护 archive，并记录 manifest 中的 PKI domain/epoch。导出后不要再修改源端。普通配置包仅用于有意叠加 archive 之后的配置变化，不能代替该 archive。
3. 停止并隔离源实例，确保它不能继续取得 lease、签发或发布安全状态。不要让复制的 source/target 同时运行。
4. 在目标挂载预期的 data/key 目录，先部署与源端完全相同的 release/schema，并按“SQLite 恢复前的回滚点”同时保存目标数据库、`dataRoot/pki` 和外置 master key。**先执行受保护恢复，不要先导入普通配置包**：若目标已经初始化为同一 PKI domain，可在页面普通导入；若是空白/不同 domain 目标，按上面的 API 步骤显式发送 `force=true`。普通导入到空白目标必然失败，不要先创建无关 domain。
5. 核对恢复后的完整 Agent、规则、证书及面板状态，并区分版本语义：普通同-domain 恢复只保证版本不回退并采用 manifest 版本，epoch 可能保持相同、security revision 可能提升，也可能是同版本幂等恢复；只有 force restore 保证 epoch 高于目标和 archive，且 `security_revision=0`。
6. 只有确实需要叠加 archive 之后的普通配置变化时，才在上述核对完成后预览、导入普通配置包；导入前的任何目标配置都会被受保护恢复覆盖。
7. 在每台 Agent 更新 `NRE_MASTER_URL` 指向新控制面。地址变化不应重建 Agent ID、tunnel identity 或证书；既有 control token 和关联保持连续。
8. 验证 Agent 接受目标实例发布的当前 epoch、安全 snapshot 和 CA generation，并确认初始响应的 `cleanup_pending=false` 或已完成上述启动恢复清理，再恢复 Relay 流量。恢复验证通过后才能升级目标版本。

以上受保护 archive 流程只适用于 file-backed SQLite。PostgreSQL/MySQL 的搬迁/恢复按[备份与恢复](./backup-restore.md#postgresql-mysql-协同冷恢复)协同处理数据库、vault/data root 和 master key。

## 灾难恢复与 force restore/activation

只有旧实例确实不可用时才使用 force restore/activation：

1. 先从网络、调度器和存储层永久隔离旧实例。
2. 部署与 archive 来源完全相同的 control-plane release/schema，挂载可写的 data/key 目录，并按“SQLite 恢复前的回滚点”同时保存目标数据库、`dataRoot/pki` 和外置 master key；不要先升级或导入普通配置。
3. 按“导入与 force restore 的实际入口”执行 `force=true` API，输入单次 passphrase 并在本地键入 `FORCE RESTORE`；恢复原因另记受控变更单，当前服务端不会保存 multipart `reason`。面板普通导入不会对空白目标生效。
4. 核对被整库恢复的 Agent、规则和其他面板状态。新实例必须以 `epoch=max(目标 epoch, archive epoch)+1, security_revision=0` 发布完整安全 snapshot；Agent 会拒绝旧 epoch，即使旧 snapshot 的 security revision 数字更大。
5. 检查初始响应的 `cleanup_pending`；为 true 或缺失时先按上节重启并确认 journal/backup/staging artifacts 全部清理。随后更新 Agent 的 `NRE_MASTER_URL` 并逐个确认收敛。不要重新运行被隔离的旧实例；恢复验证通过后才能升级目标版本。

instance lease 提供共享 canonical state 上的协作式单活，不是分布式共识。两个完全隔离、各持一份数据库副本的实例仍可能各自运行；管理员必须保证旧实例隔离。PKI epoch 让 Agent 拒绝 stale 控制面安全状态，但不能代替外部单活编排。

离线 Agent 在断开期间只能使用最后一次已验证的安全 snapshot；它不会实时得知新撤销。节点重新连接后会优先同步当前 epoch/revocation 状态并关闭失效会话，因此撤销对离线节点存在直到重连的延迟。安全敏感场景应同时在网络层隔离该节点。
