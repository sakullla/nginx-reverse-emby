# Relay TLS 与统一证书自动信任设计

- 日期：2026-04-07
- 状态：已确认设计，待进入实现计划
- 范围：`panel/frontend`、`panel/backend`、`go-agent`、现有 Relay/证书模型的兼容式演进

## 1. 背景

当前 Relay TLS 配置存在三个问题：

1. 普通用户需要理解监听器证书、Pin、CA、TLS 模式，心智负担过高。
2. `A -> Relay A -> Relay B -> B` 这类链路里，第一跳可能来自 HTTP 规则入口或 TCP L4 入口，而不是 RelayListener，本质上不适合继续要求用户手工维护每一跳的 Pin。
3. Relay 内部链路缺少“系统默认可信”的证书来源，导致默认路径无法做到开箱即用。

同时，统一证书页面仍需要保留手动上传 PEM 的能力，用于兼容高级或非默认场景。

## 2. 目标

### 2.1 主要目标

1. 让 RelayListener 默认可在不理解 Pin/CA 细节的前提下创建并投入使用。
2. 引入控制面全局唯一的 `Relay CA`，作为 Relay 内部链路的默认信任根。
3. 让规则入口到第一跳、以及 Relay 与下一跳之间的信任，默认从目标监听器证书自动派生，而不是要求用户手工维护 Pin。
4. 保留高级 TLS 覆盖能力，不破坏已有高级用户场景。
5. 保留统一证书页面的手动上传 PEM 能力。

### 2.2 非目标

本次不引入：

- mTLS / 客户端证书校验
- PFX / PKCS#12 上传
- agent 本地签发任何内部/自签证书
- 大规模重构证书存储模型
- 为规则新增“第一跳信任材料”手工编辑字段

## 3. 现状语义澄清

### 3.1 入口与 Relay 的角色

在链路 `A -> Relay A -> Relay B -> B` 中：

- `A` 可以是 HTTP 规则入口，也可以是 TCP L4 入口
- `Relay A` / `Relay B` 是 `relay_chain` 中引用的 RelayListener
- 最后一跳 `B` 是最终 `backend_url` 或 L4 upstream

规则入口先完成路由匹配，再决定是否进入 relay runtime。

### 3.2 Relay TLS 方向

当前实现中，发起连接的一方会校验目标 RelayListener 出示的服务端证书：

- 规则入口 `A` 连接 `relay_chain[0]` 时，校验第一跳监听器证书
- `Relay A` 连接 `Relay B` 时，校验 `Relay B` 的监听器证书

当前不支持 mTLS，因此监听器不会反向校验来访方客户端证书。

### 3.3 当前字段语义

- `certificate_id`：本 RelayListener 对外出示的服务端证书
- `pin_set`：用于校验该 RelayListener 证书的 Pin 集合
- `trusted_ca_certificate_ids`：用于校验该 RelayListener 证书链的 CA 集合
- `allow_self_signed`：允许信任该 RelayListener 使用自签/内部链路证书，但不是跳过校验
- `tls_mode`：底层验证组合策略，支持：
  - `pin_only`
  - `ca_only`
  - `pin_or_ca`
  - `pin_and_ca`

也就是说，这些 TLS 信任字段应该被理解为“如何信任这个监听器”，而不是“这个监听器如何人工维护下一跳”。

### 3.4 RelayListener 复用语义

- 同一个 RelayListener 可以被多个 HTTP/TCP 规则复用
- RelayListener 是可复用的 tunnel endpoint，不是规则路由器
- 多个规则引用同一个 listener 时，都会使用该 listener 的证书与信任配置

这意味着监听器证书和其自动派生出的信任配置，应被视为 listener 级资产，而不是规则私有资产。

## 4. 总体方案

采用“全局 Relay CA + 默认自动签发 + 自动派生 CA/Pin + 高级覆盖”的方案。

### 4.1 全局 Relay CA

- 控制面维护一套全局唯一的 `Relay CA`
- 控制面启动时检查该 CA；若不存在则自动初始化
- 所有 agent 共享并信任这套 CA
- agent 不能本地签发任何内部/自签证书；所有默认 Relay 证书均由控制面签发并下发

### 4.2 RelayListener 默认证书

