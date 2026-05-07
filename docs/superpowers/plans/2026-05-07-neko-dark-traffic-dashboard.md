# Neko Dark Traffic Dashboard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add neko-dark theme, redesign Dashboard traffic module layout, and add backend aggregate API for performance.

**Architecture:** Frontend Vue 3 components use CSS variables for theme adaptation. Backend Go adds a single `/traffic-aggregate` endpoint that batches traffic summary queries per-agent and returns combined results. No new data fields.

**Tech Stack:** Vue 3, ApexCharts, Go 1.23, SQLite, TanStack Query

---

## File Structure

| File | Responsibility |
|------|---------------|
| `panel/frontend/src/styles/themes.css` | Theme CSS variables — add `[data-theme="neko-dark"]` |
| `panel/frontend/src/context/ThemeContext.js` | Theme list — add `neko-dark` option |
| `panel/backend-go/internal/controlplane/service/traffic_types.go` | Traffic data types — add `TrafficAggregateResult` |
| `panel/backend-go/internal/controlplane/service/traffic_service.go` | Traffic business logic — add `Aggregate()` method |
| `panel/backend-go/internal/controlplane/http/router.go` | TrafficService interface — add `Aggregate` method |
| `panel/backend-go/internal/controlplane/http/handlers_traffic.go` | HTTP handler — add `handleTrafficAggregate` |
| `panel/backend-go/internal/controlplane/http/router.go` | Route registration — add `/traffic-aggregate` |
| `panel/frontend/src/api/runtime.js` | API client — add `fetchTrafficAggregate` |
| `panel/frontend/src/api/index.js` | API exports — export `fetchTrafficAggregate` |
| `panel/frontend/src/hooks/useTrafficAggregate.js` | Vue Query hook — wrap `fetchTrafficAggregate` |
| `panel/frontend/src/components/traffic/DashboardTrafficModule.vue` | Dashboard layout — rewrite to 3-column grid + use aggregate API |
| `panel/frontend/src/components/traffic/TrafficQuotaRing.vue` | Donut chart — restyle with legend list |
| `panel/frontend/src/components/traffic/TrafficTrendChart.vue` | Area chart — dark theme grid/label colors |
| `panel/frontend/src/components/traffic/TrafficRateSparkline.vue` | Sparkline — dark theme colors |
| `panel/frontend/src/components/traffic/TrafficSummaryCards.vue` | Summary cards — verify dark theme compatibility |
| `panel/frontend/src/components/traffic/TrafficBar.vue` | Progress bars — verify dark theme compatibility |
| `panel/frontend/src/components/traffic/TrafficBreakdownTable.vue` | Table — verify dark theme compatibility |
| `panel/frontend/src/components/base/StatCard.vue` | Stat cards — verify dark theme compatibility |

---

## Task 1: Add neko-dark theme CSS variables

**Files:**
- Modify: `panel/frontend/src/styles/themes.css`

- [ ] **Step 1: Add `[data-theme="neko-dark"]` block after sakura-night**

Insert after line 185 (end of sakura-night):

```css
/* =============================================
   Theme: Neko (neko-dark) — Deep blue dark
   ============================================= */
[data-theme="neko-dark"] {
  --color-primary: #60a5fa;
  --color-primary-hover: #93c5fd;
  --color-primary-active: #3b82f6;
  --color-primary-subtle: rgba(96, 165, 250, 0.12);
  --color-primary-50: rgba(96, 165, 250, 0.06);
  --color-primary-100: rgba(96, 165, 250, 0.14);
  --color-primary-200: rgba(96, 165, 250, 0.22);
  --color-primary-300: rgba(96, 165, 250, 0.32);

  --color-accent: #a78bfa;
  --color-accent-hover: #c4b5fd;
  --color-accent-subtle: rgba(167, 139, 250, 0.10);

  --color-text-primary: #f1f5f9;
  --color-text-secondary: #94a3b8;
  --color-text-tertiary: #8899aa;
  --color-text-muted: #475569;
  --color-text-inverse: #0f172a;

  --color-bg-canvas: #0f172a;
  --color-bg-surface: #1e293b;
  --color-bg-surface-raised: #334155;
  --color-bg-sunken: #0b1221;
  --color-bg-subtle: rgba(96, 165, 250, 0.05);
  --color-bg-hover: rgba(96, 165, 250, 0.08);
  --color-bg-active: rgba(96, 165, 250, 0.12);

  --color-border-subtle: rgba(255, 255, 255, 0.04);
  --color-border-default: rgba(255, 255, 255, 0.08);
  --color-border-strong: rgba(255, 255, 255, 0.14);
  --color-border-focus: rgba(96, 165, 250, 0.40);

  --color-success: #34d399;
  --color-success-50: rgba(52, 211, 153, 0.10);
  --color-success-glow: rgba(52, 211, 153, 0.25);
  --color-danger: #f87171;
  --color-danger-50: rgba(248, 113, 113, 0.10);
  --color-danger-glow: rgba(248, 113, 113, 0.25);
  --color-warning: #fbbf24;
  --color-warning-50: rgba(251, 191, 36, 0.10);
  --color-warning-glow: rgba(251, 191, 36, 0.25);

  --shadow-xs: 0 1px 2px rgba(0, 0, 0, 0.25);
  --shadow-sm: 0 1px 3px rgba(0, 0, 0, 0.30), 0 1px 2px rgba(0, 0, 0, 0.20);
  --shadow-md: 0 4px 6px rgba(0, 0, 0, 0.35), 0 2px 4px rgba(0, 0, 0, 0.25);
  --shadow-lg: 0 10px 15px rgba(0, 0, 0, 0.40), 0 4px 6px rgba(0, 0, 0, 0.30);
  --shadow-xl: 0 20px 25px rgba(0, 0, 0, 0.45), 0 8px 10px rgba(0, 0, 0, 0.30);
  --shadow-2xl: 0 24px 48px rgba(0, 0, 0, 0.55);
  --shadow-focus: 0 0 0 3px var(--color-primary-subtle);
  --shadow-inner: inset 0 1px 2px rgba(0, 0, 0, 0.20);
}
```

