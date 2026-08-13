# Exploration

```yaml
format: exploration
summary: "插件页面与动态表单现状已查明：4 页未遵循共享设计系统、动态表单仅扁平标量、后端 config_schema 已支持嵌套；官方包无 ui_schema、配置渲染当前为空地，无需版本迁移与版本号提升，schema_version 保持 1 直接扩展词表适配插件，风险集中在条件表达式白名单与无 ui_schema 兜底表单"
```

## 1. 插件页面现状（R1）

四个页面均挂在 `AppShell`（`path: '/'`）下，只受全局 `authGuard`（`panel/frontend/src/router/index.js:152`）保护；无路由级 `meta` 权限，页面内用 `useAccessControl`（`can('*')`、`can('resource.write')`）自行判权。

| 页面 | 文件 | 路由 | name |
|---|---|---|---|
| 已安装列表 | `panel/frontend/src/pages/plugins/PluginsPage.vue` | `plugins` | `plugins` |
| 市场 | `panel/frontend/src/pages/plugins/PluginMarketplacePage.vue` | `plugins/marketplace` | `plugin-marketplace` |
| 仓库源 | `panel/frontend/src/pages/plugins/PluginRepositoriesPage.vue` | `plugins/repositories` | `plugin-repositories` |
| 配置详情 | `panel/frontend/src/pages/plugins/PluginDetailPage.vue` | `plugins/:id` | `plugin-detail` |

注册位置 `panel/frontend/src/router/index.js:76-99`，动态 `plugins/:id` 最后声明以避免遮蔽静态兄弟路由。

与主流 UX 惯例偏离的现状（重构需替换）：
- **三种确认模式并存**：`PluginDetailPage.vue:69` 用原生 `window.confirm`；`PluginRepositoriesPage.vue:157-166` 用手写内联 `role="alertdialog"` 覆盖层；市场安装/升级用行内 checkbox 门控，无统一确认弹窗组件。
- **裸文本加载/错误/空态**：`PluginsPage.vue:36-37,52`、`PluginMarketplacePage.vue:101-102`、`PluginDetailPage.vue:159-160` 用 `<p>`，无 spinner/skeleton/图标；与仓库页较结构化的空/错态不一致。
- **原生 `<select>` 切换实例**（`PluginDetailPage.vue:179-181`）而非 tab/dropdown 组件。
- **按钮层级弱**：详情页动作全是 `btn btn-secondary`，卸载仅靠文字色 `plugin-danger`；无统一 `btn-danger` 变体，详情页无明确主操作。
- **导航不一致**：详情页有返回链接，市场/仓库无返回（市场仅链向仓库）。
- **已安装列表无表格/筛选/排序/搜索**：仅卡片网格，尽管卡片暴露生命周期/agent 状态/风险事实。
- **错误清理不一致**：市场用页级错误提示 + 选中即清，仓库用 `loadError`/`actionError`/`contentsError` 多通道并存。

## 2. 动态表单现状（R2 前端）

`panel/frontend/src/components/plugins/PluginConfigForm.vue` 不自行解析 schema，仅 `fields = normalizePluginConfigSchema(props.schema)` 后逐字段渲染。所有类型/关键字决策在 `panel/frontend/src/api/pluginSecurity.js` 的 `normalizePluginConfigSchema`（115-140 行）。

- 支持类型：`string`/`number`/`integer`/`boolean`（`allowedSchemaTypes`，pluginSecurity.js:3）。关键字：`type`/`title`/`description`/`default`/`enum`/`minimum`/`maximum`/`writeOnly` + 顶层 `required`。
- **扁平、仅顶层**：`Object.entries(properties)` + `flatMap`，从不递归 `properties`/`items`；`object`/`array` 类型被静默丢弃。`$ref`、`contentMediaType:text/html`、`pattern`/`minLength`/`maxLength`/`const`/`format` 被忽略/丢弃。
- 秘密字段：从不从 `props.config` 回填，恒初始化为 `''`；`submit()` 发出 `{config, secret_replacements}`，秘密按 JSON pointer（`/name`）进 `secret_replacements`（值或 `null` 清除），非秘密进 `config`。

