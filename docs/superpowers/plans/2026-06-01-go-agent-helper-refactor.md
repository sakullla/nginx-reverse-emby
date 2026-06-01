# go-agent Helper Refactor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reduce low-value `go-agent` code by deleting unused registry compatibility paths, extracting narrow shared helpers, and removing thin copy wrappers without changing runtime behavior.

**Architecture:** Add a dependency-free `internal/agentutil` package for cross-package primitive helpers. Keep traffic copy behavior in `internal/modules/traffic`, and make L4/relay call that package directly unless local relay-specific behavior is required. Remove registry legacy adaptation so the registry only accepts the current `module.Module` contract.

**Tech Stack:** Go 1.22+ module code, standard `testing` package, existing `go test ./...` verification.

---

## File Structure

- Create: `go-agent/internal/agentutil/maps.go`
  - Owns dependency-free `map[string]string` helpers.
- Create: `go-agent/internal/agentutil/maps_test.go`
  - Verifies clone nil behavior, deep copy isolation, and ensure behavior.
- Create: `go-agent/internal/agentutil/parse.go`
  - Owns dependency-free primitive parsing helpers.
- Create: `go-agent/internal/agentutil/parse_test.go`
  - Verifies integer parsing fallback behavior.
- Modify: `go-agent/internal/app/sync_runtime_state.go`
  - Replaces local metadata and parse helpers with `agentutil`.
- Modify: `go-agent/internal/core/store.go`
  - Replaces local runtime metadata clone helper with `agentutil.CloneStringMap`.
- Modify: `go-agent/internal/core/sync_controller.go`
  - Replaces local metadata and parse helpers with `agentutil`.
- Modify: `go-agent/internal/modules/traffic/module.go`
  - Replaces local map clone helper with `agentutil.CloneStringMap`.
- Modify: `go-agent/embedded/runtime.go`
  - Replaces local runtime metadata clone loop with `agentutil.CloneStringMap`.
- Modify: `go-agent/internal/module/module.go`
  - Deletes `LegacyModule`.
- Modify: `go-agent/internal/module/registry.go`
  - Changes `Register` to accept `Module`; deletes adapter and `StartAll`.
- Modify: `go-agent/internal/module/registry_test.go`
  - Updates tests for the narrowed registry API and removes legacy-only coverage.
- Modify: package tests under `go-agent/internal/modules/{diagnostics,http,l4,relay,traffic}` and `go-agent/internal/app`
  - Updates helper signatures from `any` to `module.Module` where the production API is narrowed.
- Modify: `go-agent/internal/modules/l4/tcp.go`
  - Calls traffic copy helper directly.
- Delete: `go-agent/internal/modules/l4/copy.go`
  - Removes one-line wrapper.
- Modify: `go-agent/internal/modules/l4/copy_test.go`
  - Deletes or moves wrapper-only coverage to traffic tests.
- Modify: `go-agent/internal/modules/relay/copy.go`
  - Keeps relay-specific `copyRelayTraffic`, removes thin generic wrappers.
- Modify: `go-agent/internal/modules/relay/*.go`
  - Replaces `copyGeneric` and `copyPreferReaderFrom` calls with traffic package calls.

## Task 1: Add Narrow Shared Helpers

**Files:**
- Create: `go-agent/internal/agentutil/maps_test.go`
- Create: `go-agent/internal/agentutil/maps.go`
- Create: `go-agent/internal/agentutil/parse_test.go`
- Create: `go-agent/internal/agentutil/parse.go`

- [ ] **Step 1: Write failing map helper tests**

Create `go-agent/internal/agentutil/maps_test.go`:

```go
package agentutil

import "testing"

func TestCloneStringMapPreservesNil(t *testing.T) {
	if got := CloneStringMap(nil); got != nil {
		t.Fatalf("CloneStringMap(nil) = %#v, want nil", got)
	}
}

func TestCloneStringMapIsIndependent(t *testing.T) {
	src := map[string]string{"alpha": "one"}

	got := CloneStringMap(src)
	src["alpha"] = "changed"
	got["beta"] = "two"

	if got["alpha"] != "one" {
		t.Fatalf("clone alpha = %q, want one", got["alpha"])
	}
	if _, exists := src["beta"]; exists {
		t.Fatalf("source received clone mutation: %#v", src)
	}
}

func TestEnsureStringMap(t *testing.T) {
	created := EnsureStringMap(nil)
	if created == nil {
		t.Fatal("EnsureStringMap(nil) = nil, want empty map")
	}
	created["key"] = "value"

	existing := map[string]string{"same": "map"}
	if got := EnsureStringMap(existing); got["same"] != "map" {
		t.Fatalf("EnsureStringMap(existing) = %#v, want original contents", got)
	}
}
```

- [ ] **Step 2: Run map helper tests and verify they fail because functions are missing**

