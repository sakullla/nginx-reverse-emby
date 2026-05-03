# Traffic Stats Redesign Design

## Goal

Redesign traffic statistics so the control plane can persist historical traffic, compute node quota usage consistently, show traffic trends in the node detail page, and support PostgreSQL/MySQL production storage.

The execution agent continues to report cumulative raw traffic counters. The control plane becomes responsible for delta calculation, bucket aggregation, quota accounting, retention, cleanup, and block decisions.

## Scope

In scope:

- Support PostgreSQL, MySQL, and legacy SQLite through a shared GORM-backed store.
- Change Docker defaults to PostgreSQL.
- Add a control-plane environment variable to disable the traffic statistics module globally.
- Provide a manual SQLite-to-PostgreSQL/MySQL migration command instead of automatic startup migration.
- Persist raw traffic cursors, hourly buckets, daily summaries, monthly summaries, node policies, cycle baselines, and calibration adjustments.
- Compute all node traffic displays, quota usage, rule breakdowns, cleanup decisions, and block decisions from the node traffic policy.
- Add node detail traffic views with summary, trend chart, per-scope breakdowns, policy settings, calibration, and cleanup.
- Support configurable node traffic direction: `rx`, `tx`, `both`, or `max`, with `both` as the default.
- Support fixed monthly cycles with a per-node monthly start day, defaulting to day `1`.
- Support a per-node monthly quota limit.
- Support optional node-wide over-quota blocking, disabled by default.
- Support calibration by starting from the current raw value as zero, or by setting the current cycle used value.
- Support configurable retention and active/manual cleanup.

Out of scope:

- Per-rule quota limits.
- Per-user or per-client identity accounting.
- Billing-provider integration.
- Automatic startup migration from SQLite to PostgreSQL/MySQL.
- Hard termination of already-open over-quota connections.

## Database Architecture

The storage layer should be generalized from `SQLiteStore` to a dialect-aware GORM store. The store should accept a driver and DSN from configuration:

- `NRE_DATABASE_DRIVER=postgres|mysql|sqlite`
- `NRE_DATABASE_DSN=<driver-specific dsn>`

Docker defaults should set:

```text
NRE_DATABASE_DRIVER=postgres
NRE_DATABASE_DSN=postgres://nre:nre@postgres:5432/nre?sslmode=disable
```

MySQL is supported by selecting `mysql` and providing a MySQL DSN. SQLite remains available for development and legacy installations, but it is no longer the Docker default.

Schema migrations should use GORM migrator APIs where practical. Any dialect-specific SQL, especially upsert behavior for bucket increments, should be isolated behind small store helper methods rather than spread through service code.

Existing SQLite users migrate explicitly with a command such as:

```text
nre-control-plane migrate-storage --from-driver sqlite --from-dsn <sqlite file> --to-driver postgres --to-dsn <pg dsn>
```

The control plane must not automatically migrate SQLite data on startup.

## Global Module Switch

The control panel should expose a process-level environment variable:

- `NRE_TRAFFIC_STATS_ENABLED=true|false`

The default is `true`.

When `NRE_TRAFFIC_STATS_ENABLED=false`, the traffic statistics module is disabled globally:

- The control plane does not run traffic-history table migrations.
- Heartbeat traffic stats are ignored and are not persisted to raw cursors, buckets, summaries, or latest display state.
- Traffic policy, summary, trend, calibration, and cleanup APIs return HTTP `404` with stable error code `TRAFFIC_STATS_DISABLED`.
- Quota calculation is disabled.
- Quota-based traffic blocking is disabled.
- Agent runtime config must send traffic reporting disabled and `traffic_blocked=false`.
- Panel bootstrap/config APIs expose `traffic_stats_enabled=false`.
- The frontend hides the node Traffic tab, traffic policy controls, calibration controls, cleanup controls, trend chart, quota indicators, and per-rule/listener traffic statistics.

