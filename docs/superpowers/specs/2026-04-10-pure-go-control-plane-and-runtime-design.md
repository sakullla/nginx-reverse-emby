# 纯 Go 控制面与执行面替换设计

日期：2026-04-10

## 1. 背景

当前仓库的主路径运行时由三部分组成：

- 控制面：Node.js backend + Vue frontend
- 执行面：Go `nre-agent`
- 数据面与证书辅助：Nginx 模板、`acme.sh`、部分 shell 脚本

目标是将主路径完全收口为 Go 实现，同时保留现有 Vue UI 和现有 API 边界，不要求用户迁移已有控制面数据，不要求正式主路径继续依赖 Node、Nginx 或 `acme.sh`。

## 2. 本次设计确认的约束

### 2.1 交付目标

- 目标是一次性切换到纯 Go 主路径。
- 不保留 Node、Nginx、`acme.sh` 作为默认并行运行路径。

### 2.2 兼容性目标

- 保留现有 Vue 前端。
- 保留现有 `/panel-api/*` 与 `/agent-api/*` 的主要协议边界。
- 兼容优先级是 UI/API/数据边界，不是逐项复刻 Nginx 运行时细节。

### 2.3 部署目标

- Docker 部署启动 master 时，默认内置本地 agent 能力，用户不需要单独再启动一个 agent。
- 远端边缘节点继续作为独立 Go agent 运行。

### 2.4 数据目标

- Go 控制面直接读写现有持久化结构。
- 不要求一次性导入。
- 不允许通过“清空重建”完成切换。

### 2.5 功能范围

- 保留当前主路径能力：面板、agent 同步、HTTP 反代、L4、relay、证书管理、版本更新。
- 删除大多数 legacy Nginx 本地 apply 路径作为主路径依赖。
- `conf.d/`、仓库根目录 `nginx.conf`、`deploy.sh` 完全不动，不纳入本次改造范围，也不作为新架构入口。

### 2.6 ACME 目标

- `acme.sh` 完全移除。
- 统一由 Go + lego 负责签发与自动续期。
- 首发只覆盖当前实际在用能力：HTTP-01 和现有 Cloudflare DNS-01。

## 3. 目标架构

纯 Go 主路径由三块组成：

1. `panel/backend-go` Go control-plane server
2. master 内置 local agent runtime
3. remote `nre-agent`

对外形态如下：

- Vue SPA 继续作为唯一管理 UI。
- `panel/backend-go` 提供前端静态文件、管理 API、agent API、资产发布与状态编排。
- master 容器内部默认带本地 agent 能力，可直接承载本机 HTTP/L4/TLS/relay 运行时。
- 远端机器继续通过独立 Go agent 接受快照、执行运行时变更、完成版本更新。

不再存在以下主路径依赖：

- Node backend
- Nginx 配置模板与 Nginx 作为默认数据面
- `acme.sh`

## 4. Go 控制面设计

Go 控制面位于 `panel/backend-go/`，作为独立于 `go-agent/` 的控制面实现存在。`go-agent/` 继续只承担执行面职责。控制面内部拆为五层。

### 4.1 HTTP 层

职责：

- 暴露 `/panel-api/*` 管理接口
- 暴露 `/agent-api/*` 同步接口
- 暴露 `/panel-api/public/*` 前端与公开资产路径
- 保留现有 token 认证边界，包括 `X-Panel-Token`、register token、agent token

要求：

- Vue 前端继续使用 `/panel-api` 作为 `baseURL`
- 不要求前端大规模改造路由或部署方式

### 4.2 Compatibility Service 层

职责：

- 复刻当前 Node `server.js` 中的主路径业务语义
- 负责 payload 规范化、字段默认值、revision bump、校验逻辑、错误信息生成
- 负责规则、L4、relay listener、证书、版本策略、agent 操作的业务编排

要求：

- “兼容”落在服务层，不让 handler 直接拼接数据库行为
- 兼容目标是已有 API contract 与业务副作用

### 4.3 Storage Facade 层

职责：

- 以 `panel/backend-go` 内的 Go repository 方式替代 Node `storage.js` / `storage-sqlite.js`
- 直接读写现有主路径持久化结构
- 维护现有 revision 语义和对象边界

