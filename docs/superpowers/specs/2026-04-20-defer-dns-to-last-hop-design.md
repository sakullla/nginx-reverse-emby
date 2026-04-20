# Relay DNS Deferral Design

## Goal

When an HTTP rule or L4 rule uses a relay chain, the configured upstream hostname must remain unresolved until the last hop opens the final upstream connection. Relay mode must still preserve per-IP preference, failure backoff, and candidate recovery, but those decisions move to the last hop relay instead of the origin agent. Direct connections without a relay chain keep the current local DNS resolution behavior.

## Problem

The current agent resolves backend hostnames before calling `relay.Dial(...)`. That changes a configured target like `b.example.com:443` into one or more concrete `ip:port` candidates at the origin agent. In a relay path such as `A -> Relay A -> Relay B -> B`, DNS resolution therefore happens at `A`, not at the last relay hop adjacent to `B`.

This breaks the intended network model:

- Relay-adjacent DNS cannot see its own local resolver view.
- Geo-specific or split-horizon DNS answers are lost.
- Diagnostics and runtime behavior disagree with the rule semantics expected by the operator.
- If we simply stop origin-side resolution, relay mode would lose the current per-IP preference behavior for multi-record hostnames.

## Scope

In scope:

- HTTP proxy runtime candidate selection in `go-agent/internal/proxy`.
- L4 TCP and UDP runtime candidate selection in `go-agent/internal/l4`.
- Relay final-hop upstream opening in `go-agent/internal/relay`.
- HTTP and L4 diagnostics candidate construction where relay chains are supported.
- Tests covering relay and non-relay behavior.

Out of scope:

- Changing the relay wire protocol.
- Global cross-hop feedback of relay-side per-IP observations back to the origin agent.
- Altering direct-connect DNS caching, ordering, or backoff behavior.

## Design

### Rule Semantics

The agent uses two distinct upstream selection modes:

1. Direct mode, when `len(RelayChain) == 0`.
   The current behavior stays unchanged. The agent resolves the configured hostname locally, expands multiple IP candidates, applies cache-backed ordering and backoff, and dials the chosen `ip:port`.

2. Relay mode, when `len(RelayChain) > 0`.
   The origin agent must not resolve the configured hostname locally. It passes the configured `host:port` through the relay chain unchanged. The last relay hop performs DNS resolution, expands multiple IP candidates, and applies relay-local per-IP preference and failure backoff before opening the final upstream connection.

### HTTP Runtime

`routeEntry.candidates(...)` currently resolves every backend URL into concrete IP candidates. In relay mode it will instead emit a single configured-backend candidate using the backend URL's original authority rendered as `host:port`.

The HTTP transport already preserves the request URL host for upstream HTTP semantics while `DialContext` controls the socket destination. That means relay mode only needs to change the dial address generation at the origin side. The transport should continue calling `relay.Dial(...)`, but with the original configured `host:port`.

Origin-side observation and retry behavior in relay mode will operate at the configured backend target level because the origin agent no longer knows which concrete address the last hop selected. Per-IP observation and retry behavior are preserved at the last relay hop.

### L4 Runtime

`l4Candidates(...)` currently resolves each configured backend into concrete IP candidates for both direct and relay paths. In relay mode it will instead emit a single configured-backend candidate using the original configured `host:port`.

`dialTCPUpstream(...)` and `dialUDPUpstream(...)` already branch on relay usage. No wire protocol change is required. They only need the candidate address supplied by `l4Candidates(...)` to remain unresolved in relay mode.

As with HTTP, origin-side relay-mode observation and failure tracking become configured-backend scoped rather than resolved-IP scoped. The last relay hop becomes responsible for per-IP scoring, backoff, and recovery.

### Relay Final-Hop Resolution

The relay subsystem currently receives a `target` string and the last hop dials it directly. That final dial path must grow a relay-local resolver/cache layer for hostnames.

At the last hop:

- If the target host is already an IP literal, dial it directly as today.
- If the target host is a hostname, resolve it locally on the last hop.
- Expand all resolved IPs into `ip:port` candidates.
- Apply relay-local ordering, failure backoff, and recovery semantics before choosing a final dial target.
- Record relay-local observations against the concrete resolved `ip:port` candidates.

This preserves the current operational benefit of adaptive IP preference while relocating the decision to the correct network vantage point.

To keep scope controlled, the relay-local cache only needs to live within the relay runtime process. It does not need to synchronize observations back to the origin agent.

### Diagnostics

HTTP and L4 diagnostics should mirror runtime behavior. In relay mode, diagnostic candidate construction must avoid origin-side DNS resolution and probe the relay path using the configured `host:port`. In direct mode, diagnostics keep the current local resolution logic.

This prevents the diagnostics UI from reporting a path that differs from the runtime path. Diagnostics do not need to expose relay-local per-IP internals in this change; they only need to follow the same dialing semantics as runtime.

## Error Handling

- Relay mode still validates relay listener references exactly as today.
- Invalid configured upstream targets still fail during host/port normalization.
- DNS lookup failures for relay mode surface from the last hop resolver/dial path instead of the origin agent's local resolver.
- Relay-local per-IP failures participate in relay-local backoff before the final upstream connect attempt is reported as failed.
- Direct mode error messages and retry behavior remain unchanged.

## Testing

Add regression tests that prove:

- HTTP candidate building with a relay chain does not call the origin resolver and uses the configured hostname as the dial target.
- L4 candidate building with a relay chain does not call the origin resolver and uses the configured hostname as the dial target.
- Existing direct-connect tests continue to resolve hostnames locally.
- Relay-backed diagnostics follow the same rule and do not perform origin-side resolution.
- Relay final-hop dialing resolves hostnames locally, expands multiple IPs, and prefers healthy IP candidates across repeated attempts.
- Relay final-hop backoff suppresses recently failed resolved IPs while still retrying alternate resolved IPs for the same hostname.

## Risks

- Adaptive ranking shifts from the origin agent to the last relay hop, which increases implementation complexity in `internal/relay`.
- Existing tests that assume relay mode either resolves at the origin or dials the hostname directly without relay-local candidate management will need updates.

These trade-offs are acceptable because the current behavior is semantically wrong for relay routing. Correct DNS locality remains the primary requirement, and relay-local IP preference preserves the existing operational benefit for multi-record hostnames without violating that requirement.
