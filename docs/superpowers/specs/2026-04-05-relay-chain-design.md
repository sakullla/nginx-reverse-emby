# Go Agent / Relay / Proxy 统一执行面设计

- 日期：2026-04-05
- 状态：已完成设计，待用户 review spec
- 范围：Go agent、Go local agent、Go proxy engine、Go relay runtime、Go updater、证书与规则控制面扩展

## 1. 背景

当前项目的控制面已经成熟：

- `panel/backend` 负责规则、证书、agent、revision、heartbeat 控制
- `panel/frontend` 负责配置与可视化管理
- Master/Agent 采用 heartbeat pull 模型

当前执行面仍主要建立在以下组合上：

- Node / shell 脚本
- Nginx HTTP / stream 配置生成
- `acme.sh` 证书签发与安装

本次需求叠加了四个方向：

1. 支持链式 relay
2. 支持 Windows 平台
3. 支持 master 本地节点
4. 支持 agent 自动更新

如果继续沿用 `Node + shell + Nginx + acme.sh` 的执行面，这四个目标会相互叠加复杂度。尤其是：

- relay 更适合在强类型网络程序中实现
- Windows 不适合长期依赖 Nginx 作为执行引擎
- 自动更新不适合建立在多进程脚本拼接模型上
- 本地节点与远端节点如果继续用不同执行栈，迁移成本会持续放大

因此本设计将执行面整体收敛为 Go。

## 2. 本次目标

### 2.1 本次要支持

1. 保持 `panel/backend` 和 `panel/frontend` 不迁移语言。
2. 所有 agent/runtime 统一改为 Go。
3. master 本地节点也改为独立 Go local agent。
4. 所有 agent 继续走现有 heartbeat pull 模型。
5. relay 作为 Go agent 内置模块实现。
6. 执行面逐步去 Nginx 化，最终由 Go proxy engine 接管 HTTP/TLS/WebSocket/L4，覆盖 TCP 与 UDP 直连能力。
7. 证书签发与装载也切到 Go，不再依赖 `acme.sh` 作为长期方案。
8. agent 支持 Master 指定 `desired_version` 的自动更新。
9. relay 继续采用“全局 RelayListener + 规则直接引用 listener 链”的对象模型。
10. Windows 与 Linux 尽量共用同一套执行逻辑。

### 2.2 本次明确不做

1. 不迁移 `panel/backend` 到 Go。
2. 不迁移 `panel/frontend` 到 Go。
3. 不保留 Nginx `stream` 特有调优指令兼容。
4. 不做 UDP relay。
5. 不做完整通用 Web 服务器，不以通用 Nginx 替代品为目标。
6. 不做未注册外部节点作为 relay。
7. 不做指令级 Nginx 完全兼容。

## 3. 推荐方案

采用“Node 控制面 + Go 统一执行面”的方案。

### 3.1 控制面保持不变

- `panel/backend` 继续负责：
  - 规则管理
  - 证书管理
  - RelayListener 管理
  - revision 管理
  - heartbeat 协议
  - agent 注册与状态展示
- `panel/frontend` 继续作为唯一 UI

### 3.2 执行面统一为 Go

Go agent 统一承担：

- heartbeat 同步
- 规则落盘与状态管理
- HTTP proxy engine
- L4 engine
- relay runtime
- 证书签发与热更新
- 自动更新与回滚

### 3.3 为什么不保留长期 Nginx 依赖

1. Windows 平台下长期依赖 Nginx 不合适。
2. relay、多跳 TLS、pin/CA 校验更适合在 Go 里统一实现。
3. 现有执行链条太依赖模板和脚本，自动更新与跨平台适配成本高。
4. L4 中 Nginx 特有指令已经不再要求保留。

## 4. 总体架构

系统分为两层。

### 4.1 控制面

- `panel/backend`
- `panel/frontend`

控制面仍以当前数据模型为中心，只做以下扩展：

- 增加 RelayListener 对象
- 增加证书用途与签发模式
- 增加版本管理与 `desired_version`
- 增加 Go agent 能力上报与运行状态

### 4.2 执行面

统一为一个 Go 二进制，逻辑上包含：

- `agent-core`
- `sync-client`
- `config-store`
- `proxy-engine`
- `l4-engine`
- `relay-runtime`
- `cert-manager`
- `updater`

本地节点与远端节点都运行这同一套二进制。

