# Relay 隧道

Relay 在 Agent 之间建立加密通道。流量经过中继节点再到后端，适合入口节点无法直连后端、需要利用中继节点线路优势的场景。

## 前置条件

- 控制面已部署并正常运行
- 至少有两个 Agent 节点（一个做入口，一个做中继）
- 中继节点的监听端口在防火墙中放行

## 创建 Relay 监听器

进入 **基础设施 → Relay 监听器**，选中继节点，点击 **新建监听器**。

| 字段 | 示例 | 说明 |
| --- | --- | --- |
| 名称 | `relay-fast-vps` | 给中继起个名字 |
| 监听证书来源 | 自动签发（Relay CA） | 维护升级前由系统管理；内部 PKI 激活后由 tunnel identity 接管 |
| 绑定地址 | `0.0.0.0` | 监听所有网卡 |
| 监听端口 | `7443` | 中继节点实际监听的端口 |
| 公网入口 | `relay.example.com:7443` | 其他节点连接中继时用的地址 |
| 传输方式 | `TLS/TCP` | 新手用默认值，熟悉后可试 QUIC |

![创建 Relay 监听器](/screenshots/panel-relay-form.png)

保存后在列表中确认监听器已启用。

生产环境应先完成[内部 PKI 升级与激活](../operations/internal-pki.md)。激活后的 TLS/TCP 与 QUIC Relay 都使用双向 TLS，双方证书必须属于当前 PKI domain，并匹配 Agent/listener identity、用途、有效期和撤销状态。界面中仍可见的旧 Relay CA/Pin 字段只服务维护升级前的兼容状态，不是故障时的安全回退。

![Relay 监听器列表](/screenshots/panel-relay-listener.png)

## 在规则中使用 Relay

创建 HTTP 或 L4 规则时，在 **Relay 配置** 标签页点击 **添加 Relay 层**，选择刚创建的 Relay 监听器。

```text
客户端 → 入口 Agent → Relay 隧道 → 出站 Agent → 后端
```

每层可以放多个 Relay 监听器作为并行候选，多层表示按顺序逐跳转发。新手先配一层一个节点，跑通后再扩展。

![L4 Relay 配置](/screenshots/panel-l4-relay-form.png)

## 验证

保存规则后回到列表，规则卡片带 `Relay` 标记说明已关联隧道。测试入口端口或域名确认连通。

还应验证无证书、错误 CA/EKU/identity/domain、过期或已撤销证书均被拒绝；同时 Agent 的 heartbeat/revision 等控制请求仍通过原 URL 和 `X-Agent-Token` 成功。PKI degraded 时 Relay 数据面会 fail closed，但不会把控制协议切换成 mTLS。

## Relay vs 直接转发

| 场景 | 用 Relay | 直接转发 |
| --- | --- | --- |
| 入口节点能直连后端 | 不需要 | ✓ |
| 入口节点无法直连后端 | ✓ | 不通 |
| 想利用中继节点线路 | ✓ | 不需要 |
| 需要多跳（A→B→C→后端） | ✓ | 不支持 |

深入了解 Relay 的协议细节，见 [Relay 协议内幕](../reference/relay-internals.md)。
