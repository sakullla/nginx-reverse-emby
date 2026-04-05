# 链式 Relay 与 Relay 证书管理设计

- 日期：2026-04-05
- 状态：已完成设计，待用户 review spec
- 范围：HTTP 规则、L4 TCP 规则、Relay Listener 对象、证书管理扩展、Master/Agent 同步、Agent apply/runtime

## 1. 背景

当前项目已经具备以下能力：

- Master/Agent 架构下发 HTTP 规则、L4 规则和证书策略
- 面板统一管理规则、证书与 agent
- Agent 通过 heartbeat 获取增量配置并执行 apply
- Nginx 负责 HTTP 与 L4 入口代理

当前缺失的能力是：在现有规则体系中增加一套“链式 relay”模型，让请求在内部链路上经过一跳或多跳 relay 节点后，再由最后一跳访问最终后端。客户端不感知 relay 的存在。

目标流转示例：

- 客户端 -> A
- A -> relay A
- relay A -> relay B
- relay B -> B 或最终 backend

其中内部 hop 之间通过证书与 pin/CA 信任建立 TLS 隧道。

## 2. 本次目标

### 2.1 本次要支持

1. 增加全局 `RelayListener` 对象，归属于已注册 Agent。
2. 一个 Agent 支持配置多个 Relay Listener。
3. HTTP 规则与 L4 规则支持引用具体的 `relay_chain`。
4. `relay_chain` 支持：
   - 不经 relay
   - 一跳 relay
   - 多跳 relay
5. HTTP 规则最终仍落到现有 `backend_url`。
6. L4 规则第一版仅支持 `tcp` 使用 relay。
7. 证书管理复用现有体系，并扩展 relay 隧道用途。
8. Relay 证书支持：
   - 自动签发
   - 手动上传
   - 自签名证书
9. Relay TLS 信任支持：
   - pin
   - CA
   - 两者组合策略
10. 仅支持“已注册 Agent”作为 relay 节点。
11. 保持现有 Master/Agent 下发与 apply 模型可扩展，不破坏现有无 relay 规则。

### 2.2 本次明确不做

1. 不支持未注册外部节点作为 relay。
2. 不支持 UDP relay。
3. 不做端到端单隧道语义，采用逐跳 TLS。
4. 不做自动故障绕行、动态选路、智能路由。
5. 不做完整自建 PKI 平台，仅支持通过现有证书管理上传或生成所需证书材料。
6. 不把多跳 relay 逻辑直接塞进复杂 Nginx 配置模板中实现。

## 3. 推荐方案

采用“全局 Relay Listener + 规则直接引用具体 listener 链”的方案。

### 原因

1. 与需求完全一致：规则直接选择具体 relay listener，而不是选 Agent 模板。
2. 支持一个 Agent 多个 relay listener，扩展性足够。
3. 证书、pin、CA 信任可以清晰绑定到 listener，而不是散落在规则里。
4. HTTP 与 L4 共用一套 relay 对象池，建模统一。
5. 更契合现有 Master/Agent 控制面：规则、证书、listener 都是可下发对象。

### 不采用的方案

- 规则内嵌 relay 配置：
  - 配置重复严重
  - 不利于证书复用、轮换与审计
- 引入额外 relay profile / 模板层：
  - 第一版复杂度过高
  - 当前没有足够收益

## 4. 架构设计

### 4.1 核心对象边界

新增独立对象：`RelayListener`

职责划分如下：

- `ManagedCertificate`
  - 负责证书材料、签发方式、用途
- `RelayListener`
  - 负责某个 Agent 上监听什么地址端口、绑定哪个证书、用什么 TLS 信任策略
- `Rule` / `L4Rule`
  - 负责业务入口与最终后端
  - 通过 `relay_chain` 引用具体 listener 链

为简化规则引用与多 Agent 编排，`RelayListener.id` 采用全局唯一语义，不做“每 Agent 内局部自增 ID”。

### 4.2 规则语义

HTTP 规则：

- 入口仍由 A 上的现有 HTTP 规则承载
- 当 `relay_chain` 为空时，行为与现在一致
- 当 `relay_chain` 非空时，请求进入本地 relay runtime，再经多跳 relay 到达最后一跳，由最后一跳访问 `backend_url`

