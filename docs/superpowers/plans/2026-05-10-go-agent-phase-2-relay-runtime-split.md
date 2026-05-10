# Go Agent Phase 2 Relay Runtime Split Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Split `go-agent/internal/relay/runtime.go` into responsibility-focused relay runtime files without changing relay behavior, protocol semantics, defaults, or tests.

**Architecture:** Keep the package name `relay` stable and perform same-package declaration moves only. `runtime.go` remains the core server lifecycle entry point, while dial/runtime selection, TCP handling, QUIC server handling, UDP stream handling, and pipe helpers move into focused files with package-private APIs preserved.

**Tech Stack:** Go standard library networking, `github.com/quic-go/quic-go`, existing `internal/relay`, `internal/traffic`, and `internal/upstream` packages.

---

## Scope And Constraints

This plan implements Phase 2 of `docs/superpowers/specs/2026-05-09-go-agent-performance-refactor-design.md` for `internal/relay/runtime.go` only.

Required constraints:

- Move declarations out of `go-agent/internal/relay/runtime.go`; do not rewrite behavior.
- Keep `package relay` for every new file.
- Do not change exported function signatures, relay protocol frames, timeout defaults, transport selection behavior, logging text, traffic accounting, or test expectations.
- Do not edit `go-agent/internal/relay/tls_tcp_session_pool.go`, `go-agent/internal/relay/protocol.go`, `go-agent/internal/relay/validation.go`, or `go-agent/internal/relay/quic_runtime.go` except for moving QUIC server-side declarations from `runtime.go` into `quic_runtime.go`.
- Run `gofmt` on changed Go files after each task.
- Run `cd go-agent && go test ./internal/relay` after each task.
- Commit after each task with the exact commit message listed in that task.

## Target File Map

- Keep: `go-agent/internal/relay/runtime.go`
  - Owns core exported runtime types and lifecycle: `DialOptions`, `DialResult`, `Server`, `Start`, `(*Server).startListener`, `(*Server).Close`, traffic block accessors, and `ListenersChanged`.
- Create: `go-agent/internal/relay/tcp_server.go`
  - Owns TCP listener accept path and TCP connection tracking.
- Create: `go-agent/internal/relay/dial_runtime.go`
  - Owns public and server-side relay dialing, resolving, outbound proxy state, metadata conversion, and legacy TLS TCP dialing.
- Create: `go-agent/internal/relay/transport_selection.go`
  - Owns relay transport planner state, QUIC probe/backoff/fallback decisions, score test hooks, and verified fallback store.
- Modify: `go-agent/internal/relay/quic_runtime.go`
  - Also owns server-side QUIC accept/connection/stream handling and QUIC connection tracking.
- Create: `go-agent/internal/relay/udp_stream.go`
  - Owns UDP relay stream handling.
- Create: `go-agent/internal/relay/pipe_runtime.go`
  - Owns bidirectional runtime pipe helpers that coordinate traffic recorder behavior and half-close handling.

## Declaration Inventory

The starting declaration inventory in `go-agent/internal/relay/runtime.go` must be distributed as follows.

Remain in `runtime.go`:

```go
type DialOptions struct { ... }
type DialResult struct { ... }
func (o DialOptions) clone() DialOptions
type Server struct { ... }
func Start(ctx context.Context, listeners []Listener, provider TLSMaterialProvider) (*Server, error)
func (s *Server) startListener(listener Listener) error
func (s *Server) Close() error
func (s *Server) currentTrafficBlockState() TrafficBlockState
func (s *Server) SetTrafficBlockState(state TrafficBlockState)
func ListenersChanged(previous, next []Listener) bool
```

Move to `tcp_server.go`:

```go
func (s *Server) acceptLoop(ln net.Listener, listener Listener)
func (s *Server) handleConn(rawConn net.Conn, listener Listener)
func (s *Server) trackConn(conn net.Conn)
func (s *Server) untrackConn(conn net.Conn)
func (s *Server) closeConns()
```

Move to `dial_runtime.go`:

