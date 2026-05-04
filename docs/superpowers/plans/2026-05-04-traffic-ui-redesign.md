# Traffic UI Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Redesign traffic statistics frontend with Chart.js charts, dashboard overview module, per-rule trend popups, and collapsible node detail traffic tab.

**Architecture:** Add `chart.js` + `vue-chartjs` dependencies. Create reusable traffic chart components under `src/components/traffic/`. Add a backend `GET /api/traffic-overview` endpoint for dashboard aggregation. Modify existing rule pages to add clickable traffic lines that open trend modals. Restructure the node detail traffic tab into collapsible sections.

**Tech Stack:** Vue 3 Composition API, Chart.js, vue-chartjs, TanStack Vue Query, Go (Gin/stdlib), GORM/SQLite

---

## File Structure

### New files (frontend)

| File | Responsibility |
|---|---|
| `panel/frontend/src/components/traffic/TrafficTrendChart.vue` | Reusable Chart.js line/bar chart for traffic trends |
| `panel/frontend/src/components/traffic/TrafficTrendModal.vue` | Modal wrapping TrendChart with granularity toggle, uses BaseModal |
| `panel/frontend/src/components/traffic/TrafficSummaryCards.vue` | Monthly quota summary card group (used, quota, remaining, cycle, direction, blocked) |
| `panel/frontend/src/components/traffic/TrafficBreakdownTable.vue` | Per-scope breakdown table with clickable rows |
| `panel/frontend/src/components/traffic/TrafficPolicyForm.vue` | Traffic policy settings form (direction, quota, retention, blocking) |
| `panel/frontend/src/components/traffic/TrafficHistoryManager.vue` | Calibration + cleanup controls |
| `panel/frontend/src/components/traffic/TrafficCollapsibleSection.vue` | Collapsible section wrapper (header + body with expand/collapse) |
| `panel/frontend/src/components/traffic/DashboardTrafficModule.vue` | Dashboard traffic block (trend chart + node selector + summary cards) |
| `panel/frontend/src/hooks/useTrafficOverview.js` | Hook for `GET /api/traffic-overview` |

### New files (backend)

| File | Responsibility |
|---|---|
| (none — changes are in existing files) | |

### Modified files (frontend)

| File | Change |
|---|---|
| `panel/frontend/package.json` | Add chart.js, vue-chartjs dependencies |
| `panel/frontend/src/api/runtime.js` | Add `fetchTrafficOverview` function |
| `panel/frontend/src/pages/DashboardPage.vue` | Add DashboardTrafficModule |
| `panel/frontend/src/pages/AgentDetailPage.vue` | Restructure traffic tab into collapsible sections |
| `panel/frontend/src/pages/RulesPage.vue` | Add traffic trend modal state + pass to cards |
| `panel/frontend/src/pages/L4RulesPage.vue` | Add traffic trend modal state + pass to cards |
| `panel/frontend/src/pages/RelayListenersPage.vue` | Add traffic trend modal state + pass to cards |
| `panel/frontend/src/components/rules/RuleCard.vue` | Make traffic-line clickable, emit event |
| `panel/frontend/src/components/l4/L4RuleItem.vue` | Make traffic-line clickable, emit event |
| `panel/frontend/src/components/relay/RelayCard.vue` | Make traffic-line clickable, emit event |

### Modified files (backend)

| File | Change |
|---|---|
| `panel/backend-go/internal/controlplane/http/router.go` | Add `TrafficOverview` to `TrafficService` interface, add route |
| `panel/backend-go/internal/controlplane/http/handlers_traffic.go` | Add `handleTrafficOverview` handler |
| `panel/backend-go/internal/controlplane/service/traffic_types.go` | Add `TrafficOverviewAgent` and `TrafficOverviewResult` types |
| `panel/backend-go/internal/controlplane/service/traffic_service.go` | Add `Overview` method |
| `panel/backend-go/internal/controlplane/storage/traffic_store.go` | Add `ListAllAgentsTrafficTrend` method |

---

### Task 1: Install Chart.js dependencies

**Files:**
- Modify: `panel/frontend/package.json`

- [ ] **Step 1: Install chart.js and vue-chartjs**

```bash
cd panel/frontend && npm install chart.js vue-chartjs
```

- [ ] **Step 2: Verify installation**

```bash
cd panel/frontend && npm ls chart.js vue-chartjs
```

Expected: both packages listed with versions

- [ ] **Step 3: Verify build still passes**

```bash
cd panel/frontend && npm run build
```

Expected: build succeeds

- [ ] **Step 4: Commit**

```bash
git add panel/frontend/package.json panel/frontend/package-lock.json
git commit -m "chore(panel): add chart.js and vue-chartjs dependencies"
```

---

### Task 2: Create TrafficTrendChart component

**Files:**
- Create: `panel/frontend/src/components/traffic/TrafficTrendChart.vue`

This is the core reusable Chart.js chart. It receives trend data as props and renders a dual-series (RX/TX) line chart with an optional quota reference line.

- [ ] **Step 1: Create the component**

Create `panel/frontend/src/components/traffic/TrafficTrendChart.vue`:

```vue
<template>
  <div class="traffic-trend-chart">
    <canvas ref="canvasRef"></canvas>
  </div>
</template>

<script setup>
import { ref, watch, onMounted, onUnmounted } from 'vue'
import { Chart, registerables } from 'chart.js'
import { formatBytes } from '../../utils/trafficStats.js'

Chart.register(...registerables)

const props = defineProps({
  points: { type: Array, default: () => [] },
  granularity: { type: String, default: 'day' },
  quotaBytes: { type: Number, default: null }
})

const canvasRef = ref(null)
let chartInstance = null

function formatLabel(bucketStart) {
  if (!bucketStart) return ''
  const date = new Date(bucketStart)
  if (Number.isNaN(date.getTime())) return ''
  if (props.granularity === 'hour') {
    return date.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })
  }
  if (props.granularity === 'month') {
    return date.toLocaleDateString('zh-CN', { year: '2-digit', month: 'short' })
  }
  return date.toLocaleDateString('zh-CN', { month: 'short', day: 'numeric' })
}

function buildConfig() {
  const labels = props.points.map(p => formatLabel(p.bucket_start))
  const rxData = props.points.map(p => Number(p.rx_bytes) || 0)
  const txData = props.points.map(p => Number(p.tx_bytes) || 0)
  const datasets = [
    {
      label: 'RX',
      data: rxData,
      borderColor: 'rgba(99, 102, 241, 0.9)',
      backgroundColor: 'rgba(99, 102, 241, 0.1)',
      fill: true,
      tension: 0.3,
      pointRadius: 2,
      pointHoverRadius: 4
    },
    {
      label: 'TX',
      data: txData,
      borderColor: 'rgba(16, 185, 129, 0.9)',
      backgroundColor: 'rgba(16, 185, 129, 0.1)',
      fill: true,
      tension: 0.3,
      pointRadius: 2,
      pointHoverRadius: 4
    }
  ]
  if (props.quotaBytes != null && props.quotaBytes > 0) {
    datasets.push({
      label: '月额度',
      data: labels.map(() => props.quotaBytes),
      borderColor: 'rgba(239, 68, 68, 0.5)',
      borderDash: [6, 4],
      borderWidth: 1,
      pointRadius: 0,
      fill: false
    })
  }
  return {
    type: 'line',
    data: { labels, datasets },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      interaction: { mode: 'index', intersect: false },
      plugins: {
        legend: { display: true, position: 'top', labels: { boxWidth: 12, padding: 12, font: { size: 12 } } },
        tooltip: {
          callbacks: {
            label: (ctx) => {
              const value = ctx.parsed.y
              return ` ${ctx.dataset.label}: ${formatBytes(value)}`
            }
          }
        }
      },
      scales: {
        x: {
          grid: { display: false },
          ticks: { maxRotation: 45, font: { size: 11 } }
        },
        y: {
          beginAtZero: true,
          grid: { color: 'rgba(0, 0, 0, 0.05)' },
          ticks: {
            font: { size: 11 },
            callback: (value) => formatBytes(value)
          }
        }
      }
    }
  }
}

function renderChart() {
  if (!canvasRef.value) return
  if (chartInstance) {
    chartInstance.destroy()
    chartInstance = null
  }
  chartInstance = new Chart(canvasRef.value.getContext('2d'), buildConfig())
}

onMounted(renderChart)

watch(() => [props.points, props.granularity, props.quotaBytes], renderChart, { deep: true })

onUnmounted(() => {
  if (chartInstance) {
    chartInstance.destroy()
    chartInstance = null
  }
})
</script>

<style scoped>
.traffic-trend-chart {
  position: relative;
  width: 100%;
  height: 280px;
}
</style>
```