L4 规则：

- 第一版仅允许 `protocol=tcp` 时配置 `relay_chain`
- 当 `relay_chain` 为空时，行为与现在一致
- 当 `relay_chain` 非空时，TCP 连接进入本地 relay runtime，经多跳 relay 后由最后一跳访问最终 upstream
- 最终 upstream 语义与当前规则保持一致，默认仍以现有主 upstream 字段或首个 backend 作为出口目标

### 4.3 数据流

对客户端可见的入口：

- 客户端 -> A

内部 relay hop：

- A runtime -> relay listener 1
- relay listener 1 -> relay listener 2
- ...
- relay listener N -> 最终出口

出口语义：

- HTTP：最后一跳访问规则的 `backend_url`
- L4 TCP：最后一跳访问规则的 `upstream_host:upstream_port` 或主 backend

客户端对 relay 无感知。

## 5. 运行时方案

### 5.1 推荐实现

新增独立的 Node relay runtime，不把多跳 relay 协议逻辑硬编码进 Nginx 模板。

Nginx 保持职责简单：

- HTTP/L4 入口接流量
- 对于不使用 relay 的规则，继续直接代理
- 对于使用 relay 的规则，把流量转发到本地 relay runtime

relay runtime 负责：

- 建立逐跳 TLS 隧道
- 校验 pin 与 CA
- 按 `relay_chain` 执行一跳或多跳转发
- 作为最后一跳访问最终 `backend_url` 或 TCP upstream

### 5.2 为什么不直接用 Nginx 多跳实现

1. Nginx 更适合单跳 proxy，而不是多跳编排。
2. 多跳 + pin + CA + HTTP/L4 统一支持会让模板和 shell 脚本变得不可维护。
3. 逐跳握手、证书校验、错误定位、状态上报更适合在 Node runtime 中实现。

### 5.3 relay runtime 角色

同一 runtime 内包含两个能力：

- relay listener server
  - 监听 `listen_host:listen_port`
  - 持有 server 证书
  - 接收上一跳隧道连接
- relay dialer
  - 按 `relay_chain` 向下一跳发起 TLS 连接
  - 执行 pin / CA 校验

## 6. TLS 与信任模型

### 6.1 总体原则

采用逐跳 TLS，而不是端到端单一 TLS。

每个 hop 独立完成：

- TLS 建连
- server 证书校验
- pin / CA 信任判断

### 6.2 支持的信任方式

第一版支持以下模式：

- `pin_only`
- `ca_only`
- `pin_or_ca`
- `pin_and_ca`

默认推荐：`pin_or_ca`

### 6.3 pin 模型

pin 使用集合而不是单值，便于证书轮换。

建议支持：

- 证书指纹 pin
- SPKI / public key pin

展示层优先向用户展示：

- 证书 SHA-256 指纹
- SPKI pin

实现层优先推荐 SPKI pin，因为续签但复用同一密钥时更稳定。

### 6.4 CA 信任模型

RelayListener 可以引用一组信任 CA 证书：

- `trusted_ca_certificate_ids`

这些 CA 证书也走现有证书管理体系。

### 6.5 自签名证书

第一版支持两种与 relay 直接相关的证书来源：

- 手动上传 `cert/key`
- 平台生成自签名证书

自签名证书场景下，推荐用 pin 作为第一版主要信任方式。

## 7. 数据模型设计

### 7.1 新增 RelayListener

建议新增持久化对象 `RelayListener`：

```json
{
  "id": 1,
  "agent_id": "agent-a",
  "name": "relay-a-public",
  "listen_host": "0.0.0.0",
  "listen_port": 18443,
  "enabled": true,
  "certificate_id": 12,
  "tls_mode": "pin_or_ca",
  "pin_set": [
    {
      "type": "spki_sha256",
      "value": "BASE64_VALUE"
    }
  ],
  "trusted_ca_certificate_ids": [31],
  "allow_self_signed": true,
  "tags": ["relay", "public"],
  "revision": 3
}
```

字段定义：