- 新建 RelayListener 时，默认不要求用户手工选择已有证书
- 系统默认以“自动签发（Relay CA）”方式为该 listener 创建 `relay_tunnel` 证书
- 用户仍可通过高级路径改为绑定已有证书或手动上传证书

### 4.3 自动信任派生

当一个 caller 连接目标 RelayListener 时，默认信任材料从目标 listener 的证书自动派生：

- 自动提取目标监听器证书的 SPKI Pin，写入 `pin_set`
- 若证书链对应受管 `Relay CA`，自动写入 `trusted_ca_certificate_ids`
- 默认实际策略为 `pin_and_ca`

该规则同时适用于：

- 规则入口到第一跳 `relay_chain[0]`
- Relay 到下一跳 Relay

### 4.4 高级覆盖

高级用户仍可对单个 RelayListener 显式覆盖默认信任配置：

- 修改 `tls_mode`
- 手工编辑 `pin_set`
- 手工编辑 `trusted_ca_certificate_ids`
- 控制 `allow_self_signed`

一旦某个 listener 使用高级覆盖，其被所有规则/所有上游 caller 访问时都应使用该 listener 的覆盖后的信任配置。

## 5. 统一证书设计

## 5.1 页面定位

统一证书继续作为全局证书中心，负责：

- 展示系统证书与业务证书
- 新建不同用途证书
- 编辑证书元信息
- 手动上传 PEM 材料

## 5.2 系统管理证书

统一证书列表需要显式展示一项系统管理对象：

- 全局 `Relay CA`

该对象：

- 由控制面自动创建或恢复
- 默认不可删除
- 不依赖 agent 本地生成
- 可查看元信息与状态
- 可在后续实现中支持轮换，但不作为第一版用户流程重点

## 5.3 新建流程：用途模板

用户点击“新建证书”后，默认看到以下用途模板：

1. 网站 HTTPS
2. Relay 监听证书
3. 手动上传证书
4. 内部证书（高级）

不再把“Relay CA 证书”作为普通用户手工创建模板，因为系统已经维护全局 Relay CA。

## 5.4 模板默认映射

### 网站 HTTPS

- `usage=https`
- 默认 `certificate_type=acme`
- 默认按现有签发能力展示签发方式

### Relay 监听证书

- `usage=relay_tunnel`
- 默认 `certificate_type=managed`
- 默认由控制面使用全局 `Relay CA` 自动签发
- 不要求用户先上传 PEM

### 手动上传证书

- `certificate_type=uploaded`
- 直接进入 PEM 粘贴表单

### 内部证书（高级）

- 默认由控制面使用全局 `Relay CA` 签发
- 不表示“每个 agent 本地自签”
- 主要用于内网或高级场景下的内部服务证书

## 5.5 手动上传入口

当证书来源为“手动上传”时，显示 PEM 输入区。

最小可用版本支持文本粘贴：

- 证书链 PEM
- 私钥 PEM
- CA PEM（可选）

第一版不要求必须提供本地文件上传控件；如果前端额外提供“读取文件到文本框”的辅助能力，应仍以 PEM 文本为最终提交内容。

## 5.6 手动上传验证规则

### 服务端证书

- 证书链 PEM 必填
- 私钥 PEM 必填
- 证书与私钥必须匹配

### CA 证书

- CA PEM 必填
- 第一版不要求导入可签发私钥，除非后续扩展到自定义内部 CA

### 通用规则

- PEM 格式必须合法
- 非法内容返回明确错误信息

## 6. RelayListener 交互设计

## 6.1 默认表单字段

普通用户默认看到：

- 名称
- 监听地址
- 监听端口
- 监听证书来源
- 信任策略
- 启用监听器

默认不显示：

- TLS 模式
- Pin Set
- 可信 CA 证书
- 允许自签名
- 证书高级来源切换细节

## 6.2 默认文案

### 监听证书来源

默认显示为：**自动签发（Relay CA）**

### 信任策略

默认显示为：**自动（Relay CA + Pin）**

### 高级文案

- `Pin Set`：**用于校验该监听器证书的指纹（Pin）**
- `可信 CA 证书`：**用于校验该监听器证书链的 CA**
- `允许自签名`：**允许信任该监听器使用的内部/自签链路证书**

## 6.3 默认创建流程

推荐路径：