- [ ] **Step 2: Verify build**

```bash
cd panel/frontend && npm run build
```

Expected: build succeeds (component is not imported yet, so just verifies no syntax errors)

- [ ] **Step 3: Commit**

```bash
git add panel/frontend/src/components/traffic/TrafficTrendChart.vue
git commit -m "feat(panel): add TrafficTrendChart component with Chart.js"
```

---

### Task 3: Create TrafficTrendModal component

**Files:**
- Create: `panel/frontend/src/components/traffic/TrafficTrendModal.vue`

This modal wraps TrafficTrendChart with a granularity toggle. It uses the existing BaseModal component.

- [ ] **Step 1: Create the component**

Create `panel/frontend/src/components/traffic/TrafficTrendModal.vue`:

```vue
<template>
  <BaseModal v-model="visible" :title="`流量趋势 — ${scopeLabel}`" size="lg">
    <div class="traffic-trend-modal">
      <div class="traffic-trend-modal__controls">
        <div class="traffic-trend-modal__granularity">
          <button
            v-for="opt in granularityOptions"
            :key="opt.value"
            class="traffic-trend-modal__mode"
            :class="{ 'traffic-trend-modal__mode--active': granularity === opt.value }"
            type="button"
            @click="granularity = opt.value"
          >
            {{ opt.label }}
          </button>
        </div>
      </div>
      <div v-if="trendQuery.isLoading.value" class="traffic-trend-modal__loading">
        <div class="spinner"></div>
      </div>
      <div v-else-if="trendPoints.length > 0" class="traffic-trend-modal__chart">
        <TrafficTrendChart :points="trendPoints" :granularity="granularity" />
      </div>
      <div v-else class="traffic-trend-modal__empty">暂无趋势数据</div>
      <div v-if="summaryText" class="traffic-trend-modal__summary">{{ summaryText }}</div>
    </div>
  </BaseModal>
</template>

<script setup>
import { ref, computed, watch } from 'vue'
import BaseModal from '../base/BaseModal.vue'
import TrafficTrendChart from './TrafficTrendChart.vue'
import { useTrafficTrend } from '../../hooks/useTraffic.js'
import { normalizeTrafficTrendPoints, formatBytes } from '../../utils/trafficStats.js'

const props = defineProps({
  visible: { type: Boolean, default: false },
  agentId: { type: String, default: '' },
  scopeType: { type: String, default: '' },
  scopeId: { type: String, default: '' },
  scopeLabel: { type: String, default: '' },
  direction: { type: String, default: 'both' }
})

const emit = defineEmits(['update:visible'])

const visible = computed({
  get: () => props.visible,
  set: (val) => emit('update:visible', val)
})

const granularityOptions = [
  { value: 'hour', label: '小时' },
  { value: 'day', label: '日' },
  { value: 'month', label: '月' }
]
const granularity = ref('day')

const trendQuery = useTrafficTrend(
  computed(() => props.visible ? props.agentId : null),
  computed(() => ({
    granularity: granularity.value,
    scope_type: props.scopeType,
    scope_id: String(props.scopeId)
  }))
)

const trendPoints = computed(() => normalizeTrafficTrendPoints(trendQuery.data.value ?? [], props.direction))

const summaryText = computed(() => {
  const points = trendPoints.value
  if (!points.length) return ''
  const totalRx = points.reduce((sum, p) => sum + (Number(p.rx_bytes) || 0), 0)
  const totalTx = points.reduce((sum, p) => sum + (Number(p.tx_bytes) || 0), 0)
  return `合计  RX ${formatBytes(totalRx)}  TX ${formatBytes(totalTx)}`
})

watch(() => props.visible, (val) => {
  if (val) {
    granularity.value = 'day'
  }
})
</script>

<style scoped>
.traffic-trend-modal__controls {
  display: flex;
  justify-content: flex-end;
  margin-bottom: 1rem;
}
.traffic-trend-modal__granularity {
  display: inline-flex;
  gap: 2px;
  padding: 2px;
  background: var(--color-bg-subtle);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
}
.traffic-trend-modal__mode {
  min-width: 2.75rem;
  padding: 0.3rem 0.55rem;
  border: 0;
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--color-text-tertiary);
  font-size: 0.75rem;
  font-weight: 600;
  cursor: pointer;
  font-family: inherit;
}
.traffic-trend-modal__mode--active {
  background: var(--color-bg-surface);
  color: var(--color-primary);
  box-shadow: var(--shadow-sm);
}
.traffic-trend-modal__chart {
  min-height: 280px;
}
.traffic-trend-modal__loading {
  display: flex;
  justify-content: center;
  padding: 3rem;
}
.traffic-trend-modal__empty {
  text-align: center;
  color: var(--color-text-muted);
  padding: 3rem;
  font-size: 0.875rem;
}
.traffic-trend-modal__summary {
  margin-top: 0.75rem;
  padding-top: 0.75rem;
  border-top: 1px solid var(--color-border-subtle);
  color: var(--color-text-secondary);
  font-size: 0.8125rem;
  font-variant-numeric: tabular-nums;
}
.spinner {
  width: 24px;
  height: 24px;
  border: 2px solid var(--color-border-default);
  border-top-color: var(--color-primary);
  border-radius: 50%;
  animation: spin 1s linear infinite;
}
@keyframes spin { to { transform: rotate(360deg); } }
</style>
```

- [ ] **Step 2: Verify build**

```bash
cd panel/frontend && npm run build
```

Expected: build succeeds

- [ ] **Step 3: Commit**

```bash
git add panel/frontend/src/components/traffic/TrafficTrendModal.vue
git commit -m "feat(panel): add TrafficTrendModal with granularity toggle"
```

---

### Task 4: Create TrafficCollapsibleSection component

**Files:**
- Create: `panel/frontend/src/components/traffic/TrafficCollapsibleSection.vue`

- [ ] **Step 1: Create the component**

Create `panel/frontend/src/components/traffic/TrafficCollapsibleSection.vue`:

```vue
<template>
  <div class="collapsible-section" :class="{ 'collapsible-section--collapsed': !expanded }">
    <button class="collapsible-section__header" type="button" @click="expanded = !expanded">
      <div class="collapsible-section__title-group">
        <h3 class="collapsible-section__title">{{ title }}</h3>
        <span v-if="subtitle" class="collapsible-section__subtitle">{{ subtitle }}</span>
      </div>
      <svg
        class="collapsible-section__chevron"
        width="16" height="16" viewBox="0 0 24 24"
        fill="none" stroke="currentColor" stroke-width="2"
      >
        <polyline points="6 9 12 15 18 9"/>
      </svg>
    </button>
    <Transition name="collapse">
      <div v-if="expanded" class="collapsible-section__body">
        <slot />
      </div>
    </Transition>
  </div>
</template>

<script setup>
import { ref } from 'vue'

const props = defineProps({
  title: { type: String, required: true },
  subtitle: { type: String, default: '' },
  defaultExpanded: { type: Boolean, default: false }
})

const expanded = ref(props.defaultExpanded)
</script>

<style scoped>
.collapsible-section {
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-lg);
  overflow: hidden;
}
.collapsible-section__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  width: 100%;
  padding: 0.875rem 1rem;
  background: transparent;
  border: none;
  cursor: pointer;
  font-family: inherit;
  text-align: left;
}
.collapsible-section__header:hover {
  background: var(--color-bg-hover);
}
.collapsible-section__title-group {
  display: flex;
  align-items: center;
  gap: 0.5rem;
}
.collapsible-section__title {
  margin: 0;
  font-size: 0.9375rem;
  font-weight: 600;
  color: var(--color-text-primary);
}
.collapsible-section__subtitle {
  color: var(--color-text-tertiary);
  font-size: 0.8125rem;
}
.collapsible-section__chevron {
  color: var(--color-text-tertiary);
  transition: transform var(--duration-normal) var(--ease-default);
  flex-shrink: 0;
}
.collapsible-section--collapsed .collapsible-section__chevron {
  transform: rotate(-90deg);
}
.collapsible-section__body {
  padding: 0 1rem 1rem;
}
.collapse-enter-active,
.collapse-leave-active {
  transition: all var(--duration-normal) var(--ease-default);
  overflow: hidden;
}
.collapse-enter-from,
.collapse-leave-to {
  opacity: 0;
  max-height: 0;
  padding-top: 0;
  padding-bottom: 0;
}
</style>
```

- [ ] **Step 2: Verify build**

```bash
cd panel/frontend && npm run build
```

Expected: build succeeds

- [ ] **Step 3: Commit**

```bash
git add panel/frontend/src/components/traffic/TrafficCollapsibleSection.vue
git commit -m "feat(panel): add TrafficCollapsibleSection component"
```

---

### Task 5: Create TrafficSummaryCards, TrafficBreakdownTable, TrafficPolicyForm, TrafficHistoryManager components

**Files:**
- Create: `panel/frontend/src/components/traffic/TrafficSummaryCards.vue`
- Create: `panel/frontend/src/components/traffic/TrafficBreakdownTable.vue`
- Create: `panel/frontend/src/components/traffic/TrafficPolicyForm.vue`
- Create: `panel/frontend/src/components/traffic/TrafficHistoryManager.vue`

These are extracted from the existing AgentDetailPage.vue traffic tab. They take props and emit events — no data fetching internally.

- [ ] **Step 1: Create TrafficSummaryCards.vue**

Create `panel/frontend/src/components/traffic/TrafficSummaryCards.vue`:

```vue
<template>
  <div class="traffic-summary-cards">
    <div class="traffic-summary-card">
      <span class="traffic-summary-card__label">已用</span>
      <span class="traffic-summary-card__value">{{ formatBytes(summary.used_bytes) }}</span>
    </div>
    <div class="traffic-summary-card">
      <span class="traffic-summary-card__label">月额度</span>
      <span class="traffic-summary-card__value">{{ formatQuota(summary.monthly_quota_bytes) }}</span>
    </div>
    <div class="traffic-summary-card">
      <span class="traffic-summary-card__label">剩余</span>
      <span class="traffic-summary-card__value">{{ summary.remaining_bytes == null ? '无限制' : formatBytes(summary.remaining_bytes) }}</span>
    </div>
    <div class="traffic-summary-card">
      <span class="traffic-summary-card__label">周期</span>
      <span class="traffic-summary-card__value">{{ summary.cycle_start ? formatCycle(summary.cycle_start, summary.cycle_end) : '—' }}</span>
    </div>
    <div class="traffic-summary-card">
      <span class="traffic-summary-card__label">计费方向</span>
      <span class="traffic-summary-card__value">{{ directionLabel }}</span>
    </div>
    <div class="traffic-summary-card" :class="{ 'traffic-summary-card--blocked': summary.blocked }">
      <span class="traffic-summary-card__label">状态</span>
      <span class="traffic-summary-card__value">{{ summary.blocked ? '已阻断' : '正常' }}</span>
    </div>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import { formatBytes, formatQuota } from '../../utils/trafficStats.js'

const props = defineProps({
  summary: { type: Object, default: () => ({}) },
  direction: { type: String, default: 'both' }
})

const directionLabel = computed(() => {
  switch (String(props.direction || 'both').toLowerCase()) {
    case 'rx': return '入站'
    case 'tx': return '出站'
    case 'max': return '取最大值'
    default: return '双向'
  }
})

function formatCycle(start, end) {
  if (!start || !end) return '—'
  return `${new Date(start).toLocaleDateString()} - ${new Date(end).toLocaleDateString()}`
}
</script>

<style scoped>
.traffic-summary-cards {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 0.75rem;
  margin-bottom: 1rem;
}
.traffic-summary-card {
  min-width: 0;
  padding: 0.75rem;
  background: var(--color-bg-subtle);
  border-radius: var(--radius-md);
}
.traffic-summary-card--blocked {
  background: var(--color-danger-50);
}
.traffic-summary-card__label {
  display: block;
  margin-bottom: 0.25rem;
  color: var(--color-text-tertiary);
  font-size: 0.75rem;
}
.traffic-summary-card__value {
  display: block;
  color: var(--color-text-primary);
  font-size: 1.125rem;
  font-weight: 700;
  font-variant-numeric: tabular-nums;
}
@media (max-width: 720px) {
  .traffic-summary-cards { grid-template-columns: repeat(2, 1fr); }
}
</style>
```

- [ ] **Step 2: Create TrafficBreakdownTable.vue**

Create `panel/frontend/src/components/traffic/TrafficBreakdownTable.vue`:

```vue
<template>
  <div class="traffic-breakdown">
    <div
      v-for="row in rows"
      :key="rowKey(row)"
      class="traffic-breakdown__row"
      :class="{ 'traffic-breakdown__row--clickable': clickable }"
      @click="clickable && $emit('click-row', row)"
    >
      <span class="traffic-breakdown__name">{{ rowLabel(row) }}</span>
      <span class="traffic-breakdown__value">{{ formatBytes(row.accounted_bytes) }}</span>
      <span class="traffic-breakdown__raw">RX {{ formatBytes(row.rx_bytes) }} / TX {{ formatBytes(row.tx_bytes) }}</span>
    </div>
    <p v-if="rows.length === 0" class="traffic-breakdown__empty">暂无分项流量</p>
  </div>
</template>

<script setup>
import { formatBytes } from '../../utils/trafficStats.js'

const props = defineProps({
  rows: { type: Array, default: () => [] },
  clickable: { type: Boolean, default: false }
})

defineEmits(['click-row'])

function rowKey(row) {
  return `${row.scope_type || 'scope'}-${row.scope_id || 'aggregate'}`
}

function rowLabel(row) {
  switch (row.scope_type) {
    case 'http': return 'HTTP'
    case 'l4': return 'L4'
    case 'relay': return 'Relay'
    case 'http_rule': return `HTTP 规则 #${row.scope_id}`
    case 'l4_rule': return `L4 规则 #${row.scope_id}`
    case 'relay_listener': return `Relay 监听 #${row.scope_id}`
    default: return row.scope_id ? `${row.scope_type} #${row.scope_id}` : row.scope_type || '-'
  }
}
</script>

