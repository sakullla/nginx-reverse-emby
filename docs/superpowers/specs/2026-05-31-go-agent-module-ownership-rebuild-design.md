# go-agent module ownership rebuild design

## Problem

The 2026-05-30 core/plugin restructure did not reach the intended goal. It added module-looking packages, but ownership stayed in `internal/app` and `internal/core`: app still creates business runtimes up front, core still contains HTTP/L4/Relay/WireGuard/Egress-specific activation decisions, and `internal/modules/*` mostly acts as thin adapters around objects owned elsewhere.

That shape increases maintenance cost instead of reducing it. It also keeps too many packages and too many low-value tests around obsolete boundaries.

## Goals

1. Preserve external behavior and control-plane protocol compatibility.
2. Make `core` a small infrastructure layer only.
3. Move all business capabilities into real modules.
4. Invert ownership so modules own their runtimes and publish generic capabilities.
5. Remove implementation-named coupling such as `WireGuardRuntimeProvider` from non-WireGuard modules.
6. Reduce package count by merging implementation packages into their owning modules.
7. Delete adapter-only, duplicate, and low-value tests while keeping behavioral coverage.

## Non-goals

1. Do not split go-agent into multiple binaries.
2. Do not introduce Go plugin loading or external plugin processes.
3. Do not change control-plane snapshot JSON semantics.
4. Do not remove externally supported features.
5. Do not keep long-lived compatibility adapters for old internal package boundaries.

Temporary bridge code is acceptable only inside the restructuring branch while a module is being moved. The final tree must not retain packages whose only purpose is to preserve the old internal package layout.

## Architecture Boundary

`cmd/nre-agent` starts the process.

`internal/app` is composition only:

- load and normalize config
- construct store and API clients
- choose enabled modules
- register modules with core
- wire process lifecycle

`internal/app` must not contain HTTP/L4/Relay/WireGuard/Egress business apply logic.

`internal/core` owns only agent infrastructure:

- config normalization hooks
- store access orchestration
- sync loop
- task dispatch base
- snapshot lifecycle
- provider registry
- module registry
- module dependency ordering
- runtime state recording
- generic prepare/commit/rollback flow

`internal/core` must not import HTTP, L4, Relay, WireGuard, Egress, Diagnostics, Certs, or Traffic implementations.

`internal/module` owns stable contracts:

- `Module`
- `ModuleDescriptor`
- `ApplyRequest`
- `ProviderRegistry`
- `ProviderResolver`
- `Capability`
- `Health`
- optional transaction contracts

Business capabilities are real modules:

- `internal/modules/certs`
- `internal/modules/http`
- `internal/modules/l4`
- `internal/modules/relay`
- `internal/modules/wireguard`
- `internal/modules/egress`
- `internal/modules/diagnostics`
- `internal/modules/traffic`

## Module Contract

The module contract should stay small:

```go
type Module interface {
    Name() string
    Descriptor() ModuleDescriptor
    RegisterProviders(ProviderRegistry) error
    Capabilities(SnapshotView) []Capability
    Apply(context.Context, ApplyRequest) error
    Stop(context.Context) error
}
```

`ApplyRequest` carries previous and next snapshots plus provider access:

```go
type ApplyRequest struct {
    Previous  model.Snapshot
    Next      model.Snapshot
    Providers ProviderResolver
}
```

Modules decide their own diff, apply, and rollback behavior. Core does not pre-slice snapshots into HTTP/L4/Relay-specific input structs.

## Provider Model

Provider contracts are named by behavior, not implementation.

Examples:

- `tls.material`
- `overlay.dialer`
- `overlay.listener`
- `transparent.listener`
- `finalhop.dialer`
- `egress.resolver`
- `traffic.sink`
- `diagnostics.http.source`
- `diagnostics.l4.source`
- `diagnostics.relay.source`

Provider IDs are stable string references. Provider payloads are small Go interfaces for a behavior, kept in `internal/module` or in the owning module when only that module consumes the behavior. The rewrite should not create a new package per provider contract.

WireGuard is only one implementation of overlay and transparent network capabilities. Therefore:

- no non-WireGuard module imports `internal/modules/wireguard` implementation packages
- no non-WireGuard module accepts a `WireGuardRuntimeProvider`
- no shared provider interface is named after WireGuard
- HTTP/L4/Relay consume generic network capabilities
- WireGuard registers provider implementations for those generic capabilities

The control-plane snapshot may continue to contain `wireguard_profiles`; that is protocol data. Runtime dependencies should still be expressed through generic providers.

Diagnostics must also follow this model. L4 rules that use `final_hop_selector` are diagnosable through generic final-hop and route sources; diagnostics must not assume a direct backend list is required. A chain such as SOCKS5 listener -> relay -> SOCKS5 final hop should be diagnosable when the involved modules publish diagnostic sources. Missing links should be reported as route/provider resolution failures, not as unrelated listener backend validation errors.

## Dependency Ordering

Modules declare provided, required, and optional providers:

```go
type ModuleDescriptor struct {
    Name     string
    Provides []ProviderRef
    Requires []ProviderRef
    Optional []ProviderRef
}
```

Core collects descriptors, validates required providers, and applies modules in dependency order. Core does not hard-code business ordering such as certs before relay or WireGuard before L4. That ordering falls out of provider requirements.

