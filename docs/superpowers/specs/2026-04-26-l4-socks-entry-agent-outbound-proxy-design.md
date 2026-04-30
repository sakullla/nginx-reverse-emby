# L4 Proxy Entry And Agent Outbound Proxy Design

## Goal

Add two independent proxy features:

1. L4 rules can expose a proxy entry. Clients connect to the agent using standard SOCKS or HTTP CONNECT, and the agent forwards the requested TCP target through either a Relay chain or a configured upstream SOCKS/HTTP proxy.
2. Agents can define a node-level outbound proxy for their own TCP egress. Relay `tls_tcp` hop dialing can use this proxy when that agent initiates the connection.

These features must not change the existing Relay transport model. Relay remains `tls_tcp` or `quic`; this design only adds proxy behavior around TCP dialing paths.

## Scope

In scope:

- Add an explicit L4 proxy entry mode for TCP rules.
- Support SOCKS4, SOCKS4a, SOCKS5, and HTTP CONNECT on the L4 proxy entry.
- Support SOCKS5 username/password authentication and HTTP Basic authentication on the L4 proxy entry.
- Let a proxy-entry L4 rule choose either Relay egress or standard proxy egress.
- Preserve domain targets until the selected egress endpoint can resolve them.
- Add agent-level outbound proxy configuration for agent-initiated TCP egress.
- Support proxy URL formats commonly used by Git, curl, and Linux tools.
- Apply agent outbound proxy to Relay `tls_tcp` hop dialing.

Out of scope for the first implementation:

- QUIC through an outbound proxy.
- SOCKS UDP associate.
- Plain HTTP forward-proxy request rewriting.
- Mixed auto-detection between raw TCP and SOCKS on the same L4 listener.
- Direct no-Relay proxy entry egress unless it goes through an explicit upstream SOCKS/HTTP proxy.

## Terminology

`Proxy entry` means an L4 listener that accepts client SOCKS or HTTP CONNECT requests. It is a server-side feature on the agent that owns the L4 rule.

`Proxy egress` means an upstream proxy that the agent dials after parsing a proxy entry request. The upstream proxy can be SOCKS or HTTP CONNECT.

`Agent outbound proxy` means node-level egress policy used when that agent initiates TCP connections, such as dialing the next Relay listener in a `tls_tcp` Relay chain.

These are separate settings. Proxy entry credentials are client-facing credentials. Proxy egress and agent outbound proxy credentials are upstream credentials. They are not shared.

## L4 Proxy Entry

L4 rules gain an explicit listener mode. A representative shape is:

```json
{
  "protocol": "tcp",
  "listen_mode": "proxy",
  "proxy_entry_auth": {
    "enabled": true,
    "username": "client",
    "password": "secret"
  },
  "proxy_egress_mode": "relay"
}
```

`listen_mode` defaults to `tcp` for existing rules. `listen_mode=proxy` is valid only with `protocol=tcp`.

When a client connects to a proxy entry listener, the agent parses the client request and extracts the requested target host and port. SOCKS4, SOCKS4a, SOCKS5 CONNECT, and HTTP CONNECT are supported. SOCKS5 username/password auth and HTTP Basic auth are supported when configured. SOCKS4 user ID can be accepted but is not treated as a secure password mechanism.

HTTP entry support is limited to CONNECT. Plain HTTP forward-proxy requests such as `GET http://example.com/ HTTP/1.1` are rejected in the first implementation.

The requested target becomes the outbound target. If the target is a domain name, the entry agent does not resolve it just to normalize the request. Domain resolution is deferred to the selected egress:

- Relay egress preserves the domain target through the Relay chain so the final hop resolves and connects it.
- SOCKS/HTTP proxy egress sends the domain target to the upstream proxy when the selected proxy protocol supports remote resolution.

## L4 Proxy Egress

A proxy entry rule must choose one egress mode.

Relay egress:

```json
{
  "proxy_egress_mode": "relay",
  "relay_layers": [[101], [202]]
}
```

Relay egress uses the existing Relay planning and dialing path. The parsed client-request target is passed as the Relay target. The current first-release requirement is that proxy entry over Relay must have at least one Relay listener in `relay_chain` or `relay_layers`.

Proxy egress:

```json
{
  "proxy_egress_mode": "proxy",
  "proxy_egress_url": "socks://user:pass@127.0.0.1:1080"
}
```

Proxy egress dials a configured upstream SOCKS/HTTP proxy and requests a TCP tunnel to the client-specified target. It does not require Relay. It is still explicit configuration, not a fallback to direct dialing.

Supported proxy URL schemes for egress:

- `socks://user:pass@host:port`
- `socks4://user@host:port`
- `socks4a://user@host:port`
- `socks5://user:pass@host:port`
- `socks5h://user:pass@host:port`
- `http://user:pass@host:port`

`socks://` is a convenience alias for a standard SOCKS client path. The implementation should treat it as SOCKS5-compatible by default while keeping the URL parser structured so explicit SOCKS4, SOCKS4a, SOCKS5, and SOCKS5h behavior can be selected by scheme.

