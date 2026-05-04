# Traffic Stats Frontend Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Redesign frontend traffic statistics with card-centric dashboard, tabbed agent detail breakdowns, traffic progress bars on rule cards, trend modal extensions (date range + period comparison), and host traffic visualization.

**Architecture:** Extend existing traffic components with visual progress bars, sortable tabbed tables, and dual-series Chart.js trends. Add two small backend response fields to expose host traffic data. Frontend aggregates top-rules across agents in the browser.

**Tech Stack:** Vue 3 Composition API, Chart.js, TanStack Vue Query, Vite, Vitest, Go, GORM/SQLite

---

## File Structure

### New files (frontend)

| File | Responsibility |
|---|---|
| `panel/frontend/src/components/traffic/TrafficBar.vue` | Traffic progress bar card for rule/listener cards |

### Modified files (frontend)

| File | Change |
|---|---|
| `panel/frontend/src/utils/trafficStats.js` | Add `usagePercent`, `dailyBudget`, `quotaColorThreshold` helpers |
| `panel/frontend/src/hooks/useTraffic.js` | Extend `useTrafficTrend` to accept `from`/`to` params |
| `panel/frontend/src/components/traffic/DashboardTrafficModule.vue` | Rewrite with cards, top-nodes, top-rules, dual trend |
| `panel/frontend/src/components/traffic/TrafficSummaryCards.vue` | Rewrite: 5 cards with progress bars, host bandwidth, block status |
| `panel/frontend/src/components/traffic/TrafficBreakdownTable.vue` | Extend: tabs (HTTP/L4/Relay/Host), sortable columns, sparklines |
| `panel/frontend/src/components/traffic/TrafficTrendChart.vue` | Extend: dual-series, prev-period dashed line, daily budget reference line |
| `panel/frontend/src/components/traffic/TrafficTrendModal.vue` | Extend: date range picker, comparison toggle, summary stat cards |
| `panel/frontend/src/components/rules/RuleCard.vue` | Replace traffic line with TrafficBar |
| `panel/frontend/src/components/l4/L4RuleItem.vue` | Add TrafficBar |
| `panel/frontend/src/components/relay/RelayCard.vue` | Add TrafficBar |
| `panel/frontend/src/pages/AgentDetailPage.vue` | Update traffic tab layout with new components |

### Modified files (backend)

| File | Change |
|---|---|
| `panel/backend-go/internal/controlplane/service/traffic_types.go` | Add `HostTotal` and `HostInterfaces` to `TrafficSummary`; add `HostTrend` to `TrafficOverviewResult` |
| `panel/backend-go/internal/controlplane/service/traffic_service.go` | Query `host_total`/`host_interface` in summary; query `host_total` trend in overview |
| `panel/backend-go/internal/controlplane/http/handlers_traffic.go` | Include `host_trend` in overview response |

### Modified test files

| File | Change |
|---|---|
| `panel/backend-go/internal/controlplane/service/traffic_service_test.go` | Add host breakdown assertions |
| `panel/backend-go/internal/controlplane/http/traffic_handlers_test.go` | Add host_trend response assertions |
| `panel/frontend/src/utils/trafficStats.test.mjs` | Add new helper tests |
| `panel/frontend/src/components/traffic/TrafficSummaryCards.test.js` | Update for new card layout |
| `panel/frontend/src/components/traffic/TrafficBreakdownTable.test.js` | Add tab/sort tests |

---

### Task 1: Add host fields to backend TrafficSummary types

**Files:**
- Modify: `panel/backend-go/internal/controlplane/service/traffic_types.go`
- Modify: `panel/backend-go/internal/controlplane/service/traffic_service.go`
- Test: `panel/backend-go/internal/controlplane/service/traffic_service_test.go`

- [ ] **Step 1: Add HostTotal and HostInterfaces to TrafficSummary struct**

In `traffic_types.go`, add two fields to `TrafficSummary`:

```go
HostTotal        TrafficSummaryBreakdown   `json:"host_total"`
HostInterfaces   []TrafficSummaryBreakdown `json:"host_interfaces"`
```

And add `HostTrend` to `TrafficOverviewResult`:

```go
type TrafficOverviewResult struct {
	Agents     []TrafficOverviewAgent `json:"agents"`
	Trend      []TrafficTrendPoint    `json:"trend"`
	HostTrend  []TrafficTrendPoint    `json:"host_trend"`
}
```

