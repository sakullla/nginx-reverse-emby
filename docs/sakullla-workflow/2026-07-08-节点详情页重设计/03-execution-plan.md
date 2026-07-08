# 执行计划：节点详情页重设计

## 1. 执行总览

- source_solution：`docs/sakullla-workflow/2026-07-08-节点详情页重设计/02-technical-solution.md`
- source_materials：
  - `docs/requirements/2026-07-08-节点详情页重设计.md`（已确认需求文档，R1-R7 confirmed）
  - `docs/sakullla-workflow/2026-07-08-节点详情页重设计/01-exploration.md`（代码探索主稿）
- requirement_anchor_summary：R1-R7 全部为 confirmed
- top_project / modules：panel/frontend/src/pages/AgentDetailPage.vue、panel/frontend/src/styles/、panel/frontend/src/components/base/、panel/frontend/src/hooks/、panel/frontend/src/constants/
- complexity_mode：multi_batch
- complexity_basis：
  - 核心页面重构涉及状态栏、指标卡、可折叠区块、操作入口、响应式布局，risk_complexity 总体 medium。
  - 单元测试需要同步更新，且存在暗色/响应式视觉回归风险。
  - 任务间存在文件依赖（主要集中在 `AgentDetailPage.vue`），无法大规模并行。
- execution_strategy：按 Batches 顺序执行；同一 Batch 内若存在 disjoint 文件可本地并行，但本计划以顺序交付为主，降低同一文件冲突风险。
- execution_profile：
  - recommended_default：balanced
  - parallel_candidates：无（核心实现均集中在 `AgentDetailPage.vue`，顺序编辑收益高于并行）
  - local_only_tasks：T1、T2、T3、T4、T5、T6（共享主文件 + 需要连续视觉验证）
- standards_lookup：
  - status：empty
  - source_scope：developer_standards
  - query_basis：Vue 3 / UnoCSS / Vitest 前端组件重构
  - refs：无
  - notes：项目未配置 standards_path，未命中开发规范；执行期按项目现有代码风格与 `CLAUDE.md` 约束执行。
- artifact_state：delivery_docs = not_needed（本次交付为代码变更与测试，无独立交付文档）

## 2. 任务拓扑

- order：T1 -> T2 -> T3 -> T4 -> T5 -> T6
- shared_owners：
  - `AgentDetailPage.vue` 由 T2/T3/T4 顺序修改，T2 为首个 owner，T3/T4 以 `base_ref=task:T2/T3` 消费。
  - `themes.css` / `index.css` 由 T1 修改，T2/T3/T4 只读消费。
- critical_path：T2 -> T3 -> T4 -> T5 -> T6（页面重构主体）
- checkpoints：
  - CP1（B1 结束）：顶部状态栏与指标卡片渲染正常、token 无缺失。
  - CP2（B2 结束）：可折叠区块与操作入口功能正常。
  - CP3（B3 结束）：单元测试与构建全绿。

## 3. Batches

### B1 布局基础

- goal：补齐全局设计 token，完成顶部状态栏与指标卡片重构，建立响应式网格基础。
- tasks：T1、T2
- entry_condition：02 已确认
- exit_checkpoint：CP1
- shared_constraints：
  - 所有新增/修改样式必须基于 `themes.css` 变量，不得引入 `#xxx` 或新 `px` 硬编码常量。
  - `AgentDetailPage.vue` 的修改需保留现有 API hook 调用与字段消费。
- resume_hint：从当前 batch header 读取，回退到 `AgentDetailPage.vue` 当前状态与 `themes.css` 变更。

### B2 内容与操作

- goal：将 tab 结构替换为可折叠详情区块，并补齐强制同步、复制注册命令、删除/注销、跳转等操作入口。
- tasks：T3、T4
- entry_condition：CP1 通过
- exit_checkpoint：CP2
- shared_constraints：
  - 操作按钮使用现有 API 函数与 hook，不得新增后端接口。
  - 删除/注销需二次确认并跳转回列表页。
- resume_hint：从当前 batch header 读取，回退到 `AgentDetailPage.vue` 当前状态。

### B3 测试与验证

- goal：更新单元测试以覆盖新布局与操作，运行最终验证门。
- tasks：T5、T6
- entry_condition：CP2 通过
- exit_checkpoint：CP3
- shared_constraints：
  - 测试断言只覆盖本需求锚点对应的行为，不引入无关重构。
  - 最终验证门需覆盖 `npm run test` 与 `npm run build`。
