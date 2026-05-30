# go-agent Core Plugin Restructure Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restructure `go-agent` into a single binary with a clear `internal/core` orchestration boundary, stable `internal/module` contracts, and optional first-party modules for WireGuard, traffic stats, certs, diagnostics, and egress while preserving external behavior.

**Architecture:** `internal/app` remains the bootstrap/composition layer. `internal/core` owns sync/apply/runtime-state orchestration and snapshot activation decisions for certs, agent config, HTTP/L4/relay, module activation, rollback, and capability construction. `internal/module` defines stable module contracts and ordered registration; `internal/modules/*` provides adapters that wrap existing implementation packages without changing control-plane protocol semantics.

**Tech Stack:** Go 1.x, standard `testing`, existing `go-agent/internal/{model,store,runtime,sync,update,traffic,wireguard,certs,diagnostics,egress}` packages.

---

## Phase 1: Establish Core And Module Boundaries

### Task 1: Add Stable Module Contracts And Registry

**Files:**
- Create: `go-agent/internal/module/module.go`
- Create: `go-agent/internal/module/registry.go`
- Test: `go-agent/internal/module/registry_test.go`

- [ ] **Step 1: Write failing registry tests**

Create `go-agent/internal/module/registry_test.go` with tests that prove deterministic registration order, duplicate rejection, capability aggregation, and health aggregation:

```go
package module

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

type testModule struct {
	name         string
	capabilities []Capability
	health       Health
	startErr     error
	stopErr      error
	starts       int
	stops        int
}

func (m *testModule) Name() string { return m.name }
func (m *testModule) Capabilities() []Capability {
	return append([]Capability(nil), m.capabilities...)
}
func (m *testModule) Health(context.Context) Health { return m.health }
func (m *testModule) Start(context.Context, model.Snapshot) error {
	m.starts++
	return m.startErr
}
func (m *testModule) Stop(context.Context) error {
	m.stops++
	return m.stopErr
}

func TestRegistryOrdersModulesAndAggregatesCapabilities(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(&testModule{name: "traffic", capabilities: []Capability{{Name: "traffic_stats"}}}); err != nil {
		t.Fatalf("Register traffic: %v", err)
	}
	if err := registry.Register(&testModule{name: "wireguard", capabilities: []Capability{{Name: "wireguard"}}}); err != nil {
		t.Fatalf("Register wireguard: %v", err)
	}

	names := registry.Names()
	if !reflect.DeepEqual(names, []string{"traffic", "wireguard"}) {
		t.Fatalf("Names() = %+v", names)
	}

	capabilities := registry.Capabilities()
	if got, want := capabilityNames(capabilities), []string{"traffic_stats", "wireguard"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Capabilities() = %+v, want %+v", got, want)
	}
}

func TestRegistryRejectsDuplicateAndBlankNames(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(&testModule{name: ""}); !errors.Is(err, ErrInvalidModule) {
		t.Fatalf("blank Register() error = %v, want ErrInvalidModule", err)
	}
	if err := registry.Register(&testModule{name: "wireguard"}); err != nil {
		t.Fatalf("Register wireguard: %v", err)
	}
	if err := registry.Register(&testModule{name: " wireguard "}); !errors.Is(err, ErrDuplicateModule) {
		t.Fatalf("duplicate Register() error = %v, want ErrDuplicateModule", err)
	}
}

func TestRegistryStartsAndStopsInStableOrder(t *testing.T) {
	first := &testModule{name: "certs"}
	second := &testModule{name: "traffic"}
	registry := NewRegistry()
	_ = registry.Register(first)
	_ = registry.Register(second)

	if err := registry.StartAll(context.Background(), model.Snapshot{Revision: 7}); err != nil {
		t.Fatalf("StartAll() error = %v", err)
	}
	if first.starts != 1 || second.starts != 1 {
		t.Fatalf("starts first=%d second=%d", first.starts, second.starts)
	}
	if err := registry.StopAll(context.Background()); err != nil {
		t.Fatalf("StopAll() error = %v", err)
	}
	if first.stops != 1 || second.stops != 1 {
		t.Fatalf("stops first=%d second=%d", first.stops, second.stops)
	}
}

func capabilityNames(capabilities []Capability) []string {
	names := make([]string, 0, len(capabilities))
	for _, capability := range capabilities {
		names = append(names, capability.Name)
	}
	return names
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd go-agent && go test ./internal/module`