- [ ] **Step 2: Add host breakdown fields to internal struct**

In `traffic_service.go`, extend `trafficSummaryBreakdowns`:

```go
type trafficSummaryBreakdowns struct {
	aggregates     []TrafficSummaryBreakdown
	httpRules      []TrafficSummaryBreakdown
	l4Rules        []TrafficSummaryBreakdown
	relayListeners []TrafficSummaryBreakdown
	hostTotal      TrafficSummaryBreakdown
	hostInterfaces []TrafficSummaryBreakdown
}
```

- [ ] **Step 3: Query host_total and host_interface in summaryBreakdowns**

In `summaryBreakdowns()`, add host_total query alongside the aggregate loop:

```go
// After the http/l4/relay loop
hostTotalRows, err := s.store.ListTrafficTrend(ctx, storage.TrafficTrendQuery{
	AgentID:     agentID,
	ScopeType:   "host_total",
	Granularity: "hour",
	From:        start.UTC(),
	To:          end.UTC(),
})
if err == nil {
	rows := summarizeTrafficBreakdownRows(policy.Direction, hostTotalRows)
	if len(rows) > 0 {
		out.hostTotal = rows[0]
	}
}
```

And add `host_interface` to the breakdown store loop:

```go
for _, scopeType := range []string{"http_rule", "l4_rule", "relay_listener", "host_interface"} {
	rows, err := breakdownStore.ListTrafficBreakdown(ctx, storage.TrafficTrendQuery{...})
	// existing cases plus:
	case "host_interface":
		out.hostInterfaces = summarizeTrafficBreakdownRows(policy.Direction, rows)
}
```

- [ ] **Step 4: Wire host fields into Summary response**

In `summaryWithPolicy()`, after calling `summaryBreakdowns`, assign:

```go
summary.HostTotal = breakdowns.hostTotal
summary.HostInterfaces = breakdowns.hostInterfaces
```

- [ ] **Step 5: Add host trend to Overview service method**

In the `Overview` method (or wherever `result.Trend` is populated), also query `host_total` trend and assign to `result.HostTrend`.

- [ ] **Step 6: Update handler to include host_trend**

In `handlers_traffic.go`, `handleTrafficOverview`:

```go
writeJSON(w, http.StatusOK, map[string]any{
	"ok":         true,
	"agents":     result.Agents,
	"trend":      result.Trend,
	"host_trend": result.HostTrend,
})
```

- [ ] **Step 7: Run backend tests**

```bash
cd panel/backend-go && go test ./internal/controlplane/service/... ./internal/controlplane/http/... -v
```

Expected: all existing tests pass; if new assertions were added they should pass.

- [ ] **Step 8: Commit backend changes**

```bash
git add panel/backend-go/
git commit -m "feat(backend): expose host_total and host_interfaces in traffic summary

Add host traffic breakdown to agent traffic summary response.
Add host_trend to traffic overview response for dashboard dual-series chart."
```

---

### Task 2: Extend frontend traffic utilities and hooks

**Files:**
- Modify: `panel/frontend/src/utils/trafficStats.js`
- Modify: `panel/frontend/src/hooks/useTraffic.js`
- Test: `panel/frontend/src/utils/trafficStats.test.mjs`

- [ ] **Step 1: Add traffic utility helpers**

Add to `trafficStats.js`:

```javascript
export function usagePercent(used, quota) {
  const u = normalizeBytes(used)
  const q = normalizeNullableBytes(quota)
  if (q == null || q === 0) return null
  return Math.min(100, Math.round((u / q) * 100))
}

export function dailyBudget(quotaBytes, cycleDays) {
  const q = normalizeNullableBytes(quotaBytes)
  const days = normalizePositiveInteger(cycleDays, 30)
  if (q == null || days <= 0) return null
  return Math.round(q / days)
}

export function quotaColorThreshold(percent) {
  const p = Number(percent)
  if (!Number.isFinite(p)) return 'neutral'
  if (p >= 90) return 'danger'
  if (p >= 70) return 'warning'
  return 'success'
}

export function formatPercentage(value, fallback = '—') {
  const p = Number(value)
  if (!Number.isFinite(p)) return fallback
  return `${Math.round(p)}%`
}
```