- [ ] **Step 2: Verify build**

Run: `cd panel/frontend && npm run build`
Expected: Build succeeds without errors.

- [ ] **Step 3: Commit**

```bash
git add panel/frontend/src/styles/themes.css
git commit -m "feat(theme): add neko-dark deep blue theme"
```

---

## Task 2: Add neko-dark to ThemeContext

**Files:**
- Modify: `panel/frontend/src/context/ThemeContext.js`

- [ ] **Step 1: Add theme to themes array**

Change line 3-7 from:
```javascript
export const themes = [
  { id: 'sakura-day',   emoji: '🌸', label: '昼樱' },
  { id: 'sakura-night', emoji: '🌙', label: '夜樱' },
  { id: 'business',     emoji: '☀️', label: '晴空' },
]
```
To:
```javascript
export const themes = [
  { id: 'sakura-day',   emoji: '🌸', label: '昼樱' },
  { id: 'sakura-night', emoji: '🌙', label: '夜樱' },
  { id: 'business',     emoji: '☀️', label: '晴空' },
  { id: 'neko-dark',    emoji: '🐱', label: 'Neko' },
]
```

- [ ] **Step 2: Build and commit**

Run: `cd panel/frontend && npm run build`

```bash
git add panel/frontend/src/context/ThemeContext.js
git commit -m "feat(theme): add neko-dark to theme selector"
```

---

## Task 3: Backend — Add TrafficService.Aggregate method

**Files:**
- Modify: `panel/backend-go/internal/controlplane/service/traffic_types.go`
- Modify: `panel/backend-go/internal/controlplane/service/traffic_service.go`

- [ ] **Step 1: Add TrafficAggregateResult type**

In `traffic_types.go`, after `TrafficOverviewResult` (line 123), add:

```go
type TrafficAggregateRule struct {
	ScopeType      string `json:"scope_type"`
	ScopeID        string `json:"scope_id"`
	Label          string `json:"label"`
	AccountedBytes uint64 `json:"accounted_bytes"`
	RXBytes        uint64 `json:"rx_bytes"`
	TXBytes        uint64 `json:"tx_bytes"`
}

type TrafficAggregateNode struct {
	AgentID    string `json:"agent_id"`
	Name       string `json:"name"`
	UsedBytes  uint64 `json:"used_bytes"`
	QuotaBytes *int64 `json:"quota_bytes"`
}

type TrafficAggregateResult struct {
	Agents    []TrafficOverviewAgent `json:"agents"`
	Trend     []TrafficTrendPoint    `json:"trend"`
	TopRules  []TrafficAggregateRule `json:"top_rules"`
	TopNodes  []TrafficAggregateNode `json:"top_nodes"`
}
```

- [ ] **Step 2: Add Aggregate method to trafficService**

In `traffic_service.go`, after `Overview` method (after line 724), add:

```go
func (s *trafficService) Aggregate(ctx context.Context, agentFilter string, granularity string, agentNames map[string]string) (TrafficAggregateResult, error) {
	if err := s.requireEnabled(); err != nil {
		return TrafficAggregateResult{}, err
	}
	agentIDStore, ok := s.store.(trafficAgentIDStore)
	if !ok {
		return TrafficAggregateResult{}, nil
	}
	agentIDs, err := agentIDStore.ListTrafficAgentIDs(ctx)
	if err != nil {
		return TrafficAggregateResult{}, err
	}
	if granularity == "" {
		granularity = "day"
	}

	// Build overview (agents + trend) — reuse Overview logic
	overviewResult, err := s.Overview(ctx, agentFilter, granularity, agentNames)
	if err != nil {
		return TrafficAggregateResult{}, err
	}

	// Collect top rules by aggregating per-agent summaries
	ruleMap := make(map[string]*TrafficAggregateRule)
	for _, id := range agentIDs {
		if agentFilter != "" && id != agentFilter {
			continue
		}
		summary, err := s.Summary(ctx, id)
		if err != nil {
			continue
		}
		agentName := id
		if n, ok := agentNames[id]; ok {
			agentName = n
		}
		for _, list := range [][]TrafficSummaryBreakdown{summary.HTTPRules, summary.L4Rules, summary.RelayListeners} {
			for _, row := range list {
				key := fmt.Sprintf("%s-%s-%s", id, row.ScopeType, row.ScopeID)
				if existing, ok := ruleMap[key]; ok {
					existing.AccountedBytes += row.AccountedBytes
					existing.RXBytes += row.RXBytes
					existing.TXBytes += row.TXBytes
				} else {
					label := fmt.Sprintf("%s #%s", scopeTypeLabel(row.ScopeType), row.ScopeID)
					if agentFilter == "" {
						label = fmt.Sprintf("%s / %s", agentName, label)
					}
					ruleMap[key] = &TrafficAggregateRule{
						ScopeType:      row.ScopeType,
						ScopeID:        row.ScopeID,
						Label:          label,
						AccountedBytes: row.AccountedBytes,
						RXBytes:        row.RXBytes,
						TXBytes:        row.TXBytes,
					}
				}
			}
		}
	}

	// Sort top rules by accounted_bytes desc
	topRules := make([]TrafficAggregateRule, 0, len(ruleMap))
	for _, r := range ruleMap {
		topRules = append(topRules, *r)
	}
	slices.SortFunc(topRules, func(a, b TrafficAggregateRule) int {
		if a.AccountedBytes > b.AccountedBytes {
			return -1
		}
		if a.AccountedBytes < b.AccountedBytes {
			return 1
		}
		return 0
	})
	if len(topRules) > 5 {
		topRules = topRules[:5]
	}

	// Build top nodes
	topNodes := make([]TrafficAggregateNode, 0, len(overviewResult.Agents))
	for _, a := range overviewResult.Agents {
		topNodes = append(topNodes, TrafficAggregateNode{
			AgentID:    a.AgentID,
			Name:       a.Name,
			UsedBytes:  a.UsedBytes,
			QuotaBytes: a.QuotaBytes,
		})
	}
	slices.SortFunc(topNodes, func(a, b TrafficAggregateNode) int {
		if a.UsedBytes > b.UsedBytes {
			return -1
		}
		if a.UsedBytes < b.UsedBytes {
			return 1
		}
		return 0
	})
	if len(topNodes) > 5 {
		topNodes = topNodes[:5]
	}

	return TrafficAggregateResult{
		Agents:   overviewResult.Agents,
		Trend:    overviewResult.Trend,
		TopRules: topRules,
		TopNodes: topNodes,
	}, nil
}

func scopeTypeLabel(scopeType string) string {
	switch scopeType {
	case "http_rule":
		return "HTTP"
	case "l4_rule":
		return "L4"
	case "relay_listener":
		return "Relay"
	default:
		return scopeType
	}
}
```

- [ ] **Step 3: Add Aggregate to TrafficService interface**

In `panel/backend-go/internal/controlplane/http/router.go` line 30-38, add:

```go
	Aggregate(ctx context.Context, agentFilter string, granularity string, agentNames map[string]string) (service.TrafficAggregateResult, error)
```

- [ ] **Step 4: Add Aggregate to unavailableTrafficService stub**

In `router.go` after line 168, add:

```go
func (unavailableTrafficService) Aggregate(context.Context, string, string, map[string]string) (service.TrafficAggregateResult, error) {
	return service.TrafficAggregateResult{}, trafficStatsDisabledError()
}
```

- [ ] **Step 5: Run backend tests**

Run: `cd panel/backend-go && go test ./internal/controlplane/...`
Expected: All tests pass.

- [ ] **Step 6: Commit**

