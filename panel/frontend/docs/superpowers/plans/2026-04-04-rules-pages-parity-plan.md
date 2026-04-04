# Rules Pages Feature Parity Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bring HTTP rules, L4 rules, and certificate management pages to feature parity with the `develop` branch by adding search, tag filtering, copy, rich cards, and using existing form components.

**Architecture:** Three pages (`RulesPage.vue`, `L4RulesPage.vue`, `CertsPage.vue`) are modified in-place to add search/tag-filter computed logic, replace inline forms with existing `RuleForm`/`L4RuleForm`/`CertificateForm` components, and add copy functionality. A new `L4RuleItem.vue` card component is created for rich L4 rule display. The `L4RuleForm.vue` template is refactored into three tabs.

**Tech Stack:** Vue 3 Composition API, TanStack Vue Query, CSS custom properties

---

### Task 1: Create L4RuleItem.vue card component

**Files:**
- Create: `panel/frontend/src/components/l4/L4RuleItem.vue`

- [ ] **Step 1: Create the L4RuleItem component**

Create `panel/frontend/src/components/l4/L4RuleItem.vue` with the following content. This is a presentational card component that emits events for parent actions.

Props: `rule` (Object, required)
Emits: `edit`, `delete`, `copy`, `toggle`

The component displays:
- Header: `#id` badge, protocol badge (TCP blue / UDP purple), status badge, action buttons (toggle pause/play, copy, edit, delete)
- Listen address: `listen_host:listen_port` in mono font
- Arrow: `→`
- Upstream: single backend shows `host:port`, multiple shows `primary +N` with `title` tooltip listing all backends
- Load balancing badge: `RR`/`LC`/`RND`/`HASH` with hover title
- Tuning summary tags: compact badges for non-default tuning values (timeouts, limits, health check, proxy protocol, reuseport, etc.)
- User tags row at bottom

