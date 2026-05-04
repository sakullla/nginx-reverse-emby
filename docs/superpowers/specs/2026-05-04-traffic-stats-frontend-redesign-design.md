# Traffic Stats Frontend Redesign Design

## Goal

Redesign the frontend traffic statistics and display to address four core pain points:

1. **Multi-node global view is not intuitive** — the current dashboard sums usage across nodes with different cycles/policies, making it hard to spot which node is at risk.
2. **Cannot find top consumers** — rule breakdowns are flat and unsorted; finding heavy-hitters among hundreds of rules is painful.
3. **Quota/cycle/block status is not prominent enough** — used/remaining/remaining-days require mental math; there is no progress bar or visual alert.
4. **Weak trend analysis** — no date range picker, no previous-period comparison, no node-to-node overlay, no export.

Additionally, expose **host traffic monitoring** (host_total + per-interface breakdowns) which the agent already reports but the frontend does not display.

## Scope

In scope:

- **Dashboard Traffic Module** — card-centric redesign with time range picker, node selector, business-vs-host dual trend, top-nodes, top-rules, quota progress, and alert cards.
- **Agent Detail Traffic Tab** — redesigned summary cards with visual progress bars, tabbed breakdown tables (HTTP / L4 / Relay / Host Interfaces), sortable columns with sparklines, and host bandwidth card.
- **Rule/Listener Card Traffic Display** — replace the small text line with a traffic progress bar card showing accounted usage, percentage of node total, and RX/TX split.
- **Trend Modal** — add date range picker, previous-period comparison toggle, daily-budget reference line, and summary stat cards (current total / previous total / MoM / daily average).
- **Host Traffic Visualization** — show `host_total` aggregate and per-interface (`host_interface`) breakdowns in both the agent detail tab and dashboard.
- **Backend API additions** — two small additions to expose host data in existing APIs.

Out of scope:

- Backend traffic ingestion, accounting, quota logic, or storage schema changes (beyond two response field additions).
- Agent-side changes (host traffic is already reported).
- Per-rule quota limits or billing.
- Alerting/webhook features.
- PDF/image export of charts.

## Design Approach

**Card + Panoramic (方案 B)** — the dashboard emphasizes visual status cards, progress bars, and a panoramic trend chart. This prioritizes "at-a-glance" health awareness over dense tabular scanning. Tables are still used inside agent detail breakdowns where sorting and precise values matter.

## Dashboard Traffic Module

### Layout

```
┌─────────────────────────────────────────────────────────────┐
│ 流量统计   [本月] [本日] [近7天] [自定义]   [全部节点 ▼] [导出] │
├─────────────────────────────────────────────────────────────┤
│ ┌──────────────┐ ┌──────────────┐ ┌──────────┐ ┌──────────┐ │
│ │ 业务总用量   │ │ 主机总带宽   │ │ 节点告警 │ │ 周期信息 │ │
│ │ 465/600 GiB  │ │ 1.65 GiB     │ │ 1 即将超额│ │ 剩余27天 │ │
│ │ ████████░░   │ │ ████░░░░░░   │ │          │ │ 3.0GiB/天│ │
│ └──────────────┘ └──────────────┘ └──────────┘ └──────────┘ │
├─────────────────────────────────────────────────────────────┤
│ 流量趋势 — 业务 vs 主机带宽              [小时] [日] [月]    │
│                                                            │
│    ┌────────────────────────────────────────────┐         │
│    │ 业务(accounted) ──────────●─────●          │         │
│    │ 主机带宽  - - - - - - ● - - - - ●          │         │
│    │ ─ ─ ─ 日均预算 ─ ─ ─ ─ ─ ─ ─ ─ ─           │         │
│    └────────────────────────────────────────────┘         │
├─────────────────────────────────────────────────────────────┤
│ ┌────────────────────────┐  ┌────────────────────────┐     │
│ │ Top 节点 — 按已用占比   │  │ Top 规则 — 按 accounted │     │
│ │ ●84% edge-hk-01        │  │ #9 TCP ... 12 KiB      │     │
│ │ ● 5% dev-local         │  │ #7 HTTPS ... 3 KiB     │     │
│ └────────────────────────┘  └────────────────────────┘     │
└─────────────────────────────────────────────────────────────┘
```

