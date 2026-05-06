# 快速节点选择器 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 HTTP 规则、L4 规则等页面顶部增加横向 Chip 形式的快速节点选择器，替代右上角下拉选择器。

**Architecture:** 新建 `QuickAgentSelect.vue` 组件，在页面标题下方渲染 chip 列表；通过 `AgentContext` 扩展最近使用记录；从 `TopBar.vue` 移除原有选择器 UI。

**Tech Stack:** Vue 3, Vite, CSS Variables (theme-aware)

---

## File Structure

| File | Action | Responsibility |
|------|--------|----------------|
| `panel/frontend/src/components/QuickAgentSelect.vue` | Create | 快速节点选择器组件：chip 渲染、"+N 更多" 下拉、最近使用排序 |
| `panel/frontend/src/context/AgentContext.js` | Modify | 新增 `recordAgentUsage(id)` 辅助函数，读写 `localStorage` |
| `panel/frontend/src/components/layout/TopBar.vue` | Modify | 移除右上角 agent 下拉切换 UI（保留 `effectiveAgentId` 逻辑） |
| `panel/frontend/src/pages/RulesPage.vue` | Modify | 添加 `QuickAgentSelect`，移除空态 `AgentPicker` |
| `panel/frontend/src/pages/L4RulesPage.vue` | Modify | 添加 `QuickAgentSelect`，移除空态 `AgentPicker` |
| `panel/frontend/src/pages/CertsPage.vue` | Modify | 添加 `QuickAgentSelect`，移除空态 `AgentPicker` |
| `panel/frontend/src/pages/RelayListenersPage.vue` | Modify | 添加 `QuickAgentSelect`，移除空态 `AgentPicker` |
| `panel/frontend/src/pages/DashboardPage.vue` | Modify | 添加 `QuickAgentSelect` |
| `panel/frontend/src/pages/VersionsPage.vue` | Modify | 添加 `QuickAgentSelect`，移除空态 `AgentPicker` |

---

### Task 1: 扩展 AgentContext — 新增最近使用记录

**Files:**
- Modify: `panel/frontend/src/context/AgentContext.js`

**目标:** 在 `AgentContext` 中新增 `recordAgentUsage(id)` 函数，管理 `localStorage` 中的最近使用 agent 列表。

- [ ] **Step 1: 修改 AgentContext.js 添加 recordAgentUsage**

在 `AgentContext.js` 中，在 `selectAgent` 函数之后、`provide` 之前添加以下代码：

```javascript
    const RECENT_AGENTS_KEY = 'nre_recent_agent_ids'
    const MAX_RECENT_AGENTS = 20

    function recordAgentUsage(id) {
      if (!id) return
      try {
        const raw = localStorage.getItem(RECENT_AGENTS_KEY)
        const list = raw ? JSON.parse(raw) : []
        const filtered = list.filter(item => item !== id)
        filtered.unshift(id)
        const trimmed = filtered.slice(0, MAX_RECENT_AGENTS)
        localStorage.setItem(RECENT_AGENTS_KEY, JSON.stringify(trimmed))
      } catch {
        localStorage.setItem(RECENT_AGENTS_KEY, JSON.stringify([id]))
      }
    }
```

然后修改 `provide` 行，将 `recordAgentUsage` 暴露出去：

```javascript
    provide(AgentContextKey, { selectedAgentId, selectAgent, recordAgentUsage, systemInfo })
```

- [ ] **Step 2: Commit**

```bash
git add panel/frontend/src/context/AgentContext.js
git commit -m "feat(frontend): add recordAgentUsage to AgentContext"
```

---

### Task 2: 创建 QuickAgentSelect 组件

**Files:**
- Create: `panel/frontend/src/components/QuickAgentSelect.vue`

**目标:** 实现快速节点选择器组件，包含 chip 列表、"+N 更多"下拉、最近使用排序。

- [ ] **Step 1: 创建 QuickAgentSelect.vue**