## 5. Go Agent 模块划分

### 5.1 agent-core

负责：

- 进程入口
- 配置加载
- 生命周期管理
- heartbeat 调度
- apply 调度
- 模块热重载协调
- 自动更新调度

### 5.2 sync-client

继续走 heartbeat pull。

上报：

- `agent_id`
- `current_revision`
- `desired_revision`
- `version`
- `platform`
- `capabilities`
- `last_apply_status`
- `last_apply_message`
- relay listener 状态
- proxy engine 状态
- l4 engine 状态

拉取：

- `rules`
- `l4_rules`
- `certificates`
- `certificate_policies`
- `relay_listeners`
- `desired_version`
- `version_package`
- 其他 runtime 所需配置

### 5.3 config-store

负责本地持久化：

- 当前生效配置
- staging 配置
- 当前 revision
- 当前版本与上一个稳定版本
- 证书与信任材料
- relay runtime 配置
- 自动更新元数据

### 5.4 proxy-engine

负责：

- HTTP reverse proxy
- 基于 `frontend_url` 的路由
- TLS 终止
- WebSocket / Upgrade
- 请求头改写
- `proxy_redirect` 的产品行为兼容
- 将需要 relay 的 HTTP 请求转交 relay runtime

### 5.5 l4-engine

负责：

- L4 TCP / UDP 监听
- TCP 直连转发
- UDP 直连转发
- TCP relay 转发
- 基本负载均衡
- 基础超时与连接限制

### 5.6 relay-runtime

负责：

- RelayListener 监听
- 下一跳 dial
- 多跳 relay chain
- hop 级 TLS
- pin / CA 校验
- 最后一跳回源

### 5.7 cert-manager

负责：

- ACME 签发
- 自签名证书生成
- 手动导入证书装载
- pin / fingerprint 计算
- trust store 构建
- 证书热更新

### 5.8 updater

负责：

- 比对 `desired_version`
- 下载对应平台包
- 校验 hash / 签名
- 原子替换
- 重启与回滚

## 6. 本地节点模型

本地节点不再由 `panel/backend` 内嵌 apply。

改为：

- master 机器上额外运行一个 Go local agent
- Go local agent 与远端 agent 使用相同 heartbeat 协议
- `panel/backend` 仍将其视为 agent，只是 `is_local=true`

这样可以统一：

- revision
- apply
- relay
- 自动更新
- 证书签发与装载

本地节点与远端节点的差异仅保留在：

- 部署位置
- 默认注册方式
- 可选默认能力标签

不再保留独立的 Node 本地执行逻辑。

## 7. Relay 架构

### 7.1 核心对象

Relay 仍采用：

- 全局 `RelayListener`
- 规则直接引用 `relay_chain`

### 7.2 RelayListener 语义

`RelayListener` 归属于具体 Agent，一个 Agent 可以拥有多个 listener。

建议 `RelayListener.id` 全局唯一，避免多 agent 链接引用时产生局部 ID 歧义。

### 7.3 规则语义

HTTP：

- 保持现有 `backend_url`
- `relay_chain` 为空时直接回源
- `relay_chain` 非空时先进入 relay runtime，再由最后一跳访问 `backend_url`

L4：

- `tcp` 与 `udp` 都支持直连
- 仅 `tcp` 支持 `relay_chain`
- `relay_chain` 为空时直接访问最终 upstream
- `relay_chain` 非空时通过 relay runtime 建立多跳链

### 7.4 典型流转

HTTP：

- Client -> Go proxy engine(A)
- Go proxy engine(A) -> relay runtime(A)
- relay A -> relay B -> ... -> final hop
- final hop -> `backend_url`

L4 TCP：

- Client -> l4-engine(A)
- l4-engine(A) -> relay runtime(A)
- relay A -> relay B -> ... -> final hop
- final hop -> upstream

L4 UDP：

- Client -> l4-engine(A)
- l4-engine(A) -> upstream
- 第一版不支持 UDP relay

客户端无感知。

## 8. Go Proxy Engine 设计

### 8.1 目标

目标不是重写一个通用 Nginx，而是实现本项目实际需要的入口代理能力。

### 8.2 第一版必须支持

1. Host/path 路由
2. HTTP reverse proxy
3. TLS 终止
4. WebSocket / Upgrade
5. 现有请求头透传与自定义 header 行为
6. `User-Agent` 改写
7. `proxy_redirect` 的现有产品语义尽量兼容
8. relay chain 集成
9. 证书热加载
10. 基本访问日志与健康状态

