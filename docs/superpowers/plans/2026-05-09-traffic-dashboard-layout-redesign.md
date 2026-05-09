# 流量统计看板布局优化 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Redesign the homepage traffic stats dashboard to move stats into a top bar, remove bottom stats row, and rebalance the three-column layout.

**Architecture:** All changes are confined to a single Vue component (`DashboardTrafficModule.vue`). The top bar gains an inline stats strip. The bottom stats row is removed. The three columns are reordered. The trend chart is given `flex: 1` to fill remaining vertical space. Top rules/nodes capped at 5.

**Tech Stack:** Vue 3 (Composition API, `<script setup>`), CSS Grid/Flexbox

---

### Task 1: Add inline stats bar to the top bar header

**Files:**
- Modify: `panel/frontend/src/components/traffic/DashboardTrafficModule.vue`

- [ ] **Step 1: Update the template — top bar header with inline stats**

Replace the existing `<div class="dashboard-traffic__header">` block (lines 3-26) with the following:

```vue
<div class="dashboard-traffic__header">
  <div class="dashboard-traffic__header-left">
    <h2 class="dashboard-traffic__title">流量统计</h2>
    <div class="dashboard-traffic__stats-inline" v-if="statsVisible">
      <span class="dt-stat-inline" :class="{ 'dt-stat-inline--alert': blockedCount > 0 }">
        <span class="dt-stat-inline__label">阻断</span>
        <span class="dt-stat-inline__value">{{ blockedCount }} / {{ overviewAgents.length }}</span>
      </span>
      <span class="dt-stat-inline">
        <span class="dt-stat-inline__label">已用/额度</span>
        <span class="dt-stat-inline__value">{{ formatBytes(selectedSummary?.used_bytes || 0) }} / {{ selectedSummary?.quota_bytes != null ? formatBytes(selectedSummary.quota_bytes) : '—' }}</span>
      </span>
      <span class="dt-stat-inline">
        <span class="dt-stat-inline__label">剩余</span>
        <span class="dt-stat-inline__value" :class="{ 'dt-stat-inline__value--success': (selectedSummary?.remaining_bytes || 0) > 0 }">{{ remainingLabel }}</span>
      </span>
    </div>
  </div>
  <div class="dashboard-traffic__toolbar">
    <div class="dashboard-traffic__granularity" role="group" aria-label="趋势粒度">
      <button
        v-for="option in granularityOptions"
        :key="option.value"
        type="button"
        class="dashboard-traffic__granularity-btn"
        :class="{ 'dashboard-traffic__granularity-btn--active': granularity === option.value }"
        @click="granularity = option.value"
      >
        {{ option.label }}
      </button>
    </div>
    <AgentPicker
      :agents="selectableAgents"
      v-model:model-id="selectedAgentId"
      :show-all-option="true"
      all-label="全部节点"
      class="dashboard-traffic__agent-picker"
    />
  </div>
</div>
```

Add the `statsVisible` computed property in the `<script setup>` section (after `blockedCount`):

```js
const statsVisible = computed(() => overviewAgents.value.length > 0)
```

- [ ] **Step 2: Add CSS for the inline stats bar**

Add these styles to the `<style scoped>` section (before the `.spinner` rule):

```css
.dashboard-traffic__header-left {
  display: flex;
  align-items: center;
  gap: 1.25rem;
  min-width: 0;
}
.dashboard-traffic__stats-inline {
  display: flex;
  align-items: center;
  gap: 1rem;
}
.dt-stat-inline {
  display: inline-flex;
  align-items: center;
  gap: 0.3rem;
  font-size: 0.75rem;
}
.dt-stat-inline--alert {
  color: var(--color-danger, #ef4444);
}
.dt-stat-inline__label {
  color: var(--color-text-tertiary);
  font-weight: 500;
  text-transform: uppercase;
  font-size: 0.65rem;
  letter-spacing: 0.3px;
}
.dt-stat-inline__value {
  font-weight: 700;
  color: var(--color-text-primary);
  font-variant-numeric: tabular-nums;
}
.dt-stat-inline__value--success {
  color: var(--color-success, #34d399);
}
```

- [ ] **Step 3: Verify the dev build**

Run: `cd panel/frontend && npm run build`
Expected: no errors

- [ ] **Step 4: Commit**

```bash
git add panel/frontend/src/components/traffic/DashboardTrafficModule.vue
git commit -m "feat(panel): move stats into top bar of traffic dashboard"
```

---

### Task 2: Reorder three-column layout, add cycle card, cap top lists at 5

**Files:**
- Modify: `panel/frontend/src/components/traffic/DashboardTrafficModule.vue`

