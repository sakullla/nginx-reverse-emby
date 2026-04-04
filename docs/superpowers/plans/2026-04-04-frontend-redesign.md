# 前端重新设计实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 重新设计前端：TopBar节点名称显示、侧边栏节点管理入口、节点详情页、全局搜索修复、主题统一、L4编辑功能

**Architecture:**
- TopBar 显示当前节点名称（纯文字，无下拉）
- Sidebar 移除嵌入式节点列表，添加"节点管理"导航链接
- 新增 AgentDetailPage（/agents/:id），4个Tab展示该节点详情
- Rules/L4 页面支持 URL query agentId，实现从节点详情页直接进入
- 全局搜索：AppShell 引入 GlobalSearch，监听 TopBar 事件，实现跨节点搜索
- 主题系统：ThemeContext 统一导出 themes（3个主题），ThemeSelector/SettingsPage 共用

**Tech Stack:** Vue 3, Vue Router 4, TanStack Vue Query

---

## Task 1: ThemeContext — 统一themes数组，移除cyberpunk

**Files:**
- Modify: `src/context/ThemeContext.js`

- [ ] **Step 1: 查看当前 ThemeContext 内容**

读取 `src/context/ThemeContext.js` 完整内容，确认当前 themes 定义位置和 useTheme 导出方式。

- [ ] **Step 2: 重构 themes 到 ThemeContext 并移除 cyberpunk**

将 themes 数组定义移到 ThemeContext.js 中，移除 cyberpunk 条目，导出 themes：

```js
// src/context/ThemeContext.js
import { ref, computed } from 'vue'

export const themes = [
  { id: 'sakura',   emoji: '🌸', label: '二次元' },
  { id: 'business', emoji: '☀️', label: '晴空'    },
  { id: 'midnight', emoji: '🌙', label: '暗夜'   }
]

export function useTheme() {
  const currentThemeId = ref(localStorage.getItem('theme') || 'sakura')
  // 如果之前是 cyberpunk，回退到 sakura
  if (currentThemeId.value === 'cyberpunk') {
    currentThemeId.value = 'sakura'
    localStorage.setItem('theme', 'sakura')
  }
  // ... 保持现有 setTheme 等逻辑
}
```

- [ ] **Step 3: 更新 ThemeSelector**

修改 `src/components/base/ThemeSelector.vue`：
- 移除本地的 `themes` 数组定义
- 改为 `import { themes } from '../../context/ThemeContext'`
- `useTheme` 继续使用，但 themes 不再本地定义

- [ ] **Step 4: 更新 SettingsPage 主题区块**

修改 `src/pages/SettingsPage.vue`：
- 移除本地的 themes 定义
- 改为 `import { themes } from '../context/ThemeContext'`
- 主题选项改用导入的 themes 数组渲染（不再是硬编码2个）

- [ ] **Step 5: 验证**

启动 `npm run dev`，访问设置页，确认主题显示3个（🌸 ☀️ 🌙），切换主题正常工作。

- [ ] **Step 6: Commit**

```bash
git add src/context/ThemeContext.js src/components/base/ThemeSelector.vue src/pages/SettingsPage.vue
git commit -m "refactor(frontend): unify themes to ThemeContext, remove cyberpunk"
```

---

## Task 2: TopBar — 显示当前节点名称

**Files:**
- Modify: `src/components/layout/TopBar.vue`

- [ ] **Step 1: 读取当前 TopBar 内容**

- [ ] **Step 2: 引入 useAgent 获取当前节点名**

```vue
<script setup>
import ThemeSelector from '../base/ThemeSelector.vue'
import { useAgent } from '../../context/AgentContext'
import { useAgents } from '../../hooks/useAgents'

const { selectedAgentId } = useAgent()
const { data: agentsData } = useAgents()

const currentAgentName = computed(() => {
  if (!selectedAgentId.value || !agentsData.value) return '—'
  const agent = agentsData.value.find(a => a.id === selectedAgentId.value)
  return agent?.name || '—'
})
</script>
```

- [ ] **Step 3: 在 TopBar template 中添加节点名称显示**

在 `topbar__center` 的搜索按钮右侧添加节点名称：

```vue
<div class="topbar__center">
  <button class="topbar__search" @click="$emit('open-search')" title="全局搜索 (⌘K)">
    <!-- 现有搜索按钮内容 -->
  </button>
  <span class="topbar__current-agent">{{ currentAgentName }}</span>
</div>
```

