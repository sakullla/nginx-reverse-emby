# Go-Agent Weak-Network Adaptive Upstream Design

## Summary

This design upgrades `go-agent` from fixed upstream transport behavior to adaptive weak-network behavior that works for both direct upstream access and relay-based access. The goal is to improve startup latency, reduce stalls and disconnects, recover throughput under loss and high RTT, and reduce head-of-line blocking without adding user-facing tuning switches.

The design keeps existing HTTP, L4, and relay protocol semantics intact where possible, but introduces a shared upstream policy layer that all three traffic planes use. Relay protocol changes are allowed and must remain backward compatible during rollout.

## Goals

- Improve startup time for small requests and media playback under weak networks.
- Reduce stream interruption frequency for HTTP streaming, TCP relay, and UDP relay.
- Improve sustained throughput under packet loss, high latency, and jitter.
- Reduce head-of-line blocking in `tls_tcp` relay tunnels.
- Apply improvements to direct upstream traffic as well as relay traffic.
- Make the behavior automatic by default, without new required control-plane settings.

## Non-Goals

- Full multipath transport across multiple upstream paths for a single logical stream.
- Application-aware media chunking or transcoding changes.
- User-facing transport tuning UI in this phase.
- Replacing existing adaptive backend ordering logic in `internal/backends`; this design extends and feeds it.

## Current State

The current codebase already contains useful pieces:

- `internal/proxy` uses a shared `http.Transport`, backend observations, and resume support for HTTP range requests.
- `internal/l4` supports direct upstream TCP and UDP, relay dialing, and buffered initial payload forwarding for relay TCP.
- `internal/relay` already supports `quic` relay, `tls_tcp` relay multiplexing, session pools, and transport fallback from `quic` to `tls_tcp`.

The weak-network issues come from a common pattern across these areas:

- fixed dial, handshake, frame, reply, and header timeouts
- single-path attempt behavior for most connections
- transport pools that do not isolate latency-sensitive traffic from bulk traffic
- no shared scoring model across direct and relay paths
- `tls_tcp` multiplexing that can still accumulate queueing pressure and HOL-like effects when large and small streams mix

## High-Level Approach

Introduce a shared adaptive upstream layer in `go-agent/internal/upstream/` and route HTTP direct, L4 direct, and relay transport decisions through it.

This layer owns:

1. request classification
2. path planning
3. connection and tunnel pool coordination
4. weak-network scoring
5. adaptive timeout estimation
6. guarded fallback and recovery

The existing `proxy`, `l4`, and `relay` packages remain responsible for protocol semantics and data movement. They stop making isolated transport policy decisions.

## Architecture

### 1. Upstream Classifier

The classifier tags traffic into one of three classes:

- `interactive`: small requests, metadata, images, short control traffic, requests without clear bulk characteristics
- `bulk`: video range traffic, long-lived transfers, large responses, TCP forwarding sessions, and sustained UDP sessions
- `unknown`: a short-lived initial state before enough information is available

Classification inputs:

- HTTP method and request headers such as `Range`
- response size hints and streaming behavior when available
- L4 protocol type and connection duration signals
- observed bytes transferred and transfer duration after completion

Classification rules must be automatic and local. No control-plane changes are required to opt in.

### 2. Path Model

Each usable upstream path is modeled uniformly, even when the transport differs:

- direct HTTP/TCP upstream
- direct UDP upstream
- relay over `quic`
- relay over `tls_tcp`

Each path record stores recent:

- handshake latency
- first-byte or first-reply latency
- short-window throughput
- timeout rate
- post-connect disconnect rate
- recent backoff state
- confidence level based on sample count

The path abstraction allows the same planner to compare direct and relay candidates without separate logic trees.

### 3. Dial Planner

The dial planner chooses:

- which path family to try first
- whether to race a fallback path
- whether to reuse an existing connection or open a new one
- whether a request should avoid a congested shared tunnel

Default behavior:

- `interactive` requests may use a bounded two-path race when confidence is low or the primary path is unstable.
- `bulk` requests prefer known high-throughput paths and avoid broad racing.
- already-degraded paths are only used for limited probe traffic until they prove stable.

This is intentionally more conservative than generic happy-eyeballs. The goal is to improve startup time without allowing bulk traffic to stampede the network.

