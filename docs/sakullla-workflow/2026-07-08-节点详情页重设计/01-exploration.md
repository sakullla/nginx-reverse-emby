# 01-exploration：节点详情页重设计

**top_project：** panel/frontend  
**local_modules：**
- src/pages/AgentDetailPage.vue
- src/components/base/（BaseBadge、BaseListCard、BaseCard、BaseButton、BaseModal、BaseTabs 等）
- src/components/AgentMetricTile.vue、AgentStatusBadge.vue、AgentMonitorCard.vue
- src/hooks/（useAgents、useRules、useL4Rules、useCertificates、useRelayListeners、useDeleteAgent、useUpdateAgent）
- src/api/runtime.js
- src/styles/（themes.css、index.css、utilities.css、animations.css）
- src/context/ThemeContext.js
- src/utils/agentHelpers.js、agentMetrics.js
- src/stores/messages.js
- src/router/index.js
**exploration_mode：** targeted

---

## source_materials

- source_type：chat
- source_ref：本 workflow 启动前的需求澄清对话
- local_ref：docs/requirements/2026-07-08-节点详情页重设计.md
- captured_at：2026-07-08
- content_scope：用户确认“重新设计节点详情页”的需求范围、用户、信息架构、操作边界、设计系统范围与验收标准。

---

## requirement_anchor_coverage

| 锚点 | 状态 | 覆盖说明 |
|---|---|---|
| R1：重新设计 `nre-agent` 节点详情页（`AgentDetailPage.vue`），借视觉升级顺带优化信息架构 | covered | 已定位页面路径 `panel/frontend/src/pages/AgentDetailPage.vue`，梳理当前模板分区、API 调用与字段消费。 |
| R2：目标用户为 SRE / 运维排障人员 | covered | 来自需求文档确认；当前页面字段（在线状态、版本、最后心跳、同步失败提示）与本次新增的首屏聚合目标一致。 |
| R3：信息架构采用“顶部状态栏 + 指标卡片 + 可折叠详情区块”，首屏突出健康状态 | covered | 当前页面已有 `BaseListCard` 摘要卡、4 个 `AgentMetricTile`、规则/流量/系统信息 tab；重构需把证书、监听、同步错误等关键信息前置，并引入可折叠区块。 |
| R4：页面操作边界为“查看为主 + 轻量运维操作”，包括强制同步、复制注册命令、删除/注销节点、跳转编辑页 | covered | 删除/注销可通过 `useDeleteAgent` 直接复用；强制同步 API 与复制注册命令逻辑当前只在 `AgentsPage.vue` 实现，需迁移到详情页；规则跳转已存在，证书/监听跳转可复用列表页 query 模式。 |
| R5：全局设计 token + 节点详情页完整暗色模式/响应式 | covered | 项目通过 `styles/themes.css` + `data-theme` 已具备设计 token 与多主题暗色能力；响应式断点在 `styles/index.css` 有 1024/640，详情页需补齐 768/375 等验收断点。 |
| R6：字段层面仅重组现有字段，不动后端 | covered | `AgentSummary` 已直接返回 `http_rules_count`、`l4_rules_count`；证书/监听数需在前端通过 `useCertificates`、`useRelayListeners` 取数组长度，无需后端改动。 |
| R7：验收标准为可扫描性 + 暗色/响应式无样式断裂，SRE 能在 3 秒内判断节点健康 | covered | 关键字段均已确认存在；重构后首屏可见在线状态、模式、版本、最后心跳、规则/证书/监听数、同步错误提示。 |

---

## 探索问题与回答

### Q1：当前 `AgentDetailPage.vue` 的实现结构与字段消费

**文件路径：** `panel/frontend/src/pages/AgentDetailPage.vue`

**路由：**
- `panel/frontend/src/router/index.js:32-37`：`path: 'agents/:id'`，`name: 'agent-detail'`，组件懒加载。
- agent ID 从 `route.params.id` 获取（`AgentDetailPage.vue:319-321`）。

**页面自顶向下结构（当前）：**

