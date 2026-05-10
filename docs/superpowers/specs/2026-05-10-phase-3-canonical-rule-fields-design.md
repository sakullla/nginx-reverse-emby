# Phase 3 Canonical Rule Fields Design

Date: 2026-05-10

## Context

Phase 3 of `docs/superpowers/specs/2026-05-09-go-agent-performance-refactor-design.md` removes legacy compatibility paths after the Phase 1 helper extraction and Phase 2 hotspot file splits. The three compatibility paths to remove together are:

- HTTP single-backend fallback: `backend_url`.
- L4 single-backend fallback: `upstream_host` and `upstream_port`.
- Relay linear path fallback: `relay_chain`.

The canonical fields after this change are:

- HTTP rule targets: `backends[]`.
- L4 rule targets: `backends[]`.
- Relay routing: `relay_layers`.

These fields are already present across the agent, control plane, and frontend. The remaining work is to stop treating the old fields as runtime/API input sources and to migrate stored data into the canonical fields.

## Goals

- Make `backends[]` the only runtime and API source for HTTP and L4 backend targets.
- Make `relay_layers` the only runtime and API source for relay paths.
- Keep legacy SQLite columns as migration inputs for one release path, but stop exposing or writing them as active API payload fields.
- Update `go-agent`, `panel/backend-go`, and `panel/frontend` in the same implementation phase.
- Preserve existing stored deployments by migrating old column values into canonical JSON fields during schema bootstrap.

## Non-Goals

- Do not drop SQLite columns in this phase. Dropping columns is a separate storage migration after deployed data has been normalized.
- Do not change load balancing, relay path expansion semantics, proxy-entry behavior, backend health scoring, or traffic accounting.
- Do not tune timeouts, buffer sizes, retry defaults, or relay transport defaults.
- Do not change the shape of `relay_layers` itself.

## Chosen Approach

Use a synchronized canonical-field cutover:

1. Storage migration first normalizes legacy data:
   - HTTP rows with empty `backends` and non-empty `backend_url` are migrated to `backends=[{"url": backend_url}]`.
   - L4 rows with empty `backends` and valid `upstream_host/upstream_port` are migrated to `backends=[{"host": upstream_host, "port": upstream_port}]`.
   - HTTP and L4 rows with empty `relay_layers` and non-empty `relay_chain` are migrated to one relay layer per legacy hop, preserving linear path order as `[[id1], [id2], ...]`.
2. Control-plane services accept only canonical fields for create/update:
   - HTTP input requires `backends`.
   - L4 input requires `backends` except `listen_mode=proxy`, which remains backendless.
   - HTTP and L4 input use `relay_layers`; `relay_chain` is ignored or rejected depending on endpoint compatibility policy in the implementation plan.
3. Agent runtime and diagnostics stop falling back to old fields:
   - HTTP parse/binding/diagnostics read `rule.Backends`.
   - L4 validation/candidates/diagnostics read `rule.Backends`.
   - Relay route resolution reads `rule.RelayLayers`.
4. Frontend forms, runtime API helpers, display helpers, search helpers, tests, and dev mocks stop constructing or relying on `backend_url`, `upstream_host/upstream_port`, and `relay_chain`.

This keeps the breaking surface deliberate while preserving old stored rows through migration.

## Backend Storage And API Design

### Storage

`panel/backend-go/internal/controlplane/storage` keeps legacy DB columns and row struct fields for now:

- `rules.backend_url`
- `l4_rules.upstream_host`
- `l4_rules.upstream_port`
- `rules.relay_chain`
- `l4_rules.relay_chain`

Schema bootstrap adds normalization statements that populate canonical JSON columns from legacy columns when canonical columns are empty. Row-to-snapshot conversion reads only canonical fields after migration.

New writes should store canonical fields and write neutral legacy values:

- HTTP `BackendURL` row field becomes `""`.
- L4 `UpstreamHost` row field becomes `""`.
- L4 `UpstreamPort` row field becomes `0`.
- HTTP/L4 `RelayChainJSON` becomes `[]`.

### Service API

HTTP rule service:

- `HTTPRuleInput.BackendURL` is removed from create/update normalization paths.
- `normalizeHTTPBackendsInput` requires valid `Backends`.
- `HTTPRule.BackendURL` is removed from service response structs or left empty only if required by storage compatibility tests during the transition.
- `httpRuleFromRow` does not synthesize `Backends` from `BackendURL`.
- `httpRuleToRow` writes canonical `BackendsJSON` and neutral legacy fields.

L4 rule service:

- `L4RuleInput.UpstreamHost` and `L4RuleInput.UpstreamPort` are removed from create/update normalization paths.
- `normalizeL4BackendsInput` requires valid `Backends` for non-proxy listen mode.
- Proxy listen mode remains backendless and keeps its existing proxy egress validation.
- `l4RuleFromRow` does not synthesize `Backends` from `UpstreamHost/UpstreamPort`.
- `l4RuleToRow` writes canonical `BackendsJSON` and neutral legacy fields.