### 4. Shared Pool Manager

The pool manager provides a single load view across:

- HTTP direct keep-alive connections
- relay `quic` sessions
- relay `tls_tcp` tunnels
- L4 direct connections where reuse is meaningful

The key addition is logical separation by traffic class:

- latency-first pool usage for `interactive`
- bulk-first pool usage for `bulk`

This is not a new public API. It is an internal scheduling rule that prevents small requests from sitting behind large flows.

### 5. Adaptive Timeout Estimator

Replace fixed transport timers with estimated timers built from:

- rolling observed handshake latency
- rolling first-byte latency
- recent tail spikes
- path confidence

Examples:

- relay handshake timeout should become `base + multiplier * estimated_handshake_latency`, bounded by sane floors and ceilings
- HTTP response-header timeout should tolerate known high-latency but stable paths
- UDP reply timeout in `internal/l4` should grow for high-RTT paths instead of remaining fixed at one second

The estimator must preserve upper bounds so broken paths still fail quickly.

### 6. Recovery Controller

The recovery controller manages failure and re-entry:

- fast penalty for hard failures
- slow recovery through probe traffic
- bounded speculative retries
- automatic contraction under local resource pressure

This prevents oscillation where a flaky path alternates between winning and failing every few requests.

## Package and Module Boundaries

### New package

Create `go-agent/internal/upstream/` with focused files such as:

- `classify.go`
- `planner.go`
- `score.go`
- `timeouts.go`
- `types.go`
- `resources.go`

### Existing package integration

#### `internal/proxy`

- Keep HTTP proxy request cloning, response rewriting, and stream resume behavior.
- Route backend transport choice through the new planner.
- Split connection pressure accounting by traffic class.
- Feed response-header latency, total duration, and throughput observations back to upstream scoring.

#### `internal/l4`

- Keep TCP and UDP proxy semantics.
- Replace direct single-path upstream selection with planner output.
- Use adaptive UDP reply timeout per session.
- Feed connect latency, disconnect timing, and sustained transfer signals back to upstream scoring.

#### `internal/relay`

- Keep protocol framing and server behavior.
- Move path choice, fallback policy, and tunnel selection inputs into the shared layer.
- Extend session and tunnel pools with load reporting and traffic-class-aware selection.
- Add compatibility-safe protocol metadata when needed.

## Direct Upstream Strategy

Weak-network optimization must work when relay is not configured.

### HTTP Direct

For direct HTTP upstream:

- preserve `http.Transport` reuse
- stop treating all requests as equivalent
- prevent long media flows from consuming the best reusable connections for small requests
- allow bounded fallback racing when the current path is unstable or cold

The transport remains Go standard library based. The change is in selection, isolation, and timeout policy.

### L4 Direct TCP

For direct L4 TCP:

- classify long-lived sessions as `bulk`
- allow a limited alternate-path probe when recent attempts show high failure or timeout rates
- avoid static connect timeout assumptions

For single-address upstreams, this mainly means better timeout estimation and better interaction with backend candidate ordering. For multi-address upstreams, it also means smarter candidate promotion.

### L4 Direct UDP

For direct UDP:

- replace the fixed reply timeout with an adaptive estimate
- avoid penalizing known-high-latency but stable paths as if they were dead
- keep session reuse, but re-evaluate path scoring based on reply delay and loss patterns

## Relay Strategy

### QUIC Relay

`quic` remains the preferred weak-network transport when it is actually performing well, but the planner must stop assuming that all weak networks benefit from it equally.

Changes:

- keep pooled `quic` sessions
- score `quic` by handshake success, open-stream latency, first-byte latency, and sustained throughput
- allow `quic` to lose priority for bulk traffic when recent loss makes throughput unstable
- keep fallback to `tls_tcp`, but drive it by path score and confidence instead of only immediate failure

### TLS/TCP Relay

`tls_tcp` relay needs the biggest scheduling changes.

Changes:

- expand tunnel load reporting beyond simple stream counts
- track queue depth and estimated buffered bytes per tunnel
- maintain multiple active tunnels per key
- prefer lightly loaded tunnels for `interactive`
- prefer separate or bulk-tagged tunnels for `bulk`
- avoid admitting new `interactive` streams to congested tunnels