```vue
<template>
  <div class="l4-card" :class="{ 'l4-card--disabled': !rule.enabled }">
    <div class="l4-card__header">
      <div class="l4-card__badges">
        <span class="l4-card__id">#{{ rule.id }}</span>
        <span class="l4-card__proto" :class="`l4-card__proto--${rule.protocol}`">
          {{ rule.protocol?.toUpperCase() }}
        </span>
        <span class="l4-card__status" :class="`l4-card__status--${status}`">
          {{ statusLabel }}
        </span>
      </div>
      <div class="l4-card__actions">
        <button class="l4-card__action l4-card__action--toggle" :title="rule.enabled ? '停用' : '启用'" @click="$emit('toggle', rule)">
          <svg v-if="rule.enabled" width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><rect x="6" y="4" width="4" height="16" rx="1"/><rect x="14" y="4" width="4" height="16" rx="1"/></svg>
          <svg v-else width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><polygon points="5 3 19 12 5 21 5 3"/></svg>
        </button>
        <button class="l4-card__action" title="复制" @click="$emit('copy', rule)">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>
        </button>
        <button class="l4-card__action" title="编辑" @click="$emit('edit', rule)">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/><path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/></svg>
        </button>
        <button class="l4-card__action l4-card__action--delete" title="删除" @click="$emit('delete', rule)">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="3 6 5 6 21 6"/><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/></svg>
        </button>
      </div>
    </div>
    <div class="l4-card__mapping">
      <div class="l4-card__endpoint">
        <code class="l4-card__addr">{{ rule.listen_host }}:{{ rule.listen_port }}</code>
      </div>
      <span class="l4-card__arrow">→</span>
      <div class="l4-card__endpoint">
        <code class="l4-card__addr" v-if="!hasMultipleBackends">{{ primaryBackend }}</code>
        <code class="l4-card__addr" v-else :title="backendsTooltip">{{ primaryBackend }} <span class="l4-card__more">+{{ backendCount - 1 }}</span></code>
        <span class="l4-card__lb" :title="lbTitle">{{ lbLabel }}</span>
      </div>
    </div>
    <div v-if="tuningTags.length" class="l4-card__tuning">
      <span v-for="tag in tuningTags" :key="tag" class="l4-card__tuning-tag">{{ tag }}</span>
    </div>
    <div v-if="rule.tags?.length" class="l4-card__tags">
      <span v-for="tag in rule.tags" :key="tag" class="tag">{{ tag }}</span>
    </div>
  </div>
</template>

<script setup>
import { computed } from 'vue'

const props = defineProps({ rule: { type: Object, required: true } })
defineEmits(['edit', 'delete', 'copy', 'toggle'])

const status = computed(() => {
  if (!props.rule.enabled) return 'disabled'
  if (props.rule.last_apply_status === 'failed') return 'failed'
  return 'active'
})
const statusLabel = computed(() => ({ active: '启用', disabled: '已禁用', failed: '同步失败' }[status.value]))

const backends = computed(() => {
  if (Array.isArray(props.rule.backends) && props.rule.backends.length > 0) return props.rule.backends
  if (props.rule.upstream_host && props.rule.upstream_port) return [{ host: props.rule.upstream_host, port: props.rule.upstream_port }]
  return []
})
const backendCount = computed(() => backends.value.length)
const hasMultipleBackends = computed(() => backendCount.value > 1)
const primaryBackend = computed(() => { const b = backends.value[0]; return b ? `${b.host}:${b.port}` : '-' })
const backendsTooltip = computed(() => backends.value.map((b, i) => {
  let s = `${i + 1}. ${b.host}:${b.port}`
  if (b.weight > 1) s += ` (权重${b.weight})`
  if (b.backup) s += ' [备用]'
  return s
}).join('\n'))

const LB_MAP = { round_robin: 'RR', least_conn: 'LC', random: 'RND', hash: 'HASH' }
const LB_TITLES = { round_robin: '轮询 (Round Robin)', least_conn: '最少连接', random: '随机', hash: '哈希 (Hash)' }
const lbLabel = computed(() => LB_MAP[props.rule.load_balancing?.strategy] || 'RR')
const lbTitle = computed(() => LB_TITLES[props.rule.load_balancing?.strategy] || '轮询')

const tuningTags = computed(() => {
  const t = props.rule.tuning
  if (!t) return []
  const tags = []
  const isUdp = props.rule.protocol === 'udp'
  const defaultIdle = isUdp ? '20s' : '10m'
  if (t.proxy?.idle_timeout && t.proxy.idle_timeout !== defaultIdle) tags.push(`超时:${t.proxy.idle_timeout}`)
  if (t.proxy?.connect_timeout && t.proxy.connect_timeout !== '10s') tags.push(`连接:${t.proxy.connect_timeout}`)
  if (t.limit_conn?.count && Number(t.limit_conn.count) > 0) tags.push(`限连:${t.limit_conn.count}`)
  const mf = t.upstream?.max_fails
  const ft = t.upstream?.fail_timeout
  if ((mf !== undefined && mf !== 3) || (ft && ft !== '30s')) tags.push(`健检:${mf ?? 3}/${ft || '30s'}`)
  if (t.listen?.reuseport === true && !isUdp) tags.push('reuseport')
  if (t.listen?.so_keepalive === true) tags.push('keepalive')
  if (t.proxy?.buffer_size && t.proxy.buffer_size !== '16k') tags.push(`buf:${t.proxy.buffer_size}`)
  if (t.proxy_protocol?.decode) tags.push('PP接收')
  if (t.proxy_protocol?.send) tags.push('PP发送')
  return tags
})
</script>

<style scoped>
.l4-card {
  background: var(--color-bg-surface);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  padding: 1.25rem;
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
  transition: opacity 0.15s;
}
.l4-card--disabled { opacity: 0.6; }
.l4-card__header { display: flex; align-items: center; justify-content: space-between; }
.l4-card__badges { display: flex; align-items: center; gap: 0.5rem; }
.l4-card__id { font-size: 0.75rem; font-family: var(--font-mono); color: var(--color-text-tertiary); }
.l4-card__proto { display: inline-block; font-size: 0.7rem; font-weight: 700; padding: 2px 6px; border-radius: var(--radius-sm); font-family: var(--font-mono); }
.l4-card__proto--tcp { background: var(--color-warning-50); color: var(--color-warning); }
.l4-card__proto--udp { background: #f3e8ff; color: #7c3aed; }
.l4-card__status { font-size: 0.75rem; font-weight: 600; padding: 2px 8px; border-radius: var(--radius-full); }
.l4-card__status--active { background: var(--color-success-50); color: var(--color-success); }
.l4-card__status--disabled { background: var(--color-bg-subtle); color: var(--color-text-muted); }
.l4-card__status--failed { background: var(--color-danger-50); color: var(--color-danger); }
.l4-card__actions { display: flex; gap: 0.25rem; opacity: 0; transition: opacity 0.15s; }
.l4-card:hover .l4-card__actions { opacity: 1; }
.l4-card__action { display: flex; align-items: center; justify-content: center; width: 28px; height: 28px; border-radius: var(--radius-md); border: none; background: transparent; color: var(--color-text-tertiary); cursor: pointer; transition: all 0.15s; }
.l4-card__action:hover { background: var(--color-bg-hover); color: var(--color-text-primary); }
.l4-card__action--delete:hover { background: var(--color-danger-50); color: var(--color-danger); }
.l4-card__action--toggle:hover { background: var(--color-warning-50); color: var(--color-warning); }
.l4-card__mapping { display: flex; align-items: center; gap: 0.75rem; }
.l4-card__endpoint { display: flex; align-items: center; gap: 0.5rem; flex: 1; min-width: 0; }
.l4-card__addr { font-family: var(--font-mono); font-size: 0.9375rem; font-weight: 600; color: var(--color-text-primary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.l4-card__arrow { color: var(--color-text-muted); font-size: 0.875rem; flex-shrink: 0; }
.l4-card__more { color: var(--color-text-muted); font-weight: 400; }
.l4-card__lb { font-size: 0.7rem; font-weight: 700; padding: 1px 6px; background: var(--color-primary-subtle); color: var(--color-primary); border-radius: var(--radius-sm); flex-shrink: 0; }
.l4-card__tuning { display: flex; gap: 0.25rem; flex-wrap: wrap; }
.l4-card__tuning-tag { font-size: 0.7rem; padding: 1px 6px; background: var(--color-bg-subtle); border: 1px solid var(--color-border-subtle); border-radius: var(--radius-sm); color: var(--color-text-secondary); font-family: var(--font-mono); }
.l4-card__tags { display: flex; gap: 0.25rem; flex-wrap: wrap; }
.tag { font-size: 0.75rem; padding: 2px 8px; background: var(--color-primary-subtle); color: var(--color-primary); border-radius: var(--radius-full); font-weight: 500; }
</style>
```