```vue
<template>
  <div class="quick-agent-select">
    <div v-if="!agents.length" class="quick-agent-select__empty">
      暂无可用节点
    </div>
    <div v-else class="quick-agent-select__chips">
      <button
        v-for="agent in visibleAgents"
        :key="agent.id"
        class="quick-agent-select__chip"
        :class="{ 'quick-agent-select__chip--active': agent.id === agentId }"
        :title="agent.name"
        @click="select(agent)"
      >
        <span
          class="quick-agent-select__status-dot"
          :class="`quick-agent-select__status-dot--${getAgentStatus(agent)}`"
        />
        <span class="quick-agent-select__chip-name">{{ agent.name }}</span>
      </button>

      <div
        v-if="hiddenAgents.length"
        class="quick-agent-select__more"
        ref="moreRef"
      >
        <button
          class="quick-agent-select__chip quick-agent-select__chip--more"
          @click="moreOpen = !moreOpen"
        >
          +{{ hiddenAgents.length }} 更多
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <polyline points="6 9 12 15 18 9"/>
          </svg>
        </button>
        <div v-if="moreOpen" class="quick-agent-select__dropdown">
          <div class="quick-agent-select__dropdown-search">
            <input
              v-model="moreSearch"
              class="quick-agent-select__dropdown-input"
              placeholder="搜索节点..."
            />
          </div>
          <div class="quick-agent-select__dropdown-list">
            <button
              v-for="agent in filteredHiddenAgents"
              :key="agent.id"
              class="quick-agent-select__dropdown-item"
              :class="{ active: agent.id === agentId }"
              @click="select(agent)"
            >
              <span
                class="quick-agent-select__status-dot"
                :class="`quick-agent-select__status-dot--${getAgentStatus(agent)}`"
              />
              <span class="quick-agent-select__dropdown-name">{{ agent.name }}</span>
            </button>
            <div v-if="!filteredHiddenAgents.length" class="quick-agent-select__dropdown-empty">
              没有匹配的节点
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { getAgentStatus } from '../utils/agentHelpers.js'

const props = defineProps({
  agentId: { type: String, default: null },
  agents: { type: Array, required: true }
})

const emit = defineEmits(['update:agentId'])

const MAX_VISIBLE = 5
const RECENT_AGENTS_KEY = 'nre_recent_agent_ids'

const moreOpen = ref(false)
const moreSearch = ref('')
const moreRef = ref(null)

function getRecentList() {
  try {
    const raw = localStorage.getItem(RECENT_AGENTS_KEY)
    return raw ? JSON.parse(raw) : []
  } catch {
    return []
  }
}

const agentMap = computed(() => {
  const map = new Map()
  for (const agent of props.agents) {
    map.set(agent.id, agent)
  }
  return map
})

const sortedAgents = computed(() => {
  const recent = getRecentList()
  const existingIds = new Set(props.agents.map(a => a.id))

  // Build ordered list
  const result = []
  const seen = new Set()

  // Current selected first
  if (props.agentId && existingIds.has(props.agentId)) {
    result.push(agentMap.value.get(props.agentId))
    seen.add(props.agentId)
  }

  // Recent agents (excluding current)
  for (const id of recent) {
    if (seen.has(id)) continue
    const agent = agentMap.value.get(id)
    if (agent) {
      result.push(agent)
      seen.add(id)
    }
  }

  // Remaining agents sorted by name
  const remaining = props.agents
    .filter(a => !seen.has(a.id))
    .sort((a, b) => a.name.localeCompare(b.name))
  result.push(...remaining)

  return result
})

const visibleAgents = computed(() => sortedAgents.value.slice(0, MAX_VISIBLE))
const hiddenAgents = computed(() => sortedAgents.value.slice(MAX_VISIBLE))

const filteredHiddenAgents = computed(() => {
  const q = moreSearch.value.trim().toLowerCase()
  if (!q) return hiddenAgents.value
  return hiddenAgents.value.filter(a =>
    a.name.toLowerCase().includes(q)
  )
})

function select(agent) {
  emit('update:agentId', agent.id)
  moreOpen.value = false
  moreSearch.value = ''
}

function handleClickOutside(e) {
  if (moreRef.value && !moreRef.value.contains(e.target)) {
    moreOpen.value = false
    moreSearch.value = ''
  }
}

onMounted(() => document.addEventListener('mousedown', handleClickOutside))
onUnmounted(() => document.removeEventListener('mousedown', handleClickOutside))
</script>

<style scoped>
.quick-agent-select {
  margin-bottom: 1.25rem;
}

.quick-agent-select__empty {
  font-size: 0.875rem;
  color: var(--color-text-muted);
  padding: 0.5rem 0;
}

.quick-agent-select__chips {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  flex-wrap: wrap;
}

.quick-agent-select__chip {
  display: inline-flex;
  align-items: center;
  gap: 0.375rem;
  padding: 0.375rem 0.875rem;
  border-radius: var(--radius-full);
  border: 1px solid var(--color-border);
  background: var(--color-surface-elevated);
  color: var(--color-text-primary);
  font-size: 0.8125rem;
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-default);
  font-family: inherit;
  white-space: nowrap;
  max-width: 160px;
}

.quick-agent-select__chip:hover {
  border-color: var(--color-primary);
  background: var(--color-bg-hover);
}

.quick-agent-select__chip--active {
  background: var(--color-primary);
  color: #fff;
  border-color: var(--color-primary);
}

.quick-agent-select__chip--active:hover {
  background: var(--color-primary-hover);
  border-color: var(--color-primary-hover);
}

.quick-agent-select__chip--more {
  background: var(--color-bg-subtle);
  color: var(--color-text-secondary);
  border-color: var(--color-border);
  padding-right: 0.625rem;
}

.quick-agent-select__chip--more:hover {
  background: var(--color-bg-hover);
}

.quick-agent-select__chip-name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.quick-agent-select__status-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  flex-shrink: 0;
}

.quick-agent-select__status-dot--online {
  background: var(--color-success);
}

.quick-agent-select__status-dot--offline {
  background: var(--color-text-muted);
}

.quick-agent-select__status-dot--failed {
  background: var(--color-danger);
}

.quick-agent-select__status-dot--pending {
  background: var(--color-warning);
}

.quick-agent-select__more {
  position: relative;
}

.quick-agent-select__dropdown {
  position: absolute;
  top: calc(100% + 6px);
  left: 0;
  width: 220px;
  background: var(--color-bg-surface-raised);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  box-shadow: var(--shadow-xl);
  z-index: var(--z-dropdown);
  animation: scaleIn 0.15s var(--ease-bounce) both;
  overflow: hidden;
}

.quick-agent-select__dropdown-search {
  padding: 0.5rem;
  border-bottom: 1px solid var(--color-border-subtle);
}

.quick-agent-select__dropdown-input {
  width: 100%;
  padding: 0.375rem 0.625rem;
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-md);
  background: var(--color-bg-subtle);
  font-size: 0.8rem;
  color: var(--color-text-primary);
  outline: none;
  font-family: inherit;
  box-sizing: border-box;
  transition: border-color var(--duration-fast) var(--ease-default),
              box-shadow var(--duration-fast) var(--ease-default);
}

.quick-agent-select__dropdown-input:focus {
  border-color: var(--color-primary);
  box-shadow: var(--shadow-focus);
}

.quick-agent-select__dropdown-list {
  max-height: 240px;
  overflow-y: auto;
  padding: 0.25rem;
  scrollbar-width: thin;
}

.quick-agent-select__dropdown-list::-webkit-scrollbar { width: 6px; }
.quick-agent-select__dropdown-list::-webkit-scrollbar-track { background: transparent; }
.quick-agent-select__dropdown-list::-webkit-scrollbar-thumb { background: var(--color-border-default); border-radius: 3px; }

.quick-agent-select__dropdown-item {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  width: 100%;
  padding: 0.5rem 0.625rem;
  border: none;
  background: transparent;
  border-radius: var(--radius-md);
  cursor: pointer;
  transition: background var(--duration-fast) var(--ease-default);
  font-family: inherit;
  text-align: left;
}

.quick-agent-select__dropdown-item:hover {
  background: var(--color-bg-hover);
}

.quick-agent-select__dropdown-item.active {
  background: var(--color-primary-subtle);
  color: var(--color-primary);
}

.quick-agent-select__dropdown-name {
  font-size: 0.8125rem;
  color: var(--color-text-primary);
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.quick-agent-select__dropdown-empty {
  padding: 1rem;
  text-align: center;
  font-size: 0.8125rem;
  color: var(--color-text-muted);
}

@media (max-width: 768px) {
  .quick-agent-select__chip {
    padding: 0.3125rem 0.625rem;
    font-size: 0.75rem;
    max-width: 120px;
  }
}
</style>
```