添加 CSS：
```css
.topbar__current-agent {
  font-size: 0.8rem;
  color: var(--color-text-tertiary);
  padding: 0.25rem 0.5rem;
  background: var(--color-bg-subtle);
  border-radius: var(--radius-md);
}
```

- [ ] **Step 4: 验证**

访问任意页面，确认 TopBar 搜索按钮右侧显示当前选中节点名称。

- [ ] **Step 5: Commit**

```bash
git add src/components/layout/TopBar.vue
git commit -m "feat(frontend): show current agent name in TopBar"
```

---

## Task 3: Sidebar — 移除节点列表，添加节点管理入口

**Files:**
- Modify: `src/components/layout/Sidebar.vue`

- [ ] **Step 1: 读取当前 Sidebar 内容**

- [ ] **Step 2: 定位节点列表区域**

当前 Sidebar 底部有 `<!-- Agent List -->` 区块，包含节点搜索、展开/收起、节点项（名称/状态/重命名/删除按钮）。

- [ ] **Step 3: 移除节点列表区块**

删除以下内容（大约在第 150-220 行左右）：
- `节点` 标题
- `刷新` / `收起` 按钮
- `搜索节点...` 输入框
- 整个 `v-for` 节点列表循环
- `renamingAgent` / `newName` / `deletingAgent` 等相关 ref（如果仅用于节点列表）

同时删除以下不再需要的代码（如果确认仅用于节点列表）：
- `useAgents` 引入
- `loadAgents` 引用
- `collapsed` 相关逻辑（如果仅用于收起节点列表）
- `searchQuery` ref
- `startRename` / `confirmRename` / `startDelete` / `confirmDelete` 函数
- 重命名弹窗 Teleport（`renamingAgent` 弹窗）
- 删除确认弹窗 Teleport（`deletingAgent` 弹窗）

- [ ] **Step 4: 添加"节点管理"导航链接**

在现有导航链接后添加：

```vue
<RouterLink to="/agents" class="sidebar__nav-item" :class="{ active: route.name === 'agents' }">
  <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
    <rect x="2" y="3" width="20" height="14" rx="2"/><line x1="8" y1="21" x2="16" y2="21"/><line x1="12" y1="17" x2="12" y2="21"/>
  </svg>
  <span>节点管理</span>
</RouterLink>
```

需要引入 `useRoute`：`const route = useRoute()`

- [ ] **Step 5: 调整 CSS**

移除与节点列表相关的 CSS：
- `.sidebar__agents-header`
- `.sidebar__agents-list`
- `.sidebar__agent-item`
- `.sidebar__agent-actions`
- `.sidebar__agent-action`
- 节点重命名/删除相关 modal CSS

保留导航相关 CSS。

- [ ] **Step 6: 验证**

访问任意页面，确认：
1. 侧边栏底部节点列表已消失
2. "节点管理" 导航链接存在且能正确跳转 /agents
3. 导航高亮正确（当前页对应链接高亮）
4. 无 console 错误

- [ ] **Step 7: Commit**

```bash
git add src/components/layout/Sidebar.vue
git commit -m "refactor(frontend): remove agent list from Sidebar, add 节点管理 nav link"
```

---

## Task 4: AppShell — 引入 GlobalSearch，监听TopBar事件

**Files:**
- Modify: `src/components/layout/AppShell.vue`

- [ ] **Step 1: 读取 AppShell 当前内容**

- [ ] **Step 2: 添加 GlobalSearch 引入和状态**

```vue
<script setup>
import { ref } from 'vue'
import TopBar from './TopBar.vue'
import Sidebar from './Sidebar.vue'
import BottomNav from './BottomNav.vue'
import GlobalSearch from '../GlobalSearch.vue'

const mobileSidebarOpen = ref(false)
const isMobile = ref(window.innerWidth < 1024)
const searchOpen = ref(false)

function checkMobile() {
  isMobile.value = window.innerWidth < 1024
}
</script>
```

- [ ] **Step 3: 修改 TopBar 使用 v-bind 监听事件**

```vue
<TopBar @open-search="searchOpen = true" />
```

- [ ] **Step 4: 在 template 中添加 GlobalSearch**

在 `<TopBar />` 之后添加：

```vue
<GlobalSearch
  :open="searchOpen"
  @update:open="searchOpen = $event"
/>
```

- [ ] **Step 5: 验证**

访问首页，按 ⌘K 或点击搜索按钮，确认全局搜索弹窗出现。无 console 错误。

