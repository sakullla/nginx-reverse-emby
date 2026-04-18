# Go Proxy Streaming Optimization Design

## Background

The current Go execution-plane proxy handles direct HTTP/HTTPS proxying and resumable media responses for Emby/Jellyfin-style playback. In high-RTT paths, sustained throughput is materially worse than Nginx, especially for large `200`/`206` media responses.

The main suspected bottleneck is the resumable copy loop in `go-agent/internal/proxy/resume.go`, which currently copies in `32KB` chunks and flushes after every chunk.

## Goals

- Improve sustained throughput for large media responses on the direct proxy path.
- Preserve correctness for `Range`, `206`, interruption resume, headers, and upgrade handling.
- Follow Go standard-library behavior and reuse patterns.
- Keep tests private: use synthetic hosts, fake tokens, and local `httptest` servers only.

## Non-Goals

- No rewrite to `httputil.ReverseProxy`.
- No relay-path redesign in this phase.
- No public fixture data, real tokens, or real upstream domains in tests.

## Design

### 1. Make resumable copying throughput-first

Replace the current per-32KB flush loop with an adaptive copy strategy:

- use a larger reusable buffer for response copying
- avoid flushing on every chunk
- flush only at the end, or at a conservative threshold when needed for streaming latency

The default behavior should match Go's proxy defaults: buffered forwarding, not immediate flush per write.

### 2. Keep resumable semantics intact

The resume path must still:

- drain interrupted bodies before retrying
- preserve `Range` and `206` behavior
- validate response continuity before resuming
- stop on short write, read error, or backend failure

### 3. Tune transport conservatively

Keep the shared `http.Transport` model, and only make conservative tuning changes if they are clearly tied to throughput or connection reuse.

Initial preference:

- preserve transport reuse
- keep HTTP/2 enabled
- keep idle connection pooling
- avoid speculative per-request transport creation

### 4. Private tests

Tests should cover:

- no per-chunk flush for large media copies
- resumable copy still works after interruption
- `206` and `Range` behavior remains correct
- transport reuse still works
- no secrets or real hostnames appear in fixtures or logs

## Implementation Plan

1. Refactor the resumable copy helper to support buffered transfer and conditional flush.
2. Update or replace the flush-focused test so it validates the new policy instead of enforcing flush-per-chunk.
3. Add regression tests for large-response throughput-friendly copying and resume correctness.
4. Run Go tests for affected packages.

## Verification

- `cd go-agent && go test ./...`
- If proxy transport behavior changes, run the package tests covering `internal/proxy` and `internal/app`.

## Review Notes

- Placeholder scan: none.
- Consistency: goals, non-goals, and implementation plan all align on a direct-path-only phase.
- Scope: contained enough for one implementation pass.
- Ambiguity resolved: "streaming optimization" means sustained throughput first, not aggressive low-latency flushing.