| 区域 | 行号 | 内容 |
|---|---|---|
| 返回链接 | 2-5 | `RouterLink` 返回 `/agents` |
| 顶部摘要卡 | 7-70 | `BaseListCard`（`:status="statusTone"`），含 `AgentStatusBadge`、`BaseBadge` 模式、HTTP/L4 规则统计、地址/最后活跃元信息、4 个 `AgentMetricTile` |
| 同步失败块 | 72-84 | 原生 SVG + `agent.last_apply_message` |
| 标签页 | 86 | `BaseTabs`：`rules` / `traffic`（可选）/ `info` |
| 流量 tab | 89-198 | 3 个 `BaseListCard`：监控（`TrafficSummaryCards`、`TrafficTrendChart`）、分析（`TrafficBreakdownTable`）、管理（`TrafficPolicyForm`、`TrafficHistoryManager`） |
| 规则 tab | 200-237 | `BaseListCard` 包裹手写规则表格，合并 HTTP 与 L4 规则 |
| 系统信息 tab | 239-267 | 3 个 `BaseListCard`：运行包、节点身份、同步状态 |
| 加载/不存在 | 270-281 | spinner、节点不存在提示 |

**API 调用与字段消费：**

| 用途 | 调用位置 | API 函数 | Endpoint |
|---|---|---|---|
| agent 数据 | `AgentDetailPage.vue:323-324` | `useAgents` → `fetchAgents` | `GET /agents` |
| agent 统计 | `AgentDetailPage.vue:341-346` | `useQuery` → `fetchAgentStats` | `GET /agents/:id/stats` |
| 系统信息 | `AgentDetailPage.vue:347-350` | `useQuery` → `fetchSystemInfo` | `GET /info` |
| HTTP 规则 | `AgentDetailPage.vue:328` | `useRules` → `fetchRules` | `GET /agents/:id/rules` |
| L4 规则 | `AgentDetailPage.vue:332` | `useL4Rules` → `fetchL4Rules` | `GET /agents/:id/l4-rules` |
| 流量策略/摘要/趋势 | `AgentDetailPage.vue:355-366` | `useTrafficPolicy/Summary/Trend` | 若干 `/agents/:id/traffic-*` |

当前消费的主要 `agent` 字段：
`id`、`name`、`agent_url`、`last_seen_at`、`last_seen_ip`、`mode`、`status`、`version`、`runtime_package_version`、`runtime_package_platform`、`runtime_package_arch`、`runtime_package_sha256`、`desired_package_sha256`、`package_sync_status`、`desired_revision`、`current_revision`、`last_apply_revision`、`last_apply_status`、`last_apply_message`、`is_local`、`tags`。

**约束与可复用点：**
- `panel/frontend/src/components/canonicalBackendDisplay.test.js:33` 要求页面源码不能出现 `backend_url`、`upstream_host`、`upstream_port` 回退字段；当前规则显示已使用 `backends[].url` / `backends[].host:port`，符合要求。
- `AgentDetailPage.test.js` 共 17 个用例，覆盖状态卡、tab 切换、规则列表、流量统计、系统信息、校准/清理历史等；重构需保持这些测试通过或同步更新。
- `utils/agentMetrics.js` 中的 `barTone`、`bytesPair`、`cpuUsage`、`rate` 已被详情页与 `AgentMonitorCard` 共用。

---

### Q2：现有设计系统组件接口

**`BaseBadge.vue`**（`panel/frontend/src/components/base/BaseBadge.vue`）
- Props：`tone`（success/warning/danger/primary/neutral）、`subtone`（muted/secondary，仅 neutral）、`shape`（pill/square）、`size`（sm/md）、`mono`、`dot`。
- Slot：默认插槽。
- 使用示例：`AgentStatusBadge.vue:2`（dot size="md"）、`AgentDetailPage.vue:10`（模式 badge，tone="primary" size="sm"）、`AgentMonitorCard.vue:71`（标签 badge，tone="neutral"）。

**`AgentMetricTile.vue`**（`panel/frontend/src/components/AgentMetricTile.vue`）
- Props：`icon`、`label`、`value`、`unit`、`percent`、`tone`、`variant`（default/compact）、`networkDown`、`networkUp`。
- 测试覆盖：`AgentMetricTile.test.js`。
- 使用示例：`AgentDetailPage.vue:37-67`、`AgentMonitorCard.vue:36-68`。