1. 用户新建 RelayListener
2. 填写名称、地址、端口
3. 保持“监听证书来源 = 自动签发（Relay CA）”
4. 保持“信任策略 = 自动（Relay CA + Pin）”
5. 保存
6. 控制面自动签发 listener 证书并绑定 `certificate_id`
7. 控制面自动派生 `pin_set`、`trusted_ca_certificate_ids`、`tls_mode`

普通用户不需要先进入统一证书手工准备材料。

## 6.4 自动模式的推导规则

自动模式不再依赖用户手工填写 Pin/CA，而是根据目标 listener 证书自动推导：

- 能同时得到 Relay CA 与 SPKI Pin -> `pin_and_ca`
- 只能得到 Pin -> `pin_only`
- 只能得到 CA -> `ca_only`
- 两者都无法得到 -> 保存报错

对于系统默认的 RelayListener：

- 应始终至少得到 Relay CA
- 默认也应提取 SPKI Pin
- 因此推荐默认落为 `pin_and_ca`

`pin_or_ca` 仍保留在高级设置中，仅供兼容或排障场景使用。

## 6.5 高级设置

展开后显示：

- 监听证书来源切换
- TLS 模式（四种全部显示）
- Pin Set
- 可信 CA 证书
- 允许自签名

高级设置用于：

- 兼容旧配置
- 显式选择 `pin_or_ca`
- 手工绑定已有证书或上传证书
- 精细排障
- 资深用户的安全策略控制

## 6.6 对共享 listener 的影响提示

由于同一个 RelayListener 可被多个规则复用：

- 编辑 listener 的证书或高级 TLS 设置，会影响所有引用它的规则
- 删除 listener 时，若仍被规则引用，必须阻止删除并明确提示引用关系

## 7. 数据与运行时映射

## 7.1 Rule / L4Rule

规则模型保持不变：

- HTTP 规则继续只保存 `relay_chain`
- TCP L4 规则继续只保存 `relay_chain`
- 不新增“第一跳信任材料”手工编辑字段

## 7.2 RelayListener

RelayListener 继续保留现有 TLS 字段：

- `certificate_id`
- `tls_mode`
- `pin_set`
- `trusted_ca_certificate_ids`
- `allow_self_signed`

但默认来源改为：

- `certificate_id`：控制面自动签发或用户显式绑定
- `pin_set` / `trusted_ca_certificate_ids` / `tls_mode`：由该 listener 自身证书自动派生出的默认信任配置

换言之，这些字段在默认路径下是该 listener 的“发布式信任资料”，供所有上游 caller 在连接该 listener 时使用。

## 7.3 自动与覆盖

实现层应区分两类状态：

- 自动派生值
- 用户显式覆盖值

具体实现可以：

- 新增元字段标记 `auto` / `custom`
- 或在服务端内部维护来源标记

但无论采用哪种实现，都不能破坏现有运行时字段对 agent 的兼容性。

## 7.4 运行时派生位置

推荐在控制面同步/装配阶段生成“每一跳实际使用的 TLS 信任上下文”：

- 规则入口到第一跳的信任，从 `relay_chain[0]` 对应 listener 的证书自动派生
- Relay 到下一跳的信任，从目标 listener 的证书自动派生

这样可以自然覆盖：

- HTTP 规则入口
- TCP L4 规则入口
- 多跳 Relay

而无需在规则模型中引入额外的手工 TLS 字段。

## 7.5 兼容已有记录

已有证书和 RelayListener 记录必须可继续编辑：

- 旧记录进入新 UI 时，应根据现有底层字段反推默认显示状态
- 若记录使用 `pin_and_ca`，前端必须正确显示
- 若记录使用 `pin_or_ca`，前端应进入高级模式并明确标注
- 旧记录若本来就是手工 Pin/CA，默认视为自定义覆盖，而不是强行改写为自动派生

## 8. API / 后端 / agent 设计要求

## 8.1 控制面启动行为

控制面启动时需要：

- 检查全局 `Relay CA` 是否存在
- 若不存在，则自动初始化
- 将其作为系统管理证书纳入统一证书视图

## 8.2 证书签发职责

- 控制面负责签发默认 RelayListener 证书
- agent 只接收成品证书与信任材料
- agent 不负责签发任何内部/自签证书

