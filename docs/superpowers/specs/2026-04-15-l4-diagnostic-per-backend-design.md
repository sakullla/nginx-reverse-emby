# L4 Diagnostic Per-Backend Sampling Design

## Goal

Make L4 diagnostic behavior match the updated HTTP diagnostic behavior for multi-backend rules:

- each configured backend is probed a fixed number of times
- diagnostic output includes both overall rule summary and per-backend summaries
- the existing frontend diagnostic modal can show per-backend latency cards for L4 rules without a separate UI path

## Scope

This change applies only to L4 TCP diagnostics:

- Go execution-plane L4 diagnostic probing
- diagnostic result serialization from agent task handler
- existing frontend modal data consumption for L4 tasks

This change does not alter request routing, load-balancing behavior, or runtime proxy health logic outside diagnostics.

## Current Problem

The current L4 diagnostic implementation sends `Attempts` probes total and rotates across available backends. For rules with multiple backends, this means:

- some backends may receive fewer than 5 probes
- summary data is less comparable across backends
- the frontend cannot reliably show per-backend latency accuracy

This is inconsistent with the desired diagnostic semantics and now inconsistent with HTTP diagnostics.

## Design

### Probe Strategy

For L4 diagnostics, `Attempts` becomes attempts per backend, not total attempts across the rule.

If a rule has 3 backends and `Attempts` is 5:

- backend A is probed 5 times
- backend B is probed 5 times
- backend C is probed 5 times
- overall sample count becomes 15

The probe order should be deterministic over the resolved candidate list:

1. resolve configured backends into candidate addresses
2. iterate candidates in the resolved order
3. for each candidate, execute `Attempts` dial probes

This keeps output predictable and aligns with the HTTP rule diagnostic change.

### Result Shape

L4 diagnostics should reuse the shared `diagnostics.Report` structure already used by HTTP diagnostics:

- `summary`: overall rule-level summary across all L4 samples
- `backends`: per-backend summary entries
- `samples`: raw probe attempts

Each backend entry should include:

- `backend`
- `summary.sent`
- `summary.succeeded`
- `summary.failed`
- `summary.loss_rate`
- `summary.avg_latency_ms`
- `summary.min_latency_ms`
- `summary.max_latency_ms`
- `summary.quality`

For L4, `backend` should remain the resolved `host:port` string used in each sample.

### Frontend Rendering

No new modal or L4-specific diagnostic component is required.

The existing `RuleDiagnosticModal.vue` already reads:

- `result.summary`
- `result.backends`
- `result.samples`

Once L4 task results include `backends`, the existing "后端延迟" section should render automatically for L4 rules as well. The current L4 rules page can remain unchanged except for consuming the updated task payload.

### Error Handling

Existing dial failure handling remains unchanged:

- failed connection attempts produce failed samples
- backend cache failure/success marking remains in place
- if no healthy candidates are available after resolution, the diagnostic still returns an error

Per-backend summaries must handle all-failed backends correctly by reporting:

- `succeeded = 0`
- `quality = 不可用`
- latency metrics left at `0`

### Testing

Add or update tests to cover:

- multi-backend L4 diagnostics produce `Attempts x backendCount` samples
- each backend receives exactly 5 probes when `Attempts = 5`
- per-backend summaries are populated in the report
- task serialization includes `backends` for L4 diagnostic task results
- existing single-backend L4 diagnostic behavior still passes with the new semantics

## Implementation Notes

- Reuse the shared report aggregation already added for HTTP diagnostics rather than introducing L4-specific result types.
- Keep the TCP dial logic unchanged except for looping semantics and backend labeling.
- Do not add extra UI state or frontend branching unless a real rendering gap appears.

## Risks

- Test expectations that currently assume `Sent == Attempts` for multi-backend L4 diagnostics will need updating.
- Because total samples now scale with backend count, very large backend lists will produce proportionally more diagnostic traffic. This is acceptable for the current diagnostic use case.

## Success Criteria

The change is complete when:

- a multi-backend L4 rule produces 5 samples per backend
- the returned task result includes per-backend summaries
- the existing diagnostic modal shows per-backend latency cards for L4 rules
- Go tests and frontend build pass