### Behavior

- **Time range picker**: presets (本月 / 本日 / 近7天) + custom date range. Granularity auto-adjusts based on range length (large ranges → day/month; small ranges → hour).
- **Node selector**: "全部节点" shows aggregated cluster view; selecting a single node switches to that node's trend and top-rules.
- **Top nodes card**: sorted by used/quota percentage descending. Nodes without quota sorted by used bytes descending and shown after quota-bearing nodes. Each row shows a circular percentage badge (green/yellow/red thresholds) and a mini progress bar.
- **Top rules card**: across all nodes when "全部节点" is selected, or scoped to the selected node. Sorted by `accounted_bytes`. Shows rule ID, name, node, and percentage of that node's total.
- **Business vs Host trend**: dual-series line chart. Business = `accounted_bytes` from `agent_total` scope. Host = `host_total` scope. A daily-budget reference line is shown when a monthly quota is configured.
- **Export button**: downloads CSV of current view (agents + usage + host bandwidth).

### Quota Reference Line Rule

To avoid comparing a cycle total (e.g. 500 GiB/month) against daily/hourly bucket values on the same Y-axis:

- **Hour / Day granularity**: render a **daily budget line** = `quota_bytes / cycle_days`. This is directly comparable to daily traffic.
- **Month granularity**: render the raw **monthly quota line**, since monthly aggregates approach the cycle total.
- **No quota configured**: hide the reference line entirely.

## Agent Detail Traffic Tab

### Layout

Replace the current 6-card summary + flat breakdown with:

1. **5 summary cards** (top row):
   - **已用** — large number + progress bar + threshold-colored percentage (green <70%, yellow 70-90%, red >90%).
   - **额度** — quota value + accounting direction label.
   - **剩余 / 日均可用** — remaining bytes + days left in cycle + daily budget.
   - **主机带宽 (24h)** — host_total for the last 24h + top interface split (e.g. "eth0 92% · ens1 8%").
   - **状态** — "正常" or "已阻断" with red highlight when blocked.

2. **Trend chart** — same dual-series design as dashboard (accounted + host_total + daily budget line), with date range picker and granularity toggle.

3. **Tabbed breakdown section** — 4 tabs:
   - **HTTP** — sortable table of HTTP rules with columns: name/ID, accounted, RX, TX, % of HTTP total, 7-day sparkline.
   - **L4** — same for L4 rules.
   - **Relay** — same for relay listeners.
   - **主机接口** — table of `host_interface` scopes with columns: interface name, RX, TX, % of host total, 7-day sparkline.
   - Click any row to open the trend modal for that scope.

4. **Collapsible sections** below (retained, styled consistently):
   - **策略设置** — TrafficPolicyForm.
   - **历史管理** — TrafficHistoryManager (calibrate + cleanup).

### Host Interface Tab

- Data comes from `trafficSummary.host_interfaces[]` (new backend field).
- `scope_id` is the interface name (e.g. `eth0`, `ens1`).
- If no host interfaces are present (agent not reporting host traffic), the tab is visible but shows an empty state: "未检测到主机接口流量".

## Rule / Listener Card Traffic Display

Replace the current bottom text line:

```
用量 3.00 KiB  入 1.00 KiB  出 2.00 KiB
```

With a **traffic progress bar card** inside the rule card:

- **Top row**: "用量 3.0 KiB" (left) + "占节点 20%" (right).
- **Progress bar**: percentage of the node's total `accounted_bytes`, color-coded:
  - Blue (< 30%)
  - Yellow (30-70%)
  - Red (> 70%)
