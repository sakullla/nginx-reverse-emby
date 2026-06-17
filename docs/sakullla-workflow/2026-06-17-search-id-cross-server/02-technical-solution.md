# 技术方案：搜索 #id= 跨服务器直达

## 1. 一句话方案
- 我们要做的是：让功能页搜索框和全局搜索框的 `#id=` 语法跨所有服务器解析——功能页当前 agent 优先、未命中则自动跳转到目标 agent 并定位；全局搜索走结果列表点选。
- 方案状态：proposed
- 待用户确认：无（草案已确认）

## 2. 背景与问题

面板运维从日志/工单拿到一个记录 ID 后，必须先知道它在哪个服务器、手动切换到该服务器才能搜到。当前功能页搜索框（RulesPage、L4RulesPage、CertsPage）只在当前 agent 已加载数据上做纯前端 `#id=` 过滤，全局搜索（Ctrl+K）则完全不识别 `#id=` 语法。定位路径断裂、体验差。

后端 ID 分配模型实际上是**全局唯一**的（HTTP/L4/WireGuard 共享 `usedRuleIDs` map，`config_identity_allocator.go`），同一 ID 不会出现在不同 agent 上。但功能页搜索的前端过滤只作用于当前 agent 已加载数据，无法感知其他 agent 上的记录。

## 3. 目标与不做

- 要达成：
  - 功能页搜索框输入 `#id=xxx`：当前 agent 命中则停留+过滤+滚动+高亮；未命中则跨 agent 解析，找到后自动跳转并定位
  - 全局搜索框输入 `#id=xxx`：识别为 id 精确过滤，跨所有服务器显示结果列表，用户点选
  - 保持现有 `#id=` 精确匹配语法兼容
- 明确不做：
  - `#id=` 前缀/模糊/范围匹配
  - 自动打开详情/编辑抽屉
  - 后端 ID 分配模型改造
  - 全局搜索结果列表形态重设计
- 关键约束：
  - ID 全局唯一（HTTP/L4/WireGuard 共享）→ 同 ID 不会跨 agent 重复，R4 多候选场景在当前模型下不发生
  - 保持 `fetchAgents*` 并行拉取的容错模式（`Promise.allSettled` + `.catch`）
  - 跳转切换 `selectedAgentId` 与现有全局搜索跳转行为一致

## 4. 依据、线索与假设

- 原始材料：
  - source_ref：用户通过 requirement-clarification 提交的参数 + 逐轮澄清对话
  - requirement_anchors：R1-R9 全部 confirmed（见 `docs/requirements/2026-06-17-search-id-cross-server.md`）
- 源码证据：
  - `panel/frontend/src/components/GlobalSearch.vue#doSearch` — 全局搜索逻辑，当前不识别 `#id=`
  - `panel/frontend/src/components/GlobalSearch.vue#navigateToItem` — 结果跳转，生成 `#id=` 参数
  - `panel/frontend/src/pages/RulesPage.vue:253-257` — 功能页 `#id=` 正则匹配与过滤
  - `panel/frontend/src/pages/L4RulesPage.vue:246-250` — 同上
  - `panel/frontend/src/pages/CertsPage.vue:176-180` — 同上
  - `panel/frontend/src/api/runtime.js:401-510` — `fetchAllAgents*` 跨 agent 数据拉取
  - `panel/frontend/src/context/AgentContext.js:19-24,73-77` — `selectedAgentId` 同步与切换
  - `panel/backend-go/internal/controlplane/service/config_identity_allocator.go:123-125,188-219` — ID 全局唯一分配
- workflow 证据：`01-exploration.md` — 全局搜索/功能页/ID 分配模型/AgentContext 完整代码证据
- 官方资料/规范依据：无
- 知识线索：无
- 开发规范：待 03 前 Standards Lookup
- 假设：无

## 5. 设计思路

方案按两个入口分别设计，共享底层跨 agent 数据拉取能力。

**功能页 `#id=` 跨服务器解析**是核心链路。用户在功能页搜索框输入 `#id=xxx` 后，先在当前 agent 已加载数据上做精确过滤（现有逻辑，零延迟）。若命中，执行定位动作（滚动+高亮）。若未命中，调用 `fetchAllAgents*` 拉取所有 agent 数据，在全量数据中按 id 搜索。找到匹配后，通过 `router.push` 切换到目标 agent 对应页面，携带 `agentId` 和 `search: '#id=xxx'` 参数——复用现有的全局搜索跳转模式。目标页的 `watchEffect` 自动预填搜索框，`filteredXxx` computed 触发精确过滤，数据加载完成后执行定位。