**`BaseListCard.vue`**（`panel/frontend/src/components/base/BaseListCard.vue`）
- Props：`status`（success/warning/danger/neutral，左侧色条）、`disabled`、`clickable`、`title`、`as`。
- Slots：`header-left`、`header-right`、默认 body、`footer`。
- 使用示例：`AgentDetailPage.vue:7`（顶部摘要卡）、`AgentMonitorCard.vue:2-73`。

**`BaseCard.vue`**（`panel/frontend/src/components/base/BaseCard.vue`）
- Props：`title`、`hover`、`glass`。
- Slots：`header`、`actions`、默认 body、`footer`。
- 现状：已存在但**未被任何页面使用**。

**其它可能复用的基础组件：**
- `EmptyState.vue`：`icon`、`title`、`description`、`action` slot。
- `DeleteConfirmDialog.vue`：`show`、`title`、`message`、`name`、`confirmText`、`loading`。
- `BaseModal.vue`：`modelValue`、`title`、`subtitle`、`size`、`showFooter`。
- `BaseButton.vue`：`type`、`variant`（primary/secondary/danger/success）、`disabled`、`loading`。
- `BaseIconButton.vue`：`tone`、`size`、`title`、`disabled`。
- `BaseTabs.vue`：`tabs`、`modelValue`。
- `StatCard.vue`：`tone`、`size`、`value`、`label`、`subLabel`、`progress`、`to`、`icon` slot。
- `BaseMetricBar.vue`：`label`、`value`、`unit`、`percent`、`tone`。
- `TrafficCollapsibleSection.vue`：`title`、`subtitle`、`defaultExpanded`。
- `ThemeSelector.vue`、`ThemeToggle.vue`。

**注意：** 当前没有独立的 Tooltip、Skeleton/Loading、Toast、Accordion 组件；Toast 通过 `stores/messages.js` 的全局 `messageStore` 实现。

---

### Q3：项目主题、设计 token 与样式体系

**样式入口：**
- `panel/frontend/src/main.js:6-7`：
  ```js
  import './styles/index.css'
  import 'virtual:uno.css'
  ```

**构建工具：**
- 项目使用 **UnoCSS**，而非 Tailwind。
- `vite.config.js:3,8` 引入 `UnoCSS`。
- `uno.config.js:14-32` 将主题色映射到 CSS 变量，并定义 shortcuts（`btn`、`btn-primary`、`card`、`input-base` 等）。
- 未自定义响应式断点；响应式依赖 `presetWind` 默认断点 + 自定义 media query。

**设计 token：**
- 全局共享 token 定义于 `panel/frontend/src/styles/themes.css:4-79`（在 `[data-theme]` 上）：
  - spacing：`--space-1` ~ `--space-12` 等。
  - radius：`--radius-sm`、`--radius-md`、`--radius-lg`、`--radius-xl`、`--radius-full`。
  - font/size/weight：`--font-sans`、`--font-mono`、`--text-sm`、`--text-xl`、`--font-medium` 等。
  - transition：`--duration-fast`、`--ease-default`。
  - z-index：`--z-modal`、`--z-toast` 等。
  - layout：`--sidebar-width`、`--header-height`。
- 颜色 token 以 `[data-theme="..."]` 覆盖，例如：
  - `fresh-green`（`themes.css:84-141`）：`--color-primary: #059669`、`--color-bg-canvas: #f6fbf8`。
  - `sakura-night`（`themes.css:208-265`）：`--color-primary: #f472b6`、`--color-bg-canvas: #0f0f14`。
  - `neko-dark`（`themes.css:270-327`）：`--color-primary: #60a5fa`、`--color-bg-canvas: #0f172a`。

**其它样式文件：**
- `styles/index.css`：导入 `themes.css`、`animations.css`、`utilities.css`；定义基础样式、滚动条、ApexCharts 覆盖、响应式工具类（断点 2560/1024/640）。
- `styles/animations.css`：关键帧与动画工具类。
- `styles/utilities.css`：全局工具类，部分仍使用硬编码值（如第 39 行 `color: white`、第 71 行 `background: #dc2626`）。
- `style.css`：孤儿文件，未导入，可忽略。

