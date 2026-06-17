---
top_project: "nginx-reverse-emby"
local_modules:
  - "panel/frontend/src/components/GlobalSearch.vue"
  - "panel/frontend/src/pages/RulesPage.vue"
  - "panel/frontend/src/pages/L4RulesPage.vue"
  - "panel/frontend/src/pages/CertsPage.vue"
  - "panel/frontend/src/hooks/useAgentFilters.js"
  - "panel/frontend/src/context/AgentContext.js"
  - "panel/frontend/src/api/runtime.js"
  - "panel/backend-go/internal/controlplane/service/config_identity_allocator.go"
exploration_mode: "full"
---

# 01 Exploration: 搜索 #id= 跨服务器直达

## source_materials

- source_type: chat
- source_ref: 用户通过 requirement-clarification 提交的参数 + 逐轮澄清对话
- local_ref: `docs/requirements/2026-06-17-search-id-cross-server.md`
- captured_at: 2026-06-17

## requirement_anchor_coverage

| Anchor | 覆盖状态 | 代码证据 |
|--------|----------|----------|
| R1: #id= 无视当前服务器，跨所有服务器解析 | code_evidence | 功能页 #id= 只在当前 agent 数据上做前端过滤（RulesPage:253-257 等）；全局搜索不识别 #id=（GlobalSearch:183-224）；需新增跨 agent 解析逻辑 |
| R2: 当前服务器优先——命中时停留+过滤+滚动+高亮 | code_evidence | 功能页已有 #id= 精确过滤（RulesPage:253-257, L4RulesPage:246-250, CertsPage:176-180），但缺少滚动+高亮；需补充 |
| R3: 单命中跨服务器跳转+定位 | unknown | 当前无跨 agent 跳转逻辑，需新增 |
| R4: 多候选列出候选让用户挑选 | unknown | 当前无候选列表 UI，需新增 |
| R5: 全局搜索 #id= 走结果列表 | code_evidence | 全局搜索当前不识别 #id=（GlobalSearch:183-224），需在 doSearch 中添加 #id= 精确匹配分支 |
| R6: 全局搜索普通关键词不变 | code_evidence | 当前 doSearch 逻辑（GlobalSearch:187-224）按关键词做 includes 匹配，保持即可 |
| R7: 精确匹配语法 /^#id=(\S+)$/ 兼容 | code_evidence | 三页+useAgentFilters 已统一使用此正则（RulesPage:255, L4RulesPage:248, CertsPage:178, useAgentFilters:93） |
| R8: 定位=过滤+滚动+高亮，不自动打开详情 | code_evidence | 过滤已有；滚动+高亮需新增；当前无自动打开详情逻辑 |
| R9: 跳转切换 selectedAgentId | code_evidence | AgentContext.js:19-24 监听 route.query.agentId 同步到 selectedAgentId；selectAgent():73-77 更新 localStorage；页面通过 selectedOrRouteAgentId computed 合并（RulesPage:186, L4RulesPage:182, CertsPage:144） |

## 探索问题与回答

### Section A: 全局搜索现状（exp-a, global-search）

**Q1: GlobalSearch.vue 中 #id= 当前如何处理？**

全局搜索**不识别 `#id=` 语法**。输入 `#id=123` 会被当作普通子串在所有字段上做 `includes` 匹配（GlobalSearch.vue:183-224）。`#id=` 仅在点击结果跳转时作为 URL query 参数生成（GlobalSearch.vue:253-265 `navigateToItem`），目标页通过 `watchEffect` 预填到搜索框触发精确过滤。

**Q2: fetchAllAgents* 跨 agent 数据拉取？**

四个函数（runtime.js:401-510）已实现跨所有 agent 并行拉取：
- `fetchAllAgentsRules` → `Array<{ agentId, rules: NormalizedRule[] }>`
- `fetchAllAgentsL4Rules` → `Array<{ agentId, l4Rules: NormalizedL4Rule[] }>`
- `fetchAllAgentsCertificates` → `Array<{ agentId, certificates }>`
- `fetchAllAgentsRelayListeners` → `Array<{ agentId, listeners }>`

全部使用 `Promise.allSettled`，单 agent 失败不影响其它。GlobalSearch.vue 中额外包了 `.catch(() => [])` 双重容错。

**Q3: 结果点击跳转逻辑？**

`navigateToItem`（GlobalSearch.vue:253-265）按记录类型跳转到对应功能页，URL 带 `agentId` + `search: '#id=${item.id}'`。relay 类型只带 `agentId` 不带 `search`。目标页通过 `selectedOrRouteAgentId` computed 合并 `route.query.agentId || selectedAgentId.value`，`watchEffect` 预填 `route.query.search` 到 `searchQuery`。

### Section B: 功能页搜索与上下文（exp-b, page-search-context）

**Q4: 功能页 #id= 正则匹配？**

三页 + useAgentFilters **各自内联定义** `/^#id=(\S+)$/` 正则，无共享封装：
- RulesPage.vue:255 — `const idMatch = raw.match(/^#id=(\S+)$/)`
- L4RulesPage.vue:248 — 同上
- CertsPage.vue:178 — 同上
- useAgentFilters.js:93 — 同上（用于 AgentsPage）

匹配成功后在当前 agent 已加载数据上做精确等值过滤。placeholder 文本提示用户支持 `#id=...` 语法。

**Q5: useAgentFilters 过滤逻辑？**

`useAgentFilters.js` 是 AgentsPage（节点页）专用 composable，管理 agent 列表过滤/排序/URL 同步。`#id=` 搜索（lines 91-105）在 agent 列表上做精确 id 匹配。**不与 selectedAgentId 交互**——它过滤全量 agent 列表，不是 per-agent 数据。