Run:

```powershell
go test ./internal/agentutil
```

Expected: `FAIL` with compile errors containing `undefined: CloneStringMap` and `undefined: EnsureStringMap`.

- [ ] **Step 3: Implement map helpers**

Create `go-agent/internal/agentutil/maps.go`:

```go
package agentutil

func CloneStringMap(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func EnsureStringMap(src map[string]string) map[string]string {
	if src == nil {
		return make(map[string]string)
	}
	return src
}
```

- [ ] **Step 4: Run map helper tests and verify they pass**

Run:

```powershell
go test ./internal/agentutil
```

Expected: `ok github.com/sakullla/nginx-reverse-emby/go-agent/internal/agentutil`.

- [ ] **Step 5: Write failing parse helper tests**

Create `go-agent/internal/agentutil/parse_test.go`:

```go
package agentutil

import "testing"

func TestParseInt64Default(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		fallback int64
		want     int64
	}{
		{name: "valid", raw: "42", fallback: 7, want: 42},
		{name: "trimmed", raw: " 9 ", fallback: 7, want: 9},
		{name: "blank", raw: "", fallback: 7, want: 7},
		{name: "invalid", raw: "nope", fallback: 7, want: 7},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ParseInt64Default(tt.raw, tt.fallback); got != tt.want {
				t.Fatalf("ParseInt64Default(%q, %d) = %d, want %d", tt.raw, tt.fallback, got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 6: Run parse helper tests and verify they fail because function is missing**

Run:

```powershell
go test ./internal/agentutil
```

Expected: `FAIL` with compile error containing `undefined: ParseInt64Default`.

- [ ] **Step 7: Implement parse helper**

Create `go-agent/internal/agentutil/parse.go`:

```go
package agentutil

import (
	"strconv"
	"strings"
)