<style scoped>
.traffic-breakdown { display: flex; flex-direction: column; gap: 0.35rem; }
.traffic-breakdown__row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto minmax(10rem, auto);
  gap: 0.75rem;
  align-items: center;
  padding: 0.55rem 0.65rem;
  background: var(--color-bg-subtle);
  border-radius: var(--radius-md);
  font-size: 0.8125rem;
}
.traffic-breakdown__row--clickable { cursor: pointer; }
.traffic-breakdown__row--clickable:hover { background: var(--color-bg-hover); }
.traffic-breakdown__name {
  min-width: 0;
  color: var(--color-text-primary);
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.traffic-breakdown__value { color: var(--color-text-primary); font-weight: 700; font-variant-numeric: tabular-nums; white-space: nowrap; }
.traffic-breakdown__raw { color: var(--color-text-tertiary); font-family: var(--font-mono); font-size: 0.75rem; text-align: right; white-space: nowrap; }
.traffic-breakdown__empty { text-align: center; color: var(--color-text-muted); padding: 1.5rem; font-size: 0.875rem; margin: 0; }
@media (max-width: 720px) {
  .traffic-breakdown__row { grid-template-columns: 1fr auto; }
  .traffic-breakdown__raw { grid-column: 1 / -1; text-align: left; }
}
</style>
```

- [ ] **Step 3: Create TrafficPolicyForm.vue**

Create `panel/frontend/src/components/traffic/TrafficPolicyForm.vue`:

```vue
<template>
  <div class="traffic-policy-form">
    <div class="traffic-policy-form__grid">
      <label class="traffic-policy-form__field">
        <span class="traffic-policy-form__label">方向</span>
        <select :value="modelValue.direction" class="traffic-policy-form__input" @change="updateField('direction', $event.target.value)">
          <option value="both">双向</option>
          <option value="rx">入站</option>
          <option value="tx">出站</option>
          <option value="max">取最大值</option>
        </select>
      </label>
      <label class="traffic-policy-form__field">
        <span class="traffic-policy-form__label">月周期起始日</span>
        <input :value="modelValue.cycle_start_day" class="traffic-policy-form__input" type="number" min="1" max="28" @input="updateField('cycle_start_day', Number($event.target.value))">
      </label>
      <label class="traffic-policy-form__field">
        <span class="traffic-policy-form__label">月额度</span>
        <div class="traffic-policy-form__quota">
          <input :value="modelValue.monthly_quota_value" class="traffic-policy-form__input" type="text" placeholder="留空表示无限制" @input="updateField('monthly_quota_value', $event.target.value)">
          <select :value="modelValue.monthly_quota_unit" class="traffic-policy-form__input traffic-policy-form__unit" @change="updateField('monthly_quota_unit', $event.target.value)">
            <option v-for="unit in quotaUnits" :key="unit.value" :value="unit.value">{{ unit.label }}</option>
          </select>
        </div>
      </label>
      <label class="traffic-policy-form__field traffic-policy-form__field--switch">
        <span class="traffic-policy-form__label">超额阻断</span>
        <input :checked="modelValue.block_when_exceeded" type="checkbox" @change="updateField('block_when_exceeded', $event.target.checked)">
      </label>
      <label class="traffic-policy-form__field">
        <span class="traffic-policy-form__label">小时保留</span>
        <input :value="modelValue.hourly_retention_days" class="traffic-policy-form__input" type="number" min="1" @input="updateField('hourly_retention_days', Number($event.target.value))">
      </label>
      <label class="traffic-policy-form__field">
        <span class="traffic-policy-form__label">日保留</span>
        <input :value="modelValue.daily_retention_months" class="traffic-policy-form__input" type="number" min="1" @input="updateField('daily_retention_months', Number($event.target.value))">
      </label>
      <label class="traffic-policy-form__field">
        <span class="traffic-policy-form__label">月保留</span>
        <input :value="modelValue.monthly_retention_months" class="traffic-policy-form__input" type="number" min="1" placeholder="留空表示永久" @input="updateField('monthly_retention_months', $event.target.value)">
      </label>
    </div>
    <div class="traffic-policy-form__footer">
      <button class="btn btn-primary" type="button" :disabled="saving" @click="$emit('save')">保存</button>
    </div>
  </div>
</template>

<script setup>
const props = defineProps({
  modelValue: { type: Object, required: true },
  saving: { type: Boolean, default: false }
})

const emit = defineEmits(['update:modelValue', 'save'])

const quotaUnits = [
  { value: 'B', label: 'B' },
  { value: 'KiB', label: 'KiB' },
  { value: 'MiB', label: 'MiB' },
  { value: 'GiB', label: 'GiB' },
  { value: 'TiB', label: 'TiB' }
]

function updateField(field, value) {
  emit('update:modelValue', { ...props.modelValue, [field]: value })
}
</script>

<style scoped>
.traffic-policy-form__grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 0.75rem;
}
.traffic-policy-form__field { display: flex; flex-direction: column; gap: 0.35rem; min-width: 0; }
.traffic-policy-form__field--switch { flex-direction: row; align-items: center; justify-content: space-between; }
.traffic-policy-form__label { color: var(--color-text-secondary); font-size: 0.8125rem; font-weight: 500; }
.traffic-policy-form__input {
  width: 100%;
  min-width: 0;
  padding: 0.5rem 0.75rem;
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
  background: var(--color-bg-surface);
  color: var(--color-text-primary);
  font-size: 0.875rem;
  box-sizing: border-box;
}
.traffic-policy-form__input:focus { outline: none; border-color: var(--color-primary); box-shadow: var(--shadow-focus); }
.traffic-policy-form__quota { display: grid; grid-template-columns: minmax(0, 1fr) 5.5rem; gap: 0.5rem; }
.traffic-policy-form__unit { font-family: var(--font-mono); }
.traffic-policy-form__footer { display: flex; justify-content: flex-end; margin-top: 0.75rem; }
.btn { padding: 0.5rem 1rem; border-radius: var(--radius-lg); font-size: 0.875rem; font-weight: 500; cursor: pointer; transition: all 0.15s; border: none; font-family: inherit; display: inline-flex; align-items: center; gap: 0.375rem; }
.btn-primary { background: var(--gradient-primary); color: white; }
.btn:disabled { opacity: 0.6; cursor: not-allowed; }
@media (max-width: 720px) { .traffic-policy-form__grid { grid-template-columns: 1fr; } }
</style>
```

- [ ] **Step 4: Create TrafficHistoryManager.vue**

Create `panel/frontend/src/components/traffic/TrafficHistoryManager.vue`:

```vue
<template>
  <div class="traffic-history-manager">
    <div class="traffic-history-manager__group">
      <h4 class="traffic-history-manager__heading">校准</h4>
      <div class="traffic-history-manager__actions">
        <button class="btn btn-secondary" type="button" :disabled="calibrating" @click="$emit('calibrate')">校准</button>
        <button class="btn btn-secondary" type="button" :disabled="calibrating" @click="$emit('calibrate-zero')">从现在归零</button>
      </div>
    </div>
    <div class="traffic-history-manager__group">
      <h4 class="traffic-history-manager__heading">历史数据</h4>
      <div class="traffic-history-manager__actions">
        <button class="btn btn-secondary" type="button" :disabled="cleaning" @click="$emit('cleanup')">清理过期数据</button>
      </div>
    </div>
  </div>
