# HTTP Multi-IP Backend Selection Design

## Context

The current HTTP rule pipeline expands a hostname backend into multiple resolved `IP:port` candidates internally, but it does not model those candidates consistently across diagnostics and live proxying:

- HTTP diagnostics probe every resolved `IP:port`, but group the samples under the original backend URL string. A backend like `https://echo.hoppscotch.io` can therefore produce many probe attempts while the UI only shows one backend card.
- The live proxy also expands hostname backends into multiple `IP:port` candidates, but request ordering is effectively determined by the configured backend order plus DNS resolution order. There is no preference for the candidate that has recently delivered lower latency or higher success.

This produces two user-facing problems:

1. Diagnostics overcount effective backends for hostname targets with multiple IPs.
2. Live traffic may continue hitting a weaker IP even after the process has observed a better one.

## Goals

- Make HTTP diagnostics represent each resolved `IP:port` candidate explicitly.
- Keep the original backend URL visible so users can still identify the configured backend entry.
- Improve live proxy selection for hostname backends that resolve to multiple IPs.
- Reuse existing runtime signals from real traffic and diagnostics instead of introducing a background health-check scheduler.
- Preserve current retry and failure backoff behavior.

## Non-Goals

- No active health-check daemon or periodic probing loop.
- No change to the configured rule schema.
- No UI redesign beyond showing the more accurate backend grouping already supported by the diagnostic modal.
- No change to L4/TCP behavior in this work.

## Current Root Cause

### Diagnostics

`go-agent/internal/diagnostics/http.go` creates one `httpProbeCandidate` per resolved `IP:port`, but the candidate `backendLabel` is the original backend URL string. When a hostname resolves to multiple IPs:

- probe attempts multiply by resolved candidate count;
- summaries and backend cards aggregate by URL string;
- distinct IP candidates are invisible in the report.

### Proxy Selection

`go-agent/internal/proxy/server.go` also expands each configured backend into resolved `IP:port` candidates. Those candidates already participate in failure backoff keyed by the actual `IP:port`, but there is no positive scoring that prefers the best-performing resolved candidate. As a result:

- cold-start ordering follows backend ordering and resolver ordering;
- successful but slow candidates can continue receiving traffic even after faster candidates have been observed;
- multi-IP hostname behavior is not meaningfully optimized beyond avoiding temporarily failed addresses.

## Proposed Design

### 1. Represent resolved HTTP candidates explicitly

For HTTP diagnostics and live proxy selection, treat each resolved `IP:port` as an explicit candidate derived from a configured backend.

Each runtime candidate should carry:

- the configured backend URL;
- the resolved dial address (`IP:port`);
- the backend authority/host information needed for upstream HTTP/TLS behavior;
- a stable display label for diagnostics that distinguishes candidates from the same hostname backend.

Recommended diagnostic label shape:

- `https://echo.hoppscotch.io [104.21.32.1:443]`

This keeps the configured backend visible while making the actual probed endpoint explicit.

### 2. Fix HTTP diagnostics grouping

Update HTTP diagnostics so backend aggregation keys use the resolved-candidate display label instead of only the configured backend URL.

Expected result:

- a hostname backend that resolves to four IPs will produce four backend cards;
- probe attempt counts will still reflect actual attempts;
- backend summaries will align with what was truly probed.

No change is needed in the frontend modal contract because it already renders multiple backend summaries and raw samples from backend response data.

### 3. Add lightweight per-candidate performance memory

Introduce lightweight runtime stats for actual `IP:port` candidates. This state should live alongside the existing backend cache because it is already keyed by actual resolved address and already stores backoff state.

For each `IP:port` candidate, store:

- recent success count;
- recent failure count;
- most recent successful latency;
- timestamp of last update.

This is intentionally lightweight. It is not a full rolling metrics system.

### 4. Prefer the best observed candidate during proxy selection

When the proxy builds HTTP candidates for a route, keep the current configured load-balancing order as the base ordering, then apply candidate preference within the resolved candidate list using observed runtime state.

Recommended ordering rules:

1. Candidates currently in failure backoff remain excluded as today.
2. Candidates with successful recent observations are preferred over candidates with only failures or no history.
3. Among successful candidates, lower recent latency ranks higher.
4. If two candidates have no meaningful performance difference, preserve the current load-balancing order.

This yields a "prefer best known candidate, but do not destroy configured balancing semantics" model:

- `round_robin` still rotates across configured backend entries;
- inside a single hostname backend, the route prefers the better observed IP;
- `random` still randomizes configured backend entry order, then prefers better resolved candidates within each chosen backend.

### 5. Feed runtime stats from real traffic and diagnostics

Update candidate stats from:

- successful live proxy round trips;
- failed live proxy round trips;
- successful HTTP diagnostic probes;
- failed HTTP diagnostic probes.

This lets manual diagnostics immediately improve future routing decisions without adding a separate health subsystem.

## Detailed Behavior

### Diagnostics Example

Configured backend:

- `https://echo.hoppscotch.io`

Resolved IPs:

- `104.21.32.1:443`
- `172.67.10.2:443`

If attempts are `5`, the report may contain `10` total samples under the current implementation. After this change, it will still contain `10` samples, but backend summaries will be split into:

- `https://echo.hoppscotch.io [104.21.32.1:443]`
- `https://echo.hoppscotch.io [172.67.10.2:443]`

That makes the multiplied attempts understandable instead of misleading.

### Proxy Example

Configured backend:

- `https://media.example.test`

Resolved candidates:

- `1.1.1.1:443` with recent success latency `45ms`
- `1.1.1.2:443` with recent success latency `210ms`

The proxy should prefer `1.1.1.1:443` until:

- it starts failing and enters backoff;
- or newer observations indicate `1.1.1.2:443` is now better.

This is adaptive preference, not permanent pinning.

## Data Model Changes

No external API or persisted rule schema changes are required.

Internal candidate structs should be extended to expose both:

- configured backend identity;
- resolved candidate identity.

The backend cache area should gain ephemeral candidate performance stats keyed by actual resolved address.

These stats are process-local and reset on agent restart.

## Error Handling

- Resolver cancellation should continue propagating as today.
- Resolver failures for one hostname should continue allowing other configured backends to participate.
- A candidate that times out or errors should still enter existing failure backoff.
- Candidate score updates must not panic or block request flow; missing stats should simply mean "no preference".

## Testing Strategy

### Diagnostics Tests

- Add a failing test showing that one hostname resolving to multiple IPs produces multiple backend summaries instead of one aggregated summary.
- Verify the backend labels include both configured backend URL and resolved `IP:port`.

### Proxy Tests

- Add a failing test showing that a multi-IP hostname backend prefers the lower-latency candidate after observations exist.
- Add a test proving failure backoff still overrides performance preference.
- Add a test proving candidates with no history still respect base load-balancing order.

### Cache/State Tests

- Add focused tests for candidate performance stat recording and ordering behavior.
- Verify stats are keyed by actual resolved address, not only hostname.

## Implementation Slices

1. Extend resolved HTTP candidate representation to distinguish configured backend from resolved `IP:port`.
2. Fix HTTP diagnostics grouping and labels.
3. Add ephemeral candidate performance tracking in the backend cache/runtime selection layer.
4. Apply candidate preference ordering in HTTP proxy selection.
5. Add regression tests for multi-IP diagnostics and proxy preference behavior.

## Risks

- Over-preferring one IP could reduce effective balancing if scoring is too aggressive.
- Mixing configured backend balancing with per-IP preference can become confusing if the ordering rules are not explicit in code.
- If diagnostic traffic updates runtime preference too strongly, a manual probe could temporarily bias production routing more than expected.

## Mitigations

- Keep the scoring model simple and deterministic.
- Use current configured load-balancing order as the base order, then only refine candidate order within resolved candidates.
- Keep stats ephemeral and lightweight rather than introducing persistent or long-lived affinity.

## Recommendation

Implement the lightweight passive-preference approach:

- expose resolved `IP:port` candidates clearly in diagnostics;
- record candidate results from real traffic and diagnostics;
- prefer the best recently observed candidate during live proxying;
- keep the existing backoff and retry structure intact.

This directly addresses the current user-visible defects without introducing a new active health-check subsystem.