- resume_hint：从当前 batch header 读取，回退到 `AgentDetailPage.test.js` 当前状态。

## 4. Task Recipes

### T1 补齐全局设计 token 与响应式工具

- purpose：为节点详情页重构提供必要的共享设计 token，确保暗色模式与响应式有变量可用。
- requirement_anchors：R5
- depends_on：无
- batch：B1
- allowed_files：
  - panel/frontend/src/styles/themes.css
  - panel/frontend/src/styles/index.css
- forbidden_files：
  - panel/frontend/src/pages/AgentDetailPage.vue
  - panel/frontend/src/components/**/*.vue
  - panel/frontend/src/hooks/**/*.js
- risk_complexity：
  - score：2
  - level：low
  - drivers：仅 CSS 变量与工具类补充，无业务逻辑；风险为变量命名冲突或暗色主题漏覆盖。
- implementation_outline：
  1. 检查 `themes.css` 是否已有足够 token；如缺少卡片阴影、小间距、响应式断点变量，按需补充。
  2. 在 `index.css` 补充 768px / 375px 响应式工具类或 media query 片段（若当前无）。
  3. 确保新增变量在所有主题（含 `sakura-night`、`neko-dark`）下均有取值。
- acceptance：
  - A1：`themes.css` 中新增/使用的 token 在所有主题下有效。
  - A2：`index.css` 包含 768px 与 375px 断点工具类或示例。
- acceptance_anchor_map：
  - A1 -> R5
  - A2 -> R5
- verification_scope：
  - kind：task_verify
  - covers_tasks：T1
  - minimum_command_coverage：无
  - immediate_commands：无（CSS 变量通过后续页面 task 间接验证）
  - deferred_to：T6
- verify：
  - commands：无
  - expected_evidence：diff 中无 `#xxx` 或新增 `px` 硬编码常量
- tests：
  - unit_test：not_applicable
  - responsibility：纯 CSS 变量与工具类声明，无自定义逻辑
  - minimum_command：
  - not_applicable_reason：纯 CSS 变量与工具类声明，无自定义逻辑。
  - key_assertions：
    - 新增 token 变量命名与现有 themes.css 风格一致
    - 暗色主题下新增变量均有取值
    - 无新增 #xxx 或 px 硬编码常量
- review_mode：self_check
- review_focus：变量命名一致性、暗色主题覆盖、无新增硬编码常量。
- standards_refs：无（standards_lookup 未命中）
- boundary_extension_hints：无
- delegation：local_only
- commit_policy：per_task
- recovery_notes：若后续任务发现 token 不足，可回退到 T1 补充。

### T2 重构顶部状态栏与指标卡片区

- purpose：将 `AgentDetailPage.vue` 顶部改为“状态栏 + 指标卡片”布局，实现首屏健康信息聚合。
- requirement_anchors：R1、R2、R3、R5、R6、R7
- depends_on：T1
- batch：B1
- allowed_files：
  - panel/frontend/src/pages/AgentDetailPage.vue
  - panel/frontend/src/constants/agentDetailLabels.js
- forbidden_files：
  - panel/frontend/src/pages/AgentDetailPage.test.js
  - 后端代码
- risk_complexity：
  - score：5
  - level：medium
  - drivers：修改核心页面模板与多个 computed；涉及组件组合、响应式网格、计数来源判断。
- implementation_outline：
  1. 新建 `constants/agentDetailLabels.js`，集中管理节点详情页中文文案。
  2. 在 `AgentDetailPage.vue` 中保留 `useAgents`、`useAgentStats` 等 hook；移除顶部旧的分散统计文案。
  3. 用 `BaseListCard` 重构顶部摘要卡：
     - `#header-left`：节点名、`AgentStatusBadge`、模式 `BaseBadge`、版本、最后心跳、标签。
     - `#header-right`：预留操作按钮位置（由 T4 填充）。
  4. body 分为两行指标网格：
     - 资源指标：`AgentMetricTile` CPU/内存/磁盘/网络。
     - 业务计数：`StatCard` HTTP 规则数、L4 规则数、证书数、监听数、同步状态；计数来源使用 `agent.http_rules_count` / `l4_rules_count` 与新增 `useCertificates` / `useRelayListeners` length。
  5. 添加 1280/1024/768/375 响应式 grid 列数切换。