### 8.3 第一版不追求

1. Nginx 指令级兼容
2. 通用 rewrite 生态
3. 静态站点能力
4. Nginx 所有边缘优化特性
5. HTTP/3 与与之相关的特定特性

### 8.4 proxy_redirect 处理原则

不追求复制 Nginx `proxy_redirect` 指令实现方式。

改为在 Go 中直接实现产品行为：

- 识别后端返回的 `Location`
- 按当前入口 URL、Host、Scheme、Port 改写
- 尽量维持当前用户可感知行为一致

兼容的是行为，不是 Nginx 指令本身。

## 9. L4 Engine 设计

### 9.1 保留的能力

1. TCP listen
2. UDP listen
3. TCP 直连上游
4. UDP 直连上游
5. 多 backend
6. 基本负载均衡
7. connect timeout
8. idle timeout
9. TCP relay chain
10. 基本连接限制
11. 可选 PROXY protocol

### 9.2 去掉的能力

1. 只为 Nginx `stream` 指令存在的复杂 tuning
2. 平台相关、跨平台不稳定的 socket 调优项
3. UDP relay
4. 当前不再需要的 L4 特有 Nginx 配置字段

这意味着前后端 L4 数据模型需要收缩，以 Go 可稳定实现的能力集为准：保留 UDP 直连，去掉 UDP relay。

## 10. TLS、Pin 与证书模型

### 10.1 总体原则

relay 采用逐跳 TLS。

每个 hop 独立完成：

- 建链
- 证书校验
- pin / CA 信任判断

### 10.2 支持的信任模式

- `pin_only`
- `ca_only`
- `pin_or_ca`
- `pin_and_ca`

默认推荐：`pin_or_ca`

### 10.3 pin 模型

pin 使用集合而不是单值，便于轮换。

建议支持：

- 证书指纹
- SPKI / public key pin

### 10.4 证书用途

现有证书对象扩展用途：

- `edge`
- `relay_tunnel`
- `relay_ca`

### 10.5 证书来源

Go cert-manager 统一支持：

- ACME 自动签发
- 手动上传 cert/key
- 自签名证书

不再把 `acme.sh` 作为长期依赖。

## 11. 数据模型扩展

### 11.1 RelayListener

