# WireGuard Relay And L4 Design

## Goal

Add first-class WireGuard support to the pure-Go runtime so Relay listeners can use WireGuard as a transport and L4 rules can use WireGuard as both an inbound listener mode and an outbound egress mode.

## Scope

The first implementation supports `generic_wireguard` profiles: standard WireGuard key, peer, endpoint, allowed IP, and virtual address configuration managed by the control plane and applied by the Go agent. Cloudflare WARP is supported only when the user can provide a standard WireGuard profile, or by routing through an external Cloudflare WARP client outside this feature. The agent will not implement Cloudflare device registration, policy sync, MASQUE, or WARP key rotation.

## Architecture

Introduce reusable WireGuard profiles owned by an agent. A profile contains the local private key, virtual addresses, listen port, peers, DNS fields for UI/documentation, and a revision. Backend APIs redact private keys on read and require re-entry when an edited value is redacted.

The Go agent gains an internal WireGuard runtime built on `golang.zx2c4.com/wireguard/tun/netstack`. The runtime caches active netstack devices by profile fingerprint, exposes TCP/UDP dial and listen helpers, and shuts devices down when rules/listeners no longer reference them.

Relay `transport_mode=wireguard` uses the WireGuard runtime to reach the first hop and then runs the existing Relay mux protocol over that connection. L4 `listen_mode=wireguard` exposes a TCP/UDP service inside the selected WireGuard profile; clients connect to the profile endpoint using standard WireGuard and then access the configured virtual service address/port. L4 `proxy_egress_mode=wireguard` dials the requested target through the selected profile, analogous to current `proxy_egress_mode=proxy`.

## Data Model

Add `WireGuardProfile` to backend service/storage and agent model:

- `id`
- `agent_id`
- `name`
- `mode` with first value `generic_wireguard`
- `private_key`
- `listen_port`
- `addresses`
- `peers`
- `dns`
- `mtu`
- `enabled`
- `tags`
- `revision`

Each peer contains:

- `name`
- `public_key`
- `preshared_key`
- `endpoint`
- `allowed_ips`
- `persistent_keepalive_seconds`

Relay listener adds:

- `wireguard_profile_id`

L4 rule adds:

- `wireguard_profile_id`
- `wireguard_listen_host`

For L4 inbound WireGuard mode, `listen_host/listen_port` remain the service address and port inside the WireGuard netstack. `wireguard_listen_host` is optional and defaults to `listen_host`. The profile's own `listen_port` is the public UDP WireGuard endpoint.

## API And UI

Add API routes under agent scope:

- `GET /api/agents/{agentID}/wireguard-profiles`
- `POST /api/agents/{agentID}/wireguard-profiles`
- `PUT /api/agents/{agentID}/wireguard-profiles/{id}`
- `DELETE /api/agents/{agentID}/wireguard-profiles/{id}`

The panel gets a WireGuard profile management page. Relay Listener form adds `WireGuard` to Relay Transport and shows a profile selector when selected. L4 form adds `WireGuard` to inbound listen mode and outbound egress mode and shows a profile selector.

## Runtime Behavior

Relay listener startup:

1. `tls_tcp` and `quic` keep current behavior.
2. `wireguard` validates `wireguard_profile_id`.
3. The agent ensures the selected profile runtime is active.
4. The Relay server listens inside the WireGuard netstack at `listen_host:listen_port`.

Relay dial:

1. If first hop `transport_mode=wireguard`, the dialer resolves that hop's `wireguard_profile_id`.
2. It dials the hop address through the profile runtime.
3. Existing Relay OPEN/mux framing is reused.

L4 inbound:

1. `listen_mode=tcp` and `listen_mode=proxy` keep current behavior.
2. `listen_mode=wireguard` starts a listener inside the selected WireGuard runtime.
3. TCP and UDP rules both use existing backend selection and copy paths after accepting the netstack-side connection/packet stream.

L4 outbound:

1. `proxy_egress_mode=proxy` keeps SOCKS/HTTP behavior.
2. `proxy_egress_mode=wireguard` dials the target through the selected WireGuard profile.
3. Relay egress remains selected with `proxy_egress_mode=relay`.

## Validation

Backend rejects:

- WireGuard profile without private key, address, or at least one peer.
- invalid WireGuard key strings.
- invalid CIDR values in addresses and allowed IPs.
- invalid endpoint host:port values.
- Relay `transport_mode=wireguard` without `wireguard_profile_id`.
- L4 `listen_mode=wireguard` or `proxy_egress_mode=wireguard` without `wireguard_profile_id`.
- using `listen_mode=wireguard` with disabled profile.

Secrets are redacted on read:

- `private_key`
- `preshared_key`

## Testing

Backend tests cover normalization, redaction, CRUD, snapshot inclusion, and validation references. Agent tests cover WireGuard config conversion, runtime lifecycle with a test double where possible, Relay transport selection, and L4 inbound/outbound path selection. Frontend tests cover payload generation and secret redaction handling.

Full live WireGuard data-plane integration may be gated behind focused tests because userspace WireGuard netstack setup can be sensitive to CI networking. The minimal required verification is backend Go tests, agent package tests for relay/l4/wireguard, and frontend build.