- acceptance：
  - A1：首屏可见节点名、状态 badge、模式 badge、版本、最后心跳。
  - A2：首屏可见 CPU/内存/磁盘/网络指标卡与规则/证书/监听计数卡。
  - A3：计数卡数值正确（与后端字段或 hook 数组长度一致）。
  - A4：响应式下无溢出/重叠。
  - A5：无新增硬编码 `#xxx` 或 `px` 常量。
- acceptance_anchor_map：
  - A1 -> R2、R3、R7
  - A2 -> R3、R5、R7
  - A3 -> R3、R6
  - A4 -> R5、R7
  - A5 -> R5
- verification_scope：
  - kind：task_verify
  - covers_tasks：T2
  - minimum_command_coverage：无
  - immediate_commands：无
  - deferred_to：T6
- verify：
  - commands：无
  - expected_evidence：页面渲染截图 / 手动走查
- tests：
  - unit_test：deferred_to_gate
  - responsibility：T6 最终验证门覆盖首屏状态栏与指标卡渲染断言
  - minimum_command：
  - not_applicable_reason：
  - key_assertions：
    - 首屏状态栏展示节点名、状态 badge、模式 badge、版本、最后心跳
    - 指标卡展示 CPU/内存/磁盘/网络及规则/证书/监听计数
    - 计数来源与后端字段或 hook 数组长度一致
    - 响应式下指标卡无溢出/重叠
    - 无新增 #xxx 或 px 硬编码常量
- review_mode：delegate
- review_focus：组件组合正确性、计数来源、响应式 grid、文案集中对象、无新增硬编码常量。
- standards_refs：无
- boundary_extension_hints：无
- delegation：local_only
- commit_policy：per_task
- recovery_notes：若 `StatCard` 不适合计数场景，可回退到 `AgentMetricTile`。

### T3 用可折叠详情区块替换 tab 结构

- purpose：把“规则/流量/系统信息”tab 改为纵向可折叠区块，提升 SRE 信息扫描效率。
- requirement_anchors：R1、R3
- depends_on：T2
- batch：B2
- allowed_files：
  - panel/frontend/src/pages/AgentDetailPage.vue
- forbidden_files：
  - panel/frontend/src/pages/AgentDetailPage.test.js
  - panel/frontend/src/components/base/BaseTabs.vue（只读，不修改）
- risk_complexity：
  - score：4
  - level：medium
  - drivers：页面结构大改，需保留现有流量统计功能；折叠状态管理增加局部状态。
- implementation_outline：
  1. 移除 `BaseTabs` 与 tab 状态。
  2. 引入 `TrafficCollapsibleSection.vue`。
  3. 按顺序添加折叠区块：规则列表（默认展开）、证书列表、监听列表、流量统计、系统信息、同步事件（当 `last_apply_status !== 'success'` 时默认展开）。
  4. 规则列表复用当前 HTTP/L4 合并表格；证书/监听列表使用简单列表或 `BaseListCard` 展示。
  5. 各区块标题使用 `agentDetailLabels.js` 文案。
- acceptance：
  - A1：页面不再使用 `BaseTabs`，改为纵向折叠区块。
  - A2：规则、证书、监听、流量统计、系统信息、同步事件区块可见。
  - A3：规则区块默认展开；同步事件区块在同步失败时默认展开。
  - A4：各区块展开/收起交互正常。
- acceptance_anchor_map：
  - A1 -> R3
  - A2 -> R1、R3
  - A3 -> R3
  - A4 -> R3
- verification_scope：
  - kind：task_verify
  - covers_tasks：T3
  - minimum_command_coverage：无
  - immediate_commands：无
  - deferred_to：T6
- verify：
  - commands：无
  - expected_evidence：页面交互走查 / 测试用例
- tests：
  - unit_test：deferred_to_gate
  - responsibility：T6 最终验证门覆盖折叠区块渲染与交互断言
  - minimum_command：
  - not_applicable_reason：
  - key_assertions：
    - 页面不再使用 BaseTabs，改为纵向折叠区块
    - 规则、证书、监听、流量统计、系统信息、同步事件区块可见
    - 规则区块默认展开；同步事件在同步失败时默认展开
    - 展开/收起交互正常
- review_mode：delegate
- review_focus：折叠区块顺序与默认展开逻辑、现有流量统计功能保留、状态管理。
- standards_refs：无
- boundary_extension_hints：无
- delegation：local_only
- commit_policy：per_task
- recovery_notes：若 `TrafficCollapsibleSection` 不适合，可改用 `BaseCard` + 自定义折叠逻辑。