- [ ] **Step 6: Commit**

```bash
git add src/components/layout/AppShell.vue
git commit -m "fix(frontend): wire GlobalSearch to TopBar open-search event"
```

---

## Task 5: 全局搜索 — 实现跨节点搜索逻辑

**Files:**
- Modify: `src/components/GlobalSearch.vue`

- [ ] **Step 1: 读取当前 GlobalSearch 内容**

- [ ] **Step 2: 添加搜索结果状态和API调用**

GlobalSearch 目前只有 UI，缺少实际搜索逻辑。需要添加：

```vue
<script setup>
import { ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useAgents } from '../../hooks/useAgents'
import * as api from '../../api'

// ... 现有 props/emits/code ...

const router = useRouter()
const { data: agentsData } = useAgents()
const results = ref([])
const isLoading = ref(false)

watch(query, async (val) => {
  if (!val.trim()) {
    results.value = []
    return
  }
  isLoading.value = true
  try {
    // 并行获取所有在线节点的规则
    const agents = agentsData.value || []
    const onlineAgents = agents.filter(a => a.status !== 'offline')
    const searches = onlineAgents.map(agent =>
      api.fetchRules(agent.id).then(rules => ({
        agentId: agent.id,
        agentName: agent.name,
        online: agent.status === 'online',
        rules: (rules || []).filter(r =>
          r.frontend_url?.includes(val) ||
          r.backend_url?.includes(val) ||
          (r.tags || []).some(tag => tag.includes(val))
        )
      })).catch(() => null)
    ))
    const groupResults = await Promise.all(searches)
    results.value = groupResults.filter(g => g && g.rules.length > 0)
  } finally {
    isLoading.value = false
  }
}, { immediate: true })
</script>
```

- [ ] **Step 3: 验证**

在搜索框输入 "emby"，确认：
1. 显示所有包含 "emby" 的规则
2. 按节点分组（显示 agentName）
3. 点击结果能导航到对应规则

- [ ] **Step 4: Commit**

```bash
git add src/components/GlobalSearch.vue
git commit -m "feat(frontend): implement cross-agent search in GlobalSearch"
```

---

## Task 6: RulesPage / L4RulesPage — 支持 URL query agentId

**Files:**
- Modify: `src/pages/RulesPage.vue`, `src/pages/L4RulesPage.vue`

- [ ] **Step 1: 修改 RulesPage 支持 URL query agentId**

```vue
<script setup>
import { computed } from 'vue'
import { useRoute } from 'vue-router'
// ... existing imports ...

const route = useRoute()
const { selectedAgentId } = useAgent()

// 优先从 URL query 获取，否则用 AgentContext
const agentId = computed(() => route.query.agentId || selectedAgentId.value)

const { data: _rulesData, isLoading, refetch } = useRules(agentId)
const rules = computed(() => _rulesData.value ?? [])
// ... rest unchanged ...
</script>
```

- [ ] **Step 2: 同样修改 L4RulesPage**

```vue
const route = useRoute()
const { selectedAgentId } = useAgent()
const agentId = computed(() => route.query.agentId || selectedAgentId.value)
const { data: _rulesData, isLoading } = useL4Rules(agentId)
```

- [ ] **Step 3: 验证**

从节点详情页 `/agents/:id` 点击"HTTP 规则" tab，确认 URL 变为 `/rules?agentId=:id`，且正确加载该节点的规则。

- [ ] **Step 4: Commit**

```bash
git add src/pages/RulesPage.vue src/pages/L4RulesPage.vue
git commit -m "feat(frontend): support URL query agentId in RulesPage and L4RulesPage"
```

---

## Task 7: L4RulesPage — 补充编辑功能

**Files:**
- Modify: `src/pages/L4RulesPage.vue`

- [ ] **Step 1: 添加编辑按钮到操作列**

```vue
<button class="btn-icon" title="编辑" @click="startEdit(rule)">
  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
    <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/>
    <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/>
  </svg>
</button>
```

- [ ] **Step 2: 添加 startEdit 函数和 editingRule ref**

```js
const editingRule = ref(null)

function startEdit(rule) {
  editingRule.value = rule
  form.value = {
    protocol: rule.protocol,
    listen_host: rule.listen_host,
    listen_port: String(rule.listen_port),
    upstream_host: rule.upstream_host,
    upstream_port: String(rule.upstream_port),
    tags: (rule.tags || []).join(', ')
  }
  showAddForm.value = true
}
```

