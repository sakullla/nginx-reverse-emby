# Traffic Stats Reporting Design

## Goal

Show persisted traffic statistics on each HTTP rule, L4 rule, and Relay listener while keeping the first implementation on the existing latest-stats storage model.

The node should also support a configurable traffic stats reporting interval. This interval controls when the agent includes traffic stats in heartbeat payloads and therefore when the control plane persists a new latest snapshot.

## Scope

In scope:

- Extend agent traffic counters from aggregate buckets to per-object buckets.
- Track cumulative `rx_bytes` and `tx_bytes` for:
  - total traffic
  - aggregate HTTP traffic
  - aggregate L4 traffic
  - aggregate Relay listener traffic
  - each HTTP rule ID
  - each L4 rule ID
  - each Relay listener ID
- Persist the latest reported stats JSON through the existing control-plane paths.
- Add a node-level traffic stats reporting interval to agent configuration.
- Surface stats in the panel for HTTP rules, L4 rules, and Relay listeners.
- Preserve compatibility with older agents and older stored stats payloads.

Out of scope for this implementation:

- Time-series buckets.
- Trend charts based on historical buckets.
- Retention policies.
- Database schema for historical metrics.
- Resetting counters on a configured period.
- Differentiating unique business traffic from layer-by-layer relay traffic.

## Data Shape

The agent reports a cumulative latest snapshot. A representative payload is:

```json
{
  "traffic": {
    "total": { "rx_bytes": 1024, "tx_bytes": 2048 },
    "http": { "rx_bytes": 100, "tx_bytes": 200 },
    "l4": { "rx_bytes": 300, "tx_bytes": 400 },
    "relay": { "rx_bytes": 624, "tx_bytes": 1448 },
    "http_rules": {
      "11": { "rx_bytes": 100, "tx_bytes": 200 }
    },
    "l4_rules": {
      "21": { "rx_bytes": 300, "tx_bytes": 400 }
    },
    "relay_listeners": {
      "31": { "rx_bytes": 624, "tx_bytes": 1448 }
    }
  }
}
```

IDs are encoded as strings in JSON maps to avoid ambiguity across JavaScript and Go map handling.

The existing aggregate keys remain present so current panel views and older code paths continue to work.

## Counting Semantics

`rx_bytes` means bytes received by the agent-side listener from the client-facing side of that component.

`tx_bytes` means bytes written back toward that client-facing side.

For HTTP rules, request body bytes count as `rx_bytes` when the upstream request body is read. Response body bytes count as `tx_bytes` when copied to the downstream response writer.

For L4 rules, bytes from the accepted client connection to the selected upstream count as `rx_bytes`. Bytes from upstream back to the accepted client count as `tx_bytes`.

For Relay listeners, bytes entering the Relay listener from a relay client count as `rx_bytes`. Bytes sent back toward that relay client count as `tx_bytes`. Relay traffic is a layer-level measurement and can overlap with HTTP or L4 traffic seen on another node.

## Agent Runtime

The `traffic` package should provide recorders that can be scoped to an object:

- HTTP rule recorder keyed by rule ID.
- L4 rule recorder keyed by rule ID.
- Relay listener recorder keyed by listener ID.

Existing aggregate recorder APIs can remain for compatibility, but rule/listener execution paths should use scoped recorders where the model object is available.

Counters remain in memory and cumulative for the current agent process. The first implementation does not attempt to restore counters after agent restart. The control plane still preserves the latest reported snapshot, so the panel has the last known value even if the node is offline.

When traffic stats are disabled, the agent should continue sending an explicit empty stats payload so the control plane clears previously reported stats.

## Reporting Interval

Add `traffic_stats_interval` to `agent_config`.

The interval controls whether `syncRequest` includes traffic stats:

- If traffic stats are disabled, include explicit empty stats immediately, as today.
- If no interval is configured, preserve current behavior: include non-zero traffic stats on heartbeat.
- If an interval is configured, include stats when the interval has elapsed since the last stats report.
- The interval must be positive when present.

This setting does not change the heartbeat interval. Heartbeat remains responsible for config synchronization and liveness.

The agent should persist enough local runtime metadata to avoid sending stats on every restart loop when a reporting interval is configured. It does not need historical buckets.

## Control Plane

Agent records already persist latest remote stats in `agents.last_reported_stats`. Embedded local agent stats already flow through local runtime metadata. Those storage paths remain authoritative for this implementation.

The control plane should store and expose the node traffic stats interval with other agent settings. It should include the value in snapshots under `agent_config` so remote and embedded agents receive the same shape.

The stats API should return the latest stats payload unchanged except for existing fallback status behavior. Missing per-object maps should be treated as empty by callers.

No metrics table is added in this phase.

## Frontend

The panel should show latest cumulative traffic stats in:

- HTTP rule list and rule detail.
- L4 rule list and rule detail.
- Relay listener list.
- Agent detail aggregate summary.

When stats are missing, the UI should render `0 B` for both directions. It should not display deleted-rule or deleted-listener orphan stats because the display should be driven by current configured objects.

Agent settings should expose a traffic stats interval control. The control should make clear through labels that this is a reporting interval, not a reset period.

## Compatibility

Older agents may only report aggregate `traffic.http`, `traffic.l4`, and `traffic.relay`. The frontend and backend must tolerate missing `http_rules`, `l4_rules`, and `relay_listeners`.

Older stored snapshots may omit `agent_config.traffic_stats_interval`; agents should treat that as the default current behavior.

New agents should continue to include aggregate buckets so older panel code remains usable during rolling upgrades.

## Testing

Agent tests:

- Scoped HTTP rule traffic increments both aggregate HTTP and `http_rules[id]`.
- Scoped L4 traffic increments both aggregate L4 and `l4_rules[id]`.
- Scoped Relay traffic increments both aggregate Relay and `relay_listeners[id]`.
- Disabled traffic stats clear previously reported stats.
- Configured reporting interval suppresses stats until elapsed and then includes a snapshot.
- Missing reporting interval preserves current heartbeat stats behavior.

Control-plane tests:

- Agent traffic stats interval persists and appears in snapshots.
- Heartbeat still persists latest stats JSON with per-object maps.
- Stats endpoints tolerate older aggregate-only stats payloads.

Frontend tests:

- HTTP rules render per-rule traffic from `traffic.http_rules`.
- L4 rules render per-rule traffic from `traffic.l4_rules`.
- Relay listeners render per-listener traffic from `traffic.relay_listeners`.
- Missing per-object stats render as zero.
- Agent settings payload includes the traffic stats interval.
