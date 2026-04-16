# Adaptive HTTP And L4 Load Balancing Design

## Context

The current work already improves HTTP hostname backends that resolve to multiple IPs:

- diagnostics can show resolved `IP:port` candidates explicitly;
- proxy traffic can learn from passive observations;
- resolved candidates can be ranked by recent runtime observations.

That is still narrower than the desired product behavior:

- the adaptive preference only exists for HTTP resolved IP selection, not for full backend selection;
- L4 rules still use only `round_robin` and `random`;
- control-plane defaults still normalize to `round_robin`;
- the frontend diagnostics UI does not expose the three evaluation factors behind the ranking;
- latency and bandwidth are currently visible only as separate implementation details, not as a combined performance judgment.

The requested target behavior is broader:

- add a new `load_balancing.strategy = "adaptive"` option;
- make `adaptive` the default for new HTTP and L4 rules;
- keep existing saved `round_robin` and `random` rules unchanged;
- apply adaptive ranking at both backend level and DNS/IP level;
- expose the ranking factors directly in the diagnostics UI;
- evaluate latency and bandwidth together so a low-latency but low-throughput candidate is not incorrectly preferred.

## Goals

- Add `adaptive` as a first-class load-balancing strategy for both HTTP and L4 rules.
- Make `adaptive` the default strategy for newly created HTTP and L4 rules.
- Keep explicit legacy strategies (`round_robin`, `random`) available and behaviorally stable.
- Extend adaptive selection from DNS/IP preference to full backend selection.
- Apply the same adaptive model to HTTP, TCP, and UDP traffic with protocol-appropriate observations.
- Limit stability scoring to the most recent 24 hours of observations.
- Expose stability, latency, estimated bandwidth, and combined performance in diagnostics.

## Non-Goals

- No automatic migration that rewrites existing saved legacy strategies.
- No active bandwidth benchmark or synthetic throughput test daemon.
- No persistent long-term historical metrics store beyond existing process-local runtime observations.
- No changes to non-HTTP/L4 product areas unrelated to load balancing.

## Product Decisions

- New strategy name: `adaptive`
- Default strategy for new HTTP rules: `adaptive`
- Default strategy for new L4 rules: `adaptive`
- Existing rules already saved as `round_robin` or `random` remain unchanged until manually edited.
- Diagnostics UI must show the three evaluation factors.
- Latency and bandwidth must be evaluated together as a combined performance score.

## Proposed Model

### Strategy Set

Supported strategy values become:

- `adaptive`
- `round_robin`
- `random`

Normalization rules:

- empty strategy => `adaptive`
- invalid strategy => `adaptive`
- explicit `round_robin` => preserve
- explicit `random` => preserve
- explicit `adaptive` => preserve

This applies to control-plane services, storage snapshot parsing, local runtime projection, and agent model decoding.

### Two-Level Adaptive Selection

Adaptive ranking is applied in two layers.

Layer 1: backend preference

- For a rule with multiple configured backends, rank the backends against each other.
- The backend score is derived from observations aggregated across that backend's effective traffic.

Layer 2: resolved candidate preference

- Within each backend, rank its resolved `IP:port` candidates.
- This uses per-resolved-address observations, as already introduced for HTTP multi-IP selection.

Selection order under `adaptive`:

1. Exclude candidates in active failure backoff.
2. Prefer higher stability.
3. Among similarly stable candidates, prefer higher combined performance.
4. Preserve configured order as the final tie-breaker.

This preserves operator intent while still adapting to runtime conditions.

## Scoring Model

### Stability

Stability is evaluated only from the most recent 24 hours.

Inputs:

- recent success count
- recent failure count

Properties:

- old successes and failures outside the 24-hour window do not affect ranking;
- repeated historical wins cannot permanently pin a candidate;
- candidates with recent failures are penalized even if they had earlier success.

Implementation expectation:

- use a sliding 24-hour window with lightweight hourly buckets or equivalent bounded aging;
- apply the same model to backend-level and resolved-candidate-level observations.

### Performance

Performance is a combined score built from latency and estimated bandwidth.

Inputs:

- latency estimate
- estimated bandwidth

Required behavior:

- lower latency improves score;
- higher bandwidth improves score;
- bandwidth can materially offset a latency advantage;
- a very low-latency but very low-throughput candidate must not automatically win.

This is intentionally not a hard lexicographic comparison such as "latency first, then bandwidth". It is a combined performance judgment.

Implementation expectation:

- normalize latency and bandwidth into comparable dimensions;
- compute a single performance score;
- use stable thresholds or smoothing so brief spikes do not cause excessive oscillation.

### Overall Ranking

The effective adaptive score is:

- first dimension: stability
- second dimension: combined performance
- final dimension: original backend order

Backoff remains a hard exclusion before scoring.

## Observation Sources

### HTTP

Backend-level and resolved-candidate-level observations are both updated from:

- successful live proxy traffic;
- failed live proxy traffic;
- successful diagnostics traffic;
- failed diagnostics traffic.

Metrics:

- stability from recent successes and failures;
- latency from response-header time;
- estimated bandwidth from successful response bytes divided by total response transfer duration.

For hostname backends:

- per-backend aggregates summarize all traffic belonging to the configured backend;
- per-resolved-candidate observations remain keyed by actual `IP:port`.

### L4 TCP

Backend-level and resolved-candidate-level observations are updated from real TCP proxy traffic.

Metrics:

- stability from connection success/failure and retry outcomes;
- latency from connect time or first successful upstream response milestone;
- estimated bandwidth from total relayed bytes over session lifetime.

### L4 UDP

UDP uses a coarser passive model.

Metrics:

- stability from session-level success/failure and reply timeout behavior;
- latency from time to first valid reply;
- estimated bandwidth from bytes transferred over active session duration.

No active synthetic speed test is introduced.

## Backend Aggregation

Adaptive backend ranking needs aggregate observations, not only per-resolved-address observations.

Add a second observation scope in backend cache/runtime memory:

- backend-scope observation keyed by configured backend identity within a rule;
- resolved-candidate observation keyed by actual `IP:port`.

Backend identity requirements:

- HTTP backend identity must distinguish configured backend URL entries within a rule;
- L4 backend identity must distinguish configured `host:port` entries within a rule;
- identity should be scoped to the rule/listener so unrelated rules do not share preference state accidentally.

This gives the runtime enough information to rank:

- which backend to try first;
- which resolved address inside that backend to try first.

## HTTP Runtime Behavior

For `adaptive` HTTP rules:

1. Build configured backend candidates.
2. Rank configured backends by adaptive backend score.
3. For each backend, resolve hostname candidates.
4. Rank resolved `IP:port` candidates by adaptive resolved score.
5. Attempt candidates in that final order.

For explicit legacy strategies:

- `round_robin` preserves current backend rotation semantics;
- `random` preserves current randomized backend selection semantics.

Resolved candidates inside legacy backend strategies may still use the existing resolved-address preference if already implemented for safety and quality, but the main configured backend ordering remains controlled by the selected legacy strategy.

## L4 Runtime Behavior

For `adaptive` L4 rules:

1. Build configured backend candidates.
2. Rank configured backends by adaptive backend score.
3. Resolve hostname backends into `IP:port` candidates.
4. Rank resolved candidates by adaptive resolved score.
5. Attempt connections/sessions in that final order.

This applies to:

- TCP direct upstream dialing;
- TCP relay dialing;
- UDP direct upstream sessions;
- UDP relay sessions.

Legacy strategies continue to behave as they do today unless explicitly changed to `adaptive`.

## Diagnostics Behavior

Diagnostics must expose not only probe outcomes but also adaptive evaluation state.

### Backend Layer

Show each configured backend with:

- recent 24h stability summary;
- latency estimate;
- estimated bandwidth;
- combined performance score;
- whether it is currently the preferred backend.

### DNS/IP Layer

For hostname backends, show each resolved `IP:port` with:

- recent 24h stability summary;
- latency estimate;
- estimated bandwidth;
- combined performance score;
- whether it is currently the preferred resolved candidate.

### Ranking Reason

For preferred items, show a concise reason label, for example:

- `stability higher`
- `performance higher`

Do not show misleading labels such as `latency lower` when bandwidth materially reduced the result.

## Frontend Diagnostics UI

The frontend diagnostics page must surface the adaptive decision factors directly.

Required fields for each backend and resolved candidate:

- `stability`
- `latency`
- `estimated_bandwidth`
- `performance_score`

Required presentation rules:

- clearly label stability as "recent 24h";
- distinguish configured backends from resolved DNS/IP candidates;
- sort display in the same order the runtime prefers;
- mark the currently preferred backend and preferred resolved candidate;
- expose enough detail that the operator can understand why a candidate was preferred or demoted.

Recommended visual model:

- backend row/card summary first;
- expandable resolved-candidate section under each hostname backend;
- compact badges or columns for the three factors and combined performance.

## Control-Plane Compatibility

Affected control-plane areas:

- HTTP rule service normalization
- L4 rule service normalization
- storage snapshot parsing
- local agent runtime projection
- fixture builders and compatibility fixtures
- API response models and tests

Expected compatibility behavior:

- legacy stored `round_robin` or `random` values remain intact;
- omitted strategy for new writes becomes `adaptive`;
- snapshots sent to agents contain `adaptive` by default for newly created rules.

## Testing Strategy

### Control Plane

- HTTP create/update defaults to `adaptive`
- L4 create/update defaults to `adaptive`
- invalid or empty strategies normalize to `adaptive`
- explicit `round_robin` and `random` remain unchanged
- snapshot parsing and compatibility fixtures accept `adaptive`

### HTTP Agent

- adaptive backend ranking across multiple configured backends
- adaptive resolved-candidate ranking inside one backend
- 24-hour stability window aging
- performance score prefers higher-throughput candidate when latency-only preference would be wrong
- diagnostics feed backend and resolved observations

### L4 Agent

- adaptive backend ranking for TCP
- adaptive resolved-candidate ranking for TCP hostname backends
- adaptive backend ranking for UDP
- adaptive resolved-candidate ranking for UDP hostname backends
- stability window and backoff behavior remain correct

### Frontend

- diagnostics page renders the three factors and combined performance
- preferred backend and preferred resolved candidate are visible
- labels reflect backend layer versus DNS/IP layer correctly

## Risks

- Excessive score churn could create oscillation between similar candidates.
- Backend-level aggregation could accidentally blend unrelated traffic if keys are too broad.
- UDP bandwidth estimation may be noisier than HTTP/TCP.
- Diagnostics and runtime could diverge if they read different observation summaries.

## Mitigations

- Keep observations bounded to a 24-hour window.
- Use smoothed latency and bandwidth estimates instead of raw last-sample values.
- Scope backend observation keys per rule/backend identity.
- Reuse one shared observation model for runtime ranking and diagnostics rendering.

## Recommendation

Implement `adaptive` as the new default strategy for HTTP and L4 rules, with:

- backend-level adaptive preference;
- DNS/IP-level adaptive preference;
- 24-hour stability scoring;
- combined latency-plus-bandwidth performance scoring;
- diagnostics UI that exposes all three factors and the final performance score.

This directly matches the requested product behavior while preserving explicit legacy strategies for operators who still want deterministic `round_robin` or `random`.