- [ ] **Step 3: 修改 submitForm 支持编辑**

```js
function submitForm() {
  const payload = {
    protocol: form.value.protocol,
    listen_host: form.value.listen_host,
    listen_port: Number(form.value.listen_port),
    upstream_host: form.value.upstream_host,
    upstream_port: Number(form.value.upstream_port),
    tags: form.value.tags ? form.value.tags.split(',').map(t => t.trim()).filter(Boolean) : [],
    enabled: editingRule.value ? editingRule.value.enabled : true
  }
  if (editingRule.value) {
    updateL4Rule.mutate({ id: editingRule.value.id, ...payload })
  } else {
    createL4Rule.mutate(payload)
  }
  showAddForm.value = false
  editingRule.value = null
  form.value = { protocol: 'tcp', listen_host: '0.0.0.0', listen_port: '', upstream_host: '', upstream_port: '', tags: '' }
}
```

- [ ] **Step 4: 修改弹窗 header 显示编辑/添加状态**

```vue
<div class="modal__header">{{ editingRule ? '编辑 L4 规则' : '添加 L4 规则' }}</div>
```

- [ ] **Step 5: 验证**

访问 L4 规则页，点击编辑按钮，确认弹窗显示"编辑 L4 规则"且表单预填充正确，保存后规则更新。

- [ ] **Step 6: Commit**

```bash
git add src/pages/L4RulesPage.vue
git commit -m "feat(frontend): add edit functionality to L4RulesPage"
```

---

## Task 8: SettingsPage — 移除访问令牌区块

**Files:**
- Modify: `src/pages/SettingsPage.vue`

- [ ] **Step 1: 读取 SettingsPage 内容**

- [ ] **Step 2: 移除令牌区块 HTML**

删除以下 section：
```vue
<!-- Token Config -->
<section class="settings-section">
  <div class="settings-section__header">
    <h2 class="settings-section__title">访问令牌</h2>
    ...
  </div>
</section>
```

- [ ] **Step 3: 移除 token/showToken/systemInfo refs**

如果这些 ref 只用于令牌区块，也一并移除。

- [ ] **Step 4: 验证**

访问 /settings，确认"访问令牌"区块已消失，页面只显示主题、部署模式、关于三个区块。

- [ ] **Step 5: Commit**

```bash
git add src/pages/SettingsPage.vue
git commit -m "feat(frontend): remove token section from settings"
```

---

## Task 9: AgentDetailPage — 新增节点详情页

**Files:**
- Create: `src/pages/AgentDetailPage.vue`
- Modify: `src/router/index.js`

- [ ] **Step 1: 创建 AgentDetailPage.vue**

创建 `src/pages/AgentDetailPage.vue`，内容：

