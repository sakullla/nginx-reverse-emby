# go-agent Module Ownership Rebuild Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rebuild go-agent so `internal/core` is only lifecycle infrastructure, `internal/app` is only composition, and HTTP/L4/Relay/WireGuard/Egress/Diagnostics/Traffic/Certs are true owning modules.

**Architecture:** `internal/module` owns stable contracts, provider registry, dependency ordering, and registry execution. `internal/core` invokes the registry generically with previous/next snapshots and records runtime state. Business modules own their runtime managers and exchange behavior-named providers such as `tls.material`, `overlay.runtime`, `finalhop.dialer`, and `diagnostics.l4.source`.

**Tech Stack:** Go 1.x, standard `go test`, existing `go-agent/internal/model`, existing runtime implementations moved into `internal/modules/*`.

---

## Current Evidence

- Current package count from `cd go-agent && go list ./...`: 37 packages.
- Current internal test files: 86.
- Current business coupling:
  - `go-agent/internal/core/activation.go` imports `internal/l4` and `internal/relay`.
  - `go-agent/internal/app/app.go` creates `newSharedWireGuardRuntime()` and passes it into HTTP/L4/Relay managers.
  - `go-agent/internal/app/snapshot_activation.go` contains HTTP/L4/Relay/Cert/Traffic apply logic.
  - `go-agent/internal/modules/*` packages are mostly wrappers around app-owned or old-package-owned runtime objects.
- Final verification must prove:
  - `internal/core` imports no business modules.
  - `internal/app` creates no HTTP/L4/Relay/WireGuard/Egress/Diagnostics/Traffic/Certs runtime directly.
  - No non-WireGuard package imports or names `WireGuardRuntimeProvider`.
  - Old adapter package paths are deleted.
  - `cd go-agent && go test ./...` passes.

## File Structure Target

### Shared contracts

- Modify: `go-agent/internal/module/module.go`
  - Define `Module`, `ModuleDescriptor`, `ApplyRequest`, `ProviderRef`, `ProviderRegistry`, `ProviderResolver`, `TransactionalModule`, and `ModuleTransaction`.
- Modify: `go-agent/internal/module/registry.go`
  - Register modules, collect provider factories, validate required providers, topologically order modules, apply modules transactionally, and stop modules.
- Create: `go-agent/internal/module/providers.go`
  - Define behavior-named shared provider refs and provider interfaces.
- Modify: `go-agent/internal/module/registry_test.go`
  - Keep only dependency graph, provider resolution, rollback, duplicate module, and provider conflict coverage.

### Core lifecycle

- Replace: `go-agent/internal/core/activation.go`
  - Remove HTTP/L4/Relay-specific snapshot input structs and diff logic.
  - Provide generic module activator construction.
- Modify: `go-agent/internal/core/sync_controller.go`
  - Replace `ModuleLifecycle.StartAll` with module registry `Apply`.
- Modify: `go-agent/internal/core/capabilities.go`
  - Build capabilities from modules generically.
- Modify tests: `go-agent/internal/core/*_test.go`
  - Keep generic lifecycle, runtime rollback, capability, and sync-controller tests.

### Owning business modules

- Move into `go-agent/internal/modules/certs/`:
  - `go-agent/internal/certs/*.go`
  - existing `go-agent/internal/modules/certs/module.go`
- Move into `go-agent/internal/modules/wireguard/`:
  - `go-agent/internal/wireguard/*.go`
  - `go-agent/internal/wireguard/wgnetstack/*.go`
  - existing `go-agent/internal/modules/wireguard/*.go`
- Move into `go-agent/internal/modules/egress/`:
  - `go-agent/internal/egress/*.go`
  - existing `go-agent/internal/modules/egress/*.go`
- Move into `go-agent/internal/modules/relay/`:
  - `go-agent/internal/relay/*.go`
  - `go-agent/internal/relayplan/*.go`
  - `go-agent/internal/relayroute/*.go`
  - relay runtime manager currently in `go-agent/internal/app/relay_runtime.go`
- Move into `go-agent/internal/modules/http/`:
  - `go-agent/internal/proxy/*.go`
  - HTTP runtime manager currently in `go-agent/internal/app/http_runtime.go`
- Move into `go-agent/internal/modules/l4/`:
  - `go-agent/internal/l4/*.go`
  - L4 runtime manager currently in `go-agent/internal/app/l4_runtime.go`
  - shared local runtime helpers currently in `go-agent/internal/app/local_runtime.go` only if still needed by L4/HTTP/Relay after provider inversion.
- Move into `go-agent/internal/modules/diagnostics/`:
  - `go-agent/internal/diagnostics/*.go`
  - diagnostic task assembly currently in `go-agent/internal/app/app.go`
- Move into `go-agent/internal/modules/traffic/`:
  - `go-agent/internal/traffic/*.go`
  - `go-agent/internal/hosttraffic/*.go`
  - traffic report adapter currently in `go-agent/internal/modules/traffic/reporter.go`

### Composition only

- Modify: `go-agent/internal/app/app.go`
  - Construct config/store/sync/update/task clients.
  - Construct modules and register them.
  - Do not construct business runtime managers directly.
- Delete or shrink after migration:
  - `go-agent/internal/app/snapshot_activation.go`
  - `go-agent/internal/app/http_runtime.go`
  - `go-agent/internal/app/l4_runtime.go`
  - `go-agent/internal/app/relay_runtime.go`
  - `go-agent/internal/app/egress_wireguard.go`
  - `go-agent/internal/app/wireguard_runtime_compat.go`

---

## Phase 1: Contracts and Generic Lifecycle

### Task 1: Rewrite `internal/module` Contracts and Provider Graph

**Files:**
- Modify: `go-agent/internal/module/module.go`
- Modify: `go-agent/internal/module/registry.go`
- Create: `go-agent/internal/module/providers.go`
- Modify: `go-agent/internal/module/registry_test.go`

- [ ] **Step 1: Write provider and dependency graph tests**

Replace wrapper-oriented tests in `go-agent/internal/module/registry_test.go` with tests for descriptor validation, dependency order, provider resolution, and rollback. Include these test functions:

```go
func TestRegistryOrdersModulesByRequiredProviders(t *testing.T) {
	registry := module.NewRegistry()
	events := []string{}
	mustRegister(t, registry, &recordingModule{
		name:     "http",
		requires: []module.ProviderRef{module.ProviderTLSMaterial},
		apply: func(context.Context, module.ApplyRequest) error {
			events = append(events, "http")
			return nil
		},
	})
	mustRegister(t, registry, &recordingModule{
		name:     "certs",
		provides: []module.ProviderRef{module.ProviderTLSMaterial},
		register: func(reg module.ProviderRegistry) error {
			return reg.Provide(module.ProviderTLSMaterial, fakeTLSMaterial{})
		},
		apply: func(context.Context, module.ApplyRequest) error {
			events = append(events, "certs")
			return nil
		},
	})

	if err := registry.Apply(context.Background(), model.Snapshot{}, model.Snapshot{}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if got, want := strings.Join(events, ","), "certs,http"; got != want {
		t.Fatalf("apply order = %s, want %s", got, want)
	}
}

func TestRegistryRejectsMissingRequiredProvider(t *testing.T) {
	registry := module.NewRegistry()
	mustRegister(t, registry, &recordingModule{
		name:     "http",
		requires: []module.ProviderRef{module.ProviderTLSMaterial},
	})
	err := registry.Apply(context.Background(), model.Snapshot{}, model.Snapshot{})
	if !errors.Is(err, module.ErrMissingProvider) {
		t.Fatalf("Apply() error = %v, want ErrMissingProvider", err)
	}
}

func TestRegistryRollsBackPreparedTransactionsInReverseOrder(t *testing.T) {
	registry := module.NewRegistry()
	events := []string{}
	mustRegister(t, registry, &transactionalRecordingModule{
		recordingModule: recordingModule{name: "first"},
		prepare: func(context.Context, module.ApplyRequest) (module.ModuleTransaction, error) {
			events = append(events, "prepare:first")
			return module.TransactionFuncs{
				CommitFunc:   func() error { events = append(events, "commit:first"); return nil },
				RollbackFunc: func() error { events = append(events, "rollback:first"); return nil },
			}, nil
		},
	})
	mustRegister(t, registry, &transactionalRecordingModule{
		recordingModule: recordingModule{name: "second"},
		prepare: func(context.Context, module.ApplyRequest) (module.ModuleTransaction, error) {
			events = append(events, "prepare:second")
			return nil, errors.New("boom")
		},
	})

	if err := registry.Apply(context.Background(), model.Snapshot{}, model.Snapshot{}); err == nil {
		t.Fatal("Apply() error = nil, want failure")
	}
	if got, want := strings.Join(events, ","), "prepare:first,prepare:second,rollback:first"; got != want {
		t.Fatalf("events = %s, want %s", got, want)
	}
}
```

Use helper fakes in the same test file:

```go
type fakeTLSMaterial struct{}

type recordingModule struct {
	name     string
	provides []module.ProviderRef
	requires []module.ProviderRef
	optional []module.ProviderRef
	register func(module.ProviderRegistry) error
	apply    func(context.Context, module.ApplyRequest) error
}
```

- [ ] **Step 2: Run tests and verify failure**

Run: `cd go-agent && go test ./internal/module`

Expected: FAIL because `ProviderRef`, `ProviderTLSMaterial`, `ApplyRequest`, `ProviderRegistry`, `ErrMissingProvider`, and `Registry.Apply` are not implemented yet.

- [ ] **Step 3: Implement contracts**

Replace `go-agent/internal/module/module.go` with this contract shape:

```go
package module

import (
	"context"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

type ProviderRef string

type Capability struct {
	Name     string
	Enabled  bool
	Metadata map[string]string
}

type Health struct {
	Status  string
	Message string
}

type ModuleDescriptor struct {
	Name     string
	Provides []ProviderRef
	Requires []ProviderRef
	Optional []ProviderRef
}

type SnapshotView = model.Snapshot

type ApplyRequest struct {
	Previous  model.Snapshot
	Next      model.Snapshot
	Providers ProviderResolver
}

type ProviderRegistry interface {
	Provide(ProviderRef, any) error
}

type ProviderResolver interface {
	Resolve(ProviderRef) (any, bool)
}

type Module interface {
	Name() string
	Descriptor() ModuleDescriptor
	RegisterProviders(ProviderRegistry) error
	Capabilities(SnapshotView) []Capability
	Apply(context.Context, ApplyRequest) error
	Stop(context.Context) error
}

type TransactionalModule interface {
	Module
	Prepare(context.Context, ApplyRequest) (ModuleTransaction, error)
}

type ModuleTransaction interface {
	Commit() error
	Rollback() error
}

type TransactionFuncs struct {
	CommitFunc   func() error
	RollbackFunc func() error
}

func (f TransactionFuncs) Commit() error {
	if f.CommitFunc == nil {
		return nil
	}
	return f.CommitFunc()
}

func (f TransactionFuncs) Rollback() error {
	if f.RollbackFunc == nil {
		return nil
	}
	return f.RollbackFunc()
}
```

Create `go-agent/internal/module/providers.go` with behavior-named refs:

```go
package module

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
)

const (
	ProviderTLSMaterial            ProviderRef = "tls.material"
	ProviderOverlayRuntime         ProviderRef = "overlay.runtime"
	ProviderTransparentListener    ProviderRef = "transparent.listener"
	ProviderFinalHopDialer         ProviderRef = "finalhop.dialer"
	ProviderEgressResolver         ProviderRef = "egress.resolver"
	ProviderTrafficSink            ProviderRef = "traffic.sink"
	ProviderDiagnosticsHTTPSource  ProviderRef = "diagnostics.http.source"
	ProviderDiagnosticsL4Source    ProviderRef = "diagnostics.l4.source"
	ProviderDiagnosticsRelaySource ProviderRef = "diagnostics.relay.source"
)

type TLSMaterial interface {
	ServerCertificate(ctx context.Context, certificateID int) (*tls.Certificate, error)
	TrustedCAPool(ctx context.Context, certificateIDs []int) (*x509.CertPool, error)
}

type HostTLSMaterial interface {
	ServerCertificateForHost(ctx context.Context, host string) (*tls.Certificate, error)
}

type OverlayRuntime interface {
	DialContext(ctx context.Context, agentID string, profileID int, network string, address string) (net.Conn, error)
	ListenTCP(ctx context.Context, agentID string, profileID int, address string) (net.Listener, error)
	ListenTransparentTCP(ctx context.Context, agentID string, profileID int) (net.Listener, error)
	ListenUDP(ctx context.Context, agentID string, profileID int, address string) (net.PacketConn, error)
}

type FinalHopDialer interface {
	DialTCP(ctx context.Context, target string, profileID *int) (net.Conn, error)
	OpenUDP(ctx context.Context, target string, profileID *int) (any, error)
}
```

Implement provider registry and dependency ordering in `registry.go`. Keep duplicate-name behavior and add:

```go
var (
	ErrInvalidModule    = errors.New("invalid module")
	ErrDuplicateModule  = errors.New("duplicate module")
	ErrMissingProvider  = errors.New("missing provider")
	ErrDuplicateProvider = errors.New("duplicate provider")
	ErrProviderCycle    = errors.New("provider dependency cycle")
)
```

Use this public API:

```go
func (r *Registry) Apply(ctx context.Context, previous, next model.Snapshot) error
func (r *Registry) Resolve(ref ProviderRef) (any, bool)
func (r *Registry) OrderedModules() ([]Module, error)
```

- [ ] **Step 4: Run module tests and fix compile errors**

Run: `cd go-agent && go test ./internal/module`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add go-agent/internal/module
git commit -m "feat(agent): add module provider graph"
```

### Task 2: Replace Core Business Activation With Generic Module Apply

**Files:**
- Modify: `go-agent/internal/core/activation.go`
- Modify: `go-agent/internal/core/sync_controller.go`
- Modify: `go-agent/internal/core/capabilities.go`
- Modify: `go-agent/internal/core/*_test.go`

- [ ] **Step 1: Write core boundary tests**

Replace activation tests with generic tests:

```go
func TestNewSnapshotActivatorAppliesModulesWithPreviousAndNext(t *testing.T) {
	var got module.ApplyRequest
	registry := module.NewRegistry()
	mustRegister(t, registry, &coreTestModule{
		name: "traffic",
		apply: func(_ context.Context, req module.ApplyRequest) error {
			got = req
			return nil
		},
	})

	previous := model.Snapshot{Revision: 1}
	next := model.Snapshot{Revision: 2}
	activator := core.NewSnapshotActivator(registry)
	if err := activator(context.Background(), previous, next); err != nil {
		t.Fatalf("activator() error = %v", err)
	}
	if got.Previous.Revision != 1 || got.Next.Revision != 2 {
		t.Fatalf("request revisions = %d/%d, want 1/2", got.Previous.Revision, got.Next.Revision)
	}
}

func TestCorePackageDoesNotImportBusinessPackages(t *testing.T) {
	cmd := exec.Command("go", "list", "-f", "{{join .Imports \"\\n\"}}", "./internal/core")
	cmd.Dir = "../.."
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list imports: %v", err)
	}
	for _, forbidden := range []string{"/internal/l4", "/internal/relay", "/internal/proxy", "/internal/wireguard", "/internal/egress", "/internal/diagnostics", "/internal/certs", "/internal/traffic"} {
		if strings.Contains(string(out), forbidden) {
			t.Fatalf("core imports forbidden package %s:\n%s", forbidden, out)
		}
	}
}
```

- [ ] **Step 2: Run core tests and verify failure**

Run: `cd go-agent && go test ./internal/core`

Expected: FAIL because `core.NewSnapshotActivator` still takes agent identity and business handlers.

- [ ] **Step 3: Implement generic activator**

Replace `core.NewSnapshotActivator` with:

```go
type ModuleApplier interface {
	Apply(context.Context, model.Snapshot, model.Snapshot) error
}

func NewSnapshotActivator(modules ModuleApplier) agentruntime.Activator {
	return func(ctx context.Context, previous, next model.Snapshot) error {
		if modules == nil {
			return nil
		}
		return modules.Apply(ctx, previous, next)
	}
}
```

Delete `SnapshotActivationHandlers`, `SnapshotHTTPInput`, `SnapshotL4Input`, `SnapshotRelayInput`, and all HTTP/L4/Relay-specific diff helpers from `core/activation.go`.

- [ ] **Step 4: Update sync controller**

Change `ModuleLifecycle` in `core/sync_controller.go`:

```go
type ModuleLifecycle interface {
	Apply(context.Context, model.Snapshot, model.Snapshot) error
	StopAll(context.Context) error
}
```

Remove separate `StartAll` calls after runtime apply. Runtime apply now invokes module apply through its activator. Rollback stays generic through `Runtime.Rollback`.

- [ ] **Step 5: Update capability collection**

Change `core.CapabilityNames` to depend only on `module.Registry.Capabilities(snapshot)` or a small interface:

```go
type CapabilitySource interface {
	Capabilities(module.SnapshotView) []module.Capability
}
```

It must not hard-code WireGuard capability gates. App decides which modules are registered.

- [ ] **Step 6: Run tests**

Run: `cd go-agent && go test ./internal/core ./internal/runtime`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add go-agent/internal/core go-agent/internal/runtime
git commit -m "feat(agent): make core module activation generic"
```

---

## Phase 2: Foundational Provider Modules

### Task 3: Move Cert Runtime Into `modules/certs`

**Files:**
- Move: `go-agent/internal/certs/*.go` -> `go-agent/internal/modules/certs/`
- Modify: `go-agent/internal/modules/certs/module.go`
- Move tests: `go-agent/internal/certs/*_test.go` -> `go-agent/internal/modules/certs/`
- Modify imports across go-agent from `internal/certs` to `internal/modules/certs`
- Delete: `go-agent/internal/certs/`

- [ ] **Step 1: Write module behavior tests**

Add tests to `go-agent/internal/modules/certs/module_test.go`:

```go
func TestModuleAppliesSnapshotCertificatesAndPublishesTLSMaterial(t *testing.T) {
	manager := newRecordingCertManager()
	mod := certs.NewModule(manager)
	registry := module.NewRegistry()
	mustRegister(t, registry, mod)

	next := model.Snapshot{
		Certificates: []model.ManagedCertificateBundle{{ID: 7}},
		CertificatePolicies: []model.ManagedCertificatePolicy{{ID: 8}},
	}
	if err := registry.Apply(context.Background(), model.Snapshot{}, next); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if manager.appliedBundles != 1 || manager.appliedPolicies != 1 {
		t.Fatalf("applied bundles/policies = %d/%d, want 1/1", manager.appliedBundles, manager.appliedPolicies)
	}
	if _, ok := registry.Resolve(module.ProviderTLSMaterial); !ok {
		t.Fatal("tls.material provider not registered")
	}
}
```

- [ ] **Step 2: Run tests and verify failure**

Run: `cd go-agent && go test ./internal/modules/certs`

Expected: FAIL because cert manager files still live outside the module and module does not implement the new contract.

- [ ] **Step 3: Move files and implement descriptor**

Update `Module`:

```go
func (m *Module) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{
		Name:     m.Name(),
		Provides: []module.ProviderRef{module.ProviderTLSMaterial},
	}
}

func (m *Module) RegisterProviders(reg module.ProviderRegistry) error {
	if m == nil || m.manager == nil {
		return nil
	}
	if err := reg.Provide(module.ProviderTLSMaterial, m.manager); err != nil {
		return err
	}
	return nil
}

func (m *Module) Apply(ctx context.Context, req module.ApplyRequest) error {
	if req.Next.Certificates == nil && req.Next.CertificatePolicies == nil {
		return nil
	}
	return m.manager.Apply(ctx, req.Next.Certificates, req.Next.CertificatePolicies)
}
```

- [ ] **Step 4: Update imports and run focused tests**

Run:

```bash
cd go-agent
go test ./internal/modules/certs ./internal/app
```

Expected: PASS or app compile failures that are only import-path updates from `internal/certs` to `internal/modules/certs`.

- [ ] **Step 5: Commit**

```bash
git add go-agent/internal/modules/certs go-agent/internal/app
git add -u go-agent/internal/certs
git commit -m "refactor(agent): move cert runtime into certs module"
```

### Task 4: Move WireGuard Runtime Into `modules/wireguard` With Behavior-Named Provider

**Files:**
- Move: `go-agent/internal/wireguard/*.go` -> `go-agent/internal/modules/wireguard/`
- Move: `go-agent/internal/wireguard/wgnetstack/*.go` -> `go-agent/internal/modules/wireguard/wgnetstack/`
- Modify: `go-agent/internal/modules/wireguard/module.go`
- Modify: `go-agent/internal/modules/wireguard/runtime.go`
- Modify imports across go-agent.
- Delete: `go-agent/internal/app/wireguard_runtime_compat.go` after consumers are migrated.
- Delete: `go-agent/internal/wireguard/`

- [ ] **Step 1: Write provider tests**

In `go-agent/internal/modules/wireguard/runtime_test.go`, replace relay-provider naming tests with behavior-provider tests:

```go
func TestModulePublishesOverlayRuntimeProvider(t *testing.T) {
	runtime := NewRuntimeWithFactory(func(context.Context, Config) (RuntimeHandle, error) {
		return &recordingRuntimeHandle{}, nil
	})
	mod := NewModule(runtime)
	registry := module.NewRegistry()
	mustRegister(t, registry, mod)

	next := model.Snapshot{WireGuardProfiles: []model.WireGuardProfile{{ID: 9, AgentID: "local"}}}
	if err := registry.Apply(context.Background(), model.Snapshot{}, next); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	provider, ok := registry.Resolve(module.ProviderOverlayRuntime)
	if !ok {
		t.Fatal("overlay.runtime provider missing")
	}
	overlay, ok := provider.(module.OverlayRuntime)
	if !ok {
		t.Fatalf("overlay provider type = %T, want module.OverlayRuntime", provider)
	}
	if _, err := overlay.DialContext(context.Background(), "local", 9, "tcp", "127.0.0.1:80"); err != nil {
		t.Fatalf("DialContext() error = %v", err)
	}
}
```

- [ ] **Step 2: Run tests and verify failure**

Run: `cd go-agent && go test ./internal/modules/wireguard`

Expected: FAIL because the module still returns relay `WireGuardRuntimeProvider`.

- [ ] **Step 3: Move implementation and rename contracts**

Update imports from `internal/wireguard` to `internal/modules/wireguard`. Rename the old runtime interface internally:

```go
type RuntimeHandle interface {
	DialContext(ctx context.Context, network string, address string) (net.Conn, error)
	ListenTCP(ctx context.Context, address string) (net.Listener, error)
	ListenTransparentTCP(ctx context.Context) (net.Listener, error)
	ListenUDP(ctx context.Context, address string) (net.PacketConn, error)
	ListenTransparentUDP(ctx context.Context, address string) (TransparentUDPConn, error)
	Close() error
}
```

Expose only:

```go
func (m *Module) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{
		Name:     m.Name(),
		Provides: []module.ProviderRef{module.ProviderOverlayRuntime, module.ProviderTransparentListener},
	}
}

func (m *Module) RegisterProviders(reg module.ProviderRegistry) error {
	return reg.Provide(module.ProviderOverlayRuntime, m.runtime.Provider())
}
```

- [ ] **Step 4: Keep temporary relay adapter private to migrating modules only**

If HTTP/L4/Relay still require old relay provider in this phase, create private adapter methods inside the consuming module migration branches. Do not expose `WireGuardRuntimeProvider` from `internal/module`, `internal/core`, or `internal/app`.

- [ ] **Step 5: Run focused tests**

Run:

```bash
cd go-agent
go test ./internal/modules/wireguard ./internal/modules/egress ./internal/relay ./internal/l4 ./internal/proxy
```

Expected: PASS after import updates and temporary adapters are scoped outside core/app.

- [ ] **Step 6: Commit**

```bash
git add go-agent/internal/modules/wireguard
git add -u go-agent/internal/wireguard go-agent/internal/app go-agent/internal/relay go-agent/internal/l4 go-agent/internal/proxy go-agent/internal/egress
git commit -m "refactor(agent): move wireguard runtime into module"
```

### Task 5: Move Egress Resolver and Final-Hop Dialer Into `modules/egress`

**Files:**
- Move: `go-agent/internal/egress/*.go` -> `go-agent/internal/modules/egress/`
- Modify: `go-agent/internal/modules/egress/module.go`
- Move tests: `go-agent/internal/egress/*_test.go` -> `go-agent/internal/modules/egress/`
- Modify imports in HTTP/L4/Relay modules.
- Delete: `go-agent/internal/egress/`

- [ ] **Step 1: Write final-hop provider tests**

Add to `go-agent/internal/modules/egress/module_test.go`:

```go
func TestModulePublishesFinalHopDialerAndResolver(t *testing.T) {
	mod := egress.NewModule()
	registry := module.NewRegistry()
	mustRegister(t, registry, mod)
	next := model.Snapshot{EgressProfiles: []model.EgressProfile{{ID: 11, Type: "direct", Enabled: true}}}
	if err := registry.Apply(context.Background(), model.Snapshot{}, next); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if _, ok := registry.Resolve(module.ProviderFinalHopDialer); !ok {
		t.Fatal("finalhop.dialer provider missing")
	}
	if _, ok := registry.Resolve(module.ProviderEgressResolver); !ok {
		t.Fatal("egress.resolver provider missing")
	}
}
```

- [ ] **Step 2: Run tests and verify failure**

Run: `cd go-agent && go test ./internal/modules/egress`

Expected: FAIL until module owns resolver and provider registration.

- [ ] **Step 3: Implement module ownership**

`Module.Apply` updates its current profile resolver and inline WireGuard egress runtime from `req.Next.EgressProfiles`. `RegisterProviders` provides:

```go
func (m *Module) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{
		Name:     m.Name(),
		Provides: []module.ProviderRef{module.ProviderFinalHopDialer, module.ProviderEgressResolver},
		Optional: []module.ProviderRef{module.ProviderOverlayRuntime},
	}
}
```

The final-hop dialer must not require `WireGuardRuntimeProvider`; it consumes `module.ProviderOverlayRuntime` when egress profiles of type `wireguard` are used.

- [ ] **Step 4: Run focused tests**

Run: `cd go-agent && go test ./internal/modules/egress`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add go-agent/internal/modules/egress
git add -u go-agent/internal/egress
git commit -m "refactor(agent): move egress runtime into module"
```

---

## Phase 3: Traffic Path Modules

### Task 6: Move Relay Runtime, Relay Plan, and Relay Route Into `modules/relay`

**Files:**
- Move: `go-agent/internal/relay/*.go` -> `go-agent/internal/modules/relay/`
- Move: `go-agent/internal/relayplan/*.go` -> `go-agent/internal/modules/relay/relayplan/`
- Move: `go-agent/internal/relayroute/*.go` -> `go-agent/internal/modules/relay/relayroute/`
- Move runtime manager logic: `go-agent/internal/app/relay_runtime.go` -> `go-agent/internal/modules/relay/module.go` and focused helper files.
- Modify imports in HTTP/L4/Diagnostics modules.
- Delete old relay package directories after compile.

- [ ] **Step 1: Write relay module apply tests**

Create or update `go-agent/internal/modules/relay/module_test.go`:

```go
func TestModuleAppliesLocalRelayListenersAndConsumesProviders(t *testing.T) {
	tlsProvider := newFakeTLSMaterialProvider()
	mod := relaymodule.NewModule(relaymodule.Config{AgentID: "agent-a", AgentName: "node-a"})
	registry := module.NewRegistry()
	mustRegister(t, registry, staticProviderModule("certs", module.ProviderTLSMaterial, tlsProvider))
	mustRegister(t, registry, staticProviderModule("egress", module.ProviderFinalHopDialer, fakeFinalHopDialer{}))
	mustRegister(t, registry, mod)

	next := model.Snapshot{RelayListeners: []model.RelayListener{{
		ID: 1, AgentID: "agent-a", Protocol: "tcp", ListenHost: "127.0.0.1", ListenPort: 0,
	}}}
	if err := registry.Apply(context.Background(), model.Snapshot{}, next); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if _, ok := registry.Resolve(module.ProviderDiagnosticsRelaySource); !ok {
		t.Fatal("diagnostics.relay.source provider missing")
	}
}
```

- [ ] **Step 2: Run tests and verify failure**

Run: `cd go-agent && go test ./internal/modules/relay`

Expected: FAIL because module does not exist yet.

- [ ] **Step 3: Move relay packages**

Use package names:

```go
package relay
package relayplan
package relayroute
```

Update imports to:

```go
github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay
github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay/relayplan
github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay/relayroute
```

- [ ] **Step 4: Implement relay module**

The relay module owns listener lifecycle and transaction:

```go
func (m *Module) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{
		Name:     "relay",
		Provides: []module.ProviderRef{ProviderRuntime, module.ProviderDiagnosticsRelaySource},
		Requires: []module.ProviderRef{module.ProviderTLSMaterial},
		Optional: []module.ProviderRef{module.ProviderOverlayRuntime, module.ProviderFinalHopDialer},
	}
}

func (m *Module) Prepare(ctx context.Context, req module.ApplyRequest) (module.ModuleTransaction, error) {
	tlsMaterial, _ := req.Providers.Resolve(module.ProviderTLSMaterial)
	overlay, _ := req.Providers.Resolve(module.ProviderOverlayRuntime)
	finalHop, _ := req.Providers.Resolve(module.ProviderFinalHopDialer)
	nextRuntime, err := m.buildRuntime(ctx, req.Next, tlsMaterial, overlay, finalHop)
	if err != nil {
		return nil, err
	}
	oldRuntime := m.runtime
	return module.TransactionFuncs{
		CommitFunc: func() error {
			m.runtime = nextRuntime
			if oldRuntime != nil {
				return oldRuntime.Close()
			}
			return nil
		},
		RollbackFunc: func() error {
			return nextRuntime.Close()
		},
	}, nil
}
```

Relay module filters listeners by local `AgentID`/`AgentName`; core must not do this.

- [ ] **Step 5: Preserve diagnostic final-hop behavior**

Add a relay diagnostics test proving SOCKS5 listener -> relay -> SOCKS5 final hop remains diagnosable and does not produce `at least one backend is required for 0.0.0.0:0`.

- [ ] **Step 6: Run focused tests**

Run:

```bash
cd go-agent
go test ./internal/modules/relay ./internal/modules/egress ./internal/modules/wireguard
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add go-agent/internal/modules/relay
git add -u go-agent/internal/relay go-agent/internal/relayplan go-agent/internal/relayroute go-agent/internal/app
git commit -m "refactor(agent): move relay runtime into module"
```

### Task 7: Move HTTP Runtime Into `modules/http`

**Files:**
- Move: `go-agent/internal/proxy/*.go` -> `go-agent/internal/modules/http/`
- Move runtime manager logic: `go-agent/internal/app/http_runtime.go` -> `go-agent/internal/modules/http/module.go` and helpers.
- Modify imports in diagnostics and app.
- Delete: `go-agent/internal/proxy/`

- [ ] **Step 1: Write HTTP module apply tests**

Create `go-agent/internal/modules/http/module_test.go`:

```go
func TestModuleAppliesHTTPRulesWithTLSAndOptionalFinalHop(t *testing.T) {
	mod := httpmodule.NewModule(httpmodule.Config{HTTP3Enabled: false})
	registry := module.NewRegistry()
	mustRegister(t, registry, staticProviderModule("certs", module.ProviderTLSMaterial, newFakeTLSMaterialProvider()))
	mustRegister(t, registry, staticProviderModule("egress", module.ProviderFinalHopDialer, fakeFinalHopDialer{}))
	mustRegister(t, registry, mod)

	next := model.Snapshot{Rules: []model.HTTPRule{{
		ID: 1, FrontendURL: "http://example.test", Backends: []model.HTTPBackend{{URL: "http://127.0.0.1:1"}},
	}}}
	if err := registry.Apply(context.Background(), model.Snapshot{}, next); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if _, ok := registry.Resolve(module.ProviderDiagnosticsHTTPSource); !ok {
		t.Fatal("diagnostics.http.source provider missing")
	}
}
```

- [ ] **Step 2: Run tests and verify failure**

Run: `cd go-agent && go test ./internal/modules/http`

Expected: FAIL because module does not exist yet.

- [ ] **Step 3: Move proxy implementation**

Use package name `httpmodule` or `http` consistently. Update imports from `internal/proxy` to `internal/modules/http`.

- [ ] **Step 4: Implement HTTP module**

Descriptor:

```go
func (m *Module) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{
		Name:     "http",
		Provides: []module.ProviderRef{ProviderRuntime, module.ProviderDiagnosticsHTTPSource},
		Requires: []module.ProviderRef{module.ProviderTLSMaterial},
		Optional: []module.ProviderRef{module.ProviderOverlayRuntime, module.ProviderFinalHopDialer, module.ProviderEgressResolver},
	}
}
```

Module owns the old HTTP runtime manager cache, HTTP3 setting, traffic block state, and rollback transaction. It consumes generic overlay/final-hop providers and no longer accepts `WireGuardProfiles` as a direct apply parameter.

- [ ] **Step 5: Run focused tests**

Run:

```bash
cd go-agent
go test ./internal/modules/http ./internal/modules/relay ./internal/modules/egress
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add go-agent/internal/modules/http
git add -u go-agent/internal/proxy go-agent/internal/app
git commit -m "refactor(agent): move http runtime into module"
```

### Task 8: Move L4 Runtime Into `modules/l4`

**Files:**
- Move: `go-agent/internal/l4/*.go` -> `go-agent/internal/modules/l4/`
- Move runtime manager logic: `go-agent/internal/app/l4_runtime.go` -> `go-agent/internal/modules/l4/module.go` and helpers.
- Modify imports in diagnostics and app.
- Delete: `go-agent/internal/l4/`

- [ ] **Step 1: Write L4 module apply and final-hop diagnostic tests**

Create `go-agent/internal/modules/l4/module_test.go`:

```go
func TestModuleAppliesL4RuleWithFinalHopSelector(t *testing.T) {
	mod := l4module.NewModule(l4module.Config{})
	registry := module.NewRegistry()
	mustRegister(t, registry, staticProviderModule("certs", module.ProviderTLSMaterial, newFakeTLSMaterialProvider()))
	mustRegister(t, registry, staticProviderModule("egress", module.ProviderFinalHopDialer, fakeFinalHopDialer{}))
	mustRegister(t, registry, mod)

	profileID := 17
	next := model.Snapshot{L4Rules: []model.L4Rule{{
		ID: 1, Protocol: "tcp", ListenHost: "127.0.0.1", ListenPort: 0,
		Backends: []model.L4Backend{{Host: "127.0.0.1", Port: 1}},
		EgressProfileID: &profileID,
	}}}
	if err := registry.Apply(context.Background(), model.Snapshot{}, next); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if _, ok := registry.Resolve(module.ProviderDiagnosticsL4Source); !ok {
		t.Fatal("diagnostics.l4.source provider missing")
	}
}
```

- [ ] **Step 2: Run tests and verify failure**

Run: `cd go-agent && go test ./internal/modules/l4`

Expected: FAIL because module does not exist yet.

- [ ] **Step 3: Move L4 implementation**

Update imports from `internal/l4` to `internal/modules/l4`. L4 server consumes:

```go
module.ProviderTLSMaterial
module.ProviderOverlayRuntime
module.ProviderFinalHopDialer
module.ProviderEgressResolver
```

It must not import or name `WireGuardRuntimeProvider`.

- [ ] **Step 4: Implement L4 module**

Descriptor:

```go
func (m *Module) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{
		Name:     "l4",
		Provides: []module.ProviderRef{ProviderRuntime, module.ProviderDiagnosticsL4Source},
		Optional: []module.ProviderRef{
			module.ProviderTLSMaterial,
			module.ProviderOverlayRuntime,
			module.ProviderFinalHopDialer,
			module.ProviderEgressResolver,
		},
	}
}
```

Module owns old L4 runtime manager cache, traffic block state, and rollback transaction.

- [ ] **Step 5: Run focused tests**

Run:

```bash
cd go-agent
go test ./internal/modules/l4 ./internal/modules/relay ./internal/modules/egress
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add go-agent/internal/modules/l4
git add -u go-agent/internal/l4 go-agent/internal/app
git commit -m "refactor(agent): move l4 runtime into module"
```

---

## Phase 4: Diagnostics and Traffic Modules

### Task 9: Move Diagnostics Into `modules/diagnostics`

**Files:**
- Move: `go-agent/internal/diagnostics/*.go` -> `go-agent/internal/modules/diagnostics/`
- Modify: `go-agent/internal/modules/diagnostics/module.go`
- Modify: `go-agent/internal/task` imports if it references diagnostics directly.
- Modify: `go-agent/internal/app/app.go` task handler composition.
- Delete: `go-agent/internal/diagnostics/`

- [ ] **Step 1: Write diagnostics source tests**

Add to `go-agent/internal/modules/diagnostics/module_test.go`:

```go
func TestModuleConsumesAvailableDiagnosticSources(t *testing.T) {
	mod := diagnosticsmodule.NewModule()
	registry := module.NewRegistry()
	mustRegister(t, registry, staticProviderModule("http", module.ProviderDiagnosticsHTTPSource, fakeHTTPDiagnosticSource{}))
	mustRegister(t, registry, staticProviderModule("l4", module.ProviderDiagnosticsL4Source, fakeL4DiagnosticSource{}))
	mustRegister(t, registry, staticProviderModule("relay", module.ProviderDiagnosticsRelaySource, fakeRelayDiagnosticSource{}))
	mustRegister(t, registry, mod)
	if err := registry.Apply(context.Background(), model.Snapshot{}, model.Snapshot{}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if mod.Handler() == nil {
		t.Fatal("Handler() = nil")
	}
}
```

- [ ] **Step 2: Run tests and verify failure**

Run: `cd go-agent && go test ./internal/modules/diagnostics`

Expected: FAIL until diagnostics consumes providers instead of app-owned probers.

- [ ] **Step 3: Implement diagnostics module**

Descriptor:

```go
func (m *Module) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{
		Name: "diagnostics",
		Optional: []module.ProviderRef{
			module.ProviderDiagnosticsHTTPSource,
			module.ProviderDiagnosticsL4Source,
			module.ProviderDiagnosticsRelaySource,
		},
	}
}
```

Diagnostics owns task handler assembly. It must support L4 final-hop diagnostics where a rule uses `EgressProfileID` and relay chains.

- [ ] **Step 4: Run focused tests**

Run:

```bash
cd go-agent
go test ./internal/modules/diagnostics ./internal/task
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add go-agent/internal/modules/diagnostics go-agent/internal/task go-agent/internal/app
git add -u go-agent/internal/diagnostics
git commit -m "refactor(agent): move diagnostics into module"
```

### Task 10: Move Traffic and Host Traffic Into `modules/traffic`

**Files:**
- Move: `go-agent/internal/traffic/*.go` -> `go-agent/internal/modules/traffic/`
- Move: `go-agent/internal/hosttraffic/*.go` -> `go-agent/internal/modules/traffic/hosttraffic/`
- Modify: `go-agent/internal/modules/traffic/module.go`
- Modify imports in HTTP/L4/Relay modules.
- Delete: `go-agent/internal/traffic/`
- Delete: `go-agent/internal/hosttraffic/`

- [ ] **Step 1: Write traffic module tests**

Add to `go-agent/internal/modules/traffic/module_test.go`:

```go
func TestModuleAppliesTrafficConfigAndReportsStats(t *testing.T) {
	mod := trafficmodule.NewModule(trafficmodule.Config{Interfaces: []string{"lo"}})
	next := model.Snapshot{AgentConfig: model.AgentConfig{TrafficStatsInterval: "5s"}}
	if err := mod.Apply(context.Background(), module.ApplyRequest{Next: next}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	report, err := mod.TrafficReport(context.Background(), map[string]string{})
	if err != nil {
		t.Fatalf("TrafficReport() error = %v", err)
	}
	if report.RuntimeMetadata == nil {
		t.Fatal("RuntimeMetadata = nil")
	}
}
```

- [ ] **Step 2: Run tests and verify failure**

Run: `cd go-agent && go test ./internal/modules/traffic`

Expected: FAIL until traffic owns config application and host collector.

- [ ] **Step 3: Implement traffic module**

Descriptor:

```go
func (m *Module) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{
		Name:     "traffic",
		Provides: []module.ProviderRef{module.ProviderTrafficSink},
	}
}
```

Traffic module owns:

- `SetEnabled`
- block state
- host traffic collector
- traffic report runtime metadata

HTTP/L4/Relay consume traffic state through provider or a small state object passed during module construction, not app callbacks.

- [ ] **Step 4: Run focused tests**

Run:

```bash
cd go-agent
go test ./internal/modules/traffic ./internal/modules/http ./internal/modules/l4 ./internal/modules/relay
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add go-agent/internal/modules/traffic
git add -u go-agent/internal/traffic go-agent/internal/hosttraffic go-agent/internal/app
git commit -m "refactor(agent): move traffic into module"
```

---

## Phase 5: App Composition, Cleanup, and Final Verification

### Task 11: Simplify `internal/app` to Composition Only

**Files:**
- Modify: `go-agent/internal/app/app.go`
- Modify: `go-agent/internal/app/embedded.go`
- Modify: `go-agent/internal/app/sync_runtime_state.go`
- Delete or shrink: `go-agent/internal/app/snapshot_activation.go`
- Delete migrated runtime files from app.
- Modify tests in `go-agent/internal/app`.

- [ ] **Step 1: Write app composition tests**

Replace private-field module-wrapper tests with boundary tests:

```go
func TestNewComposesModulesWithoutBusinessRuntimeFields(t *testing.T) {
	app, err := app.New(app.Config{DataDir: t.TempDir(), AgentID: "agent-a"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer app.Close()

	names := app.ModuleNames()
	for _, want := range []string{"certs", "egress", "relay", "http", "l4", "diagnostics", "traffic"} {
		if !slices.Contains(names, want) {
			t.Fatalf("module %q missing from %v", want, names)
		}
	}
}
```

Add a source boundary test:

```go
func TestAppPackageDoesNotCreateBusinessRuntimesDirectly(t *testing.T) {
	files := readGoFiles(t, "internal/app")
	for path, src := range files {
		for _, forbidden := range []string{
			"newHTTPRuntimeManager",
			"newL4RuntimeManager",
			"newRelayRuntimeManager",
			"newSharedWireGuardRuntime",
			"certs.NewManager",
			"hosttraffic.NewCollector",
		} {
			if strings.Contains(src, forbidden) {
				t.Fatalf("%s contains forbidden business runtime constructor %q", path, forbidden)
			}
		}
	}
}
```

- [ ] **Step 2: Run tests and verify failure**

Run: `cd go-agent && go test ./internal/app`

Expected: FAIL until app is reduced to composition and modules expose constructors.

- [ ] **Step 3: Implement module composition**

`app.New` should look like:

```go
modules, err := newConfiguredModules(cfg)
if err != nil {
	return nil, err
}
registry, err := newAppModuleRegistry(modules...)
if err != nil {
	return nil, err
}
runtime := agentruntime.NewWithActivator(core.NewSnapshotActivator(registry))
```

`newConfiguredModules` constructs modules only:

```go
func newConfiguredModules(cfg Config) ([]agentmodule.Module, error) {
	mods := []agentmodule.Module{
		modulecerts.NewModule(modulecerts.Config{DataDir: cfg.DataDir}),
		moduleegress.NewModule(),
		modulerelay.NewModule(modulerelay.Config{AgentID: cfg.AgentID, AgentName: cfg.AgentName, Timeouts: cfg.RelayTimeouts}),
		modulehttp.NewModule(modulehttp.Config{HTTP3Enabled: cfg.HTTP3Enabled, Resilience: cfg.HTTPResilience}),
		modulel4.NewModule(modulel4.Config{AgentID: cfg.AgentID}),
		modulediagnostics.NewModule(),
		moduletraffic.NewModule(moduletraffic.Config{Interfaces: cfg.TrafficInterfaces, Enabled: cfg.TrafficStatsEnabled}),
	}
	if cfg.WireGuardModuleEnabled() {
		mods = append(mods, modulewireguard.NewModule(modulewireguard.Config{}))
	}
	return mods, nil
}
```

- [ ] **Step 4: Keep external APIs compatible**

`App.Diagnose`, `App.DiagnoseSnapshot`, `App.SyncNow`, embedded runtime constructors, and sync client behavior must keep existing signatures unless the call is internal-only and all callers are updated.

- [ ] **Step 5: Run app tests**

Run: `cd go-agent && go test ./internal/app ./embedded`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add go-agent/internal/app go-agent/embedded
git commit -m "refactor(agent): reduce app to module composition"
```

### Task 12: Delete Obsolete Packages and Low-Value Tests

**Files:**
- Delete old package directories:
  - `go-agent/internal/certs`
  - `go-agent/internal/diagnostics`
  - `go-agent/internal/egress`
  - `go-agent/internal/hosttraffic`
  - `go-agent/internal/l4`
  - `go-agent/internal/proxy`
  - `go-agent/internal/relay`
  - `go-agent/internal/relayplan`
  - `go-agent/internal/relayroute`
  - `go-agent/internal/traffic`
  - `go-agent/internal/wireguard`
- Delete adapter-only tests in:
  - `go-agent/internal/app/app_test.go`
  - `go-agent/internal/core/activation_test.go`
  - `go-agent/internal/modules/*/module_test.go`
- Keep behavioral tests under owning modules.

- [ ] **Step 1: Run source scans and capture failures**

Run:

```bash
cd go-agent
rg -n "internal/(certs|diagnostics|egress|hosttraffic|l4|proxy|relay|relayplan|relayroute|traffic|wireguard)" .
rg -n "WireGuardRuntimeProvider|DefaultWireGuardRuntimeProvider|SetDefaultWireGuardRuntimeProvider" internal
```

Expected before cleanup: any output points to files that must be migrated or deleted.

- [ ] **Step 2: Remove obsolete package dirs**

Use native git-aware removal:

```bash
git rm -r internal/certs internal/diagnostics internal/egress internal/hosttraffic internal/l4 internal/proxy internal/relay internal/relayplan internal/relayroute internal/traffic internal/wireguard
```

If any directory has already been moved and deleted, omit it from the command.

- [ ] **Step 3: Remove low-value tests**

Delete tests that assert only:

- wrapper delegates to wrapper
- constructor stores private field
- app/core/module duplicate the same behavior
- mock-heavy obsolete package boundary

Keep tests that assert protocol behavior, runtime behavior, provider resolution, rollback, diagnostics paths, and snapshot compatibility.

- [ ] **Step 4: Run scans again**

Run:

```bash
cd go-agent
rg -n "internal/(certs|diagnostics|egress|hosttraffic|l4|proxy|relay|relayplan|relayroute|traffic|wireguard)" .
rg -n "WireGuardRuntimeProvider|DefaultWireGuardRuntimeProvider|SetDefaultWireGuardRuntimeProvider" internal
go list ./...
```

Expected:

- No imports of deleted package paths.
- No `WireGuardRuntimeProvider` outside `internal/modules/wireguard` compatibility tests. Prefer zero matches outside deleted-history comments.
- Package count materially below 37.

- [ ] **Step 5: Run tests**

Run: `cd go-agent && go test ./...`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add -A go-agent
git commit -m "refactor(agent): remove obsolete module boundaries"
```

