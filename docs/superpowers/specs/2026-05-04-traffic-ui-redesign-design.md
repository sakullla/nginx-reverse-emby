# Traffic UI Redesign Design

## Goal

Redesign the traffic statistics frontend: replace the current pure-CSS bar chart with Chart.js, add a dashboard traffic overview module, add per-rule/listener traffic trend popups, and restructure the node detail traffic tab into collapsible sections.

## Scope

In scope:

- Introduce `chart.js` + `vue-chartjs` as chart dependencies.
- Create reusable `TrafficTrendChart.vue`, `TrafficTrendModal.vue`, `TrafficSummaryCards.vue`, `TrafficBreakdownTable.vue`, and `TrafficPolicyForm.vue` components.
- Add a new `GET /api/traffic-overview` backend API for dashboard aggregation.
- Add a traffic statistics module to the dashboard page with trend chart and node switching.
- Add traffic trend popups to RulesPage, L4RulesPage, and RelayListenersPage for per-rule/listener trends.
- Restructure the AgentDetailPage traffic tab into three collapsible sections (overview, policy, history).

Out of scope:

- Backend traffic ingestion, accounting, or quota logic changes.
- Agent-side changes.
- Per-rule quota limits.
- New traffic storage tables or schema changes.
- Billing, alerting, or export features.

## Dependencies

- `chart.js` — chart rendering engine.
- `vue-chartjs` — Vue 3 wrapper for Chart.js.

Install:

```bash
cd panel/frontend
npm install chart.js vue-chartjs
```

## Component Architecture

```
src/components/traffic/
  TrafficTrendChart.vue      — Reusable Chart.js chart (line/bar, receives data + config)
  TrafficTrendModal.vue      — Modal dialog wrapping TrendChart + granularity toggle
  TrafficSummaryCards.vue    — Monthly quota summary card group
  TrafficBreakdownTable.vue  — Per-scope traffic breakdown table with clickable rows
  TrafficPolicyForm.vue      — Traffic policy settings form
  TrafficHistoryManager.vue  — Calibration + cleanup controls
src/composables/
  useTrafficTrendChart.js    — Chart.js instance lifecycle + data mapping
```

`TrafficTrendChart.vue` accepts:

- `trendPoints: TrafficTrendPoint[]` — data array.
- `granularity: 'hour' | 'day' | 'month'` — controls x-axis formatting.
- `direction: string` — accounted direction for display.
- Optional `quotaBytes: number | null` — renders a reference line when set.

`TrafficTrendModal.vue` accepts:

- `visible: boolean` — v-model for open/close.
- `agentId: string`.
- `scopeType: string` — `http_rule`, `l4_rule`, or `relay_listener`.
- `scopeId: string`.
- `scopeLabel: string` — display name for the modal title.

The modal uses `useTrafficTrend` internally and renders `TrafficTrendChart` with a granularity toggle.

## Backend: Traffic Overview API

### Endpoint

`GET /api/traffic-overview`

### Parameters

- `agent_id` (query, optional) — filter trend data to a single agent. Omit for all agents combined.

### Response

```json
{
  "agents": [
    {
      "agent_id": "uuid",
      "name": "node-1",
      "used_bytes": 137438953472,
      "quota_bytes": 536870912000,
      "remaining_bytes": 399431958528,
      "blocked": false,
      "direction": "both"
    }
  ],
  "trend": [
    {
      "bucket_start": "2026-05-01T00:00:00Z",
      "rx_bytes": 10737418240,
      "tx_bytes": 8589934592
    }
  ]
}
```

### Implementation

The handler calls the traffic service to:

1. List all agents with their traffic summary (used, quota, remaining, blocked, direction).
2. When `agent_id` is provided, query daily trend for that agent's `agent_total` scope for the current cycle.
3. When `agent_id` is omitted, query daily trend for all agents' `agent_total` scopes and sum the buckets by `bucket_start`.

This requires a new service method `TrafficOverview(ctx, agentID *string)` and a new store method that can aggregate trend data across agents.

## Frontend: Dashboard Traffic Module

Add a traffic statistics card/block to the existing dashboard page.

### Layout

