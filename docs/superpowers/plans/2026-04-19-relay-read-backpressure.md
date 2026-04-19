# Relay Read Backpressure Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prevent `tlsTCPLogicalStream` from buffering unbounded relay download payloads in user-space memory on slow downlink consumers.

**Architecture:** Add a per-stream queued-byte cap in the relay TLS TCP session pool and block the mux reader when that cap is reached. Keep the existing `WriteTo` fast path and wake blocked producers whenever consumers drain queued bytes or the stream closes.

**Tech Stack:** Go, `go test`, existing relay TLS TCP session pool tests.

---

### Task 1: Add a failing regression test

**Files:**
- Modify: `go-agent/internal/relay/tls_tcp_session_pool_test.go`

- [ ] **Step 1: Write the failing test**

Add a test that creates a logical stream with a very small buffered-read limit, queues enough bytes to fill it, starts a goroutine that appends one more chunk, verifies that goroutine blocks, then drains bytes with `Read()` and verifies the goroutine resumes.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd go-agent && go test ./internal/relay -run TestTLSTCPLogicalStreamAppendDataChunkBackpressuresSlowReader -count=1`

Expected: FAIL because the extra append completes immediately with the current unbounded queue behavior.

### Task 2: Implement bounded queued-read backpressure

**Files:**
- Modify: `go-agent/internal/relay/tls_tcp_session_pool.go`

- [ ] **Step 1: Add stream state for queued-byte accounting and producer wakeups**

Track queued bytes per stream and add a wakeup channel used by blocked `appendDataChunk()` producers.

- [ ] **Step 2: Apply the byte limit in `appendDataChunk()`**

Block additional queueing once the configured byte limit is reached, unless the queue is empty and needs to accept a single chunk.

- [ ] **Step 3: Decrement queued-byte accounting on drain paths**

Update `Read()`, `WriteTo()`, `prependReadChunk()`, `discardReadChunks()`, and close/error paths so queued bytes stay accurate and blocked producers wake promptly.

### Task 3: Verify relay tests

**Files:**
- Test: `go-agent/internal/relay/tls_tcp_session_pool_test.go`

- [ ] **Step 1: Re-run the focused regression test**

Run: `cd go-agent && go test ./internal/relay -run TestTLSTCPLogicalStreamAppendDataChunkBackpressuresSlowReader -count=1`

Expected: PASS.

- [ ] **Step 2: Run the full relay package tests**

Run: `cd go-agent && go test ./internal/relay`

Expected: PASS.