## 8.3 统一证书接口

后端需要继续支持手动上传 PEM 所需的数据接收与验证逻辑。

建议最小字段：

- `certificate_pem`
- `private_key_pem`
- `ca_pem`

字段命名可在实现阶段最终确定，但应避免和现有证书元字段冲突。

## 8.4 RelayListener 接口

后端继续接受现有 payload，不要求破坏式修改接口。

但在默认路径下，服务端需要支持：

- 创建 listener 时自动申请/绑定 RelayListener 证书
- 基于 listener 证书自动生成默认 `pin_set`
- 基于证书链自动填充 `trusted_ca_certificate_ids`
- 默认写入 `tls_mode=pin_and_ca`（若材料完整）

## 8.5 同步与运行时

agent 运行时需要能够：

- 使用 listener 的服务端证书对外提供 TLS
- 使用目标 listener 的自动派生信任资料完成 hop 校验
- 同时兼容手工覆盖后的 Pin/CA 模式

## 8.6 验证补齐

前后端需保持一致：

- 启用的 RelayListener 必须绑定证书
- `pin_and_ca` 必须同时具备 Pin 与 CA
- `pin_only` 必须有 Pin
- `ca_only` 必须有 CA
- 自动模式下不得出现“无法从监听证书派生任何信任材料”的情况

## 9. 用户流程

## 9.1 创建 RelayListener（推荐路径）

1. 用户新建 RelayListener
2. 填写名称、地址、端口
3. 保持“监听证书来源 = 自动签发（Relay CA）”
4. 保持“信任策略 = 自动（Relay CA + Pin）”
5. 保存
6. 控制面自动签发证书并完成 listener 绑定
7. 该 listener 可直接被加入 `relay_chain`

## 9.2 创建带 Relay 的 HTTP/TCP 规则

1. 用户新建或编辑 HTTP/TCP 规则
2. 选择 `relay_chain`
3. 无需手工填写第一跳 Pin/CA
4. 系统根据链路中每个目标 listener 的证书自动建立信任

## 9.3 高级路径

用户仍可：

- 手工上传证书后绑定到 listener
- 展开高级设置覆盖自动 TLS 模式
- 手工编辑 Pin / CA

## 10. 风险与处理

### 10.1 风险：自动派生与旧语义混淆

处理：

- 明确说明默认信任来自“目标 listener 证书”
- 对旧记录以高级模式回显
- 对自动/自定义状态做清晰标记

### 10.2 风险：共享 listener 修改影响多个规则

处理：

- 在编辑和删除时显示影响提示
- 删除前阻止被引用 listener 被误删

### 10.3 风险：Relay CA 初始化失败

处理：

- 阻止默认 RelayListener 创建
- 明确展示错误，不静默降级为“无校验”

### 10.4 风险：证书轮换导致 Pin 变化

处理：

- 由控制面在证书轮换后自动重新派生 SPKI Pin
- 自动模式下相关 hop 的信任资料同步更新

## 11. 实现边界建议

本设计建议拆分为以下实现块：

1. 控制面启动：确保全局 Relay CA 存在
2. 统一证书：系统管理 Relay CA 展示与用途模板调整
3. RelayListener：默认自动签发监听证书
4. RelayListener：自动派生 `pin_set` / `trusted_ca_certificate_ids` / `tls_mode`
5. 规则与 relay runtime：第一跳及多跳自动信任装配
6. 高级设置：兼容旧数据与覆盖自动模式
7. 手动上传 PEM：继续支持高级场景

## 12. 验收标准

### 12.1 普通用户

- 不理解 `tls_mode` 也能创建可工作的 RelayListener
- 不需要手工上传证书也能完成默认 Relay 配置
- 不需要手工维护第一跳或多跳 Pin/CA
- HTTP/TCP 规则在选择 `relay_chain` 后即可使用自动信任

### 12.2 高级用户

- 可以继续编辑 `pin_only` / `ca_only` / `pin_or_ca` / `pin_and_ca`
- 可以改用手工上传证书或已有证书
- 不会因为新 UI 丢失旧配置语义

### 12.3 兼容性

- 已有 RelayListener 和证书记录能够正常展示、编辑、保存
- agent 不需要新增本地签发能力
- 现有 TLS 运行时字段保持兼容