```go
var relayOutboundProxyURL atomic.Value
func (s *Server) openUpstream(network, target string, chain []Hop, options DialOptions) (net.Conn, error)
func (s *Server) openUpstreamWithResult(network, target string, chain []Hop, options DialOptions) (net.Conn, DialResult, error)
func (s *Server) openUDPPeer(target string, chain []Hop) (udpPacketPeer, error)
func (s *Server) openUDPPeerWithResult(target string, chain []Hop) (udpPacketPeer, string, error)
func (s *Server) openUDPPeerWithResultOptions(target string, chain []Hop, options DialOptions) (udpPacketPeer, string, error)
func (s *Server) resolveTargetCandidates(target string, chain []Hop) ([]string, error)
func Dial(ctx context.Context, network, target string, chain []Hop, provider TLSMaterialProvider, opts ...DialOptions) (net.Conn, error)
func DialWithResult(ctx context.Context, network, target string, chain []Hop, provider TLSMaterialProvider, opts ...DialOptions) (net.Conn, DialResult, error)
func SetOutboundProxyURL(raw string)
func OutboundProxyURL() string
func ResolveCandidates(ctx context.Context, target string, chain []Hop, provider TLSMaterialProvider) ([]string, error)
func relayDialTrafficClass(network string, options DialOptions) upstream.TrafficClass
func relayMetadataForDialOptions(network string, options DialOptions) map[string]any
func relayDialOptionsFromMetadata(network string, metadata map[string]any) DialOptions
func dialRelayTCPWithProxy(ctx context.Context, address string, _ Listener, proxyURL string) (net.Conn, error)
func dialTLSTCP(ctx context.Context, network, target string, chain []Hop, provider TLSMaterialProvider) (net.Conn, error)
```

Move to `transport_selection.go`:

```go
type relayPathPlanner interface { ... }
var relayPlanner relayPathPlanner
var relayRuntimeScore = upstream.NewScoreStore(time.Now)
var relayVerifiedFallbacks = newRelayVerifiedFallbackStore()
const relayQUICProbeInterval = 30 * time.Second
func setRelayPlannerForTest(planner relayPathPlanner) func()
type relayVerifiedFallbackStore struct { ... }
func newRelayVerifiedFallbackStore() *relayVerifiedFallbackStore
func (s *relayVerifiedFallbackStore) Mark(firstHop Hop)
func (s *relayVerifiedFallbackStore) Clear(firstHop Hop)
func (s *relayVerifiedFallbackStore) Has(firstHop Hop) bool
func selectRelayRuntimeTransport(firstHop Hop) string
func chooseRelayTransport(firstHop Hop) string
func relayTransportCandidates(firstHop Hop) []upstream.PathSnapshot
func relayQUICProbeDue(firstHop Hop) bool
func relayQUICBackoffActive(firstHop Hop) bool
func consumeRelayQUICProbe(firstHop Hop) bool
func relayPathConfidence(state upstream.PathState, probeDue bool) float64
func relayQUICPathKey(firstHop Hop) upstream.PathKey
func relayHopIdentityKey(firstHop Hop) string
func relayVerifiedFallbackAvailable(firstHop Hop) bool
func isRelayApplicationError(err error) bool
func markRelayVerifiedFallback(firstHop Hop)
func clearRelayVerifiedFallback(firstHop Hop)
func observeRelayQUICFailureForHop(firstHop Hop)
func observeRelayQUICSuccessForHop(firstHop Hop)
func setRelayRuntimeScoreForTest(score *upstream.ScoreStore) func()
func setRelayVerifiedFallbacksForTest(store *relayVerifiedFallbackStore) func()
```

Move to existing `quic_runtime.go`:

```go
func (s *Server) acceptQUICLoop(ln *quic.Listener, listener Listener)
func (s *Server) handleQUICConn(conn *quic.Conn, listener Listener)
func (s *Server) handleQUICStream(conn *quic.Conn, stream *quic.Stream, listener Listener)
func (s *Server) trackQUICConn(conn *quic.Conn)
func (s *Server) untrackQUICConn(conn *quic.Conn)
func (s *Server) closeQUICConns()
```

Move to `udp_stream.go`:

```go
func listenerUsesEarlyWindowMask(listener Listener) bool
func (s *Server) handleUDPRelayStream(clientConn net.Conn, listener Listener, target string, chain []Hop, options DialOptions)
func pipeUDPPackets(clientConn net.Conn, upstream udpPacketPeer, recorder *traffic.Recorder)
```