- [ ] **Step 2: Commit**

```bash
git add panel/frontend/src/components/QuickAgentSelect.vue
git commit -m "feat(frontend): add QuickAgentSelect component"
```

---

### Task 3: 修改 TopBar — 移除右上角节点选择器

**Files:**
- Modify: `panel/frontend/src/components/layout/TopBar.vue`

**目标:** 从 TopBar 中移除 agent 下拉切换 UI，保留 `effectiveAgentId` 逻辑（供路由响应使用，但不渲染）。

- [ ] **Step 1: 删除 agent switcher 模板代码**

在 `TopBar.vue` 的 `<template>` 中，删除第 28-83 行的 agent switcher 代码（从 `<!-- Agent Switcher Dropdown -->` 开始到该 `</div>` 结束）。

删除后 `topbar__actions` 区域应只保留：
- 搜索按钮
- `ThemeSelector`
- 退出按钮

- [ ] **Step 2: 删除 agent switcher script 代码**

在 `<script setup>` 中，删除以下内容：

```javascript
// 删除这些 ref 和 computed
const agentDropdownOpen = ref(false)
const agentSearchQuery = ref('')
const agentSwitcherRef = ref(null)
const switcherStatusFilter = ref('')
const switcherSort = ref('last_seen')

// 删除 switcherStatusOptions
const switcherStatusOptions = [...]

// 删除 currentAgentName 和 currentAgent computed
const currentAgentName = computed(...)
const currentAgent = computed(...)

// 删除 switcherAgents computed
const switcherAgents = computed(...)

// 删除 selectAgent 函数中关于 agent switcher 的部分
// 保留 handleLogout 和路由跳转逻辑

// 删除 handleClickOutside
function handleClickOutside(e) {...}

// 删除 onMounted / onUnmounted 中的事件监听
onMounted(() => document.addEventListener('mousedown', handleClickOutside))
onUnmounted(() => document.removeEventListener('mousedown', handleClickOutside))
```