func ParseInt64Default(raw string, fallback int64) int64 {
	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return fallback
	}
	return value
}
```

- [ ] **Step 8: Run helper tests and commit**

Run:

```powershell
go test ./internal/agentutil
```

Expected: package passes.

Commit:

```powershell
git add go-agent/internal/agentutil
git commit -m "refactor(agent): add shared primitive helpers"
```

## Task 2: Replace Duplicated Metadata Helpers

**Files:**
- Modify: `go-agent/internal/app/sync_runtime_state.go`
- Modify: `go-agent/internal/core/store.go`
- Modify: `go-agent/internal/core/sync_controller.go`
- Modify: `go-agent/internal/modules/traffic/module.go`
- Modify: `go-agent/embedded/runtime.go`

- [ ] **Step 1: Run current package tests before replacement**

Run:

```powershell
go test ./internal/app ./internal/core ./internal/modules/traffic ./embedded
```

Expected: all listed packages pass before edits.

- [ ] **Step 2: Replace app helpers with `agentutil`**

In `go-agent/internal/app/sync_runtime_state.go`, add the import:

```go
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/agentutil"
```

Change:

```go
a.pendingSyncMetadata = copyStringMap(plan.RuntimeMetadata)
metadata := copyStringMap(a.pendingSyncMetadata)
return parseInt64(raw, fallback)
```

to:

```go
a.pendingSyncMetadata = agentutil.CloneStringMap(plan.RuntimeMetadata)
metadata := agentutil.CloneStringMap(a.pendingSyncMetadata)
return agentutil.ParseInt64Default(raw, fallback)
```

Change `ensureMetadata` to:

```go
func ensureMetadata(meta map[string]string) map[string]string {
	return agentutil.EnsureStringMap(meta)
}
```

Delete local `copyStringMap` and `parseInt64`. Remove no-longer-used `strconv` and `strings` imports.

- [ ] **Step 3: Replace core store metadata clone**

In `go-agent/internal/core/store.go`, add:

```go
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/agentutil"
```

Change both `copyStoreMetadata(...)` calls to `agentutil.CloneStringMap(...)`.

Delete local `copyStoreMetadata`.

- [ ] **Step 4: Replace core sync controller helpers**

In `go-agent/internal/core/sync_controller.go`, add:

```go
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/agentutil"
```

Change:

```go
meta := ensureMetadata(state.Metadata)
plan.Request.LastApplyRevision = int(parseInt64(meta["last_apply_revision"], applied.Revision))
plan.RuntimeMetadata = copyMetadata(report.RuntimeMetadata)
```

to:

```go
meta := ensureMetadata(state.Metadata)
plan.Request.LastApplyRevision = int(agentutil.ParseInt64Default(meta["last_apply_revision"], applied.Revision))
plan.RuntimeMetadata = agentutil.CloneStringMap(report.RuntimeMetadata)
```

Change `ensureMetadata` to:

```go
func ensureMetadata(meta map[string]string) map[string]string {
	return agentutil.EnsureStringMap(meta)
}
```

Delete local `parseInt64` and `copyMetadata`. Keep `strconv` because `setApplyMetadata` still uses `strconv.FormatInt`; keep `strings` because the file still trims and parses durations.

- [ ] **Step 5: Replace traffic module metadata clone**

In `go-agent/internal/modules/traffic/module.go`, add:

```go
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/agentutil"
```

Change each `copyStringMap(...)` call to `agentutil.CloneStringMap(...)`.

Delete local `copyStringMap`.

- [ ] **Step 6: Replace embedded runtime metadata clone**

In `go-agent/embedded/runtime.go`, add:

```go
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/agentutil"
```

Change `copyRuntimeState` to:

```go
func copyRuntimeState(state RuntimeState) RuntimeState {
	copyValue := state
	copyValue.Metadata = agentutil.CloneStringMap(state.Metadata)
	return copyValue
}
```

- [ ] **Step 7: Run focused tests**

Run:

```powershell
go test ./internal/app ./internal/core ./internal/modules/traffic ./embedded
```

Expected: all listed packages pass.

- [ ] **Step 8: Commit metadata helper replacement**

Commit:

```powershell
git add go-agent/internal/app/sync_runtime_state.go go-agent/internal/core/store.go go-agent/internal/core/sync_controller.go go-agent/internal/modules/traffic/module.go go-agent/embedded/runtime.go
git commit -m "refactor(agent): share metadata helpers"
```

## Task 3: Remove Module Registry Legacy Compatibility

**Files:**
- Modify: `go-agent/internal/module/module.go`
- Modify: `go-agent/internal/module/registry.go`
- Modify: `go-agent/internal/module/registry_test.go`
- Modify: test helper signatures in module registration tests that currently accept `any`.

- [ ] **Step 1: Add registry API compile expectation**

In `go-agent/internal/module/registry_test.go`, keep registration tests using `module.Module` helper signatures. Add this assertion near helper types:

```go
var _ module.Module = (*recordingModule)(nil)
```

This assertion passes before the production change and protects the replacement API from using legacy-only helper types.

- [ ] **Step 2: Run registry tests before deletion**

Run:

```powershell
go test ./internal/module
```

Expected: package passes before edits.

- [ ] **Step 3: Narrow `Register` to current modules**

In `go-agent/internal/module/registry.go`, change:

```go
func (r *Registry) Register(candidate any) error {
	module, err := adaptModule(candidate)
	if err != nil {
		return err
	}
	name, err := validateModule(module)
```

to:

```go
func (r *Registry) Register(module Module) error {
	name, err := validateModule(module)
```

Delete `adaptModule`, `legacyModuleAdapter`, and every method on `legacyModuleAdapter`.

- [ ] **Step 4: Delete legacy module interface**

In `go-agent/internal/module/module.go`, delete:

```go
type LegacyModule interface {
	Name() string
	Capabilities() []Capability
	Health(context.Context) Health
	Start(context.Context, model.Snapshot) error
	Stop(context.Context) error
}
```

Keep `Health` only if code still references it. If `rg -n "module.Health|Health\\{" go-agent/internal go-agent/embedded` finds no references after deletion, delete `type Health` too.

- [ ] **Step 5: Delete `StartAll` migration shim**

In `go-agent/internal/module/registry.go`, delete the full `StartAll` method:

```go
func (r *Registry) StartAll(ctx context.Context, snapshot model.Snapshot) error {
	...
}
```

Do not delete `StopAll`; it is still used by app shutdown and tests.

- [ ] **Step 6: Update tests that register `any` candidates**

Run:

```powershell
go test ./...
```

Expected: compile failures point at test helpers whose parameter type is still `any`.

For each helper that only registers current modules, change:

```go
func mustRegister(t *testing.T, registry *module.Registry, mod any) {
```

to:

```go
func mustRegister(t *testing.T, registry *module.Registry, mod module.Module) {
```

For tests that intentionally pass invalid non-module values, delete that assertion because `Register` no longer accepts non-module values at compile time.

- [ ] **Step 7: Run focused module and registry-dependent tests**

Run:

```powershell
go test ./internal/module ./internal/app ./internal/modules/certs ./internal/modules/diagnostics ./internal/modules/egress ./internal/modules/http ./internal/modules/l4 ./internal/modules/relay ./internal/modules/traffic ./internal/modules/wireguard
```

Expected: all listed packages pass.

- [ ] **Step 8: Commit registry cleanup**

Commit:

```powershell
git add go-agent/internal/module/module.go go-agent/internal/module/registry.go go-agent/internal/module/registry_test.go go-agent/internal/app go-agent/internal/modules
git commit -m "refactor(agent): remove legacy module registry shim"
```

## Task 4: Remove Thin Copy Wrappers

**Files:**
- Modify: `go-agent/internal/modules/l4/tcp.go`
- Delete: `go-agent/internal/modules/l4/copy.go`
- Modify or delete: `go-agent/internal/modules/l4/copy_test.go`
- Modify: `go-agent/internal/modules/relay/copy.go`
- Modify: relay files that call `copyGeneric` or `copyPreferReaderFrom`.

- [ ] **Step 1: Run current copy tests**

Run:

```powershell
go test ./internal/modules/traffic ./internal/modules/l4 ./internal/modules/relay
```

Expected: all listed packages pass before edits.

- [ ] **Step 2: Replace L4 copy wrapper usage**

In `go-agent/internal/modules/l4/tcp.go`, ensure imports include traffic already. Change:

```go
return copyPreferReaderFrom(wrapped, src)
```

to:

```go
return traffic.CopyPreferReaderFrom(wrapped, src)
```

Delete `go-agent/internal/modules/l4/copy.go`.

- [ ] **Step 3: Remove wrapper-only L4 test**

Delete `go-agent/internal/modules/l4/copy_test.go` if its only assertion is that `copyPreferReaderFrom` forwards to traffic behavior. The traffic package already owns the copy behavior tests in `go-agent/internal/modules/traffic/stream_test.go`.

- [ ] **Step 4: Remove relay generic wrappers**

In `go-agent/internal/modules/relay/copy.go`, delete:

```go
func copyPreferReaderFrom(dst io.Writer, src io.Reader) (int64, error) {
	return traffic.CopyPreferReaderFrom(dst, src)
}

func copyGeneric(dst io.Writer, src io.Reader) (int64, error) {
	return traffic.CopyGeneric(dst, src)
}
```

Keep `copyRelayTraffic` and `shouldUseRelayDestinationReadFrom` because they contain relay-specific behavior.

- [ ] **Step 5: Replace relay wrapper calls**

Run:

```powershell
rg -n "copyGeneric|copyPreferReaderFrom" go-agent\internal\modules\relay
```

For production files, replace:

```go
copyGeneric(dst, src)
copyPreferReaderFrom(dst, src)
```

with:

```go
traffic.CopyGeneric(dst, src)
traffic.CopyPreferReaderFrom(dst, src)
```

Add the traffic import where needed:

```go
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/traffic"
```

Do not replace `copyRelayTraffic`.

- [ ] **Step 6: Run focused copy tests**

Run:

```powershell
go test ./internal/modules/traffic ./internal/modules/l4 ./internal/modules/relay
```

Expected: all listed packages pass.

- [ ] **Step 7: Commit copy wrapper cleanup**

Commit:

```powershell
git add go-agent/internal/modules/l4 go-agent/internal/modules/relay
git commit -m "refactor(agent): remove thin copy wrappers"
```

## Task 5: Final Verification and Cleanup

**Files:**
- Verify all changed files.

- [ ] **Step 1: Format changed Go files**

Run:

```powershell
gofmt -w go-agent/internal/agentutil go-agent/internal/app/sync_runtime_state.go go-agent/internal/core/store.go go-agent/internal/core/sync_controller.go go-agent/internal/modules/traffic/module.go go-agent/embedded/runtime.go go-agent/internal/module/module.go go-agent/internal/module/registry.go go-agent/internal/module/registry_test.go go-agent/internal/modules/l4 go-agent/internal/modules/relay
```

Expected: command exits with code 0.

- [ ] **Step 2: Search for deleted helper names**

Run:

```powershell
rg -n "LegacyModule|legacyModuleAdapter|StartAll\(|copyStoreMetadata|copyMetadata|copyStringMap|func copyPreferReaderFrom|func copyGeneric|parseInt64\(" go-agent
```

Expected: no production references. Test names or comments that refer to ordinary copy behavior are acceptable only if they do not reference deleted wrappers.

- [ ] **Step 3: Run full agent test suite**

Run:

```powershell
go test ./...
```

Expected: every package passes.

- [ ] **Step 4: Review diff for scope**

Run:

```powershell
git diff --stat HEAD
git diff -- go-agent/internal/agentutil go-agent/internal/module go-agent/internal/app/sync_runtime_state.go go-agent/internal/core/store.go go-agent/internal/core/sync_controller.go go-agent/internal/modules/traffic/module.go go-agent/embedded/runtime.go go-agent/internal/modules/l4 go-agent/internal/modules/relay
```

Expected: diff is limited to helper extraction, registry compatibility deletion, copy wrapper cleanup, and related tests.

- [ ] **Step 5: Commit final formatting or test-only cleanup if needed**

If Step 1 through Step 4 created uncommitted changes after Task 4, commit them:

```powershell
git add go-agent
git commit -m "test(agent): verify helper refactor cleanup"
```

If there are no uncommitted changes, no final commit is needed.