</template>

<script setup>
defineProps({
  calibrating: { type: Boolean, default: false },
  cleaning: { type: Boolean, default: false }
})

defineEmits(['calibrate', 'calibrate-zero', 'cleanup'])
</script>

<style scoped>
.traffic-history-manager { display: flex; flex-direction: column; gap: 1rem; }
.traffic-history-manager__group { }
.traffic-history-manager__heading { font-size: 0.875rem; font-weight: 600; color: var(--color-text-primary); margin: 0 0 0.5rem; }
.traffic-history-manager__actions { display: flex; gap: 0.5rem; flex-wrap: wrap; }
.btn { padding: 0.5rem 1rem; border-radius: var(--radius-lg); font-size: 0.875rem; font-weight: 500; cursor: pointer; transition: all 0.15s; border: none; font-family: inherit; display: inline-flex; align-items: center; gap: 0.375rem; }
.btn-secondary { background: var(--color-bg-subtle); color: var(--color-text-primary); border: 1px solid var(--color-border-default); }
.btn:disabled { opacity: 0.6; cursor: not-allowed; }
</style>
```

- [ ] **Step 5: Verify build**

```bash
cd panel/frontend && npm run build
```

Expected: build succeeds

- [ ] **Step 6: Commit**

```bash
git add panel/frontend/src/components/traffic/TrafficSummaryCards.vue panel/frontend/src/components/traffic/TrafficBreakdownTable.vue panel/frontend/src/components/traffic/TrafficPolicyForm.vue panel/frontend/src/components/traffic/TrafficHistoryManager.vue
git commit -m "feat(panel): add traffic summary, breakdown, policy, and history components"
```

---

### Task 6: Add backend traffic-overview API

**Files:**
- Modify: `panel/backend-go/internal/controlplane/service/traffic_types.go` — add overview types
- Modify: `panel/backend-go/internal/controlplane/service/traffic_service.go` — add Overview method
- Modify: `panel/backend-go/internal/controlplane/storage/traffic_store.go` — add aggregated trend query
- Modify: `panel/backend-go/internal/controlplane/http/router.go` — add route + interface method
- Modify: `panel/backend-go/internal/controlplane/http/handlers_traffic.go` — add handler

- [ ] **Step 1: Add overview types to traffic_types.go**

Append to `panel/backend-go/internal/controlplane/service/traffic_types.go`:

```go
type TrafficOverviewAgent struct {
	AgentID        string  `json:"agent_id"`
	Name           string  `json:"name"`
	UsedBytes      uint64  `json:"used_bytes"`
	QuotaBytes     *int64  `json:"quota_bytes"`
	RemainingBytes *int64  `json:"remaining_bytes"`
	Blocked        bool    `json:"blocked"`
	Direction      string  `json:"direction"`
}

type TrafficOverviewResult struct {
	Agents []TrafficOverviewAgent `json:"agents"`
	Trend  []TrafficTrendPoint    `json:"trend"`
}
```

- [ ] **Step 2: Add Overview method to traffic_service.go**

The method:
1. If `agentID` is provided, get that agent's summary + trend
2. If `agentID` is empty, get all agents' summaries + sum their `agent_total` daily trends

Add this method to `traffic_service` in `panel/backend-go/internal/controlplane/service/traffic_service.go`. The method needs access to agent names, so it calls the store's `ListAgents` or similar. Since the traffic service only has a store reference, the simplest approach is to iterate agent IDs from the traffic policies table, call `Summary` for each, and call `Trend` for each.

However, for efficiency with 20+ agents, we add a store method that returns all agent IDs that have traffic data.

Add to `panel/backend-go/internal/controlplane/storage/traffic_store.go`:

```go
func (s *GormStore) ListTrafficAgentIDs(ctx context.Context) ([]string, error) {
	var ids []string
	err := s.db.WithContext(ctx).
		Model(&AgentTrafficRawCursorRow{}).
		Distinct("agent_id").
		Pluck("agent_id", &ids).Error
	return ids, err
}

func (s *GormStore) ListAllAgentsDailyTrend(ctx context.Context) ([]TrafficBucketRow, error) {
	var rows []AgentTrafficDailySummaryRow
	err := s.db.WithContext(ctx).
		Where("scope_type = ? AND scope_id = ?", "agent_total", "").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	result := make([]TrafficBucketRow, 0, len(rows))
	for _, r := range rows {
		result = append(result, TrafficBucketRow{
			AgentID:     r.AgentID,
			BucketStart: r.PeriodStart,
			RXBytes:     r.RXBytes,
			TXBytes:     r.TXBytes,
		})
	}
	return result, err
}
```

Then in `traffic_service.go`, add the `Overview` method that:
1. Lists agent IDs from cursors
2. For each agent, calls `Summary` and builds a `TrafficOverviewAgent`
3. If a specific `agentID` is requested, only returns that agent's trend
4. If no specific agent, sums all agents' daily trend buckets by `bucket_start`

The service needs agent names. Add a `NameResolver` interface or simply have the Overview handler pass agent names from AgentService. The cleanest approach: add a method to the traffic service that accepts an optional `agentNameMap map[string]string` parameter. But to keep it simple, have the Overview handler in the HTTP layer resolve names using AgentService, then pass them to the traffic service.

**Simpler approach:** Add the Overview method to a new combined handler that uses both `AgentService` and `TrafficService` directly. The handler in `handlers_traffic.go` already has access to `Dependencies` which contains both services.

Implementation in `handlers_traffic.go`:

```go
func (d Dependencies) handleTrafficOverview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	if d.writeTrafficDisabledIfNeeded(w) {
		return
	}
	agentFilter := r.URL.Query().Get("agent_id")
	agents, err := d.AgentService.List(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorPayload("failed to list agents"))
		return
	}
	overviewAgents := make([]service.TrafficOverviewAgent, 0)
	for _, a := range agents {
		if agentFilter != "" && a.ID != agentFilter {
			continue
		}
		summary, err := d.TrafficService.Summary(r.Context(), a.ID)
		if err != nil {
			continue
		}
		overviewAgents = append(overviewAgents, service.TrafficOverviewAgent{
			AgentID:        a.ID,
			Name:           a.Name,
			UsedBytes:      summary.UsedBytes,
			QuotaBytes:     summary.MonthlyQuotaBytes,
			RemainingBytes: summary.RemainingBytes,
			Blocked:        summary.Blocked,
			Direction:      summary.Policy.Direction,
		})
	}
	var trend []service.TrafficTrendPoint
	if agentFilter != "" {
		trend, _ = d.TrafficService.Trend(r.Context(), service.TrafficTrendQuery{
			AgentID:     agentFilter,
			ScopeType:   "agent_total",
			Granularity: "day",
		})
	} else {
		trend = d.aggregateOverviewTrend(r.Context(), agents)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"agents": overviewAgents,
		"trend":  trend,
	})
}