- `id`
- `agent_id`
- `name`
- `listen_host`
- `listen_port`
- `enabled`
- `certificate_id`
- `tls_mode`
- `pin_set`
- `trusted_ca_certificate_ids`
- `allow_self_signed`
- `tags`
- `revision`

### 7.2 扩展 Rule

HTTP 规则新增：

```json
{
  "relay_chain": [101, 204, 309]
}
```

语义：

- 数组中的值是具体 `RelayListener.id`
- 顺序即 hop 顺序
- 空数组表示不经 relay

### 7.3 扩展 L4Rule

L4 规则新增：

```json
{
  "relay_chain": [101, 204]
}
```

校验约束：

- 仅 `protocol=tcp` 允许非空 `relay_chain`

### 7.4 扩展 ManagedCertificate

现有证书对象新增 `usage` 或 `usages` 字段，至少支持：

- `edge`
- `relay_tunnel`
- `relay_ca`

并扩展来源 / 模式：

- `master_cf_dns`
- `local_http01`
- `manual_upload`
- `self_signed`

## 8. API 设计

### 8.1 新增 RelayListener 接口

- `GET /api/agents/:agentId/relay-listeners`
- `POST /api/agents/:agentId/relay-listeners`
- `PUT /api/agents/:agentId/relay-listeners/:id`
- `DELETE /api/agents/:agentId/relay-listeners/:id`

### 8.2 扩展现有规则接口

HTTP 规则：

- `GET /api/agents/:agentId/rules`
- `POST /api/agents/:agentId/rules`
- `PUT /api/agents/:agentId/rules/:id`

L4 规则：

- `GET /api/agents/:agentId/l4-rules`
- `POST /api/agents/:agentId/l4-rules`
- `PUT /api/agents/:agentId/l4-rules/:id`

扩展字段：

- `relay_chain`

### 8.3 扩展证书接口

证书管理接口新增：

- 证书用途字段
- 手动上传证书材料
- 创建自签名证书
- 返回可展示的 pin / fingerprint 信息

## 9. Master/Agent 同步设计

### 9.1 heartbeat 下发扩展

现有 heartbeat `sync` 载荷已下发：

- `rules`
- `l4_rules`
- `certificates`
- `certificate_policies`

本次扩展为：

- `relay_listeners`
- `relay_runtime_config` 或能生成该配置的等价字段

### 9.2 下发原则

对某个 Agent，只下发：

- 属于该 Agent 的 relay listeners
- 该 Agent 作为入口或中间 hop、最后一跳所需的证书材料
- 该 Agent 需要信任的 CA / pin 信息
- 该 Agent 需要消费的 HTTP / L4 relay chain 数据

### 9.3 revision 设计

以下任一变化都应推动对应 Agent 的 `desired_revision` 增长：

- RelayListener 变更
- 规则 `relay_chain` 变更
- Relay 证书与信任材料变更

## 10. Agent apply 设计

### 10.1 apply 输入

agent apply 阶段需要额外落盘：

- `relay_listeners.json`
- `relay_runtime.json`
- relay 证书材料
- relay trust 材料

### 10.2 apply 输出

1. 继续生成 Nginx HTTP / stream 配置。
2. 对使用 relay 的规则，Nginx 不直接指向最终后端，而是指向本地 relay runtime。
3. 生成 relay runtime 配置文件。
4. reload 或重启 relay runtime。

### 10.3 与现有脚本关系

需要扩展：

- `server.js`
- `scripts/light-agent.js`
- `scripts/light-agent-apply.sh`
- `docker/25-dynamic-reverse-proxy.sh`

其中 shell 脚本负责：

- 配置落盘
- 进程启动 / reload

具体 relay 协议逻辑不放在 shell 里实现。

## 11. 前端设计

### 11.1 新增 Relay Listener 管理界面

建议新增独立 UI 区域或页面，用于管理某个 Agent 上的 Relay Listener：

- 名称
- 监听地址
- 监听端口
- 证书
- TLS 模式
- pin
- trusted CA
- enabled

### 11.2 HTTP / L4 规则表单扩展

在 HTTP 与 L4 规则表单中新增 relay 配置区：

- 是否启用 relay
- relay chain 选择器
- 直接选择具体 listener
- 支持链路排序

