# Go Agent Phase 2 App Runtime Split Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Split `go-agent/internal/app/app.go` and `go-agent/internal/app/local_runtime.go` into responsibility-focused files where it reduces coupling around runtime managers and snapshot application, without changing behavior.

**Architecture:** Keep package `app` stable and perform same-package declaration moves only. Runtime manager implementations move out of `local_runtime.go` into HTTP/L4/relay-specific files; snapshot activation and sync/runtime-state persistence move out of `app.go` into focused files. Constructors, public `App` type, `Run`, `Close`, diagnostics entry points, and update handling stay in `app.go` unless a task explicitly moves them.

**Tech Stack:** Go standard library, existing `internal/app`, `internal/runtime`, `internal/proxy`, `internal/l4`, `internal/relay`, `internal/store`, `internal/sync`, `internal/traffic`, and `internal/model` packages.

---

## Scope And Constraints

This plan implements the optional Phase 2 app runtime split from `docs/superpowers/specs/2026-05-09-go-agent-performance-refactor-design.md`.

Required constraints:

- Move declarations within `package app`; do not introduce a new package.
- Do not change behavior, signatures, logging text, stored metadata keys, traffic reporting cadence, snapshot merge semantics, runtime activation order, validation messages, or tests.
- Do not remove compatibility paths. Phase 3 compatibility cleanup is out of scope.
- Do not modify `panel/backend-go`, `panel/frontend`, `panel/data`, Docker files, or README files.
- Keep imports minimal after each move.
- Run `gofmt` on changed Go files after each task.
- Run `cd go-agent && go test ./internal/app` after each task.
- Commit after each task with the exact commit message listed in that task.

## Target File Map

- Keep: `go-agent/internal/app/app.go`
  - Owns type aliases/interfaces, `App`, constructor wiring, diagnostics entry points, `Run`, `Close`, `performSync`, `SyncNow`, local runtime closing, and update handling.
- Keep and shrink: `go-agent/internal/app/local_runtime.go`
  - Owns shared local runtime interfaces/helpers only: traffic block state wrappers, `L4Applier`, `RelayApplier`, `validateL4Rules`, `validateRelayListeners`, `httpBindingsOverlap`, and `backendCacheConfigFromAppConfig`.
- Create: `go-agent/internal/app/http_runtime.go`
  - Owns `httpRuntimeManager` and its constructors/methods.
- Create: `go-agent/internal/app/l4_runtime.go`
  - Owns `l4RuntimeManager` and its constructors/methods.
- Create: `go-agent/internal/app/relay_runtime.go`
  - Owns `relayRuntimeManager` and its constructor/methods.
- Create: `go-agent/internal/app/sync_runtime_state.go`
  - Owns sync request construction, traffic stats metadata helpers, runtime-state persistence helpers, and sync-once error persistence.
- Create: `go-agent/internal/app/snapshot_activation.go`
  - Owns snapshot payload merge, rollback, apply helpers, snapshot activator construction, traffic block state update, and local relay listener filtering.

## Task 1: Split HTTP Runtime Manager

**Files:**
- Create: `go-agent/internal/app/http_runtime.go`
- Modify: `go-agent/internal/app/local_runtime.go`
- Test: existing tests under `go-agent/internal/app`

- [ ] **Step 1: Capture baseline**

Run:

```powershell
cd go-agent
go test ./internal/app
```

Expected: PASS.

- [ ] **Step 2: Move HTTP manager declarations**

Create `go-agent/internal/app/http_runtime.go` with `package app` and move these declarations from `local_runtime.go` exactly:

```go
type httpRuntimeManager struct { ... }
func newHTTPRuntimeManager() *httpRuntimeManager
func newHTTPRuntimeManagerWithTLS(provider proxy.TLSMaterialProvider) *httpRuntimeManager
func newHTTPRuntimeManagerWithTLSAndHTTP3(provider proxy.TLSMaterialProvider, http3Enabled bool) *httpRuntimeManager
func newHTTPRuntimeManagerWithConfig(cfg Config) *httpRuntimeManager
func newHTTPRuntimeManagerWithTLSAndHTTP3AndConfig(provider proxy.TLSMaterialProvider, http3Enabled bool, cfg Config) *httpRuntimeManager
func (m *httpRuntimeManager) Apply(ctx context.Context, rules []model.HTTPRule) error
func (m *httpRuntimeManager) ApplyWithRelay(ctx context.Context, rules []model.HTTPRule, relayListeners []model.RelayListener) error
func (m *httpRuntimeManager) UpdateTrafficBlockState(state proxy.TrafficBlockState)
func (m *httpRuntimeManager) currentTrafficBlockState() proxy.TrafficBlockState
func (m *httpRuntimeManager) Close() error
```

Required imports in `http_runtime.go` are the imports those moved declarations actually use. Do not leave unused imports in `local_runtime.go`.