```vue
<template>
  <div class="agent-detail" v-if="agent">
    <!-- 返回链接 -->
    <div class="agent-detail__back">
      <RouterLink to="/agents" class="back-link">← 返回节点管理</RouterLink>
    </div>

    <!-- 节点信息头部 -->
    <div class="agent-detail__header">
      <div>
        <h1 class="agent-detail__name">{{ agent.name }}</h1>
        <p class="agent-detail__url">{{ agent.agent_url || agent.last_seen_ip || '—' }}</p>
      </div>
      <div class="agent-detail__status" :class="`agent-detail__status--${getStatus(agent)}`">
        {{ getStatusLabel(agent) }}
      </div>
    </div>

    <!-- 统计卡片 -->
    <div class="agent-detail__stats">
      <div class="stat-mini">
        <span class="stat-mini__value">{{ httpRulesCount }}</span>
        <span class="stat-mini__label">HTTP 规则</span>
      </div>
      <div class="stat-mini">
        <span class="stat-mini__value">{{ l4RulesCount }}</span>
        <span class="stat-mini__label">L4 规则</span>
      </div>
      <div class="stat-mini">
        <span class="stat-mini__value">{{ agent.last_seen ? timeAgo(agent.last_seen) : '—' }}</span>
        <span class="stat-mini__label">最后活跃</span>
      </div>
    </div>

    <!-- Tab 导航 -->
    <div class="agent-detail__tabs">
      <button v-for="tab in tabs" :key="tab.id" class="tab-btn" :class="{ active: activeTab === tab.id }" @click="activeTab = tab.id">{{ tab.label }}</button>
    </div>

    <!-- Tab 内容 -->
    <div class="agent-detail__tab-content">
      <!-- HTTP 规则 Tab -->
      <div v-if="activeTab === 'http'" class="tab-panel">
        <div class="tab-panel__header">
          <button class="btn btn-primary" @click="router.push({ path: '/rules', query: { agentId } })">查看全部规则</button>
        </div>
        <div class="rules-preview">
          <div v-for="rule in httpRules.slice(0, 5)" :key="rule.id" class="rule-preview-item">
            <span class="rule-preview-item__url">{{ rule.frontend_url }}</span>
            <span class="rule-preview-item__backend">→ {{ rule.backend_url }}</span>
          </div>
          <p v-if="!httpRules.length" class="empty-hint">暂无 HTTP 规则</p>
        </div>
      </div>

      <!-- L4 规则 Tab -->
      <div v-if="activeTab === 'l4'" class="tab-panel">
        <div class="tab-panel__header">
          <button class="btn btn-primary" @click="router.push({ path: '/l4', query: { agentId } })">查看全部规则</button>
        </div>
        <div class="rules-preview">
          <div v-for="rule in l4Rules.slice(0, 5)" :key="rule.id" class="rule-preview-item">
            <span class="rule-preview-item__url">{{ rule.listen_host }}:{{ rule.listen_port }}</span>
            <span class="rule-preview-item__backend">→ {{ rule.upstream_host }}:{{ rule.upstream_port }}</span>
          </div>
          <p v-if="!l4Rules.length" class="empty-hint">暂无 L4 规则</p>
        </div>
      </div>

      <!-- 系统信息 Tab -->
      <div v-if="activeTab === 'info'" class="tab-panel">
        <div class="info-grid">
          <div class="info-row"><span>版本</span><span>{{ agent.version || '—' }}</span></div>
          <div class="info-row"><span>角色</span><span>{{ getModeLabel(agent.mode) }}</span></div>
          <div class="info-row"><span>IP</span><span>{{ agent.last_seen_ip || '—' }}</span></div>
          <div class="info-row"><span>最后活跃</span><span>{{ agent.last_seen ? new Date(agent.last_seen).toLocaleString() : '—' }}</span></div>
          <div class="info-row"><span>同步状态</span><span>{{ agent.last_apply_status || '—' }}</span></div>
        </div>
      </div>
    </div>
  </div>
  <div v-else-if="isLoading" class="agent-detail__loading">
    <div class="spinner"></div>
  </div>
</template>

<script setup>
import { ref, computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useRules } from '../hooks/useRules'
import { useL4Rules } from '../hooks/useL4Rules'
import { useAgents } from '../hooks/useAgents'

const route = useRoute()
const router = useRouter()
const agentId = computed(() => route.params.id)

const { data: agentsData, isLoading } = useAgents()
const agent = computed(() => agentsData.value?.find(a => a.id === agentId.value))

const { data: httpRulesData } = useRules(agentId)
const httpRules = computed(() => httpRulesData.value ?? [])
const httpRulesCount = computed(() => httpRules.value.length)

const { data: l4RulesData } = useL4Rules(agentId)
const l4Rules = computed(() => l4RulesData.value ?? [])
const l4RulesCount = computed(() => l4Rules.value.length)

const activeTab = ref('http')
const tabs = [
  { id: 'http', label: 'HTTP 规则' },
  { id: 'l4', label: 'L4 规则' },
  { id: 'info', label: '系统信息' }
]

function getStatus(agent) {
  if (agent.status === 'offline') return 'offline'
  if (agent.last_apply_status === 'failed') return 'failed'
  if (agent.desired_revision > agent.current_revision) return 'pending'
  return 'online'
}

function getStatusLabel(agent) {
  const map = { online: '在线', offline: '离线', failed: '失败', pending: '同步中' }
  return map[getStatus(agent)] || '—'
}

function getModeLabel(mode) {
  return { local: '本机', master: '主控' }[mode] || '拉取'
}

function timeAgo(date) {
  const seconds = Math.floor((Date.now() - new Date(date)) / 1000)
  if (seconds < 60) return '刚刚'
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes} 分钟前`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours} 小时前`
  return `${Math.floor(hours / 24)} 天前`
}
</script>

<style scoped>
.agent-detail { max-width: 900px; margin: 0 auto; }
.agent-detail__back { margin-bottom: 1.5rem; }
.back-link { color: var(--color-text-secondary); font-size: 0.875rem; text-decoration: none; }
.back-link:hover { color: var(--color-primary); }
.agent-detail__header { display: flex; align-items: flex-start; justify-content: space-between; margin-bottom: 1.5rem; }
.agent-detail__name { font-size: 1.5rem; font-weight: 700; margin: 0 0 0.25rem; color: var(--color-text-primary); }
.agent-detail__url { font-size: 0.875rem; color: var(--color-text-tertiary); font-family: var(--font-mono); margin: 0; }
.agent-detail__status { font-size: 0.8rem; font-weight: 600; padding: 0.25rem 0.75rem; border-radius: var(--radius-full); }
.agent-detail__status--online { background: var(--color-success-50); color: var(--color-success); }
.agent-detail__status--offline { background: var(--color-bg-subtle); color: var(--color-text-muted); }
.agent-detail__status--failed { background: var(--color-danger-50); color: var(--color-danger); }
.agent-detail__status--pending { background: var(--color-warning-50); color: var(--color-warning); }
.agent-detail__stats { display: flex; gap: 1rem; margin-bottom: 1.5rem; }
.stat-mini { flex: 1; background: var(--color-bg-surface); border: 1.5px solid var(--color-border-default); border-radius: var(--radius-xl); padding: 1rem; text-align: center; }
.stat-mini__value { display: block; font-size: 1.5rem; font-weight: 700; color: var(--color-text-primary); }
.stat-mini__label { font-size: 0.75rem; color: var(--color-text-tertiary); }
.agent-detail__tabs { display: flex; gap: 0.25rem; border-bottom: 1.5px solid var(--color-border-default); margin-bottom: 1.5rem; }
.tab-btn { padding: 0.5rem 1rem; border: none; background: transparent; color: var(--color-text-secondary); font-size: 0.875rem; cursor: pointer; border-bottom: 2px solid transparent; margin-bottom: -1.5px; transition: all 0.15s; font-family: inherit; }
.tab-btn.active { color: var(--color-primary); border-bottom-color: var(--color-primary); font-weight: 500; }
.tab-panel__header { display: flex; justify-content: flex-end; margin-bottom: 1rem; }
.rules-preview { display: flex; flex-direction: column; gap: 0.5rem; }
.rule-preview-item { display: flex; gap: 0.75rem; padding: 0.75rem 1rem; background: var(--color-bg-surface); border: 1px solid var(--color-border-subtle); border-radius: var(--radius-lg); font-size: 0.8125rem; }
.rule-preview-item__url { flex: 1; color: var(--color-text-primary); font-family: var(--font-mono); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.rule-preview-item__backend { color: var(--color-text-tertiary); font-family: var(--font-mono); }
.empty-hint { text-align: center; color: var(--color-text-muted); padding: 2rem; font-size: 0.875rem; }
.info-grid { display: flex; flex-direction: column; gap: 0.5rem; }
.info-row { display: flex; justify-content: space-between; padding: 0.75rem 1rem; background: var(--color-bg-surface); border-radius: var(--radius-lg); font-size: 0.875rem; }
.info-row span:first-child { color: var(--color-text-secondary); }
.info-row span:last-child { color: var(--color-text-primary); font-weight: 500; }
.agent-detail__loading { display: flex; justify-content: center; padding: 3rem; }
.spinner { width: 24px; height: 24px; border: 2px solid var(--color-border-default); border-top-color: var(--color-primary); border-radius: 50%; animation: spin 1s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }
.btn { padding: 0.5rem 1rem; border-radius: var(--radius-lg); font-size: 0.875rem; font-weight: 500; cursor: pointer; transition: all 0.15s; border: none; font-family: inherit; display: inline-flex; align-items: center; gap: 0.375rem; }
.btn-primary { background: var(--gradient-primary); color: white; }
</style>
```

