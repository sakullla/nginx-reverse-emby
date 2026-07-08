# 技术方案：节点详情页重设计

## 1. 一句话方案

- 我们要做的是：在不改动后端的前提下，将 `panel/frontend/src/pages/AgentDetailPage.vue` 从“标签页 + 内联混合信息”重构为“顶部状态栏 + 指标卡片 + 可折叠详情区块”的信息架构；全局设计 token 在 `themes.css` 中落地，确保节点详情页在暗色模式与 1280/1024/768/375px 宽度下无样式断裂。
- 方案状态：proposed
- 待用户确认：无关键业务取舍；下述技术决策（是否引入最小 i18n 对象、是否用 `StatCard` 展示计数、是否新增证书/监听 hook）均为实现层选择，可在 02 确认门中一并确认。

## 2. 背景与问题

当前 `AgentDetailPage.vue` 把节点状态、规则列表、流量统计、系统信息放在三个 tab 内，首屏只能看到一个摘要卡，SRE 需要切换 tab 才能判断同步异常位置。同时页面中文案硬编码、部分样式未走设计 token，暗色模式与响应式支持不完整。

根据 `01-exploration.md` 的梳理：
- 页面当前从 `GET /agents` 全量列表过滤出当前 agent，字段已足够支撑新架构（`name`、`mode`、`status`、`last_seen_at`、`version`、`http_rules_count`、`l4_rules_count`、`last_apply_status`、`last_apply_message` 等）。
- 设计系统组件 `BaseBadge`、`AgentMetricTile`、`BaseListCard`、`StatCard`、`EmptyState`、`DeleteConfirmDialog`、`BaseButton`、`TrafficCollapsibleSection` 已存在且可直接复用。
- 主题系统通过 `styles/themes.css` + `data-theme` 已具备 token 与暗色能力，但响应式断点目前只有 1024/640，需要补充 768/375。

## 3. 目标与不做

- 要达成：
  - SRE 打开节点详情页后，3 秒内能判断在线/离线/同步失败状态、版本、规则/证书/监听数量。
  - 强制同步、复制注册命令、删除/注销、跳转规则/证书/监听列表等操作在首屏或顶部折叠区可见。
  - 所有新增/重构样式使用 `themes.css` 设计 token，不新增 `#xxx` 或 `px` 硬编码常量。
  - 节点详情页在 5 套主题（含暗色）及 1280/1024/768/375px 宽度下无样式断裂。
- 明确不做：
  - 不新增后端接口或字段；证书/监听数通过前端已有 hook 取数组长度。
  - 不做节点详情页内联编辑规则/证书/监听。
  - 不做一次性全局所有页面的暗色/响应式全量迁移。
  - 不做批量节点操作、同步历史服务、审计日志。
- 关键约束：
  - 仅重组现有后端返回字段。
  - 保持 `canonicalBackendDisplay.test.js` 对 `AgentDetailPage.vue` 的回退字段限制。
  - 保持现有 `AgentDetailPage.test.js` 覆盖场景，必要时同步更新测试。

## 4. 依据、线索与假设

- 原始需求材料：
  - source_ref：`docs/requirements/2026-07-08-节点详情页重设计.md`
  - requirement_anchors：R1-R7 均为 confirmed
- 源码证据：
  - `panel/frontend/src/pages/AgentDetailPage.vue` - 当前页面结构与字段消费来源。
  - `panel/frontend/src/components/base/BaseBadge.vue`、`BaseListCard.vue`、`BaseButton.vue` - 设计系统组件接口。
  - `panel/frontend/src/components/AgentMetricTile.vue`、`StatCard.vue` - 指标/计数卡片组件。
  - `panel/frontend/src/components/traffic/TrafficCollapsibleSection.vue` - 可折叠区块组件。
  - `panel/frontend/src/styles/themes.css` - 设计 token 与主题色。
  - `panel/frontend/src/context/ThemeContext.js` - 主题切换机制。
  - `panel/frontend/src/hooks/useAgents.js`、`useRules.js`、`useL4Rules.js`、`useCertificates.js`、`useRelayListeners.js` - 数据获取 hook。
  - `panel/frontend/src/api/runtime.js:383-390` - `applyConfig` 强制同步 API。
  - `panel/frontend/src/hooks/useAgents.js:16-28` - `useDeleteAgent` 已封装删除 mutation。
  - `panel/frontend/src/pages/AgentsPage.vue:247-310` - 注册命令生成与复制逻辑。
