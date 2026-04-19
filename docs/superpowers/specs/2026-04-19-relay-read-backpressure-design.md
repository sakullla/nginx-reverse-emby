# Relay Read Backpressure Design

## Problem

Under realistic download latency (`CLI -> A = 30ms` one-way, `A -> Relay B = 10ms` one-way), `L4+Relay` can stall and time out on long downlink transfers. Profiling shows the bottleneck is not sustained CPU saturation in `relay-b`; instead, `agent-a` accumulates several GiB of queued relay payloads in `tlsTCPBulkBufferPool` allocations.

## Root Cause

`tlsTCPTunnel.readLoop()` keeps reading mux DATA frames and hands them to `tlsTCPLogicalStream.appendDataChunk()` without any per-stream backpressure. When the downstream client on `agent-a` is slower than the relay ingress, `readChunks` grows without bound and moves the bottleneck from socket/kernel buffers into Go heap memory.

## Design

Add a per-logical-stream buffered-read byte limit in `tls_tcp_session_pool.go`. When queued bytes for a stream would exceed the limit, `appendDataChunk()` blocks until the consumer drains queued bytes or the stream/tunnel closes. This keeps backpressure on the relay socket instead of on unbounded Go heap allocations.

The limit is byte-based rather than chunk-count-based so it matches actual retained memory. The implementation should allow one chunk when the queue is empty, even if that chunk alone exceeds the limit, to avoid deadlock on oversized frames.

## Behavior Changes

- Slow downlink consumers stop causing unbounded `readChunks` growth.
- Relay ingress pauses once the stream queue reaches the configured byte limit.
- Existing `WriteTo` fast path remains intact.
- Stream close/error paths wake blocked producers so they do not hang.

## Testing

- Add a regression test that proves `appendDataChunk()` blocks once the queued-byte limit is reached and resumes after `Read()` drains data.
- Run focused relay tests first, then the full relay package tests.
