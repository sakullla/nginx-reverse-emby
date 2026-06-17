# 执行计划：搜索 #id= 跨服务器直达

## 1. 执行总览
- source_solution：`docs/sakullla-workflow/2026-06-17-search-id-cross-server/02-technical-solution.md`
- source_materials：用户当前明确需求：让 `#id=` 搜索跨服务器直达（功能页跳转+定位，全局搜索结果列表）
- requirement_anchor_summary：R1-R9 全部 confirmed；R4 多候选为防御性设计（后端 ID 全局唯一，当前不触发）
- top_project：nginx-reverse-emby
- modules：panel/frontend/src/hooks、panel/frontend/src/utils、panel/frontend/src/components、panel/frontend/src/pages
- complexity_mode：simple
- complexity_basis：5 个 Work Item，纯前端改动，无后端变更，无数据迁移，风险集中在跨模块逻辑复用
- execution_strategy：T1（基础设施）→ T2/T3 可部分并行 → T4 依赖 T3 → T5 验证门
- execution_profile：
  - recommended_default：balanced
  - parallel_candidates：T2 与 T3（T2 独立改 GlobalSearch，T3 依赖 T1 改功能页，无文件冲突）
  - local_only_tasks：T5（验证门，需全量构建）
  - conflict_resources：RulesPage.vue、L4RulesPage.vue、CertsPage.vue（T3 和 T4 都改，T4 必须依赖 T3）
- standards_lookup：
  - status：empty
  - source_scope：developer_standards
  - query_basis：02 改动点，涉及 Vue 3 composable、Vue 组件、前端 hooks
  - refs：无
  - notes：t8k_standards_sync 返回 missing — 无 source 配置 standards_path
- artifact_state：delivery_docs = not_needed（无正式交付文档需求）

## 2. 任务拓扑
- order：T1 → T2（独立）/ T3（依赖 T1）→ T4（依赖 T3）→ T5（验证门）
- shared_owners：useIdSearch composable（T1 创建，T2/T3 消费）
- critical_path：T1 → T3 → T4 → T5
- checkpoints：T3 完成后（核心功能可用）；T5 验证门

## 3. Task Recipes

### T1 新建 useIdSearch composable + scrollToAndHighlight 工具函数
- purpose：为功能页和全局搜索提供共享的 #id= 解析、跨 agent 记录查找、滚动高亮定位能力
- requirement_anchors：R1, R2, R3, R4, R7, R8
- depends_on：[]
- allowed_files：
  - panel/frontend/src/hooks/useIdSearch.js
  - panel/frontend/src/utils/scrollHighlight.js