Move to `pipe_runtime.go`:

```go
func pipeBothWays(left, right net.Conn, recorder *traffic.Recorder)
func pipeBothWaysWithInitialRelayRX(left, right net.Conn, initialRX int64, recorder *traffic.Recorder)
func relayRecorderOrAggregate(recorder *traffic.Recorder) *traffic.Recorder
func closeWrite(conn net.Conn)
func closeRead(conn net.Conn)
```

## Task 1: Split TCP Server Handling

**Files:**
- Create: `go-agent/internal/relay/tcp_server.go`
- Modify: `go-agent/internal/relay/runtime.go`
- Test: existing tests under `go-agent/internal/relay`

- [ ] **Step 1: Capture baseline**

Run:

```powershell
cd go-agent
go test ./internal/relay
```

Expected: PASS.

- [ ] **Step 2: Move TCP declarations**

Create `go-agent/internal/relay/tcp_server.go` with `package relay` and move these declarations from `runtime.go` exactly:

```go
func (s *Server) acceptLoop(ln net.Listener, listener Listener)
func (s *Server) handleConn(rawConn net.Conn, listener Listener)
func (s *Server) trackConn(conn net.Conn)
func (s *Server) untrackConn(conn net.Conn)
func (s *Server) closeConns()
```

Required imports in the new file are the imports those moved declarations actually use. Do not leave unused imports in `runtime.go`.

- [ ] **Step 3: Format and test**

Run:

```powershell
gofmt -w internal/relay/runtime.go internal/relay/tcp_server.go
go test ./internal/relay
```

Expected: PASS.

- [ ] **Step 4: Commit**

Run:

```powershell
git add go-agent/internal/relay/runtime.go go-agent/internal/relay/tcp_server.go
git commit -m "refactor(relay): split tcp server handling"
```

## Task 2: Split Dial Runtime And Transport Selection

**Files:**
- Create: `go-agent/internal/relay/dial_runtime.go`
- Create: `go-agent/internal/relay/transport_selection.go`
- Modify: `go-agent/internal/relay/runtime.go`
- Test: existing tests under `go-agent/internal/relay`

- [ ] **Step 1: Capture current passing state**

Run:

```powershell
cd go-agent
go test ./internal/relay
```

Expected: PASS.

- [ ] **Step 2: Move transport selection declarations**

Create `go-agent/internal/relay/transport_selection.go` with `package relay` and move these declarations from `runtime.go` exactly:

```go
type relayPathPlanner interface { ... }
var relayPlanner relayPathPlanner
var relayRuntimeScore = upstream.NewScoreStore(time.Now)
var relayVerifiedFallbacks = newRelayVerifiedFallbackStore()
const relayQUICProbeInterval = 30 * time.Second
func setRelayPlannerForTest(planner relayPathPlanner) func()
type relayVerifiedFallbackStore struct { ... }
func newRelayVerifiedFallbackStore() *relayVerifiedFallbackStore
func (s *relayVerifiedFallbackStore) Mark(firstHop Hop)
func (s *relayVerifiedFallbackStore) Clear(firstHop Hop)
func (s *relayVerifiedFallbackStore) Has(firstHop Hop) bool
func selectRelayRuntimeTransport(firstHop Hop) string
func chooseRelayTransport(firstHop Hop) string
func relayTransportCandidates(firstHop Hop) []upstream.PathSnapshot
func relayQUICProbeDue(firstHop Hop) bool
func relayQUICBackoffActive(firstHop Hop) bool
func consumeRelayQUICProbe(firstHop Hop) bool
func relayPathConfidence(state upstream.PathState, probeDue bool) float64
func relayQUICPathKey(firstHop Hop) upstream.PathKey
func relayHopIdentityKey(firstHop Hop) string
func relayVerifiedFallbackAvailable(firstHop Hop) bool
func isRelayApplicationError(err error) bool
func markRelayVerifiedFallback(firstHop Hop)
func clearRelayVerifiedFallback(firstHop Hop)
func observeRelayQUICFailureForHop(firstHop Hop)
func observeRelayQUICSuccessForHop(firstHop Hop)
func setRelayRuntimeScoreForTest(score *upstream.ScoreStore) func()
func setRelayVerifiedFallbacksForTest(store *relayVerifiedFallbackStore) func()
```

