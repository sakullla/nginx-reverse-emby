# HTTP Tunnel Throughput Design

## Goal

Improve HTTP download throughput when a rule forwards traffic through a single-hop `tls_tcp` relay with `relay_obfs` enabled. The change should raise sustained transfer performance on high-latency links without changing control-plane schema, user-facing rule configuration, or relay protocol compatibility.

## Scope

In scope:

- Go agent data-plane changes under `go-agent/internal/proxy` and `go-agent/internal/relay`
- Single-hop HTTP relay downloads over `tls_tcp`
- Cases where `relay_obfs` is enabled on the relay listener
- Throughput improvements for long-lived response-body streaming
- Regression coverage for the download path

Out of scope:

- Control-plane API or database schema changes
- Frontend or panel changes
- Multi-hop relay redesign
- New user-visible tuning fields
- Replacing `tls_tcp` with a different relay protocol
- Large protocol refactors such as per-stream congestion control or parallel tunnel striping

## Problem Summary

The slow path is not initial connection setup. The observed behavior is a sustained throughput ceiling on repeated large downloads over the same single-hop `tls_tcp` relay path, which points to the steady-state relay data path rather than TLS handshake cost.

The current `tls_tcp` relay implementation has three characteristics that work against high-latency download performance:

- a single physical TCP tunnel serializes frame writes behind one tunnel-wide write mutex
- each logical stream write copies the caller payload into a fresh frame payload
- each logical stream read appends incoming frame payloads into one growing `[]byte`, then copies again into the consumer buffer

With `relay_obfs` enabled, the early masked window adds some extra framing overhead near the start of the stream, but the repeated-download symptom indicates that the main bottleneck is the ongoing logical-stream buffering and copying behavior, not setup overhead alone.

## Approaches

### Recommended: keep the existing protocol, optimize the `tls_tcp` data path

Preserve the current relay protocol and control-plane contract, but reduce per-chunk overhead in the `tls_tcp` logical stream implementation. This means tuning the underlying TCP connection for bulk transfer and replacing the current single-slice read buffer with a chunk queue so downloads stop paying repeated append-and-copy costs.

Pros:

- No protocol or schema migration
- Targets the likely root cause directly
- Lower rollout risk than redesigning mux behavior
- Keeps existing relay listener compatibility

Cons:

- Does not solve every theoretical `tls_tcp` limitation
- Throughput gains depend on the current bottleneck really being copy/buffer pressure

### Alternative: only tune socket and transport buffers

Increase TCP read/write buffers and related transport defaults while leaving the logical stream buffer model unchanged.

Pros:

- Smallest code change
- Low compatibility risk

Cons:

- Likely incomplete because it does not remove repeated allocations and copies in the hot path
- May improve peak throughput less than needed on high-RTT links

### Alternative: redesign the mux protocol

Change frame batching, add explicit flow control, or open multiple physical tunnels per route.

Pros:

- Highest theoretical ceiling

Cons:

- Much larger change surface
- Harder to verify safely
- Unnecessary before exhausting lower-risk data-path fixes

## Design

### 1. Relay tunnel socket tuning

When a client dials a `tls_tcp` relay tunnel, configure the underlying TCP connection for bulk transfer before wrapping it in TLS. The implementation should attempt to set larger OS socket buffers on both read and write directions and leave the current behavior unchanged if the platform rejects the hint.

This is a best-effort optimization, not a new requirement. Failure to apply the tuning must not break relay establishment.

### 2. Logical stream read buffering

Replace the current `tlsTCPLogicalStream.readBuf []byte` model with a queue of payload chunks plus a lightweight offset into the head chunk.

Target behavior:

- incoming `muxFrameTypeData` payloads are enqueued as chunks
- `Read` drains from the head chunk into the caller buffer
- fully consumed chunks are dropped immediately
- no repeated whole-buffer reallocation or slice shifting is performed for sustained downloads

This keeps protocol behavior unchanged while removing a large amount of hot-path copying for response streaming.

### 3. Logical stream write behavior

Keep the frame model unchanged, but avoid unnecessary work around writes where possible.

The implementation should preserve existing semantics:

- `Write` still maps one caller write to one `muxFrameTypeData` frame
- frame ordering stays serialized per physical tunnel
- close, FIN, and reset behavior remain unchanged

This work does not attempt to redesign the write scheduler. The immediate goal is to remove avoidable overhead in the existing steady-state path, not introduce a new mux discipline.

### 4. `relay_obfs` compatibility

No change to rule fields, listener fields, or the existing obfuscation mode names is introduced.

The optimized path must continue to work with:

- `relay_obfs=off`
- `relay_obfs=early_window_v2`

The obfuscation layer still only masks the configured early window. The throughput work should not rely on disabling obfuscation or shrinking its safety behavior.

### 5. Error handling and fallback behavior

If socket tuning cannot be applied:

- log nothing at normal level
- continue with the existing connection path

If the logical stream sees EOF, FIN, or reset:

- preserve the existing stream-close behavior
- release queued chunks promptly

If relay framing is malformed or the tunnel closes:

- preserve the current error propagation model to the HTTP proxy path

### 6. Testing strategy

Add focused Go tests for the relay implementation that cover:

- chunk-queue read behavior across multiple incoming data frames
- prompt release of consumed chunks and correct EOF propagation
- relay tunnel socket tuning as a best-effort path that does not fail the connection when unsupported

Add or extend integration-style tests in the proxy/relay packages to exercise large streamed downloads over single-hop `tls_tcp` relay with `relay_obfs` enabled. The test does not need to assert an exact Mbps target, but it should be structured to catch regressions in chunk handling and sustained streaming behavior.

## Acceptance Criteria

- HTTP downloads over a single-hop `tls_tcp` relay no longer rely on repeated whole-buffer append/copy behavior inside `tlsTCPLogicalStream`
- The raw relay TCP socket attempts bulk-transfer-friendly buffer sizing without making connection setup fragile
- Existing relay protocol compatibility remains intact
- `relay_obfs` enabled downloads continue to work without configuration changes
- Go test coverage is added for the optimized buffering behavior
- Existing `go-agent` test suites continue to pass
