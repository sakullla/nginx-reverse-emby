# HTTP/3, QUIC Relay, and Relay Obfuscation Design

## Summary

This design makes `NRE_HTTP3_ENABLED` a real runtime feature, adds listener-scoped QUIC relay transport, enables L4 TCP and UDP relay over the shared relay subsystem, and replaces the current relay obfuscation method with a new early-window masking strategy for `tls_tcp` relay listeners.

The design keeps relay responsibilities separated:

- HTTP/3 and QUIC relay exist to improve transport performance and latency.
- Obfuscation remains a property of the `tls_tcp` relay transport and is focused on hiding early inner `ss` or `TLS` traffic signatures.

## Goals

- Make `NRE_HTTP3_ENABLED` enable real HTTP/3 ingress on HTTPS listeners.
- Add relay listener transport selection with `tls_tcp` and `quic`.
- Keep `tls_tcp` as the default relay transport.
- Support HTTP-over-relay, L4 TCP-over-relay, and L4 UDP-over-relay through the same relay subsystem.
- Support UDP relay on both QUIC and `tls_tcp`, using QUIC streams for the preferred path and UoT framing for the TCP fallback path.
- Replace the current relay obfuscation implementation with a new early-window masking method.
- Allow transport fallback when configured.

## Non-Goals

- Do not preserve the old `first_segment_v1` obfuscation implementation.
- Do not make QUIC relay responsible for traffic disguise.
- Do not optimize for inner protocols other than `TLS` and `ss` at this stage.
- Do not add native QUIC datagram support for UDP relay in this iteration.

## Current State

- `NRE_HTTP3_ENABLED` is parsed in agent config but does not affect the HTTP runtime.
- HTTP ingress only serves TCP TLS with `h2` and `http/1.1`.
- Relay transport is TCP plus TLS only.
- Relay obfuscation is a lightweight first-segment framing and padding method.
- L4 supports local TCP and UDP listeners, but relay traffic only works for TCP.
- UDP rules with relay chains are normalized away in the control plane.

## Final Design

### 1. HTTP Runtime

`NRE_HTTP3_ENABLED` controls whether HTTPS bindings also expose HTTP/3 over UDP QUIC.

For each HTTPS binding:

- TCP listener remains active for TLS, `h2`, and `http/1.1`.
- If `NRE_HTTP3_ENABLED=true`, a UDP QUIC listener is also started on the same port.
- HTTP routing, certificate selection, backend balancing, relay-aware upstream dialing, and header logic are shared between TCP and QUIC ingress.
- Failure to start the QUIC listener only disables HTTP/3 for that binding. TCP HTTPS remains active.

### 2. Relay Listener Model

`RelayListener` gains the following fields:

- `transport_mode`: `tls_tcp` or `quic`
- `allow_transport_fallback`: boolean, default `true`
- `obfs_mode`: `off` or `early_window_v2`

Rules:

- Default `transport_mode` is `tls_tcp`.
- Default `allow_transport_fallback` is `true`.
- Default `obfs_mode` is `off`.
- `obfs_mode` only applies when `transport_mode=tls_tcp`.
- `obfs_mode` is ignored for `transport_mode=quic`.

### 3. Relay Transport

Two relay transports are supported.

#### `tls_tcp`

- Keeps the current TCP plus TLS listener and dial path.
- Reuses the existing trust model: `pin_only`, `ca_only`, `pin_or_ca`, `pin_and_ca`.
- Replaces the old first-segment obfuscation implementation with `early_window_v2`.

#### `quic`

- Uses UDP QUIC as the relay transport.
- Keeps the same certificate and trust configuration semantics as relay TLS listeners.
- Uses one QUIC connection per hop and multiplexes multiple logical sessions as QUIC streams.
- Exists to reduce handshake cost, improve multiplexing, and lower latency on relay-heavy paths.

### 4. Relay Protocol v2

QUIC relay uses a new stream-oriented relay protocol.

For each logical relay session:

- One bidirectional QUIC stream is opened.
- The stream starts with an open frame containing:
  - `kind`: `tcp` or `udp`
  - `target`: final `host:port`
  - `chain`: remaining relay hops
  - `metadata`: reserved object for future use

After the open frame:

- TCP mode becomes raw byte streaming.
- UDP mode becomes framed packet streaming, where each UDP payload is length-prefixed and sent through the stream.

This keeps the transport model uniform while allowing UDP relay without building a second subsystem.

### 5. Relay Fallback

Fallback is controlled by listener configuration.

When a hop is configured as `quic`:

- The agent first attempts QUIC transport.
- If QUIC setup fails and `allow_transport_fallback=true`, the agent falls back to the `tls_tcp` transport for that hop.
- If fallback is disabled, the hop fails immediately.

Fallback is transport-level only. It does not silently change listener identity or route to a different relay listener.

### 6. L4 Relay

#### TCP

TCP relay supports both relay transports:

- `tls_tcp`
- `quic`

Each TCP connection maps to one relay session.

#### UDP

UDP relay supports both relay transports.

Implementation:

- QUIC path:
  - One UDP session maps to one QUIC stream.
  - Each UDP packet is encoded as one length-prefixed frame on that stream.
- `tls_tcp` path:
  - One UDP session maps to one relay TCP session.
  - UDP packets are carried through a UoT framing layer inside the TLS relay session.