**组件消费 token 示例：**
- `BaseBadge.vue:53`：`transition: all var(--duration-fast) var(--ease-default);`
- `BaseBadge.vue:66-67`：`background: var(--color-success-50); color: var(--color-success);`
- `BaseListCard.vue:72`：`background: var(--color-bg-surface);`
- `BaseListCard.vue:73`：`border: 1.5px solid var(--color-border-default);`
- `BaseListCard.vue:74`：`border-radius: var(--radius-xl);`

**硬编码风险点：**
- `BaseBadge.vue` 中存在 `gap: 4px`、`width: 6px`、`font-size: 0.7rem`、`padding: 2px 6px`。
- `BaseListCard.vue` 中存在 `border: 1.5px`、transition 时间写死、`transform: translateY(-2px)`、`min-height: 28px`。
- `utilities.css` 中存在 `padding: 10px 24px`、`color: white`、`background: #dc2626`。

验收标准“不新增 `#xxx` 或 `px` 硬编码常量”意味着本次重构自身不得新增这类值；对旧值的逐步替换属于可选优化，不在本次最小范围内。

---

### Q4：暗色模式与响应式现状

**暗色模式实现：**
- `ThemeContext.js:3-9` 提供 5 套主题：`fresh-green`、`sakura-day`、`sakura-night`、`business`、`neko-dark`。
- `ThemeContext.js:17-26` 从 `localStorage.getItem('theme')` 读取并迁移旧 ID。
- `ThemeContext.js:30-35` 切换主题时写入 `localStorage` 并通过 `document.documentElement.setAttribute('data-theme', id)` 应用到 `<html>`。
- `ThemeSelector.vue:38-52` 使用 `useTheme()` 切换多主题。
- `ThemeToggle.vue:26-52` **绕过 `ThemeContext`**，直接操作 `localStorage` 和 `<html data-theme="dark">` 实现明暗二态；该组件目前与 `ThemeContext` 机制不一致。

**响应式断点：**
- `styles/index.css:91-117` 定义了 3 个断点：
  - `2560px`（4K）
  - `1024px`（max-width，tablet）
  - `640px`（max-width，mobile）
- 需求验收要求 1280/1024/768/375，当前缺少 768/375 显式断点，但可通过新增 media query 或 UnoCSS 响应式前缀实现。

**现有响应式示例：**
- `AgentDetailPage.vue:968-1002`：`@media (max-width: 900px)` 规则列表卡片化。
- `AgentDetailPage.vue:1078-1080`：`@media (max-width: 720px)` 指标卡片单列。
- `AgentMonitorCard.vue:190-194`：`@media (max-width: 420px)` 指标卡片单列。

**让详情页完整支持暗色/响应式的建议路径：**
1. 继续基于 `themes.css` 的 CSS 变量；新增/重构样式全部使用 `--color-*`、`--space-*`、`--radius-*`。
2. 在 `AgentDetailPage.vue` 的 `<style scoped>` 中新增 1024/768/640/375 的 media query，覆盖顶部状态栏、指标卡片网格、可折叠区块。
3. 保持 `ThemeSelector.vue` 作为主题入口；`ThemeToggle.vue` 的二态逻辑若保留，需改造成调用 `useTheme().setTheme(...)`。

---

### Q5：agent 数据模型与可消费字段

**当前详情页并未调用单 agent 接口**，而是从全量列表过滤：
- `AgentDetailPage.vue:323-324`：`const { data: agentsData } = useAgents(); const agent = computed(() => agentsData.value?.find(a => a.id === agentId.value))`
- `useAgents` 调用 `GET /agents`（`api/runtime.js:297-300`）。

**后端 `AgentSummary` 字段（`panel/backend-go/internal/controlplane/service/agents.go:54-84`）：**