This reduces HOL effects without changing the user-facing relay model.

### Relay Protocol Compatibility

Relay protocol evolution is allowed, but rollout must be backward compatible.

If new metadata is required, add optional fields to `relayOpenFrame.Metadata`. Possible examples:

- traffic class hint
- planner-selected path identity
- optional tunnel preference hint

Old peers must ignore unknown metadata. New peers must continue working when metadata is absent.

## Request Flow

### HTTP Flow

1. Receive request in `internal/proxy`.
2. Classify as `interactive`, `bulk`, or `unknown`.
3. Ask planner for candidate path order and racing policy.
4. Reuse or open connection according to class-aware pool rules.
5. Complete request.
6. Report handshake, header latency, transfer duration, bytes, and recovery outcome.
7. Update path score.

`unknown` requests start with conservative interactive behavior and are promoted to `bulk` once transfer behavior proves it.

### L4 TCP Flow

1. Accept downstream TCP connection.
2. Classify early based on rule type and initial behavior.
3. Ask planner for upstream candidate selection.
4. Dial direct or relay through planner-selected path.
5. Pipe data bidirectionally.
6. Feed connect latency, early disconnect, and transfer size back into scoring.

### L4 UDP Flow

1. Receive packet and find or create UDP session.
2. Ask planner for upstream path if session is new.
3. Use adaptive reply timeout for that path.
4. Update score based on reply timing and error behavior.

## Symptom-to-Strategy Mapping

### Low Startup Speed

Addressed by:

- bounded two-path racing for cold or unstable interactive traffic
- prewarmed healthy pooled connections when available
- faster separation of small requests from busy bulk pools

### Stalls and Disconnects

Addressed by:

- earlier path downgrading after post-connect failures
- adaptive timeout estimates instead of static low thresholds
- more aggressive but bounded HTTP resume triggers for eligible range responses
- probe-only recovery for unstable paths

### Throughput Collapse

Addressed by:

- explicit throughput scoring, not only latency scoring
- bulk traffic preferring high-throughput paths over lowest-latency paths
- bulk tunnel separation in `tls_tcp` relay
- avoiding heavy racing for bulk traffic

### Loss and High RTT

Addressed by:

- timer estimation from observed path behavior
- lower penalty for high-latency paths that still succeed consistently
- faster penalty for repeated timeout clusters and high jitter tails
- transport choice based on recent path performance, not protocol preference alone

### Head-of-Line Blocking

Addressed by:

- class-aware tunnel admission in `tls_tcp`
- tunnel congestion thresholds using queue depth and buffered bytes
- bulk and interactive separation at the scheduling level

## Error Handling and Guardrails

### Failure Weighting

Treat these as high-severity failures:

- handshake timeout
- response-header or first-reply timeout
- immediate post-connect disconnect
- repeated relay stream open failures

These should rapidly reduce path priority.

### Slow Recovery

Recovery happens through probe traffic only:

- limited sample volume
- gradual score restoration
- no immediate promotion after a single success

### Bounded Speculation

Speculative racing is capped:

- at most two paths for interactive requests
- no uncontrolled racing for bulk flows
- no racing when local resource pressure is high

### Resource Pressure Contraction

When local process pressure rises, reduce aggressiveness automatically:

- stop racing fallback paths
- prefer reuse over new tunnel creation
- cap tunnel growth

Resource inputs can include:

- active connections
- relay tunnel count
- queued write depth
- open HTTP requests

## Data and State

The scoring state should be local, in-memory, and resettable. It does not need durable persistence in phase one. Existing backend observation caches remain useful and can be extended or fed by the new layer.

If later persistence is desirable, it should be optional and carefully bounded to avoid stale network assumptions surviving environment changes.

## Observability

Add low-noise internal visibility for debugging:

- selected path family
- whether racing happened
- traffic class
- current path score summary
- current adaptive timeout values
- tunnel load summary for `tls_tcp`

This should be suitable for diagnostics and logs without requiring a new mandatory UI.

## Testing Strategy

### Unit Tests

Add test coverage for:

- request classification
- path scoring and confidence behavior
- adaptive timeout estimation bounds
- recovery controller state transitions
- tunnel selection under mixed small and bulk stream loads

### Integration Tests