- [ ] **Step 2: Verify the file compiles**

Run: `cd panel/frontend && npx vue-tsc --noEmit 2>&1 | head -20` or `npm run build 2>&1 | tail -20`
Expected: No errors related to L4RuleItem

- [ ] **Step 3: Commit**

```bash
git add panel/frontend/src/components/l4/L4RuleItem.vue
git commit -m "feat(frontend): add L4RuleItem card component with tuning summary"
```

---

### Task 2: Refactor L4RuleForm.vue into tab layout

**Files:**
- Modify: `panel/frontend/src/components/L4RuleForm.vue`

The script logic (`buildPayload`, `hasTuningChanges`, backend management, tag logic) stays unchanged. Only the `<template>` is restructured into three tabs, and two new per-tab computed properties are added.

- [ ] **Step 1: Add per-tab hasTuningChanges computed properties and activeTab ref**

In `<script setup>`, after the existing `hasTuningChanges` computed (around line 462), add:

```javascript
const activeTab = ref('basic')

const hasAdvancedTuning = computed(() => {
  const defaults = getDefaultTuning(form.value.protocol)
  const t = form.value.tuning
  const hasBackendExtensions = form.value.backends.some(b => b.backup || (b.max_conns && b.max_conns > 0))
  return (
    hasBackendExtensions ||
    t.proxy.connect_timeout !== defaults.proxy.connect_timeout ||
    t.proxy.idle_timeout !== defaults.proxy.idle_timeout ||
    t.proxy.buffer_size !== defaults.proxy.buffer_size ||
    t.upstream.max_conns !== defaults.upstream.max_conns ||
    t.upstream.max_fails !== defaults.upstream.max_fails ||
    t.upstream.fail_timeout !== defaults.upstream.fail_timeout ||
    t.limit_conn.count !== defaults.limit_conn.count
  )
})

const hasProtocolTuning = computed(() => {
  const defaults = getDefaultTuning(form.value.protocol)
  const t = form.value.tuning
  return (
    t.proxy_protocol.decode !== defaults.proxy_protocol.decode ||
    t.proxy_protocol.send !== defaults.proxy_protocol.send ||
    t.listen.reuseport !== defaults.listen.reuseport ||
    t.listen.tcp_nodelay !== defaults.listen.tcp_nodelay ||
    t.listen.so_keepalive !== defaults.listen.so_keepalive ||
    (t.listen.backlog !== null && t.listen.backlog !== defaults.listen.backlog) ||
    (form.value.protocol === 'udp' && (
      (t.proxy.udp_proxy_requests !== null && t.proxy.udp_proxy_requests !== defaults.proxy.udp_proxy_requests) ||
      (t.proxy.udp_proxy_responses !== null && t.proxy.udp_proxy_responses !== defaults.proxy.udp_proxy_responses)
    ))
  )
})
```

