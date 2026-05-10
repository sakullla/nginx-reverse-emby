# Phase 3 Canonical Rule Fields Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove active compatibility use of HTTP `backend_url`, L4 `upstream_host`/`upstream_port`, and relay `relay_chain` by switching runtime, API, and frontend flows to canonical `backends[]` and `relay_layers`.

**Architecture:** Keep legacy DB columns as migration inputs, but stop using legacy fields as API/runtime payload sources. Implement the cutover in four slices: storage migration and service output/write behavior, go-agent runtime/diagnostics, frontend canonical payloads, then final full-stack verification.

**Tech Stack:** Go 1.26 modules in `go-agent` and `panel/backend-go`, Vue 3/Vite frontend in `panel/frontend`, existing SQLite/GORM storage, existing Vitest frontend test setup.

---

## Scope And Constraints

This plan implements `docs/superpowers/specs/2026-05-10-phase-3-canonical-rule-fields-design.md`.

Required constraints:

- Canonical target fields:
  - HTTP: `backends[]`
  - L4: `backends[]`
  - Relay: `relay_layers`
- Legacy fields must no longer be active API/runtime sources:
  - HTTP `backend_url`
  - L4 `upstream_host`, `upstream_port`
  - HTTP/L4 `relay_chain`
- Do not drop legacy SQLite columns in this phase.
- Preserve old stored rows with idempotent bootstrap normalization into canonical fields.
- Do not change load balancing, relay path expansion, proxy-entry behavior, backend health scoring, traffic accounting, timeout defaults, or buffer sizes.
- Keep commits scoped by task.
- If a task encounters unexpected incompatibility, report `BLOCKED` instead of broadening scope silently.

## Target Verification

Minimum final verification:

```powershell
cd go-agent
go test ./...
cd ..\panel\backend-go
go test ./...
cd ..\frontend
npm run build
cd ..\..
git diff --check
git status --short
```

For image-impacting completion, also run:

```powershell
docker build -t nginx-reverse-emby .
```

## Task 1: Backend Storage Canonical Migration

**Files:**
- Modify: `panel/backend-go/internal/controlplane/storage/schema.go`
- Modify: `panel/backend-go/internal/controlplane/storage/sqlite_store.go`
- Modify: `panel/backend-go/internal/controlplane/storage/sqlite_store_test.go`
- Modify: `panel/backend-go/internal/controlplane/storage/snapshot_types.go`

- [ ] **Step 1: Add failing storage tests**

Add or update tests in `panel/backend-go/internal/controlplane/storage/sqlite_store_test.go` that prove:

```go
func TestBootstrapSchemaMigratesLegacyHTTPRuleFieldsToCanonical(t *testing.T) {
	// Seed a legacy HTTP row with backend_url and relay_chain populated,
	// backends='[]', relay_layers='[]'.
	// Bootstrap schema again.
	// ListHTTPRules should return BackendsJSON containing backend_url and
	// RelayLayersJSON containing one layer per relay_chain item.
}

func TestBootstrapSchemaMigratesLegacyL4RuleFieldsToCanonical(t *testing.T) {
	// Seed a legacy L4 row with upstream_host/upstream_port and relay_chain populated,
	// backends='[]', relay_layers='[]'.
	// Bootstrap schema again.
	// ListL4Rules should return BackendsJSON containing host/port and
	// RelayLayersJSON containing one layer per relay_chain item.
}
```

Run:

```powershell
cd panel/backend-go
go test ./internal/controlplane/storage -run "TestBootstrapSchemaMigratesLegacy.*Canonical" -count=1
```

Expected: FAIL before implementation.

- [ ] **Step 2: Implement idempotent SQLite normalization**

In `schema.go`, add normalization statements to populate canonical columns only when canonical values are empty:

- HTTP:
  - `rules.backends` from `rules.backend_url`.
  - `rules.relay_layers` from `rules.relay_chain` as `[[id1],[id2]]`.
- L4:
  - `l4_rules.backends` from `l4_rules.upstream_host/upstream_port`.
  - `l4_rules.relay_layers` from `l4_rules.relay_chain` as `[[id1],[id2]]`.

Keep statements SQLite-compatible and idempotent. If JSON1 helpers are already used safely in this file, use them. Otherwise add small Go migration helpers in storage that load rows, parse legacy JSON with existing parse helpers, and save normalized JSON back through GORM.

- [ ] **Step 3: Stop storage snapshot fallback synthesis**

In `sqlite_store.go`, update snapshot conversion so rows no longer synthesize canonical fields from legacy fields after bootstrap:

- `HTTPRule.Backends` should come from `BackendsJSON` only.
- `HTTPRule.RelayLayers` should come from `RelayLayersJSON` only.
- `L4Rule.Backends` should come from `BackendsJSON` only.
- `L4Rule.RelayLayers` should come from `RelayLayersJSON` only.
- Legacy fields may remain in `snapshot_types.go` only if required to compile, but should not be the source of canonical behavior.

- [ ] **Step 4: Run storage tests**

Run:

```powershell
go test ./internal/controlplane/storage
```

Expected: PASS.

- [ ] **Step 5: Commit**

Run:

```powershell
git add panel/backend-go/internal/controlplane/storage
git commit -m "refactor(storage): migrate rule fields to canonical form"
```

## Task 2: Backend Service Canonical API

**Files:**
- Modify: `panel/backend-go/internal/controlplane/service/rules.go`
- Modify: `panel/backend-go/internal/controlplane/service/l4.go`
- Modify: relevant tests under `panel/backend-go/internal/controlplane/service`

- [ ] **Step 1: Add/update service tests**

Update service tests so create/update uses canonical payloads:

- HTTP create/update with `backends` succeeds.
- HTTP create/update with only `backend_url` fails with an invalid argument.
- L4 create/update with `backends` succeeds.
- L4 non-proxy create/update with only `upstream_host/upstream_port` fails.
- Relay path create/update uses `relay_layers`; relay-chain-only payloads fail or are ignored consistently with the implemented policy.

Run targeted tests:

```powershell
cd panel/backend-go
go test ./internal/controlplane/service -run "RuleService|L4" -count=1
```

Expected: FAIL before implementation updates are complete.

- [ ] **Step 2: Update HTTP service normalization**

In `rules.go`:

- Remove `BackendURL` as an active input source in `normalizeHTTPBackendsInput`.
- Require `Backends` to contain at least one valid URL.
- Stop writing legacy `BackendURL` values in `httpRuleToRow`; write `BackendURL: ""`.
- Stop returning synthesized `BackendURL` in `httpRuleFromRow`; keep response `Backends` canonical.
- Normalize relay by using `RelayLayers`; do not preserve/update `RelayChain` from input.
- Write `RelayChainJSON: "[]"`.

- [ ] **Step 3: Update L4 service normalization**

In `l4.go`:

- Remove `UpstreamHost` and `UpstreamPort` as active input sources in `normalizeL4BackendsInput`.
- Require `Backends` to contain at least one valid backend for non-proxy listen mode.
- Keep proxy listen mode backendless behavior.
- Stop writing legacy `UpstreamHost` and `UpstreamPort` values in `l4RuleToRow`; write neutral values.
- Stop returning synthesized upstream values in `l4RuleFromRow`; keep response `Backends` canonical.
- Normalize relay by using `RelayLayers`; do not preserve/update `RelayChain` from input.
- Write `RelayChainJSON: "[]"`.

- [ ] **Step 4: Update related service tests and fixtures**

Update tests, backup/import fixtures, agent snapshot tests, cutover fixtures, and local-agent tests that still assert legacy fields as active state. They should assert canonical `Backends` and `RelayLayers`.

- [ ] **Step 5: Run backend service tests**

Run:

```powershell
go test ./internal/controlplane/service ./internal/controlplane/localagent ./internal/controlplane/http
```

Expected: PASS.

- [ ] **Step 6: Commit**

Run:

```powershell
git add panel/backend-go/internal/controlplane/service panel/backend-go/internal/controlplane/localagent panel/backend-go/internal/controlplane/http
git commit -m "refactor(controlplane): use canonical rule fields"
```

## Task 3: Go Agent Canonical Runtime

**Files:**
- Modify: `go-agent/internal/model/http.go`
- Modify: `go-agent/internal/model/l4.go`
- Modify: `go-agent/internal/proxy`
- Modify: `go-agent/internal/l4`
- Modify: `go-agent/internal/diagnostics`
- Modify: `go-agent/internal/app`
- Modify: `go-agent/internal/sync`
- Modify: related tests under `go-agent/internal`

- [ ] **Step 1: Add/update agent tests**

Update tests so canonical payloads are the only accepted behavior:

- HTTP runtime rejects rules with no `Backends`.
- HTTP runtime no longer accepts `BackendURL` alone.
- L4 runtime rejects non-proxy rules with no `Backends`.
- L4 runtime no longer accepts `UpstreamHost/UpstreamPort` alone.
- Relay resolution uses `RelayLayers`; relay-chain-only tests are removed or rewritten to layers.
- Sync decode tests assert canonical fields.

Run targeted tests:

```powershell
cd go-agent
go test ./internal/model ./internal/proxy ./internal/l4 ./internal/diagnostics ./internal/app ./internal/sync
```

Expected: FAIL before implementation updates are complete.

- [ ] **Step 2: Update models**

In `go-agent/internal/model/http.go` and `l4.go`:

- Remove active use of `BackendURL`, `UpstreamHost`, `UpstreamPort`, and `RelayChain`.
- Prefer removing fields entirely. If compile impact is too broad for this task, leave fields with comments only as ignored legacy payload fields, but no runtime code may read them.

- [ ] **Step 3: Update HTTP runtime and diagnostics**

In `go-agent/internal/proxy` and `go-agent/internal/diagnostics/http.go`:

- `parseHTTPBackends` reads `rule.Backends` only.
- `runtimeRuleSpec` validates `rule.Backends` URL schemes instead of `rule.BackendURL`.
- Error text references `backends[].url`.
- Relay route calls use `rule.RelayLayers` and no `RelayChain` fallback.
- Backoff keys for layered paths use `RelayBackoffKeyForLayers(nil, rule.RelayLayers, address)` or an equivalent canonical form.

- [ ] **Step 4: Update L4 runtime and diagnostics**

In `go-agent/internal/l4` and `go-agent/internal/diagnostics/l4tcp.go`:

- Validation and candidate construction read `rule.Backends` only for non-proxy rules.
- Error text references `backends`.
- Relay route calls use `rule.RelayLayers` and no `RelayChain` fallback.
- Backoff keys for layered paths use canonical layers.

- [ ] **Step 5: Update app validation and sync fixtures**

In `go-agent/internal/app` and `go-agent/internal/sync`:

- App relay validation checks `flattenRelayLayers(rule.RelayLayers)` only.
- Sync tests and fixtures use canonical fields.

- [ ] **Step 6: Run agent tests**

Run:

```powershell
go test ./internal/model ./internal/proxy ./internal/l4 ./internal/diagnostics ./internal/app ./internal/sync
go test ./...
```

Expected: PASS.

- [ ] **Step 7: Commit**

Run:

```powershell
git add go-agent/internal
git commit -m "refactor(agent): use canonical rule fields"
```

## Task 4: Frontend Canonical Payloads

**Files:**
- Modify: `panel/frontend/src/api/runtime.js`
- Modify: `panel/frontend/src/api/devMocks/data.js`
- Modify: `panel/frontend/src/components/RuleForm.vue`
- Modify: `panel/frontend/src/components/L4RuleForm.vue`
- Modify: display/search components and tests under `panel/frontend/src`

- [ ] **Step 1: Update frontend tests first**

Update or add tests to assert:

- HTTP save payload includes `backends` and `relay_layers`, not `backend_url` or `relay_chain`.
- L4 save payload includes `backends` and `relay_layers`, not `upstream_host`, `upstream_port`, or `relay_chain`.
- Runtime API normalization does not synthesize canonical fields from legacy fields.
- Search/display helpers use `backends` only.

Run:

```powershell
cd panel/frontend
npm run test -- --run
```

Expected: FAIL before implementation updates are complete.

- [ ] **Step 2: Update runtime API helpers**

In `runtime.js`:

- `normalizeHttpBackends` reads `rule.backends` only.
- `normalizeL4Backends` reads `rule.backends` only.
- Payload normalization writes `backends` and `relay_layers` only.
- Remove legacy payload constructors for `backend_url`, `upstream_host`, `upstream_port`, and `relay_chain` where not needed for UI-only local state.

- [ ] **Step 3: Update forms**

In `RuleForm.vue` and `L4RuleForm.vue`:

- Initialize backend rows from `initialData.backends` only.
- Submit canonical payloads with `backends` and `relay_layers`.
- Remove submitted `backend_url`, `upstream_host`, `upstream_port`, and `relay_chain`.
- Keep the UI's default single backend row behavior.

- [ ] **Step 4: Update displays, search, mocks, and compatibility tests**

Update components and tests that fallback-display old fields:

- `GlobalSearch.vue`, `useGlobalSearch.js`, rule cards/tables/items, Agent detail pages.
- `devMocks/data.js`.
- Relay compatibility tests should assert `relay_layers` only or be removed if they only defend legacy `relay_chain`.

- [ ] **Step 5: Run frontend tests/build**

Run:

```powershell
npm run test -- --run
npm run build
```

Expected: PASS.

- [ ] **Step 6: Commit**

Run:

```powershell
git add panel/frontend
git commit -m "refactor(frontend): use canonical rule payloads"
```

## Task 5: Full Stack Verification And Cleanup

**Files:**
- Modify docs or tests only if required by review.

- [ ] **Step 1: Search for active legacy runtime/API usage**

Run:

```powershell
rg -n "BackendURL|backend_url|UpstreamHost|UpstreamPort|upstream_host|upstream_port|RelayChain|relay_chain" go-agent panel/backend-go panel/frontend/src -g "!panel/data/**"
```

Expected:

- Remaining matches are limited to storage row legacy columns, migration code, historical tests explicitly named legacy migration, or docs.
- No runtime, diagnostics, service input normalization, frontend payload, frontend display, or agent model active path reads old fields.

- [ ] **Step 2: Run final verification**

Run:

```powershell
cd go-agent
go test ./...
cd ..\panel\backend-go
go test ./...
cd ..\frontend
npm run build
cd ..\..
git diff --check
git status --short
```

Expected: PASS/clean.

- [ ] **Step 3: Run docker build**

Run:

```powershell
docker build -t nginx-reverse-emby .
```

Expected: PASS.

- [ ] **Step 4: Commit final cleanup if needed**

If any docs/test-only cleanup is needed:

```powershell
git add <files>
git commit -m "test: verify canonical rule field cleanup"
```

If no cleanup is needed, do not create an empty commit.
