# HTTP H2 H3 Listener Design

## Goal

Make HTTPS listeners in `go-agent` advertise and serve HTTP/2 by default, while reserving HTTP/3 behind an environment variable that stays disabled by default.

## Scope

This design covers only the `go-agent` HTTPS listener behavior.

Included:
- Explicit HTTP/2 enablement on HTTPS listeners
- A new environment-variable-backed HTTP/3 toggle in agent config
- Tests for config parsing and TLS protocol advertisement

Excluded:
- Actual QUIC or UDP listener startup
- Alt-Svc advertising
- Control-plane UI or API changes
- New capability negotiation for HTTP/3

## Current State

`go-agent` terminates HTTPS with `tls.NewListener` in `go-agent/internal/proxy/server.go`. The TLS config does not explicitly declare ALPN protocols. There is no HTTP/3 or QUIC runtime path in the HTTP proxy listener, and agent config does not expose any HTTP protocol toggles.

## Approaches

### Recommended: Explicit H2 + Reserved H3 Toggle

Explicitly set HTTPS TLS `NextProtos` to include `h2` and `http/1.1`. Add `NRE_HTTP3_ENABLED` to agent config, defaulting to `false`, but do not attach any QUIC listener yet.

Why this is preferred:
- It delivers the requested HTTP/2 behavior now.
- It keeps HTTP/3 off by default as requested.
- It avoids introducing partial QUIC runtime behavior that would require UDP sockets, Alt-Svc coordination, and wider testing.

### Alternative: Full H2 + H3 Runtime

Add HTTP/2 plus a real HTTP/3 server behind an environment variable.

Why this is not chosen now:
- It materially expands scope.
- It needs new dependencies and listener lifecycle changes.
- It creates more failure modes than the current request justifies.

## Design

### Config

Add `HTTP3Enabled bool` to `go-agent/internal/config/config.go`.

Parse it from `NRE_HTTP3_ENABLED`:
- default: `false`
- accepted true values should follow Go boolean parsing via the existing style used elsewhere in the repo
- invalid values should fail config loading with a clear error

This config only records intent for now. It does not guarantee that HTTP/3 traffic is served.

### HTTPS Listener Behavior

Update the HTTPS TLS config in `go-agent/internal/proxy/server.go` so ALPN explicitly includes:
- `h2`
- `http/1.1`

This should apply only to HTTPS listeners. Plain HTTP listeners are unchanged.

The TLS config should not advertise `h3` at this stage. HTTP/3 remains disabled in practice even if `NRE_HTTP3_ENABLED=true`, because the runtime does not yet create QUIC listeners.

### Runtime Semantics

Behavior after this change:
- HTTPS listeners negotiate HTTP/2 when the client supports it
- Fallback remains HTTP/1.1
- HTTP/3 remains off by default
- Setting `NRE_HTTP3_ENABLED=true` only enables configuration state for future runtime wiring, not actual H3 service

This is intentional to match the requested staged rollout.

## Error Handling

If `NRE_HTTP3_ENABLED` is set to an invalid boolean value, agent startup should fail during config loading with an error naming `NRE_HTTP3_ENABLED`.

No new runtime fallback behavior is needed for HTTPS listeners beyond existing TLS setup.

## Testing

Add or update tests to cover:
- `go-agent/internal/config/config_test.go`
  - default `HTTP3Enabled == false`
  - explicit true value parses successfully
  - invalid value returns an error mentioning `NRE_HTTP3_ENABLED`
- `go-agent/internal/proxy/server_test.go`
  - HTTPS listener TLS config advertises `h2`
  - HTTPS listener TLS config still advertises `http/1.1`
  - HTTPS listener TLS config does not advertise `h3`

## Risks

The only deliberate limitation is that `NRE_HTTP3_ENABLED` will exist before actual HTTP/3 serving exists. The implementation should keep this explicit in naming, tests, and any follow-up notes so operators do not assume QUIC is already active.