要求：

- 不要求继续使用 Prisma 作为运行时依赖
- 允许 Go 直接操作现有 SQLite schema
- 兼容对象至少包括 rules、l4 rules、relay listeners、managed certificates、version policies、agents、runtime state

### 4.4 Packaging 与静态资源层

职责：

- 直接提供前端静态页面
- 直接发布 agent 二进制资产
- 提供 `join-agent.sh` 和版本升级所需资产元数据

要求：

- 不再依赖 Node 或 Nginx 来发布静态资源
- 继续支持当前 agent 安装与更新入口

### 4.5 Orchestration 层

职责：

- 生成 agent 可消费的 snapshot
- 根据 agent 上报状态计算展示状态
- 处理 apply、heartbeat、desired revision、runtime state 持久化

要求：

- 控制面保持声明式期望状态模型
- 控制面不再承担本地 Nginx apply 逻辑

## 5. 数据与同步模型

### 5.1 持久化兼容

Go 控制面不引入新的主存储格式。它围绕现有实际落盘数据实现兼容仓储层。

要求：

- 现有 SQLite 数据可直接读取与继续写入
- 现有 revision 规则保持不变
- 不依赖一次性导入或初始化脚本

### 5.2 Snapshot 兼容

Go 控制面继续下发与当前 agent 一致的 snapshot 形状：

- `desired_version`
- `desired_revision`
- `version_package`
- `rules`
- `l4_rules`
- `relay_listeners`
- `certificates`
- `certificate_policies`

要求：

- 现有 Go agent 的同步边界可被保留
- 首发不重写 agent 与控制面的核心同步协议

### 5.3 Revision 语义

所有影响 agent 期望状态的配置变更仍由统一 revision bump 驱动。

要求：

- 前端“待同步 / 已生效 / 应用失败”判断逻辑不需要重定义
- Go 控制面接管 revision 计算与持久化

### 5.4 Agent 状态模型

agent 继续通过 heartbeat/pull 方式上报：

- 当前 revision
- runtime status
- last sync error
- 当前版本

要求：

- 不改成 push orchestration
- 控制面继续基于 agent 上报状态计算页面展示与 apply 结果

### 5.5 JSON backend

JSON backend 不作为纯 Go 版本主路径目标。即使保留测试或降级用途，也不是主实现标准。

## 6. 执行面设计

`go-agent/` 升级为唯一数据面 runtime。Nginx 不再参与主路径 HTTP/L4/TLS/relay 转发。

### 6.1 HTTP Runtime

职责：

- 监听 HTTP/HTTPS
- 基于域名与规则匹配做反向代理
- 处理基础请求头转发策略
- 处理基础负载均衡
- 与证书选择联动

要求：

- 兼容规则字段与 API 语义
- 不承诺逐项复刻 Nginx 指令行为

### 6.2 L4 Runtime

职责：

- 承载 TCP/UDP 规则
- 与 snapshot apply 生命周期对齐
- 监听器与 backend 连接逻辑独立于 HTTP runtime

### 6.3 Relay Runtime

职责：

- 处理 relay listeners
- 处理 relay chain
- 处理 relay 隧道 TLS 与信任策略

要求：

- 继续沿用当前仓库已有的 Go model 与主数据结构
- 将 relay 从附属功能提升为纯 Go 数据面的一级能力

### 6.4 TLS 与证书绑定

职责：

- 直接加载本地证书材料
- 构建 SNI 选择表
- 在运行时热切换监听器与证书

要求：

- 证书不再通过 Nginx 模板消费
- 证书成为 runtime 的直接输入

### 6.5 Master 内置 Local Agent

要求：

- Docker 启动 master 时默认具有本地 agent 能力
- 用户不需要单独再启动一个独立 agent
- 远端节点继续以独立 Go agent 存在

实现边界：

- 对用户表现为“master 开箱即带执行能力”
- 内部可实现为同一 Go 进程中的 local runtime 模块，或同容器托管子组件

## 7. 证书子系统设计

`acme.sh` 完全退出，由 Go 内部 lego 证书服务统一负责签发和自动续期。

### 7.1 证书策略层

职责：

