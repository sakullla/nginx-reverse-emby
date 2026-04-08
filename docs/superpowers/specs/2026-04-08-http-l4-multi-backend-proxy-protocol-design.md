# HTTP/L4 多后端、固定 DNS 缓存与 L4 PROXY Protocol 设计

- 日期：2026-04-08
- 状态：已确认设计，待进入实现计划
- 范围：`panel/frontend`、`panel/backend`、`go-agent`、现有 HTTP/L4 规则模型的兼容式演进

## 1. 背景

当前仓库的控制面与前端已经出现了部分 L4 多后端、负载均衡和 `PROXY Protocol` 字段，但执行面并未真正闭环：

1. Go agent 的 L4 runtime 仍然只按单一 `upstream_host/upstream_port` 工作。
2. HTTP 规则当前仍然是单一 `backend_url` 模型，不支持多后端。
3. 域名后端没有统一的执行面缓存与失败处理策略，无法满足 DDNS 与多目标切换场景。
4. L4 的 `PROXY Protocol` 需要真正落到纯 Go runtime，而不是仅存在于控制面表单。

本次设计的目标不是保留所有历史或 Nginx 风格能力，而是收敛出一套在 Go execution plane 中稳定可实现、HTTP 与 L4 语义一致的能力集合。

## 2. 目标

### 2.1 主要目标

1. HTTP 规则支持多个后端，并在单个请求失败时自动切换到后续后端。
2. L4 规则支持多个后端，并在 TCP/UDP 转发失败时自动切换到后续后端。
3. HTTP 与 L4 的 hostname backend 统一支持固定 `30s` DNS 缓存。
4. HTTP 与 L4 的失败目标统一支持按实际 `IP:port` 维度的失败缓存与指数退避。
5. L4 `TCP` 规则支持 `PROXY Protocol decode + send`。
6. 在不要求用户手工迁移规则的前提下，兼容现有单 backend / 单 upstream 规则。
7. 在引入多 backend、DNS 缓存与重试逻辑的同时，优化 HTTP 与 L4 转发热路径，避免明显性能回退。

### 2.2 非目标

本次不引入：

- `hash` 负载均衡
- `least_conn` 负载均衡
- `weight`、`backup`、`max_conns` 等高级 upstream 语义
- UDP `relay_chain`
- UDP `PROXY Protocol`
- 基于 HTTP 业务响应状态码的自动重试策略细分
- 独立的后台健康检查系统
- 复杂 metrics / observability 子系统

## 3. 总体方案

采用“控制面兼容扩展 + Go agent 统一 backend 执行层”的方案。

### 3.1 统一 backend 执行层

Go agent 新增一套共享 backend 执行层，HTTP runtime 与 L4 runtime 都通过它完成：

- backend 列表解析
- 简单策略选择
- hostname 的固定 `30s` DNS 缓存
- 实际 `IP:port` 的失败缓存
- 本次请求/连接内的失败切换
- 热路径上的缓存复用、连接复用与低分配路径

该执行层是本次设计的核心，避免 HTTP 与 L4 各自维护一套不同的缓存、失败与重试逻辑。

### 3.2 简单策略集

首版策略只保留：

- `round_robin`
- `random`

其他当前已在控制面或前端出现、但执行面不打算实现的字段，要么删除，要么在兼容层归一化后不再暴露给用户配置。

## 4. HTTP 规则设计

### 4.1 数据模型

HTTP 规则新增字段：

- `backends: [{ url }]`
- `load_balancing: { strategy }`

兼容字段继续保留：

- `backend_url`

语义约定：

- 运行时以 `backends` 为主。
- 若规则没有 `backends`，则将 `backend_url` 折叠成单 backend。
- API 返回中继续保留 `backend_url`，并镜像 `backends[0].url`，用于兼容旧页面与旧测试。

### 4.2 请求级 backend 选择

每个 HTTP 请求都会独立选择目标 backend：

1. 根据规则的 `backends` 与 `load_balancing.strategy` 生成本次候选顺序。
2. 若 backend 为 hostname，则通过固定 `30s` DNS 缓存获取可用 IP 列表。
3. 跳过当前处于失败缓存窗口中的 `IP:port`。
4. 尝试向当前候选目标发起请求。
5. 若发生可重试失败，则切换到下一个 backend 或该 backend 的下一个可用 IP。
6. 所有候选目标都失败时，返回 `502`。

### 4.3 HTTP 失败切换语义

用户已经明确要求：所有 HTTP 方法都允许自动重试到后续 backend。

因此本次设计采用“请求在上游不可用时允许跨 backend 重试”的统一语义，而不区分幂等与非幂等方法。该行为存在重复写入风险，但属于显式设计决策。

首版只对“上游不可用”的失败执行自动切换，包含：

- TCP connect 失败
- TLS 握手失败
- 请求发送失败
- 在收到响应头前连接中断

首版不把普通业务 `5xx` 响应自动视为可重试信号。

### 4.4 现有 HTTP 语义保持不变

以下行为保持现状：

