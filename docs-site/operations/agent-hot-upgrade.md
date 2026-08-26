# 独立 Agent 热升级

`hot_upgrade` 只适用于声明自更新能力的独立 `nre-agent`。Embedded local Agent 与 control-plane 容器本身不通过此路径热升级。当前完整 listener/packet FD handoff 支持平台是：

- `linux/amd64`（version policy platform 写 `linux-amd64`）
- `linux/arm64`（version policy platform 写 `linux-arm64`）

这条二进制热升级路径不替代旧 Relay 认证迁移。首次切换到内部 PKI mTLS 时仍需维护窗口、bound re-enrollment、稳定 Agent/listener 关联核对和显式 activation；见[内部 PKI 升级与运维](./internal-pki.md)。控制面的 revision/upgrade 请求始终使用既有 token 控制协议，不新增 mTLS 控制端口。

非 Linux、其他 architecture 或未声明对应 package/capability 的 Agent 会在 preflight 显式拒绝，不会降级为有停机的进程替换。

## Package 与 generation

默认镜像在 public Agent assets 目录随附受支持平台的 `nre-agent-<platform>`。Version policy 设置目标版本；`packages` 留空时，control plane 按 Agent 报告的平台选择 bundled binary，并生成包含 URL、SHA-256、platform、filename、size 的完整 `version_package_meta`：

```json
{
  "id": "stable",
  "channel": "stable",
  "desired_version": "2.0.0",
  "packages": [],
  "tags": []
}
```

如果使用自建分发流程，送达 Agent snapshot 的 package metadata 仍必须同时提供安全 basename `filename`、正 `size`、64 位十六进制 SHA-256 和精确 platform；缺一项或 platform 不匹配会在 preflight 拒绝。不要只用 URL/SHA 假定可以热升级。

Agent 把 package 下载到 digest 命名的 immutable 目录，校验 size/SHA-256 和可执行文件，写入 current/previous pointer 后启动 child。目标二进制和目标 snapshot 绑定同一 revision/generation identity，child 必须完成 snapshot prepare、listener/packet authority 接管和 readiness 才能激活。

Package 下载、校验和 immutable 暂存由 Agent 进程内的单飞协调器执行，不占用周期 heartbeat，也不计入 revision apply lease。下载很慢时，Agent 仍按既有周期上报当前运行 package、当前 revision 和 apply 状态；控制面因而继续更新最近心跳并按原有三倍心跳阈值判断真实在线/离线。暂存中的 package 在节点状态中保持 `pending`，不能视为目标版本或 revision 已应用。

只有 package 已 verified-ready，且后续 heartbeat 仍指向相同 immutable package 与目标版本时，Agent 才重新拉取当前 revision lease，并进入既有 Start、Activate、apply 和 report 流程。下载失败、校验失败、目标变化或 Agent shutdown 会取消或结束暂存 attempt，旧 binary、旧 revision 和现有 HTTP/L4 数据面继续服务；下一次同步沿用既有重试规则，不会把暂存失败报告成 applied。URL 只是获取位置，同一 platform/SHA-256/size/filename identity 不会因 URL 或版本展示值变化而并发重复下载。

升级期间：

1. Parent 继续服务旧 generation，并把新 stream/packet authority 有序交给 child。
2. Child ready 后新连接进入目标 generation；旧 TCP 会话与 UDP/QUIC association 仍由原 owner 服务。
3. Parent 按该 revision 固化的 drain timeout 排空；authority journal 记录 launch、activation 和 transfer checkpoint。
4. 接管完成后 current pointer 保持目标 binary，previous pointer 保留前一版本；parent 退出，child 成为 supervisor。

Child prepare、activation、authority transfer 或 identity 校验失败时，parent 保持或恢复 authority 和 last-known-good generation，失败 child 被终止，current pointer 恢复 previous。失败不会把未 ready generation 报为 applied。

## 操作步骤

1. 确认 Agent 列表中的 runtime platform/arch、当前 package SHA-256、capabilities 和最近心跳。
2. 确认 control plane 的 public assets 中存在目标平台 binary，并在 heartbeat/snapshot 中核对完整 `version_package_meta` 的 SHA-256、filename 和 size。
3. 将目标 Agent 的 `desired_version` 更新到新版本；该写入返回 202 operation，不是同步完成。
4. 跟踪 operation `status_url`、Agent revision、`nre_hot_restart_upgrade_total` / `nre_agent_hot_restart_upgrade_total` 和 correlation 日志。
5. 只有 revision 为 applied 且 drain 已 drained/forced 后，才把升级视为稳定完成。

异常处理：

- `unsupported`：核对 Agent 报告的平台是否为 Linux amd64/arm64，且 version policy 有精确 platform package；不要强制使用其他平台 binary。
- digest/size mismatch：撤下损坏 artifact，发布新 immutable URL/digest；不要覆盖已有 digest 目录。
- 下载长时间未完成：先确认节点最近心跳仍持续推进且 package 状态为 `pending`；不要因 apply deadline 尚未开始而手工标记失败。若最近心跳停止，则按普通 Agent 网络/进程失联排查，控制面仍会在原有离线阈值后标记 offline。
- child readiness/authority failure：保留日志中的 revision/generation/attempt，确认 parent 仍服务旧 generation，再显式 retry failed revision。
- 目标配置本身有误：使用 revision rollback，把 last-known-good snapshot 复制成新的 desired revision；不要手工改历史记录。

## 验证

```bash
cd go-agent && go test ./internal/hotrestart ./internal/app ./internal/platform
cd go-agent && go test ./...
cd scripts/generation-soak && go test -run TestGenerationMatrix -count=1 ./...
```

Linux 发布验证还应检查真实 process/packet matrix：新连接无由升级导致的拒绝，旧会话保持原路径，重复升级后 child/parent 进程、FD、goroutine 和 draining generation 均回到有界基线。