- workflow 证据：
  - `docs/sakullla-workflow/2026-07-08-节点详情页重设计/01-exploration.md#Q1` - 当前页面结构与 API 字段。
  - `docs/sakullla-workflow/2026-07-08-节点详情页重设计/01-exploration.md#Q2` - 设计系统组件清单。
  - `docs/sakullla-workflow/2026-07-08-节点详情页重设计/01-exploration.md#Q4` - 暗色/响应式现状。
  - `docs/sakullla-workflow/2026-07-08-节点详情页重设计/01-exploration.md#Q5` - agent 数据模型与操作复用点。
- 官方资料 / 规范依据：无
- 知识线索：无
- 开发规范：待 03 前 Standards Lookup 固化 `standards_refs`。
- 假设：
  - 后端 `AgentSummary` 中的 `http_rules_count` / `l4_rules_count` 字段稳定可用。
  - `useCertificates` / `useRelayListeners` 返回结构与列表页一致，可直接取 `length`。
  - 用户接受“错误/告警数”以同步失败状态 + `last_apply_message` 替代数值计数（当前无聚合错误数字段）。

## 5. 设计思路

整体把页面从“tab 容器”改成“单页滚动 + 可折叠区块”。首屏用 `BaseListCard` 作为顶部状态栏，把节点名、在线状态 badge、模式 badge、版本、最后心跳、标签、操作按钮全部放在卡片头部；其 body 放置资源指标（CPU/内存/磁盘/网络）与业务计数指标（规则/证书/监听/同步状态）两行网格。

状态栏下方依次是若干 `TrafficCollapsibleSection` 可折叠区块：规则列表、证书列表、监听列表、流量统计、系统信息、同步事件。默认展开“规则”与“同步事件”（当存在同步失败时），其余默认折叠，减少首屏噪音。每个计数指标卡可点击跳转到对应折叠区块或列表页。

样式层全部基于 `themes.css` 变量。新增响应式 media query 覆盖 1280/1024/768/375，使用 CSS grid 的 `repeat(auto-fit, minmax(...))` 或显式列数切换，保证小屏下指标卡片垂直堆叠、折叠区块内边距与字号适配。

操作层复用现有 API 与 hook：`applyConfig` 做强制同步；`useDeleteAgent` + `DeleteConfirmDialog` 做删除；复制注册命令的逻辑从 `AgentsPage.vue` 抽取为 `useJoinCommand` composable 或内联迁移；规则/证书/监听跳转复用 `navigateToRule` 模式并扩展到证书/监听。

## 5.1 需求覆盖核验

| 需求锚点 | 方案覆盖 | 代码证据 / 探索证据 | 状态 |
|---|---|---|---|
| R1 | 整体重构 `AgentDetailPage.vue` 信息架构与视觉 | `01-exploration.md#Q1` | covered |
| R2 | 首屏聚合健康/同步信息，面向 SRE | `01-exploration.md#Q1`、`#Q5` | covered |
| R3 | 顶部状态栏 + 指标卡片 + 可折叠详情区块 | 设计思路与改动点 1-3 | covered |
| R4 | 强制同步、复制注册命令、删除/注销、跳转编辑入口 | 改动点 4 | covered |
| R5 | 全局设计 token + 暗色/响应式 | `themes.css`、`ThemeContext`、改动点 5 | covered |
| R6 | 仅重组现有字段，不动后端 | `01-exploration.md#Q5` | covered |
| R7 | 3 秒可扫描、暗色/响应式无断裂 | 改动点 1、5 与验收标准 | covered |

## 6. 改动点

### 改动点 1：重构顶部状态栏与指标卡片区