Relay path service behavior:

- Create/update paths use `RelayLayers`.
- Legacy relay chain JSON in storage is migrated to layers and is no longer emitted as active API state.
- Validation checks `flattenRelayLayers(relayLayers)`.

## Agent Runtime Design

`go-agent/internal/model` removes or deprecates active usage of:

- `HTTPRule.BackendURL`
- `L4Rule.UpstreamHost`
- `L4Rule.UpstreamPort`
- `HTTPRule.RelayChain`
- `L4Rule.RelayChain`

The implementation may remove fields outright if all consumers are updated in the same plan. If that makes the plan too large, the fields may remain temporarily with ignored JSON tags for compatibility, but runtime code must not read them.

HTTP runtime:

- `runtimeRuleSpec` validates `FrontendURL` and validates `Backends` via the same URL rules currently applied to `BackendURL`.
- `parseHTTPBackends` reads `rule.Backends` only.
- Error text should refer to `backends[].url`, not `backend_url`.

L4 runtime:

- `ValidateRule` reads `rule.Backends` only for non-proxy mode.
- `l4Candidates` reads `rule.Backends` only.
- Error text should refer to `backends`, not `upstream_host/upstream_port`.

Diagnostics:

- HTTP diagnostics require `rule.Backends` and no longer synthesize from `BackendURL`.
- L4 diagnostics require `rule.Backends` and no longer synthesize from `UpstreamHost/UpstreamPort`.
- Relay diagnostics use `RelayLayers`; observation/backoff logic must keep layered keys.

Relay route:

- `relayroute.UsesRelay` and route resolution call sites pass canonical relay layers.
- Any use of `RelayChain` as a fallback path is removed from HTTP, L4, diagnostics, and app validation.

## Frontend Design

Frontend data flow becomes canonical:

- HTTP forms initialize and submit `backends` only.
- L4 forms initialize and submit `backends` only.
- Relay path UI reads/writes `relay_layers` only.
- Runtime API helpers stop filling `backend_url`, `upstream_host`, `upstream_port`, or `relay_chain`.
- Display helpers and search helpers use `backends` and `relay_layers` only.
- Dev mocks and tests are updated to canonical payloads.

The user-facing form can still present a single backend row by default. That is a UI convenience, not an API compatibility field.

## Data Migration

SQLite bootstrap normalization must be idempotent:

- Only populate canonical fields from legacy fields when canonical fields are empty or invalid.
- Do not overwrite non-empty canonical fields.
- Normalize legacy relay chain to relay layers only when `relay_layers` is empty.
- After migration, row-to-snapshot and service response paths should use canonical fields.

PostgreSQL or non-SQLite behavior should rely on existing schema migration support and canonical service writes. If non-SQLite legacy migration is required, add an explicit storage migration task rather than hidden fallback reads.

## Error Handling

- HTTP missing targets: `backends[].url is required` or `at least one valid backend is required`.
- L4 missing targets: `at least one valid backend is required`.
- Relay missing paths in proxy relay mode: `proxy relay egress requires relay_layers`.
- Relay validation errors keep listener IDs and rule context in messages.

## Testing Strategy

Agent tests:

- `cd go-agent && go test ./internal/model ./internal/proxy ./internal/l4 ./internal/diagnostics ./internal/app ./internal/sync`
- `cd go-agent && go test ./...`

Control-plane tests:

- `cd panel/backend-go && go test ./internal/controlplane/storage ./internal/controlplane/service ./internal/controlplane/localagent ./internal/controlplane/http`
- `cd panel/backend-go && go test ./...`

Frontend tests/build:

- `cd panel/frontend && npm run build`
- Run targeted frontend tests where package scripts exist for rule forms, runtime API helpers, search, and relay layer compatibility.

Full image-impacting verification:

- `docker build -t nginx-reverse-emby .` after agent/backend/frontend pass.

## Acceptance Criteria

- New API and frontend payloads use `backends[]` and `relay_layers` only.
- Agent runtime and diagnostics do not read `backend_url`, `upstream_host`, `upstream_port`, or `relay_chain` as fallbacks.
- Stored legacy rows are migrated into canonical fields on bootstrap.
- Service responses and local agent snapshots carry canonical fields.
- Existing behavior is preserved for backend selection, relay path expansion, proxy mode, obfuscation, and diagnostics after canonical migration.
- All required Go tests and frontend build pass.

## Risks

The highest risk is breaking existing stored rows. Mitigation is to migrate old values into canonical fields before removing fallback reads from runtime and response construction.

The second risk is changing relay path semantics when converting `relay_chain` to `relay_layers`. The conversion must preserve order by turning `[1,2,3]` into `[[1],[2],[3]]`.

The third risk is test churn across frontend and backend. Mitigation is to stage implementation by subsystem, keep compatibility column retention separate from active API removal, and run package-level tests after each slice.
