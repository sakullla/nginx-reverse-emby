# go-agent Core Readability Refactor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Improve `go-agent` readability by removing the low-value `agentutil` package, simplifying the app/core sync path, and moving HTTP/L4/Relay module support details out of lifecycle entry files.

**Architecture:** Prefer package-local helpers over generic utility packages. Keep public behavior and module lifecycle contracts stable while splitting large files by domain concept: sync planning/apply/metadata/update for `core`, and providers/runtime state/bindings/clones for HTTP/L4/Relay modules. Changes are mechanical refactors protected by existing package tests and final `go test ./...`.

**Tech Stack:** Go, standard `testing`, existing `go-agent` module tests.

---

## File Structure

- Delete: `go-agent/internal/agentutil/maps.go`
- Delete: `go-agent/internal/agentutil/maps_test.go`
- Delete: `go-agent/internal/agentutil/parse.go`
- Delete: `go-agent/internal/agentutil/parse_test.go`
- Modify: `go-agent/embedded/runtime.go`
  - Owns local runtime metadata cloning.
- Modify: `go-agent/internal/app/app.go`
  - Calls core sync controller methods directly where app wrappers are removed.
- Modify: `go-agent/internal/app/sync_runtime_state.go`
  - Keeps only app-owned sync controller construction and traffic reporter fallback, or is removed if empty.
- Modify: `go-agent/internal/app/snapshot_activation.go`
  - Calls `core.MergeSnapshotPayload` directly or keeps only app-owned activation helpers.
- Modify: `go-agent/internal/core/store.go`
  - Owns local runtime metadata cloning for in-memory store.
- Modify: `go-agent/internal/core/sync_controller.go`
  - Keeps core sync types and high-level orchestration surface.
- Create: `go-agent/internal/core/sync_plan.go`
  - Owns `BuildSyncRequest` and `BuildSyncPlan`.
- Create: `go-agent/internal/core/sync_apply.go`
  - Owns `PerformSync`, `PerformSyncPlan`, and runtime rollback.
- Create: `go-agent/internal/core/sync_metadata.go`
  - Owns runtime metadata parsing, apply metadata, and runtime-state persistence helpers.
- Create: `go-agent/internal/core/sync_update.go`
  - Owns pending update package handling.
- Create: `go-agent/internal/core/snapshot_merge.go`
  - Owns `MergeSnapshotPayload`.
- Modify: `go-agent/internal/modules/traffic/module.go`
  - Owns local metadata cloning helpers.
- Modify: `go-agent/internal/modules/http/module.go`
- Create: `go-agent/internal/modules/http/providers.go`
- Create: `go-agent/internal/modules/http/runtime_state.go`
- Create: `go-agent/internal/modules/http/bindings.go`
- Create: `go-agent/internal/modules/http/clones.go`
- Modify: `go-agent/internal/modules/l4/module.go`
- Create: `go-agent/internal/modules/l4/providers.go`
- Create: `go-agent/internal/modules/l4/runtime_state.go`
- Create: `go-agent/internal/modules/l4/bindings.go`
- Create: `go-agent/internal/modules/l4/clones.go`
- Modify: `go-agent/internal/modules/relay/module.go`
- Create: `go-agent/internal/modules/relay/providers.go`
- Create: `go-agent/internal/modules/relay/bindings.go`
- Create: `go-agent/internal/modules/relay/clones.go`

## Task 1: Remove `agentutil` and Localize Primitive Helpers

**Files:**
- Delete: `go-agent/internal/agentutil/maps.go`
- Delete: `go-agent/internal/agentutil/maps_test.go`
- Delete: `go-agent/internal/agentutil/parse.go`
- Delete: `go-agent/internal/agentutil/parse_test.go`
- Modify: `go-agent/embedded/runtime.go`
- Modify: `go-agent/internal/app/sync_runtime_state.go`
- Modify: `go-agent/internal/core/store.go`
- Modify: `go-agent/internal/core/sync_controller.go`
- Modify: `go-agent/internal/modules/traffic/module.go`

- [ ] **Step 1: Run current focused tests before removing helpers**

Run:

```powershell
go test ./embedded ./internal/app ./internal/core ./internal/modules/traffic ./internal/agentutil
```

Expected: all packages pass before edits.

- [ ] **Step 2: Replace embedded metadata clone with local helper**

In `go-agent/embedded/runtime.go`, remove the `internal/agentutil` import and change `copyRuntimeState` to call a local helper:

```go
func copyRuntimeState(state RuntimeState) RuntimeState {
	copyValue := state
	copyValue.Metadata = cloneRuntimeMetadata(state.Metadata)
	return copyValue
}

func cloneRuntimeMetadata(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}
```

