# Relay Tunnel Throughput And Latency Design

## Context

This repository already supports relay-backed traffic for HTTP, L4 TCP, L4 UDP, and QUIC relay listeners. Recent debugging established that the embedded local-agent apply path had been broken and is now fixed. After that fix, the remaining user-facing issue is not basic functionality but relay data-plane performance:

- `L4 TCP over tls_tcp relay` shows abnormally high low-load single-connection RTT.
- `L4 TCP over tls_tcp relay` also underperforms on large-stream throughput.
- `HTTP over tls_tcp relay` is functionally acceptable today, but should be able to reuse the same throughput improvements.
- The next version should also cover `UDP` and `QUIC`, but without forcing all protocols through one identical transport design.

The user explicitly accepts a protocol change and does not require wire compatibility with older agent binaries. This removes the need for a gray rollout or dual-stack long-term compatibility path.

## Problem Statement

There are two distinct issues in the current relay implementation:

1. `L4 TCP over tls_tcp relay` has extra startup latency under low load.
2. `tls_tcp relay` data transfer does extra per-frame copying and allocation, limiting throughput at a given CPU budget.

The current root-cause evidence for the RTT issue is:

- Every `L4 TCP` downstream connection opens a fresh relay logical stream.
- `relay.Dial()` on the `tls_tcp` path blocks on `openStream()`.
- `openStream()` writes an `OPEN` frame and waits for `OPEN_RESULT`.
- The relay server only returns `OPEN_RESULT` after it has already dialed the upstream target.

That means the first application byte cannot move until a full `OPEN -> upstream dial -> OPEN_RESULT` sequence completes. HTTP often hides this better because `http.Transport` reuses connections, so the stream-open cost is amortized across requests.

The current root-cause evidence for throughput inefficiency is:

- `tlsTCPLogicalStream.Write()` copies each write payload into a new slice before framing it.
- Read-side logical streams accumulate frame payloads in `[][]byte`, increasing allocation churn.
- `pipeBothWays()` relies on generic `io.Copy`, so high-volume flows still pay the stream framing cost for many small writes.

## Goals

### Primary goals

- Reduce low-load single-connection RTT for `L4 TCP over tls_tcp relay`.
- Improve `tls_tcp relay` throughput at the same CPU budget for large TCP and HTTP streams.
- Preserve correctness for half-close, reset, and stream teardown semantics.

### Secondary goals

- Improve or at least not regress `UDP over tls_tcp relay`.
- Improve or at least not regress `TCP/UDP over QUIC relay`.
- Keep the control-plane configuration model unchanged.

### Non-goals

- No attempt to preserve interoperability with older relay binaries.
- No control-plane API or schema redesign.
- No repo-wide refactor of unrelated transport layers.
- No attempt in this version to optimize beyond relay data plane, such as kernel tuning or OS-specific socket offload work.

## Constraints And Decisions

- The user prioritizes `same CPU, higher throughput`.
- The user also reported that current `L4` latency is abnormal even under low load, so latency must be part of the acceptance criteria.
- Relay may be a dedicated independent node, or may run on the B-side agent. Both topologies must be tested.
- The user wants the work implemented directly without a gray rollout.

## High-Level Design

The design splits relay improvements by transport family while sharing only the minimum common infrastructure.

### 1. `tls_tcp` stream redesign

This is the main work item and the first implementation target.

Keep:

- TLS tunnel establishment
- session pooling
- stream identity
- control frames for `open`, `open_result`, `fin`, and `rst`

Change:

- Extend `OPEN` to optionally carry initial application data.
- Change the `tls_tcp` data-plane framing so large payload movement uses pooled large chunks rather than repeated copy-per-write behavior.
- Remove the current “open must finish before any payload can be sent” restriction for the first chunk.

This redesign applies to:

- `L4 TCP over tls_tcp relay`
- `HTTP over tls_tcp relay`

### 2. `tls_tcp` UDP path tuning

Do not force UDP through the TCP bulk-stream design. UDP must preserve packet boundaries.

For `UDP over tls_tcp relay`, optimize the existing packet path by reducing:

- per-packet allocations
- frame serialization overhead
- lock contention on packet send and receive paths

Optional micro-batching is allowed only if it preserves application-visible packet boundaries and does not introduce harmful queueing delay.

### 3. `QUIC` path optimization

Treat QUIC as a separate implementation family.