`panel/frontend/src/components/plugins/PluginDeclarativeUI.vue` + `PluginDeclarativeComponent.vue` 渲染 `ui.schema.json`：
- 组件词表：`section`/`notice`/`toggle`/`select`/`number`/`secret`/`text`/`textarea`；动作：`submit`/`reset`/`dynamic`（`dynamic` 受 `canAct` 门控、带 `target_id`/`confirmed`）。
- **前端完全不读 `schema_version`**：两个 `.vue` 均无 `schemaVersion` 引用，仅测试 fixture（`PluginDeclarativeUI.test.js:6`）作为惰性数据。无版本门控/迁移/能力分支。`title`/`description`/`label` 用 `{{ }}` 文本插值，包标记永不解释（测试已验证不渲染 `<script>`/`<img>`）。

## 3. 后端契约现状（R2/R3/R4 后端 + SDK）

`panel/backend-go/internal/controlplane/plugins/types.go`：
- `DeclarativeUISchemaVersion = 1`（types.go:21），`declarative_ui.go:271-273` 拒绝其他任何值。
- 组件常量 types.go:23-31（8 种），动作 types.go:33-35（`submit`/`reset`/`dynamic`），未知类型在 `declarative_ui.go:482-483` 拒绝。

`panel/backend-go/internal/controlplane/plugins/declarative_ui.go`：
- **预算**（17-27 行）：文档 64 KiB、JSON 深度 32、组件嵌套 8、单集合子项 64、总组件 256、动作 8、每 select 选项 128/总选项 512、累计文本 32 KiB。
- **严格解码**：每个对象经 `decodeStrictUIObject` + 显式允许字段表，未知字段失败；`text()`（755-784 行）trim/拒绝 `< > {{ }} ${ url(`、危险 scheme（`javascript:`/`vbscript:`/`data:`/`file:`）与控制/bidi 字符。
- **绑定校验** `binding()`（609-646）：规范 JSON pointer、1-8 段、`^[a-z][a-z0-9_]{0,63}$`、禁 `__proto__`/`prototype`/`constructor`，再解析到已校验 config schema 的已声明属性；`validateUIBindingContract`（658-710）强制类型对应、`required`/`readOnly`/`writeOnly` 一致、select↔enum、number↔`minimum`/`maximum`/`multipleOf` 精确相等。
- **动态动作门控**（576-581）：需签名 `ui.dynamic-actions` 权限 + 签名权限集中该动作能力。

`panel/backend-go/internal/controlplane/plugins/config.go` `ValidateConfig`：
- **已支持嵌套对象与数组**。关键字白名单（`validator.go:1544`）：`$schema`/`type`/`enum`/`title`/`description`/`default`/`properties`/`required`/`additionalProperties`/`items`/`minItems`/`maxItems`/`uniqueItems`/`minLength`/`maxLength`/`pattern`/`minimum`/`maximum`/`multipleOf`/`readOnly`/`writeOnly`。
- 类型：`object`/`array`/`string`/`integer`/`number`/`boolean`/`null`。数组单 `items` schema + `minItems`/`maxItems`/`uniqueItems`（语义键去重，上限 1024）；数字用 `big.Rat` 精确比较（`multipleOf` 支持），数字受 `boundedJSONNumber`（尾数/指数上限 4096）保护。

`plugin-sdk/go/manifest.go`：`config_schema`（required，`manifest.go:42`）与 `ui_schema`（optional，`manifest.go:43`）均为**包内路径引用**而非内联内容；SDK schema（`plugin-sdk/go/schema/plugin-manifest-v1.schema.json`）限字符串 1..2048。host 用 `securePackagePath` + `readBoundedFile` 解析并 `validateJSONSchema`。

## 4. 设计系统（R1 交叉）