- [ ] **Step 1: Replace the three-column grid template**

Replace the entire `<div class="dashboard-traffic__grid">` block (lines 33-84) with:

```vue
<div class="dashboard-traffic__grid">
  <!-- Left Column -->
  <div class="dashboard-traffic__col">
    <div class="dt-card">
      <h3 class="dt-card__title">流量分布</h3>
      <TrafficQuotaRing
        :used-bytes="selectedSummary?.used_bytes ?? 0"
        :quota-bytes="selectedSummary?.quota_bytes ?? null"
        :remaining-bytes="selectedSummary?.remaining_bytes ?? null"
        :agents="selectedAgentId && selectedSummary ? [selectedSummary] : overviewAgents"
      />
    </div>
    <div class="dt-card">
      <h3 class="dt-card__title">计费周期</h3>
      <div class="dt-cycle">
        <span class="dt-cycle__value">{{ cycleLabel }}</span>
      </div>
      <div class="dt-cycle__meta">
        <span>方向: {{ directionLabel }}</span>
        <span v-if="dailyBudgetText">{{ dailyBudgetText }}</span>
      </div>
    </div>
    <div class="dt-card">
      <h3 class="dt-card__title">Top 节点</h3>
      <div v-for="(node, i) in topNodes" :key="node.agent_id" class="dt-top-item" @click="navigateToAgent(node)">
        <span class="dt-top-item__rank" :style="rankStyle(i)">{{ i + 1 }}</span>
        <span class="dt-top-item__name">{{ node.name || node.agent_id }}</span>
        <span class="dt-top-item__value">{{ formatBytes(node.used_bytes) }}</span>
      </div>
      <p v-if="!topNodes.length" class="dt-card__empty">暂无节点数据</p>
    </div>
  </div>

  <!-- Center Column -->
  <div class="dashboard-traffic__col dashboard-traffic__col--center">
    <div class="dt-card dt-card--tall">
      <h3 class="dt-card__title">流量趋势</h3>
      <TrafficTrendChart
        :points="trendPoints"
        :granularity="granularity"
        :quota-bytes="selectedSummary?.quota_bytes ?? null"
      />
    </div>
  </div>

  <!-- Right Column -->
  <div class="dashboard-traffic__col">
    <div class="dt-card dt-card--grow">
      <h3 class="dt-card__title">Top 规则</h3>
      <div v-for="(rule, i) in topRules" :key="topRuleKey(rule)" class="dt-top-rule" @click="navigateToAgent(rule)">
        <div class="dt-top-rule__info">
          <span class="dt-top-rule__name">{{ rule.label }}</span>
          <span class="dt-top-rule__value">{{ formatBytes(rule.accounted_bytes) }}</span>
        </div>
        <div class="dt-top-rule__bar">
          <div class="dt-top-rule__fill" :style="{ width: topRulePercent(rule) + '%', background: DISTRIBUTION_COLORS[i % DISTRIBUTION_COLORS.length] }" />
        </div>
      </div>
      <p v-if="!topRules.length" class="dt-card__empty">暂无规则数据</p>
    </div>
    <div class="dt-card">
      <h3 class="dt-card__title">Top 节点</h3>
      <div v-for="(node, i) in topNodes" :key="'right-' + node.agent_id" class="dt-top-item" @click="navigateToAgent(node)">
        <span class="dt-top-item__rank" :style="rankStyle(i)">{{ i + 1 }}</span>
        <span class="dt-top-item__name">{{ node.name || node.agent_id }}</span>
        <span class="dt-top-item__value">{{ formatBytes(node.used_bytes) }}</span>
      </div>
      <p v-if="!topNodes.length" class="dt-card__empty">暂无节点数据</p>
    </div>
  </div>
</div>
```

- [ ] **Step 2: Cap `topRules` and `topNodes` at 5**

Replace the `topNodes` computed (line 236-247) with:

```js
const topNodes = computed(() => {
  const nodes = aggregateQuery.data.value?.top_nodes ?? []
  if (nodes.length) return nodes.slice(0, 5)
  if (!import.meta.env.DEV) return []
  const agents = [...overviewAgents.value]
  agents.sort((a, b) => {
    const pa = a.quota_bytes ? a.used_bytes / a.quota_bytes : a.used_bytes
    const pb = b.quota_bytes ? b.used_bytes / b.quota_bytes : b.used_bytes
    return pb - pa
  })
  return agents.slice(0, 5)
})
```

Replace the `topRules` computed (line 249) with:

```js
const topRules = computed(() => (aggregateQuery.data.value?.top_rules ?? []).slice(0, 5))
```