- [ ] **Step 2: Replace the template with tab layout**

Replace the entire `<template>` content. The template wraps the existing form fields into three tab panels controlled by `activeTab`. Tab 1 contains everything from Protocol through Tags + Enabled toggle. Tab 2 contains Timeouts, Health Check, Connection Limit, Buffer, and Backend Extensions groups. Tab 3 contains Proxy Protocol, Listen Options, and UDP-specific groups.

The tab bar looks like:
```html
<div class="form-tabs">
  <button type="button" class="form-tabs__btn" :class="{ 'form-tabs__btn--active': activeTab === 'basic' }" @click="activeTab = 'basic'">基础配置</button>
  <button type="button" class="form-tabs__btn" :class="{ 'form-tabs__btn--active': activeTab === 'advanced' }" @click="activeTab = 'advanced'">高级调优 <span v-if="hasAdvancedTuning" class="form-tabs__badge">已配置</span></button>
  <button type="button" class="form-tabs__btn" :class="{ 'form-tabs__btn--active': activeTab === 'protocol' }" @click="activeTab = 'protocol'">协议与监听 <span v-if="hasProtocolTuning" class="form-tabs__badge">已配置</span></button>
</div>
```

Then `v-if="activeTab === 'basic'"` / `v-if="activeTab === 'advanced'"` / `v-if="activeTab === 'protocol'"` wrap each section.

The "高级参数" collapsible `<div class="advanced-section">` wrapper and its toggle button are removed. Each group that was inside it is now directly inside the appropriate tab panel.

The Tags, error display, enabled toggle, and submit button remain outside the tabs (always visible at the bottom).

- [ ] **Step 3: Add tab CSS styles**

Add to `<style scoped>`:

```css
.form-tabs {
  display: flex;
  border-bottom: 1px solid var(--color-border-default);
  gap: 0;
  margin-bottom: var(--space-4);
}

.form-tabs__btn {
  padding: var(--space-3) var(--space-4);
  border: none;
  background: transparent;
  cursor: pointer;
  font-size: var(--text-sm);
  font-weight: var(--font-medium);
  color: var(--color-text-muted);
  border-bottom: 2px solid transparent;
  transition: all var(--duration-fast);
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.form-tabs__btn:hover {
  color: var(--color-text-secondary);
  background: var(--color-bg-hover);
}

.form-tabs__btn--active {
  color: var(--color-primary);
  border-bottom-color: var(--color-primary);
}

.form-tabs__badge {
  font-size: 9px;
  font-weight: var(--font-bold);
  padding: 1px 6px;
  background: var(--color-primary-subtle);
  color: var(--color-primary);
  border-radius: var(--radius-sm);
}
```