- [ ] **Step 3: Replace core store clone with local helper**

In `go-agent/internal/core/store.go`, remove the `internal/agentutil` import. Change `SaveRuntimeState` and `LoadRuntimeState` to call `cloneRuntimeMetadata`.

Add this helper near the in-memory store methods:

```go
func cloneRuntimeMetadata(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}
```

- [ ] **Step 4: Replace sync controller primitive helpers locally**

In `go-agent/internal/core/sync_controller.go`, remove the `internal/agentutil` import. Replace:

```go
agentutil.ParseInt64Default(...)
agentutil.CloneStringMap(...)
agentutil.EnsureStringMap(...)
```

with:

```go
parseInt64Default(...)
cloneStringMap(...)
ensureMetadata(...)
```

Add or update helpers at the bottom of the file:

```go
func ensureMetadata(meta map[string]string) map[string]string {
	if meta == nil {
		return make(map[string]string)
	}
	return meta
}

func cloneStringMap(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func parseInt64Default(raw string, fallback int64) int64 {
	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return fallback
	}
	return value
}
```

- [ ] **Step 5: Replace traffic module clone with local helpers**

In `go-agent/internal/modules/traffic/module.go`, remove the `internal/agentutil` import. Replace `agentutil.CloneStringMap` with `cloneStringMap`, and replace:

```go
effective := agentutil.EnsureStringMap(agentutil.CloneStringMap(meta))
```

with:

```go
effective := ensureStringMap(cloneStringMap(meta))
```

Add helpers near `installState`:

```go
func cloneStringMap(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func ensureStringMap(src map[string]string) map[string]string {
	if src == nil {
		return make(map[string]string)
	}
	return src
}
```

- [ ] **Step 6: Replace app local use or prepare it for removal**

In `go-agent/internal/app/sync_runtime_state.go`, remove the `internal/agentutil` import. Replace pending sync metadata clone calls with a local `cloneStringMap` helper if `syncRequest` and `syncOnce` still exist after this task:

```go
func cloneStringMap(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}
```

Change `ensureMetadata` to package-local logic if it still exists:

```go
func ensureMetadata(meta map[string]string) map[string]string {
	if meta == nil {
		return make(map[string]string)
	}
	return meta
}
```

- [ ] **Step 7: Delete `internal/agentutil`**

Delete:

```text
go-agent/internal/agentutil/maps.go
go-agent/internal/agentutil/maps_test.go
go-agent/internal/agentutil/parse.go
go-agent/internal/agentutil/parse_test.go
```

- [ ] **Step 8: Verify no `agentutil` references remain**

Run:

```powershell
rg -n "agentutil|internal/agentutil|CloneStringMap|EnsureStringMap|ParseInt64Default" go-agent
```

Expected: no matches outside deleted files. If matches remain in production code, replace them with package-local helpers.

- [ ] **Step 9: Run focused tests and commit**

Run:

```powershell
gofmt -w go-agent/embedded/runtime.go go-agent/internal/app/sync_runtime_state.go go-agent/internal/core/store.go go-agent/internal/core/sync_controller.go go-agent/internal/modules/traffic/module.go
go test ./embedded ./internal/app ./internal/core ./internal/modules/traffic
```

Expected: all listed packages pass.

Commit:

```powershell
git add go-agent/embedded/runtime.go go-agent/internal/app/sync_runtime_state.go go-agent/internal/core/store.go go-agent/internal/core/sync_controller.go go-agent/internal/modules/traffic/module.go go-agent/internal/agentutil
git commit -m "refactor(agent): remove low-value agentutil package"
```

## Task 2: Remove App Forwarding Helpers and Shorten Sync Call Path

**Files:**
- Modify: `go-agent/internal/app/app.go`
- Modify: `go-agent/internal/app/sync_runtime_state.go`
- Modify: `go-agent/internal/app/snapshot_activation.go`
- Modify: `go-agent/internal/app/app_test.go` if compile errors expose obsolete app-only helpers.

- [ ] **Step 1: Confirm current app/core tests pass**

Run:

```powershell
go test ./internal/app ./internal/core
```

Expected: both packages pass before edits.

- [ ] **Step 2: Remove unused app-only sync request wrappers**

In `go-agent/internal/app/sync_runtime_state.go`, delete `syncRequest` and `syncOnce` if `rg -n "syncRequest\\(|syncOnce\\(" go-agent/internal/app` shows they are definitions only.

After deleting them, remove `pendingSyncMetadata map[string]string` from `App` in `go-agent/internal/app/app.go` if no references remain.

