# Technical Solution

```yaml
format: solution
summary: "4 页统一到共享设计系统骨架；动态表单收敛为单一渲染器——ui_schema 声明组件树与 config_schema 合成的组件树走同一条渲染路径，支持嵌套对象/数组增删重排/JSON 谓词条件显示/约束校验提示；schema_version 保持 1 原地扩展词表；秘密字段与动态动作语义不变"
```

## 目标 / 非目标

**目标**
- 4 个插件页（已安装 / 配置详情 / 市场 / 仓库源）统一到非插件页共享骨架与设计令牌（R1）。
- 动态表单收敛为单一渲染入口，可编辑任意合法 `config_schema`（嵌套对象 / 数组）并保存生成 revision（R2）。
- 新增能力：嵌套对象渲染、数组可重复分组（增删 / 重排）、条件显示、校验提示（R2）。
- `schema_version` 保持 1，声明式 UI 词表原地扩展，无版本迁移、无版本号提升（R4）。
- 安全边界不变：前端只渲染宿主内置组件，新增词表 / 条件表达式纳入后端白名单与预算校验（R3）。

**非目标（不做）**
- 不改插件运行时 / 执行面（go-agent、wasm / process 宿主）、不改插件业务存储 / 校验语义（R5）。
- 不动非插件页面及其路由 / 行为 / 视觉（R6）。
- 不引入第三方 UI 库；不引入 `schema_version` 2 或任何版本协商分支。

## Requirement 覆盖

| 需求 | 方案落点 |
|---|---|
| R1 页面全新设计 | 4 页重构，统一骨架 / 确认弹窗 / 加载错空态 / 按钮层级 / 导航 |
| R2 动态表单升级 | 单一渲染器 + 嵌套 / 数组 / 条件显示 / 校验提示 + 无 ui_schema 兜底 |
| R3 安全边界不变 | 后端词表白名单 / 绑定校验 / 条件表达式白名单 + 预算；前端仅宿主内置组件 |
| R4 契约与兼容 | schema_version 保持 1；动态动作 / 秘密字段语义不变；测试全绿 |
| R5 不动执行面 | 仅改声明式 UI 契约校验与前端渲染，不触运行时 |
| R6 不动非插件页 | 改动限定 `pages/plugins/*`、`components/plugins/*`、`api/pluginSecurity.js`、后端 `plugins/declarative_ui.go` / `types.go` 及其测试 |

## 架构决策

### D1 页面骨架统一（R1）

4 页全部改为非插件页的规范骨架（exploration §4 已定位）：`page-header`（标题 / 副标题 + 右侧 `btn btn-primary` 主操作）→ `OperationStatusList` → `ResourceListFilterBar` + `ViewToggle` → 卡片网格或表格 → `.spinner` / `EmptyState` 空错态 → `BaseModal`（建 / 编）+ `DeleteConfirmDialog`（破坏性确认）。

- 确认弹窗统一：删除 `window.confirm`（`PluginDetailPage.vue:69`）与手写 `role="alertdialog"`（`PluginRepositoriesPage.vue:157-166`）与市场行内 checkbox 门控，全部走 `DeleteConfirmDialog`（破坏性）或新增的通用确认态（非破坏性）。
- 加载 / 错误 / 空态统一：裸 `<p>` 替换为 `.spinner` / `EmptyState`，与仓库页较结构化状态对齐。
- 按钮层级：详情页主操作明确为 `btn btn-primary`，卸载 / 删除为 `btn btn-danger`（`utilities.css` 已提供变体）。
- 实例切换：原生 `<select>`（`PluginDetailPage.vue:179-181`）替换为 `BaseTabs` / `BaseActionMenu`。
- 已安装列表：卡片网格补 `ResourceListFilterBar` 筛选 / 搜索，暴露生命周期 / agent 状态 / 风险事实；卡片复用 `BaseListCard` / `BaseCard`。
- 导航一致：市场 / 仓库页补返回链接，与详情页对齐。

### D2 动态表单收敛为单一渲染器（R2）

现有两套表单（`PluginConfigForm` 扁平 + `PluginDeclarativeUI` 声明式）收敛为一条渲染路径：

- `PluginDeclarativeUI` 成为唯一渲染器，输入是「组件树」。
- 组件树有两个来源：
  1. **声明**：插件提供 `ui.schema.json` 时，经后端校验后的组件列表。
  2. **合成**：无 `ui_schema` 时，纯函数 `schemaToUIComponents(configSchema)` 从 `config_schema` 递归生成组件树（对象 → `section` 分组、标量 → 对应组件、数组 → `array` 可重复分组）。
- 两者走同一渲染器，视觉语言唯一；`PluginConfigForm` 的扁平 `normalizePluginConfigSchema` 角色由递归 schema 遍历取代，扁平路径删除，不留过渡层。
- 秘密字段复用 `SecretWriteOnlyField`，JSON pointer 支持嵌套路径（如 `/nested/secret`），「留空保留 / 清除 / 轮换」语义经 `secret_replacements` 维持不变。

### D3 嵌套对象与数组（R2）

- **对象**：由 `config_schema.properties` 递归驱动；ui_schema 用 `section`（已存在）分组，叶子组件 `binding` 为规范 JSON pointer，指向任意深度已声明属性（后端 `binding()` 已支持 1-8 段）。
- **数组**：新增 `array` 组件类型（可重复分组），`items` 取自 `config_schema.items`；前端提供新增 / 删除 / 重排（上移 / 下移）操作，提交时组装为 `config` 中的 JSON 数组。
- 后端 `config_schema` 已支持嵌套对象 / 数组（`config.go ValidateConfig` + `validator.go` 关键字白名单），本次后端只扩展 `ui_schema` 词表以描述数组容器，不改 `config_schema` 校验。