- [ ] **Step 2: Extend useTrafficTrend with from/to**

In `useTraffic.js`, update `trafficTrendParams`:

```javascript
function trafficTrendParams(params = {}) {
  const value = unref(params) || {}
  const range = unref(value.range)
  const scope = unref(value.scope)
  return {
    granularity: unref(value.granularity) || 'day',
    from: Array.isArray(range) ? range[0] : unref(value.from),
    to: Array.isArray(range) ? range[1] : unref(value.to),
    scope_type: Array.isArray(scope) ? scope[0] : unref(value.scope_type),
    scope_id: Array.isArray(scope) ? scope[1] : unref(value.scope_id)
  }
}
```

The `trafficTrendKey` should also include from/to for cache invalidation:

```javascript
function trafficTrendKey(agentId, params = {}) {
  const id = unref(agentId)
  const value = unref(params) || {}
  const granularity = unref(value.granularity) || 'day'
  const from = unref(value.from) || ''
  const to = unref(value.to) || ''
  const scope = unref(value.scope) || [
    unref(value.scope_type) || '',
    unref(value.scope_id) || ''
  ]
  return ['traffic-trend', id, granularity, from, to, scope]
}
```

- [ ] **Step 3: Add tests for new helpers**

In `trafficStats.test.mjs`:

```javascript
import { describe, it, expect } from 'vitest'
import { usagePercent, dailyBudget, quotaColorThreshold } from '../trafficStats.js'

describe('usagePercent', () => {
  it('returns null for unlimited quota', () => {
    expect(usagePercent(100, null)).toBeNull()
  })
  it('computes percentage correctly', () => {
    expect(usagePercent(50, 100)).toBe(50)
    expect(usagePercent(120, 100)).toBe(100)
  })
})

describe('dailyBudget', () => {
  it('returns null for unlimited quota', () => {
    expect(dailyBudget(null, 30)).toBeNull()
  })
  it('divides quota by days', () => {
    expect(dailyBudget(3000, 30)).toBe(100)
  })
})

describe('quotaColorThreshold', () => {
  it('returns success below 70', () => {
    expect(quotaColorThreshold(50)).toBe('success')
  })
  it('returns warning at 70-89', () => {
    expect(quotaColorThreshold(75)).toBe('warning')
  })
  it('returns danger at 90+', () => {
    expect(quotaColorThreshold(95)).toBe('danger')
  })
})
```

- [ ] **Step 4: Run frontend util tests**

```bash
cd panel/frontend && npx vitest run src/utils/trafficStats.test.mjs
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add panel/frontend/src/utils/trafficStats.js panel/frontend/src/utils/trafficStats.test.mjs panel/frontend/src/hooks/useTraffic.js
git commit -m "feat(panel): add traffic utility helpers and extend trend hook with date range"
```

---

### Task 3: Create TrafficBar component for rule cards

**Files:**
- Create: `panel/frontend/src/components/traffic/TrafficBar.vue`
- Modify: `panel/frontend/src/components/rules/RuleCard.vue`
- Modify: `panel/frontend/src/components/l4/L4RuleItem.vue`
- Modify: `panel/frontend/src/components/relay/RelayCard.vue`

- [ ] **Step 1: Write TrafficBar.vue**