- [ ] **Step 3: Remove traffic/core forwarding wrappers from app**

In `go-agent/internal/app/sync_runtime_state.go`, delete wrappers that are not used by production app code:

```go
func mergeTrafficStats(base, extra map[string]any) map[string]any
func shouldReportTrafficStats(meta map[string]string, now time.Time) bool
func hasTrafficStatsInterval(meta map[string]string) bool
func parseTrafficStatsInterval(raw string) (string, error)
func setTrafficStatsIntervalMetadata(meta map[string]string, raw string) error
func setTrafficBlockedMetadata(meta map[string]string, cfg model.AgentConfig)
func ensureMetadata(meta map[string]string) map[string]string
```

If app tests fail because they used one of these helpers, update the test to call the owning package directly:

```go
moduletraffic.MergeTrafficStats(...)
moduletraffic.ShouldReportTrafficStats(...)
moduletraffic.HasTrafficStatsInterval(...)
core.ParseTrafficStatsInterval(...)
core.SetTrafficStatsIntervalMetadata(...)
core.SetTrafficBlockedMetadata(...)
```

- [ ] **Step 4: Replace app error/interval forwarding methods with direct core controller use**

In `go-agent/internal/app/app.go`, replace calls to `a.recordRuntimeErrorWithRevision(err, revision)` with:

```go
_ = a.syncController().RecordRuntimeErrorWithRevision(err, revision)
```

In `Run`, replace:

```go
if err := a.persistTrafficStatsInterval(hydratedApplied.AgentConfig.TrafficStatsInterval); err != nil {
```

with:

```go
if err := a.syncController().PersistTrafficStatsInterval(hydratedApplied.AgentConfig.TrafficStatsInterval); err != nil {
```

Then delete these app methods from `sync_runtime_state.go`:

```go
func (a *App) persistTrafficStatsInterval(raw string) error
func (a *App) recordRuntimeErrorWithRevision(syncErr error, revision int64) error
```

- [ ] **Step 5: Replace snapshot merge forwarding wrapper**

In `go-agent/internal/app/app.go`, replace:

```go
return mergeSnapshotPayload(applied, desired)
```

with:

```go
return core.MergeSnapshotPayload(applied, desired)
```

In `go-agent/internal/app/snapshot_activation.go`, delete:

```go
func mergeSnapshotPayload(next, previous Snapshot) Snapshot {
	return core.MergeSnapshotPayload(next, previous)
}
```

- [ ] **Step 6: Keep or shrink `sync_runtime_state.go`**

If `sync_runtime_state.go` only contains `syncController` and `trafficReporter`, keep it as the app sync wiring file. Its imports should be limited to:

```go
import (
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/core"
	moduletraffic "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/traffic"
)
```

- [ ] **Step 7: Verify app/core tests and commit**

Run:

```powershell
gofmt -w go-agent/internal/app/app.go go-agent/internal/app/sync_runtime_state.go go-agent/internal/app/snapshot_activation.go go-agent/internal/app/app_test.go
go test ./internal/app ./internal/core
```

Expected: both packages pass.

Commit:

```powershell
git add go-agent/internal/app
git commit -m "refactor(agent): shorten app sync forwarding path"
```

## Task 3: Split `core.SyncController` by Sync Responsibility

**Files:**
- Modify: `go-agent/internal/core/sync_controller.go`
- Create: `go-agent/internal/core/sync_plan.go`
- Create: `go-agent/internal/core/sync_apply.go`
- Create: `go-agent/internal/core/sync_metadata.go`
- Create: `go-agent/internal/core/sync_update.go`
- Create: `go-agent/internal/core/snapshot_merge.go`
- Test: `go-agent/internal/core/sync_controller_test.go`

- [ ] **Step 1: Confirm core tests pass before moving code**

Run:

```powershell
go test ./internal/core
```

Expected: package passes before edits.

- [ ] **Step 2: Leave types in `sync_controller.go`**

Keep these declarations in `go-agent/internal/core/sync_controller.go`:

```go
type SyncClient interface
type Updater interface
type TrafficReporter interface
type TrafficReport struct
type SyncPlan struct
type ManagedCertificateReporter interface
type SyncController struct
```

After moving functions out, expected imports for this file should be only the packages needed by the remaining type declarations:

```go
import (
	"context"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/control"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)
```

- [ ] **Step 3: Create `sync_plan.go`**

Move these methods from `sync_controller.go` into `go-agent/internal/core/sync_plan.go`:

```go
func (c *SyncController) BuildSyncRequest(ctx context.Context, applied model.Snapshot) (control.SyncRequest, error)
func (c *SyncController) BuildSyncPlan(ctx context.Context, applied model.Snapshot) (SyncPlan, error)
```