- [ ] **Step 3: Add CSS for the cycle card**

Add these styles to `<style scoped>` (after `.dt-card__empty`):

```css
.dt-cycle__value {
  display: block;
  font-size: 0.875rem;
  font-weight: 600;
  color: var(--color-text-primary);
  margin-bottom: 0.25rem;
}
.dt-cycle__meta {
  display: flex;
  flex-direction: column;
  gap: 0.125rem;
  font-size: 0.75rem;
  color: var(--color-text-tertiary);
}
```

- [ ] **Step 4: Verify the dev build**

Run: `cd panel/frontend && npm run build`
Expected: no errors

- [ ] **Step 5: Commit**

```bash
git add panel/frontend/src/components/traffic/DashboardTrafficModule.vue
git commit -m "feat(panel): reorder traffic dashboard columns, cap top lists at 5"
```

---

### Task 3: Remove bottom stats row, make trend chart fill height, update responsive

**Files:**
- Modify: `panel/frontend/src/components/traffic/DashboardTrafficModule.vue`

- [ ] **Step 1: Remove the bottom stats row**

Delete the entire `<div class="dashboard-traffic__stats">` block (lines 87-111):

```vue
<!-- DELETE these lines entirely: -->
<div class="dashboard-traffic__stats">
  <div class="dt-stat" :class="{ 'dt-stat--alert': blockedCount > 0 }">
    ...
  </div>
  <div class="dt-stat">
    ...
  </div>
  <div class="dt-stat">
    ...
  </div>
  <div class="dt-stat">
    ...
  </div>
</div>
```

- [ ] **Step 2: Update grid CSS for responsive breakpoints**

Replace the existing `@media` blocks (lines 581-617) with:

```css
@media (max-width: 1023px) {
  .dashboard-traffic__grid {
    grid-template-columns: 1fr 1fr;
  }
  .dashboard-traffic__col--center {
    grid-column: 1 / -1;
    order: -1;
  }
  .dashboard-traffic__header-left {
    flex-wrap: wrap;
    gap: 0.5rem;
  }
  .dashboard-traffic__stats-inline {
    gap: 0.75rem;
  }
}

@media (max-width: 640px) {
  .dashboard-traffic__grid {
    grid-template-columns: 1fr;
  }
  .dashboard-traffic__col--center {
    order: 0;
  }
  .dashboard-traffic__header {
    flex-direction: column;
    align-items: flex-start;
    gap: 0.75rem;
  }
  .dashboard-traffic__toolbar {
    width: 100%;
    flex-wrap: wrap;
  }
  .dashboard-traffic__agent-picker {
    flex: 1;
    min-width: 0;
  }
  .dashboard-traffic__stats-inline {
    display: none;
  }
}
```

- [ ] **Step 3: Remove old bottom stats CSS**

Delete these CSS rules from `<style scoped>` (lines 520-569):

```css
/* DELETE these rules: */
.dashboard-traffic__stats { ... }
.dt-stat { ... }
.dt-stat--alert { ... }
.dt-stat__label { ... }
.dt-stat__value { ... }
.dt-stat__value--success { ... }
.dt-stat__sub { ... }
.dt-stat__sub--alert { ... }
.dt-stat__track { ... }
.dt-stat__fill { ... }
```

- [ ] **Step 4: Verify the dev build**

Run: `cd panel/frontend && npm run build`
Expected: no errors

- [ ] **Step 5: Run frontend tests**

Run: `cd panel/frontend && npx vitest run --reporter=verbose`
Expected: all tests pass

- [ ] **Step 6: Commit**

```bash
git add panel/frontend/src/components/traffic/DashboardTrafficModule.vue
git commit -m "feat(panel): remove bottom stats, make trend chart fill height"
```

---

### Task 4: Visual verification in dev server

**Files:**
- No file changes

- [ ] **Step 1: Start the dev server**

Run: `cd panel/frontend && npm run dev`

- [ ] **Step 2: Verify layout in browser**

Open the dev server URL and confirm:
1. Top bar shows: 流量统计 title + inline stats (阻断 / 已用/额度 / 剩余) + granularity toggle + agent picker
2. Left column: 流量分布 ring → 计费周期 → Top 节点 list
3. Center column: 流量趋势 chart fills remaining vertical height
4. Right column: Top 规则 (max 5) → Top 节点 (max 5)
5. No bottom stats row visible
6. Responsive: shrink window to <1024px and <640px to verify breakpoints

- [ ] **Step 3: Final commit if any adjustments needed**

```bash
git add panel/frontend/src/components/traffic/DashboardTrafficModule.vue
git commit -m "fix(panel): polish traffic dashboard layout"
```