- **无第三方 UI 库**：`panel/frontend/package.json` = Vue 3.5 + Router + Pinia + `@tanstack/vue-query` + UnoCSS（`presetWind`+`presetIcons`），无 Element Plus/Naive UI/Tailwind。`main.js` 引 `styles/index.css` 与 `virtual:uno.css`。
- **设计令牌单一来源**：`panel/frontend/src/styles/themes.css`（`[data-theme]` 作用域）定义 `--color-*`/`--space-*`/`--radius-*`/`--font-*`/`--text-*`/`--shadow-*`/`--duration-*`/`--z-*`/`--sidebar-width`/`--header-height`；5 套主题（fresh-green/sakura-day/sakura-night/neko-dark/business）。
- **共享组件**：`panel/frontend/src/components/base/` = `BaseButton`/`BaseCard`/`BaseInput`/`BaseBadge`/`BaseIconButton`/`BaseTabs`/`BaseActionMenu`/`BaseListCard`/`BaseMetricBar`/`StatCard`/`EmptyState`/`BaseModal`/`ThemeToggle`/`TokenAuth`；`components/common/` = `ListPagination`/`ResourceListFilterBar`/`AgentSearchSelect`；域表格/卡片（`RuleCard|RuleTable`、`CertCard|CertTable`、`AgentTable` 等）。`styles/utilities.css` 定义 `.btn(--primary|secondary|danger|ghost…)`、`.spinner`、`.empty-state`、`.modal-overlay/.modal`、`.form-group/.input-base`、`.page-header` 等。`DeleteConfirmDialog` 用于破坏性确认。
- **非插件页规范骨架**：`page-header` 标题/副标题 + 右侧 `btn btn-primary` 主操作 → `<OperationStatusList/>` → `ResourceListFilterBar` + `ViewToggle` → 卡片网格或表格 → `.spinner`/`EmptyState`/`v-if/v-else-if` 空态 → `BaseModal`（建/编）+ `DeleteConfirmDialog`（删）。
- **插件页当前绕过共享组件**：用内联 `<p>` 加载/错/空、`window.confirm`、手写 `.plugin-card`/`.repository-*`、原生 `<select>`，未用 `BaseListCard`/`BaseCard`/`EmptyState`/`ResourceListFilterBar`/`ListPagination`。动态表单组件复用 `SecretWriteOnlyField` 但用裸内联 input 而非 `BaseInput`/`.input-base`。

## 5. 测试现状（R4）

前端（`panel/frontend/src/pages/plugins/*.test.js` + `components/plugins/*.test.js` + `api/pluginSecurity.test.mjs`）：
- `PluginsPage.test.js`：可见性过滤；`PluginDetailPage.test.js`：提交载荷绑定字段 + 错误 token 脱敏；`PluginDetailPage.integration.test.js`：生产 API 投影/秘密剥离/实例切换；`PluginRepositoriesPage.test.js`：官方源不可变/删除警告/载荷；`PluginMarketplacePage.test.js`：包 `ui` 标记不渲染/安装按钮门控。
- `PluginConfigForm.test.js`：`writeOnly` 秘密语义（不渲染、不回填、clear/rotate、载荷拆分）；`PluginDeclarativeUI.test.js`：宿主固定控件、`window.confirm` 门控、目标/确认发出、秘密 preserve/rotate/clear；`pluginSecurity.test.mjs`：`normalizePluginConfigSchema` 只保留 host 标量字段、丢 `contentMediaType:text/html`/`$ref`、`writeOnly` 标 secret 并剥 `default`。

后端（`panel/backend-go/internal/controlplane/plugins/*_test.go`）：
- `declarative_ui_test.go`：声明式 UI 不变量（动态动作需签名能力、类型/绑定/required/range/enum/readOnly 一致、secret↔writeOnly、预算、拒绝 iframe/navigate/注入/重复键/越界绑定）；`config_numeric_test.go`：精确数字边界；`validator_test.go`（2188 行）：签名/ABI/权限/RE2 界限/uniqueItems/enum 上限/TOCTOU 等；`official_v1_test.go`：官方市场 9 个规范包（**仅 config_schema，无 ui_schema**）。