建议新增持久化对象：

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
  "tags": ["relay"],
  "revision": 3
}
```

### 11.2 Rule 扩展

HTTP 规则新增：

```json
{
  "relay_chain": [101, 204, 309]
}
```

### 11.3 L4Rule 扩展

L4 规则新增：

```json
{
  "relay_chain": [101, 204]
}
```

并约束：

- 仅 `protocol=tcp` 可非空

### 11.4 Agent 扩展

agent 状态需扩展：

- `version`
- `platform`
- `desired_version`
- `last_update_status`
- `last_update_message`

## 12. API 扩展

### 12.1 RelayListener 接口

- `GET /api/agents/:agentId/relay-listeners`
- `POST /api/agents/:agentId/relay-listeners`
- `PUT /api/agents/:agentId/relay-listeners/:id`
- `DELETE /api/agents/:agentId/relay-listeners/:id`

### 12.2 规则接口扩展

HTTP / L4 规则接口扩展字段：

- `relay_chain`

### 12.3 证书接口扩展

证书管理增加：

- 用途
- 签发模式
- pin / fingerprint 展示
- 是否可作为 relay CA

### 12.4 版本接口

控制面需新增版本管理能力，用于维护：

- 可用版本清单
- 平台包下载地址
- sha256 / 签名
- agent 或 agent 组的 `desired_version`

## 13. Heartbeat 与同步协议扩展

heartbeat `sync` 除现有字段外，扩展：

- `relay_listeners`
- `desired_version`
- `version_package`
- `version_sha256`
- `min_supported_version`

Agent 上报扩展：

- 当前版本
- 当前平台
- 更新状态
- relay / proxy / l4 引擎健康状态

## 14. 自动更新设计

### 14.1 控制面策略

采用 Master 指定目标版本：

- 每个 agent 或 agent 组有 `desired_version`
- agent 不自行追“最新”

### 14.2 agent 升级流程

1. heartbeat 发现当前版本落后
2. 在安全点执行升级，而不是在 apply 中途切换
3. 下载平台包
4. 校验 hash / 签名
5. 写入待切换元数据
6. 重启并切换版本
7. 启动成功后上报
8. 失败则回滚

### 14.3 Windows 特殊性

Windows 上不能直接覆盖正在运行的 exe，因此需要：

- updater helper 或等价外部更新进程
- 负责停进程、替换文件、拉起新版本

Linux 与 Windows 在升级流程概念上统一，但平台适配层不同。

## 15. 校验规则

### 15.1 RelayListener

1. `listen_host` 必须合法
2. `listen_port` 必须合法
3. 同一 agent 下监听地址端口不能冲突
4. `certificate_id` 必须存在且支持 `relay_tunnel`
5. `pin_set` 与 `trusted_ca_certificate_ids` 不能同时为空
6. `tls_mode` 必须是受支持枚举

### 15.2 relay_chain

1. 所有 listener 必须存在
2. 所有 listener 必须属于已注册 agent
3. 所有 listener 必须启用
4. 同一 listener 不允许在同一 chain 中重复出现
5. L4 非 TCP 时不允许使用

### 15.3 版本策略

1. `desired_version` 必须能映射到当前平台可下载包
2. 未知版本不能下发
3. 低于 `min_supported_version` 的 agent 要在控制面可见

## 16. 错误处理与可观测性

### 16.1 控制面错误

保存配置时直接拦截：

- 非法 relay_chain
- 非法证书用途
- 非法升级目标
- 非 TCP 的 L4 relay

### 16.2 运行时错误

至少应支持：

- `hop 1 tls pin mismatch`
- `hop 2 ca verify failed`
- `hop 2 dial timeout`
- `exit backend dial failed`
- `proxy bind failed`
- `certificate load failed`
- `update verify failed`
- `rollback executed`

### 16.3 状态上报

建议上报：

- proxy engine 状态
- l4 engine 状态
- relay listener 状态
- 最近成功时间
- 最近错误
- 当前 revision
- 当前 version
- 最近更新状态

## 17. 测试设计

### 17.1 后端测试

至少覆盖：

1. RelayListener normalize / validate
2. Rule / L4Rule 的 `relay_chain` 校验
3. 证书用途校验
4. 版本策略校验
5. revision 增长逻辑
6. 存储 roundtrip

### 17.2 Go agent 单元测试

至少覆盖：

1. heartbeat 同步
2. config-store 切换
3. HTTP 代理与头处理
4. `proxy_redirect` 行为
5. L4 engine
6. relay 单跳 / 多跳
7. pin / CA 校验
8. 证书热更新
9. updater 下载、校验、回滚

### 17.3 跨平台测试

至少覆盖：

1. Linux remote agent
2. Linux local agent
3. Windows remote agent
4. Windows 自动更新
5. 混合 relay chain

## 18. 实施顺序

### 阶段 1：控制面扩展

- RelayListener 模型
- 证书用途扩展
- agent 版本字段
- 版本管理接口
- 前端表单扩展

### 阶段 2：Go agent 基础框架

- heartbeat pull
- config-store
- local agent 模型
- 基础 apply / reload 框架

### 阶段 3：Go proxy engine / l4-engine

- HTTP reverse proxy
- TLS 终止
- WebSocket
- L4 TCP / UDP 直连
- 规则热加载

### 阶段 4：Go relay runtime

- RelayListener
- 多跳 relay
- pin / CA
- 最后一跳回源

### 阶段 5：Go cert-manager

- ACME
- 手动导入
- 自签名
- 热更新

### 阶段 6：Go updater

- desired_version
- 下载与校验
- Linux / Windows 更新流程
- 回滚

## 19. 最终结论

本次设计的最终推荐方案是：

1. 控制面继续保留 Node/Vue。
2. 执行面统一迁移到 Go。
3. master 本地节点也改为独立 Go local agent。
4. 所有 agent 继续使用 heartbeat pull 协议。
5. relay 采用全局 RelayListener + 规则直接引用 listener 链。
6. Go proxy engine 取代长期 Nginx 依赖。
7. L4 保留跨平台可稳定实现的 TCP / UDP 直连能力，但 relay 仅支持 TCP。
8. 证书签发与热更新也迁移到 Go。
9. agent 自动更新采用 Master 指定 `desired_version` 的模型。
10. Windows 支持、relay、多跳、本地节点、自动更新统一在一套 Go 执行面内完成。