Expected: FAIL because `internal/module` does not exist.

- [ ] **Step 3: Add contract and registry implementation**

Create `go-agent/internal/module/module.go`:

```go
package module

import (
	"context"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

type Capability struct {
	Name     string
	Enabled  bool
	Metadata map[string]string
}

type Health struct {
	Status  string
	Message string
}

type Module interface {
	Name() string
	Capabilities() []Capability
	Health(context.Context) Health
	Start(context.Context, model.Snapshot) error
	Stop(context.Context) error
}

type Activator interface {
	Activate(context.Context, model.Snapshot) error
}
```

Create `go-agent/internal/module/registry.go`:

```go
package module

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

var (
	ErrInvalidModule    = errors.New("invalid module")
	ErrDuplicateModule  = errors.New("duplicate module")
)

type Registry struct {
	modules []Module
	byName  map[string]Module
}

func NewRegistry() *Registry {
	return &Registry{byName: make(map[string]Module)}
}

func (r *Registry) Register(module Module) error {
	if module == nil {
		return fmt.Errorf("%w: nil module", ErrInvalidModule)
	}
	name := strings.TrimSpace(module.Name())
	if name == "" {
		return fmt.Errorf("%w: blank name", ErrInvalidModule)
	}
	if r.byName == nil {
		r.byName = make(map[string]Module)
	}
	key := strings.ToLower(name)
	if _, exists := r.byName[key]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateModule, name)
	}
	r.modules = append(r.modules, module)
	r.byName[key] = module
	return nil
}

func (r *Registry) Modules() []Module {
	if r == nil || len(r.modules) == 0 {
		return nil
	}
	return append([]Module(nil), r.modules...)
}

func (r *Registry) Names() []string {
	modules := r.Modules()
	names := make([]string, 0, len(modules))
	for _, module := range modules {
		names = append(names, strings.TrimSpace(module.Name()))
	}
	return names
}

func (r *Registry) Capabilities() []Capability {
	modules := r.Modules()
	var capabilities []Capability
	for _, module := range modules {
		capabilities = append(capabilities, module.Capabilities()...)
	}
	return capabilities
}

func (r *Registry) StartAll(ctx context.Context, snapshot model.Snapshot) error {
	for _, module := range r.Modules() {
		if err := module.Start(ctx, snapshot); err != nil {
			return fmt.Errorf("module %s start: %w", strings.TrimSpace(module.Name()), err)
		}
	}
	return nil
}

func (r *Registry) StopAll(ctx context.Context) error {
	var firstErr error
	for i := len(r.modules) - 1; i >= 0; i-- {
		module := r.modules[i]
		if err := module.Stop(ctx); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("module %s stop: %w", strings.TrimSpace(module.Name()), err)
		}
	}
	return firstErr
}
```

- [ ] **Step 4: Run module tests**

Run: `cd go-agent && go test ./internal/module`

Expected: PASS.

- [ ] **Step 5: Commit**

Run:

```sh
git add go-agent/internal/module
git commit -m "feat(agent): add module registry contracts"
```

### Task 2: Move Capability Construction Into Core

**Files:**
- Create: `go-agent/internal/core/capabilities.go`
- Test: `go-agent/internal/core/capabilities_test.go`
- Modify: `go-agent/internal/app/app.go`
- Modify: `go-agent/internal/app/app_test.go`

- [ ] **Step 1: Write failing core capability tests**

Create `go-agent/internal/core/capabilities_test.go` to verify default capabilities, conditional WireGuard, conditional HTTP/3, and module capability extension:

```go
package core

import (
	"reflect"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/config"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
)

func TestCapabilitiesPreserveExistingAdvertisedValues(t *testing.T) {
	cfg := config.Default()
	cfg.WireGuardEnabled = true
	cfg.WireGuardExplicit = true
	cfg.HTTP3Enabled = true

	got := CapabilityNames(cfg, nil)
	want := []string{"http_rules", "cert_install", "local_acme", "l4", "relay_quic", "wireguard", "egress_profiles", "http3_ingress"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CapabilityNames() = %+v, want %+v", got, want)
	}
}

func TestCapabilitiesAppendModuleCapabilitiesInRegistryOrder(t *testing.T) {
	cfg := config.Default()
	cfg.WireGuardEnabled = false
	cfg.WireGuardExplicit = true
	registry := module.NewRegistry()
	_ = registry.Register(staticModule{name: "traffic", capabilities: []module.Capability{{Name: "traffic_stats", Enabled: true}}})

	got := CapabilityNames(cfg, registry)
	want := []string{"http_rules", "cert_install", "local_acme", "l4", "relay_quic", "egress_profiles", "traffic_stats"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CapabilityNames() = %+v, want %+v", got, want)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd go-agent && go test ./internal/core`

Expected: FAIL because `internal/core` does not exist.

- [ ] **Step 3: Implement core capability builder and app delegation**

Create `go-agent/internal/core/capabilities.go`:

```go
package core

import (
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/config"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
)

type CapabilityRegistry interface {
	Capabilities() []module.Capability
}

func CapabilityNames(cfg config.Config, registry CapabilityRegistry) []string {
	capabilities := []string{"http_rules", "cert_install", "local_acme", "l4", "relay_quic"}
	if cfg.WireGuardModuleEnabled() {
		capabilities = append(capabilities, "wireguard")
	}
	capabilities = append(capabilities, "egress_profiles")
	if cfg.HTTP3Enabled {
		capabilities = append(capabilities, "http3_ingress")
	}
	if registry != nil {
		for _, capability := range registry.Capabilities() {
			name := strings.TrimSpace(capability.Name)
			if name != "" && capability.Enabled {
				capabilities = append(capabilities, name)
			}
		}
	}
	return capabilities
}
```

Modify `go-agent/internal/app/app.go` so `advertisedCapabilities` calls `core.CapabilityNames(cfg, nil)`. Keep the function name for existing tests:

```go
func advertisedCapabilities(cfg Config) []string {
	return core.CapabilityNames(cfg, nil)
}
```

- [ ] **Step 4: Run capability and app tests**

Run: `cd go-agent && go test ./internal/core ./internal/app`

Expected: PASS.

- [ ] **Step 5: Commit**

Run:

```sh
git add go-agent/internal/core go-agent/internal/app/app.go go-agent/internal/app/app_test.go
git commit -m "feat(agent): move capability construction into core"
```

### Task 3: Move Snapshot Activation Decisions Into Core

**Files:**
- Create: `go-agent/internal/core/activation.go`
- Create: `go-agent/internal/core/activation_test.go`
- Modify: `go-agent/internal/app/snapshot_activation.go`
- Modify: `go-agent/internal/runtime/runtime.go`
- Modify: `go-agent/internal/runtime/activation_test.go`

- [ ] **Step 1: Write failing core activation tests**

Create `go-agent/internal/core/activation_test.go` with coverage for activation order, WireGuard/egress dependent refresh, relay listener localization, and skipped unrelated relay changes. The test handlers should append strings such as `certs`, `agent_config`, `http`, `l4`, and `relay` and assert the order `certs -> agent_config -> http -> l4 -> relay`.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd go-agent && go test ./internal/core -run TestSnapshotActivator`

Expected: FAIL until `core.NewSnapshotActivator` exists.

- [ ] **Step 3: Implement core activation**

Move the snapshot decision logic currently in `go-agent/internal/app/snapshot_activation.go` into `go-agent/internal/core/activation.go`. The exported API should be:

```go
type SnapshotActivationHandlers struct {
	ActivateAgentConfig         func(context.Context, model.AgentConfig) error
	ActivateManagedCertificates func(context.Context, []model.ManagedCertificateBundle, []model.ManagedCertificatePolicy) error
	ActivateHTTPRules           func(context.Context, SnapshotHTTPInput) error
	ActivateL4Rules             func(context.Context, SnapshotL4Input) error
	ActivateRelayListeners      func(context.Context, SnapshotRelayInput) error
}