- **Bottom row**: "入 1.0 KiB / 出 2.0 KiB" in muted text.
- **Click behavior**: opens the trend modal for that rule/listener scope.

Applied to: `RuleCard` (HTTP), `L4RuleItem`, `RelayCard`.

## Trend Modal Extensions

The existing `TrafficTrendModal` is extended with:

1. **Date range picker** — `from` / `to` inputs that feed the existing `useTrafficTrend` hook (the API already supports these params).
2. **Previous period comparison** — a toggle checkbox "对比上一周期". When enabled:
   - Compute previous period dates as `[from - duration, to - duration]`.
   - Issue a second trend request for the previous period.
   - Render current period as a solid line, previous period as a lighter dashed line.
   - If previous period has no data, the dashed series is omitted gracefully.
3. **Daily budget reference line** — shown at day/hour granularity when the agent has a monthly quota.
4. **Summary stat cards** (bottom of modal):
   - 本周期合计
   - 上一周期合计
   - 环比 (percentage change, colored green/red)
   - 日均

The modal continues to support all scope types: `agent_total`, `http_rule`, `l4_rule`, `relay_listener`, `host_total`, `host_interface`.

## Backend API Additions

Two small additive changes to existing endpoints:

### 1. TrafficSummary — add host fields

`GET /api/agents/{id}/traffic-summary` response shape:

```json
{
  "ok": true,
  "summary": {
    "agent_id": "...",
    "used_bytes": 420000000000,
    "monthly_quota_bytes": 536870912000,
    ...
    "aggregates": [...],
    "http_rules": [...],
    "l4_rules": [...],
    "relay_listeners": [...],
    "host_total": { "scope_type": "host_total", "scope_id": "", "rx_bytes": 1200000000, "tx_bytes": 450000000, "accounted_bytes": 1650000000 },
    "host_interfaces": [
      { "scope_type": "host_interface", "scope_id": "eth0", "rx_bytes": 1100000000, "tx_bytes": 400000000, "accounted_bytes": 1500000000 },
      { "scope_type": "host_interface", "scope_id": "ens1", "rx_bytes": 100000000, "tx_bytes": 50000000, "accounted_bytes": 150000000 }
    ]
  }
}
```

Implementation: in `trafficService.Summary`, query `host_total` and `host_interface` scopes the same way `http`/`l4`/`relay` aggregates are queried, and append them to the response.

### 2. TrafficOverview — add host trend

`GET /api/traffic-overview` response shape:

```json
{
  "ok": true,
  "agents": [...],
  "trend": [...],
  "host_trend": [...]
}
```

`host_trend` follows the same `TrafficTrendPoint` structure as `trend`, but sourced from `host_total` scope. Used for the dashboard dual-series chart. When the global traffic module is disabled, both `trend` and `host_trend` are omitted.

## Data Flow

### Dashboard

```
DashboardPage
  ├─ useTrafficOverview(agentFilter)     → GET /api/traffic-overview
  ├─ useAgents()                         → GET /api/agents
  ├─ per-agent summaries (parallel)      → GET /api/agents/{id}/traffic-summary (for top-rules aggregation)
  └─ computed top-nodes, top-rules, trend series
```

Top-rules aggregation runs in the browser: collect `http_rules` + `l4_rules` + `relay_listeners` from each agent's summary, flatten, sort by `accounted_bytes`, take top N (e.g. 5 or 10). This avoids a new backend endpoint and is acceptable for typical fleet sizes (< 50 agents).

### Agent Detail

All existing hooks continue to work. The traffic tab receives:
- `useTrafficPolicy(agentId)`
- `useTrafficSummary(agentId)` — now includes host fields
- `useTrafficTrend(agentId, { granularity, from, to, scope_type, scope_id })`

### Trend Modal