- `Host` / 转发目标 URL 的基础改写逻辑
- `X-Forwarded-*`
- `proxy_redirect`
- `pass_proxy_headers`
- `custom_headers`
- `relay_chain`

多 backend 只改变“本次请求选哪个目标”，不改变 HTTP 规则已有头部或 relay 语义。

## 5. L4 规则设计

### 5.1 数据模型

L4 规则运行时以以下字段为主：

- `protocol`
- `backends: [{ host, port }]`
- `load_balancing: { strategy }`
- `tuning.proxy_protocol.decode`
- `tuning.proxy_protocol.send`
- `relay_chain`

兼容字段继续保留：

- `upstream_host`
- `upstream_port`

语义约定：

- 运行时统一从 `backends` 工作。
- 若没有 `backends`，则自动将 `upstream_host/upstream_port` 折叠成单 backend。
- API 返回中继续保留 `upstream_host/upstream_port`，并镜像首个 backend，用于兼容旧页面与旧测试。

### 5.2 TCP 多后端语义

对 `protocol=tcp` 的规则：

- 每个新入站连接独立选择 backend。
- 若选中的 backend 拨号失败，则按本次候选顺序继续尝试后续 backend。
- 所有 backend 都不可用时，关闭该客户端连接。

### 5.3 UDP 多后端语义

对 `protocol=udp` 的规则：

- 每个客户端会话绑定一个 backend。
- 首次向上游发送时，根据策略和缓存选择 backend。
- 若当前 backend 明显不可用，则在本次发送路径上切换到后续 backend。

UDP 不支持：

- `relay_chain`
- `PROXY Protocol`

### 5.4 L4 PROXY Protocol

首版仅 `TCP` 支持 `PROXY Protocol`。

#### `decode`

当 `tuning.proxy_protocol.decode=true` 时：

- 入站连接建立后，先解析 PROXY Protocol 头。
- 首版支持 `v1` 与 `v2`。
- 首版只读取核心地址信息，不处理 TLV 扩展。
- 若配置了 `decode` 但入站没有合法 PROXY Protocol 头，直接拒绝该连接。

#### `send`

当 `tuning.proxy_protocol.send=true` 时：

- 与目标 backend 建立 TCP 连接后，先写入 PROXY Protocol 头，再开始双向转发。
- 若本连接已经通过 `decode` 取得真实客户端地址，则优先使用解析出的真实地址写入 PROXY 头。
- 若未启用 `decode`，则使用当前 socket 的远端地址作为客户端地址。

#### `decode + send`

当同一规则同时启用 `decode` 与 `send` 时：

- agent 负责把前置代理传入的真实客户端地址继续透传给最终 backend。
- 这满足“前置 LB/代理 -> agent -> 最终服务”链路的真实地址透传需求。

### 5.5 relay 语义

L4 `relay_chain` 继续只允许 `TCP` 规则使用。

当规则存在 `relay_chain` 时：

- 目标 backend 仍然是最终 upstream/backend。
- `send=true` 时，PROXY Protocol 头会写入最终建立出的 TCP 数据流。
- relay hop 本身不增加特殊的 PROXY Protocol 控制语义，保持其为透明 TCP 载荷。

## 6. DNS 与失败缓存设计

### 6.1 DNS 缓存

hostname backend 使用固定 TTL 的本地缓存：

- 缓存 key：hostname
- 缓存值：解析得到的 IP 列表
- 过期时间：固定 `30s`

语义约定：

- DNS 缓存只负责域名解析结果缓存。
- DNS 缓存与失败缓存是两套独立机制。
- DNS 解析失败不会污染已有缓存结构，也不会被解释为 DDNS 变化信号。

### 6.2 失败缓存

失败缓存只记录“实际拨号目标”的短时失败状态：

- 缓存 key：实际 `IP:port`
- 不按配置的域名 backend 做整体熔断

失败缓存覆盖的典型失败包括：

- 连接超时
- 连接被拒绝
- 网络不可达
- TLS 握手失败
- 在建立可用上游连接前发生的其他连接层异常

### 6.3 指数退避

失败缓存过期时间采用指数退避：

- 连续失败时按指数增长
- 成功一次即清零
- 上限 `60s`

这样可以在目标持续故障时快速降低无效重试压力，又不会长期压制已经恢复的目标。

### 6.4 DNS 与失败缓存联动

同一个 hostname 解析出多个 IP 时：

- DNS 缓存提供 IP 列表
- 失败缓存只屏蔽其中最近失败的 `IP:port`
- 该 hostname 的其他未失败 IP 仍然允许继续尝试

这使得 DDNS、多 A 记录或短时部分节点故障场景都能稳定工作。

### 6.5 性能约束

新增能力不能以明显放大转发热路径开销为代价。

首版需要显式优化以下路径：