func (d Dependencies) aggregateOverviewTrend(ctx context.Context, agents []service.AgentSummary) []service.TrafficTrendPoint {
	type bucketKey struct{ bucketStart string }
	merged := make(map[bucketKey]*service.TrafficTrendPoint)
	for _, a := range agents {
		points, err := d.TrafficService.Trend(ctx, service.TrafficTrendQuery{
			AgentID:     a.ID,
			ScopeType:   "agent_total",
			Granularity: "day",
		})
		if err != nil {
			continue
		}
		for _, p := range points {
			key := bucketKey{p.BucketStart}
			if existing, ok := merged[key]; ok {
				existing.RXBytes += p.RXBytes
				existing.TXBytes += p.TXBytes
				existing.AccountedBytes += p.AccountedBytes
			} else {
				merged[key] = &service.TrafficTrendPoint{
					BucketStart:    p.BucketStart,
					RXBytes:        p.RXBytes,
					TXBytes:        p.TXBytes,
					AccountedBytes: p.AccountedBytes,
				}
			}
		}
	}
	result := make([]service.TrafficTrendPoint, 0, len(merged))
	for _, p := range merged {
		result = append(result, *p)
	}
	slices.SortFunc(result, func(a, b service.TrafficTrendPoint) int {
		if a.BucketStart < b.BucketStart {
			return -1
		}
		if a.BucketStart > b.BucketStart {
			return 1
		}
		return 0
	})
	return result
}
```

Note: requires `"slices"` import and `"context"` import in `handlers_traffic.go`.

- [ ] **Step 3: Add route in router.go**

In `router.go`, inside the route registration loop (after the existing traffic-cleanup route), add:

```go
mux.Handle(prefix+"/traffic-overview", resolved.requirePanelToken(http.HandlerFunc(resolved.handleTrafficOverview)))
```

- [ ] **Step 4: Verify backend builds and tests pass**

```bash
cd panel/backend-go && go build ./... && go test ./...
```

Expected: builds and tests pass

- [ ] **Step 5: Commit**

```bash
git add panel/backend-go/internal/controlplane/service/traffic_types.go panel/backend-go/internal/controlplane/service/traffic_service.go panel/backend-go/internal/controlplane/storage/traffic_store.go panel/backend-go/internal/controlplane/http/router.go panel/backend-go/internal/controlplane/http/handlers_traffic.go
git commit -m "feat(backend): add traffic-overview API for dashboard aggregation"
```

---

### Task 7: Add frontend traffic-overview API + hook

**Files:**
- Modify: `panel/frontend/src/api/runtime.js` — add `fetchTrafficOverview`
- Create: `panel/frontend/src/hooks/useTrafficOverview.js`

- [ ] **Step 1: Add fetchTrafficOverview to runtime.js**

Append to `panel/frontend/src/api/runtime.js`:

```js
export async function fetchTrafficOverview(agentId) {
  const params = new URLSearchParams()
  if (agentId) params.set('agent_id', agentId)
  const suffix = params.toString() ? `?${params.toString()}` : ''
  const { data } = await api.get(`/traffic-overview${suffix}`)
  return data
}
```

- [ ] **Step 2: Create useTrafficOverview.js**

Create `panel/frontend/src/hooks/useTrafficOverview.js`:

```js
import { useQuery } from '@tanstack/vue-query'
import { computed, unref } from 'vue'
import { fetchTrafficOverview } from '../api'

export function useTrafficOverview(agentId) {
  return useQuery({
    queryKey: computed(() => ['traffic-overview', unref(agentId) || 'all']),
    queryFn: () => fetchTrafficOverview(unref(agentId) || null),
    refetchInterval: 30_000
  })
}
```

- [ ] **Step 3: Verify build**

```bash
cd panel/frontend && npm run build
```

Expected: build succeeds

- [ ] **Step 4: Commit**

```bash
git add panel/frontend/src/api/runtime.js panel/frontend/src/hooks/useTrafficOverview.js
git commit -m "feat(panel): add traffic-overview API client and hook"
```

---

### Task 8: Create DashboardTrafficModule and update DashboardPage

**Files:**
- Create: `panel/frontend/src/components/traffic/DashboardTrafficModule.vue`
- Modify: `panel/frontend/src/pages/DashboardPage.vue`

- [ ] **Step 1: Create DashboardTrafficModule.vue**

Create `panel/frontend/src/components/traffic/DashboardTrafficModule.vue`:

```vue
<template>
  <div v-if="visible" class="dashboard-traffic">
    <div class="dashboard-traffic__header">
      <h2 class="dashboard-traffic__title">流量统计</h2>
      <div class="dashboard-traffic__selector">
        <select v-model="selectedAgentId" class="dashboard-traffic__select">
          <option value="">全部节点</option>
          <option v-for="agent in overviewAgents" :key="agent.agent_id" :value="agent.agent_id">{{ agent.name }}</option>
        </select>
      </div>
    </div>
    <div v-if="overviewQuery.isLoading.value" class="dashboard-traffic__loading">
      <div class="spinner"></div>
    </div>
    <template v-else>
      <div class="dashboard-traffic__chart">
        <TrafficTrendChart :points="trendPoints" granularity="day" :quota-bytes="selectedSummary?.quota_bytes ?? null" />
      </div>
      <div class="dashboard-traffic__cards">
        <div class="dashboard-traffic__card">
          <span class="dashboard-traffic__card-label">已用</span>
          <span class="dashboard-traffic__card-value">{{ formatBytes(selectedSummary?.used_bytes ?? 0) }}</span>
        </div>
        <div class="dashboard-traffic__card">
          <span class="dashboard-traffic__card-label">额度</span>
          <span class="dashboard-traffic__card-value">{{ formatQuota(selectedSummary?.quota_bytes ?? null) }}</span>
        </div>
        <div class="dashboard-traffic__card">
          <span class="dashboard-traffic__card-label">剩余</span>
          <span class="dashboard-traffic__card-value">{{ formatBytes(selectedSummary?.remaining_bytes ?? 0) }}</span>
        </div>
      </div>
    </template>
  </div>
</template>

<script setup>
import { ref, computed } from 'vue'
import { useTrafficOverview } from '../../hooks/useTrafficOverview.js'
import { useQuery } from '@tanstack/vue-query'
import { fetchSystemInfo } from '../../api'
import TrafficTrendChart from './TrafficTrendChart.vue'
import { formatBytes, formatQuota } from '../../utils/trafficStats.js'

const { data: systemInfo } = useQuery({
  queryKey: ['system-info'],
  queryFn: fetchSystemInfo
})

const visible = computed(() => systemInfo.value?.traffic_stats_enabled !== false)

const selectedAgentId = ref('')

const overviewQuery = useTrafficOverview(selectedAgentId)

const overviewAgents = computed(() => overviewQuery.data.value?.agents ?? [])

const trendPoints = computed(() => {
  const raw = overviewQuery.data.value?.trend ?? []
  return raw.map(p => ({
    bucket_start: p.bucket_start,
    rx_bytes: Number(p.rx_bytes) || 0,
    tx_bytes: Number(p.tx_bytes) || 0,
    accounted_bytes: Number(p.accounted_bytes) || 0
  }))
})

const selectedSummary = computed(() => {
  const agents = overviewAgents.value
  if (selectedAgentId.value) {
    return agents.find(a => a.agent_id === selectedAgentId.value) ?? null
  }
  if (!agents.length) return null
  return {
    used_bytes: agents.reduce((s, a) => s + (a.used_bytes || 0), 0),
    quota_bytes: agents.every(a => a.quota_bytes == null) ? null : agents.reduce((s, a) => s + (a.quota_bytes || 0), 0),
    remaining_bytes: agents.reduce((s, a) => s + (a.remaining_bytes || 0), 0)
  }
})
</script>