**Q6: AgentContext 作用域同步？**

- `selectedAgentId` 从 localStorage 初始化（AgentContext.js:13-14）
- `route.query.agentId` → `selectedAgentId` 单向同步 watch（AgentContext.js:19-24）
- `selectAgent()` 更新 ref + localStorage + recordAgentUsage（AgentContext.js:73-77）
- 各页面通过 `selectedOrRouteAgentId = computed(() => route.query.agentId || selectedAgentId.value)` 合并
- **切换 agentId 的副作用**：useRules/useL4Rules/useCertificates hooks 的 query key 包含 agentId → 切换时触发完整数据重新拉取

**Q7: 后端 ID 分配模型？**

`config_identity_allocator.go` 中 `AllocateRuleID`（line 123-125）调用 `allocatePreferredID`（line 250-266）。**关键发现：ID 全局唯一，非按 agent 编号**：
- HTTP/L4/WireGuard 共享单个 `usedRuleIDs` map（seedIDs lines 188-219）
- allocator 从所有 agent 加载全部规则（loadConfigIdentityAllocatorBaseState lines 77-121）
- 分配时找全局未用 ID：优先用 preferred ID（不冲突时），否则 `max(used) + 1`

**注意**：需求文档中 "id 按服务器各自编号" 的描述与实际代码不符。实际 ID 是**全局唯一**的，HTTP/L4/WireGuard 共享同一 ID 空间。这意味着同一 ID 在不同 agent 上**不会重复**，跨服务器多命中场景（R4）在当前 ID 模型下**不会发生**。

**Q8: 前端跨 agent 单条查询能力？**

**不存在。** 各页面数据加载全部按 `agentId` 范围：`useRules(agentId)` / `useL4Rules(agentId)` / `useCertificates(agentId)`，agentId 作为 query key，切换时完整重新拉取。无 `GET /api/rules/42` 这类跨 agent 单条查询 API。`#id=` 过滤纯前端，在已加载数据上操作。

## 项目/模块边界

- **前端核心改动域**：GlobalSearch.vue、RulesPage.vue、L4RulesPage.vue、CertsPage.vue、AgentsPage（useAgentFilters.js）
- **前端基础设施**：AgentContext.js（selectedAgentId 同步）、runtime.js（fetchAllAgents* API）
- **后端**：ID 分配器已全局唯一——无需后端改造；除非新增跨 agent 单条查询 API

## 入口与调用链

```
功能页搜索框输入 #id=xxx
  → filteredXxx computed 匹配 /^#id=(\S+)$/
  → 在当前 agent 已加载数据上精确过滤
  → [当前] 无命中则空结果
  → [目标] 无命中 → 跨 agent 解析 → 跳转/候选列表

全局搜索框输入 #id=xxx
  → [当前] doSearch → val.toLowerCase() → includes 子串匹配（不识别 #id=）
  → [目标] doSearch 识别 #id= → fetchAllAgents* 跨 agent 按 id 过滤 → 结果列表

点击搜索结果跳转
  → navigateToItem → router.push({ path, query: { agentId, search: '#id=xxx' } })
  → 目标页 watchEffect 预填 searchQuery → filteredXxx 精确过滤
```

## 业务知识检索状态

无外部知识源（Jira/PRD/知识库）。需求完全来自用户澄清对话。

## 可复用点

1. **fetchAllAgents* 系列函数**（runtime.js:401-510）：已实现跨 agent 并行数据拉取，可直接复用于功能页跨服务器 #id= 解析
2. **#id= 正则**（`/^#id=(\S+)$/`）：四处一致使用，保持兼容
3. **navigateToItem 跳转模式**（GlobalSearch.vue:253-265）：agentId + search query 参数模式可复用于跨服务器跳转
4. **selectedOrRouteAgentId 合并模式**（RulesPage:186 等）：route.query.agentId 优先的 computed 模式已建立

## 风险与验证关注点

1. **ID 模型认知偏差**：需求文档假设 "id 按服务器各自编号、非全局唯一"，但实际后端 ID 全局唯一（HTTP/L4/WireGuard 共享）。需在技术方案阶段确认：R4（多候选列表）是否仍有意义——如果 ID 全局唯一，则同 ID 不会出现在多个 agent 上，多候选场景不会发生
2. **性能**：功能页跨 agent 解析需拉取所有 agent 数据。fetchAllAgents* 在 agent 数量大时可能慢；全局搜索已有此调用可作性能参考
3. **selectedAgentId 切换副作用**：切换 agentId 触发 useRules/useL4Rules/useCertificates 完整数据重新拉取（query key 变化）。跨服务器跳转时需确认切换体验是否流畅
4. **#id= 正则四处重复**：抽取共享 composable 可减少维护成本，但非本次必须
5. **滚动+高亮**：R2/R8 要求的 "滚动到可见 + 短暂高亮" 当前未实现，需新增 DOM 操作逻辑

## 未知项

1. **R4 多候选是否真实存在**：后端 ID 全局唯一，同 ID 不会跨 agent 重复 → 需确认是否仍需候选列表 UI，或简化为 "全局唯一 → 直接跳转"
2. **跨 agent 解析的前后端路径**：前端复用 fetchAllAgents* vs 新增后端 API（如 `GET /api/rules/by-id/:id` 返回 agentId + record）
3. **RelayListeners 是否需要 #id= 支持**：当前 navigateToItem 对 relay 类型不带 search 参数（GlobalSearch.vue:264-265），useAgentFilters 支持但 AgentsPage 可能不需要跳转

---

*exploration by: exp-a (global-search) + exp-b (page-search-context), both complete*