HTTP proxy egress uses `CONNECT host:port HTTP/1.1`. Basic authentication is supported when credentials are present in the URL.

## Agent Outbound Proxy

Agents gain node-level outbound proxy configuration. A representative shape is:

```json
{
  "outbound_proxy_url": "http://user:pass@proxy.example:8080"
}
```

The control plane stores this setting on the agent record or an agent settings record, not on Relay listeners. This matches ownership: the node initiating the outbound connection owns how its egress leaves the machine or network.

The first implementation applies `outbound_proxy_url` to Relay `tls_tcp` hop dialing. For a chain:

```text
client-agent -> Relay A -> Relay B -> target
```

the effective behavior is:

```text
client-agent uses client-agent outbound_proxy_url when dialing Relay A
Relay A uses Relay A outbound_proxy_url when dialing Relay B
Relay B uses Relay B outbound_proxy_url only for egress paths explicitly added later
```

If an outbound proxy is configured and a Relay hop would use `quic`, the proxy is not applied. First implementation behavior should force Relay proxy dialing to `tls_tcp` for that outbound connection or reject incompatible configuration during validation. The preferred behavior is validation that prevents ambiguous `quic` plus TCP-only outbound proxy combinations.

The supported URL schemes match L4 proxy egress:

- `socks://user:pass@host:port`
- `socks4://user@host:port`
- `socks4a://user@host:port`
- `socks5://user:pass@host:port`
- `socks5h://user:pass@host:port`
- `http://user:pass@host:port`

## Data Flow

L4 proxy entry with Relay egress:

```text
client SOCKS4/4a/5 or HTTP CONNECT
  -> L4 proxy entry on agent
  -> parse target
  -> Relay tls_tcp chain
  -> final Relay hop resolves/connects target
  -> bidirectional TCP copy
```

L4 proxy entry with proxy egress:

```text
client SOCKS4/4a/5 or HTTP CONNECT
  -> L4 proxy entry on agent
  -> parse target
  -> upstream SOCKS/HTTP proxy CONNECT
  -> target
  -> bidirectional TCP copy
```

Relay hop with agent outbound proxy:

```text
current agent
  -> node-level outbound proxy CONNECT
  -> next Relay listener public endpoint
  -> existing Relay tls_tcp handshake and mux
```

## Validation

Control-plane validation:

- `listen_mode=proxy` requires `protocol=tcp`.
- `listen_mode=proxy` requires `proxy_egress_mode=relay` or `proxy_egress_mode=proxy`.
- Relay egress requires non-empty `relay_chain` or `relay_layers`.
- Proxy egress requires a valid `proxy_egress_url`.
- Agent `outbound_proxy_url`, when present, must parse as a supported proxy URL.
- Proxy URLs must include a host and port.

Agent runtime validation:

- Reject unsupported SOCKS commands other than CONNECT.
- Reject HTTP methods other than CONNECT on proxy entry listeners.
- Reject proxy-entry requests missing a valid target host and port.
- Reject proxy URL schemes not supported by the runtime.
- Reject or bypass outbound proxy for non-`tls_tcp` Relay transport according to the final validation policy.

## Security

Credentials can appear in proxy URLs and proxy entry auth settings. They must be treated as secrets:

- API responses should redact passwords where possible.
- Logs must not print full proxy URLs with passwords.
- Diagnostics should display proxy type and host but redact credentials.
- Backup/export behavior should follow existing sensitive configuration rules. If exports include these fields, documentation must warn that proxy credentials are included.

Proxy entry authentication should be optional but available. Deployments exposing a proxy entry on non-loopback addresses should be encouraged to enable authentication.

## Testing

Backend tests:

- L4 rule normalization and validation for `listen_mode=proxy`.
- Proxy URL parsing and credential handling.
- Agent settings persistence for `outbound_proxy_url`.
- Snapshot compatibility for older agents/rules without new fields.

Agent tests:

- SOCKS4 CONNECT parses IPv4 targets.
- SOCKS4a CONNECT preserves domain targets.
- SOCKS5 no-auth CONNECT parses IPv4, IPv6, and domain targets.
- SOCKS5 username/password auth accepts valid credentials and rejects invalid credentials.
- HTTP CONNECT parses authority targets and supports Basic auth.
- HTTP CONNECT proxy egress sends the expected CONNECT request and auth header.
- SOCKS proxy egress dials the requested target through the upstream proxy.
- Relay `tls_tcp` dialing uses node-level outbound proxy when configured.
- Relay chain preserves domain target until final hop.

Frontend tests:

- L4 form exposes proxy entry mode and egress mode controls.
- Proxy URL and auth fields are included in payloads.
- Existing TCP and UDP rules keep their current default behavior.

## Compatibility

Existing L4 rules default to `listen_mode=tcp` and behave unchanged.

Existing Relay listeners keep their current `transport_mode` behavior. No Relay listener field is added for proxy egress.

Agents that do not understand the new L4 SOCKS fields should not receive SOCKS-entry rules unless version/capability gating confirms support.