### T4 补齐轻量运维操作入口

- purpose：在节点详情页提供强制同步、复制注册命令、删除/注销、跳转规则/证书/监听列表等操作。
- requirement_anchors：R4
- depends_on：T3
- batch：B2
- allowed_files：
  - panel/frontend/src/pages/AgentDetailPage.vue
  - panel/frontend/src/composables/useJoinCommand.js
- forbidden_files：
  - panel/frontend/src/pages/AgentDetailPage.test.js
  - 后端代码
- risk_complexity：
  - score：5
  - level：medium
  - drivers：涉及 API 调用、删除确认弹窗、剪贴板、页面跳转；操作错误可能导致误删除或状态不一致。
- implementation_outline：
  1. 在顶部摘要卡 `#header-right` 添加操作按钮：
     - “推送配置”：`applyConfig(agentId)` + `applying` ref + `messageStore` 反馈。
     - “复制注册命令”：基于 `systemInfo.master_register_token` 与 `window.location.origin` 拼接（可抽取 `useJoinCommand`）。
     - “删除节点”：`useDeleteAgent()` + `DeleteConfirmDialog`，确认后 `router.push('/agents')`。
  2. 为规则/证书/监听计数卡添加跳转：
     - HTTP 规则 → `/rules?agentId={id}&search=#id={rule.id}`
     - L4 规则 → `/l4?agentId={id}&search=#id={rule.id}`
     - 证书 → `/certs?agentId={id}`
     - 监听 → `/relay-listeners?agentId={id}`
  3. 在规则列表行保留并复用 `navigateToRule`。
- acceptance：
  - A1：首屏可见“推送配置”“复制注册命令”“删除节点”入口。
  - A2：点击“推送配置”调用 `applyConfig` 并显示 loading / success / error 反馈。
  - A3：点击“复制注册命令”将命令写入剪贴板并提示成功。
  - A4：点击“删除节点”弹出确认弹窗，确认后删除并跳转 `/agents`。
  - A5：计数卡/规则行可跳转到对应列表页并带 agentId 过滤。
- acceptance_anchor_map：
  - A1 -> R4
  - A2 -> R4
  - A3 -> R4
  - A4 -> R4
  - A5 -> R4
- verification_scope：
  - kind：task_verify
  - covers_tasks：T4
  - minimum_command_coverage：无
  - immediate_commands：无
  - deferred_to：T6
- verify：
  - commands：无
  - expected_evidence：操作交互走查 / 测试用例
- tests：
  - unit_test：deferred_to_gate
  - responsibility：T6 最终验证门覆盖操作按钮 API 调用与跳转断言
  - minimum_command：
  - not_applicable_reason：
  - key_assertions：
    - 首屏可见“推送配置”“复制注册命令”“删除节点”入口
    - “推送配置”调用 applyConfig 并显示 loading / success / error 反馈
    - “复制注册命令”将命令写入剪贴板并提示成功
    - “删除节点”弹出确认弹窗，确认后删除并跳转 /agents
    - 计数卡/规则行可跳转到对应列表页并带 agentId 过滤
- review_mode：delegate
- review_focus：API 调用正确性、删除二次确认、跳转参数、剪贴板降级处理。
- standards_refs：无
- boundary_extension_hints：无
- delegation：local_only
- commit_policy：per_task
- recovery_notes：若 `useJoinCommand` 抽取后其它页面需要复用，可后续独立提取。

### T5 更新 AgentDetailPage 单元测试

- purpose：让测试覆盖重构后的首屏状态栏、指标卡、折叠区块与操作入口。
- requirement_anchors：R7
- depends_on：T4
- batch：B3
- allowed_files：
  - panel/frontend/src/pages/AgentDetailPage.test.js
- forbidden_files：
  - panel/frontend/src/pages/AgentDetailPage.vue（只读）
- risk_complexity：
  - score：4
  - level：medium
  - drivers：测试选择器与页面结构同步更新；mock 数据需补充证书/监听字段。
- implementation_outline：
  1. 更新现有选择器：tab 切换测试改为折叠区块展开/收起测试。
  2. 新增测试：
     - 首屏渲染节点名、状态 badge、模式 badge、版本、最后心跳。
     - 指标卡片展示 HTTP/L4 规则数、证书数、监听数。
     - “推送配置”“复制注册命令”“删除节点”按钮存在；删除确认后跳转 `/agents`。
     - 折叠区块默认展开/收起行为。
  3. 保持对流量统计、规则列表、系统信息核心场景的覆盖。