Example descriptors:

`certs`:

- provides `tls.material`

`wireguard`:

- provides `overlay.dialer`
- provides `overlay.listener`
- provides `transparent.listener`

`egress`:

- provides `finalhop.dialer`
- provides `egress.resolver`
- optionally consumes `overlay.dialer`

`relay`:

- provides `relay.runtime`
- provides `diagnostics.relay.source`
- requires `tls.material`
- optionally consumes `overlay.dialer`
- optionally consumes `finalhop.dialer`

`http`:

- provides `http.runtime`
- provides `diagnostics.http.source`
- requires `tls.material`
- optionally consumes `overlay.listener`
- optionally consumes `finalhop.dialer`

`l4`:

- provides `l4.runtime`
- provides `diagnostics.l4.source`
- optionally consumes `overlay.listener`
- optionally consumes `overlay.dialer`
- optionally consumes `finalhop.dialer`

`diagnostics`:

- consumes available diagnostics sources
- does not own HTTP/L4/Relay runtimes

## Rollback Model

Each module owns its runtime transaction. Core only coordinates generic lifecycle.

Optional transaction shape:

```go
type TransactionalModule interface {
    Prepare(context.Context, ApplyRequest) (ModuleTransaction, error)
}

type ModuleTransaction interface {
    Commit() error
    Rollback() error
}
```

Flow:

1. Core computes modules affected by the snapshot.
2. Core calls `Prepare` for transactional modules and `Apply` for simple modules.
3. If preparation fails, core rolls back prepared module transactions in reverse order.
4. If all preparation succeeds, core commits in order.
5. Modules are responsible for keeping their old runtime live until commit succeeds.
6. Core records module-scoped runtime state on failure.

This replaces scattered rollback logic currently living in app-level HTTP/L4/Relay/WireGuard managers.

## Package Consolidation

Target package ownership:

- `internal/proxy` moves into `internal/modules/http`
- `internal/l4` moves into `internal/modules/l4`
- `internal/relay`, `internal/relayplan`, and `internal/relayroute` move into `internal/modules/relay`
- `internal/wireguard` and `internal/wireguard/wgnetstack` move into `internal/modules/wireguard`
- `internal/egress` moves into `internal/modules/egress`
- `internal/diagnostics` and diagnostic task handling move into `internal/modules/diagnostics`
- `internal/certs` moves into `internal/modules/certs`
- `internal/traffic` and `internal/hosttraffic` move into `internal/modules/traffic`

The old `internal/modules/*` adapter packages should be replaced by real module implementations, not kept as wrappers.

`internal/model`, `internal/store`, `internal/config`, and low-level platform helpers can remain shared infrastructure.

The target is fewer, larger ownership packages, not another layer of package names. New subpackages inside `internal/modules/*` should be added only when a runtime area is independently meaningful and too large to maintain in one package.

## Migration Plan

### Phase 1: Replace contracts and core lifecycle

- rewrite `internal/module`
- add provider registry and dependency graph
- replace core business-specific activation handlers with generic module apply
- remove `SnapshotHTTPInput`, `SnapshotL4Input`, and `SnapshotRelayInput`
- keep external behavior unchanged through current modules during the transition only inside the branch

### Phase 2: Move foundational provider modules

- move cert management into `modules/certs`
- move WireGuard runtime ownership into `modules/wireguard`
- move egress resolver and final-hop dialer into `modules/egress`
- expose only behavior-named providers

### Phase 3: Move traffic path modules

- move relay runtime into `modules/relay`
- move HTTP runtime into `modules/http`
- move L4 runtime into `modules/l4`
- delete app-level runtime managers after each module owns its runtime
- remove direct `WireGuardProfiles` parameters from HTTP/L4/Relay apply paths

### Phase 4: Move diagnostics and traffic

- move diagnostics into `modules/diagnostics`
- diagnostics consume diagnostic sources from HTTP/L4/Relay modules
- move traffic and host traffic into `modules/traffic`
- remove app-level prober/cache assembly

### Phase 5: Delete obsolete packages and tests

- delete old implementation package paths after their module replacements compile
- delete adapter-only tests
- move retained behavioral tests to owning modules
- keep a small compatibility and smoke suite

## Test Strategy

Keep tests that cover real contracts:

- control-plane snapshot compatibility
- embedded runtime compatibility
- module dependency graph and provider resolution
- module prepare/commit/rollback behavior
- HTTP/L4/Relay runtime behavior
- WireGuard runtime behavior
- egress and final-hop behavior
- diagnostics behavior through provider sources
- a small end-to-end smoke path from snapshot to applied runtime

Delete tests that only preserve obsolete internals:

- wrapper calls another wrapper
- constructor stores fields
- app/core/module duplicate the same assertion
- mocks replace every meaningful dependency
- tests exist only because old adapter package boundaries existed

## Completion Criteria

The rewrite is complete when:

1. `internal/core` imports no business modules.
2. `internal/app` creates no business runtime directly.
3. WireGuard implementation is owned by `modules/wireguard`.
4. HTTP/L4/Relay do not import or name WireGuard runtime contracts.
5. Provider contracts are behavior-named.
6. Old adapter packages are removed.
7. Package count is materially lower than the current structure.
8. `go test ./...` passes.
9. External control-plane and embedded runtime behavior remain compatible.