- forbidden_files：
  - panel/frontend/src/pages/*
  - panel/frontend/src/components/*
- risk_complexity：
  - score：2
  - level：low
  - drivers：新建文件 2 个 +0；纯展示逻辑 +0；线性局部变更 +0；同仓有相似 composable 模式 +0；已有精准测试或容易补 +0
- implementation_outline：
  1. `useIdSearch.js`：导出 `parseIdQuery(input)` 返回 `{ isIdSearch, id }` 或 null；`findRecordInData(allData, id, type)` 按 id 在 fetchAllAgents* 返回结构中查找记录+来源 agentId
  2. `scrollHighlight.js`：导出 `scrollToAndHighlight(element)` — `scrollIntoView({ behavior: 'smooth', block: 'center' })` + 添加 CSS class 触发高亮动画（~1.5s 后移除）
- acceptance：
  - `parseIdQuery('#id=123')` 返回 `{ isIdSearch: true, id: '123' }`
  - `parseIdQuery('keyword')` 返回 null
  - `findRecordInData` 在 fetchAllAgentsRules 返回结构中正确找到目标记录
  - `scrollToAndHighlight` 对 DOM 元素执行 scrollIntoView 并添加高亮 class
- acceptance_anchor_map：parseIdQuery → R7（精确匹配语法兼容）；findRecordInData → R1+R3（跨 agent 查找）；scrollToAndHighlight → R2+R8（定位呈现）
- verification_scope：
  - kind：task_verify
  - covers_tasks：T1
  - immediate_commands：`cd panel/frontend && npx vitest run src/hooks/__tests__/useIdSearch.test.js src/utils/__tests__/scrollHighlight.test.js`
  - deferred_to：无
- verify：
  - commands：`cd panel/frontend && npx vitest run src/hooks/__tests__/useIdSearch.test.js src/utils/__tests__/scrollHighlight.test.js`
  - expected_evidence：测试通过，parseIdQuery/findRecordInData/scrollToAndHighlight 行为正确
- tests：
  - unit_test：required
  - responsibility：覆盖 parseIdQuery 正则匹配（#id= 命中/未命中/边界）、findRecordInData 单命中/未命中、scrollToAndHighlight DOM 操作
  - minimum_command：`cd panel/frontend && npx vitest run src/hooks/__tests__/useIdSearch.test.js src/utils/__tests__/scrollHighlight.test.js`
  - not_applicable_reason：
  - key_assertions：parseIdQuery 正确识别 #id= 语法；findRecordInData 跨 agent 数据结构正确定位记录
- review_mode：self_check，原因：纯新建文件，low 风险，逻辑清晰
- review_focus：composable API 设计是否合理；正则是否与现有 /^#id=(\S+)$/ 一致；scrollHighlight 是否有副作用
- standards_refs：无
- boundary_extension_hints：无
- delegation：allowed，原因：自包含、边界清晰、verify 可独立跑
- commit_policy：per_task
- recovery_notes：新建文件，无前置依赖，可独立恢复

### T2 全局搜索 #id= 识别与精确过滤
- purpose：在 GlobalSearch.vue 的 doSearch 中新增 #id= 识别分支，跨所有 agent 按 id 精确过滤并以结果列表展示
- requirement_anchors：R5, R6, R7
- depends_on：[]
- allowed_files：
  - panel/frontend/src/components/GlobalSearch.vue
- forbidden_files：
  - panel/frontend/src/pages/*
  - panel/frontend/src/hooks/*
- risk_complexity：
  - score：2
  - level：low
  - drivers：1 个文件 +0；普通业务规则 +1；线性局部变更 +0；同仓有清晰相似实现 +0；已有精准测试或容易补 +0
- implementation_outline：
  1. 在 doSearch 函数开头（line ~183 之前）新增 `#id=` 正则分支：`const idMatch = val.match(/^#id=(\S+)$/)`
  2. 命中时：遍历已拉取的 rulesResults/l4Results/certsResults/relayResults，按 id 精确匹配，构建结果列表（复用现有 results 结构）
  3. 结果列表中标注 `_type` 和 `agentId`，复用现有 navigateToItem 跳转逻辑
  4. 未命中时：走现有 includes 子串匹配逻辑（不变）
- acceptance：
  - 全局搜索框输入 `#id=123` 后，结果列表显示跨所有 agent 的 id 命中项
  - 点击结果正常跳转到对应 agent 功能页
  - 输入普通关键词行为不变
- acceptance_anchor_map：#id= 精确过滤 → R5（全局搜索走结果列表）；普通关键词不变 → R6；正则兼容 → R7
- verification_scope：
  - kind：task_verify
  - covers_tasks：T2
  - immediate_commands：`cd panel/frontend && npx vitest run src/components/__tests__/GlobalSearch.test.js`
  - deferred_to：无
- verify：
  - commands：`cd panel/frontend && npx vitest run src/components/__tests__/GlobalSearch.test.js`
  - expected_evidence：测试通过，#id= 分支正确过滤
- tests：
  - unit_test：required
  - responsibility：覆盖 doSearch #id= 分支（命中/未命中/跨 agent 数据结构遍历）
  - minimum_command：`cd panel/frontend && npx vitest run src/components/__tests__/GlobalSearch.test.js`
  - not_applicable_reason：
  - key_assertions：#id= 输入触发精确过滤而非 includes；结果列表包含 agentId 信息
- review_mode：self_check，原因：单文件改动，low 风险，逻辑简单（在已有搜索流程中新增分支）
- review_focus：#id= 分支是否正确插入 doSearch 开头；是否影响普通搜索性能；结果列表结构是否与现有兼容
- standards_refs：无
- boundary_extension_hints：无
- delegation：allowed，原因：自包含单文件、边界清晰
- commit_policy：per_task
- recovery_notes：单文件改动，可独立恢复

### T3 功能页 #id= 跨 agent 解析 + 跳转
- purpose：RulesPage / L4RulesPage / CertsPage 的 filteredXxx 中，#id= 未命中当前 agent 时调用 fetchAllAgents* 跨 agent 搜索，找到后跳转到目标 agent 并定位
- requirement_anchors：R1, R2, R3, R4, R8, R9
- depends_on：[T1]
- allowed_files：
  - panel/frontend/src/pages/RulesPage.vue
  - panel/frontend/src/pages/L4RulesPage.vue
  - panel/frontend/src/pages/CertsPage.vue
- forbidden_files：
  - panel/frontend/src/components/*
  - panel/frontend/src/hooks/useIdSearch.js
  - panel/frontend/src/utils/scrollHighlight.js
- risk_complexity：
  - score：4
  - level：medium
  - drivers：3 个文件 +1；普通业务规则 +1；跨模块 fetchAllAgents* 复用 +1；同仓有清晰相似实现 +0；需搭建 fixture/mock +1
- implementation_outline：
  1. 各页 filteredXxx computed 中：#id= 匹配当前 agent 未命中时，异步调用 `useIdSearch` + `fetchAllAgents*` 跨 agent 搜索
  2. 找到匹配：`router.push({ path, query: { agentId: targetAgentId, search: '#id=xxx' } })` — 复用现有跳转模式
  3. 多匹配（防御性）：设置 state 触发候选列表弹出（T4 实现 UI）
  4. 无匹配：显示友好空状态提示
  5. 数据加载后触发 scrollToAndHighlight（通过 watch 监听 filteredXxx 结果变化）
- acceptance：
  - 功能页输入 `#id=xxx`，当前 agent 有该 id → 停留+过滤+滚动+高亮
  - 功能页输入 `#id=xxx`，当前 agent 无、另一 agent 有 → 自动跳转并定位
  - 功能页输入 `#id=xxx`，所有 agent 均无 → 友好空状态
  - 跳转后 selectedAgentId 正确切换
- acceptance_anchor_map：当前命中+定位 → R2+R8；跨 agent 跳转 → R1+R3+R9；多候选 → R4（防御性）
- verification_scope：
  - kind：task_verify
  - covers_tasks：T3
  - immediate_commands：`cd panel/frontend && npx vitest run src/pages/__tests__/RulesPage.test.js src/pages/__tests__/L4RulesPage.test.js src/pages/__tests__/CertsPage.test.js`
  - deferred_to：T5
- verify：
  - commands：`cd panel/frontend && npm run build`
  - expected_evidence：构建成功，无编译错误
- tests：
  - unit_test：deferred_to_gate
  - responsibility：T5 验证门覆盖：跨 agent 解析逻辑、跳转参数传递、空状态处理
  - minimum_command：
  - not_applicable_reason：
  - key_assertions：#id= 未命中时正确触发跨 agent 搜索；router.push 参数正确；空状态友好
- review_mode：delegate，原因：medium 风险，跨 3 个文件 + 跨模块调用 fetchAllAgents*
- review_focus：跨 agent 异步搜索的竞态处理（快速输入时取消旧请求）；router.push 参数与现有跳转模式一致性；scrollToAndHighlight 触发时机
- standards_refs：无
- boundary_extension_hints：无
- delegation：allowed，原因：文件边界清晰（3 个页面文件），verify 可独立跑
- commit_policy：per_task
- recovery_notes：依赖 T1（useIdSearch），T1 未完成时不可开始

### T4 候选列表 UI（防御性）
- purpose：跨 agent 搜索返回多个匹配时弹出候选列表（当前 ID 全局唯一不触发，作为防御性设计）
- requirement_anchors：R4
- depends_on：[T3]
- allowed_files：
  - panel/frontend/src/components/IdCandidateModal.vue
  - panel/frontend/src/pages/RulesPage.vue
  - panel/frontend/src/pages/L4RulesPage.vue
  - panel/frontend/src/pages/CertsPage.vue
- forbidden_files：
  - panel/frontend/src/hooks/*
  - panel/frontend/src/utils/*
  - panel/frontend/src/components/GlobalSearch.vue
- risk_complexity：
  - score：2
  - level：low
  - drivers：1 个新文件 + 3 个已有文件（小幅改动）+1；纯展示/弹窗 +0；线性局部变更 +0；有清晰 UI 模式可参考 +0；容易补测试 +0
- implementation_outline：
  1. 新建 `IdCandidateModal.vue`：接收 candidates 数组（每项含 agentId、record、recordType），展示列表，用户选择后 emit 选中项
  2. 各页面在 T3 新增的跨 agent 搜索逻辑中，多匹配时设置 state 触发 Modal 显示
  3. 用户选择后执行与 T3 单命中相同的跳转逻辑
- acceptance：
  - 跨 agent 多命中时弹出候选列表（当前 ID 模型不触发，可通过 mock 验证）
  - 候选列表显示来源服务器
  - 选择后正确跳转
- acceptance_anchor_map：候选列表 → R4
- verification_scope：
  - kind：batch_verify
  - covers_tasks：T3, T4
  - immediate_commands：`cd panel/frontend && npm run build`
  - deferred_to：T5
- verify：
  - commands：`cd panel/frontend && npm run build`
  - expected_evidence：构建成功
- tests：
  - unit_test：deferred_to_gate
  - responsibility：T5 验证门覆盖：Modal 渲染、选择回调、跳转逻辑
  - minimum_command：
  - not_applicable_reason：
  - key_assertions：Modal 正确接收和显示候选数据；选择后触发正确跳转
- review_mode：self_check，原因：low 风险，纯 UI 组件 + 简单状态管理
- review_focus：Modal 组件 API 设计；与 T3 跨 agent 搜索逻辑的衔接；当前 ID 模型下不触发的保证
- standards_refs：无
- boundary_extension_hints：无
- delegation：allowed，原因：自包含 UI 组件，边界清晰
- commit_policy：per_task
- recovery_notes：依赖 T3，T3 未完成时不可开始

### T5 构建验证 + 回归
- purpose：全量构建验证 + 确保普通搜索、#id= 语法、点击跳转兼容性不退化
- requirement_anchors：R1-R9 全量回归
- depends_on：[T2, T4]
- allowed_files：
  - （空 — 纯验证任务，不修改文件）
- forbidden_files：
  - panel/frontend/src/*
- risk_complexity：
  - score：0
  - level：low
  - drivers：无生产代码变更 +0
- implementation_outline：
  1. 运行 `npm run build` 确保无编译错误
  2. 运行全量前端测试（如有）
  3. 手动冒烟：全局搜索 #id=、功能页 #id=、普通关键词、点击跳转
- acceptance：
  - `npm run build` 成功
  - 全局搜索 #id= 跨 agent 显示结果列表
  - 功能页 #id= 当前 agent 命中时停留+过滤
  - 功能页 #id= 跨 agent 单命中时跳转+定位
  - 普通关键词搜索行为不变
  - 现有点击跳转行为不变
- acceptance_anchor_map：全量回归 → R1-R9
- verification_scope：
  - kind：final_verify
  - covers_tasks：T1, T2, T3, T4
  - immediate_commands：`cd panel/frontend && npm run build && npm run test`
  - deferred_to：无
- verify：
  - commands：`cd panel/frontend && npm run build && npm run test`
  - expected_evidence：构建成功 + 测试全通过
- tests：
  - unit_test：not_applicable
  - responsibility：验证任务不新增测试，只运行已有测试
  - minimum_command：
  - not_applicable_reason：纯验证任务，不修改代码
  - key_assertions：构建成功；现有测试不退化
- review_mode：delivery_gate_only，原因：纯验证任务，无 diff
- review_focus：构建结果；测试覆盖范围
- standards_refs：无
- boundary_extension_hints：无
- delegation：local_only，原因：验证任务需全量构建，不适合委派
- commit_policy：none
- recovery_notes：纯验证，可重复执行
