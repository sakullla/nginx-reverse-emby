# Refactor Agents Page with Global Search and Remove Version Policy from Nav

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Refactor the Agents page to a searchable card grid, extend GlobalSearch to include agents, and remove the version policy link from navigation.

**Architecture:** Follow the existing RulesPage card-grid + header-search pattern. Reuse the same CSS classes and search interaction model. Keep the versions route alive but hide it from Sidebar and BottomNav.

**Tech Stack:** Vue 3 (Composition API), Vue Router, scoped CSS, existing API hooks (`useAgents`).

---

### Task 1: Remove "版本策略" from Sidebar.vue navigation

**Files:**
- Modify: `panel/frontend/src/components/layout/Sidebar.vue`

- [ ] **Step 1: Remove expanded nav link**

Delete the entire `RouterLink` block for `/versions` inside `.sidebar__nav` (lines 50-57):

```vue
      <RouterLink to="/versions" class="sidebar__nav-item" active-class="sidebar__nav-item--active">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M3 5h18"/>
          <path d="M3 12h10"/>
          <path d="M3 19h6"/>
        </svg>
        <span>版本策略</span>
      </RouterLink>
```

- [ ] **Step 2: Remove collapsed nav link**

Delete the entire `RouterLink` block for `/versions` inside `.sidebar__nav--collapsed` (lines 105-111):

```vue
      <RouterLink to="/versions" class="sidebar__nav-icon" title="版本策略" active-class="sidebar__nav-icon--active">
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M3 5h18"/>
          <path d="M3 12h10"/>
          <path d="M3 19h6"/>
        </svg>
      </RouterLink>
```

- [ ] **Step 3: Verify no other version policy references remain in Sidebar.vue**

Run: `grep -n "versions\|版本策略" panel/frontend/src/components/layout/Sidebar.vue`
Expected: no output.

- [ ] **Step 4: Commit**

```bash
git add panel/frontend/src/components/layout/Sidebar.vue
git commit -m "feat(nav): remove version policy from sidebar"
```

---

### Task 2: Verify BottomNav.vue has no version policy reference

**Files:**
- Verify: `panel/frontend/src/components/layout/BottomNav.vue`

- [ ] **Step 1: Search for any version/versions references**

Run: `grep -n "versions\|版本策略" panel/frontend/src/components/layout/BottomNav.vue`
Expected: no output (nothing to change).

If output exists, remove the corresponding `RouterLink` or dropdown item.

- [ ] **Step 2: Commit (if changes were made)**

Skip if no changes.

---

### Task 3: Refactor AgentsPage.vue to card-grid with local search

**Files:**
- Modify: `panel/frontend/src/pages/AgentsPage.vue`

- [ ] **Step 1: Add search input to header**

Replace the header right-side div with a version that includes the search wrapper (before the buttons), mirroring RulesPage:

Old:
```vue
      <div style="display:flex;gap:0.5rem">
```

New:
```vue
      <div class="agents-page__header-right">
        <div class="search-wrapper" v-if="agents.length" @click="focusSearch">
          <svg class="search-icon-btn" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/></svg>
          <input ref="searchInputRef" v-model="searchQuery" name="agent-search" class="search-input" placeholder="搜索节点名称 / IP / 标签 / #id=...">
          <button v-if="searchQuery" class="clear-btn" @click.stop="searchQuery = ''">
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
          </button>
        </div>
```

Close the `div` after the "加入节点" button (replace the closing `</div>` that was at line 20 with the same but now it closes `agents-page__header-right`).

- [ ] **Step 2: Add search state and filtered agents logic**

In `<script setup>`, add:

```js
// Search
const searchQuery = ref('')
const searchInputRef = ref(null)
function focusSearch() { searchInputRef.value?.focus() }

const filteredAgents = computed(() => {
  const raw = searchQuery.value.trim()
  if (!raw) return agents.value
  const idMatch = raw.match(/^#id=(\S+)$/)
  if (idMatch) return agents.value.filter(agent => String(agent.id) === idMatch[1])
  const q = raw.toLowerCase()
  return agents.value.filter(agent =>
    String(agent.name || '').toLowerCase().includes(q) ||
    String(agent.agent_url || '').toLowerCase().includes(q) ||
    String(agent.last_seen_ip || '').toLowerCase().includes(q) ||
    (agent.tags || []).some(tag => String(tag).toLowerCase().includes(q))
  )
})
```