- Accepts `from`/`to` via new props.
- When "对比上一周期" is enabled, computes `prevFrom`/`prevTo` and issues a second `useTrafficTrend` call (or fetches directly via the API function).
- Merges both series into a single chart config.

## Error Handling & Edge Cases

| Scenario | Behavior |
|---|---|
| Agent has no traffic data | Trend chart shows empty state "暂无流量数据"; summary cards show 0 B. |
| Agent does not report host traffic | Host bandwidth card hidden; Host Interfaces tab shows empty state. |
| Quota is unlimited | Quota/remaining/progress bar sections show "无限制"; daily budget line hidden. |
| Previous period has no data | Comparison toggle renders unchecked and disabled with tooltip "上一周期无数据". |
| Date range too large | Frontend caps hour granularity to 7 days, day granularity to 90 days; shows validation message. |
| Node blocked | Status card turns red with "已阻断" label; progress bar stays visible. |
| Multi-node dashboard, nodes have different cycles | Top nodes sort by percentage first, then by raw bytes. No attempt to merge incompatible cycles. |

## Component Architecture

New / modified components:

```
src/components/traffic/
  DashboardTrafficModule.vue         (rewrite)
  TrafficTrendChart.vue              (extend: dual-series, prev-period, budget line)
  TrafficTrendModal.vue              (extend: date range, comparison toggle, summary cards)
  TrafficSummaryCards.vue            (rewrite: 5 cards with progress bars)
  TrafficBreakdownTable.vue          (extend: sortable headers, sparkline column, tabs)
  TrafficBar.vue                     (new: rule-card traffic progress bar)
  TrafficPolicyForm.vue              (keep, style-align)
  TrafficHistoryManager.vue          (keep, style-align)
  TrafficCollapsibleSection.vue      (keep)

src/hooks/
  useTraffic.js                      (extend useTrafficTrend to accept from/to)
  useTrafficOverview.js              (keep)

src/utils/trafficStats.js            (keep + add helpers for percentage, color thresholds)
```

## Testing Strategy

### Component tests

- `DashboardTrafficModule`: renders cards when data loaded; hides when traffic stats disabled; node selector switches data; time range presets work.
- `TrafficSummaryCards`: shows correct colors for threshold levels; hides host card when no host data; shows "无限制" when no quota.
- `TrafficBreakdownTable`: sorting by accounted/rx/tx/percentage; tab switching; empty state per tab.
- `TrafficBar`: renders progress bar with correct width and color; click emits event.
- `TrafficTrendModal`: date range changes trigger new queries; comparison toggle renders second series; summary stats compute correctly.

### Hook tests

- `useTrafficTrend`: forwards `from`/`to` parameters correctly; caches by full param key.
- Dashboard computed top-rules: correctly flattens and sorts multi-agent summaries.

### Integration tests

- Agent detail: tab switching renders correct scope tables; clicking row opens modal with correct scope.
- Dashboard: selecting "全部节点" aggregates across agents; selecting single node scopes to that node.

### Backend tests

- `traffic-summary` handler returns `host_total` and `host_interfaces` when host traffic data exists.
- `traffic-overview` handler returns `host_trend` alongside `trend`.
- Both fields are omitted (not null) when the global traffic module is disabled.

## Verification Commands

```bash
# Frontend build
cd panel/frontend && npm run build

# Backend tests
cd panel/backend-go && go test ./...

# Agent tests
cd go-agent && go test ./...

# Container build
docker build -t nginx-reverse-emby .
```

## Commit Style

Follow Conventional Commits:

- `feat(panel): redesign dashboard traffic module with cards and top-n`
- `feat(panel): add host traffic breakdown to agent detail traffic tab`
- `feat(panel): add traffic progress bar to rule cards`
- `feat(panel): extend trend modal with date range and period comparison`
- `feat(backend): expose host_total and host_interfaces in traffic summary`
- `feat(backend): add host_trend to traffic overview response`