Extend existing package tests to cover:

- HTTP direct under delayed-header and delayed-body conditions
- HTTP direct small-request isolation from concurrent large transfers
- L4 TCP direct under connect delay and early disconnect
- L4 UDP direct under delayed reply and loss-like timeout patterns
- relay `quic` score downgrade and `tls_tcp` recovery
- relay `tls_tcp` tunnel separation for mixed flow sizes

### Fault Injection

Use existing test seams and add new ones where needed for:

- dial delay
- handshake delay
- response-header delay
- write queue congestion
- packet loss simulation
- high-latency success cases

### Verification Criteria

Minimum success criteria for rollout:

- interactive requests start faster under mixed weak-network simulations
- large transfers show higher median and tail throughput under loss and high RTT
- fewer false backoffs for stable high-latency paths
- small requests are not starved by concurrent bulk relay traffic
- no regression in normal-network direct and relay operation

## Rollout Plan

### Phase 1: Shared scoring and classification

- add `internal/upstream`
- implement classifier, scorer, timeout estimator
- integrate with existing observation points in `proxy` and `l4`

### Phase 2: Direct path adoption

- wire HTTP direct and L4 direct selection through planner
- add class-aware HTTP transport pressure handling
- make UDP reply timeout adaptive

### Phase 3: Relay adoption

- integrate planner with relay path choice
- upgrade `tls_tcp` tunnel selection and congestion accounting
- refine `quic` versus `tls_tcp` fallback using shared scoring

### Phase 4: Compatibility metadata and diagnostics

- add optional relay metadata if needed
- expand diagnostics output and logging summaries

## Risks

- Over-aggressive racing can increase local and upstream load.
- Weak heuristics can misclassify traffic and hurt normal-network performance.
- Too-fast recovery can cause route flapping.
- Too-slow recovery can leave performance on the table after transient issues clear.
- Relay tunnel load measurement that only counts streams will be insufficient; queue metrics are required.

## Mitigations

- keep speculation bounded and resource-aware
- use conservative defaults for unknown traffic
- recover slowly through probes
- preserve fallback to current transport behavior when planner confidence is absent or code paths fail

## Implementation Defaults

The implementation plan should preserve these defaults unless tests show a clear regression.

### Confidence Thresholds

- allow bounded two-path racing for `interactive` when path confidence is below `0.35`
- stop racing and trust the primary path once confidence reaches `0.60`
- mark a degraded path as probe-only after two consecutive high-severity failures within the active observation window
- require three consecutive successful probes before leaving probe-only state

### Adaptive Timeout Bounds

Use `max(floor, min(ceiling, base + multiplier * estimate))` with these defaults:

- dial timeout: floor `2s`, ceiling `12s`
- relay handshake timeout: floor `3s`, ceiling `15s`
- relay frame timeout: floor `3s`, ceiling `15s`
- HTTP response-header timeout: floor `5s`, ceiling `45s`
- L4 UDP reply timeout: floor `500ms`, ceiling `5s`

Initial multipliers:

- dial and handshake: `base + 4x estimate`
- response-header and first-reply timers: `base + 5x estimate`

### Direct HTTP Isolation Model

Direct HTTP class isolation should use two cloned transports derived from the same base settings:

- one `interactive` transport with tighter concurrency and lower tolerance for queue buildup
- one `bulk` transport with higher per-host concurrency and no priority for reuse by small requests

Per-request planner coordination selects between those transports. The design does not use a single transport with mixed class scheduling.

### TLS/TCP Tunnel Congestion Thresholds

Treat a `tls_tcp` tunnel as congested for new `interactive` admission when either condition is met:

- queued logical-stream writes exceed `4`
- estimated buffered outbound payload exceeds `512 KiB`

When congested:

- reject new `interactive` placement on that tunnel
- continue admitting `bulk` only if no less-loaded bulk-designated tunnel exists and the session-per-key ceiling has been reached

### Session Growth Defaults

- keep the existing `tlsTCPMuxSessionsPerKey` ceiling as the initial hard cap
- prefer opening a new `tls_tcp` tunnel for `bulk` once the least-loaded existing tunnel has at least `2` active logical streams
- keep `quic` session reuse as the default and do not open parallel sessions for the same key unless the pooled session becomes unavailable