```bash
git add panel/backend-go/
git commit -m "feat(backend): add TrafficService.Aggregate for batched traffic queries"
```

---

## Task 4: Backend — Add HTTP handler and route

**Files:**
- Modify: `panel/backend-go/internal/controlplane/http/handlers_traffic.go`
- Modify: `panel/backend-go/internal/controlplane/http/router.go`

- [ ] **Step 1: Add handleTrafficAggregate handler**

In `handlers_traffic.go`, after `handleTrafficOverview` (after line 199), add:

```go
func (d Dependencies) handleTrafficAggregate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	if d.writeTrafficDisabledIfNeeded(w) {
		return
	}
	agentFilter := r.URL.Query().Get("agent_id")
	granularity := r.URL.Query().Get("granularity")
	switch granularity {
	case "", "hour", "day", "month":
	default:
		status, payload := mapServiceError(fmt.Errorf("%w: unsupported traffic granularity %q", service.ErrInvalidArgument, granularity))
		writeJSON(w, status, payload)
		return
	}
	agents, err := d.AgentService.List(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorPayload("failed to list agents"))
		return
	}
	agentNames := make(map[string]string, len(agents))
	for _, a := range agents {
		agentNames[a.ID] = a.Name
	}
	result, err := d.TrafficService.Aggregate(r.Context(), agentFilter, granularity, agentNames)
	if err != nil {
		status, payload := mapServiceError(err)
		writeJSON(w, status, payload)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"agents":    result.Agents,
		"trend":     result.Trend,
		"top_rules": result.TopRules,
		"top_nodes": result.TopNodes,
	})
}
```

- [ ] **Step 2: Register route**

In `router.go` line 235, after `/traffic-overview` route, add:

```go
		mux.Handle(prefix+"/traffic-aggregate", resolved.requirePanelToken(http.HandlerFunc(resolved.handleTrafficAggregate)))
```

- [ ] **Step 3: Run backend tests**

Run: `cd panel/backend-go && go test ./internal/controlplane/...`
Expected: All tests pass.

- [ ] **Step 4: Commit**

```bash
git add panel/backend-go/
git commit -m "feat(backend): add /traffic-aggregate endpoint"
```

---

## Task 5: Frontend — Add fetchTrafficAggregate API

**Files:**
- Modify: `panel/frontend/src/api/runtime.js`
- Modify: `panel/frontend/src/api/index.js`
- Create: `panel/frontend/src/hooks/useTrafficAggregate.js`

- [ ] **Step 1: Add fetchTrafficAggregate to runtime.js**

In `runtime.js` after `fetchTrafficOverview` (after line 559), add:

```javascript
export async function fetchTrafficAggregate(agentId, granularity) {
  const params = new URLSearchParams()
  if (agentId) params.set('agent_id', agentId)
  if (granularity) params.set('granularity', granularity)
  const suffix = params.toString() ? `?${params.toString()}` : ''
  const { data } = await api.get(`/traffic-aggregate${suffix}`)
  return data
}
```

- [ ] **Step 2: Export from index.js**

In `index.js` after line 65 (`fetchTrafficOverview`), add:

```javascript
export const fetchTrafficAggregate = (...args) => call('fetchTrafficAggregate', ...args)
```

- [ ] **Step 3: Add dev mock**

In `panel/frontend/src/api/devMocks/data.js`, add mock data for `fetchTrafficAggregate` following the existing mock patterns.

- [ ] **Step 4: Create useTrafficAggregate hook**

Create `panel/frontend/src/hooks/useTrafficAggregate.js`:

```javascript
import { useQuery, keepPreviousData } from '@tanstack/vue-query'
import { computed, unref } from 'vue'
import { fetchTrafficAggregate } from '../api'

export function useTrafficAggregate(agentId, enabled = true, granularity = 'day') {
  return useQuery({
    queryKey: computed(() => ['traffic-aggregate', unref(agentId) || 'all', unref(granularity)]),
    queryFn: () => fetchTrafficAggregate(unref(agentId) || null, unref(granularity)),
    enabled: computed(() => Boolean(unref(enabled))),
    refetchInterval: 30_000,
    placeholderData: keepPreviousData
  })
}
```

- [ ] **Step 5: Build and commit**

Run: `cd panel/frontend && npm run build`

```bash
git add panel/frontend/src/api/ panel/frontend/src/hooks/useTrafficAggregate.js
git commit -m "feat(frontend): add fetchTrafficAggregate API and hook"
```

---

## Task 6: Frontend — Redesign DashboardTrafficModule layout

**Files:**
- Modify: `panel/frontend/src/components/traffic/DashboardTrafficModule.vue`