<style scoped>
.dashboard-traffic {
  background: var(--color-bg-surface);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-2xl);
  overflow: hidden;
  margin-bottom: 2.5rem;
}
.dashboard-traffic__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 1rem 1.25rem;
  border-bottom: 1px solid var(--color-border-subtle);
}
.dashboard-traffic__title {
  font-size: 0.875rem;
  font-weight: 600;
  color: var(--color-text-primary);
  margin: 0;
}
.dashboard-traffic__select {
  padding: 0.35rem 0.75rem;
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
  background: var(--color-bg-surface);
  color: var(--color-text-primary);
  font-size: 0.8125rem;
  font-family: inherit;
  cursor: pointer;
}
.dashboard-traffic__loading {
  display: flex;
  justify-content: center;
  padding: 2rem;
}
.dashboard-traffic__chart {
  padding: 1rem 1.25rem 0;
}
.dashboard-traffic__cards {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 0.75rem;
  padding: 1rem 1.25rem 1.25rem;
}
.dashboard-traffic__card {
  padding: 0.5rem 0.75rem;
  background: var(--color-bg-subtle);
  border-radius: var(--radius-md);
}
.dashboard-traffic__card-label {
  display: block;
  color: var(--color-text-tertiary);
  font-size: 0.75rem;
}
.dashboard-traffic__card-value {
  display: block;
  color: var(--color-text-primary);
  font-size: 1rem;
  font-weight: 700;
  font-variant-numeric: tabular-nums;
}
.spinner {
  width: 24px;
  height: 24px;
  border: 2px solid var(--color-border-default);
  border-top-color: var(--color-primary);
  border-radius: 50%;
  animation: spin 1s linear infinite;
}
@keyframes spin { to { transform: rotate(360deg); } }
</style>
```

- [ ] **Step 2: Update DashboardPage.vue**

In `panel/frontend/src/pages/DashboardPage.vue`:
1. Import `DashboardTrafficModule`
2. Add it after the stats grid and before the agents section

Add the import in `<script setup>`:

```js
import DashboardTrafficModule from '../components/traffic/DashboardTrafficModule.vue'
```

Add the module in the template, between `</div>` (closing stats-grid) and `<div v-if="agents?.length"`:

```html
<DashboardTrafficModule />
```

So the template becomes:

```html
    </div> <!-- end stats-grid -->

    <DashboardTrafficModule />

    <div v-if="agents?.length" class="dashboard-section">
```

- [ ] **Step 3: Verify build**

```bash
cd panel/frontend && npm run build
```

Expected: build succeeds

- [ ] **Step 4: Commit**

```bash
git add panel/frontend/src/components/traffic/DashboardTrafficModule.vue panel/frontend/src/pages/DashboardPage.vue
git commit -m "feat(panel): add traffic overview module to dashboard"
```

---

### Task 9: Make rule cards' traffic lines clickable + add trend modals to rule pages

**Files:**
- Modify: `panel/frontend/src/components/rules/RuleCard.vue` — make traffic-line clickable
- Modify: `panel/frontend/src/components/l4/L4RuleItem.vue` — make traffic-line clickable
- Modify: `panel/frontend/src/components/relay/RelayCard.vue` — make traffic-line clickable
- Modify: `panel/frontend/src/pages/RulesPage.vue` — add modal state
- Modify: `panel/frontend/src/pages/L4RulesPage.vue` — add modal state
- Modify: `panel/frontend/src/pages/RelayListenersPage.vue` — add modal state

- [ ] **Step 1: Update RuleCard.vue**

In `panel/frontend/src/components/rules/RuleCard.vue`:

Make the traffic-line emit an event. Change the traffic-line div to:

```html
    <div v-if="hasTraffic" class="traffic-line traffic-line--clickable" @click.stop="$emit('traffic-click', rule)">
```

Add `'traffic-click'` to the `defineEmits` array (find the existing emits and add to it).

Add CSS:

```css
.traffic-line--clickable { cursor: pointer; }
.traffic-line--clickable:hover { color: var(--color-primary); text-decoration: underline; }
```

- [ ] **Step 2: Update L4RuleItem.vue**

Same pattern as RuleCard. In `panel/frontend/src/components/l4/L4RuleItem.vue`:

Change the traffic-line div to:

```html
    <div v-if="hasTraffic" class="traffic-line traffic-line--clickable" @click.stop="$emit('traffic-click', rule)">
```

Add `'traffic-click'` to the `defineEmits`.

Add the same CSS for `.traffic-line--clickable`.

- [ ] **Step 3: Update RelayCard.vue**

Same pattern. In `panel/frontend/src/components/relay/RelayCard.vue`:

Change the traffic-line div to:

```html
    <div v-if="hasTraffic" class="traffic-line traffic-line--clickable" @click.stop="$emit('traffic-click', listener)">
```

Add `'traffic-click'` to the `defineEmits`.

Add the same CSS for `.traffic-line--clickable`.

- [ ] **Step 4: Update RulesPage.vue**

In `panel/frontend/src/pages/RulesPage.vue`:

Add imports:

```js
import { ref } from 'vue'
import TrafficTrendModal from '../components/traffic/TrafficTrendModal.vue'
```

Add state (in `<script setup>`):

```js
const trendModal = ref({ visible: false, agentId: '', scopeId: '', scopeLabel: '' })

function openTrendModal(rule) {
  const agentId = selectedAgentId.value
  if (!agentId) return
  trendModal.value = {
    visible: true,
    agentId,
    scopeType: 'http_rule',
    scopeId: String(rule.id),
    scopeLabel: `HTTP 规则 #${rule.id}`
  }
}
```

Find where `RuleCard` components are rendered and add `@traffic-click="openTrendModal"` event handler.

Add the modal to the template (at the end, before the closing `</template>`):

```html
<TrafficTrendModal
  v-model:visible="trendModal.visible"
  :agent-id="trendModal.agentId"
  :scope-type="trendModal.scopeType"
  :scope-id="trendModal.scopeId"
  :scope-label="trendModal.scopeLabel"
  :direction="trafficDirection"