| JSON 字段 | 说明 |
|---|---|
| `id`、`name`、`agent_url` | 基础信息 |
| `version`、`runtime_package_version`、`runtime_package_platform`、`runtime_package_arch` | 版本与平台 |
| `runtime_package_sha256`、`desired_package_sha256`、`package_sync_status` | 包同步 |
| `desired_version` | 策略期望版本 |
| `tags` | 标签 |
| `outbound_proxy_url`、`traffic_stats_interval` | 网络/流量配置 |
| `mode`（local/master/pull）、`is_local` | 模式 |
| `desired_revision`、`current_revision`、`last_apply_revision`、`last_apply_status`、`last_apply_message` | 同步状态 |
| `last_seen_at`、`status`（online/offline）、`last_seen_ip` | 心跳/在线 |
| `capabilities` | 能力列表 |
| `http_rules_count`、`l4_rules_count` | 后端聚合的规则数 |

**计数来源：**
- HTTP/L4 规则数：后端已聚合为 `http_rules_count`、`l4_rules_count`；当前页面未使用，而是分别调用 `useRules`、`useL4Rules` 后取数组长度。
- 证书数：后端未聚合；需调用 `useCertificates(agentId)`（`hooks/useCertificates.js:9-16` → `GET /agents/:id/certificates`）并取数组长度。
- Relay 监听数：后端未聚合；需调用 `useRelayListeners(agentId)`（`hooks/useRelayListeners.js:6-15` → `GET /agents/:id/relay-listeners`）并取数组长度。

**在线/同步状态判断：**
- 后端 `agentStatus`（`agents.go:1234-1252`）根据 `last_seen_at` 与心跳超时计算 `online/offline`。
- 前端 `getAgentStatus`（`utils/agentHelpers.js:1-18`）综合 `status`、`desired_revision`、`current_revision`、`last_apply_status` 返回 `online/offline/pending/failed`。
- `AgentStatusBadge.vue:16-26` 将其映射为 UI 状态与文案。

**缺口：**
- 没有 `created_at` / `register_time` 字段，无法展示“注册时间”。
- 没有聚合的 `errors` / `alerts` 计数，只能展示单条 `last_apply_message`。
- 证书/监听数需要新增前端 hook 调用，但不需要后端改动。

---

### Q6：现有轻量运维操作实现

**强制同步（推送配置）：**
- API：`POST /agents/:id/apply`，函数 `applyConfig`（`api/runtime.js:383-390`）。
- 当前实现：`AgentsPage.vue:228-238`，本地 `applying` ref + 按钮文案变化；**无错误提示，成功后不主动刷新**，依赖 `useAgents` 10 秒轮询。
- 在详情页复用建议：调用 `applyConfig(agentId.value)`，补充 `messageStore.success/error` 与 `queryClient.invalidateQueries(['agents'])`。

**复制注册命令：**
- 生成位置：`AgentsPage.vue:247-256`，基于 `window.location.origin` 与 `systemInfo.value.master_register_token` 拼接：
  ```js
  `${origin}/panel-api/public/join-agent.sh | sh -s -- --register-token ${token} --install-systemd`
  ```
- 复制逻辑：`AgentsPage.vue:281-310`，优先 `navigator.clipboard.writeText`，降级 `document.execCommand('copy')`，成功后 `messageStore.success('已复制到剪贴板')`。
- 现状：复制逻辑未抽取为 composable，需迁移到详情页或抽取 `useClipboard`。

**删除/注销 agent：**
- API：`DELETE /agents/:id`，`deleteAgent`（`api/runtime.js:392-395`）。
- Hook：`useDeleteAgent`（`hooks/useAgents.js:16-28`）已封装 mutation，成功后 `invalidateQueries(['agents'])` + `messageStore.success('节点已删除')`。
- 确认弹窗：`DeleteConfirmDialog.vue` 已可复用。
- 列表页调用：`AgentsPage.vue:351-360`；详情页若新增删除入口，删除后需 `router.push('/agents')`。

**跳转规则/证书/监听：**
- 规则跳转：`AgentDetailPage.vue:812-821` 已存在 `navigateToRule(rule)`，HTTP 规则跳 `/rules`，L4 规则跳 `/l4`，query 带 `agentId` 与 `search=#id=${rule.id}`。
- 路由已存在：`/rules`、`/l4`、`/certs`、`/relay-listeners`（`router/index.js:38-61`）。
- `CertsPage.vue` 支持 `agentId` + `search=#id=` 过滤；`RelayListenersPage.vue` 支持 `agentId` 但不支持 `search=#id=`。
- 当前详情页**没有证书/监听的跳转入口**，可参照规则跳转模式补充。