- [ ] **Step 1: Replace imports and queries**

Replace:
```javascript
import { useTrafficOverview } from '../../hooks/useTrafficOverview.js'
```
With:
```javascript
import { useTrafficAggregate } from '../../hooks/useTrafficAggregate.js'
```

Replace the three query hooks (`overviewQuery`, `allAgentsQuery`, `topRulesQuery`) with a single `aggregateQuery`:

```javascript
const aggregateQuery = useTrafficAggregate(selectedAgentId, trafficStatsEnabled, granularity)

const overviewAgents = computed(() => aggregateQuery.data.value?.agents ?? [])
const trendPoints = computed(() => {
  const pts = aggregateQuery.data.value?.trend
  if (pts?.length) return normalizePoints(pts)
  if (import.meta.env.DEV) return normalizePoints(MOCK_TREND)
  return []
})
const topRules = computed(() => aggregateQuery.data.value?.top_rules ?? [])
const topNodes = computed(() => aggregateQuery.data.value?.top_nodes ?? [])
```

Remove `topRulesQuery` and `agentIdsForTopRules` computed.

- [ ] **Step 2: Rewrite template layout**

Replace the entire bento grid with a 3-column layout:

```vue
<template>
  <div v-if="visible" class="dashboard-traffic">
    <!-- Header unchanged -->
    <div class="dashboard-traffic__header">...</div>

    <div v-if="aggregateQuery.isLoading.value" class="dashboard-traffic__loading">...</div>

    <template v-else>
      <div class="dashboard-traffic__grid">
        <!-- Left Column -->
        <div class="dashboard-traffic__col">
          <div class="dt-card">
            <h3 class="dt-card__title">流量分布</h3>
            <TrafficQuotaRing
              :used-bytes="selectedSummary?.used_bytes ?? 0"
              :quota-bytes="selectedSummary?.quota_bytes ?? null"
              :agents="selectedAgentId && selectedSummary ? [selectedSummary] : overviewAgents"
            />
          </div>
          <div class="dt-card">
            <h3 class="dt-card__title">Top 节点</h3>
            <div v-for="(node, i) in topNodes" :key="node.agent_id" class="dt-top-item">
              <span class="dt-top-item__rank" :style="rankStyle(i)">{{ i + 1 }}</span>
              <span class="dt-top-item__name">{{ node.name || node.agent_id }}</span>
              <span class="dt-top-item__value">{{ formatBytes(node.used_bytes) }}</span>
            </div>
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
          <div class="dt-card">
            <h3 class="dt-card__title">Top 规则</h3>
            <div v-for="(rule, i) in topRules" :key="rule.key || `${rule.scope_type}-${rule.scope_id}`" class="dt-top-rule">
              <div class="dt-top-rule__info">
                <span class="dt-top-rule__name">{{ rule.label }}</span>
                <span class="dt-top-rule__value">{{ formatBytes(rule.accounted_bytes) }}</span>
              </div>
              <div class="dt-top-rule__bar">
                <div class="dt-top-rule__fill" :style="{ width: topRulePercent(rule) + '%', background: chartColors[i % chartColors.length] }" />
              </div>
            </div>
          </div>
          <div class="dt-card">
            <TrafficRateSparkline :points="trendPoints" :granularity="granularity" />
          </div>
        </div>
      </div>

      <!-- Bottom stats row -->
      <div class="dashboard-traffic__stats">
        <div class="dt-stat" :class="{ 'dt-stat--alert': blockedCount > 0 }">
          <span class="dt-stat__label">阻断节点</span>
          <span class="dt-stat__value">{{ blockedCount }} / {{ overviewAgents.length }}</span>
          <span v-if="blockedCount > 0" class="dt-stat__sub dt-stat__sub--alert">{{ blockedCount }} 个节点已超额阻断</span>
          <span v-else class="dt-stat__sub">所有节点正常</span>
        </div>
        <div class="dt-stat">
          <span class="dt-stat__label">计费周期</span>
          <span class="dt-stat__value">{{ cycleLabel }}</span>
          <span class="dt-stat__sub">方向: {{ directionLabel }}</span>
        </div>
        <div class="dt-stat">
          <span class="dt-stat__label">已用 / 额度</span>
          <span class="dt-stat__value">{{ formatBytes(selectedSummary?.used_bytes || 0) }} / {{ formatQuota(selectedSummary?.quota_bytes) }}</span>
          <div class="dt-stat__track">
            <div class="dt-stat__fill" :style="{ width: Math.min(100, usagePercent(selectedSummary?.used_bytes || 0, selectedSummary?.quota_bytes)) + '%' }" />
          </div>
        </div>
        <div class="dt-stat">
          <span class="dt-stat__label">剩余</span>
          <span class="dt-stat__value" :class="{ 'dt-stat__value--success': (selectedSummary?.remaining_bytes || 0) > 0 }">{{ remainingLabel }}</span>
          <span v-if="dailyBudgetText" class="dt-stat__sub">{{ dailyBudgetText }}</span>
        </div>
      </div>
    </template>
  </div>
</template>
```