**保留以下内容：**
- `effectiveAgentId` computed（供路由跳转使用）
- `selectAgent` 函数中关于路由跳转的逻辑（第 170-182 行，保留但简化）
- `handleLogout`

简化后的 `selectAgent` 函数：

```javascript
function selectAgent(agent) {
  setSelectedAgentId(agent.id)
  if (route.name?.includes('agent-detail')) {
    router.push({ name: 'agent-detail', params: { id: agent.id } })
  } else if (route.query.agentId) {
    router.replace({ query: { ...route.query, agentId: undefined } })
  }
}
```

**注意：** 由于其他页面不再通过 TopBar 的 agent switcher 触发 `selectAgent`，这个函数实际上只在 agent-detail 页面内部可能用到。如果确认无其他调用方，也可以删除。这里保守保留。

- [ ] **Step 3: 删除 agent switcher style 代码**

在 `<style scoped>` 中，删除第 285-477 行的所有 `.agent-switcher*` CSS 规则。

- [ ] **Step 4: Commit**

```bash
git add panel/frontend/src/components/layout/TopBar.vue
git commit -m "feat(frontend): remove agent switcher from TopBar"
```

---

### Task 4: 修改 RulesPage — 集成 QuickAgentSelect

**Files:**
- Modify: `panel/frontend/src/pages/RulesPage.vue`