- 覆盖需求锚点：R1、R2、R3、R7
- 改哪里：`panel/frontend/src/pages/AgentDetailPage.vue` 顶部摘要区域与 `<script setup>` 中的派生 computed。
- 现状：当前顶部只有一个 `BaseListCard` 摘要卡，内部放置 `AgentStatusBadge`、模式 badge、HTTP/L4 统计文案、地址/最后活跃元信息、4 个 `AgentMetricTile`；规则/证书/监听计数未聚合，同步失败信息以独立原生块展示。
- 改成：
  - `BaseListCard` 的 `#header-left` 放置节点名、`AgentStatusBadge`、模式 `BaseBadge`、版本、最后心跳时间、标签列表。
  - `#header-right` 放置“推送配置”“复制注册命令”“删除节点”操作按钮（使用 `BaseButton` / `BaseIconButton`）。
  - body 分为两行指标网格：
    - 资源指标：`AgentMetricTile` 展示 CPU、内存、磁盘、网络（复用现有 `agentMetrics.js` 格式化函数）。
    - 业务计数：`StatCard` 展示 HTTP 规则数、L4 规则数、证书数、监听数、同步状态；计数卡使用 `to` 或 `@click` 跳转到对应折叠区块或列表页。
- 为什么：把 SRE 最关心的健康状态、计数与操作全部前置到首屏，减少 tab 切换；`StatCard` 比 `AgentMetricTile` 更适合纯数字计数与跳转场景，而 `AgentMetricTile` 更适合带进度条的资源指标。

### 改动点 2：引入可折叠详情区块并替换 tab 结构

- 覆盖需求锚点：R1、R3
- 改哪里：`panel/frontend/src/pages/AgentDetailPage.vue` 中 `BaseTabs` 及规则/流量/系统信息 tab 的模板与状态。
- 现状：页面使用 `BaseTabs` 切换“规则”“流量统计”“系统信息”三个 tab，流量 tab 内部再嵌套监控/分析/管理三个 `BaseListCard`。
- 改成：
  - 移除 `BaseTabs`，改为页面内纵向堆叠的 `TrafficCollapsibleSection` 折叠区块。
  - 区块顺序：规则列表（HTTP + L4）、证书列表、监听列表、流量统计、系统信息、同步事件。
  - 默认展开“规则”与“同步事件（当 `last_apply_status !== 'success'` 时）”，其余默认折叠。
  - 规则列表复用当前合并 HTTP/L4 的表格逻辑；证书/监听列表使用新的简单表格或 `BaseListCard` 列表，展示 id/name/状态/跳转链接。
- 为什么：折叠区块让 SRE 在首屏看到全部信息入口，按需展开，避免 tab 隐藏关键内容；同时保留现有复杂流量统计功能作为一个可折叠区块。

### 改动点 3：新增证书与监听计数及跳转

- 覆盖需求锚点：R3、R6
- 改哪里：`panel/frontend/src/pages/AgentDetailPage.vue` 的 hook 引入与 computed。
- 现状：详情页未调用 `useCertificates` / `useRelayListeners`，不展示证书与监听。
- 改成：
  - 引入 `useCertificates(agentId)` 与 `useRelayListeners(agentId)`。
  - 新增 computed：`certificatesCount`、`relayListenersCount`。
  - 在指标卡片区增加“证书”“监听”两张 `StatCard`。
  - 点击计数卡跳转：证书 → `/certs?agentId={id}`；监听 → `/relay-listeners?agentId={id}`（`RelayListenersPage` 暂不支持 `#id=` 搜索，只按 agent 过滤）。
- 为什么：满足 R3 指标卡片要求，且完全复用现有 hook 与列表页 query 参数，不改动后端。

### 改动点 4：补齐轻量运维操作入口