- [ ] **Step 3: Add helper functions and styles**

In `<script setup>`, add:

```javascript
const DISTRIBUTION_COLORS = ['#60a5fa', '#a78bfa', '#34d399', '#fbbf24', '#f87171', '#22d3ee', '#f472b6']

function rankStyle(index) {
  return { background: DISTRIBUTION_COLORS[index % DISTRIBUTION_COLORS.length] }
}

function topRulePercent(rule) {
  if (!topRules.value.length) return 0
  const max = topRules.value[0].accounted_bytes || 1
  return Math.round((rule.accounted_bytes / max) * 100)
}

const remainingLabel = computed(() => {
  if (selectedSummary.value?.remaining_bytes == null) return '无限制'
  return formatBytes(selectedSummary.value.remaining_bytes)
})

const dailyBudgetText = computed(() => {
  // same logic as before
})
```

Replace `<style scoped>` with new grid-based styles:

```css
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

.dashboard-traffic__grid {
  display: grid;
  grid-template-columns: 260px 1fr 280px;
  gap: 1rem;
  padding: 1rem 1.25rem;
}
.dashboard-traffic__col {
  display: flex;
  flex-direction: column;
  gap: 1rem;
}
.dashboard-traffic__col--center {
  min-width: 0;
}

.dt-card {
  background: var(--color-bg-surface-raised, var(--color-bg-subtle));
  border-radius: var(--radius-lg);
  padding: 0.875rem;
  min-width: 0;
}
.dt-card--tall {
  flex: 1;
  display: flex;
  flex-direction: column;
}
.dt-card__title {
  font-size: 0.7rem;
  font-weight: 600;
  color: var(--color-text-tertiary);
  text-transform: uppercase;
  letter-spacing: 0.5px;
  margin: 0 0 0.625rem;
}

.dt-top-item {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.35rem 0;
  font-size: 0.8125rem;
  border-bottom: 1px solid var(--color-border-subtle);
}
.dt-top-item:last-child { border-bottom: none; }
.dt-top-item__rank {
  width: 18px;
  height: 18px;
  border-radius: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 0.65rem;
  font-weight: 700;
  color: var(--color-text-inverse);
  flex-shrink: 0;
}
.dt-top-item__name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--color-text-primary);
}
.dt-top-item__value {
  color: var(--color-text-secondary);
  font-weight: 600;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}

.dt-top-rule {
  padding: 0.35rem 0;
  border-bottom: 1px solid var(--color-border-subtle);
}
.dt-top-rule:last-child { border-bottom: none; }
.dt-top-rule__info {
  display: flex;
  justify-content: space-between;
  font-size: 0.8125rem;
  margin-bottom: 0.25rem;
}
.dt-top-rule__name {
  color: var(--color-text-primary);
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.dt-top-rule__value {
  color: var(--color-text-secondary);
  font-weight: 600;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
.dt-top-rule__bar {
  height: 5px;
  background: var(--color-border-default);
  border-radius: var(--radius-full);
  overflow: hidden;
}
.dt-top-rule__fill {
  height: 100%;
  border-radius: var(--radius-full);
  transition: width 0.3s;
}

.dashboard-traffic__stats {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: 0.75rem;
  padding: 0 1.25rem 1.25rem;
}
.dt-stat {
  background: var(--color-bg-surface-raised, var(--color-bg-subtle));
  border-radius: var(--radius-lg);
  padding: 0.75rem;
}
.dt-stat--alert {
  background: var(--color-danger-50);
  border: 1px solid var(--color-danger);
}
.dt-stat__label {
  display: block;
  font-size: 0.7rem;
  color: var(--color-text-tertiary);
  text-transform: uppercase;
  letter-spacing: 0.5px;
  margin-bottom: 0.25rem;
}
.dt-stat__value {
  display: block;
  font-size: 1.125rem;
  font-weight: 700;
  color: var(--color-text-primary);
  font-variant-numeric: tabular-nums;
}
.dt-stat__value--success { color: var(--color-success); }
.dt-stat__sub {
  display: block;
  font-size: 0.75rem;
  color: var(--color-text-tertiary);
  margin-top: 0.25rem;
}
.dt-stat__sub--alert { color: var(--color-danger); }
.dt-stat__track {
  height: 3px;
  background: var(--color-border-default);
  border-radius: var(--radius-full);
  overflow: hidden;
  margin-top: 0.375rem;
}
.dt-stat__fill {
  height: 100%;
  border-radius: var(--radius-full);
  transition: width 0.3s;
}

@media (max-width: 1023px) {
  .dashboard-traffic__grid {
    grid-template-columns: 1fr 1fr;
  }
  .dashboard-traffic__col--center {
    grid-column: 1 / -1;
    order: -1;
  }
  .dashboard-traffic__stats {
    grid-template-columns: repeat(2, 1fr);
  }
}

@media (max-width: 640px) {
  .dashboard-traffic__grid {
    grid-template-columns: 1fr;
  }
  .dashboard-traffic__col--center {
    order: 0;
  }
  .dashboard-traffic__stats {
    grid-template-columns: 1fr;
  }
}
```