Use this import shape, adding/removing imports after gofmt as needed:

```go
import (
	"context"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/control"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)
```

Keep the logic unchanged except for local helper names introduced in Task 1.

- [ ] **Step 4: Create `sync_apply.go`**

Move these methods into `go-agent/internal/core/sync_apply.go`:

```go
func (c *SyncController) PerformSync(ctx context.Context, req control.SyncRequest) error
func (c *SyncController) PerformSyncPlan(ctx context.Context, plan SyncPlan) error
func (c *SyncController) rollbackRuntime(ctx context.Context, previousApplied, targetApplied model.Snapshot) error
```

Use this import shape:

```go
import (
	"context"
	"errors"
	"fmt"
	"log"
	"reflect"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/control"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)
```

Keep behavior and error wrapping unchanged.

- [ ] **Step 5: Create `sync_metadata.go`**

Move these functions and methods into `go-agent/internal/core/sync_metadata.go`:

```go
func (c *SyncController) RecordRuntimeErrorWithRevision(syncErr error, revision int64) error
func (c *SyncController) PersistTrafficStatsInterval(raw string) error
func ParseTrafficStatsInterval(raw string) (string, error)
func SetTrafficStatsIntervalMetadata(meta map[string]string, raw string) error
func SetTrafficBlockedMetadata(meta map[string]string, cfg model.AgentConfig)
func (c *SyncController) recordRuntimeError(syncErr error) error
func (c *SyncController) recordRuntimeErrorWithRevision(syncErr error, revision int64) error
func (c *SyncController) recordPersistedRuntimeErrorWithRevision(syncErr error, revision int64) error
func (c *SyncController) persistRuntimeMetadata(metadata map[string]string) error
func (c *SyncController) persistRuntimeState(clearLastSyncError bool) error
func (c *SyncController) runtimeStateForPersistence() (RuntimeState, error)
func ensureMetadata(meta map[string]string) map[string]string
func setApplyMetadata(meta map[string]string, revision int64, status string, message string)
func cloneStringMap(src map[string]string) map[string]string
func parseInt64Default(raw string, fallback int64) int64
```

Use this import shape:

```go
import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)
```

- [ ] **Step 6: Create `sync_update.go`**

Move these functions into `go-agent/internal/core/sync_update.go`:

```go
func (c *SyncController) HandlePendingUpdate(ctx context.Context, snapshot model.Snapshot) error
func (c *SyncController) handlePendingUpdate(ctx context.Context, snapshot model.Snapshot) error
```

Use this import shape:

```go
import (
	"context"
	"errors"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)
```

- [ ] **Step 7: Create `snapshot_merge.go`**

Move this function into `go-agent/internal/core/snapshot_merge.go`:

```go
func MergeSnapshotPayload(next, previous model.Snapshot) model.Snapshot
```

Use this import:

```go
import "github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
```

- [ ] **Step 8: Verify core flow still passes**

Run:

```powershell
gofmt -w go-agent/internal/core/sync_controller.go go-agent/internal/core/sync_plan.go go-agent/internal/core/sync_apply.go go-agent/internal/core/sync_metadata.go go-agent/internal/core/sync_update.go go-agent/internal/core/snapshot_merge.go
go test ./internal/core
```

Expected: package passes.

- [ ] **Step 9: Commit core split**

Commit:

```powershell
git add go-agent/internal/core
git commit -m "refactor(agent): split sync controller responsibilities"
```

## Task 4: Move HTTP Module Support Details Out of `module.go`

**Files:**
- Modify: `go-agent/internal/modules/http/module.go`
- Create: `go-agent/internal/modules/http/providers.go`
- Create: `go-agent/internal/modules/http/runtime_state.go`
- Create: `go-agent/internal/modules/http/bindings.go`
- Create: `go-agent/internal/modules/http/clones.go`

- [ ] **Step 1: Confirm HTTP package tests pass**

Run:

```powershell
go test ./internal/modules/http
```

Expected: package passes before edits.

- [ ] **Step 2: Move provider helpers to `providers.go`**

Create `go-agent/internal/modules/http/providers.go` and move these declarations from `module.go`:

```go
func (m *Module) runtimeProviders(resolver module.ProviderResolver, egressProfiles []model.EgressProfile) (Providers, error)
func overlayRuntimeFromProvider(provider any) module.OverlayRuntime
func finalHopDialerFromProvider(provider any) relay.FinalHopDialer
type moduleFinalHopDialer struct
func (d moduleFinalHopDialer) DialTCP(ctx context.Context, target string, profileID *int) (net.Conn, error)
func (d moduleFinalHopDialer) OpenUDP(ctx context.Context, target string, profileID *int) (relay.UDPPacketPeer, error)
type moduleOverlayRuntimeProvider struct
func (p moduleOverlayRuntimeProvider) OverlayRuntime(profileID int) (relay.WireGuardRuntime, bool)
func (p moduleOverlayRuntimeProvider) OverlayRuntimeForAgent(agentID string, profileID int) (relay.WireGuardRuntime, bool)
func (p moduleOverlayRuntimeProvider) OverlayRuntimeForHop(hop relay.Hop) (relay.WireGuardRuntime, bool)
type moduleOverlayWireGuardRuntime struct
func (r moduleOverlayWireGuardRuntime) DialContext(ctx context.Context, network string, address string) (net.Conn, error)
func (r moduleOverlayWireGuardRuntime) ListenTCP(ctx context.Context, address string) (net.Listener, error)
func (r moduleOverlayWireGuardRuntime) ListenTransparentTCP(context.Context) (net.Listener, error)
func (r moduleOverlayWireGuardRuntime) ListenUDP(ctx context.Context, address string) (net.PacketConn, error)
func (r moduleOverlayWireGuardRuntime) ListenTransparentUDP(context.Context, string) (module.TransparentUDPConn, error)
```

Use imports for `context`, `fmt`, `net`, `strings`, `model`, `module`, `moduleegress`, and `relay` as required by the moved code.

- [ ] **Step 3: Move runtime state helpers to `runtime_state.go`**

Create `go-agent/internal/modules/http/runtime_state.go` and move:

```go
type runtimeState struct
func (m *Module) committedRuntimeStateLocked() runtimeState
func (m *Module) restoreRuntimeState(ctx context.Context, state runtimeState, closeCurrent bool) error
type rollbackOverlayRestorer interface
func restoreEgressOverlayForRollback(ctx context.Context, rules []model.HTTPRule, overlay any) error
func hasEgressWireGuardRule(rules []model.HTTPRule) bool
```

Use imports for `context`, `model`, and any same-package dependencies needed by the moved code.

- [ ] **Step 4: Move clone helpers to `clones.go`**

Create `go-agent/internal/modules/http/clones.go` and move:

```go
func cloneHTTPRules(rules []model.HTTPRule) []model.HTTPRule
func cloneRelayListeners(listeners []model.RelayListener) []model.RelayListener
func cloneIntLayers(layers [][]int) [][]int
func cloneEgressProfiles(profiles []model.EgressProfile) []model.EgressProfile
func cloneProviders(providers Providers) Providers
func snapshotProviders(providers Providers, egressProfiles []model.EgressProfile) Providers
```

Use imports for `strings`, `model`, and `moduleegress`.

- [ ] **Step 5: Move binding/retry helpers to `bindings.go`**

Create `go-agent/internal/modules/http/bindings.go` and move:

```go
func retryRuntimeBindConflict[T any](ctx context.Context, start func() (T, error)) (T, error)
func isRuntimeBindConflict(err error) bool
func bindingKeysOverlap(left, right []string) bool
type bindingKey struct
func parseBindingKey(raw string) (bindingKey, bool)
func (k bindingKey) overlaps(other bindingKey) bool
func normalizeBindingHost(host string) string
func bindingHostsEquivalent(left, right string) bool
func isLoopbackBindingHost(host string) bool
func bindingHostIsWildcard(host string) bool
```

Use imports for `context`, `net`, `strings`, and `time`.

- [ ] **Step 6: Verify `module.go` remains lifecycle-focused**

After moves, `go-agent/internal/modules/http/module.go` should still contain:

```go
type Config struct
type Module struct
func NewModule(cfg Config) *Module
func (m *Module) Name() string
func (m *Module) Descriptor() module.ModuleDescriptor
func (m *Module) RegisterProviders(reg module.ProviderRegistry) error
func (m *Module) Capabilities(module.SnapshotView) []module.Capability
func (m *Module) Apply(ctx context.Context, req module.ApplyRequest) error
func (m *Module) Prepare(ctx context.Context, req module.ApplyRequest) (module.ModuleTransaction, error)
func (m *Module) activeRuntime() *Runtime
func (m *Module) Stop(context.Context) error
func (m *Module) Close() error
func (m *Module) UpdateTrafficBlockState(state TrafficBlockState)
func (m *Module) currentTrafficBlockStateLocked() TrafficBlockState
func (m *Module) Cache() *model.Cache
func (m *Module) Transport() *http.Transport
func (m *Module) ResilienceOptions() StreamResilienceOptions
func (m *Module) HTTP3Enabled() bool
func (m *Module) ActiveRuntimeForTest() *Runtime
func (m *Module) storeLastAppliedStateLocked(state runtimeState)
func httpEffectiveInputsEqual(...)
func httpRelayInputsEqual(...)
func httpOverlayInputsEqual(...)
func httpEgressInputsEqual(...)
```