Required imports in the new file are the imports those moved declarations actually use.

- [ ] **Step 3: Move dial runtime declarations**

Create `go-agent/internal/relay/dial_runtime.go` with `package relay` and move these declarations from `runtime.go` exactly:

```go
var relayOutboundProxyURL atomic.Value
func (s *Server) openUpstream(network, target string, chain []Hop, options DialOptions) (net.Conn, error)
func (s *Server) openUpstreamWithResult(network, target string, chain []Hop, options DialOptions) (net.Conn, DialResult, error)
func (s *Server) openUDPPeer(target string, chain []Hop) (udpPacketPeer, error)
func (s *Server) openUDPPeerWithResult(target string, chain []Hop) (udpPacketPeer, string, error)
func (s *Server) openUDPPeerWithResultOptions(target string, chain []Hop, options DialOptions) (udpPacketPeer, string, error)
func (s *Server) resolveTargetCandidates(target string, chain []Hop) ([]string, error)
func Dial(ctx context.Context, network, target string, chain []Hop, provider TLSMaterialProvider, opts ...DialOptions) (net.Conn, error)
func DialWithResult(ctx context.Context, network, target string, chain []Hop, provider TLSMaterialProvider, opts ...DialOptions) (net.Conn, DialResult, error)
func SetOutboundProxyURL(raw string)
func OutboundProxyURL() string
func ResolveCandidates(ctx context.Context, target string, chain []Hop, provider TLSMaterialProvider) ([]string, error)
func relayDialTrafficClass(network string, options DialOptions) upstream.TrafficClass
func relayMetadataForDialOptions(network string, options DialOptions) map[string]any
func relayDialOptionsFromMetadata(network string, metadata map[string]any) DialOptions
func dialRelayTCPWithProxy(ctx context.Context, address string, _ Listener, proxyURL string) (net.Conn, error)
func dialTLSTCP(ctx context.Context, network, target string, chain []Hop, provider TLSMaterialProvider) (net.Conn, error)
```

Required imports in the new file are the imports those moved declarations actually use.

- [ ] **Step 4: Format and test**

Run:

```powershell
gofmt -w internal/relay/runtime.go internal/relay/dial_runtime.go internal/relay/transport_selection.go
go test ./internal/relay
```

Expected: PASS.

- [ ] **Step 5: Commit**

Run:

```powershell
git add go-agent/internal/relay/runtime.go go-agent/internal/relay/dial_runtime.go go-agent/internal/relay/transport_selection.go
git commit -m "refactor(relay): split dial runtime selection"
```

## Task 3: Split QUIC Server Handling

**Files:**
- Modify: `go-agent/internal/relay/runtime.go`
- Modify: `go-agent/internal/relay/quic_runtime.go`
- Test: existing tests under `go-agent/internal/relay`

- [ ] **Step 1: Capture current passing state**

Run:

```powershell
cd go-agent
go test ./internal/relay
```

Expected: PASS.

- [ ] **Step 2: Move QUIC server declarations**

Move these declarations from `runtime.go` into existing `go-agent/internal/relay/quic_runtime.go`:

```go
func (s *Server) acceptQUICLoop(ln *quic.Listener, listener Listener)
func (s *Server) handleQUICConn(conn *quic.Conn, listener Listener)
func (s *Server) handleQUICStream(conn *quic.Conn, stream *quic.Stream, listener Listener)
func (s *Server) trackQUICConn(conn *quic.Conn)
func (s *Server) untrackQUICConn(conn *quic.Conn)
func (s *Server) closeQUICConns()
```

Place the moved declarations after `startQUICListener` and before client-side QUIC dialing declarations so that server-side QUIC handling stays grouped near listener setup.

- [ ] **Step 3: Format and test**

Run:

```powershell
gofmt -w internal/relay/runtime.go internal/relay/quic_runtime.go
go test ./internal/relay
```

Expected: PASS.

- [ ] **Step 4: Commit**

Run:

```powershell
git add go-agent/internal/relay/runtime.go go-agent/internal/relay/quic_runtime.go
git commit -m "refactor(relay): split quic server handling"
```

## Task 4: Split UDP Stream Handling

