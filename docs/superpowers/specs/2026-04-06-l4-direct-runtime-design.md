# L4 Direct Runtime Design

## Goal
Deliver the first runnable L4 execution plane in the Go agent: a direct TCP/UDP proxy that listens on configured ports and shuttles data straight to the upstream target without introducing a relay chain.

## Context
- The existing `internal/l4/engine.go` only stores a minimal `Rule` struct and rejects any UDP rule that carries a relay chain. It does not yet surface the listen/upstream addresses or spin up listeners.
- Backend payloads already include `protocol`, `listen_host`, `listen_port`, `upstream_host`, `upstream_port`, `relay_chain`, and `revision`, so we can align the runtime model with that schema.
- We are only building the “direct” runtime for now; relay logic (multiple hops, TLS) stays disabled while we prove the end-to-end proxy.

## Approaches

1. **Single direct runtime that owns all rules (recommended).** Build one `Server` struct that iterates over the configured rules, opens a listener per rule, and dispatches either a TCP accept loop or UDP datagram loop in a goroutine. Lifecycle is driven by a shared `context.Context` and a `Close` method. Pros: centralized resource tracking, simple to test end-to-end for multiple listeners. Cons: slightly more plumbing to coordinate multiple goroutines, but manageable.

2. **Protocol-specific runtimes per rule.** Split the runtime into separate `tcp.Server` and `udp.Server` implementations and run one instance per rule. Pros: each server only deals with either streams or packets, making the internals very simple. Cons: need to wire a dispatcher to decide which server to instantiate per rule, and each test would still need to exercise both services, leading to more boilerplate.

3. **Leverage the HTTP runtime + TCP relays.** Reuse the HTTP proxy as a base by treating L4 connections as TCP stream proxies with generic dial/accept logic. Pros: minimal new code if the HTTP runtime already abstracts connection copying. Cons: HTTP runtime has semantics (JSON payload, TLS handling) that do not align with L4, and reusing it risks dragging in unused abstractions; plus UDP cannot be represented this way.

Recommendation: go with approach (1) because it keeps the control flow centralized, aligns well with the required test patterns, and makes it easier to share validation across protocols.

## Selected Design

- **Runtime model:** Extend `model.L4Rule` to capture `listen_host`, `listen_port`, `upstream_host`, `upstream_port`, `protocol`, `relay_chain`, and `revision`. The JSON tags match backend payloads so the runtime can be populated directly from the API.
- **Rule validation:** Update `internal/l4/engine.go` so `ValidateRule` accepts the richer struct and continues to reject `protocol=udp` when `relay_chain` is non-empty, while allowing UDP direct rules. The rest of the validation remains minimal (just the relay-chain rule) for now; malformed addresses will surface as listen/dial errors during startup.
- **Server lifecycle:** Implement `Server` in `internal/l4/server.go`. It accepts a `context.Context` and `[]model.L4Rule`. On `Start` we:
  1. Validate each rule via `ValidateRule`.
  2. For TCP rules, `net.Listen("tcp", net.JoinHostPort(...))`, add it to the listener slice, and spawn `acceptLoop`.
  3. For UDP rules, `net.ListenUDP("udp", addr)` and spawn `udpLoop`.
  4. Each goroutine increments a `sync.WaitGroup`, exits when the context is canceled, and reports errors through a channel if needed.
- **TCP proxying:** On accept, dial the upstream (`net.Dial("tcp", ...)`), then copy data bidirectionally with `io.Copy` in two goroutines or `io.CopyBuffer`. Close connections when either side closes.
- **UDP proxying:** For each inbound packet, dial a fresh UDP connection to upstream (so we avoid shared read loops), forward the packet, read the response with a short deadline, and write it back to the sender. Timeout or dial errors are logged/tested but do not crash the server.
- **Shutdown:** `Server.Close()` cancels the context, closes all listeners/conns, and waits for the wait group to finish.

## Testing Strategy

- **TCP end-to-end test (`server_test.go`).** Start an upstream TCP echo listener on a random port, create a direct rule that listens on another port, start the server, open a client TCP connection, write a payload, and verify the echoed bytes traverse the server.
- **UDP end-to-end test.** Start a UDP echo helper, configure a direct UDP rule, send packets to the listener, and assert responses are returned. Use timeouts to avoid flakiness and close all sockets at the end.
- **Validation regression:** Keep the existing engine validation tests for UDP relay rejection. They will continue to instantiate `Rule{Protocol: "udp", RelayChain: []int{...}}` and should still fail even after adding the new fields.

## Risks & Considerations

- With UDP we create a new upstream connection per packet; this is acceptable for the scope of this task but may need pooling later if throughput becomes a concern.
- There is no TLS, authentication, or relay chain logic yet; future tasks will add those layers once the direct runtime is stable.
- Error handling is intentionally minimal: listener/dial failures cause `Start` to return an error, and runtime sockets exit quietly when the parent context is canceled.

## Next Steps

1. Write this spec to disk under `docs/superpowers/specs/2026-04-06-l4-direct-runtime-design.md`.
2. Self-review the spec, eliminate placeholders, and ensure the architecture description matches the requirements.
3. Commit the spec so it can be reviewed.
4. Ask the user to review the spec before proceeding with the implementation plan.