- 覆盖需求锚点：R4
- 改哪里：`panel/frontend/src/pages/AgentDetailPage.vue` 顶部状态栏 `#header-right` 与 `<script setup>`。
- 现状：详情页没有强制同步、复制注册命令、删除节点入口；规则跳转已存在，证书/监听跳转缺失。
- 改成：
  - 强制同步：调用 `applyConfig(agentId.value)`，本地 `applying` ref 控制 loading，成功/失败用 `messageStore` 提示，成功后 `queryClient.invalidateQueries(['agents'])`。
  - 复制注册命令：复用 `AgentsPage.vue` 的命令拼接逻辑（基于 `systemInfo.master_register_token` 与 `window.location.origin`），抽取为 `useJoinCommand()` composable 或内联函数；复制成功后 toast 提示。
  - 删除节点：使用 `useDeleteAgent()` hook + `DeleteConfirmDialog.vue`，确认后 `deleteAgent.mutate(agentId.value)`，onSuccess 中 `router.push('/agents')`。
  - 规则/证书/监听跳转：HTTP 规则 → `/rules?agentId={id}&search=#id={rule.id}`；L4 规则 → `/l4?...`；证书 → `/certs?agentId={id}`；监听 → `/relay-listeners?agentId={id}`。
- 为什么：把列表页已有的运维能力下沉到详情页，符合 SRE 排障路径；复用已有 API/hook/组件，避免重复实现。

### 改动点 5：全局设计 token 与暗色/响应式适配

- 覆盖需求锚点：R5、R7
- 改哪里：`panel/frontend/src/styles/themes.css`、`panel/frontend/src/pages/AgentDetailPage.vue` 的 `<style scoped>`。
- 现状：
  - `themes.css` 已有共享 token 与主题色覆盖。
  - `AgentDetailPage.vue` 当前使用部分硬编码 px/rem 与 `900px`/`720px` media query。
- 改成：
  - 如需新增 token（例如 `--space-2-5`、卡片阴影 `--shadow-sm` 等），在 `themes.css` 的共享段补充，确保所有主题可用。
  - `AgentDetailPage.vue` 所有新增/重构样式使用 `--color-*`、`--space-*`、`--radius-*`、`--font-*` 变量。
  - 新增响应式 media query：`@media (max-width: 1280px)`、`1024px`、`768px`、`375px`，控制顶部状态栏操作按钮换行、指标网格列数（4→2→1）、折叠区块内边距与字号。
  - 暗色模式无需改动切换逻辑，依赖 `data-theme` + `themes.css` 变量即可。
- 为什么：暗色/响应式能力已经由主题系统提供，只需要在页面级补齐断点和 token 使用；不引入新的主题机制。

### 改动点 6：最小化文案重命名（i18n 准备）

- 覆盖需求锚点：R6
- 改哪里：`panel/frontend/src/pages/AgentDetailPage.vue` 与新增 `panel/frontend/src/constants/agentDetailLabels.js`（或类似）。
- 现状：页面所有文案硬编码中文，无 i18n 基础设施。
- 改成：
  - 在 `constants/agentDetailLabels.js` 中定义一个中文文案对象，集中管理节点详情页所有可见文案（状态栏标签、指标标签、折叠区块标题、操作按钮、空状态）。
  - 模板中通过 `detailLabels.httpRules` 等方式引用，避免分散硬编码。
- 为什么：在不引入完整 vue-i18n 的前提下，满足“文案可重命名”的需求，并为未来完整 i18n 预留结构。

### 改动点 7：更新单元测试

- 覆盖需求锚点：R7
- 改哪里：`panel/frontend/src/pages/AgentDetailPage.test.js`。
- 现状：17 个测试用例基于当前 tab 结构。
- 改成：
  - 更新选择器：tab 切换测试改为折叠区块展开/收起测试。
  - 新增测试：首屏状态栏展示节点名/状态/模式/版本/最后心跳；指标卡片展示规则/证书/监听计数；操作按钮（推送配置、复制命令、删除）存在且可点击；删除后跳转 `/agents`。
  - 保持流量统计、系统信息、规则列表等核心场景的覆盖。
- 为什么：验证重构后的页面行为与验收标准一致，避免回归。

## 7. 关键决策与取舍