- [ ] **Step 7: Run HTTP tests and commit**

Run:

```powershell
gofmt -w go-agent/internal/modules/http/module.go go-agent/internal/modules/http/providers.go go-agent/internal/modules/http/runtime_state.go go-agent/internal/modules/http/bindings.go go-agent/internal/modules/http/clones.go
go test ./internal/modules/http
```

Expected: package passes.

Commit:

```powershell
git add go-agent/internal/modules/http
git commit -m "refactor(agent): split http module support code"
```

## Task 5: Move L4 Module Support Details Out of `module.go`

**Files:**
- Modify: `go-agent/internal/modules/l4/module.go`
- Create: `go-agent/internal/modules/l4/providers.go`
- Create: `go-agent/internal/modules/l4/runtime_state.go`
- Create: `go-agent/internal/modules/l4/bindings.go`
- Create: `go-agent/internal/modules/l4/clones.go`

- [ ] **Step 1: Confirm L4 package tests pass**

Run:

```powershell
go test ./internal/modules/l4
```

Expected: package passes before edits.

- [ ] **Step 2: Move provider helpers to `providers.go`**

Create `go-agent/internal/modules/l4/providers.go` and move:

```go
type Providers struct
func (m *Module) runtimeProviders(resolver module.ProviderResolver, egressProfiles []model.EgressProfile) Providers
func (p Providers) egressResolver() moduleegress.ProfileResolver
type rollbackOverlayRestorer interface
func restoreOverlayProvidersForRollback(ctx context.Context, rules []model.L4Rule, providers Providers) error
func restoreProviderForRollback(ctx context.Context, provider any) error
func sameProvider(left, right any) bool
func hasOverlayListenRule(rules []model.L4Rule) bool
func restoreEgressOverlayForRollback(ctx context.Context, rules []model.L4Rule, overlay any) error
func hasEgressProfileRule(rules []model.L4Rule) bool
func finalHopDialerFromProvider(provider any) relay.FinalHopDialer
type moduleFinalHopDialer struct
func (d moduleFinalHopDialer) DialTCP(ctx context.Context, target string, profileID *int) (net.Conn, error)
func (d moduleFinalHopDialer) OpenUDP(ctx context.Context, target string, profileID *int) (relay.UDPPacketPeer, error)
```

Use imports for `context`, `net`, `reflect`, `model`, `module`, `moduleegress`, and `relay`.

- [ ] **Step 3: Move runtime state helpers to `runtime_state.go`**

Create `go-agent/internal/modules/l4/runtime_state.go` and move:

```go
type runtimeState struct
func (m *Module) committedRuntimeStateLocked() runtimeState
func (m *Module) restoreRuntimeState(ctx context.Context, state runtimeState, closeCurrent bool) error
```

Use imports for `context` and package dependencies required by the moved code.

- [ ] **Step 4: Move clone helpers to `clones.go`**

Create `go-agent/internal/modules/l4/clones.go` and move:

```go
func cloneL4Rules(rules []model.L4Rule) []model.L4Rule
func cloneRelayListeners(listeners []model.RelayListener) []model.RelayListener
func cloneIntLayers(layers [][]int) [][]int
func cloneEgressProfiles(profiles []model.EgressProfile) []model.EgressProfile
func cloneProviders(providers Providers) Providers
func snapshotProviders(providers Providers, egressProfiles []model.EgressProfile) Providers
```

Use imports for `model` and `moduleegress`.

- [ ] **Step 5: Move binding/retry helpers to `bindings.go`**

Create `go-agent/internal/modules/l4/bindings.go` and move:

```go
func retryRuntimeBindConflict[T any](ctx context.Context, start func() (T, error)) (T, error)
func isRuntimeBindConflict(err error) bool
func bindingKeysOverlap(left, right []string) bool
type bindingKey struct
func parseBindingKey(raw string) (bindingKey, bool)
func (k bindingKey) overlaps(other bindingKey) bool
func normalizeBindingHost(host string) string
func bindingHostsEquivalent(left, right string) bool
func isLoopbackBindingHost(host string) bool
func bindingHostIsWildcard(host string) bool
```

Use imports for `context`, `net`, `strings`, and `time`.

- [ ] **Step 6: Verify `module.go` remains lifecycle-focused**