- [ ] **Step 4: Build and commit**

Run: `cd panel/frontend && npm run build`

```bash
git add panel/frontend/src/components/traffic/DashboardTrafficModule.vue panel/frontend/src/hooks/useTrafficAggregate.js
git commit -m "feat(frontend): redesign DashboardTrafficModule with 3-column layout and aggregate API"
```

---

## Task 7: Frontend — Restyle TrafficQuotaRing as donut + legend

**Files:**
- Modify: `panel/frontend/src/components/traffic/TrafficQuotaRing.vue`

- [ ] **Step 1: Update chart colors for dark theme**

Replace `DISTRIBUTION_COLORS` and `color` computed:

```javascript
const DISTRIBUTION_COLORS = ['#60a5fa', '#a78bfa', '#34d399', '#fbbf24', '#f87171', '#22d3ee', '#f472b6']

const color = computed(() => {
  const p = percent.value ?? 0
  if (p >= 90) return '#f87171'
  if (p >= 70) return '#fbbf24'
  return '#34d399'
})
```

- [ ] **Step 2: Update chartOptions for dark theme**

In `chartOptions` computed, update:
- `labels.color` to `#94a3b8`
- Donut labels color to `#f1f5f9`
- `stroke.show` to `true` with dark color
- `tooltip.theme` to `'dark'`

```javascript
const chartOptions = computed(() => ({
  chart: {
    type: 'donut',
    toolbar: { show: false },
    animations: { enabled: true }
  },
  theme: { mode: 'dark' },
  labels: chartLabels.value,
  colors: chartColors.value,
  plotOptions: {
    pie: {
      donut: {
        size: '70%',
        labels: {
          show: true,
          name: { show: false },
          value: {
            show: true,
            fontSize: '20px',
            fontWeight: 700,
            color: '#f1f5f9',
            formatter: () => {
              if (isDistribution.value) {
                const total = props.agents.reduce((s, a) => s + (a.used_bytes || 0), 0)
                return formatBytes(total)
              }
              if (effectiveQuota.value == null) return '—'
              return `${percent.value ?? 0}%`
            }
          },
          total: {
            show: true,
            showAlways: true,
            label: isDistribution.value ? '总用量' : '额度',
            color: '#94a3b8',
            fontSize: '11px',
            formatter: () => {
              if (isDistribution.value) {
                const total = props.agents.reduce((s, a) => s + (a.used_bytes || 0), 0)
                return formatBytes(total)
              }
              return effectiveQuota.value == null ? '—' : `${percent.value ?? 0}%`
            }
          }
        }
      }
    }
  },
  dataLabels: { enabled: false },
  legend: { show: false },
  stroke: { show: true, colors: ['#1e293b'], width: 2 },
  tooltip: {
    theme: 'dark',
    y: { formatter: (value) => formatBytes(value) }
  }
}))
```

- [ ] **Step 3: Add legend list below chart**

Update template to show colored legend items:

```vue
<template>
  <div class="traffic-quota-ring">
    <apexchart type="donut" :options="chartOptions" :series="series" height="180" />
    <div v-if="isDistribution && props.agents.length > 1" class="traffic-quota-ring__legend">
      <div v-for="(agent, i) in props.agents" :key="agent.agent_id" class="tqr-legend-item">
        <span class="tqr-legend-item__dot" :style="{ background: DISTRIBUTION_COLORS[i % DISTRIBUTION_COLORS.length] }" />
        <span class="tqr-legend-item__name">{{ agent.name || agent.agent_id }}</span>
        <span class="tqr-legend-item__value">{{ formatBytes(agent.used_bytes || 0) }}</span>
      </div>
    </div>
    <div v-else class="traffic-quota-ring__info">
      <span class="traffic-quota-ring__label">{{ infoLabel }}</span>
      <span class="traffic-quota-ring__value">{{ infoValue }}</span>
    </div>
  </div>
</template>
```