- HTTP 上游请求必须复用共享 `http.Transport` 连接池，不能为每次请求重建 transport。
- backend 选择、DNS 缓存与失败缓存查询应避免在每次请求/连接中产生不必要的大对象分配。
- DNS 缓存、失败缓存与策略状态应放在共享执行层，避免 HTTP/L4 各自重复维护数据结构。
- L4 TCP 转发应保持直接流复制，`PROXY Protocol send` 仅在建连初期额外写入一次头部。
- L4 UDP 应尽量复用会话级上游 socket，避免每个数据报都重新 `Dial`。
- backend 失败切换前应先过滤失败缓存，减少无效 connect 尝试。

性能优化目标是“在功能新增后保持或提升现有直连路径表现”，而不是引入复杂 benchmark 基础设施或做与当前范围无关的过度优化。

## 7. 控制面与前端设计

### 7.1 后端归一化与校验

`panel/backend/server.js` 需要统一规则归一化逻辑：

- HTTP：支持 `backend_url` 与 `backends` 的兼容归一化
- L4：继续支持 `upstream_*` 与 `backends` 的兼容归一化
- `load_balancing.strategy` 只允许 `round_robin` / `random`
- HTTP backend URL 只允许 `http` / `https`
- L4 backend 至少包含合法 `host + port`
- `udp` 规则若设置 `proxy_protocol`，直接返回校验错误
- `relay_chain` 仍保持仅 `tcp` 允许

### 7.2 存储兼容

存储层采用“读时兼容、写时规范化”的懒迁移：

- 不引入一次性离线迁移脚本
- 旧规则读取后仍能正常工作
- 新规则保存后统一写成新结构
- 旧兼容字段继续可读、可下发

JSON / Prisma / runtime packaging / heartbeat snapshot 都需要同步扩展新字段。

### 7.3 前端收敛

前端应收敛为与执行面一致的能力模型：

- HTTP RuleForm 从单 `backend_url` 改成多 backend 列表
- L4 RuleForm 保留现有多 backend UI，但删除或隐藏本次不实现的高级语义
- UI 上仅保留 `round_robin` / `random`
- 移除或停止暴露 `hash`、`least_conn`、`weight`、`backup`、`max_conns`

规则展示页与搜索页应支持：

- 显示首个 backend
- 标示“其余 backend 数量”
- 搜索新 `backends` 字段
- 继续兼容旧 `backend_url` / `upstream_*` 字段

## 8. 错误处理

### 8.1 运行时错误

运行时遵循“尽量切换、全部失败才报错”的原则：

- 单个 backend 失败不等于整条规则失效
- 当前请求/连接只有在所有候选目标都失败时才对客户端报错
- 某个失败目标写入失败缓存
- 某个目标成功后清零其失败退避状态

### 8.2 配置错误

配置错误在 apply/启动阶段直接失败，例如：

- 非法负载均衡策略
- 空 backend 列表
- 非法 backend URL / host / port
- UDP 上配置 `PROXY Protocol`
- 非 TCP 规则配置 `relay_chain`

runtime 不进入“部分生效、部分忽略”的模糊状态。

## 9. 测试与验收

### 9.1 后端与存储测试

至少覆盖：

- HTTP/L4 规则归一化
- 新旧字段兼容
- 存储 round-trip / compatibility / isolation / revision
- heartbeat / snapshot 下发新字段

### 9.2 Go agent HTTP 测试

至少覆盖：

- `round_robin`
- `random`
- 单 backend 兼容回退
- hostname backend + 固定 `30s` DNS 缓存
- 实际 `IP:port` 失败缓存
- 失败缓存指数退避与成功清零
- backend 失败后自动切换
- 所有 HTTP 方法都允许 failover retry

### 9.3 Go agent L4 测试

至少覆盖：

- TCP 多 backend 轮询 / 随机
- UDP 多 backend 会话行为
- hostname backend + 固定 `30s` DNS 缓存
- 实际 `IP:port` 失败缓存
- 失败缓存指数退避与成功清零
- `PROXY Protocol v1` decode
- `PROXY Protocol v2` decode
- `PROXY Protocol send`
- `decode + send` 联动
- UDP 配置 `PROXY Protocol` 被拒绝
- relay + multi-backend + PROXY Protocol 的兼容路径

### 9.4 验收标准

验收以以下结果为准：

1. HTTP 规则可配置多个 backend，并在上游不可用时自动切换。
2. L4 规则可配置多个 backend，并在目标失败时自动切换。
3. hostname backend 统一使用固定 `30s` DNS 缓存。
4. 实际 `IP:port` 统一使用失败缓存与指数退避，最大窗口 `60s`。
5. TCP L4 规则支持 `PROXY Protocol decode + send`。
6. UDP 明确不支持 `PROXY Protocol`。
7. 旧的单 `backend_url` / 单 `upstream_*` 规则无需人工迁移即可继续工作。

## 10. 分解建议

该设计可以在实现阶段拆为以下顺序：

1. 控制面模型与存储兼容扩展
2. Go agent 共享 backend 执行层
3. HTTP runtime 多 backend
4. L4 runtime 多 backend
5. L4 `PROXY Protocol`
6. 前端表单与展示收敛
7. 兼容与回归验证