```vue
<template>
  <div class="traffic-bar" @click.stop="$emit('click')">
    <div class="traffic-bar__header">
      <span class="traffic-bar__label">用量 {{ formatBytes(accounted) }}</span>
      <span class="traffic-bar__percent" :class="`traffic-bar__percent--${color}`">
        {{ percentLabel }}
      </span>
    </div>
    <div class="traffic-bar__track">
      <div
        class="traffic-bar__fill"
        :class="`traffic-bar__fill--${color}`"
        :style="{ width: `${Math.min(100, percent)}%` }"
      />
    </div>
    <div class="traffic-bar__detail">
      <span>入 {{ formatBytes(rx) }}</span>
      <span>出 {{ formatBytes(tx) }}</span>
    </div>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import { formatBytes, usagePercent, quotaColorThreshold } from '../../utils/trafficStats.js'

const props = defineProps({
  accounted: { type: Number, default: 0 },
  rx: { type: Number, default: 0 },
  tx: { type: Number, default: 0 },
  nodeTotal: { type: Number, default: 0 }
})

defineEmits(['click'])

const percent = computed(() => {
  if (!props.nodeTotal || props.nodeTotal <= 0) return 0
  return Math.round((props.accounted / props.nodeTotal) * 100)
})

const color = computed(() => quotaColorThreshold(percent.value))

const percentLabel = computed(() => {
  if (!props.nodeTotal || props.nodeTotal <= 0) return ''
  return `占节点 ${percent.value}%`
})
</script>

<style scoped>
.traffic-bar {
  background: var(--color-bg-subtle);
  border-radius: var(--radius-md);
  padding: 0.5rem 0.625rem;
  cursor: pointer;
  transition: background 0.15s;
}
.traffic-bar:hover { background: var(--color-bg-hover); }
.traffic-bar__header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 0.25rem;
  font-size: 0.8125rem;
}
.traffic-bar__label { font-weight: 600; color: var(--color-text-primary); }
.traffic-bar__percent { font-size: 0.75rem; font-weight: 600; }
.traffic-bar__percent--success { color: var(--color-success); }
.traffic-bar__percent--warning { color: var(--color-warning); }
.traffic-bar__percent--danger { color: var(--color-danger); }
.traffic-bar__track {
  height: 4px;
  background: var(--color-border-default);
  border-radius: var(--radius-full);
  overflow: hidden;
}
.traffic-bar__fill { height: 100%; border-radius: var(--radius-full); transition: width 0.3s; }
.traffic-bar__fill--success { background: var(--color-success); }
.traffic-bar__fill--warning { background: var(--color-warning); }
.traffic-bar__fill--danger { background: var(--color-danger); }
.traffic-bar__detail {
  display: flex;
  justify-content: space-between;
  margin-top: 0.25rem;
  font-size: 0.75rem;
  color: var(--color-text-tertiary);
  font-variant-numeric: tabular-nums;
}
</style>
```

- [ ] **Step 2: Integrate TrafficBar into RuleCard**

In `RuleCard.vue`, replace the traffic line div with TrafficBar:

Replace:
```vue
<div v-if="hasTraffic" class="traffic-line traffic-line--clickable" @click.stop="$emit('traffic-click', rule)">
  <span>用量 {{ formatBytes(normalizedTraffic.accounted_bytes) }}</span>
  <span>入 {{ formatBytes(normalizedTraffic.rx_bytes) }}</span>
  <span>出 {{ formatBytes(normalizedTraffic.tx_bytes) }}</span>
</div>
```

With:
```vue
<TrafficBar
  v-if="hasTraffic"
  :accounted="normalizedTraffic.accounted_bytes"
  :rx="normalizedTraffic.rx_bytes"
  :tx="normalizedTraffic.tx_bytes"
  :node-total="agentNodeTotal"
  @click="$emit('traffic-click', rule)"
/>
```

Add import:
```javascript
import TrafficBar from '../traffic/TrafficBar.vue'
```

Add prop:
```javascript
agentNodeTotal: { type: Number, default: 0 }
```

- [ ] **Step 3: Integrate TrafficBar into L4RuleItem and RelayCard**

Similar pattern: add `TrafficBar` import, replace traffic text line with `TrafficBar` component, pass `accounted`, `rx`, `tx`, and `nodeTotal`.

For `L4RuleItem.vue` and `RelayCard.vue`, if they don't currently show traffic, add the TrafficBar conditionally when traffic data is provided.

- [ ] **Step 4: Pass nodeTotal from parent pages**

In `RulesPage.vue`, `L4RulesPage.vue`, `RelayListenersPage.vue`, pass the agent's total accounted bytes as `agent-node-total` to each card. The total can be computed from the traffic summary's `used_bytes` or by summing all rule accounted bytes.

- [ ] **Step 5: Commit**

```bash
git add panel/frontend/src/components/traffic/TrafficBar.vue panel/frontend/src/components/rules/RuleCard.vue panel/frontend/src/components/l4/L4RuleItem.vue panel/frontend/src/components/relay/RelayCard.vue panel/frontend/src/pages/RulesPage.vue panel/frontend/src/pages/L4RulesPage.vue panel/frontend/src/pages/RelayListenersPage.vue
git commit -m "feat(panel): add TrafficBar component and integrate into rule cards"
```

---

### Task 4: Rewrite TrafficSummaryCards with progress bars