For `TCP over QUIC relay`:

- inspect whether the stream-open path has a similar serialized first-byte delay
- improve open-to-first-byte flow if needed
- reduce per-stream allocation overhead where practical

For `UDP over QUIC relay`:

- evaluate whether the current packet path should continue using the current framing or move toward a lighter QUIC-native approach
- keep packet semantics intact

QUIC changes must not be forced into the `tls_tcp` protocol structure.

## Detailed Data Flow

### Current `L4 TCP over tls_tcp relay`

1. Downstream client connects to agent A.
2. A opens a relay logical stream.
3. A waits for `OPEN_RESULT`.
4. Relay dials B.
5. Relay sends `OPEN_RESULT`.
6. Only then does A start forwarding application bytes.

This adds a relay-side serialized wait before the first byte can reach the target.

### Proposed `L4 TCP over tls_tcp relay`

1. Downstream client connects to agent A.
2. A reads the first available application chunk from the downstream connection.
3. A sends `OPEN` with:
   - target
   - relay chain continuation
   - first payload chunk as `initial_data`
4. Relay receives the request and dials B.
5. As soon as B is connected, Relay writes `initial_data` to B immediately.
6. Relay returns `OPEN_RESULT`.
7. A and Relay continue with large-chunk steady-state transfer.

This removes one empty RTT-sized wait on the first-byte path.

### Proposed steady-state transfer for `tls_tcp`

After stream establishment:

- sender reads into pooled large chunks
- sender writes those chunks as relay data frames without a second payload copy
- receiver exposes those chunks to the consumer without turning every frame into avoidable short-lived slices
- the chunk pool is returned promptly on read completion

### Proposed `HTTP over tls_tcp relay`

HTTP continues to use the relay-aware `http.Transport`, but the underlying relay stream inherits the same optimized data path:

- request bodies benefit when present
- response body streaming benefits most
- keep-alive still amortizes connection establishment costs

The HTTP request lifecycle should not need protocol-specific behavior beyond inheriting the improved relay stream implementation.

## Protocol Changes

### `tls_tcp` control frame changes

`OPEN` payload gains:

- `kind`
- `target`
- `chain`
- `metadata`
- `initial_data` as optional opaque bytes

`OPEN_RESULT` stays structurally simple:

- `ok`
- `error`

No gray-path mode negotiation is needed because old/new mixed compatibility is not required for this version.

### `tls_tcp` data frame changes

The current data framing should be replaced with a large-chunk-oriented format that supports:

- direct payload carriage from pooled buffers
- explicit payload length
- stream association

The exact frame type naming may be either:

- keep `DATA` semantics but change implementation and handling, or
- introduce a new bulk-oriented frame type and update both ends together

The deciding rule during implementation is simplicity. Since compatibility with previous binaries is not needed, the implementation should prefer the version with the least complexity and the fewest transitional branches.

### Teardown semantics

The new transport must preserve:

- `FIN` for half-close
- `RST` for abnormal termination
- read-side EOF behavior
- write-after-close errors

These semantics are more important than squeezing out a small extra throughput gain.

## Component-Level Changes

### `go-agent/internal/relay/tls_tcp_session_pool.go`

Primary responsibility:

- new `OPEN + initial_data` handling
- new large-chunk data path
- reduced allocation and copy behavior in logical stream read/write paths

Expected changes:

- stream open request path
- tunnel read loop
- logical stream buffering
- server-side stream handling

### `go-agent/internal/relay/protocol.go`

Primary responsibility:

- frame payload schemas
- marshaling/unmarshaling for updated `OPEN`

### `go-agent/internal/relay/mux_protocol.go`

Primary responsibility:

- wire-level frame definitions for updated data path

### `go-agent/internal/l4/server.go`

Primary responsibility:

- feed first downstream bytes into relay stream establishment for `tcp`
- preserve proxy-protocol behavior
- keep `udp` path separate

### `go-agent/internal/proxy/server.go`

Primary responsibility:

- reuse the upgraded relay data path for HTTP through the existing relay-aware transport wiring

### `go-agent/internal/relay` QUIC files

Primary responsibility:

- adjust QUIC stream open and packet handling only where profiling or direct measurement shows avoidable latency/allocation overhead

## Buffering Strategy

Use a shared pooled chunk strategy for `tls_tcp` stream data.

Implementation rules:

- start with a fixed chunk size such as `64 KiB` or `128 KiB`
- prefer a single chunk pool abstraction over ad hoc buffers in each call path
- avoid copy-on-write when the data already sits in a pooled chunk
- return chunks deterministically once read-side consumers drain them

The first implementation should prefer correctness and reduced overhead over complicated auto-tuning logic.

## Remote-Agent Validation Topologies

The design must be tested in both supported user topologies:

1. Dedicated relay node

- A = remote source agent
- Relay = independent remote relay agent
- B = remote destination agent

2. Relay hosted on B-side

- A = remote source agent
- Relay = relay listener on B-side agent
- B = destination service behind the same B-side agent

Both topologies matter because the second one can hide or amplify different connection-establishment costs.

## Test Strategy

### Unit and integration tests

Required coverage:

- `OPEN + initial_data` carries first bytes exactly once
- half-close still works
- reset still interrupts blocked peers correctly
- large transfer over `tls_tcp` relay preserves data integrity
- HTTP streaming over relay still works
- UDP packet boundaries remain intact
- QUIC paths still pass existing transport semantics

### Docker end-to-end validation

Required scenarios:

- `A -> Relay -> B` remote-agent `L4 TCP over tls_tcp`
- `A -> Relay -> B` remote-agent `HTTP over tls_tcp`
- `A -> Relay -> B` remote-agent `UDP over tls_tcp`
- `A -> Relay(B-side) -> B` variants for the same traffic types
- at least one `TCP over QUIC` and one `UDP over QUIC` remote-agent validation case

### Performance verification

#### RTT checks

- `L4 TCP over tls_tcp relay`
- low-load single connection
- measure connect-to-first-byte or first round-trip latency before and after the change

#### Throughput checks

- `1` large TCP stream over relay
- `4` concurrent large TCP streams over relay
- large HTTP download over relay

#### CPU checks

- relay agent CPU during the throughput runs
- compare baseline vs new implementation under similar conditions

### Acceptance criteria

- `L4 TCP over tls_tcp relay` low-load RTT is materially lower than current behavior.
- `tls_tcp relay` large-stream throughput improves at comparable CPU usage.
- `HTTP over tls_tcp relay` does not regress and should benefit on large body transfers.
- `UDP` and `QUIC` paths do not regress functionally, and should improve where the measurements show a real hotspot.

## Implementation Order

1. Add targeted tests that capture the current `L4 TCP over tls_tcp` first-byte delay assumptions and large-stream behavior.
2. Upgrade `tls_tcp` protocol structs and frame handling.
3. Implement `OPEN + initial_data` for `L4 TCP`.
4. Replace `tls_tcp` large-stream buffering/copy behavior with pooled chunk transfer.
5. Validate `HTTP over tls_tcp` on the new data path.
6. Tune `UDP over tls_tcp` with packet-preserving optimizations.
7. Tune `TCP/UDP over QUIC` only after `tls_tcp` improvements are measured and stable.
8. Run remote-agent Docker validation for both relay topologies.

## Risks

### Protocol replacement risk

Because compatibility with older binaries is intentionally dropped, deployment must ensure all participating agents for a relay path are upgraded together.

### Data-integrity risk

`initial_data` introduces a classic duplication/loss hazard. Tests must prove that:

- first payload is not dropped
- first payload is not replayed
- half-close after initial payload still works

### Memory-lifetime risk

Pooled chunk ownership must be explicit. The code should avoid subtle reuse-after-return bugs by keeping buffer handoff simple and local.

### Scope risk

`UDP` and `QUIC` can expand the work substantially. The implementation plan should keep `tls_tcp / L4 TCP` first so the highest-value root cause is resolved early.

## Open Implementation Questions Resolved

- Gray rollout: not required.
- Mixed-version compatibility: not required.
- HTTP support: included in the same version.
- UDP support: included, but with packet-preserving optimization rather than TCP bulk framing.
- QUIC support: included, but treated as a separate transport optimization track.

## Recommendation

Implement this version as a focused relay data-plane rewrite centered on `tls_tcp`, starting with `L4 TCP`, then extending the shared gains to HTTP, and only then tuning UDP and QUIC. The earlier debugging already identified the RTT root cause and the throughput overhead source; the highest-value path is to fix both in the same `tls_tcp` redesign rather than trying unrelated micro-optimizations first.