Remove the old `.advanced-section`, `.advanced-toggle`, `.advanced-toggle__icon`, `.advanced-toggle__badge` CSS rules.

- [ ] **Step 4: Verify build**

Run: `cd panel/frontend && npm run build 2>&1 | tail -10`
Expected: Build succeeds

- [ ] **Step 5: Commit**

```bash
git add panel/frontend/src/components/L4RuleForm.vue
git commit -m "refactor(frontend): reorganize L4RuleForm into tab layout"
```

---

### Task 3: Rewrite L4RulesPage.vue to use L4RuleForm + L4RuleItem

**Files:**
- Modify: `panel/frontend/src/pages/L4RulesPage.vue`

The page currently has an inline basic form and simple cards. Replace with: import `L4RuleForm` and `L4RuleItem`, add search + tag filter, add copy modal.

- [ ] **Step 1: Rewrite L4RulesPage.vue**

Replace the entire file. Key changes:

**Script changes:**
- Import `L4RuleForm` from `../components/L4RuleForm.vue`
- Import `L4RuleItem` from `../components/l4/L4RuleItem.vue`
- Add `searchQuery = ref('')` and `selectedTags = ref([])`
- Add `filteredRules` computed that filters `rules` by search query and selected tags (OR logic)
- Add `allTags` computed that extracts unique tags from rules, sorted alphabetically
- Add `toggleTag(tag)` function to toggle tag in `selectedTags`
- Add `copyingRule = ref(null)` and `showCopyModal = ref(false)`
- Add `handleCopy(rule)` that strips `id` from rule and opens copy modal
- Remove the old inline `form` ref and `submitForm`/`startEdit` functions
- Keep `toggleRule`, `startDelete`, `confirmDelete`

**Template changes:**
- Add search bar below header (after the header `<div>`)
- Add tag filter row below search bar (only shown when `allTags.length > 0`)
- Replace card grid items with `<L4RuleItem>` component, emitting `@edit`, `@delete`, `@copy`, `@toggle`
- Add "no search results" empty state between the rules grid and loading state
- Replace inline add/edit form modal with `<L4RuleForm :initial-data="editingRule" :agent-id="agentId" @success="closeForm" />`
- Add copy modal: `<L4RuleForm :initial-data="copyingRule" :agent-id="agentId" @success="showCopyModal = false" />`

**Filter logic:**
```javascript
const filteredRules = computed(() => {
  let result = rules.value
  // Tag filter (OR logic)
  if (selectedTags.value.length > 0) {
    result = result.filter(rule =>
      selectedTags.value.some(tag => (rule.tags || []).includes(tag))
    )
  }
  // Search query
  const raw = searchQuery.value.trim()
  if (!raw) return result
  const idMatch = raw.match(/^#id=(\S+)$/)
  if (idMatch) return result.filter(rule => String(rule.id) === idMatch[1])
  const q = raw.toLowerCase()
  return result.filter(rule =>
    String(rule.protocol || '').toLowerCase().includes(q) ||
    String(rule.listen_host || '').toLowerCase().includes(q) ||
    String(rule.upstream_host || '').toLowerCase().includes(q) ||
    String(rule.listen_port || '').includes(q) ||
    (rule.tags || []).some(tag => String(tag).toLowerCase().includes(q))
  )
})
```

**System tag detection for tag filter styling:**
```javascript
const SYSTEM_TAG_SET = new Set(['TCP', 'UDP', 'HTTP', 'HTTPS', 'RR', 'LC', 'RND', 'HASH'])
function isSystemTag(tag) {
  return SYSTEM_TAG_SET.has(tag) || /^:\d+$/.test(tag)
}
```