**schema_version 不变，声明式 UI 词表直接扩展（无迁移、无版本号提升）**：
- `types.go:21` 常量 `DeclarativeUISchemaVersion` 保持 1；`declarative_ui.go:271-273` 继续拒绝非 1；`declarative_ui_test.go:195` 硬编码 `schema_version=2` 作为"拒绝"用例继续有效，无需改写。
- `PluginDeclarativeUI.test.js:6` fixture 已为 `schema_version:1`，无需改动。
- 新增能力（嵌套对象/数组渲染、条件显示、校验提示）在 schema_version 1 词表内直接扩展：后端 `declarative_ui.go` 白名单与绑定校验扩展新组件/字段，前端扩展宿主内置组件渲染；前端无版本协商/拒绝分支。

## 复用点

- **前端**：`base/` 组件（`BaseButton`/`BaseCard`/`BaseModal`/`EmptyState`/`BaseInput`/`BaseTabs`/`BaseListCard`）、`common/`（`ResourceListFilterBar`/`ListPagination`）、`DeleteConfirmDialog`、`themes.css` 令牌、`.btn`/`.page-header`/`.spinner`/`.form-group`/`.input-base` 工具类、非插件页规范骨架；`SecretWriteOnlyField` 已存在可复用。
- **后端**：`config.go ValidateConfig` 已支持嵌套对象/数组，R2 的"完整配置可编辑"后端校验大体已具备；`declarative_ui.go` 的预算/严格解码/绑定校验/文本脱敏机制是可扩展的脚手架（新增组件与条件表达式应纳入同一套校验）。

## 使用者 / 边界

- 管理员（可配置）、`resource.write` 成员（可触发动态动作）、只读成员（仅查看）——已由页面内 `useAccessControl` + 声明式 UI 的 `canConfigure`/`canAct` 门控承载，重构需保持该权限边界（R1/R3）。

## 风险

- **无版本迁移、无版本号提升（配置渲染当前为空地）**：官方市场包仅 `config_schema` 无 `ui_schema`，声明式 UI 从未实际投入使用；`schema_version` 保持 1，词表直接扩展，无需保留兼容包袱。`official_v1_test.go` 不受影响除非官方包新增 `ui_schema`；`declarative_ui_test.go:195` 的"schema_version=2 拒绝"用例继续有效。
- **前端无版本门控**：`schema_version` 前端不读，直接以现有词表渲染；无 `ui_schema` 时用增强 Schema 自动生成表单兜底。
- **秘密语义**：嵌套对象/数组下仍须保持"留空保留 / 清除 / 轮换"语义不变（R2）。
- **安全边界**：新增组件与条件表达式必须纳入后端白名单/预算校验，前端仍只用宿主内置组件渲染，不得注入 HTML/JS/CSS/远程组件（R3）。条件表达式是全新能力，无现有契约。
- **条件表达式**：受限表达式语法与预算上限尚无既有定义，是本次需新建的契约面。
- **视口回归**：4 页重设计后需保持桌面/移动视口无布局回归（R1）。

## 未知项

- 仓库内无 `schema_version` 门控/迁移语义的既有证据（前端不读、后端只拒绝非 1）。
- 官方市场包全部无 `ui_schema`，声明式 UI 路径当前未被任何官方包使用——配置渲染实质从零开始，无需迁移、无需版本号提升。
- 条件显示所用受限表达式的具体语法/预算尚未定义。
- 已安装列表是否从卡片网格改为表格/可筛选（属 UX 决策，非证据可定）。

## 验证焦点

- 后端：`go test ./...` 全绿，声明式 UI 词表在 v1 上直接扩展、新增组件/条件表达式纳入白名单校验，`config_schema` 嵌套/数组校验保持。
- 前端：`npm run build` + vitest 全绿，动态表单具备嵌套对象/数组（增删/重排）、条件显示、校验提示，无 `ui_schema` 兜底可编辑完整配置。
- 契约：schema_version 保持 1 不变、词表直接扩展，任意合法 config_schema 可编辑保存生成 revision，动态动作触发/权限校验与秘密语义行为不变（R4）；注入/越界绑定/非法表达式仍被拒绝（R3）；非插件页不动（R6）。