- Session lifetime follows the existing UDP session management model: idle timeout, reply timeout, and backend health integration.

Control-plane validation changes:

- UDP rules may keep `relay_chain`.
- UDP rules are valid on both relay transports.
- `quic` remains the preferred path for lower latency and reduced head-of-line blocking.
- `tls_tcp` uses UoT as the compatibility path and does not promise the same latency characteristics as QUIC.
- `relay_obfs` only affects the `tls_tcp` transport and therefore may still apply to UDP relay sessions when they are carried through UoT.

### 7. Obfuscation

The old `first_segment_v1` implementation is removed and replaced by `early_window_v2`.

`early_window_v2` properties:

- Applies only to `tls_tcp` transport.
- Targets early inner `TLS` or `ss` traffic signatures without assuming exact protocol decoding.
- Operates on the first connection window, defined by both:
  - a maximum byte budget
  - a maximum early write budget
- Performs:
  - small-chunk splitting
  - bounded padding
  - disruption of repeated short-burst patterns
- Returns to direct pass-through after the early window closes.

This keeps the masking focused on hiding early inner-protocol structure without imposing long-lived throughput penalties.

### 8. UDP Over TCP Framing

When UDP is carried over `tls_tcp`, the relay transport uses UoT framing.

Properties:

- Each relay UDP session is isolated from other sessions.
- Each UDP packet is framed with an explicit length prefix.
- The framing layer is transport-only and does not change upstream UDP semantics beyond TCP-induced ordering and retransmission behavior.
- This path exists for compatibility and completeness, not as the preferred low-latency transport.

### 9. Capability Model

Agents advertise additional capabilities:

- `http3_ingress`
- `relay_quic`

The control plane uses these capabilities to validate whether an agent may receive:

- HTTP/3 ingress bindings
- QUIC relay listeners
- UDP relay chains

### 10. Data and Migration

Existing persisted `relay_obfs` boolean is migrated into listener obfuscation semantics:

- `false` becomes `obfs_mode=off`
- `true` becomes `obfs_mode=early_window_v2`

Transition rules:

- Snapshot decoding accepts the old boolean during migration.
- The runtime uses only `obfs_mode`.
- The old `first_segment_v1` runtime code path is removed.

The control plane keeps compatibility reads long enough to migrate persisted state, then schema cleanup can remove the legacy field in a later change.

## Runtime Flow

### HTTP Over Relay

1. HTTP rule resolves relay chain.
2. For each hop, the runtime chooses transport from the relay listener.
3. The runtime dials through QUIC or TCP relay as required.
4. QUIC transport reuses connections and opens a stream per backend session.

### L4 TCP Over Relay

1. Incoming TCP connection is accepted.
2. Relay chain is resolved.
3. TCP payload is proxied through one relay session.
4. QUIC transport uses one stream; TCP transport uses the existing dial path.

### L4 UDP Over Relay

1. Incoming UDP packet is assigned to a UDP session key.
2. If no active relay session exists, the runtime opens one relay session using the configured hop transport.
3. On QUIC, UDP packets are length-framed on the QUIC stream.
4. On `tls_tcp`, UDP packets are length-framed through the UoT session.
5. Reverse traffic is decoded and written back to the local UDP socket.

## Error Handling

- HTTP/3 listener startup failure does not tear down TCP HTTPS listeners.
- QUIC relay dial failures are surfaced clearly and may trigger transport fallback when allowed.
- Listener obfuscation settings on `quic` transport are ignored rather than treated as fatal misconfiguration.
- UDP relay over `tls_tcp` may exhibit higher latency or stronger head-of-line blocking than QUIC, but remains valid.

## Testing Strategy

### Agent Runtime

- Config tests for `NRE_HTTP3_ENABLED`.
- HTTP runtime tests proving:
  - HTTPS still serves TCP when HTTP/3 is disabled
  - QUIC listener starts when enabled
  - QUIC listener failure does not break TCP HTTPS
- Relay runtime tests proving:
  - QUIC listener startup and dial
  - QUIC stream multiplexing
  - transport fallback behavior
- L4 runtime tests proving:
  - TCP over QUIC relay
  - UDP over QUIC relay
  - UDP over `tls_tcp` relay through UoT
- Obfuscation tests proving:
  - early-window masking preserves byte stream fidelity
  - the early write window is masked and later traffic is pass-through

### Control Plane

- Validation tests for new relay listener fields.
- Capability-gated rule creation tests.
- L4 validation tests for UDP relay behavior on both relay transports.
- Migration tests for legacy `relay_obfs` data.

## Risks

- QUIC listener and relay lifecycle management is more complex than the current TCP-only model.
- UDP-over-stream framing is simpler than QUIC datagrams but introduces ordered delivery within a session.
- UDP over `tls_tcp` inherits TCP head-of-line blocking and retransmission costs.
- Listener-scoped transport fallback must not create ambiguous operational behavior.

## Recommendation

Implement this as one coordinated transport upgrade:

- make HTTP/3 ingress real
- add listener-scoped QUIC relay transport
- route TCP and UDP relay through the same relay subsystem
- support UDP fallback over `tls_tcp` through UoT
- replace the old obfuscation implementation with `early_window_v2`

This keeps transport concerns centralized while avoiding long-term split logic between HTTP, relay, and L4 execution paths.