System tags in the filter row get class `tag--system` (de-emphasized with lighter styling).

The modal for add/edit uses the `L4RuleForm` component:
```html
<Teleport to="body">
  <div v-if="showAddForm || editingRule" class="modal-overlay" @click.self="closeForm">
    <div class="modal modal--large">
      <div class="modal__header">{{ editingRule ? '编辑 L4 规则' : '添加 L4 规则' }}</div>
      <div class="modal__body">
        <L4RuleForm :initial-data="editingRule" :agent-id="agentId" @success="closeForm" />
      </div>
    </div>
  </div>
</Teleport>
```

Add `.modal--large` CSS: `width: min(600px, 92vw)` to accommodate the tabbed form.

- [ ] **Step 2: Verify build**

Run: `cd panel/frontend && npm run build 2>&1 | tail -10`
Expected: Build succeeds

- [ ] **Step 3: Commit**

```bash
git add panel/frontend/src/pages/L4RulesPage.vue
git commit -m "feat(frontend): rewrite L4RulesPage with search, tag filter, copy, and rich cards"
```

---

### Task 4: Update RulesPage.vue (HTTP rules) with search, tag filter, copy, and RuleForm component

**Files:**
- Modify: `panel/frontend/src/pages/RulesPage.vue`

- [ ] **Step 1: Add search, tag filter, copy, and use RuleForm component**

Modify `RulesPage.vue`. Key changes:

**Script changes:**
- Import `RuleForm` from `../components/RuleForm.vue`
- Add `searchQuery = ref('')`, `selectedTags = ref([])`
- Add `filteredRules` computed with same pattern as Task 3 but for HTTP rules (search `frontend_url`, `backend_url`, `name`, `tags`, `id`)
- Add `allTags` computed extracting tags from `rules`
- Add `toggleTag(tag)`, `isSystemTag(tag)` (same as Task 3)
- Add `copyingRule = ref(null)`, `showCopyModal = ref(false)`, `handleCopy(rule)`
- Remove old inline `form` ref and `submitForm` function
- `startEdit(rule)` now sets `editingRule.value = rule` (no form state needed — RuleForm handles it)
- `closeForm()` resets `showAddForm`, `editingRule`, `showCopyModal`, `copyingRule`

**Template changes:**
- Add search bar below header
- Add tag filter row
- Add `#id` badge to each card header (before the icon)
- Add copy button to card actions
- Replace inline form modal with `<RuleForm :initial-data="editingRule" :agent-id="agentId" @success="closeForm" />`
- Add copy modal: `<RuleForm :initial-data="copyingRule" :agent-id="agentId" @success="showCopyModal = false" />`
- Add "no search results" empty state
- Show all tags on cards (remove `.slice(0, 3)`)

**Filter logic:**
```javascript
const filteredRules = computed(() => {
  let result = rules.value
  if (selectedTags.value.length > 0) {
    result = result.filter(rule =>
      selectedTags.value.some(tag => (rule.tags || []).includes(tag))
    )
  }
  const raw = searchQuery.value.trim()
  if (!raw) return result
  const idMatch = raw.match(/^#id=(\S+)$/)
  if (idMatch) return result.filter(rule => String(rule.id) === idMatch[1])
  const q = raw.toLowerCase()
  return result.filter(rule =>
    String(rule.frontend_url || '').toLowerCase().includes(q) ||
    String(rule.backend_url || '').toLowerCase().includes(q) ||
    String(rule.name || '').toLowerCase().includes(q) ||
    (rule.tags || []).some(tag => String(tag).toLowerCase().includes(q))
  )
})
```

- [ ] **Step 2: Verify build**

Run: `cd panel/frontend && npm run build 2>&1 | tail -10`
Expected: Build succeeds

- [ ] **Step 3: Commit**

```bash
git add panel/frontend/src/pages/RulesPage.vue
git commit -m "feat(frontend): add search, tag filter, copy, and RuleForm to HTTP rules page"
```

