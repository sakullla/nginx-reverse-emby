# go-agent core/plugin 重构设计

## 目标

把 `go-agent` 从“单体业务编排包”重构成“单一二进制 + 清晰 core 边界 + 可选模块”的结构。

这次重构要同时满足：

1. 保持对外行为尽量兼容
2. 明确区分 core 和可选模块
3. 降低 `internal/app` 的职责密度
4. 清理重复、低价值、强耦合测试
5. 为后续体积优化和模块裁剪留下空间

## 非目标

1. 不引入外部 RPC 插件进程
2. 不拆成多个仓库
3. 不改控制面协议语义
4. 不做大规模业务逻辑重写

## 架构

推荐采用“core + 模块注册表”的单二进制方案。

这里的“模块”指同仓库内的可选功能包和显式注册接口，不指 Go 原生 `plugin` 包。必要时可以用 build tag 仅控制平台特定或重依赖实现，但它不是主扩展机制。

`core` 负责稳定主链路：配置归一化、store、sync、runtime/state、update、任务调度、snapshot diff、模块生命周期编排。

`HTTP / L4 / relay` 保持在 core 边界内，因为它们定义了 agent 的主数据流，不适合再切碎。

`WireGuard` 和 `traffic stats` 作为第一批独立模块拆出去。`certs`、`diagnostics`、`egress` 也通过同一模块接口收口，但可以分阶段迁移。

`internal/app` 退化为装配层，只负责拼接依赖，不再承载业务判断。

## 组件划分

### `internal/core`

负责：

- 启动和关闭
- 配置归一化
- capability 生成
- snapshot diff / apply / rollback 编排
- 模块注册和顺序控制
- runtime state 错误标记

### `internal/module`

只放稳定契约，不放业务实现。

建议包含：

- `Module`
- `Activator`
- `Capability`
- `Health`
- `Registry`

### `internal/modules/wireguard`

承接现有 WireGuard runtime、配置解析、激活和回收逻辑，作为第一批独立模块。

### `internal/modules/traffic`

承接流量统计采样、host traffic 合并、周期报送和节流逻辑。

### `internal/modules/certs`
### `internal/modules/diagnostics`
### `internal/modules/egress`

先以适配层形式迁移，目标是把它们从 `internal/app` 中剥离出来。

### `internal/app`

只保留 bootstrap 和依赖装配，不再包含大段条件分支和 snapshot 业务逻辑。

## 数据流

1. `sync` 取得 desired snapshot。
2. `core` 做归一化和 diff，判断需要激活哪些模块。
3. 按固定顺序触发激活：
   `certs -> agent config -> HTTP/L4/relay -> wireguard -> traffic`
4. 任何模块失败，`core` 负责记录错误、保持已知状态，并回滚到上一个可用 snapshot。
5. `store` 继续保存 desired/applied/runtime state，但不再参与业务编排。

## 错误处理

- 启动期失败直接退出，覆盖配置、存储、模块构造和必需依赖缺失
- 同步期失败只影响当前 snapshot
- 模块错误要带上 `module`、`capability`、`revision` 等上下文
- 兼容层单独处理旧 env / embedded 差异，避免污染业务模块

## 测试策略

保留的测试：

- `core` 的 snapshot diff、模块注册顺序、失败回滚、capability 生成
- `wireguard` 和 `traffic` 的关键回归测试
- `app` 的装配和兼容测试
- 最小端到端测试，验证一次完整 snapshot 从 sync 到激活再到落盘

准备收缩的测试：

- 仅重复 getter / wrapper 行为的测试
- 与实现细节强绑定但不覆盖真实边界的测试
- 同一行为在多层重复断言的测试

## 兼容性约束

以下内容尽量不变：

- 环境变量名称
- control-plane API 语义
- embedded runtime 输入输出语义
- runtime state 的核心字段

允许变化的内容：

- 内部包结构
- 模块编排位置
- 内部 helper 的拆分方式

## 分期建议

### Phase 1

抽出 `internal/core` 和 `internal/module`，把 `internal/app` 里的编排逻辑移走，先让职责边界成立；这一步只要求编排下沉，不要求一次性搬空所有业务实现。

### Phase 2

迁移 `WireGuard` 和 `traffic stats`，完成第一批真正的可选模块。

### Phase 3

把 `certs`、`diagnostics`、`egress` 继续模块化，收紧测试并清理冗余代码。

## 参考约束

- https://go.dev/doc/modules/layout
- https://pkg.go.dev/plugin
- https://docs.docker.com/build/building/multi-stage/