type SnapshotHTTPInput struct {
	Rules             []model.HTTPRule
	RelayListeners    []model.RelayListener
	WireGuardProfiles []model.WireGuardProfile
	EgressProfiles    []model.EgressProfile
}

type SnapshotL4Input struct {
	Rules             []model.L4Rule
	RelayListeners    []model.RelayListener
	WireGuardProfiles []model.WireGuardProfile
	EgressProfiles    []model.EgressProfile
}

type SnapshotRelayInput struct {
	RelayListeners    []model.RelayListener
	WireGuardProfiles []model.WireGuardProfile
	EgressProfiles    []model.EgressProfile
}

func NewSnapshotActivator(agentID string, agentName string, handlers SnapshotActivationHandlers) runtime.Activator
```

`internal/app/snapshot_activation.go` should keep only adapter functions from `App` dependencies to these handlers. `internal/runtime/runtime.go` should stop owning HTTP/L4/relay snapshot diff rules except generic runtime state and rollback semantics.

- [ ] **Step 4: Run activation tests**

Run: `cd go-agent && go test ./internal/core ./internal/runtime ./internal/app`

Expected: PASS.

- [ ] **Step 5: Commit**

Run:

```sh
git add go-agent/internal/core go-agent/internal/app/snapshot_activation.go go-agent/internal/runtime
git commit -m "feat(agent): move snapshot activation decisions into core"
```

### Task 4: Move Sync Apply/Persistence Orchestration Into Core

**Files:**
- Create: `go-agent/internal/core/sync_controller.go`
- Create: `go-agent/internal/core/sync_controller_test.go`
- Modify: `go-agent/internal/app/app.go`
- Modify: `go-agent/internal/app/sync_runtime_state.go`
- Modify: `go-agent/internal/app/app_test.go`

- [ ] **Step 1: Write failing core sync controller tests**

Create tests that cover: successful sync persists desired/applied/runtime state; apply failure rolls runtime back and records `last_apply_status=error`; applied save failure rolls runtime back and restores previous applied snapshot; update package staging returns restart without applying snapshot.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd go-agent && go test ./internal/core -run TestSyncController`

Expected: FAIL until `core.SyncController` exists.

- [ ] **Step 3: Implement core sync controller**

Move `syncOnce`, runtime state persistence helpers, snapshot merge, runtime rollback, and update handling from `internal/app` into `internal/core`. Keep app-owned dependencies passed through a constructor:

```go
type SyncController struct {
	Store      store.Store
	Runtime    *runtime.Runtime
	SyncClient SyncClient
	Updater    Updater
	Traffic    TrafficReporter
	CertReports ManagedCertificateReporter
}

func (c *SyncController) PerformSync(context.Context, sync.SyncRequest) error
func (c *SyncController) BuildSyncRequest(context.Context, model.Snapshot) (sync.SyncRequest, error)
```

`App.performSync` should load the applied snapshot, call the controller to build the sync request, and call `PerformSync`.

- [ ] **Step 4: Run core and app sync tests**

Run: `cd go-agent && go test ./internal/core ./internal/app`

Expected: PASS.

- [ ] **Step 5: Commit**

Run:

```sh
git add go-agent/internal/core go-agent/internal/app
git commit -m "feat(agent): move sync orchestration into core"
```

## Phase 2: Move First Optional Modules

### Task 5: Extract WireGuard Runtime Module Adapter

**Files:**
- Create: `go-agent/internal/modules/wireguard/module.go`
- Create: `go-agent/internal/modules/wireguard/runtime.go`
- Test: `go-agent/internal/modules/wireguard/runtime_test.go`
- Modify: `go-agent/internal/app/relay_runtime.go`
- Modify: `go-agent/internal/app/http_runtime.go`
- Modify: `go-agent/internal/app/l4_runtime.go`
- Modify: `go-agent/internal/app/app.go`
- Modify: `go-agent/internal/app/relay_runtime_test.go`

- [ ] **Step 1: Write failing module tests**