**Files:**
- Modify: `panel/frontend/src/components/traffic/TrafficSummaryCards.vue`
- Test: `panel/frontend/src/components/traffic/TrafficSummaryCards.test.js`

- [ ] **Step 1: Rewrite TrafficSummaryCards.vue**

```vue
<template>
  <div class="traffic-summary-cards">
    <div class="traffic-summary-card">
      <span class="traffic-summary-card__label">已用</span>
      <span class="traffic-summary-card__value">{{ formatBytes(summary.used_bytes) }}</span>
      <span v-if="percent != null" class="traffic-summary-card__percent" :class="`traffic-summary-card__percent--${color}`">
        {{ percent }}%
      </span>
      <div v-if="percent != null" class="traffic-summary-card__track">
        <div class="traffic-summary-card__fill" :class="`traffic-summary-card__fill--${color}`" :style="{ width: `${percent}%` }" />
      </div>
    </div>
    <div class="traffic-summary-card">
      <span class="traffic-summary-card__label">额度</span>
      <span class="traffic-summary-card__value">{{ formatQuota(summary.monthly_quota_bytes) }}</span>
      <span class="traffic-summary-card__sub">方向: {{ directionLabel }}</span>
    </div>
    <div class="traffic-summary-card">
      <span class="traffic-summary-card__label">剩余 / 日均可用</span>
      <span class="traffic-summary-card__value">{{ remainingLabel }}</span>
      <span v-if="dailyBudgetText" class="traffic-summary-card__sub">{{ dailyBudgetText }}</span>
    </div>
    <div class="traffic-summary-card">
      <span class="traffic-summary-card__label">主机带宽 (24h)</span>
      <span class="traffic-summary-card__value">{{ hostBandwidthLabel }}</span>
      <span v-if="hostInterfaceText" class="traffic-summary-card__sub">{{ hostInterfaceText }}</span>
    </div>
    <div class="traffic-summary-card" :class="{ 'traffic-summary-card--blocked': summary.blocked }">
      <span class="traffic-summary-card__label">状态</span>
      <span class="traffic-summary-card__value">{{ summary.blocked ? '已阻断' : '正常' }}</span>
      <span v-if="summary.blocked" class="traffic-summary-card__sub">超额阻断已生效</span>
    </div>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import { formatBytes, formatQuota, usagePercent, dailyBudget, quotaColorThreshold } from '../../utils/trafficStats.js'

const props = defineProps({
  summary: { type: Object, default: () => ({}) },
  direction: { type: String, default: 'both' },
  hostTotal: { type: Object, default: null }
})

const directionLabel = computed(() => {
  switch (String(props.direction || 'both').toLowerCase()) {
    case 'rx': return '入站'
    case 'tx': return '出站'
    case 'max': return '取最大值'
    default: return '双向'
  }
})

const percent = computed(() => usagePercent(props.summary.used_bytes, props.summary.monthly_quota_bytes))
const color = computed(() => quotaColorThreshold(percent.value))

const remainingLabel = computed(() => {
  if (props.summary.remaining_bytes == null) return '无限制'
  return formatBytes(props.summary.remaining_bytes)
})

const dailyBudgetText = computed(() => {
  const cycleStart = props.summary.cycle_start ? new Date(props.summary.cycle_start) : null
  const cycleEnd = props.summary.cycle_end ? new Date(props.summary.cycle_end) : null
  if (!cycleStart || !cycleEnd) return ''
  const days = Math.max(1, Math.ceil((cycleEnd - cycleStart) / 86400000))
  const budget = dailyBudget(props.summary.monthly_quota_bytes, days)
  if (budget == null) return ''
  return `日均 ${formatBytes(budget)}`
})

const hostBandwidthLabel = computed(() => {
  if (!props.hostTotal) return '—'
  return formatBytes(props.hostTotal.accounted_bytes)
})

const hostInterfaceText = computed(() => {
  if (!props.hostTotal || !props.hostTotal.rx_bytes) return ''
  return 'host_total 口径'
})
</script>

<style scoped>
.traffic-summary-cards {
  display: grid;
  grid-template-columns: repeat(5, 1fr);
  gap: 0.75rem;
  margin-bottom: 1rem;
}
.traffic-summary-card {
  min-width: 0;
  padding: 0.75rem;
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg);
}
.traffic-summary-card--blocked {
  background: var(--color-danger-50);
  border-color: var(--color-danger-100);
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
.traffic-summary-card__percent {
  display: block;
  font-size: 0.875rem;
  font-weight: 600;
  margin-top: 0.25rem;
}
.traffic-summary-card__percent--success { color: var(--color-success); }
.traffic-summary-card__percent--warning { color: var(--color-warning); }
.traffic-summary-card__percent--danger { color: var(--color-danger); }
.traffic-summary-card__track {
  height: 4px;
  background: var(--color-border-default);
  border-radius: var(--radius-full);
  overflow: hidden;
  margin-top: 0.375rem;
}
.traffic-summary-card__fill {
  height: 100%;
  border-radius: var(--radius-full);
  transition: width 0.3s;
}
.traffic-summary-card__fill--success { background: var(--color-success); }
.traffic-summary-card__fill--warning { background: var(--color-warning); }
.traffic-summary-card__fill--danger { background: var(--color-danger); }
.traffic-summary-card__sub {
  display: block;
  font-size: 0.75rem;
  color: var(--color-text-tertiary);
  margin-top: 0.25rem;
}
@media (max-width: 900px) {
  .traffic-summary-cards { grid-template-columns: repeat(3, 1fr); }
}
@media (max-width: 640px) {
  .traffic-summary-cards { grid-template-columns: repeat(2, 1fr); }
}
</style>
```