---

### Task 5: Update CertsPage.vue with search, tag filter, and CertificateForm component

**Files:**
- Modify: `panel/frontend/src/pages/CertsPage.vue`

- [ ] **Step 1: Add search, tag filter, use CertificateForm, show error/date**

Modify `CertsPage.vue`. Key changes:

**Script changes:**
- Import `CertificateForm` from `../components/CertificateForm.vue`
- Add `searchQuery = ref('')`, `selectedTags = ref([])`
- Add `filteredCerts` computed (search `domain`, `tags`, `id`)
- Add `allTags` computed
- Add `toggleTag(tag)`, `isSystemTag(tag)`
- Remove old inline `form` ref and `submitForm`
- `startEdit(cert)` sets `editingCert.value = cert`

**Template changes:**
- Add search bar below header
- Add tag filter row
- Add `#id` badge to each card
- Show `last_error` on card when present: `<p v-if="cert.last_error" class="cert-card__error">{{ cert.last_error }}</p>`
- Show `last_issue_at` on card when present: `<span v-if="cert.last_issue_at" class="cert-card__date">{{ formatDate(cert.last_issue_at) }}</span>`
- Add a `formatDate(dateStr)` helper in script that formats ISO date to `YYYY-MM-DD HH:mm`
- Replace inline form modal with `<CertificateForm :initial-data="editingCert" :agent-id="agentId" @success="closeForm" />`
- Add "no search results" empty state
- Show all tags on cards (remove `.slice(0, 3)`)

**Filter logic:**
```javascript
const filteredCerts = computed(() => {
  let result = certificates.value
  if (selectedTags.value.length > 0) {
    result = result.filter(cert =>
      selectedTags.value.some(tag => (cert.tags || []).includes(tag))
    )
  }
  const raw = searchQuery.value.trim()
  if (!raw) return result
  const idMatch = raw.match(/^#id=(\S+)$/)
  if (idMatch) return result.filter(c => String(c.id) === idMatch[1])
  const q = raw.toLowerCase()
  return result.filter(c =>
    c.domain.toLowerCase().includes(q) ||
    (c.tags || []).some(tag => String(tag).toLowerCase().includes(q))
  )
})
```

**Additional CSS for error/date:**
```css
.cert-card__error {
  font-size: 0.75rem;
  color: var(--color-danger);
  background: var(--color-danger-50);
  padding: 0.25rem 0.5rem;
  border-radius: var(--radius-sm);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.cert-card__date {
  font-size: 0.75rem;
  color: var(--color-text-tertiary);
}
.cert-card__id {
  font-size: 0.75rem;
  font-family: var(--font-mono);
  color: var(--color-text-tertiary);
}
```

- [ ] **Step 2: Verify build**

Run: `cd panel/frontend && npm run build 2>&1 | tail -10`
Expected: Build succeeds

- [ ] **Step 3: Commit**

```bash
git add panel/frontend/src/pages/CertsPage.vue
git commit -m "feat(frontend): add search, tag filter, and CertificateForm to certs page"
```

---

### Task 6: Final verification and cleanup

- [ ] **Step 1: Full build check**

Run: `cd panel/frontend && npm run build 2>&1`
Expected: Build succeeds with no errors

- [ ] **Step 2: Check for console errors**

Run: `cd panel/frontend && npm run dev` and visually inspect the three pages in the browser:
- HTTP rules: search, tag filter, copy, add/edit with RuleForm
- L4 rules: search, tag filter, copy, tabbed form, rich cards with tuning tags
- Certificates: search, tag filter, error/date display, CertificateForm

- [ ] **Step 3: Remove stale L4RuleTable.vue if unused**

Check if `L4RuleTable.vue` is imported anywhere:
Run: `grep -r "L4RuleTable" panel/frontend/src/`
If no imports found, delete it:
```bash
git rm panel/frontend/src/components/l4/L4RuleTable.vue
git commit -m "chore(frontend): remove unused L4RuleTable component"
```