Disabling the module must not delete existing traffic tables or history. If the process is later restarted with the module enabled, migrations run normally and existing traffic data becomes available again.

This switch is intended for deployments that do not want traffic accounting overhead, quota behavior, or traffic-history storage at all. It is separate from per-node traffic policy settings.

## Traffic Tables

The traffic module owns these tables:

- `agent_traffic_policies`
- `agent_traffic_baselines`
- `agent_traffic_raw_cursors`
- `agent_traffic_hourly_buckets`
- `agent_traffic_daily_summaries`
- `agent_traffic_monthly_summaries`
- `agent_traffic_events`

`agent_traffic_policies` stores one policy per node:

- `agent_id`
- `direction`: `rx`, `tx`, `both`, or `max`
- `cycle_start_day`: integer `1..28`, default `1`
- `monthly_quota_bytes`: nullable integer; `NULL` means no quota limit
- `block_when_exceeded`: boolean, default `false`
- `hourly_retention_days`: default `180`
- `daily_retention_months`: default `24`
- `monthly_retention_months`: nullable; `NULL` means keep long-term
- timestamps

`agent_traffic_baselines` stores the accounting baseline for a node and cycle:

- `agent_id`
- `cycle_start`
- `raw_rx_bytes`
- `raw_tx_bytes`
- `raw_accounted_bytes`
- `adjust_used_bytes`
- timestamps

The baseline primary key is `(agent_id, cycle_start)`.

`agent_traffic_raw_cursors` stores the latest cumulative raw value seen for each scope:

- `agent_id`
- `scope_type`
- `scope_id`
- `rx_bytes`
- `tx_bytes`
- `observed_at`

The primary key is `(agent_id, scope_type, scope_id)`.

Hourly, daily, and monthly rows store aggregated deltas:

- `agent_id`
- `scope_type`
- `scope_id`
- `bucket_start` or `period_start`
- `rx_bytes`
- `tx_bytes`
- timestamps

Their primary key is `(agent_id, scope_type, scope_id, bucket_start)` or `(agent_id, scope_type, scope_id, period_start)`.

Supported scope types:

- `agent_total`
- `http`
- `l4`
- `relay`
- `http_rule`
- `l4_rule`
- `relay_listener`

`scope_id` is empty for aggregate scopes and the rule/listener ID for object scopes.

`agent_traffic_events` records operational events such as counter resets, quota block transitions, calibration changes, and cleanup runs. It is not the source of accounting truth.

## Ingestion Flow

Agents report cumulative raw counters in heartbeat payloads. When the global traffic module is enabled, the control plane ingests each reported scope independently:

1. Load or create the raw cursor for `(agent_id, scope_type, scope_id)`.
2. If the new cumulative value is greater than or equal to the cursor, compute delta as `new - cursor`.
3. If the new cumulative value is lower than the cursor, treat it as an agent counter reset and compute delta from the new value.
4. Upsert the hourly bucket and increment `rx_bytes` and `tx_bytes` by the delta.
5. Upsert the daily and monthly summaries with the same delta.
6. Update the raw cursor to the new cumulative value.

This cursor model makes repeated heartbeats idempotent when cumulative values have not changed. It also prevents negative deltas after agent restart.

Heartbeat stats may be omitted when reporting is not due. Omitted stats must not clear persisted values. An explicit empty stats object from a disabled stats configuration is the signal to clear latest display state, but historical buckets should remain unless cleanup deletes them.

## Accounting Rules

Raw persisted buckets always keep separate `rx_bytes` and `tx_bytes`. Accounted usage is computed from the node policy:

- `rx`: account `rx_bytes`
- `tx`: account `tx_bytes`
- `both`: account `rx_bytes + tx_bytes`
- `max`: account `max(rx_bytes, tx_bytes)`

The default policy is `both`.

All usage views and quota decisions must apply the same accounting function:

- node summary
- traffic trend totals
- HTTP/L4/Relay aggregate breakdowns
- HTTP rule, L4 rule, and Relay listener breakdowns
- remaining quota
- over-quota state
- block state