- [ ] **Step 2: 更新路由**

在 `src/router/index.js` 添加：

```js
{
  path: 'agents/:id',
  name: 'agent-detail',
  component: () => import('../pages/AgentDetailPage.vue'),
  meta: { title: '节点详情' }
}
```

- [ ] **Step 3: 验证**

访问 `/agents/local`（或任意存在的 agent ID），确认：
1. 显示节点名称、状态、统计卡片
2. 三个 Tab 能切换
3. HTTP/L4 规则 Tab 显示该节点规则预览
4. 系统信息 Tab 显示节点元数据
5. "← 返回节点管理" 链接正确

- [ ] **Step 4: Commit**

```bash
git add src/pages/AgentDetailPage.vue src/router/index.js
git commit -m "feat(frontend): add agent detail page with tabs"
```

---

## Task 10: AgentsPage — 增强卡片（统计列+点击跳转详情）

**Files:**
- Modify: `src/pages/AgentsPage.vue`

- [ ] **Step 1: 让卡片可点击跳转**

将每个 `agent-card` 包裹在 `RouterLink` 或用 `@click` 导航：

```vue
<div v-for="agent in agents" :key="agent.id" class="agent-card" @click="router.push(`/agents/${agent.id}`)" style="cursor:pointer">
```

引入 router：
```js
const router = useRouter()
```

