# Relay DNS Deferral Design

## Goal

When an HTTP rule or L4 rule uses a relay chain, the configured upstream hostname must remain unresolved until the last hop opens the final upstream connection. Direct connections without a relay chain keep the current local DNS resolution behavior.

## Problem

The current agent resolves backend hostnames before calling `relay.Dial(...)`. That changes a configured target like `b.example.com:443` into one or more concrete `ip:port` candidates at the origin agent. In a relay path such as `A -> Relay A -> Relay B -> B`, DNS resolution therefore happens at `A`, not at the last relay hop adjacent to `B`.

This breaks the intended network model:

- Relay-adjacent DNS cannot see its own local resolver view.
- Geo-specific or split-horizon DNS answers are lost.
- Diagnostics and runtime behavior disagree with the rule semantics expected by the operator.

## Scope

In scope:

- HTTP proxy runtime candidate selection in `go-agent/internal/proxy`.
- L4 TCP and UDP runtime candidate selection in `go-agent/internal/l4`.
- HTTP and L4 diagnostics candidate construction where relay chains are supported.
- Tests covering relay and non-relay behavior.

Out of scope:

- Changing the relay wire protocol.
- Moving adaptive multi-IP selection into the relay subsystem.
- Altering direct-connect DNS caching, ordering, or backoff behavior.

## Design

### Rule Semantics

The agent uses two distinct upstream selection modes:

1. Direct mode, when `len(RelayChain) == 0`.
   The current behavior stays unchanged. The agent resolves the configured hostname locally, expands multiple IP candidates, applies cache-backed ordering and backoff, and dials the chosen `ip:port`.

2. Relay mode, when `len(RelayChain) > 0`.
   The agent must not resolve the configured hostname locally. It passes the configured `host:port` through the relay chain unchanged, and the last relay hop performs the final DNS resolution as part of its own dial path.

### HTTP Runtime

`routeEntry.candidates(...)` currently resolves every backend URL into concrete IP candidates. In relay mode it will instead emit a single candidate per configured backend using the backend URL's original authority rendered as `host:port`.

The HTTP transport already preserves the request URL host for upstream HTTP semantics while `DialContext` controls the socket destination. That means relay mode only needs to change the dial address generation. The transport should continue calling `relay.Dial(...)`, but with the original configured `host:port`.

Observation and retry behavior in relay mode will operate at the configured backend target level rather than per resolved IP. This matches the fact that the origin agent no longer knows which concrete address the last hop selected.

### L4 Runtime

`l4Candidates(...)` currently resolves each configured backend into concrete IP candidates for both direct and relay paths. In relay mode it will instead emit a single candidate using the original configured `host:port`.

`dialTCPUpstream(...)` and `dialUDPUpstream(...)` already branch on relay usage. No protocol change is required. They only need the candidate address supplied by `l4Candidates(...)` to remain unresolved in relay mode.

As with HTTP, relay-mode observation and failure tracking become configured-backend scoped rather than resolved-IP scoped.

### Diagnostics

HTTP and L4 diagnostics should mirror runtime behavior. In relay mode, diagnostic candidate construction must avoid local DNS resolution and probe the relay path using the configured `host:port`. In direct mode, diagnostics keep the current local resolution logic.

This prevents the diagnostics UI from reporting a path that differs from the runtime path.

## Error Handling

- Relay mode still validates relay listener references exactly as today.
- Invalid configured upstream targets still fail during host/port normalization.
- DNS lookup failures in relay mode surface from the last hop connection attempt instead of the origin agent's local resolver.
- Direct mode error messages and retry behavior remain unchanged.

## Testing

Add regression tests that prove:

- HTTP candidate building with a relay chain does not call the resolver and uses the configured hostname as the dial target.
- L4 candidate building with a relay chain does not call the resolver and uses the configured hostname as the dial target.
- Existing direct-connect tests continue to resolve hostnames locally.
- Relay-backed diagnostics follow the same rule and do not perform origin-side resolution.

## Risks

- Adaptive ranking in relay mode becomes less granular because multiple resolved IPs are no longer visible at the origin agent.
- Existing tests that assume relay mode still expands DNS candidates will need to be updated to the new semantics.

These trade-offs are acceptable because the current behavior is semantically wrong for relay routing. Correct DNS locality takes priority over origin-side per-IP scoring when a relay chain is explicitly configured.