### Task 13: Final Compatibility and Boundary Verification

**Files:**
- Modify tests only if final verification exposes real gaps.
- Update docs only if command names or package paths need to be documented.

- [ ] **Step 1: Run final boundary scans**

Run:

```bash
cd go-agent
go list ./... | Measure-Object
go list -f '{{.ImportPath}} {{join .Imports " "}}' ./internal/core
rg -n "newSharedWireGuardRuntime|newHTTPRuntimeManager|newL4RuntimeManager|newRelayRuntimeManager|WireGuardRuntimeProvider|SnapshotHTTPInput|SnapshotL4Input|SnapshotRelayInput" internal
rg -n "internal/(certs|diagnostics|egress|hosttraffic|l4|proxy|relay|relayplan|relayroute|traffic|wireguard)" .
```

Expected:

- `internal/core` imports only infrastructure packages.
- No app-owned business runtime constructors remain.
- No old snapshot input structs remain.
- No old package path imports remain.
- Package count is materially lower than 37.

- [ ] **Step 2: Run full go-agent tests**

Run: `cd go-agent && go test ./...`

Expected: PASS.

- [ ] **Step 3: Run control-plane tests only if go-agent interface changes touched embedded/control-plane integration**

Run: `cd panel/backend-go && go test ./...`

Expected: PASS.

