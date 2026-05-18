# WireGuard Netstack Stack Exposure Design

## Summary

This design removes the `reflect + unsafe` extraction used to obtain `*stack.Stack` from the current WireGuard userspace netstack runtime while preserving existing runtime behavior and public interfaces.

The change is intentionally narrow. It only affects the internal WireGuard runtime implementation in `go-agent/internal/wireguard` and does not change the `wireguard.Runtime` interface or any callers in `internal/l4`, `internal/relay`, or `internal/app`.

## Problem

`go-agent/internal/wireguard/runtime.go` currently uses `extractNetstackStack()` to reach into the internal layout of the object returned by `golang.zx2c4.com/wireguard/tun/netstack.CreateNetTUN(...)`.

That design has three concrete problems:

1. It depends on the private field layout of an upstream type.
2. It requires `reflect` and `unsafe` in the runtime hot path for transparent UDP support.
3. It can fail at runtime after successful initialization if the upstream layout changes.

The only reason this exists is that the current runtime needs direct access to `*stack.Stack` for transparent UDP listener creation, specifically to call `NewEndpoint(...)` and `SetReceiveOriginalDstAddress(true)`, but the current netstack constructor does not expose the stack directly.

## Goals

1. Remove `extractNetstackStack()` and all layout-based access to upstream internals.
2. Preserve current runtime behavior for:
   - `DialContext`
   - `ListenTCP`
   - `ListenUDP`
   - `ListenTransparentUDP`
3. Keep the existing `wireguard.Runtime` interface unchanged.
4. Keep all changes scoped to `go-agent/internal/wireguard` and its tests.
5. Fail during runtime construction if the netstack cannot be created correctly.

## Non-Goals

1. No redesign of the higher-level WireGuard runtime API.
2. No support for additional WireGuard backends or kernel WireGuard.
3. No changes to L4 or relay feature behavior.
4. No unrelated refactoring in other packages.

## Current Context

The current runtime is built around the userspace WireGuard stack from `wireguard-go` plus the userspace TUN/netstack helper from `golang.zx2c4.com/wireguard/tun/netstack`.

Current runtime behavior is:

1. Construct a TUN-backed netstack runtime.
2. Create a `wireguard-go` device on top of that TUN device.
3. Use the returned net object for standard TCP and UDP dial/listen operations.
4. Use `extractNetstackStack()` to recover `*stack.Stack` for transparent UDP listener creation.

Transparent UDP is the only feature that requires direct `*stack.Stack` access.

## Options Considered

### Option 1: Internal minimal netstack wrapper

Add an internal package under `go-agent/internal/wireguard/wgnetstack` that creates the userspace TUN/netstack and explicitly returns:

- `tun.Device`
- a runtime net object with the existing dial/listen capabilities
- `*stack.Stack`

Pros:

- Removes the `unsafe` dependency entirely.
- Keeps the change private to the WireGuard package boundary.
- Preserves the existing upper-layer runtime interface.
- Makes the stack dependency explicit and testable.

Cons:

- Introduces a small amount of vendored or copied implementation responsibility inside the repository.

### Option 2: Fork upstream netstack package

Fork the upstream `wireguard-go` netstack package and add a public accessor or extra return value.

Pros:

- Also removes the layout dependency.

Cons:

- Adds long-term fork maintenance overhead.
- Couples the project to a custom upstream dependency variant.

### Option 3: Keep current construction and replace reflection with another internal hook

Retain the current upstream constructor and find another non-public way to reach the internal stack.

Pros:

- Smaller short-term code delta.

Cons:

- Still relies on upstream internals.
- Does not materially improve robustness.

## Decision

Use **Option 1**.

The repository will introduce an internal `wgnetstack` package that owns the userspace TUN/netstack construction path and explicitly returns `*stack.Stack` at creation time.

This keeps the solution local, removes the layout dependency, and avoids broad interface churn.

## Detailed Design

### Package boundary

Add a new internal package:

- `go-agent/internal/wireguard/wgnetstack`

This package has one responsibility: construct the userspace WireGuard TUN/netstack runtime and expose the exact primitives the local runtime needs.

### Proposed API

The package will expose a minimal API shaped around current runtime needs:

```go
package wgnetstack

type RuntimeNet interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
	ListenTCP(addr *net.TCPAddr) (net.Listener, error)
	ListenUDP(addr *net.UDPAddr) (net.PacketConn, error)
}

func CreateNetTUN(
	localAddrs []netip.Addr,
	dnsAddrs []netip.Addr,
	mtu int,
) (tun.Device, RuntimeNet, *stack.Stack, error)
```

The API is intentionally narrow. It only includes the capabilities already consumed by `runtime.go`.

### Runtime structure changes

`netstackRuntime` will directly store the stack returned by the constructor:

```go
type netstackRuntime struct {
	net    wgnetstack.RuntimeNet
	stack  *stack.Stack
	device *device.Device
	tun    tun.Device

	mu     sync.Mutex
	closed bool
}
```

### Runtime construction flow

`NewRuntime(...)` will change as follows:

1. Parse configuration as today.
2. Call `wgnetstack.CreateNetTUN(...)`.
3. Receive `tunDevice`, `runtimeNet`, and `gstack`.
4. Create the WireGuard device using `device.NewDevice(...)`.
5. Bring the device up.
6. Return `netstackRuntime{net: runtimeNet, stack: gstack, device: dev, tun: tunDevice}`.

If `CreateNetTUN(...)` cannot return a valid stack, it must fail immediately and return an error. The runtime must not be constructed in a partially usable state.

### Call flow after the change

Existing behavior remains unchanged:

- `DialContext()` delegates to `r.net.DialContext(...)`
- `ListenTCP()` delegates to `r.net.ListenTCP(...)`
- `ListenUDP()` delegates to `r.net.ListenUDP(...)`

Transparent UDP changes only in how it obtains the stack:

- `ListenTransparentUDP()` uses `r.stack.NewEndpoint(...)` directly
- `sourceBoundConn(...)` also uses the explicit `r.stack`

### Transparent UDP behavior

Transparent UDP listener setup remains functionally identical:

1. Resolve the configured UDP listen address.
2. Convert it into a `tcpip.FullAddress`.
3. Create a UDP endpoint with `r.stack.NewEndpoint(...)`.
4. Enable `SetReuseAddress(true)`.
5. Enable `SetReceiveOriginalDstAddress(true)`.
6. Bind the endpoint.
7. Return a `netstackTransparentUDPConn`.

Reply handling and source-bound writes remain unchanged in behavior.

### Error handling

The design keeps existing external error behavior where useful:

1. Bind failures still return `net.OpError`.
2. Missing stack access is still surfaced as a clear netstack-unavailable error.
3. Errors caused by private-layout inspection disappear entirely.

The string `wireguard netstack layout mismatch` is removed because layout inspection no longer exists in the design.

The runtime should prefer construction-time failure over deferred failure when stack creation is impossible.

### Concurrency and lifecycle

The new package does not change runtime ownership rules:

- `netstackRuntime` still owns the TUN device and the WireGuard device lifecycle.
- `Close()` semantics stay the same.
- `*stack.Stack` is treated as an internal runtime dependency, not a public or shared object.

No concurrency model changes are introduced in this design.

## File-Level Impact

### New files

- `go-agent/internal/wireguard/wgnetstack/*`

### Modified files

- `go-agent/internal/wireguard/runtime.go`
- `go-agent/internal/wireguard/runtime_test.go`

### Removed implementation elements

From `runtime.go`:

- `netstackNetLayout`
- `extractNetstackStack(...)`
- `reflect` import
- `unsafe` import

## Testing Plan

### Preserve behavior tests

Existing functional tests for these behaviors should remain:

1. TCP listen works through the userspace WireGuard runtime.
2. UDP listen works through the userspace WireGuard runtime.
3. Transparent UDP listen works.
4. Transparent UDP wildcard bind works.
5. Original destination is captured correctly.
6. Reply and source-bound packet behavior works.

### Replace implementation-detail tests

Remove or rewrite tests that only validate the old unsafe extraction path, especially:

- the test that expects extraction to fail for an unexpected netstack layout

These tests will be replaced by tests aligned to the new design:

1. Runtime construction results in a non-nil stack.
2. Transparent UDP listener creation fails clearly if the runtime stack is unavailable.
3. Close behavior remains safe and idempotent.

## Rollout Plan

1. Add `wgnetstack` package with the minimal constructor API.
2. Update `runtime.go` to use the explicit returned stack.
3. Remove the unsafe extraction logic.
4. Update tests to reflect the new design.
5. Run targeted WireGuard runtime tests first, then the broader Go agent suite.

## Risks

### Internal implementation ownership

The main tradeoff is that the repository now owns a small internal netstack construction layer instead of relying on upstream as a black box.

This is acceptable because the owned surface is intentionally minimal and directly aligned with the current runtime's needs.

### Drift from upstream

The internal constructor may need review when upgrading `wireguard-go` or related gVisor dependencies.

This is still preferable to a runtime dependency on upstream private struct layout, because drift becomes visible during development rather than surfacing as a fragile production-time mismatch.

## Acceptance Criteria

The design is complete when all of the following are true:

1. `runtime.go` no longer uses `reflect` or `unsafe` to access netstack internals.
2. Transparent UDP support still works, including original-destination handling.
3. No public WireGuard runtime interfaces change.
4. `internal/l4`, `internal/relay`, and `internal/app` require no call-site changes for this refactor.
5. Tests cover the preserved behavior and no longer depend on upstream private layout assumptions.