/>
```

Where `trafficDirection` is extracted from the traffic policy or defaults to `'both'`. If the page already fetches traffic policy, use that direction. If not, default to `'both'`.

Add `trafficDirection` computed:

```js
const trafficDirection = computed(() => trafficPolicyData.value?.direction || 'both')
```

This requires that the page already fetches or has access to traffic policy data.

- [ ] **Step 5: Update L4RulesPage.vue**

Same pattern as RulesPage, but with:

```js
function openTrendModal(rule) {
  const agentId = selectedAgentId.value
  if (!agentId) return
  trendModal.value = {
    visible: true,
    agentId,
    scopeType: 'l4_rule',
    scopeId: String(rule.id),
    scopeLabel: `L4 规则 #${rule.id}`
  }
}
```

- [ ] **Step 6: Update RelayListenersPage.vue**

Same pattern, but with:

```js
function openTrendModal(listener) {
  const agentId = selectedAgentId.value
  if (!agentId) return
  trendModal.value = {
    visible: true,
    agentId,
    scopeType: 'relay_listener',
    scopeId: String(listener.id),
    scopeLabel: `Relay 监听 #${listener.id}`
  }
}
```

Note: If the RelayListenersPage shows listeners across agents, use `listener.agent_id` instead of `selectedAgentId.value`.

- [ ] **Step 7: Verify build**

```bash
cd panel/frontend && npm run build
```

Expected: build succeeds

- [ ] **Step 8: Commit**

```bash
git add panel/frontend/src/components/rules/RuleCard.vue panel/frontend/src/components/l4/L4RuleItem.vue panel/frontend/src/components/relay/RelayCard.vue panel/frontend/src/pages/RulesPage.vue panel/frontend/src/pages/L4RulesPage.vue panel/frontend/src/pages/RelayListenersPage.vue
git commit -m "feat(panel): add clickable traffic trend popups to rule cards"
```

---

### Task 10: Restructure AgentDetailPage traffic tab into collapsible sections

**Files:**
- Modify: `panel/frontend/src/pages/AgentDetailPage.vue`

This is the largest single file change. The existing traffic tab template (~130 lines) is replaced with three collapsible sections using the new components.

- [ ] **Step 1: Add new imports to AgentDetailPage.vue**

In the `<script setup>`, add imports for the new components:

```js
import TrafficTrendChart from '../components/traffic/TrafficTrendChart.vue'
import TrafficTrendModal from '../components/traffic/TrafficTrendModal.vue'
import TrafficSummaryCards from '../components/traffic/TrafficSummaryCards.vue'
import TrafficBreakdownTable from '../components/traffic/TrafficBreakdownTable.vue'
import TrafficPolicyForm from '../components/traffic/TrafficPolicyForm.vue'
import TrafficHistoryManager from '../components/traffic/TrafficHistoryManager.vue'
import TrafficCollapsibleSection from '../components/traffic/TrafficCollapsibleSection.vue'
```

- [ ] **Step 2: Add trend modal state**

```js
const trendModal = ref({ visible: false, scopeType: '', scopeId: '', scopeLabel: '' })

function openBreakdownTrendModal(row) {
  trendModal.value = {
    visible: true,
    scopeType: row.scope_type,
    scopeId: row.scope_id,
    scopeLabel: trafficBreakdownLabel(row)
  }
}
```

- [ ] **Step 3: Replace the traffic tab template**

Replace the entire `<div v-if="activeTab === 'traffic'" class="tab-panel">...</div>` block with:

```html
      <div v-if="activeTab === 'traffic'" class="tab-panel">
        <TrafficCollapsibleSection title="概览" :default-expanded="true">
          <TrafficSummaryCards :summary="trafficSummary" :direction="trafficPolicyForm.direction" />
          <div class="traffic-tab__trend">
            <div class="traffic-tab__trend-header">
              <span>趋势</span>
              <div class="traffic-trend__controls" role="group" aria-label="趋势粒度">
                <button
                  v-for="option in trafficTrendGranularityOptions"
                  :key="option.value"
                  class="traffic-trend__mode"
                  :class="{ 'traffic-trend__mode--active': trafficTrendGranularity === option.value }"
                  type="button"
                  @click="trafficTrendGranularity = option.value"
                >
                  {{ option.label }}
                </button>
              </div>
            </div>
            <TrafficTrendChart :points="trafficTrendPoints" :granularity="trafficTrendGranularity" :quota-bytes="trafficSummary.monthly_quota_bytes ?? null" />
          </div>
          <div class="traffic-tab__breakdown">
            <span class="traffic-tab__breakdown-title">分项流量（点击查看趋势）</span>
            <TrafficBreakdownTable :rows="trafficBreakdownRows" :clickable="true" @click-row="openBreakdownTrendModal" />
          </div>
        </TrafficCollapsibleSection>

        <TrafficCollapsibleSection title="策略设置" :default-expanded="false">
          <TrafficPolicyForm v-model="trafficPolicyForm" :saving="updateTrafficPolicyMutation.isPending.value" @save="saveTrafficPolicy" />
        </TrafficCollapsibleSection>

        <TrafficCollapsibleSection title="历史管理" :default-expanded="false">
          <TrafficHistoryManager
            :calibrating="calibrateTrafficMutation.isPending.value"
            :cleaning="cleanupTrafficMutation.isPending.value"
            @calibrate="calibrateTrafficSummary"
            @calibrate-zero="calibrateTrafficToZero"
            @cleanup="cleanupTrafficHistory"
          />
        </TrafficCollapsibleSection>

        <TrafficTrendModal
          v-model:visible="trendModal.visible"
          :agent-id="agentId"
          :scope-type="trendModal.scopeType"
          :scope-id="trendModal.scopeId"
          :scope-label="trendModal.scopeLabel"
          :direction="trafficPolicyForm.direction"
        />
      </div>
```

- [ ] **Step 4: Remove old traffic-related CSS**

Remove the following CSS classes that are no longer used in the template (they've been moved to sub-components):
- `.traffic-summary__cards`, `.traffic-total`, `.traffic-total__label`, `.traffic-total__value`
- `.traffic-panel`, `.traffic-panel__section`, `.traffic-panel__section-header`, `.traffic-panel__footer`
- `.traffic-trend`, `.traffic-trend__item`, `.traffic-trend__bars`, `.traffic-trend__bar`, `.traffic-trend__bar--accounted`, `.traffic-trend__label`, `.traffic-trend__quota`
- `.traffic-breakdown`, `.traffic-breakdown__row`, `.traffic-breakdown__name`, `.traffic-breakdown__value`, `.traffic-breakdown__raw`
- `.traffic-policy-grid`, `.traffic-setting`, `.traffic-setting--switch`, `.traffic-setting__label`, `.traffic-setting__input`, `.traffic-setting__quota`, `.traffic-setting__unit`

Add new CSS for the restructured tab:

```css
.traffic-tab__trend { margin-bottom: 1rem; }
.traffic-tab__trend-header { display: flex; align-items: center; justify-content: space-between; margin-bottom: 0.75rem; font-size: 0.875rem; font-weight: 600; color: var(--color-text-primary); }
.traffic-tab__breakdown { margin-top: 0.5rem; }
.traffic-tab__breakdown-title { display: block; font-size: 0.8125rem; color: var(--color-text-tertiary); margin-bottom: 0.5rem; }
```

Keep the `.traffic-trend__controls` and `.traffic-trend__mode` CSS (they're still used for the granularity toggle).

- [ ] **Step 5: Verify build**

```bash
cd panel/frontend && npm run build
```

Expected: build succeeds

- [ ] **Step 6: Commit**

```bash
git add panel/frontend/src/pages/AgentDetailPage.vue
git commit -m "feat(panel): restructure node traffic tab into collapsible sections"
```

---

### Task 11: Final verification and cleanup

- [ ] **Step 1: Run backend tests**

```bash
cd panel/backend-go && go test ./...
```

Expected: all tests pass

- [ ] **Step 2: Run frontend build**

```bash
cd panel/frontend && npm run build
```

Expected: build succeeds with no errors

- [ ] **Step 3: Run go-agent tests**

```bash
cd go-agent && go test ./...
```

Expected: all tests pass

- [ ] **Step 4: Visual smoke test checklist**

Start the dev server and verify:

1. Dashboard shows traffic module with trend chart
2. Dashboard node selector switches trend data
3. Rules page: clicking traffic line on a rule card opens trend modal
4. L4 page: clicking traffic line opens trend modal
5. Relay page: clicking traffic line opens trend modal
6. Node detail: traffic tab shows 3 collapsible sections
7. Node detail: overview section has summary cards + chart + breakdown table
8. Node detail: breakdown table rows open trend modal
9. Node detail: policy form saves
10. Node detail: calibrate/cleanup work

- [ ] **Step 5: Final commit if any fixes needed**

```bash
git add -A
git commit -m "fix(panel): address traffic UI redesign issues"
```