| 决策 | 选择 | 放弃的选项 | 为什么这么定 |
|---|---|---|---|
| 计数指标卡组件 | `StatCard`（规则/证书/监听/同步状态） | `AgentMetricTile` 全部统一 | `StatCard` 更适合纯数字计数与跳转；`AgentMetricTile` 保留给带进度条的资源指标。 |
| 规则/监听/证书计数来源 | 复用 `agent.http_rules_count` / `l4_rules_count`；证书/监听通过新增 hook 取 length | 新增后端聚合字段 | 不动后端，且 hook 已存在。 |
| 错误/告警数 | 以同步失败状态 badge + `last_apply_message` 错误块替代数值计数 | 新增后端错误聚合 | 当前无错误数字段，展示同步失败信息已足够支撑排障。 |
| 主题切换 | 沿用现有 `ThemeContext` + `themes.css`；不改造 `ThemeToggle.vue` | 引入新主题 store 或 Tailwind darkMode | 现有机制已能满足暗色模式；`ThemeToggle.vue` 与 `ThemeSelector.vue` 不一致属于已知技术债，但非本次范围。 |
| i18n | 新增最小文案对象文件 | 引入 vue-i18n 完整库 | 项目无 i18n 基础设施，本次聚焦页面重构，最小对象即可满足“文案可重命名”。 |
| 页面内操作布局 | 顶部状态栏 `#header-right` 集中放置 | 分散到各折叠区块 | SRE 首屏即可操作，符合“3 秒可扫描”目标。 |

## 8. 影响面

- 契约：
  - 新增前端 hook 调用 `GET /agents/:id/certificates` 与 `GET /agents/:id/relay-listeners`；仅读取，不改变后端契约。
  - 复制注册命令依赖 `GET /info` 返回的 `master_register_token`，与现有 `AgentsPage.vue` 一致。
- 数据或状态变化：
  - 页面局部新增 `applying`、`copied`、`deletingAgent` 等 ref；删除成功后跳转 `/agents`。
  - 强制同步成功后 invalidate `['agents']`，让详情页数据刷新。
- 专项链路：
  - 暗色/响应式：依赖 `ThemeContext` 与 `themes.css` 变量链路；需验证新增变量在各主题下均有值。
  - 测试：需同步更新 `AgentDetailPage.test.js` 的选择器与断言。
- 交付物影响：无
- 后续约束：
  - 新增文案集中对象后，未来扩展多语言只需替换该对象。
  - 若后续要在其它页面复用计数卡/折叠区块，应进一步抽象为通用组件。

## 9. 风险、验证与回滚

| 风险 / 目标 | 怎么验证或缓解 | 预期证据 |
|---|---|---|
| 重构后首屏信息仍不满足 3 秒可扫描 | 人工走查 + 截图：首屏可见节点名、状态、模式、版本、最后心跳、4 资源指标、4-5 业务计数、操作按钮 | 截图 / Lighthouse |
| 暗色模式样式断裂 | 切换 `sakura-night` / `neko-dark` 主题，检查状态栏、指标卡、折叠区块边框/背景/文字对比度 | 截图对比 |
| 响应式布局断裂 | 在 1280/1024/768/375px 宽度下截图检查无溢出/重叠 | 浏览器 DevTools 截图 |
| 新增证书/监听 hook 增加请求失败面 | 在测试中用 MSW/模拟失败响应检查错误占位 | 测试用例 |
| 删除/强制同步操作回归 | 单元测试覆盖按钮点击与 API 调用 | 测试用例 |
| 硬编码常量重新出现 | 代码审查：搜索 `#` 与新增 `px` 常量 | PR diff 审查 |

- 验证思路：以 `npm run test` 跑 `AgentDetailPage.test.js` 全绿为基础，辅以浏览器手动截图走查暗色/响应式。
- 回滚 / 恢复：
  - 方案只改前端页面与样式，未改后端；若出现严重回归，可直接 revert 前端 commit 或回退到上一个稳定构建。
  - 设计 token 新增变量不影响旧页面；若变量缺失导致断裂，可回退变量添加或在页面中使用 fallback。

## 10. 待确认与讨论记录

- 方案讨论输入：未触发。`01-exploration.md` 已提供充分证据，无关键业务取舍、风险接受、材料缺口或方案方向不唯一的问题；等待正式 02 方案确认门。
- 待确认：无
- 用户已确认：R1-R7 已在需求澄清阶段确认。
- 被推翻的方案：无