- [ ] **Step 3: Format and test**

Run:

```powershell
gofmt -w internal/app/local_runtime.go internal/app/http_runtime.go
go test ./internal/app
```

Expected: PASS.

- [ ] **Step 4: Commit**

Run:

```powershell
git add go-agent/internal/app/local_runtime.go go-agent/internal/app/http_runtime.go
git commit -m "refactor(app): split http runtime manager"
```

## Task 2: Split L4 And Relay Runtime Managers

**Files:**
- Create: `go-agent/internal/app/l4_runtime.go`
- Create: `go-agent/internal/app/relay_runtime.go`
- Modify: `go-agent/internal/app/local_runtime.go`
- Test: existing tests under `go-agent/internal/app`

- [ ] **Step 1: Capture current passing state**

Run:

```powershell
cd go-agent
go test ./internal/app
```

Expected: PASS.

- [ ] **Step 2: Move L4 manager declarations**

Create `go-agent/internal/app/l4_runtime.go` with `package app` and move these declarations from `local_runtime.go` exactly:

```go
type l4RuntimeManager struct { ... }
func newL4RuntimeManager() *l4RuntimeManager
func newL4RuntimeManagerWithRelay(provider relay.TLSMaterialProvider) *l4RuntimeManager
func newL4RuntimeManagerWithConfig(cfg Config) *l4RuntimeManager
func newL4RuntimeManagerWithRelayAndConfig(provider relay.TLSMaterialProvider, cfg Config) *l4RuntimeManager
func (m *l4RuntimeManager) Apply(ctx context.Context, rules []model.L4Rule) error
func (m *l4RuntimeManager) ApplyWithRelay(ctx context.Context, rules []model.L4Rule, relayListeners []model.RelayListener) error
func (m *l4RuntimeManager) UpdateTrafficBlockState(state l4.TrafficBlockState)
func (m *l4RuntimeManager) currentTrafficBlockState() l4.TrafficBlockState
func (m *l4RuntimeManager) Close() error
```

- [ ] **Step 3: Move relay manager declarations**

Create `go-agent/internal/app/relay_runtime.go` with `package app` and move these declarations from `local_runtime.go` exactly:

```go
type relayRuntimeManager struct { ... }
func newRelayRuntimeManager(provider relay.TLSMaterialProvider) *relayRuntimeManager
func (m *relayRuntimeManager) Apply(ctx context.Context, listeners []model.RelayListener) error
func (m *relayRuntimeManager) UpdateTrafficBlockState(state relay.TrafficBlockState)
func (m *relayRuntimeManager) currentTrafficBlockState() relay.TrafficBlockState
func (m *relayRuntimeManager) Close() error
```

Required imports in new files are the imports those moved declarations actually use. Do not leave unused imports in `local_runtime.go`.

- [ ] **Step 4: Format and test**

Run:

```powershell
gofmt -w internal/app/local_runtime.go internal/app/l4_runtime.go internal/app/relay_runtime.go
go test ./internal/app
```

Expected: PASS.

- [ ] **Step 5: Commit**

Run:

```powershell
git add go-agent/internal/app/local_runtime.go go-agent/internal/app/l4_runtime.go go-agent/internal/app/relay_runtime.go
git commit -m "refactor(app): split l4 relay runtime managers"
```

## Task 3: Split Sync And Runtime-State Helpers

**Files:**
- Create: `go-agent/internal/app/sync_runtime_state.go`
- Modify: `go-agent/internal/app/app.go`
- Test: existing tests under `go-agent/internal/app`

- [ ] **Step 1: Capture current passing state**

Run:

```powershell
cd go-agent
go test ./internal/app
```

Expected: PASS.

- [ ] **Step 2: Move sync/runtime-state declarations**

Create `go-agent/internal/app/sync_runtime_state.go` with `package app` and move these declarations from `app.go` exactly:

```go
const (
	runtimeMetaTrafficStatsInterval       = "traffic_stats_interval"
	runtimeMetaLastTrafficStatsReportUnix = "last_traffic_stats_report_unix"
	runtimeMetaTrafficBlocked             = "traffic_blocked"
	runtimeMetaTrafficBlockReason         = "traffic_block_reason"
)
func (a *App) syncRequest(ctx context.Context, applied Snapshot) (SyncRequest, error)
func (a *App) hostTrafficSnapshot() (map[string]any, error)
func mergeTrafficStats(base, extra map[string]any) map[string]any
func shouldReportTrafficStats(meta map[string]string, now time.Time) bool
func hasTrafficStatsInterval(meta map[string]string) bool
func (a *App) persistTrafficStatsInterval(raw string) error
func parseTrafficStatsInterval(raw string) (string, error)
func setTrafficStatsIntervalMetadata(meta map[string]string, raw string) error
func (a *App) syncOnce(ctx context.Context, req SyncRequest) error
func (a *App) recordRuntimeError(syncErr error) error
func (a *App) persistLastTrafficStatsReportUnix(timestamp string) error
func (a *App) recordRuntimeErrorWithRevision(syncErr error, revision int64) error
func (a *App) persistRuntimeState(clearLastSyncError bool) error
func setTrafficBlockedMetadata(meta map[string]string, cfg model.AgentConfig)
func (a *App) recordPersistedRuntimeError(syncErr error) error
func (a *App) recordPersistedRuntimeErrorWithRevision(syncErr error, revision int64) error
func ensureMetadata(meta map[string]string) map[string]string
func setApplyMetadata(meta map[string]string, revision int64, status string, message string)
func parseInt64(raw string, fallback int64) int64
func (a *App) runtimeStateForPersistence() (store.RuntimeState, error)
```

Required imports in `sync_runtime_state.go` are the imports those moved declarations actually use. Do not leave unused imports in `app.go`.

- [ ] **Step 3: Format and test**

Run:

```powershell
gofmt -w internal/app/app.go internal/app/sync_runtime_state.go
go test ./internal/app
```

Expected: PASS.

- [ ] **Step 4: Commit**

Run:

```powershell
git add go-agent/internal/app/app.go go-agent/internal/app/sync_runtime_state.go
git commit -m "refactor(app): split sync runtime state"
```

## Task 4: Split Snapshot Activation Helpers

**Files:**
- Create: `go-agent/internal/app/snapshot_activation.go`
- Modify: `go-agent/internal/app/app.go`
- Test: existing tests under `go-agent/internal/app`

- [ ] **Step 1: Capture current passing state**

Run:

```powershell
cd go-agent
go test ./internal/app
```

Expected: PASS.

- [ ] **Step 2: Move snapshot activation declarations**

Create `go-agent/internal/app/snapshot_activation.go` with `package app` and move these declarations from `app.go` exactly:

```go
func (a *App) applyManagedCertificates(ctx context.Context, snapshot Snapshot) error
func (a *App) applyHTTPRules(ctx context.Context, snapshot Snapshot) error
func mergeSnapshotPayload(next, previous Snapshot) Snapshot
func (a *App) rollbackRuntime(ctx context.Context, previousApplied, targetApplied Snapshot)
func (a *App) applyL4Rules(ctx context.Context, snapshot Snapshot) error
func (a *App) applyRelayListeners(ctx context.Context, snapshot Snapshot) error
func (a *App) snapshotActivator() agentruntime.Activator
func (a *App) snapshotActivationHandlers() agentruntime.SnapshotActivationHandlers
func (a *App) updateTrafficBlockState(cfg model.AgentConfig)
func localRelayListeners(listeners []model.RelayListener, agentID, agentName string) []model.RelayListener
```

Required imports in `snapshot_activation.go` are the imports those moved declarations actually use. Do not leave unused imports in `app.go`.

- [ ] **Step 3: Verify final declaration lists**

Run:

```powershell
rg -n "^(type|func|var|const) " internal/app/app.go internal/app/local_runtime.go
```

Expected:

- `app.go` still owns app construction, diagnostics, run loop, sync entry points, close, local runtime closing, and update handling.
- `local_runtime.go` no longer contains `httpRuntimeManager`, `l4RuntimeManager`, or `relayRuntimeManager`.
- `local_runtime.go` still contains traffic block state wrappers, `L4Applier`, `RelayApplier`, validation helpers, `httpBindingsOverlap`, and `backendCacheConfigFromAppConfig`.

- [ ] **Step 4: Format and test**

Run:

```powershell
gofmt -w internal/app/app.go internal/app/snapshot_activation.go
go test ./internal/app
```

Expected: PASS.

- [ ] **Step 5: Commit**

Run:

```powershell
git add go-agent/internal/app/app.go go-agent/internal/app/snapshot_activation.go
git commit -m "refactor(app): split snapshot activation"
```

## Final Verification

After all tasks and reviews complete, run:

```powershell
cd go-agent
go test ./internal/app
go test ./...
cd ..
git diff --check
git status --short
```

Expected:

- `go test ./internal/app` passes.
- `go test ./...` passes.
- `git diff --check` reports no whitespace errors.
- `git status --short` is clean after all commits.

## Self-Review Notes

- Spec coverage: This plan covers only the Phase 2 optional app split where it reduces coupling around runtime managers and snapshot application. It intentionally avoids Phase 3 compatibility cleanup and does not touch control-plane/UI files.
- Placeholder scan: The moved declaration lists and verification commands are explicit. No task asks workers to design new behavior.
- Type consistency: All declarations keep current names, receivers, parameters, return values, package-private visibility, and metadata key strings.
