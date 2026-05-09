# Go Agent Performance-First Refactor Design

Date: 2026-05-09

## Context

The `go-agent` execution plane is the active runtime for HTTP proxying, L4 proxying, relay transport, diagnostics, local runtime orchestration, certificates, traffic reporting, and update handling. A project scan found that the main production hotspots are concentrated in these packages:

- `internal/proxy`, especially `server.go`, `resume.go`, `http_engine.go`, and traffic handling.
- `internal/l4`, especially `server.go`, candidate selection, TCP/UDP copy paths, and relay path handling.
- `internal/relay`, especially `runtime.go`, `tls_tcp_session_pool.go`, copy paths, and listener validation.
- `internal/diagnostics`, especially HTTP/L4 probe relay path resolution.
- `internal/backends` and `internal/traffic`, which sit on hot decision and accounting paths.

The baseline command `cd go-agent && go test ./...` passes before the refactor. The current code has useful tests, but several helper patterns are duplicated across packages: relay listener endpoint selection, relay path expansion, URL and host normalization, copy wrappers, traffic block state storage, and traffic recorder flush behavior.

## Goals

This refactor is performance-first. The primary objective is to reduce hot-path overhead and make future performance work easier to reason about. Readability, deletion of dead compatibility code, and package structure improvements are also goals, but they follow the hot paths instead of driving a broad rewrite.

The work is allowed to include breaking changes. If protocol or configuration cleanup requires matching changes in `panel/backend-go` or `panel/frontend`, those changes are in scope for the later cleanup phase.

## Non-Goals

The first implementation phase will not change external timeout defaults, relay concurrency defaults, buffer sizes, or protocol semantics unless tests or benchmarks justify the change. It also will not begin by moving every large file. Large-file splitting is a separate phase after shared hot-path helpers exist.

## Chosen Approach

Use a staged performance-first refactor:

1. Extract hot-path helpers and unify duplicated behavior inside `go-agent`.
2. Split large hotspot files after helper boundaries are stable.
3. Remove old protocol and configuration compatibility paths with full-stack sync.
4. Use benchmarks and targeted tests to decide any parameter tuning.

This approach was chosen over an architecture-first split or protocol-cleanup-first plan because it creates measurable performance checkpoints while keeping each change reviewable.

## Phase 1: Hot-Path Shared Helpers

Phase 1 adds small, focused internal packages. These packages must not become a generic `utils` bucket.

### `internal/netutil`

`netutil` owns pure network and URL normalization helpers:

- Normalize host values.
- Resolve default ports and `host:port` strings.
- Normalize URL authority values.
- Extract client IP values.
- Select the dial endpoint for a relay listener from `PublicHost`, `BindHosts`, `ListenHost`, `PublicPort`, and `ListenPort`.

It may depend on the standard library and `internal/model`. It must not depend on `proxy`, `l4`, `diagnostics`, or `relay`.

Existing candidates to move include `proxy.normalizeHost`, `proxy.defaultPort`, `proxy.defaultPortString`, `proxy.normalizeURLAuthority`, and the duplicated `relayHopDialEndpoint` implementations in `proxy`, `l4`, and diagnostics.

### `internal/stream`

`stream` owns copy and lightweight IO wrappers:

- `CopyPreferReaderFrom`.
- `CopyGeneric`.
- Reader and writer wrappers that intentionally suppress `WriterTo` or `ReaderFrom` fast paths when needed.
- Traffic-aware writer/read closer helpers with configurable flush thresholds.

It may depend on `internal/traffic`. It must not depend on HTTP, L4, relay, or diagnostics packages.

Existing candidates to move include duplicated `copyPreferReaderFrom`, `readerWithoutWriterTo`, `copyGeneric`, relay traffic copy writers, and reusable response traffic flushing logic from the HTTP proxy.

### Traffic Block State

The duplicated traffic block state implementation in `proxy`, `l4`, and `relay` should be centralized. The preferred shape is to add the shared implementation under `internal/traffic`, while each package keeps a type alias or small adapter during migration so call sites remain explicit.

The shared implementation must preserve normalized reasons and atomic load/store semantics. Relay-specific error message behavior can remain in `relay` as package-specific presentation logic.

### `internal/relayroute`

`relayroute` owns shared relay route construction:

- `UsesRelay(chain, layers)`.
- Listener map construction.
- Relay path expansion from `RelayChain` and `RelayLayers`.
- Listener enabled checks and validation through `relay.ValidateListener`.
- Hop construction with `netutil` relay listener endpoints.
- Path cloning and key assignment helpers.

It may depend on `internal/model`, `internal/relay`, `internal/relayplan`, and `internal/netutil`. It should return base errors such as `relay listener 3 not found`, while callers wrap them with HTTP rule, L4 rule, or diagnostics context.

Existing candidates to move include HTTP, L4, and diagnostics relay path resolution and path clone helpers.

## Phase 2: Hotspot File Split

After Phase 1 stabilizes, split large files by responsibility without changing behavior:

- `internal/proxy/server.go` into runtime/listener startup, routing, request cloning, response copying, relay selection, and traffic helpers.
- `internal/l4/server.go` into server/listener startup, TCP handling, UDP handling, candidate selection, relay path handling, and traffic helpers.
- `internal/relay/runtime.go` into server/listener startup, dial handling, TCP relay handling, UDP relay handling, and traffic helpers.
- `internal/app/app.go` and `internal/app/local_runtime.go` only where the split reduces coupling to runtime managers or snapshot application.

The split should keep package names stable unless benchmarks or dependency boundaries show a clear benefit to new packages.

## Phase 3: Breaking Compatibility Cleanup

After hot-path refactors are complete, remove legacy compatibility paths and sync the full stack.

Candidate cleanups include:

- Remove L4 fallback fields such as `UpstreamHost` and `UpstreamPort` if `Backends` fully replaces them.
- Remove single HTTP `BackendURL` fallback if `Backends` fully replaces it.
- Consolidate relay chain configuration if `RelayLayers` becomes the only advanced model.
- Remove duplicate compatibility logic in backend snapshot merge or agent config handling when control-plane migrations cover it.

Any cleanup that changes stored data, API payloads, or UI forms must include matching changes in `panel/backend-go`, `panel/frontend`, docs, and migration behavior.

## Phase 4: Benchmark-Driven Tuning

Parameter tuning happens only after measurement. The refactor should add or organize benchmarks for:

- HTTP response copy and traffic accounting.
- HTTP upgrade copy paths.
- L4 TCP bidirectional copy.
- Relay TCP/UDP copy paths.
- Relay path expansion and clone/key assignment.
- Backend candidate ordering and observation-key construction.

The acceptance rule is no clear regression in the baseline benchmarks. Buffer size, flush threshold, or relay concurrency changes require benchmark evidence.

## Data Flow

HTTP, L4, and diagnostics call `relayroute` to resolve relay paths from rule configuration and listener snapshots. The caller provides the user-facing label for error context. During dialing, callers clone paths and assign target-specific keys before passing them to `relayplan.Racer`.

HTTP and L4 continue to use `backends.Cache` for candidate ordering and backoff. Candidate construction should use shared address normalization and endpoint helpers so relay and direct paths compute consistent observation and backoff keys.

Data transfer uses `stream` helpers for copy strategy and traffic-aware accounting. Package-specific wrappers remain only where an interface requires them, such as HTTP `ResponseWriter` wrapping.

Traffic block state is loaded and stored through the shared traffic state implementation. Package APIs can keep their existing state names during the transition.

## Error Handling

Shared packages return narrow errors with enough detail to diagnose the local failure. Calling packages wrap those errors with business context:

- HTTP wraps with `http rule "<frontend_url>"`.
- L4 wraps with listen address or rule context.
- Diagnostics wraps with probe context.
- Relay keeps listener and target context near the runtime handler.

This keeps shared packages independent from callers while preserving actionable logs and test assertions.

## Testing Strategy

Verification starts with existing tests:

- `cd go-agent && go test ./...`

New tests should cover the shared helper packages:

- `netutil`: host normalization, URL authority normalization, default ports, relay listener endpoint selection, and client IP parsing.
- `stream`: copy strategy selection, forced generic copy, traffic writer direction accounting, and flush threshold behavior.
- `traffic`: shared block state normalization and atomic load/store behavior.
- `relayroute`: relay usage detection, listener map behavior, missing/disabled/invalid listener errors, path expansion, hop construction, path clone independence, and target-specific key creation.

Regression tests must continue to cover:

- HTTP resume and range behavior.
- WebSocket and other upgrade responses.
- L4 TCP and UDP behavior, including initial payload handling.
- Relay TLS TCP, QUIC, and obfuscation behavior.
- Traffic counters and flush behavior.
- Diagnostics relay path reporting.

When Phase 3 touches the control plane or UI, add:

- `cd panel/backend-go && go test ./...`
- `cd panel/frontend && npm run build`
- `docker build -t nginx-reverse-emby .` for image-impacting changes.

## Acceptance Criteria

- Hot-path duplicated helpers are removed or reduced in HTTP, L4, relay, and diagnostics.
- Shared packages have clear dependencies and no reverse dependency on caller packages.
- Existing `go-agent` behavior is preserved through Phase 1 and Phase 2 unless a benchmarked change explicitly justifies a difference.
- Breaking changes in Phase 3 are synchronized across agent, backend, frontend, documentation, and migration behavior.
- Benchmarks show no obvious performance regression, and any tuning has measured support.
- Large hotspot files are split into responsibility-focused files after shared helper boundaries are established.

## Risks

The main risk is changing subtle networking behavior while moving helpers. Mitigation is to keep Phase 1 extractions small, add helper-level tests before replacing call sites, and run package-level regression tests after each batch.

Another risk is over-centralizing helpers into vague abstractions. Mitigation is to keep shared packages narrowly named and refuse helpers that only have one real caller.

Breaking compatibility cleanup can disrupt existing deployments. Mitigation is to defer it until after hot-path work, document migration behavior, and update backend/frontend in the same implementation phase.