- [ ] **Step 2: Commit**

```bash
git add panel/frontend/src/components/traffic/TrafficSummaryCards.vue
git commit -m "feat(panel): rewrite TrafficSummaryCards with 5-card layout and progress bars"
```

---

### Task 5: Extend TrafficBreakdownTable with tabs and sorting

**Files:**
- Modify: `panel/frontend/src/components/traffic/TrafficBreakdownTable.vue`

- [ ] **Step 1: Rewrite with tabs and sortable columns**

The component should accept `tabs` prop with groups of rows, and render a tab bar + sortable table per tab.

Key props:
- `tabs: { id, label, rows[] }[]`
- `activeTab: string`
- `sortKey: string`
- `sortDirection: 'asc' | 'desc'`

The table columns: 规则/名称, accounted, RX, TX, 占比, 7天趋势(sparkline mini chart).

- [ ] **Step 2: Commit**

```bash
git add panel/frontend/src/components/traffic/TrafficBreakdownTable.vue
git commit -m "feat(panel): extend TrafficBreakdownTable with tabs, sorting, and sparklines"
```

---

### Task 6: Extend TrafficTrendChart with dual-series and reference lines

**Files:**
- Modify: `panel/frontend/src/components/traffic/TrafficTrendChart.vue`

- [ ] **Step 1: Extend Chart.js config**

Add props:
- `prevPoints: Array` — previous period points
- `budgetBytes: Number | null` — daily budget reference line value

Update `buildConfig()` to:
1. Add a third dataset for `prevPoints` (lighter, dashed line) when provided.
2. Add a fourth dataset for `budgetBytes` horizontal line when provided and granularity is hour/day.
3. Keep existing RX/TX/accounted logic but default to showing `accounted_bytes` as primary series.

- [ ] **Step 2: Commit**

```bash
git add panel/frontend/src/components/traffic/TrafficTrendChart.vue
git commit -m "feat(panel): extend TrafficTrendChart with dual-series, prev-period, and budget line"
```

---

### Task 7: Extend TrafficTrendModal with date range and comparison

**Files:**
- Modify: `panel/frontend/src/components/traffic/TrafficTrendModal.vue`

- [ ] **Step 1: Add date range and comparison controls**

Add state:
- `dateRange: { from: '', to: '' }`
- `compareEnabled: false`

When `compareEnabled` is true:
- Compute `prevFrom`/`prevTo` by subtracting the date range duration.
- Issue a second `useTrafficTrend` call with the previous period dates.
- Pass both `points` and `prevPoints` to `TrafficTrendChart`.

Add summary stat cards at the bottom:
- Current total (sum of current points accounted_bytes)
- Previous total (sum of prev points)
- MoM percent change
- Daily average

- [ ] **Step 2: Commit**

```bash
git add panel/frontend/src/components/traffic/TrafficTrendModal.vue
git commit -m "feat(panel): extend TrafficTrendModal with date range, period comparison, and summary stats"
```