- [ ] **Step 3: Update list rendering to use card grid**

Replace the `.agents-list` block with:

```vue
    <!-- No search results -->
    <div v-if="agents.length && !filteredAgents.length" class="agents-page__empty">
      <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
        <circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/>
      </svg>
      <p>没有匹配的节点</p>
    </div>

    <!-- Card grid -->
    <div v-if="filteredAgents.length" class="agent-grid">
      <div v-for="agent in filteredAgents" :key="agent.id" class="agent-card" @click="router.push(`/agents/${agent.id}`)">
        <div class="agent-card__header">
          <div class="agent-card__badges">
            <span class="agent-card__status-badge" :class="`agent-card__status-badge--${getStatus(agent)}`">{{ getStatusLabel(agent) }}</span>
            <span class="agent-card__mode-badge">{{ getModeLabel(agent.mode) }}</span>
          </div>
          <div class="agent-card__actions" @click.stop>
            <button class="btn btn-secondary btn-sm" @click="startRename(agent)">重命名</button>
            <button v-if="!agent.is_local" class="btn btn-danger btn-sm" @click="startDelete(agent)">删除</button>
          </div>
        </div>
        <div class="agent-card__name">{{ agent.name }}</div>
        <div class="agent-card__url">{{ agent.agent_url ? getHostname(agent.agent_url) : (agent.last_seen_ip || '—') }}</div>
        <div class="agent-card__stats">
          <span class="agent-card__stat">
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/></svg>
            HTTP {{ agent.http_rules_count || 0 }}
          </span>
          <span class="agent-card__stat">
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="2" y="2" width="20" height="8" rx="2"/><rect x="2" y="14" width="20" height="8" rx="2"/></svg>
            L4 {{ agent.l4_rules_count || 0 }}
          </span>
          <span class="agent-card__last-seen">{{ timeAgo(agent.last_seen_at) }}</span>
        </div>
      </div>
    </div>

    <div v-if="!agents.length && !isLoading" class="agents-page__empty">
      <p>暂无节点</p>
    </div>
```

Delete the old `.agents-list` and `.agent-card` structure entirely.

- [ ] **Step 4: Add helper `getStatusLabel`**

Add in `<script setup>`:

```js
function getStatusLabel(agent) {
  const map = { online: '在线', offline: '离线', failed: '失败', pending: '同步中' }
  return map[getStatus(agent)] || '—'
}
```

- [ ] **Step 5: Replace scoped CSS with new grid styles**

Replace the entire `<style scoped>` block with:

```vue
<style scoped>
.agents-page { max-width: 1200px; margin: 0 auto; }
.agents-page__header { display: flex; align-items: center; justify-content: space-between; margin-bottom: 1.5rem; gap: 1rem; flex-wrap: wrap; }
.agents-page__header-right { display: flex; align-items: center; gap: 0.75rem; flex-shrink: 0; }
.agents-page__title { font-size: 1.5rem; font-weight: 700; margin: 0 0 0.25rem; color: var(--color-text-primary); }
.agents-page__subtitle { font-size: 0.875rem; color: var(--color-text-tertiary); margin: 0; }

/* Search wrapper */
.search-wrapper { display: flex; align-items: center; position: relative; }
.search-icon-btn { display: none; }
.search-input { flex: 1; min-width: 0; padding: 0.5rem 2rem 0.5rem 0.75rem; border-radius: var(--radius-lg); border: 1.5px solid var(--color-border-default); background: var(--color-bg-subtle); font-size: 0.875rem; color: var(--color-text-primary); outline: none; font-family: inherit; transition: border-color 0.15s, width 0.2s; box-sizing: border-box; }
.search-input:focus { border-color: var(--color-primary); width: 280px; }
.search-input::placeholder { color: var(--color-text-muted); }
.clear-btn { display: flex; align-items: center; justify-content: center; width: 18px; height: 18px; border: none; background: var(--color-bg-hover); border-radius: 50%; color: var(--color-text-secondary); cursor: pointer; flex-shrink: 0; padding: 0; position: absolute; right: 8px; z-index: 2; }

@media (max-width: 640px) {
  .search-wrapper { width: 36px; height: 36px; border-radius: var(--radius-lg); border: 1.5px solid var(--color-border-default); background: var(--color-bg-subtle); cursor: pointer; display: flex; align-items: center; justify-content: center; position: relative; }
  .search-icon-btn { display: flex; color: var(--color-text-secondary); }
  .search-input { position: absolute; left: 0; top: 0; width: 200px; height: 36px; opacity: 0; pointer-events: none; transition: opacity 0.2s, width 0.2s; }
  .search-wrapper:focus-within { width: 200px; }
  .search-wrapper:focus-within .search-input { opacity: 1; pointer-events: auto; border-color: var(--color-primary); }
  .search-wrapper:focus-within .clear-btn { opacity: 1; pointer-events: auto; }
  .clear-btn { opacity: 0; pointer-events: none; position: absolute; right: 8px; z-index: 2; transition: opacity 0.2s; }
}

/* Card grid */
.agent-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(280px, 1fr)); gap: 1rem; }
.agent-card {
  background: var(--color-bg-surface);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  padding: 1.125rem 1.25rem;
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
  cursor: pointer;
  transition: border-color 0.15s, transform 0.1s;
}
.agent-card:hover { border-color: var(--color-primary); transform: translateY(-1px); }
.agent-card__header { display: flex; align-items: center; justify-content: space-between; margin-bottom: 0.125rem; }
.agent-card__badges { display: flex; align-items: center; gap: 0.5rem; }
.agent-card__status-badge { font-size: 0.75rem; font-weight: 600; padding: 2px 8px; border-radius: var(--radius-full); }
.agent-card__status-badge--online { background: var(--color-success-50); color: var(--color-success); }
.agent-card__status-badge--offline { background: var(--color-bg-subtle); color: var(--color-text-muted); }
.agent-card__status-badge--failed { background: var(--color-danger-50); color: var(--color-danger); }
.agent-card__status-badge--pending { background: var(--color-warning-50); color: var(--color-warning); }
.agent-card__mode-badge { font-size: 0.75rem; padding: 1px 6px; background: var(--color-primary-subtle); color: var(--color-primary); border-radius: var(--radius-full); font-weight: 500; }
.agent-card__name { font-size: 1rem; font-weight: 600; color: var(--color-text-primary); }
.agent-card__url { font-size: 0.8125rem; color: var(--color-text-tertiary); font-family: var(--font-mono); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.agent-card__stats { display: flex; align-items: center; gap: 0.75rem; margin-top: 0.25rem; }
.agent-card__stat { display: flex; align-items: center; gap: 0.25rem; font-size: 0.75rem; color: var(--color-text-tertiary); }
.agent-card__last-seen { font-size: 0.75rem; color: var(--color-text-muted); margin-left: auto; }
.agent-card__actions { display: flex; gap: 0.5rem; }

.agents-page__empty, .agents-page__loading { display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 0.75rem; padding: 4rem 2rem; color: var(--color-text-muted); text-align: center; }

.btn { padding: 0.5rem 1rem; border-radius: var(--radius-lg); font-size: 0.875rem; font-weight: 500; cursor: pointer; transition: all 0.15s; border: none; font-family: inherit; display: inline-flex; align-items: center; gap: 0.375rem; }
.btn-primary { background: var(--gradient-primary); color: white; }
.btn-secondary { background: var(--color-bg-subtle); color: var(--color-text-primary); border: 1px solid var(--color-border-default); }
.btn-danger { background: var(--color-danger); color: white; }
.btn-sm { padding: 0.25rem 0.75rem; font-size: 0.8125rem; }
.spinner { width: 24px; height: 24px; border: 2px solid var(--color-border-default); border-top-color: var(--color-primary); border-radius: 50%; animation: spin 1s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }

/* Modals */
.modal-overlay { position: fixed; inset: 0; background: rgba(0,0,0,0.5); backdrop-filter: blur(4px); z-index: var(--z-modal); display: flex; align-items: center; justify-content: center; }
.modal { background: var(--color-bg-surface); border: 1.5px solid var(--color-border-default); border-radius: var(--radius-2xl); box-shadow: var(--shadow-xl); width: min(500px, 90vw); overflow: hidden; }
.modal--lg { width: min(640px, 92vw); }
.modal__header { padding: 1rem 1.5rem; font-weight: 600; font-size: 1rem; border-bottom: 1px solid var(--color-border-subtle); display: flex; justify-content: space-between; align-items: center; }
.modal__close { background: none; border: none; font-size: 1rem; cursor: pointer; color: var(--color-text-muted); }
.modal__body { padding: 1.5rem; display: flex; flex-direction: column; gap: 1.25rem; }
.modal__footer { padding: 1rem 1.5rem; display: flex; justify-content: flex-end; gap: 0.75rem; border-top: 1px solid var(--color-border-subtle); }
.join-tabs { display: flex; gap: 0.5rem; }
.join-tab { flex: 1; padding: 0.5rem; border: none; border-radius: var(--radius-lg); background: var(--color-bg-subtle); color: var(--color-text-secondary); font-size: 0.875rem; cursor: pointer; transition: all 0.15s; font-family: inherit; }
.join-tab.active { background: var(--color-primary); color: white; }
.join-command { display: flex; align-items: center; gap: 0.75rem; padding: 0.75rem 1rem; background: var(--color-bg-subtle); border-radius: var(--radius-lg); font-family: var(--font-mono); font-size: 0.8125rem; overflow-x: auto; }
.join-command code { flex: 1; word-break: break-all; overflow-x: auto; white-space: pre; color: var(--color-text-primary); line-height: 1.6; }
.join-steps { counter-reset: step; list-style: none; padding: 0; margin: 0; display: flex; flex-direction: column; gap: 0.5rem; }
.join-steps li { font-size: 0.875rem; color: var(--color-text-secondary); padding-left: 1.25rem; position: relative; }
.join-steps li::before { content: counter(step) "."; counter-increment: step; position: absolute; left: 0; color: var(--color-primary); font-weight: 600; }
.form-group { display: flex; flex-direction: column; gap: 0.375rem; }
.form-group label { font-size: 0.875rem; font-weight: 500; color: var(--color-text-secondary); }
.input-base { width: 100%; padding: 0.5rem 0.75rem; border-radius: var(--radius-lg); border: 1.5px solid var(--color-border-default); background: var(--color-bg-subtle); font-size: 0.875rem; color: var(--color-text-primary); outline: none; font-family: inherit; box-sizing: border-box; transition: border-color 0.15s; }
.input-base:focus { border-color: var(--color-primary); }
</style>
```