The UI may still show raw rx/tx columns for diagnosis, but quota labels and totals must use accounted usage.

## Billing Cycle And Quota

Each node uses a fixed monthly cycle. The cycle starts on the configured `cycle_start_day` at local control-plane time. The value is limited to `1..28` so every month has a valid start day.

For a node with `cycle_start_day = 1`, the May 2026 cycle is `2026-05-01 00:00:00` through before `2026-06-01 00:00:00`.

For a node with `cycle_start_day = 15`, the May 2026 cycle is `2026-05-15 00:00:00` through before `2026-06-15 00:00:00`.

Monthly quota is stored as bytes. `NULL` means unlimited. `0` means the quota is zero bytes and the node is immediately over quota once accounted usage is positive.

Cycle used bytes are:

```text
accounted bytes in current cycle - baseline raw_accounted_bytes + adjust_used_bytes
```

The computed value should not go below zero for display or blocking.

## Calibration

Calibration supports two operations.

Start from current raw value as zero:

- Read the current cumulative raw total for the node.
- Store a baseline for the current cycle using current raw rx/tx and current accounted value.
- Set `adjust_used_bytes` to `0`.

Set current used value:

- Compute the current cycle raw accounted value using the node policy.
- Set `adjust_used_bytes` so the displayed cycle used bytes equals the requested value.
- Keep historical buckets unchanged.

Calibration is cycle-scoped. A new billing cycle creates a new baseline from current raw state and does not carry forward the previous cycle's correction.

## Trend APIs

When the global traffic module is enabled, the backend exposes traffic APIs under the agent resource:

- `GET /api/agents/{id}/traffic-policy`
- `PATCH /api/agents/{id}/traffic-policy`
- `GET /api/agents/{id}/traffic-summary`
- `GET /api/agents/{id}/traffic-trend?granularity=hour|day|month&from=&to=&scope_type=&scope_id=`
- `POST /api/agents/{id}/traffic-calibration`
- `POST /api/agents/{id}/traffic-cleanup`

`traffic-summary` returns policy, current cycle boundaries, raw rx/tx, accounted used bytes, quota bytes, remaining bytes, usage percent, over-quota state, block state, and top scope breakdowns.

`traffic-trend` returns time buckets with raw rx/tx and accounted bytes. For daily/monthly queries it should read summaries rather than re-scanning hourly data.

`traffic-cleanup` triggers cleanup for one node using the node policy retention settings. Node-level cleanup is the required UI path for this redesign.

## Node Detail UI

Traffic should live in the node detail page as a dedicated Traffic tab.

When the global traffic module is disabled, the frontend should hide this tab and all traffic-derived fields instead of rendering zero traffic. This makes the disabled state explicit and avoids suggesting that accounting is active.

The tab includes:

- Cycle summary cards: used, quota, remaining, cycle range, current accounting direction.
- Trend chart with granularity switch: hour, day, month.
- Breakdown table for aggregate HTTP/L4/Relay usage.
- Breakdown table for HTTP rules.
- Breakdown table for L4 rules.
- Breakdown table for Relay listeners.
- Policy form for accounting direction, monthly start day, quota limit, and over-quota blocking.
- Retention form for hourly, daily, and monthly history.
- Calibration controls for "start from now as zero" and "set current used value".
- Manual cleanup action.

The default chart should use daily data for the current cycle. The chart should display accounted usage as the primary series, with optional raw rx/tx details available in tooltip/table form. If a monthly quota is configured, the chart should show a quota reference line.

The UI should make clear that `both` is the default accounting direction and that quota enforcement uses accounted traffic, not simply one raw direction.

## Over-Quota Blocking

When the global traffic module is enabled, the control plane computes quota state after ingesting traffic and when policy changes. If `block_when_exceeded` is enabled and current cycle used bytes exceed the quota, the control plane marks the node as traffic blocked.