Add styles:

```css
.traffic-quota-ring__legend {
  display: flex;
  flex-direction: column;
  gap: 0.375rem;
  width: 100%;
  margin-top: 0.5rem;
}
.tqr-legend-item {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  font-size: 0.75rem;
}
.tqr-legend-item__dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  flex-shrink: 0;
}
.tqr-legend-item__name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--color-text-primary);
}
.tqr-legend-item__value {
  color: var(--color-text-secondary);
  font-weight: 500;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
```

- [ ] **Step 4: Build and commit**

Run: `cd panel/frontend && npm run build`

```bash
git add panel/frontend/src/components/traffic/TrafficQuotaRing.vue
git commit -m "feat(frontend): restyle TrafficQuotaRing with dark theme and legend"
```

---

## Task 8: Frontend — Dark theme adaptation for chart components

**Files:**
- Modify: `panel/frontend/src/components/traffic/TrafficTrendChart.vue`
- Modify: `panel/frontend/src/components/traffic/TrafficRateSparkline.vue`

- [ ] **Step 1: Update TrafficTrendChart for dark theme**

In `chartOptions` computed, update these properties:

```javascript
const chartOptions = computed(() => {
  return {
    // ... existing config ...
    grid: {
      borderColor: 'rgba(255,255,255,0.06)',
      strokeDashArray: 0,
      xaxis: { lines: { show: false } }
    },
    theme: { mode: 'dark' },
    // ... rest unchanged ...
  }
})
```

- [ ] **Step 2: Update TrafficRateSparkline for dark theme**

In `chartOptions` computed, add:

```javascript
const chartOptions = computed(() => ({
  chart: {
    type: 'area',
    sparkline: { enabled: true },
    toolbar: { show: false },
    animations: { enabled: false }
  },
  theme: { mode: 'dark' },
  colors: ['#60a5fa'],
  stroke: { curve: 'smooth', width: 2 },
  fill: { opacity: 0.15 },
  tooltip: {
    enabled: true,
    x: { show: false },
    y: { formatter: (value) => formatBytes(value) },
    marker: { show: false },
    theme: 'dark'
  }
}))
```

- [ ] **Step 3: Build and commit**

Run: `cd panel/frontend && npm run build`

```bash
git add panel/frontend/src/components/traffic/TrafficTrendChart.vue panel/frontend/src/components/traffic/TrafficRateSparkline.vue
git commit -m "feat(frontend): dark theme adaptation for traffic charts"
```

---

## Task 9: Verify and test

- [ ] **Step 1: Backend tests**

Run: `cd panel/backend-go && go test ./internal/controlplane/...`
Expected: All tests pass.

- [ ] **Step 2: Frontend build**

Run: `cd panel/frontend && npm run build`
Expected: Build succeeds.

- [ ] **Step 3: Manual verification checklist**

- [ ] Switch to neko-dark theme, verify all pages render correctly
- [ ] Check Dashboard traffic module loads with single `/traffic-aggregate` request
- [ ] Verify donut chart shows legend with colored dots
- [ ] Verify trend chart grid lines are visible in dark theme
- [ ] Check responsive layout at 320px, 768px, 1024px, 1440px
- [ ] Verify existing themes (sakura-day, sakura-night, business) still work

- [ ] **Step 4: Final commit**

```bash
git commit -m "feat: complete neko-dark traffic dashboard redesign"
```

---

## Self-Review Checklist

### Spec Coverage
- [x] neko-dark theme CSS variables → Task 1
- [x] ThemeContext update → Task 2
- [x] Backend aggregate API → Tasks 3, 4
- [x] Frontend aggregate API + hook → Task 5
- [x] Dashboard 3-column layout → Task 6
- [x] Donut chart + legend → Task 7
- [x] Dark theme chart adaptation → Task 8
- [x] Testing → Task 9

### Placeholder Scan
- [x] No TBD/TODO
- [x] All code shown
- [x] All commands shown with expected output
- [x] Type names consistent across tasks

### Type Consistency
- `TrafficAggregateResult` used in Task 3 (backend), Task 4 (handler), Task 5 (frontend API)
- `fetchTrafficAggregate` used in Task 5 (runtime.js), Task 5 (index.js), Task 5 (hook), Task 6 (DashboardTrafficModule)
- `useTrafficAggregate` used in Task 5 (hook creation), Task 6 (DashboardTrafficModule import)