- [ ] **Step 6: Verify AgentsPage.vue compiles syntactically**

Run: `cd panel/frontend && npx vue-tsc --noEmit 2>&1 | head -30`
Expected: No syntax errors related to AgentsPage.vue.

- [ ] **Step 7: Commit**

```bash
git add panel/frontend/src/pages/AgentsPage.vue
git commit -m "feat(agents): refactor to card grid with local search"
```

---

### Task 4: Extend GlobalSearch.vue to include agents

**Files:**
- Modify: `panel/frontend/src/components/GlobalSearch.vue`

- [ ] **Step 1: Add agent search logic in `doSearch`**

Inside `doSearch`, after fetching `agents`, add agent filtering before building `groupResults`:

Locate this block in `doSearch`:
```js
    const q = val.toLowerCase()
    const groupResults = []
```

After it, insert:

```js
    // Agent results
    const matchedAgents = agents.filter(a =>
      String(a.name || '').toLowerCase().includes(q) ||
      String(a.agent_url || '').toLowerCase().includes(q) ||
      String(a.last_seen_ip || '').toLowerCase().includes(q) ||
      (a.tags || []).some(tag => String(tag).toLowerCase().includes(q))
    )
```

Then, before the `for (const agent of agents)` loop, add a push for the agents group:

```js
    if (matchedAgents.length) {
      groupResults.push(makeResult('agent', null, '节点', null, matchedAgents.map(a => ({ ...a, _type: 'agent' }))))
    }
```

- [ ] **Step 2: Update `navigateToItem` to handle agent type**

In `<script setup>`, modify `navigateToItem`:

```js
function navigateToItem(agentId, item) {
  close()
  if (item._type === 'agent') {
    router.push(`/agents/${item.id}`)
  } else if (item._type === 'rule') {
    router.push({ path: '/rules', query: { agentId, search: `#id=${item.id}` } })
  } else if (item._type === 'l4') {
    router.push({ path: '/l4', query: { agentId, search: `#id=${item.id}` } })
  } else if (item._type === 'cert') {
    router.push({ path: '/certs', query: { agentId, search: `#id=${item.id}` } })
  }
}
```

- [ ] **Step 3: Update `typeLabel` to return label for agent**

Change `typeLabel` to:

```js
function typeLabel(type) {
  return type === 'rule' ? 'HTTP' : type === 'l4' ? 'L4' : type === 'cert' ? '证书' : '节点'
}
```

- [ ] **Step 4: Add agent-specific result item UI in template**

In the template inside `.result-item`, update the `.result-item__info` block to handle agents:

Replace:
```vue
                <div class="result-item__info">
                  <div class="result-item__url">{{ item.frontend_url || item.domain || `${item.listen_host || ''}:${item.listen_port}` || `#${item.id}` }}</div>
                  <div v-if="item._type === 'rule'" class="result-item__backend">→ {{ formatHttpBackend(item) }}</div>
                  <div v-else-if="item._type === 'l4'" class="result-item__backend">{{ item.protocol?.toUpperCase() }} {{ item.listen_host || '*' }}:{{ item.listen_port }} → {{ formatL4Backend(item) }}</div>
                  <div v-else-if="item._type === 'cert'" class="result-item__backend">{{ getCertStatus(item) }}</div>
                </div>
```

With:
```vue
                <div class="result-item__info">
                  <div class="result-item__url">
                    {{ item._type === 'agent'
                      ? item.name
                      : item.frontend_url || item.domain || `${item.listen_host || ''}:${item.listen_port}` || `#${item.id}` }}
                  </div>
                  <div v-if="item._type === 'agent'" class="result-item__backend">{{ item.agent_url || item.last_seen_ip || '—' }}</div>
                  <div v-else-if="item._type === 'rule'" class="result-item__backend">→ {{ formatHttpBackend(item) }}</div>
                  <div v-else-if="item._type === 'l4'" class="result-item__backend">{{ item.protocol?.toUpperCase() }} {{ item.listen_host || '*' }}:{{ item.listen_port }} → {{ formatL4Backend(item) }}</div>
                  <div v-else-if="item._type === 'cert'" class="result-item__backend">{{ getCertStatus(item) }}</div>
                </div>
```

- [ ] **Step 5: Update group header click handler for agents**

In the `.result-group__header` click handler, agents should not navigate to a rules page. Replace the current header with:

```vue
              <div class="result-group__header" @click="group.agentId ? navigateToResult(group.agentId) : null">
```

- [ ] **Step 6: Add badge color for agent type**

Add the following CSS inside `<style scoped>`:

```css
.result-item__type-badge--agent { background: #f3e8ff; color: #7e22ce; }
```

- [ ] **Step 7: Verify GlobalSearch.vue compiles syntactically**

Run: `cd panel/frontend && npx vue-tsc --noEmit 2>&1 | head -30`
Expected: No syntax errors related to GlobalSearch.vue.

- [ ] **Step 8: Commit**

```bash
git add panel/frontend/src/components/GlobalSearch.vue
git commit -m "feat(search): include agents in global search"
```

---

### Task 5: Build and verify

**Files:**
- All of `panel/frontend`

- [ ] **Step 1: Run production build**

Run:
```bash
cd panel/frontend && npm run build
```

Expected: Build completes with 0 errors.

- [ ] **Step 2: If build fails, fix errors and repeat**

Address any TypeScript or Vite build errors in the modified files.

- [ ] **Step 3: Final commit (if fixes were needed)**

If any fixes were applied:
```bash
git commit -m "fix(frontend): resolve build errors after agents refactor"
```

---

## Self-Review

1. **Spec coverage:**
   - AgentsPage card grid + local search → Task 3.
   - GlobalSearch agent support → Task 4.
   - Remove version policy from nav → Task 1 (Sidebar) + Task 2 (BottomNav).
   - All covered.

2. **Placeholder scan:**
   - No TODOs/TBDs. All code snippets are complete.

3. **Type consistency:**
   - `getStatusLabel` mirrors `AgentDetailPage.vue` naming.
   - `_type: 'agent'` is used consistently in Task 4.
   - `makeResult` signature unchanged; agent group passes `agentId: null`.