When the global traffic module is disabled, quota-based blocking is disabled and the control plane always sends `traffic_blocked=false`.

The agent runtime config should include:

- `traffic_blocked`: boolean
- `traffic_block_reason`: string

The agent enforces node-level blocking across HTTP, L4, and Relay:

- HTTP returns `429`.
- L4 rejects or immediately closes new accepted connections.
- Relay rejects or immediately closes new accepted sessions.

The first implementation blocks new traffic only. Existing long-lived streams are allowed to finish naturally. This avoids surprising hard disconnects and keeps enforcement behavior simple.

## Retention And Cleanup

Default retention:

- hourly buckets: `180` days
- daily summaries: `24` months
- monthly summaries: long-term

Each node can override retention in its traffic policy.

Cleanup deletes rows older than the configured retention window for that node. Cleanup should be available as:

- manual cleanup from the node Traffic tab
- backend service method usable by a future scheduled cleanup job

Cleanup must not delete current raw cursors, current cycle baseline, current traffic policy, or current quota state.

## Backup And Restore

Backups should include traffic policies and calibration baselines because they affect current quota behavior.

Historical traffic buckets can be high volume. Backup/restore excludes traffic history by default and documents that traffic history is operational data. A later export feature can add full history export without changing the quota-critical backup path.

## Compatibility

Existing latest-stats endpoints should keep working during the transition. The new historical module can ingest the same heartbeat stats payload and should not require an immediate agent protocol rewrite beyond the already planned traffic block fields.

Older agents that do not report per-rule/per-listener maps still contribute aggregate node/http/l4/relay buckets. Missing object scopes are treated as empty.

If an older agent reports traffic while the global traffic module is disabled, the control plane ignores the stats payload. This keeps the switch effective during rolling upgrades.

Rolling upgrades should preserve:

- aggregate latest stats display
- node settings snapshots
- heartbeat compatibility
- explicit disabled-stats clearing semantics

## Testing

Storage tests:

- Verify `NRE_TRAFFIC_STATS_ENABLED=false` skips traffic-history migrations.
- Open the shared GORM store with SQLite for fast unit coverage.
- Verify migrations for traffic policies, cursors, baselines, and buckets.
- Verify dialect-aware upsert helpers through integration tests for PostgreSQL and MySQL when those services are available.

Ingestion tests:

- Disabled global module ignores heartbeat traffic stats.
- Repeated identical heartbeat does not double count.
- Increasing cumulative counters produce correct deltas.
- Counter reset produces non-negative deltas and records an event.
- Omitted stats preserve persisted cursor and summary state.
- Explicit empty stats clears latest display state without deleting historical buckets.

Accounting tests:

- `rx`, `tx`, `both`, and `max` compute expected usage.
- Cycle start day computes correct monthly windows.
- Quota and remaining bytes use calibrated cycle usage.
- Setting current used value produces the requested displayed usage.
- Starting from now as zero resets current cycle usage without deleting history.

Blocking tests:

- Disabled global module sends `traffic_blocked=false` even when stored policy/quota would otherwise block.
- Policy update triggers block state recomputation.
- Over-quota with blocking disabled does not block.
- Over-quota with blocking enabled sends `traffic_blocked` to the agent.
- Agent HTTP returns `429` while blocked.
- Agent L4 and Relay reject new traffic while blocked.

Frontend tests:

- Disabled global module hides Traffic tab and traffic-derived fields.
- Node Traffic tab renders summary, trend chart, policy form, calibration controls, and cleanup action.
- Trend granularity switch requests the correct API.
- Rule/listener breakdowns display accounted usage according to node policy.
- Quota labels and percentages update when policy changes.

Verification commands:

```sh
cd panel/backend-go && go test ./...
cd go-agent && go test ./...
cd panel/frontend && npm run build
docker build -t nginx-reverse-emby .
```

The known local macOS relay bind limitation may require running relay bind tests in a Linux environment or using targeted agent tests locally.
