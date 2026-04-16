# Adaptive Load Balancing Cold-Start And Recovery Design

## Context

The current `adaptive` strategy already ranks HTTP and L4 backends with:

- recent 24-hour stability;
- smoothed latency;
- estimated bandwidth;
- a combined performance score;
- backend-level and resolved `IP:port` diagnostics.

That is enough for steady-state preference, but it still has a weak point during cold start and post-failure recovery:

- new or recently recovered candidates may not receive enough real traffic to relearn quickly;
- one unusually large transfer sample can still influence bandwidth preference more than desired;
- recovered candidates can move from backoff to normal ranking too abruptly;
- diagnostics expose the main factors, but not the recovery state behind the decision.

The requested direction is to improve relearning quality and speed without adding expensive active probing, and to move the scoring model closer to industry patterns such as EWMA, slow start, and passive outlier detection.

## Goals

- Improve cold-start relearning for new backend candidates and newly resolved DNS/IP candidates.
- Improve post-recovery relearning after backoff expiry.
- Keep adaptive selection primarily passive and based on live traffic.
- Reduce the impact of single transfer samples, especially bandwidth spikes.
- Move scoring closer to `EWMA + slow-start + outlier-detection`.
- Keep the existing product contract:
  - default new strategy remains `adaptive`;
  - explicit legacy `round_robin` and `random` remain unchanged;
  - diagnostics continue to show stability, latency, bandwidth, and combined performance.

## Non-Goals

- No background synthetic bandwidth benchmark loop.
- No high-frequency active probe daemon.
- No new user-facing load-balancing strategy value.
- No change to the meaning of existing legacy strategies.
- No persistent long-term historical metrics store outside the in-process runtime cache.

## Product Decisions

- The enhancement applies only to the internal behavior of `adaptive`.
- HTTP and L4 both use the same adaptive state model.
- Both selection layers use the same model:
  - configured backend layer;
  - resolved DNS/IP candidate layer.
- Diagnostics will expose candidate state so operators can see whether a candidate is:
  - `cold`;
  - `recovering`;
  - `warm`;
  - degraded as an `outlier`.

## Problems To Solve

### Cold Start

A candidate with no recent successful observations is under-informed. If the runtime always prefers already-known winners, the cold candidate never receives enough traffic to build a useful score.

### Recovery

After a candidate exits backoff, it needs real traffic to prove recovery. If it is restored directly to full competition, one success can over-promote it; if it is ranked strictly by stale confidence, it may never recover.

### Sample Noise

Latency and bandwidth are both useful, but bandwidth is especially noisy:

- one large download can produce a much higher instantaneous estimate;
- a single sample should not immediately reorder candidates;
- a candidate should need repeated real success before its throughput advantage meaningfully changes ranking.

## Proposed Model

### Shared Candidate State

Each observed candidate, at both backend scope and resolved-address scope, has a derived state:

- `cold`
  - recent 24-hour sample count is below the minimum confidence threshold;
  - or there are no recent successful observations.
- `recovering`
  - the candidate previously entered failure backoff;
  - backoff has expired;
  - the candidate is in a bounded recovery window and is re-learning under controlled traffic.
- `warm`
  - the candidate has enough recent observations;
  - it is not in backoff;
  - it is not currently in the recovery window.
- `outlier`
  - this is a degradation marker, not a standalone traffic state;
  - a candidate may be `warm` or `recovering` and also marked as a recent outlier.

### Controlled Exploration

Adaptive selection should not be fully greedy.

When `cold` or `recovering` candidates exist and are not in active backoff:

- reserve a small bounded portion of request opportunities for relearning;
- distribute that opportunity without overwhelming the currently healthy leader;
- keep the majority of requests on the best `warm` candidate.

Recommended initial budgets:

- `cold` candidates share up to `10%` of opportunities;
- `recovering` candidates share up to `15%` of opportunities;
- combined exploration budget is capped at `20%`.

If no `warm` candidates exist, adaptive selection self-bootstraps by choosing among `cold` and `recovering` candidates using the same smoothed performance model.

### Recovery Slow Start

Candidates do not return to full effective weight immediately after recovery or cold-start graduation.

When a candidate transitions from:

- `cold -> warm`; or
- `recovering -> warm`

it enters a `slow_start` window.