- acceptance：
  - A1：`AgentDetailPage.test.js` 通过 `npm run test -- AgentDetailPage.test.js`。
  - A2：测试覆盖首屏状态栏关键信息。
  - A3：测试覆盖指标卡片计数。
  - A4：测试覆盖折叠区块交互。
  - A5：测试覆盖操作按钮行为。
- acceptance_anchor_map：
  - A1 -> R7
  - A2 -> R7
  - A3 -> R7
  - A4 -> R7
  - A5 -> R7
- verification_scope：
  - kind：task_verify
  - covers_tasks：T5
  - minimum_command_coverage：无
  - immediate_commands：npm run test -- AgentDetailPage.test.js
  - deferred_to：T6
- verify：
  - commands：npm run test -- AgentDetailPage.test.js
  - expected_evidence：测试命令输出全绿
- tests：
  - unit_test：required
  - responsibility：覆盖 `AgentDetailPage.vue` 重构后的渲染、交互、API 调用
  - minimum_command：npm run test -- AgentDetailPage.test.js
  - not_applicable_reason：
  - key_assertions：
    - 首屏展示节点名、状态 badge、模式 badge、版本、最后心跳
    - 指标卡展示 HTTP/L4 规则数、证书数、监听数
    - 折叠区块可展开/收起
    - “推送配置”按钮触发 API 调用
    - “删除节点”确认后跳转 `/agents`
- review_mode：delegate
- review_focus：测试与 02 验收标准对齐、mock 数据完整、无过度测试无关行为。
- standards_refs：无
- boundary_extension_hints：无
- delegation：local_only
- commit_policy：per_task
- recovery_notes：若测试因 T2/T3/T4 实现细节失败，可回退到对应 task 修复。

### T6 最终验证门

- purpose：运行全量前端测试与构建，确认无回归、无编译错误、无样式断裂。
- requirement_anchors：R5、R7
- depends_on：T5
- batch：B3
- allowed_files：无
- forbidden_files：
  - panel/frontend/src/pages/AgentDetailPage.vue
  - panel/frontend/src/pages/AgentDetailPage.test.js
  - panel/frontend/src/styles/themes.css
  - panel/frontend/src/styles/index.css
- risk_complexity：
  - score：2
  - level：low
  - drivers：纯验证任务，不引入新代码；风险为测试/构建环境不稳定。
- task_kind：final_verify
- implementation_outline：
  1. 在 `panel/frontend` 目录运行 `npm run test`。
  2. 运行 `npm run build`。
  3. 手动在浏览器切换 `sakura-night` / `neko-dark` 主题并检查 1280/1024/768/375px 布局（记录截图或检查清单）。
- acceptance：
  - A1：`npm run test` 全绿。
  - A2：`npm run build` 无错误。
  - A3：暗色模式下节点详情页无肉眼可见样式断裂。
  - A4：1280/1024/768/375px 宽度下关键信息不溢出、不重叠。
- acceptance_anchor_map：
  - A1 -> R7
  - A2 -> R7
  - A3 -> R5、R7
  - A4 -> R5、R7
- verification_scope：
  - kind：final_verify
  - covers_tasks：T1、T2、T3、T4、T5
  - minimum_command_coverage：T5
  - immediate_commands：npm run test；npm run build
  - deferred_to：无
- verify：
  - commands：
    - cd panel/frontend && npm run test
    - cd panel/frontend && npm run build
  - expected_evidence：命令输出全绿、构建产物生成、手动走查截图/检查清单
- tests：
  - unit_test：not_applicable
  - responsibility：最终验证门，集中执行测试与构建
  - minimum_command：
  - not_applicable_reason：本任务为验证门，测试执行已包含在 verify.commands 中。
  - key_assertions：
    - npm run test 全绿
    - npm run build 无错误
    - 暗色模式下节点详情页无肉眼可见样式断裂
    - 1280/1024/768/375px 宽度下关键信息不溢出、不重叠
- review_mode：delivery_gate_only
- review_focus：验证命令是否覆盖 T2-T5 的 deferred 测试债务、构建产物是否生成。
- standards_refs：无
- boundary_extension_hints：无
- delegation：local_only
- commit_policy：none
- recovery_notes：若验证失败，根据失败信息回退到 T1-T5 修复。