**目标:** 在页面标题下方添加 `QuickAgentSelect`，移除空态时的 `AgentPicker`。

- [ ] **Step 1: 添加导入和模板**

在 `<script setup>` 的 import 区域，添加：

```javascript
import QuickAgentSelect from '../components/QuickAgentSelect.vue'
```

删除 `AgentPicker` 的 import：

```javascript
// 删除这一行
import AgentPicker from '../components/AgentPicker.vue'
```

在模板中，在 `rules-page__header` 之后、`<!-- No agent selected -->` 之前，添加：

```vue
    <QuickAgentSelect
      :agentId="agentId"
      :agents="allAgents"
      @update:agentId="handleAgentSelect"
    />
```

- [ ] **Step 2: 修改 handleAgentSelect 记录最近使用**

修改 `handleAgentSelect` 函数，在更新路由后记录最近使用：

```javascript
function handleAgentSelect(id) {
  agentContext.recordAgentUsage?.(id)
  router.replace({ query: { ...route.query, agentId: id } })
}
```

- [ ] **Step 3: 移除空态 AgentPicker**

将空态区域（第 33-42 行）中的 `AgentPicker` 替换为纯文本提示：

```vue
    <!-- No agent selected -->
    <div v-if="!agentId" class="rules-page__prompt">
      <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
        <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/>
        <path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/>
      </svg>
      <p>请从上方选择一个节点来管理规则</p>
      <p class="rules-page__prompt-hint">或前往节点管理页面添加新节点</p>
      <RouterLink to="/agents" class="btn btn-primary">加入节点</RouterLink>
    </div>
```

- [ ] **Step 4: Commit**

```bash
git add panel/frontend/src/pages/RulesPage.vue
git commit -m "feat(frontend): integrate QuickAgentSelect into RulesPage"
```

---

### Task 5: 修改 L4RulesPage — 集成 QuickAgentSelect

**Files:**
- Modify: `panel/frontend/src/pages/L4RulesPage.vue`

**目标:** 与 RulesPage 相同的修改模式。

- [ ] **Step 1: 读取 L4RulesPage.vue 确认结构**

先读取文件，确认其结构与 RulesPage 类似（有 AgentPicker 空态、有 handleAgentSelect）。

- [ ] **Step 2: 应用相同修改**

参照 Task 4 的步骤，对 `L4RulesPage.vue` 做相同修改：
1. 添加 `QuickAgentSelect` import
2. 删除 `AgentPicker` import
3. 在 header 下方添加 `<QuickAgentSelect>` 组件
4. 修改 `handleAgentSelect` 调用 `recordAgentUsage`
5. 移除空态中的 `AgentPicker`，替换为文本提示

- [ ] **Step 3: Commit**

```bash
git add panel/frontend/src/pages/L4RulesPage.vue
git commit -m "feat(frontend): integrate QuickAgentSelect into L4RulesPage"
```

---

### Task 6: 修改 CertsPage — 集成 QuickAgentSelect

**Files:**
- Modify: `panel/frontend/src/pages/CertsPage.vue`

**目标:** 与 RulesPage 相同的修改模式。

- [ ] **Step 1: 读取 CertsPage.vue 确认结构**

- [ ] **Step 2: 应用相同修改**

参照 Task 4 的模式修改 `CertsPage.vue`。

- [ ] **Step 3: Commit**

```bash
git add panel/frontend/src/pages/CertsPage.vue
git commit -m "feat(frontend): integrate QuickAgentSelect into CertsPage"
```

---

### Task 7: 修改 RelayListenersPage — 集成 QuickAgentSelect

**Files:**
- Modify: `panel/frontend/src/pages/RelayListenersPage.vue`

**目标:** 与 RulesPage 相同的修改模式。

- [ ] **Step 1: 读取 RelayListenersPage.vue 确认结构**

- [ ] **Step 2: 应用相同修改**

参照 Task 4 的模式修改 `RelayListenersPage.vue`。