由于后端 ID 全局唯一，同 ID 不会出现在多个 agent 上，R4 多候选场景实际不触发。但方案仍保留候选列表判断作为防御性设计：若未来 ID 模型变更导致多命中，弹出候选列表让用户选择。

**全局搜索 `#id=`** 改动较小。在 `doSearch` 函数中新增 `#id=` 识别分支：检测到正则匹配后，将搜索模式切换为按 id 精确过滤，复用已拉取的 `fetchAllAgents*` 数据，结果以列表展示（不自动跳转）。

**滚动+高亮** 作为共享的定位呈现。新增 `scrollToAndHighlight` 工具函数，在 `#id=` 精确匹配且过滤结果唯一时，自动 `scrollIntoView` 并添加 CSS 高亮动画（~1.5s fade-out）。仅在 `#id=` 场景触发，普通关键词搜索不自动滚动。

**跨 agent 解析路径选择**：前端复用 `fetchAllAgents*`，与全局搜索已有模式一致，无需后端改动。后续如有性能问题可优化为后端 API。

## 5.1 需求覆盖核验

| 需求锚点 | 方案覆盖 | 代码证据 / 探索证据 | 状态 |
|---|---|---|---|
| R1: #id= 跨所有服务器解析 | 功能页：fetchAllAgents* 跨 agent 拉取+搜索；全局：doSearch 新增 #id= 分支 | 01-exp Q2, Q7 | covered |
| R2: 当前服务器优先+过滤+滚动+高亮 | 先过滤当前 agent 数据，命中后 scrollToAndHighlight | 01-exp Q4, Q8 | covered |
| R3: 单命中跨服务器跳转+定位 | fetchAllAgents* 找到后 router.push 带 agentId+search 参数 | 01-exp Q3 | covered |
| R4: 多候选列表 | 保留判断逻辑+候选列表 UI（当前 ID 全局唯一不触发） | 01-exp Q7 | covered（防御性） |
| R5: 全局搜索 #id= 走结果列表 | doSearch 新增 #id= 分支，结果列表展示 | 01-exp Q1 | covered |
| R6: 普通关键词不变 | doSearch 仅新增 #id= 分支，原有逻辑不动 | 01-exp Q1 | covered |
| R7: 精确匹配语法兼容 | 保持 /^#id=(\S+)$/ 正则 | 01-exp Q4 | covered |
| R8: 定位=过滤+滚动+高亮，不打开详情 | scrollToAndHighlight + 无自动打开逻辑 | 01-exp Q8 | covered |
| R9: 跳转切换 selectedAgentId | router.push 带 agentId → AgentContext watch 自动同步 | 01-exp Q6 | covered |

## 6. 改动点

### 改动点 1：新建 `useIdSearch` composable — 共享 #id= 解析与跨 agent 搜索逻辑
- 覆盖需求锚点：R1, R3, R4, R7
- 改哪里：`panel/frontend/src/hooks/useIdSearch.js`（新建）
- 现状：各功能页各自内联 `#id=` 正则匹配和过滤逻辑，无跨 agent 搜索能力
- 改成：抽取共享 composable，提供 `parseIdQuery(input)` 解析 `#id=`、`searchAcrossAgents(id, currentAgentData, allAgentsData)` 跨 agent 搜索、`findRecordById(allData, id)` 按 id 定位记录+来源 agent
- 为什么：消除四处重复的正则定义；为功能页提供统一的跨 agent 解析入口；与 `fetchAllAgents*` 组合使用

### 改动点 2：功能页搜索框接入跨 agent 解析 — RulesPage / L4RulesPage / CertsPage
- 覆盖需求锚点：R1, R2, R3, R4, R8
- 改哪里：`panel/frontend/src/pages/RulesPage.vue#filteredRules`、`L4RulesPage.vue#filteredRules`、`CertsPage.vue#filteredCerts`
- 现状：`filteredXxx` computed 中 `#id=` 匹配后只在当前 agent 数据上过滤，无命中则返回空
- 改成：`#id=` 匹配后，先过滤当前 agent 数据；未命中时触发跨 agent 解析（调用 `useIdSearch` + `fetchAllAgents*`），解析完成后自动跳转（`router.push` 带 `agentId` + `search` 参数）或弹出候选列表
- 为什么：功能页是"定位"场景，用户期望输入 id 后直达，不需要手动切换服务器