**Files:**
- Create: `go-agent/internal/relay/udp_stream.go`
- Modify: `go-agent/internal/relay/runtime.go`
- Test: existing tests under `go-agent/internal/relay`

- [ ] **Step 1: Capture current passing state**

Run:

```powershell
cd go-agent
go test ./internal/relay
```

Expected: PASS.

- [ ] **Step 2: Move UDP relay stream declarations**

Create `go-agent/internal/relay/udp_stream.go` with `package relay` and move these declarations from `runtime.go` exactly:

```go
func listenerUsesEarlyWindowMask(listener Listener) bool
func (s *Server) handleUDPRelayStream(clientConn net.Conn, listener Listener, target string, chain []Hop, options DialOptions)
func pipeUDPPackets(clientConn net.Conn, upstream udpPacketPeer, recorder *traffic.Recorder)
```

Required imports in the new file are the imports those moved declarations actually use.

- [ ] **Step 3: Format and test**

Run:

```powershell
gofmt -w internal/relay/runtime.go internal/relay/udp_stream.go
go test ./internal/relay
```

Expected: PASS.

- [ ] **Step 4: Commit**

Run:

```powershell
git add go-agent/internal/relay/runtime.go go-agent/internal/relay/udp_stream.go
git commit -m "refactor(relay): split udp stream handling"
```

## Task 5: Split Pipe Helpers And Verify Final Runtime Shape

**Files:**
- Create: `go-agent/internal/relay/pipe_runtime.go`
- Modify: `go-agent/internal/relay/runtime.go`
- Test: existing tests under `go-agent/internal/relay`

- [ ] **Step 1: Capture current passing state**

Run:

```powershell
cd go-agent
go test ./internal/relay
```

Expected: PASS.

- [ ] **Step 2: Move pipe helper declarations**

Create `go-agent/internal/relay/pipe_runtime.go` with `package relay` and move these declarations from `runtime.go` exactly:

```go
func pipeBothWays(left, right net.Conn, recorder *traffic.Recorder)
func pipeBothWaysWithInitialRelayRX(left, right net.Conn, initialRX int64, recorder *traffic.Recorder)
func relayRecorderOrAggregate(recorder *traffic.Recorder) *traffic.Recorder
func closeWrite(conn net.Conn)
func closeRead(conn net.Conn)
```

Required imports in the new file are the imports those moved declarations actually use.

- [ ] **Step 3: Verify final `runtime.go` declaration list**

Run:

```powershell
rg -n "^(type|func|var|const) " internal/relay/runtime.go
```

Expected declaration list:

```text
type DialOptions struct {
type DialResult struct {
func (o DialOptions) clone() DialOptions {
type Server struct {
func Start(ctx context.Context, listeners []Listener, provider TLSMaterialProvider) (*Server, error) {
func (s *Server) startListener(listener Listener) error {
func (s *Server) Close() error {
func (s *Server) currentTrafficBlockState() TrafficBlockState {
func (s *Server) SetTrafficBlockState(state TrafficBlockState) {
func ListenersChanged(previous, next []Listener) bool {
```

- [ ] **Step 4: Format and test**

Run:

```powershell
gofmt -w internal/relay/runtime.go internal/relay/pipe_runtime.go
go test ./internal/relay
```

Expected: PASS.

- [ ] **Step 5: Commit**

Run:

```powershell
git add go-agent/internal/relay/runtime.go go-agent/internal/relay/pipe_runtime.go
git commit -m "refactor(relay): split runtime pipe helpers"
```

## Final Verification

After all tasks and reviews complete, run:

```powershell
cd go-agent
go test ./internal/relay
go test ./...
cd ..
git diff --check
git status --short
```

Expected:

- `go test ./internal/relay` passes.
- `go test ./...` passes.
- `git diff --check` reports no whitespace errors.
- `git status --short` is clean after all commits.

## Self-Review Notes

- Spec coverage: This plan covers the Phase 2 relay runtime split called out in the design document. It intentionally excludes the already split HTTP proxy and L4 server files, and it defers optional app runtime splitting until there is a narrower coupling-reduction plan.
- Placeholder scan: The declaration list and commands are explicit. No task asks for unspecified implementation or behavior design.
- Type consistency: All declarations keep their current names, receivers, parameters, return values, and package-private visibility.