### 11.3 L4 表单限制

当 `protocol=udp` 时：

- relay UI 置灰
- 显示“第一版仅支持 TCP relay”

### 11.4 证书表单扩展

证书管理页增加：

- 用途
- 来源
- 是否可作为 relay CA
- pin / fingerprint 展示

## 12. 校验规则

### 12.1 RelayListener 校验

1. `listen_host` 必须是合法 IP 或域名。
2. `listen_port` 必须是合法端口。
3. 同一 Agent 下 `listen_host + listen_port` 不允许冲突。
4. `certificate_id` 必须存在且支持 `relay_tunnel`。
5. `pin_set` 与 `trusted_ca_certificate_ids` 不能同时为空。
6. `tls_mode` 必须是支持的枚举值。

### 12.2 relay_chain 校验

1. 所有 listener 必须存在。
2. 所有 listener 必须属于已注册 Agent。
3. 所有 listener 必须 `enabled=true`。
4. chain 中不允许同一 listener 重复出现。
5. L4 非 TCP 时不允许设置 relay_chain。

### 12.3 证书校验

1. `relay_tunnel` 证书必须包含可供 runtime 使用的 cert/key 材料。
2. `relay_ca` 证书必须能作为 trust anchor 消费。
3. 自签名证书允许被引用，但必须由信任策略明确允许。

## 13. 错误处理与可观测性

### 13.1 控制面错误

创建 / 保存阶段直接拦截：

- listener 不存在
- listener 不可用
- 证书用途不匹配
- 非 TCP 使用 L4 relay
- pin / CA 都为空
- chain 自循环

### 13.2 运行时错误

错误信息按 hop 定位，至少包括：

- `hop 1 tls pin mismatch`
- `hop 2 ca verify failed`
- `hop 2 dial timeout`
- `hop 3 certificate expired`
- `exit backend dial failed`

### 13.3 状态上报

第一版建议上报：

- RelayListener 最近握手状态
- 最近错误
- 最近成功时间
- 当前 apply revision
- 规则是否启用 relay

## 14. 测试设计

### 14.1 后端测试

至少覆盖：

1. RelayListener normalize / validate
2. Rule / L4Rule `relay_chain` normalize / validate
3. revision 增长逻辑
4. 存储 roundtrip
5. 存储兼容性
6. chain 环检测
7. 证书用途与 TLS 模式校验

### 14.2 运行时测试

至少覆盖：

1. 无 relay
2. 一跳 relay
3. 多跳 relay
4. pin 成功 / 失败
5. CA 成功 / 失败
6. self-signed + pin 成功
7. HTTP 出口访问 backend_url
8. L4 TCP 出口访问 upstream

### 14.3 集成验证

最低验证命令：

- `cd panel/backend && npm test`
- `cd panel/frontend && npm run build`
- `docker build -t nginx-reverse-emby .`

建议补充：

- 多 agent / 多跳 relay 容器化 smoke test

## 15. 实施边界与推荐顺序

### 15.1 推荐阶段

阶段 1：控制面建模

- 新增 RelayListener
- 扩展证书用途
- 扩展 Rule / L4Rule 的 `relay_chain`
- 完成前后端 CRUD

阶段 2：同步与 apply

- heartbeat payload 扩展
- relay 配置落盘
- runtime 配置生成

阶段 3：relay runtime

- TCP hop
- TLS + pin / CA
- HTTP 与 L4 TCP 转发

阶段 4：状态与补强

- 错误上报
- listener 状态展示
- 证书轮换验证

### 15.2 第一版最终结论

第一版正式落地的推荐方案是：

1. 全局 `RelayListener` 对象。
2. HTTP / L4 规则直接引用具体 listener 链。
3. 证书管理复用现有体系，扩展 `relay_tunnel` 与 `relay_ca` 用途。
4. 运行时采用独立 Node relay runtime，Nginx 只负责入口与本地转发。
5. 第一版支持 HTTP + L4 TCP，支持 0 跳 / 1 跳 / 多跳 relay。
6. 安全模型支持 pin 与 CA，默认推荐 `pin_or_ca`。
7. relay 节点仅限已注册 Agent。