**可复用工具：**
- Toast：`stores/messages.js` 的 `messageStore`。
- 确认弹窗：`DeleteConfirmDialog.vue`。
- agent 操作 hooks：`useDeleteAgent`、`useUpdateAgent`。
- 时间/状态/模式文案：`utils/agentHelpers.js`（`timeAgo`、`getModeLabel`、`getAgentStatus`）。
- 指标格式化：`utils/agentMetrics.js`。

---

## 可复用点汇总

| 能力 | 路径 | 复用方式 |
|---|---|---|
| 状态徽章 | `components/AgentStatusBadge.vue` | 直接用于顶部状态栏 |
| 模式/标签徽章 | `components/base/BaseBadge.vue` | 直接用于模式 badge、标签 |
| 指标卡片 | `components/AgentMetricTile.vue` | 直接用于 CPU/内存/磁盘/网络及规则/证书/监听计数 |
| 卡片容器 | `components/base/BaseListCard.vue` | 顶部摘要卡、可折叠详情区块 |
| 折叠区块 | `components/traffic/TrafficCollapsibleSection.vue` | 规则/证书/监听/日志/系统信息折叠 |
| 空状态 | `components/base/EmptyState.vue` | 无规则/无证书/无监听时 |
| 删除确认 | `components/DeleteConfirmDialog.vue` | 删除节点 |
| Toast | `stores/messages.js` | 复制成功、同步成功/失败、删除成功 |
| agent 操作 hooks | `hooks/useAgents.js` | `useDeleteAgent`、`useUpdateAgent` |
| 规则/证书/监听 hooks | `hooks/useRules.js`、`useL4Rules.js`、`useCertificates.js`、`useRelayListeners.js` | 取列表与计数 |
| 时间/状态工具 | `utils/agentHelpers.js` | `timeAgo`、`getModeLabel`、`getAgentStatus` |
| 指标格式化 | `utils/agentMetrics.js` | `barTone`、`bytesPair`、`cpuUsage`、`rate` |
| 主题/变量 | `context/ThemeContext.js`、`styles/themes.css` | 暗色模式与 token |

---

## 风险与未知项

### 风险

1. **主题切换机制不一致**：`ThemeToggle.vue` 直接写 `data-theme="dark"`，而 `ThemeSelector.vue` 走 `ThemeContext`。若需求要求“完整暗色模式”，需决定统一方案。
2. **硬编码样式债务**：部分基础组件和 `utilities.css` 仍含硬编码 px/rem/hex；重构时容易误新增硬编码值。
3. **响应式断点不完整**：当前自定义断点只有 1024/640，需求验收要求 768/375，需要新增 media query 或使用 UnoCSS 响应式类。
4. **证书/监听计数需新增 API 调用**：虽不改动后端，但会增加详情页请求数；需评估加载状态与错误处理。
5. **i18n 缺失**：当前页面文案全部硬编码中文；需求允许“前端文案重命名（i18n）”，但引入完整 i18n 会扩大范围。
6. **测试覆盖敏感**：`AgentDetailPage.test.js` 有 17 个用例，重构后需要同步更新测试。

### 未知项

- 无重大未知；所有关键字段、组件、主题、API 均已有代码证据。

---

## 后续方案需要回答的问题

1. 顶部状态栏具体展示哪些字段？是否包含“注册时间”（当前无该字段）？
2. 指标卡片除 CPU/内存/磁盘/网络外，是否新增规则/证书/监听计数卡？证书/监听数为 0 时的空状态如何处理？
3. “可折叠详情区块”是否替换现有的 `BaseTabs` 规则/流量/信息分区？还是保留 tab 并在其内部使用折叠？
4. 强制同步、复制注册命令、删除/注销、跳转编辑的按钮/icon 如何布局？是否放入 `BaseListCard` 的 `header-right`？
5. 暗色模式是继续走 `ThemeSelector` 多主题，还是改造为 `ThemeToggle` 二态？
6. 是否在本次引入 Vue i18n，还是先硬编码中文重命名？
7. 是否使用后端聚合的 `http_rules_count`/`l4_rules_count`，还是继续通过 hook 数组长度计算？