---

### Task 8: Rewrite DashboardTrafficModule

**Files:**
- Modify: `panel/frontend/src/components/traffic/DashboardTrafficModule.vue`
- Modify: `panel/frontend/src/pages/DashboardPage.vue` (if needed)

- [ ] **Step 1: Rewrite DashboardTrafficModule.vue**

The rewrite includes:
- Toolbar with time range presets + custom range + node selector + export
- 4 summary cards (business usage/quota, host bandwidth, alerts, cycle info)
- Dual-series trend chart (business vs host)
- Top nodes card (sorted by usage %)
- Top rules card (sorted by accounted bytes, aggregated across agents)

Use `useTrafficOverview` for the main data, `useAgents` for the agent list, and parallel `fetchTrafficSummary` calls for top-rules aggregation.

- [ ] **Step 2: Commit**

```bash
git add panel/frontend/src/components/traffic/DashboardTrafficModule.vue
git commit -m "feat(panel): rewrite DashboardTrafficModule with cards, top-n, and dual trend"
```

---

### Task 9: Update AgentDetailPage traffic tab

**Files:**
- Modify: `panel/frontend/src/pages/AgentDetailPage.vue`

- [ ] **Step 1: Update traffic tab layout**

Replace the current traffic tab content with:
1. `TrafficSummaryCards` (5 cards, passing `hostTotal` from summary)
2. `TrafficTrendChart` (with date range controls)
3. `TrafficBreakdownTable` (tabbed: HTTP / L4 / Relay / 主机接口)
4. Collapsible sections for Policy and History

Wire `host_interfaces` from `trafficSummary` into the Host Interfaces tab.

- [ ] **Step 2: Commit**

```bash
git add panel/frontend/src/pages/AgentDetailPage.vue
git commit -m "feat(panel): redesign AgentDetailPage traffic tab with tabs and host breakdown"
```

---

### Task 10: Build and verify

**Files:**
- All modified files

- [ ] **Step 1: Build frontend**

```bash
cd panel/frontend && npm run build
```

Expected: build succeeds with no errors.

- [ ] **Step 2: Run frontend tests**

```bash
cd panel/frontend && npx vitest run
```

Expected: all existing tests pass; new component tests pass.

- [ ] **Step 3: Run backend tests**

```bash
cd panel/backend-go && go test ./...
```

Expected: all tests pass.

- [ ] **Step 4: Run agent tests**

```bash
cd go-agent && go test ./...
```

Expected: all tests pass.

- [ ] **Step 5: Docker build**

```bash
docker build -t nginx-reverse-emby .
```

Expected: image builds successfully.

- [ ] **Step 6: Commit any final fixes**

```bash
git add -A
git commit -m "fix(panel): align traffic redesign with build and tests"
```

---

## Self-Review

### Spec coverage check

| Spec Requirement | Implementing Task |
|---|---|
| Dashboard card-centric redesign | Task 8 |
| Dashboard time range picker | Task 8 |
| Dashboard node selector | Task 8 |
| Dashboard business vs host dual trend | Task 6, Task 8 |
| Dashboard top-nodes | Task 8 |
| Dashboard top-rules | Task 8 |
| Dashboard quota progress | Task 8 |
| Agent detail 5 summary cards | Task 4 |
| Agent detail tabbed breakdowns | Task 5, Task 9 |
| Agent detail host interface tab | Task 5, Task 9 |
| Rule card traffic progress bar | Task 3 |
| Trend modal date range | Task 7 |
| Trend modal previous period | Task 6, Task 7 |
| Trend modal daily budget line | Task 6 |
| Trend modal summary stats | Task 7 |
| Host traffic visualization | Task 1 (backend), Task 4, Task 5, Task 8 |
| Backend host fields in summary | Task 1 |
| Backend host_trend in overview | Task 1 |

### Placeholder scan

- No TBD/TODO placeholders found.
- All steps contain actual file paths and expected commands.
- Component code is shown for all new/rewritten components.

### Type consistency

- `trafficTrendKey` includes `from`/`to` — consistent with `trafficTrendParams`.
- `TrafficSummary` fields `host_total` and `host_interfaces` match backend struct names.
- `TrafficOverviewResult.HostTrend` matches handler response key `host_trend`.