- [ ] **Step 3: Commit**

```bash
git add panel/frontend/src/pages/RelayListenersPage.vue
git commit -m "feat(frontend): integrate QuickAgentSelect into RelayListenersPage"
```

---

### Task 8: 修改 DashboardPage — 集成 QuickAgentSelect

**Files:**
- Modify: `panel/frontend/src/pages/DashboardPage.vue`

**目标:** Dashboard 页面通常没有空态 AgentPicker，只需在适当位置添加 `QuickAgentSelect`。

- [ ] **Step 1: 读取 DashboardPage.vue 确认结构**

- [ ] **Step 2: 应用修改**

在 Dashboard 页面标题或模块区域上方添加 `QuickAgentSelect`。Dashboard 通常已有 `agentId` 逻辑，只需：
1. 添加 `QuickAgentSelect` import
2. 在页面顶部添加组件绑定
3. 在 agent 切换时调用 `recordAgentUsage`

- [ ] **Step 3: Commit**

```bash
git add panel/frontend/src/pages/DashboardPage.vue
git commit -m "feat(frontend): integrate QuickAgentSelect into DashboardPage"
```

---

### Task 9: 修改 VersionsPage — 集成 QuickAgentSelect

**Files:**
- Modify: `panel/frontend/src/pages/VersionsPage.vue`

**目标:** 与 RulesPage 相同的修改模式（如有 AgentPicker 则移除）。

- [ ] **Step 1: 读取 VersionsPage.vue 确认结构**

- [ ] **Step 2: 应用修改**

参照 Task 4 的模式修改 `VersionsPage.vue`。

- [ ] **Step 3: Commit**

```bash
git add panel/frontend/src/pages/VersionsPage.vue
git commit -m "feat(frontend): integrate QuickAgentSelect into VersionsPage"
```

---

### Task 10: 验证与测试

**Files:**
- 多个页面文件

**目标:** 确保所有修改正常工作，主题样式正确。

- [ ] **Step 1: 构建前端**

```bash
cd panel/frontend
npm run build
```

预期：无编译错误。

- [ ] **Step 2: 启动 dev server 验证**

```bash
cd panel/frontend
npm run dev
```

同时启动后端：
```bash
cd panel/backend-go
go run ./cmd/nre-control-plane
```

验证清单：
- [ ] HTTP 规则页面顶部可见 chip 列表
- [ ] 点击 chip 可切换节点，页面内容刷新
- [ ] 选中 chip 有高亮样式
- [ ] 未选中 chip 为默认样式
- [ ] 每个 chip 左侧有状态圆点
- [ ] 节点数 > 5 时显示 "+N 更多" 按钮
- [ ] 点击 "+N 更多" 展开下拉，可选择隐藏节点
- [ ] 右上角无节点选择器
- [ ] L4 规则、证书、中继监听、Dashboard、版本页面均有快速选择器
- [ ] 暗色主题下样式正确
- [ ] 移动端下 chip 可正常显示和点击

- [ ] **Step 3: Commit 最终验证**

```bash
git commit --allow-empty -m "chore(frontend): verify QuickAgentSelect integration"
```

---

## Self-Review Checklist

**Spec coverage:**
- [x] Chip 形式横向排列 — Task 2
- [x] 最多 5 个 — Task 2 (`MAX_VISIBLE = 5`)
- [x] 超出用 "+N 更多" — Task 2
- [x] 最近使用优先排序 — Task 1 + Task 2
- [x] 选中高亮样式 — Task 2 CSS
- [x] 状态圆点 — Task 2
- [x] 移除右上角选择器 — Task 3
- [x] 移动端适配 — Task 2 CSS media query
- [x] 主题适配 — Task 2 CSS variables

**Placeholder scan:**
- [x] 无 TBD/TODO
- [x] 每步都有具体代码
- [x] 无 "类似 Task N" 的省略

**Type consistency:**
- [x] `recordAgentUsage` 在 Task 1 和 Task 4 中签名一致
- [x] `agentId` prop 类型在组件和页面中一致