- [ ] **Step 2: 添加统计列**

在卡片内名称行下方添加统计信息：

```vue
<div class="agent-card__stats">
  <span class="agent-card__stat">
    <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/></svg>
    HTTP {{ agent.http_rules_count || 0 }}
  </span>
  <span class="agent-card__stat">
    <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="2" y="2" width="20" height="8" rx="2"/><rect x="2" y="14" width="20" height="8" rx="2"/></svg>
    L4 {{ agent.l4_rules_count || 0 }}
  </span>
  <span class="agent-card__last-seen">
    {{ timeAgo(agent.last_seen) }}
  </span>
</div>
```

添加 CSS：
```css
.agent-card__stats { display: flex; align-items: center; gap: 0.75rem; margin-top: 0.25rem; }
.agent-card__stat { display: flex; align-items: center; gap: 0.25rem; font-size: 0.75rem; color: var(--color-text-tertiary); }
.agent-card__last-seen { font-size: 0.75rem; color: var(--color-text-muted); margin-left: auto; }
```

添加 timeAgo 函数：
```js
function timeAgo(date) {
  if (!date) return '—'
  const seconds = Math.floor((Date.now() - new Date(date)) / 1000)
  if (seconds < 60) return '刚刚'
  const m = Math.floor(seconds / 60)
  if (m < 60) return `${m}m`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h}h`
  return `${Math.floor(h / 24)}d`
}
```

- [ ] **Step 3: 修改 AgentsPage 标题显示统计**

当前 AgentsPage 显示 `{{ agents.length }} 个节点 · {{ onlineCount }} 在线`。增强为包含累计 HTTP/L4 规则数：

```js
// 需要累加所有节点的规则数
const totalHttpRules = computed(() => {
  // 如果 agents 数据包含 http_rules_count 直接用
  return agents.value.reduce((sum, a) => sum + (a.http_rules_count || 0), 0)
})
const totalL4Rules = computed(() => {
  return agents.value.reduce((sum, a) => sum + (a.l4_rules_count || 0), 0)
})
```

```html
<p class="agents-page__subtitle">
  {{ agents.length }} 个节点 · {{ onlineCount }} 在线
  · 累计 {{ totalHttpRules }} HTTP 规则 · 累计 {{ totalL4Rules }} L4 规则
</p>
```

- [ ] **Step 4: 验证**

访问 `/agents`，确认：
1. 每个节点卡片可点击，点击进入 `/agents/:id`
2. 卡片显示 HTTP 规则数、L4 规则数、最后活跃时间
3. 页面标题显示累计规则数

- [ ] **Step 5: Commit**

```bash
git add src/pages/AgentsPage.vue
git commit -m "feat(frontend): enhance AgentsPage with stats and clickable cards"
```

---

## Task 11: Dashboard — 添加节点状态表格

**Files:**
- Modify: `src/pages/DashboardPage.vue`

- [ ] **Step 1: 读取 DashboardPage 内容**

- [ ] **Step 2: 在 stat cards 下方添加节点状态表格**

```html
<!-- 节点状态表格 -->
<div v-if="agents?.length" class="dashboard-section">
  <div class="dashboard-section__header">
    <h2 class="dashboard-section__title">节点状态</h2>
    <RouterLink to="/agents" class="dashboard-section__link">查看全部 →</RouterLink>
  </div>
  <div class="dashboard-table-wrap">
    <table class="dashboard-table">
      <thead>
        <tr>
          <th>节点</th>
          <th>状态</th>
          <th>HTTP 规则</th>
          <th>L4 规则</th>
          <th>同步状态</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="agent in displayedAgents" :key="agent.id">
          <td>
            <div class="agent-cell">
              <span class="agent-cell__name">{{ agent.name }}</span>
              <span class="agent-cell__url">{{ agent.agent_url ? getHostname(agent.agent_url) : (agent.last_seen_ip || '—') }}</span>
            </div>
          </td>
          <td>
            <span class="status-badge" :class="`status-badge--${getStatus(agent)}`">
              {{ getStatusLabel(agent) }}
            </span>
          </td>
          <td>
            <span class="tag">{{ agent.http_rules_count || 0 }}</span>
          </td>
          <td>
            <span class="tag">{{ agent.l4_rules_count || 0 }}</span>
          </td>
          <td>
            <span class="sync-badge" :class="`sync-badge--${getSyncStatus(agent)}`">
              {{ getSyncLabel(agent) }}
            </span>
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</div>
```

- [ ] **Step 3: 添加 script 逻辑**

```js
const displayedAgents = computed(() => (agents.value || []).slice(0, 8))