Create tests proving the module prepares/commits/rolls back transactions, exposes `relay.WireGuardRuntimeProvider`, filters runtime lookup by local agent ID, and closes the underlying manager.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd go-agent && go test ./internal/modules/wireguard`

Expected: FAIL until the package exists.

- [ ] **Step 3: Move shared WireGuard runtime code**

Move `sharedWireGuardRuntime`, `wireGuardRuntimeProvider`, `wireGuardTransactionProvider`, `wireGuardProfileForRelayHop`, `wireGuardProfileRoutesRelayHop`, and `cloneWireGuardProfiles` from `internal/app/relay_runtime.go` into `internal/modules/wireguard/runtime.go`. Import it in app as an aliased package, for example `modulewireguard`.

- [ ] **Step 4: Register module in app bootstrap**

Construct the WireGuard module in `New`, register it in the module registry when `cfg.WireGuardModuleEnabled()` is true, and pass its runtime/provider to HTTP/L4/relay managers. Preserve the existing advertised `wireguard` capability name.

- [ ] **Step 5: Run focused tests**

Run: `cd go-agent && go test ./internal/modules/wireguard ./internal/app ./internal/relay ./internal/proxy ./internal/l4`

Expected: PASS.

- [ ] **Step 6: Commit**

Run:

```sh
git add go-agent/internal/modules/wireguard go-agent/internal/app
git commit -m "feat(agent): extract wireguard runtime module"
```

### Task 6: Extract Traffic Stats Module Adapter

**Files:**
- Create: `go-agent/internal/modules/traffic/module.go`
- Create: `go-agent/internal/modules/traffic/reporter.go`
- Test: `go-agent/internal/modules/traffic/reporter_test.go`
- Modify: `go-agent/internal/app/app.go`
- Modify: `go-agent/internal/app/sync_runtime_state.go`
- Modify: `go-agent/internal/app/app_test.go`

- [ ] **Step 1: Write failing traffic module tests**

Cover disabled traffic reporting, interval throttling via runtime metadata, host traffic merge, pending report timestamp persistence, and `traffic_stats` capability.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd go-agent && go test ./internal/modules/traffic`

Expected: FAIL until the package exists.

- [ ] **Step 3: Move traffic sync helpers**

Move traffic-specific helpers from `internal/app/sync_runtime_state.go` into `internal/modules/traffic`: `shouldReportTrafficStats`, `hasTrafficStatsInterval`, `parseTrafficStatsInterval`, `setTrafficStatsIntervalMetadata`, host traffic merge, and pending timestamp handling. Keep runtime state metadata keys stable.

- [ ] **Step 4: Wire traffic reporter into core sync controller**

`core.SyncController.BuildSyncRequest` should call the traffic module reporter instead of directly reaching into `internal/traffic` or `hosttraffic`.

- [ ] **Step 5: Run focused tests**

Run: `cd go-agent && go test ./internal/modules/traffic ./internal/core ./internal/app ./internal/traffic ./internal/hosttraffic`

Expected: PASS.

- [ ] **Step 6: Commit**

Run:

```sh
git add go-agent/internal/modules/traffic go-agent/internal/core go-agent/internal/app
git commit -m "feat(agent): extract traffic stats module"
```

## Phase 3: Move Remaining Adapters And Tighten Tests

### Task 7: Add Certs, Diagnostics, And Egress Module Adapters

**Files:**
- Create: `go-agent/internal/modules/certs/module.go`
- Create: `go-agent/internal/modules/diagnostics/module.go`
- Create: `go-agent/internal/modules/egress/module.go`
- Test: `go-agent/internal/modules/certs/module_test.go`
- Test: `go-agent/internal/modules/diagnostics/module_test.go`
- Test: `go-agent/internal/modules/egress/module_test.go`
- Modify: `go-agent/internal/app/app.go`
- Modify: `go-agent/internal/app/snapshot_activation.go`
- Modify: `go-agent/internal/app/relay_runtime.go`

- [ ] **Step 1: Write adapter tests**