- 继续保存现有证书记录、签发模式、目标 agent、用途、状态字段与错误信息
- 保持前端现有的创建、更新、手动触发签发、查看状态流程

要求：

- 证书对象形状尽量保持兼容
- 首发不引入新的证书 UI 模型

### 7.2 签发与续期执行器

职责：

- 统一由 lego 执行签发
- 统一由 lego 执行自动续期
- 对不同运行位置分别处理执行责任

执行责任：

- master 内置 local agent 场景：由 master 容器内本地 runtime 完成 HTTP-01、本地证书落地与自动续期调度
- remote agent 场景：由 agent 根据 snapshot 中的证书 bundle 或 policy 执行本地证书应用，并在本地续期模式下负责自动续期
- Cloudflare DNS-01 集中签发场景：由 Go 控制面内部服务负责签发与自动续期

要求：

- 自动续期是内建能力，不允许只保留手动 issue
- 自动续期必须有状态回写，包括最近续期时间、失败原因、重试结果
- 不再调用 shell 或外部 ACME 脚本

### 7.3 证书材料存储

职责：

- 管理证书、公钥、account key、签发元数据
- 保持与当前已有状态兼容，确保切换后可继续识别与续期

要求：

- 目标是兼容已有证书状态与数据，不是长期依赖 `acme.sh` 目录结构
- 切换后已有证书应可被识别、继续分发、继续续期

### 7.4 首发范围

首发只覆盖：

- HTTP-01
- 当前已有 Cloudflare DNS-01

首发不做：

- 通用多 provider ACME 平台
- 新的账户管理体系
- 大范围 UI 重构

## 8. 错误处理与可观测性

### 8.1 控制面兼容错误

风险：

- payload 规范化、默认值、revision 计算与现有 Node 偏差

要求：

- 关键 API 需要兼容测试
- 需要验证请求、响应与副作用

### 8.2 执行面行为回归

风险：

- Go runtime 替代 Nginx 后，在头处理、TLS 选择、负载均衡、relay 上出现行为回归

要求：

- 不要求逐条复刻 Nginx
- 必须覆盖当前主路径使用组合

### 8.3 证书续期失效

风险：

- 首发能签发，但续期周期到来后失败

要求：

- 自动续期必须可观测
- 失败状态必须回写
- 必须有明确重试策略与日志输出

### 8.4 切换回滚困难

风险：

- 一次性切换失败时回滚成本高

要求：

- 发布前必须在真实数据副本上完整演练
- 必须保留可回滚的旧镜像或制品

## 9. 测试与发布门槛

纯 Go 切换前必须至少满足以下门槛：

1. Go control-plane 通过现有 API 兼容测试
2. Go runtime 通过 HTTP/L4/relay/TLS 主路径测试
3. lego 签发与自动续期测试通过
4. Docker master 镜像验证通过，且启动后默认具备本地 agent 能力
5. 使用现有生产数据副本完成一次真实读写与 snapshot 回放演练

## 10. 一次性切换策略

建议采用：

- single-release cutover
- prevalidated in shadow data

切换步骤要求：

1. 发布前使用生产数据副本启动 Go 版控制面与 master runtime
2. 验证 UI、API、snapshot、证书、运行时链路全部闭环
3. 正式切换时替换容器镜像与主启动路径
4. 不保留 Node/Nginx 作为默认并行主路径
5. 保留必要的旧镜像或旧制品用于回滚

## 11. 不在本次范围内

以下内容不纳入本次设计范围：

- 修改 `conf.d/`
- 修改仓库根目录 `nginx.conf`
- 修改 `deploy.sh`
- 将 `deploy.sh` 改造成新架构入口
- 将 JSON backend 作为纯 Go 主路径标准
- 首发做成通用多 provider ACME 平台
- 为兼容 Nginx 而保留新的模板生成主路径

## 12. 后续计划边界

后续 implementation plan 需要按下列子系统拆分：

1. `panel/backend-go` HTTP/API compatibility
2. Go storage facade 与现有 SQLite schema 兼容
3. master 内置 local agent runtime
4. remote agent runtime 完整替换 Nginx 数据面
5. lego 证书签发与自动续期
6. Docker 与发布链路调整
7. 兼容测试、回放测试与切换验证