During slow start:

- its effective performance score is multiplied by a ramp factor;
- the factor starts below full weight and increases linearly over time.

Recommended defaults:

- backend layer slow start: `60s`;
- resolved DNS/IP layer slow start: `30s`;
- initial slow-start factor: `0.30`;
- final factor at window completion: `1.00`.

This allows good candidates to ramp in quickly while preventing one recovery success from immediately absorbing most traffic.

## Scoring Model

### Observation Window

Stability remains limited to the most recent 24 hours.

All state transitions and confidence calculations use only recent observations.

### EWMA Metrics

Each candidate maintains smoothed estimates:

- `header_latency_ewma`
  - response-header latency for HTTP;
  - connect or first-success latency for L4.
- `transfer_bandwidth_ewma`
  - derived from transferred bytes over successful transfer duration.

EWMA remains the primary smoothing mechanism for runtime learning.

### Confidence-Weighted Performance

The ranking model should avoid letting one transfer sample dominate ordering.

Add `sample_confidence`, derived from recent successful sample count:

- one sample gives only low confidence;
- confidence rises gradually as recent successful samples accumulate;
- after several successful samples, confidence approaches full weight.

Recommended behavior:

- fewer than `3` recent successful samples => confidence is materially reduced;
- `3-5` recent successful samples => confidence ramps toward `1`;
- larger sample counts saturate rather than growing unbounded.

Raw performance is calculated from the two smoothed metrics:

- `latency_component`
  - lower latency improves score with diminishing returns;
- `bandwidth_component`
  - higher bandwidth improves score after logarithmic compression.

Recommended raw combination:

- `raw_performance = 0.55 * latency_component + 0.45 * bandwidth_component`

Recommended effective combination:

- `effective_performance = raw_performance * sample_confidence * slow_start_factor * outlier_penalty`

Notes:

- latency is weighted slightly above bandwidth to avoid bandwidth spikes overpowering the rank;
- bandwidth still matters materially and can win over a lower-latency competitor when repeatedly observed;
- logarithmic compression plus confidence weighting prevents a one-off large response from taking over the order.

### Stability And Ranking Order

Within the normal path, candidate ranking remains:

1. active backoff exclusion;
2. stability;
3. effective performance;
4. configured order tie-break.

This preserves the product requirement that stability remains the primary dimension.

## Passive Outlier Detection

This design intentionally avoids a heavy active health-check loop.

Instead, add passive outlier detection based on live traffic symptoms:

- repeated consecutive failures;
- recent stability falling below a threshold after a minimum sample count;
- latency EWMA degrading materially versus the candidate's recent baseline;
- bandwidth EWMA collapsing materially versus recent normal behavior.

Outlier handling is staged:

1. first degrade effective performance through an `outlier_penalty`;
2. if failures continue, re-enter failure backoff and normal retry suppression;
3. once backoff expires, return through `recovering`.

This avoids overreacting to a single transient blip while still converging on unhealthy candidates quickly.

## Runtime Selection Behavior

### Selection Flow

For `adaptive` at both backend and resolved-candidate layers:

1. Exclude candidates still in active backoff.
2. Partition remaining candidates into:
   - `warm`
   - `recovering`
   - `cold`
3. Apply passive outlier penalty where relevant.
4. Decide whether the request falls into:
   - recovery exploration budget;
   - cold exploration budget;
   - normal warm path.
5. If exploration is selected:
   - choose from the target exploration pool using effective performance and configured order.
6. Otherwise:
   - choose from `warm` candidates using normal adaptive ordering.
7. If no `warm` candidates exist:
   - choose from `recovering`, then `cold`, using the same smoothed model.

### State Transition Rules

Recommended thresholds:

- `cold`
  - recent sample count `< 3`;
  - or no recent success.
- `recovering`
  - candidate had prior backoff;
  - backoff has expired;
  - recovery ends when:
    - consecutive successes `>= 2` and recent sample count `>= 3`; or
    - recovery window exceeds `2 minutes`.
- `warm`
  - recent sample count `>= 3`;
  - not in active backoff;
  - not in recovery.

Failure during recovery returns the candidate immediately to normal failure backoff.

## Diagnostics

Diagnostics must expose the adaptive recovery model directly.

In addition to the existing fields:

- `stability`
- `latency`
- `estimated_bandwidth`
- `performance_score`

add:

- `state`
  - `cold`, `recovering`, or `warm`
- `sample_confidence`
- `slow_start_active`
- `outlier`
- `traffic_share_hint`
  - indicates whether the candidate is currently preferred in normal traffic, in exploration, or in slow-start recovery.

This helps explain why a candidate may not be first even if one metric looks good.

## Implementation Outline

### Shared Adaptive Engine

Primary implementation stays in:

- `go-agent/internal/backends/cache.go`

Enhancements:

- extend observation state with:
  - recent success confidence inputs;
  - recovery timestamps and recovery-success counters;
  - slow-start ramp timing;
  - outlier markers or penalty state;
- compute:
  - state classification;
  - sample confidence;
  - effective performance;
  - exploration eligibility;
- expose shared preference summaries for runtime and diagnostics.

### HTTP Runtime

Update:

- `go-agent/internal/proxy/server.go`

Behavior:

- use the shared adaptive engine to choose backend-level and resolved-address candidates;
- feed success and failure outcomes into the shared state transitions;
- preserve legacy strategy behavior for non-adaptive rules.

### L4 Runtime

Update:

- `go-agent/internal/l4/server.go`

Behavior mirrors HTTP:

- backend-level adaptive choice;
- resolved DNS/IP adaptive choice;
- passive learning from TCP and UDP traffic;
- recovery and outlier behavior driven from live traffic only.

### Diagnostics Serialization

Update:

- `go-agent/internal/diagnostics/http.go`
- `go-agent/internal/diagnostics/l4tcp.go`
- `go-agent/internal/task/diagnostics.go`

Add the new adaptive state and explanation fields while keeping old fields compatible.

### Frontend

Update:

- `panel/frontend/src/components/RuleDiagnosticModal.vue`

Show the existing three factors plus:

- state;
- sample confidence;
- slow-start status;
- outlier status;
- traffic-share hint.

Rendering remains conditional so older payloads continue to work.

## Compatibility

- No new strategy name is introduced.
- New rules still default to `adaptive`.
- Existing stored `round_robin` and `random` remain unchanged.
- Existing adaptive rules automatically benefit from the improved runtime behavior.
- Diagnostics payloads remain backwards-compatible by making new fields optional.

## Testing Strategy

### Backend Cache

- cold candidates receive bounded exploration opportunities;
- recovering candidates re-enter traffic gradually after backoff expiry;
- single high-bandwidth samples do not immediately dominate ranking;
- repeated successful samples increase confidence and eventually ranking strength;
- slow-start factor ramps from partial to full effect;
- outlier penalty demotes degraded candidates before hard ejection.

### HTTP

- adaptive backend selection uses controlled exploration for cold backends;
- adaptive DNS/IP selection uses controlled exploration for cold resolved candidates;
- recovery path relearns after live-traffic success;
- diagnostics show state, confidence, slow-start, and outlier fields.

### L4

- TCP and UDP share the same state transitions and exploration policy;
- backend and resolved-address layers both honor recovery and slow start;
- passive learning only, no synthetic probe dependency.

### Frontend

- diagnostic modal renders the new adaptive explanation fields when present;
- existing payloads without new fields still render correctly.

## Risks

- Too much exploration can hurt steady-state performance.
- Too little exploration will not fix cold-start relearning.
- Outlier thresholds that are too aggressive can create oscillation.
- Recovery windows that are too short can re-promote unstable candidates too early.

## Mitigations

- Keep exploration budgets low and capped.
- Keep stability as the primary ranking dimension.
- Use EWMA smoothing, confidence weighting, and log-compressed bandwidth.
- Use slow start for both cold-start graduation and failure recovery.
- Stage outlier handling as degrade first, eject later.

## Recommendation

Implement the next adaptive improvement as:

- controlled exploration for `cold` and `recovering` candidates;
- EWMA-based latency and bandwidth scoring with confidence weighting;
- slow start after cold-start graduation and backoff recovery;
- passive outlier detection with penalty-before-ejection behavior;
- diagnostics that expose candidate state and recovery reasoning.

This directly addresses the relearning problem without adding a costly active-probing subsystem, and it moves the adaptive strategy closer to established patterns used by mature proxies and service meshes.