function getHostname(url) {
  try { return url ? new URL(url).hostname : '' } catch { return '' }
}

function getStatus(agent) {
  if (agent.status === 'offline') return 'offline'
  if (agent.last_apply_status === 'failed') return 'failed'
  if (agent.desired_revision > agent.current_revision) return 'pending'
  return 'online'
}

function getStatusLabel(agent) {
  return { online: '在线', offline: '离线', failed: '失败', pending: '同步中' }[getStatus(agent)] || '—'
}

function getSyncStatus(agent) {
  if (agent.last_apply_status === 'failed') return 'failed'
  if (agent.desired_revision > agent.current_revision) return 'pending'
  return 'synced'
}

function getSyncLabel(agent) {
  return { synced: '已同步', pending: '待同步', failed: '失败' }[getSyncStatus(agent)] || '—'
}
```

- [ ] **Step 4: 添加 CSS**

```css
.dashboard-section { background: var(--color-bg-surface); border: 1.5px solid var(--color-border-default); border-radius: var(--radius-2xl); overflow: hidden; margin-bottom: 2rem; }
.dashboard-section__header { display: flex; align-items: center; justify-content: space-between; padding: 1rem 1.25rem; border-bottom: 1px solid var(--color-border-subtle); }
.dashboard-section__title { font-size: 0.875rem; font-weight: 600; color: var(--color-text-primary); margin: 0; }
.dashboard-section__link { font-size: 0.8rem; color: var(--color-primary); text-decoration: none; }
.dashboard-table-wrap { overflow-x: auto; }
.dashboard-table { width: 100%; border-collapse: collapse; }
.dashboard-table th { text-align: left; padding: 0.6rem 1rem; font-size: 0.7rem; font-weight: 600; color: var(--color-text-tertiary); border-bottom: 1px solid var(--color-border-subtle); }
.dashboard-table td { padding: 0.75rem 1rem; border-bottom: 1px solid var(--color-border-subtle); }
.dashboard-table tr:last-child td { border-bottom: none; }
.agent-cell__name { display: block; font-size: 0.875rem; font-weight: 500; color: var(--color-text-primary); }
.agent-cell__url { display: block; font-size: 0.75rem; color: var(--color-text-tertiary); font-family: var(--font-mono); }
.status-badge { display: inline-block; font-size: 0.7rem; padding: 2px 8px; border-radius: var(--radius-full); font-weight: 500; }
.status-badge--online { background: var(--color-success-50); color: var(--color-success); }
.status-badge--offline { background: var(--color-bg-subtle); color: var(--color-text-muted); }
.status-badge--failed { background: var(--color-danger-50); color: var(--color-danger); }
.status-badge--pending { background: var(--color-warning-50); color: var(--color-warning); }
.tag { display: inline-block; font-size: 0.75rem; padding: 2px 6px; background: var(--color-primary-subtle); color: var(--color-primary); border-radius: var(--radius-full); font-weight: 500; }
.sync-badge { display: inline-block; font-size: 0.7rem; padding: 2px 8px; border-radius: var(--radius-full); font-weight: 500; }
.sync-badge--synced { background: var(--color-success-50); color: var(--color-success); }
.sync-badge--pending { background: var(--color-warning-50); color: var(--color-warning); }
.sync-badge--failed { background: var(--color-danger-50); color: var(--color-danger); }
```

- [ ] **Step 5: 验证**

访问首页，确认节点状态表格显示（前8个节点），点击"查看全部 →"跳转 /agents。

- [ ] **Step 6: Commit**

```bash
git add src/pages/DashboardPage.vue
git commit -m "feat(frontend): add node status table to dashboard"
```