- [ ] **Step 4: Build frontend only if API/UI files changed**

Run: `cd panel/frontend && npm run build`

Expected: PASS. Skip with note if no frontend or panel API files changed.

- [ ] **Step 5: Docker build only if Dockerfile, scripts, embedded runtime packaging, or repo-level runtime paths changed**

Run: `docker build -t nginx-reverse-emby .`

Expected: PASS. Skip with note if no image-impacting files changed.

- [ ] **Step 6: Commit final verification fixes**

If final verification required code or docs fixes:

```bash
git add -A
git commit -m "fix(agent): complete module ownership cleanup"
```

If no fixes were needed, do not create an empty commit.

---

## Subagent Execution Rules

Each task is executed by one fresh worker. The controller gives the worker only:

- this plan file path
- the full text of the assigned task
- the current spec path `docs/superpowers/specs/2026-05-31-go-agent-module-ownership-rebuild-design.md`
- the current branch and worktree status

Every worker must:

1. Use `superpowers:test-driven-development`.
2. Edit only the files in its task write set unless a compile error proves a directly related import update is required.
3. Not revert changes made by other workers.
4. Run the task's verification commands.
5. Commit the task with the listed commit message.
6. Report status as `DONE`, `DONE_WITH_CONCERNS`, `NEEDS_CONTEXT`, or `BLOCKED`.

After each worker:

1. Run a spec-compliance review subagent.
2. If gaps exist, send the worker a fix request and repeat the spec review.
3. Run a code-quality review subagent.
4. If issues exist, send the worker a fix request and repeat the quality review.
5. Mark the task complete only after both reviews approve.

Do not dispatch implementation workers in parallel for these tasks. This refactor has large import-path and package-boundary overlap; sequential task execution is required to avoid conflicting moves.

## Plan Self-Review Checklist

- Spec coverage:
  - Core infrastructure only: Tasks 1, 2, 11, 13.
  - App composition only: Task 11.
  - Business modules own runtime: Tasks 3 through 10.
  - Behavior-named providers: Tasks 1, 4, 5, 6, 7, 8, 9.
  - Package consolidation: Tasks 3 through 12.
  - Test cleanup: Task 12.
  - Final verification: Task 13.
- No implementation-named provider should remain outside WireGuard internals.
- Temporary adapters are allowed only while migrating a task and must be removed by Task 12.
- Diagnostics must preserve L4 final-hop and SOCKS5 listener -> relay -> SOCKS5 final-hop diagnosis.
- The final state is not complete until the scans and full `go test ./...` pass.