### D4 条件显示（R2/R3）

采用 **JSON 谓词**，不做字符串求值：

```yaml
- binding: /enableTls
  type: toggle
  visibleWhen:
    field: /mode            # 规范 JSON pointer，指向 config_schema 已声明属性
    op: eq                  # 白名单：eq/neq/in/notIn/empty/notEmpty/gt/gte/lt/lte
    value: "advanced"       # 值类型须与 field 声明的类型一致
```

- 后端校验：`field` 走既有 `binding()` 解析（指向已声明属性）；`op` 白名单；`value` 类型与 `field` 类型对应；单组件条件数上限与总量预算纳入既有预算机制。
- 前端：条件为假时整块隐藏，不参与提交（隐藏字段值不在 `config` 中，避免发送旧值）。
- 安全：无 `eval` / 无字符串脚本，谓词为纯数据，后端拒绝非法结构（R3）。

### D5 校验提示（R2）

- 前端在叶子字段渲染 `config_schema` 已有约束关键字（`minimum`/`maximum`/`minLength`/`maxLength`/`pattern`/`minItems`/`maxItems`/`uniqueItems`/`enum`/`required`）作为提示文案，输入越界时给出行内错误。
- 客户端校验在 change 与 submit 两处触发；后端 `ValidateConfig` 仍是最终权威，提交失败时错误透出到表单顶部（不吞错）。
- 不新增后端校验关键字（关键字已在 `validator.go` 白名单内）；仅补前端展示与本地校验。

### D6 后端词表原地扩展（R4）

- `types.go:21` `DeclarativeUISchemaVersion` 保持 1；`declarative_ui.go:271-273` 继续拒绝非 1。
- 词表扩展（`types.go:23-31` 组件常量、`declarative_ui.go` 白名单 / 预算 / `decodeStrictUIObject` 允许字段表）：新增 `array` 组件、`visibleWhen` 字段及谓词 `op` 白名单；新增项全部纳入既有严格解码 / 绑定 / 预算 / 文本脱敏校验。
- `declarative_ui_test.go:195` 的 `schema_version=2` 拒绝用例保持不变（仍合法有效）。

### D7 直接使用点与衔接点

- 前端新增 / 复用：`schemaToUIComponents`（新）、`conditional` 谓词解析（新）、`validation` 提示渲染（新）；复用 `SecretWriteOnlyField`、`base/*` 组件、`themes.css` 令牌、`.btn`/`.page-header`/`.spinner`/`.form-group`/`.input-base` 工具类。
- 后端扩展：`declarative_ui.go`（词表 / `visibleWhen` / 预算）、`types.go`（组件常量）；复用 `binding()`、`decodeStrictUIObject`、`text()` 脱敏、`ValidateConfig`。

## 事实 owner 与状态变化

- 设计令牌唯一 owner：`panel/frontend/src/styles/themes.css`；`.btn` 等工具类唯一 owner：`styles/utilities.css`（页面不再写内联样式 / 手写卡片）。
- `config_schema` 校验唯一 owner：后端 `config.go ValidateConfig` + `validator.go` 关键字白名单；前端不重复实现 schema 校验，只做展示 / 本地提示。
- 声明式 UI 契约校验唯一 owner：后端 `declarative_ui.go`；前端只消费后端已校验的组件树，不自行放宽。
- 前端字段归一 / 组件树合成唯一 owner：`schemaToUIComponents` + `api/pluginSecurity.js`（旧的扁平 `normalizePluginConfigSchema` 删除或重写为递归，二选一，不留双 owner）。
- 秘密字段语义唯一 owner：`SecretWriteOnlyField` + 提交层 `secret_replacements`；声明式与合成路径共用，不各写一份。

## 关键失败行为

- 非法声明式 UI（注入 / 越界绑定 / 绑定矛盾 / 非法谓词 / 超预算）→ 后端拒绝，前端不渲染任何插件提供的代码（R3）。
- 无 `ui_schema` 且 `config_schema` 含嵌套 / 数组 → 合成组件树可完整编辑；若合成遇到不支持关键字（未知类型）→ 该字段以只读文本展示而非静默丢弃，保证「任意合法配置可编辑」。
- 秘密字段留空 → 提交不含该 pointer，保留旧值；显式清除 → `secret_replacements` 置 `null`；轮换 → 提交新值（R2/R4）。
- 隐藏字段（条件为假）不参与提交，避免回写陈旧值。
- 数组空 / 越界（`minItems`/`maxItems`）→ 行内错误阻止保存，错误透出到表单顶部。
- 视口回归：桌面 / 移动下 4 页无布局回归（R1）。

## 删除面

- 删除 `window.confirm`、`PluginRepositoriesPage.vue` 手写 `role="alertdialog"`、市场行内 checkbox 门控。
- 删除 4 页裸 `<p>` 加载 / 错误 / 空态、内联 `.plugin-card` / `.repository-*` 手写样式、原生 `<select>` 实例切换。
- 删除前端扁平 `normalizePluginConfigSchema` 的扁平语义（递归化取代），不保留扁平与递归两套并行。
- 后端不删除既有词表组件 / 动作；仅新增 `array` / `visibleWhen`，`DeclarativeUISchemaVersion` 常量不改。