### 改动点 3：全局搜索 `#id=` 识别与精确过滤
- 覆盖需求锚点：R5, R6
- 改哪里：`panel/frontend/src/components/GlobalSearch.vue#doSearch`
- 现状：`doSearch` 对所有输入做 `toLowerCase()` + `includes` 子串匹配，不识别 `#id=`
- 改成：`doSearch` 开头新增 `#id=` 正则分支——匹配后在已拉取的 `fetchAllAgents*` 数据中按 id 精确过滤，结果以列表展示（复用现有结果 UI），不自动跳转
- 为什么：全局搜索是"扫描"场景，用户期望看到列表后自行选择；与现有交互一致

### 改动点 4：滚动+高亮定位呈现
- 覆盖需求锚点：R2, R8
- 改哪里：`panel/frontend/src/utils/scrollHighlight.js`（新建）、各功能页 `filteredXxx` 结果变化后的定位触发
- 现状：过滤后无自动滚动和高亮，用户需手动滚动查找
- 改成：新增 `scrollToAndHighlight(rowElement)` 工具函数——`scrollIntoView({ behavior: 'smooth', block: 'center' })` + CSS class 高亮（`background-color` 渐变动画 ~1.5s）。在 `#id=` 精确匹配且过滤结果唯一时自动触发
- 为什么：R2/R8 要求的定位呈现；仅在 `#id=` 场景触发避免干扰普通搜索

### 改动点 5：候选列表 UI（防御性）
- 覆盖需求锚点：R4
- 改哪里：功能页（RulesPage / L4RulesPage / CertsPage）新增候选列表弹出组件
- 现状：无跨 agent 候选列表
- 改成：跨 agent 搜索返回多个匹配时（当前 ID 全局唯一不会触发），弹出候选列表标注来源服务器，用户选择后跳转
- 为什么：R4 要求；当前 ID 全局唯一不触发但作为防御性设计

## 7. 关键决策与取舍

| 决策 | 选择 | 放弃的选项 | 为什么这么定 |
|---|---|---|---|
| 跨 agent 解析路径 | 前端复用 fetchAllAgents* | 新增后端 API | 与全局搜索已有模式一致，无需后端改动，复杂度低；性能问题后续可优化 |
| R4 多候选处理 | 保留候选列表 UI（防御性） | 简化为直接跳转 | 需求已确认 R4，且未来 ID 模型可能变更；当前不触发无性能开销 |
| #id= 共享逻辑 | 新建 useIdSearch composable | 各页各自修改 | 消除四处重复，统一解析入口，后续维护成本低 |
| 滚动+高亮范围 | 仅 #id= 精确匹配触发 | 所有搜索都滚动+高亮 | 避免干扰普通关键词搜索体验 |

## 8. 影响面

- 契约：功能页搜索输入新增跨 agent 解析语义；全局搜索 `doSearch` 新增 `#id=` 分支；路由参数 `agentId` + `search` 复用已有模式
- 数据或状态变化：无持久化变化；`selectedAgentId` 切换是已有机制
- 专项链路：`fetchAllAgents*` 调用频率增加（功能页原来不调用）；需关注 agent 数量大时的性能
- 交付物影响：无
- 后续约束：`useIdSearch` composable 需在新增功能页时同步接入；若后续改为后端 API，需统一替换调用点

## 9. 风险、验证与回滚

| 风险 / 目标 | 怎么验证或缓解 | 预期证据 |
|---|---|---|
| 功能页跨 agent 解析性能 | 对比全局搜索已有 fetchAllAgents* 调用耗时；agent 数量大时可优化为后端 API | 功能页输入 #id= 后跳转耗时 |
| selectedAgentId 切换体验 | 复用已有跳转模式（全局搜索结果点击），验证数据加载流畅性 | 跳转后页面数据正确加载 |
| #id= 正则兼容性 | 保持 /^#id=(\S+)$/ 不变，现有行为回归测试 | 普通搜索、#id= 搜索均正常 |

- 验证思路：在 HTTP规则/L4规则/证书/节点页搜索框、全局搜索框中分别输入 `#id=xxx`，覆盖当前命中、跨服务器跳转、全局列表、无命中空状态场景
- 回滚/恢复：纯前端改动，不涉及数据迁移，回滚即恢复代码

## 10. 待确认与讨论记录

- 方案讨论输入：用户在主会话确认草案方向（"继续写入 02"），对 R4 防御性设计、前端复用路径、RelayListeners 支持均无异议
- 待确认：无
- 用户已确认：方案草案方向（继续写入 02）
- 被推翻的方案：无