Cert tests should prove Apply and ManagedCertificateReports delegate to `certs.Manager`. Diagnostics tests should prove existing task handler/probers are exposed without changing `Diagnose` and `DiagnoseSnapshot` behavior. Egress tests should prove final-hop dialer and inline WireGuard egress profile runtime wiring are available through the module adapter.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd go-agent && go test ./internal/modules/certs ./internal/modules/diagnostics ./internal/modules/egress`

Expected: FAIL until the packages exist.

- [ ] **Step 3: Implement thin adapters**

Adapters must wrap existing packages without changing protocol shape or moving business logic wholesale. `internal/app` should instantiate these adapters and hand their narrow interfaces to `internal/core`.

- [ ] **Step 4: Run focused tests**

Run: `cd go-agent && go test ./internal/modules/certs ./internal/modules/diagnostics ./internal/modules/egress ./internal/app ./embedded`

Expected: PASS.

- [ ] **Step 5: Commit**

Run:

```sh
git add go-agent/internal/modules go-agent/internal/app go-agent/embedded
git commit -m "feat(agent): add remaining module adapters"
```

### Task 8: Shrink App Tests To Composition And Compatibility Boundaries

**Files:**
- Modify: `go-agent/internal/app/app_test.go`
- Modify: `go-agent/internal/app/relay_runtime_test.go`
- Modify: `go-agent/internal/core/*_test.go`
- Modify: `go-agent/internal/modules/**/*_test.go`

- [ ] **Step 1: Inventory duplicate tests**

Use:

```sh
rg -n "TestSnapshotActivator|TestRunRecords|TestRunPersists|TestRunMerges|TestRunDoesNotApply|TestRunAppliesExplicit" go-agent/internal/app go-agent/internal/core go-agent/internal/modules
```

Move behavior tests to `core` or module packages when they assert core/module behavior. Keep app tests only when they assert `New`, env/config compatibility, embedded semantics, runtime cache sharing, task client wiring, and close semantics.

- [ ] **Step 2: Run app/core/modules tests after each moved group**

Run: `cd go-agent && go test ./internal/app ./internal/core ./internal/modules/...`

Expected: PASS.

- [ ] **Step 3: Commit**

Run:

```sh
git add go-agent/internal/app go-agent/internal/core go-agent/internal/modules
git commit -m "test(agent): focus app tests on composition boundaries"
```

### Task 9: Final Compatibility Verification And Review Fixes

**Files:**
- Modify files identified by final review.

- [ ] **Step 1: Run full agent tests**

Run: `cd go-agent && go test ./...`

Expected: PASS.

- [ ] **Step 2: Run image-impacting verification**

Run from repo root: `docker build -t nginx-reverse-emby .`

Expected: PASS. If Docker is unavailable, record the exact error and run `cd panel/backend-go && go test ./...` plus `cd panel/frontend && npm run build` as fallback evidence.

- [ ] **Step 3: Dispatch final reviewer**

Ask a fresh review agent to compare the implementation against `docs/superpowers/specs/2026-05-30-go-agent-core-plugin-restructure-design.md` and this plan. Require findings first, ordered by severity, with file/line references.

- [ ] **Step 4: Fix every Critical and Important review issue**

Use targeted tests for each fix, then rerun `cd go-agent && go test ./...`.

- [ ] **Step 5: Commit review fixes**

Run:

```sh
git add go-agent docs/superpowers/plans/2026-05-30-go-agent-core-plugin-restructure-plan.md
git commit -m "fix(agent): address core module restructure review"
```

## Completion Audit

- `internal/app` is bootstrap/composition only; no large snapshot diff/sync persistence business flow remains there.
- `internal/core` owns capability construction, snapshot activation decisions, sync apply/persist/rollback orchestration, and runtime-state apply metadata.
- `internal/module` defines stable module contracts and ordered registry semantics.
- `internal/modules/wireguard` and `internal/modules/traffic` are real optional module adapters with focused tests.
- `internal/modules/certs`, `internal/modules/diagnostics`, and `internal/modules/egress` exist as adapter layers, even if they delegate to existing implementation packages.
- Existing external behavior is preserved: environment variables, control-plane API semantics, embedded runtime inputs/outputs, and core runtime state fields.
- Verification evidence includes fresh `cd go-agent && go test ./...` output and final review with all Critical/Important issues fixed.