```
┌──────────────────────────────────────────────┐
│ 流量统计                     [节点选择器 ▼]   │
├──────────────────────────────────────────────┤
│                                              │
│   Chart.js dual-series line chart            │
│   (RX / TX, daily granularity, current cycle)│
│                                              │
├──────────────────────────────────────────────┤
│ 已用 128 GiB  │  额度 500 GiB  │  剩余 372 GiB │
└──────────────────────────────────────────────┘
```

### Behavior

- On mount, call `GET /api/traffic-overview` to get all agents' summaries and combined trend.
- The node selector dropdown defaults to "全部节点" (all nodes combined).
- Selecting a specific node calls `GET /api/traffic-overview?agent_id=...` to get that node's trend.
- Summary cards below the chart update to reflect the selected node (or totals).
- The chart uses `TrafficTrendChart.vue` in dual-series mode (RX + TX lines).
- The entire module is conditionally rendered based on `traffic_stats_enabled` from system info.

### New Hook

`useTrafficOverview(agentId)` — composable wrapping the overview API call.

## Frontend: Per-Rule/Listener Traffic Trend Popups

### Pages Affected

- `RulesPage.vue` — HTTP rules.
- `L4RulesPage.vue` — L4 rules.
- `RelayListenersPage.vue` — Relay listeners.

### Interaction

1. Each rule card already displays a traffic usage line (e.g., `用量 3.00 KiB  入 1.00 KiB  出 2.00 KiB`).
2. Make the traffic usage line clickable (cursor pointer + hover underline).
3. On click, open `TrafficTrendModal` with the rule's `agentId`, `scopeType`, `scopeId`, and `scopeLabel`.
4. The modal fetches trend data via existing `GET /api/agents/{agentId}/traffic-trend?scope_type={scopeType}&scope_id={scopeId}` and renders the chart.

### Scope Type Mapping

| Page | scope_type | scope_id |
|---|---|---|
| RulesPage | `http_rule` | rule ID |
| L4RulesPage | `l4_rule` | rule ID |
| RelayListenersPage | `relay_listener` | listener ID |

No new backend endpoints needed — the existing trend API supports these parameters.

## Frontend: Node Detail Traffic Tab Restructure

Replace the current flat layout with three collapsible sections.

### Section 1: Overview (default: expanded)

Contains:

- `TrafficSummaryCards.vue` — 6 cards: used, quota, remaining, cycle period, direction, blocked status.
- `TrafficTrendChart.vue` — daily trend chart with granularity toggle (hour/day/month).
- `TrafficBreakdownTable.vue` — per-scope breakdown with clickable rows that open `TrafficTrendModal`.

### Section 2: Policy Settings (default: collapsed)

Contains:

- `TrafficPolicyForm.vue` — direction, cycle start day, monthly quota, block-when-exceeded, retention settings, save button.

### Section 3: History Management (default: collapsed)

Contains:

- `TrafficHistoryManager.vue` — calibrate (set used bytes), reset from now, cleanup.

### Collapsible Section Component

Each section has:

- A header bar with title + status indicator + expand/collapse chevron icon.
- Clicking the header toggles the section body visibility with a CSS transition.
- The chevron rotates 90 degrees when expanded.

### Data Flow

All data continues to come from existing hooks (`useTrafficPolicy`, `useTrafficSummary`, `useTrafficTrend`, `useUpdateTrafficPolicy`, `useCalibrateTraffic`, `useCleanupTraffic`). The page component passes data down to the extracted sub-components as props.

## Testing

Frontend verification:

```bash
cd panel/frontend && npm run build
```

Manual test checklist:

- Dashboard: traffic module renders when enabled, hides when disabled.
- Dashboard: node selector switches trend data.
- Dashboard: summary cards update on node switch.
- RulesPage/L4Page/RelayPage: clicking traffic usage opens modal with trend chart.
- Modal: granularity toggle changes chart data and x-axis labels.
- AgentDetailPage: three collapsible sections work correctly.
- AgentDetailPage: breakdown table rows open trend modal.
- AgentDetailPage: policy form saves correctly.
- AgentDetailPage: calibrate and cleanup work.

Backend verification:

```bash
cd panel/backend-go && go test ./...
```

- New `TrafficOverview` service method returns correct agent summaries and trend aggregation.
- Trend aggregation across agents produces correct summed buckets.
- Single-agent filter returns only that agent's data.