After moves, `go-agent/internal/modules/l4/module.go` should keep lifecycle and rule-specific functions:

```go
type Config struct
type Module struct
func NewModule(cfg Config) *Module
func (m *Module) Name() string
func (m *Module) Descriptor() module.ModuleDescriptor
func (m *Module) RegisterProviders(reg module.ProviderRegistry) error
func (m *Module) Capabilities(module.SnapshotView) []module.Capability
func (m *Module) Apply(ctx context.Context, req module.ApplyRequest) error
func (m *Module) Prepare(ctx context.Context, req module.ApplyRequest) (module.ModuleTransaction, error)
func (m *Module) activeServer() *Server
func (m *Module) Stop(context.Context) error
func (m *Module) Close() error
func (m *Module) UpdateTrafficBlockState(state TrafficBlockState)
func (m *Module) currentTrafficBlockStateLocked() TrafficBlockState
func (m *Module) Cache() *model.Cache
func (m *Module) ActiveServerForTest() *Server
func (m *Module) storeLastAppliedStateLocked(state runtimeState)
func l4EffectiveInputsEqual(...)
func l4RelayInputsEqual(...)
func l4OverlayInputsEqual(...)
func l4EgressInputsEqual(...)
func validateL4Rules(...)
func flattenRelayLayers(...)
func l4RuleUsesOverlay(...)
func l4RuleBindingKeys(...)
func l4RuleListenAddress(...)
func l4RuleBindingKey(...)
func valueOrZeroWireGuardProfileID(...)
func wireGuardTransparentInbound(...)
```

- [ ] **Step 7: Run L4 tests and commit**

Run:

```powershell
gofmt -w go-agent/internal/modules/l4/module.go go-agent/internal/modules/l4/providers.go go-agent/internal/modules/l4/runtime_state.go go-agent/internal/modules/l4/bindings.go go-agent/internal/modules/l4/clones.go
go test ./internal/modules/l4
```

Expected: package passes.

Commit:

```powershell
git add go-agent/internal/modules/l4
git commit -m "refactor(agent): split l4 module support code"
```

## Task 6: Move Relay Module Support Details Out of `module.go`

**Files:**
- Modify: `go-agent/internal/modules/relay/module.go`
- Create: `go-agent/internal/modules/relay/providers.go`
- Create: `go-agent/internal/modules/relay/bindings.go`
- Create: `go-agent/internal/modules/relay/clones.go`

- [ ] **Step 1: Confirm relay package tests pass**

Run:

```powershell
go test ./internal/modules/relay
```

Expected: package passes before edits. If a transient port reservation error appears, rerun once and report both outputs.

- [ ] **Step 2: Move provider helpers to `providers.go`**

Create `go-agent/internal/modules/relay/providers.go` and move:

```go
func overlayRuntimeFromProvider(provider any) module.OverlayRuntime
func finalHopDialerFromProvider(provider any) FinalHopDialer
type rollbackFinalHopProvider interface
func finalHopProviderForRollback(provider any) any
type moduleFinalHopDialer struct
func (d moduleFinalHopDialer) DialTCP(ctx context.Context, target string, profileID *int) (net.Conn, error)
func (d moduleFinalHopDialer) OpenUDP(ctx context.Context, target string, profileID *int) (UDPPacketPeer, error)
type moduleOverlayRuntimeProvider struct
func (p moduleOverlayRuntimeProvider) OverlayRuntime(profileID int) (WireGuardRuntime, bool)
func (p moduleOverlayRuntimeProvider) OverlayRuntimeForAgent(agentID string, profileID int) (WireGuardRuntime, bool)
func (p moduleOverlayRuntimeProvider) OverlayRuntimeForHop(hop Hop) (WireGuardRuntime, bool)
type moduleOverlayWireGuardRuntime struct
func (r moduleOverlayWireGuardRuntime) DialContext(ctx context.Context, network string, address string) (net.Conn, error)
func (r moduleOverlayWireGuardRuntime) ListenTCP(ctx context.Context, address string) (net.Listener, error)
func (r moduleOverlayWireGuardRuntime) ListenTransparentTCP(ctx context.Context) (net.Listener, error)
func (r moduleOverlayWireGuardRuntime) ListenUDP(ctx context.Context, address string) (net.PacketConn, error)
func (r moduleOverlayWireGuardRuntime) ListenTransparentUDP(ctx context.Context, address string) (module.TransparentUDPConn, error)
```

Use imports for `context`, `fmt`, `net`, `strings`, and `module`.

- [ ] **Step 3: Move clone/listener helpers to `clones.go`**

Create `go-agent/internal/modules/relay/clones.go` and move:

```go
func localRelayListeners(listeners []model.RelayListener, agentID, agentName string) []model.RelayListener
func cloneRelayListeners(listeners []model.RelayListener) []model.RelayListener
func relayListenerBindHosts(listener model.RelayListener) []string
```

Use imports for `strings` and `model`.

- [ ] **Step 4: Move binding helpers to `bindings.go`**

Create `go-agent/internal/modules/relay/bindings.go` and move:

```go
func relayListenerBindingKeys(listeners []model.RelayListener) []string
func serverBindingKeys(server *Server) []string
func bindingKeysOverlap(left, right []string) bool
type bindingKey struct
func parseBindingKey(raw string) (bindingKey, bool)
func (k bindingKey) overlaps(other bindingKey) bool
func normalizeBindingHost(host string) string
func bindingHostsEquivalent(left, right string) bool
func isLoopbackBindingHost(host string) bool
func bindingHostIsWildcard(host string) bool
func relayModuleValueOrZero(value *int) int
func relayListenerBindingProtocol(transportMode string) string
```

Use imports for `net`, `strconv`, `strings`, and `model`.

- [ ] **Step 5: Keep relay lifecycle and validation in `module.go`**

After moves, `go-agent/internal/modules/relay/module.go` should keep:

```go
const ProviderRuntime module.ProviderRef
type Config struct
type Module struct
func NewModule(cfg Config) *Module
func (m *Module) Name() string
func (m *Module) Descriptor() module.ModuleDescriptor
func (m *Module) RegisterProviders(reg module.ProviderRegistry) error
func (m *Module) Capabilities(module.SnapshotView) []module.Capability
func (m *Module) Apply(ctx context.Context, req module.ApplyRequest) error
func (m *Module) Prepare(ctx context.Context, req module.ApplyRequest) (module.ModuleTransaction, error)
func (m *Module) Stop(context.Context) error
func (m *Module) Close() error
func (m *Module) buildRuntime(...)
func (m *Module) buildRuntimeForListeners(...)
func (m *Module) restoreRuntime(...)
func relayEffectiveInputsEqual(...)
func outboundProxyURLTransaction(...)
func combineRelayTransactions(...)
type rollbackOverlayRestorer interface
func restoreOverlayForRollback(...)
func hasWireGuardRelayListener(...)
func validateRelayListeners(...)
func (m *Module) UpdateTrafficBlockState(...)
func (m *Module) currentTrafficBlockState() TrafficBlockState
```

- [ ] **Step 6: Run relay tests and commit**

Run:

```powershell
gofmt -w go-agent/internal/modules/relay/module.go go-agent/internal/modules/relay/providers.go go-agent/internal/modules/relay/bindings.go go-agent/internal/modules/relay/clones.go
go test ./internal/modules/relay
```

Expected: package passes. If a transient port reservation error appears, rerun once and record both outputs.

Commit:

```powershell
git add go-agent/internal/modules/relay
git commit -m "refactor(agent): split relay module support code"
```

## Task 7: Final Verification and Scope Audit

**Files:**
- Verify all changed files.

- [ ] **Step 1: Search for removed utility and forwarding symbols**

Run:

```powershell
rg -n "agentutil|internal/agentutil|CloneStringMap|EnsureStringMap|ParseInt64Default|func mergeTrafficStats|func shouldReportTrafficStats|func hasTrafficStatsInterval|func parseTrafficStatsInterval|func setTrafficStatsIntervalMetadata|func setTrafficBlockedMetadata|func ensureMetadata\\(" go-agent
```

Expected: no `agentutil` references. `ensureMetadata` may remain only in `core` if it is package-local sync metadata logic.

- [ ] **Step 2: Run focused tests**

Run:

```powershell
go test ./embedded ./internal/app ./internal/core ./internal/modules/traffic ./internal/modules/http ./internal/modules/l4 ./internal/modules/relay
```

Expected: all listed packages pass.

- [ ] **Step 3: Run full agent test suite**

Run:

```powershell
go test ./...
```

Expected: every `go-agent` package passes.

- [ ] **Step 4: Inspect diff stats for scope**

Run:

```powershell
git diff --stat 9d51c09e..HEAD -- go-agent
git diff --name-status 9d51c09e..HEAD -- go-agent
```

Expected: changed files are limited to `agentutil` removal, app/core sync readability, and HTTP/L4/Relay module support-file splits.

- [ ] **Step 5: Commit final cleanup if needed**

If gofmt or final verification creates changes, commit them:

```powershell
git add go-agent
git commit -m "test(agent): verify core readability refactor"
```

If the worktree is clean, no final commit is needed.
