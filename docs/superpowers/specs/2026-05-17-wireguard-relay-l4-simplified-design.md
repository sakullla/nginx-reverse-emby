# WireGuard Relay And L4 Simplified Design

## Goal

Make WireGuard usable as a normal transport and client access feature without requiring users to understand peers, keys, address pools, or profile wiring. Users create clients, Relay listeners, and L4 rules. The control plane creates and maintains WireGuard profiles, generated peers, endpoints, and hidden managed profiles as needed.

This design supersedes the user-facing parts of `2026-05-16-wireguard-profile-clients-design.md` where the old flow still exposed too many raw WireGuard fields.

## Product Model

WireGuard Profile is a network resource on one agent. It is not the primary workflow for ordinary users.

- Human users create WireGuard clients from a Profile page.
- Relay listeners can use WireGuard as one transport mode.
- L4 rules can receive traffic from WireGuard clients and can egress through a WireGuard URI.
- The control plane generates client peers and system peers automatically.
- Raw peer editing is advanced legacy/debug only.

The normal UI should not ask users to hand-author `private_key`, `addresses`, or `peers`.

## Relay Listener Simplification

Relay Listener creation defaults to a one-click path:

- name
- agent
- transport: `tls_tcp`, `quic`, or `wireguard`
- listen port, optional with backend allocation
- public endpoint, optional where relevant

TLS/TCP and QUIC keep the current meaning:

- bind/listen address and port are the local socket.
- public host and port are the address other agents dial.

WireGuard Relay has different address semantics:

- The WireGuard Profile endpoint is the public UDP entry, for example `relay-a.example.com:51820`.
- The Relay listener address is inside the WireGuard network, for example `10.8.0.1:19001`.
- Ordinary users should not type the inner listen host. It defaults to the selected profile server address.
- In WireGuard transport mode, the UI labels the public UDP endpoint as the profile endpoint, not as the Relay listener public address.

When a user creates a WireGuard Relay listener:

1. The backend finds or creates the agent's default enabled WireGuard Profile.
2. It assigns the listener an inner WireGuard listen host from that profile's server address.
3. It allocates a listener port if omitted.
4. It stores enough normalized fields for current agent runtime compatibility.

## Mixed Relay Transport Chains

Relay chains support mixed transports per hop:

```text
A -> Relay A (TLS/TCP) -> Relay B (WireGuard) -> Relay C (QUIC) -> B
```

WireGuard is one Relay transport option, not an all-or-nothing chain mode.

The current cross-agent restriction for WireGuard Relay listeners must be removed. Instead, when an HTTP or L4 rule references a WireGuard Relay listener owned by another agent, the control plane creates system peers between:

- the rule owner's effective WireGuard profile used for dialing that listener, and
- the listener owner's WireGuard profile used by the WireGuard Relay listener.

Snapshots must include the profiles and peers needed by both the dialing agent and listener agent. System peers are read-only and shown as generated connections, not user-editable peers.

## WireGuard Profiles

Profile creation should have an easy default path:

- generated private key.
- default listen host `0.0.0.0`.
- default listen port `51820`, or the next free enabled profile port on that agent.
- allocated address pool, one `/24` per profile by default.
- server address as the first usable address in the pool.
- public endpoint host/port used for generated client configs and WireGuard URI sharing.

The Profile page is still useful, but its ordinary flow is client management:

- create client by name.
- enable/disable/delete client.
- download `.conf`.
- show QR code for `.conf`.
- copy `wireguard://` sharing URI.

The Profile edit modal keeps advanced fields collapsed:

- raw private key.
- address pool/server address.
- DNS/MTU.
- legacy peers.
- generated system connections.

No mihomo YAML export is in scope for this iteration.

## Client Defaults And Sharing

Creating a client requires only a name. The backend generates:

- client private/public key.
- preshared key.
- address from the profile pool.
- enabled peer on the profile.

Client configs default to full tunnel:

```text
AllowedIPs = 0.0.0.0/0, ::/0
```

This makes L4 WireGuard transparent rules work without asking users to edit routes manually.

Supported outputs:

- WireGuard `.conf`.
- QR code whose content is the `.conf`.
- `wireguard://` URI.

Standard `.conf` output does not include `reserved` because it is not a standard WireGuard config field.

## WireGuard URI And Reserved

The project supports its own WireGuard URI dialect because WireGuard does not define an official URI standard.

The URI supports:

```text
wireguard://<private_key>@<endpoint_host>:<endpoint_port>?publickey=<peer_public_key>&psk=<preshared_key>&address=<cidr,cidr>&allowedips=<cidr,cidr>&dns=<ip,ip>&mtu=<mtu>&reserved=<byte,byte,byte>#<name>
```

`reserved` is supported and must be preserved through:

- URI parse preview.
- URI import.
- client sharing URI generation.
- L4 WireGuard URI egress managed profiles.
- profile peer/runtime snapshot model.
- agent WireGuard runtime, when supported by the underlying userspace implementation.

If the runtime cannot apply reserved bytes, activation must fail clearly for profiles that require reserved, rather than silently ignoring it.

## L4 Rule Model

The L4 form is split into entry and exit concepts.

Entry modes:

- TCP/UDP listen.
- SOCKS/HTTP proxy entry, TCP only.
- WireGuard transparent entry.
- WireGuard inner address entry, advanced only.

Exit modes:

- direct backend.
- Relay chain.
- SOCKS/HTTP URI.
- WireGuard URI.

Ordinary users do not select a WireGuard Profile in the L4 form. When WireGuard entry is selected, the system uses the agent's default client-access profile unless an advanced selector overrides it. When WireGuard URI egress is selected, the backend materializes a hidden managed profile owned by the rule.

### WireGuard Transparent Entry

Transparent entry is the default WireGuard L4 mode.

The user flow:

1. User creates or selects a WireGuard client config.
2. Client imports the config; full-tunnel `AllowedIPs` sends traffic into WireGuard.
3. User accesses the intended destination directly, for example `1.1.1.1:443`, `example.com:443`, or `10.10.0.5:3306`.
4. The agent matches L4 WireGuard transparent rules by destination host/IP and port.
5. The matched rule forwards through direct backend, Relay chain, SOCKS/HTTP URI, or WireGuard URI egress.

Transparent entry supports both TCP and UDP.

For UDP, matching uses destination IP/host and port. Relay egress must use the existing UDP relay path, and WireGuard URI egress must be able to dial/send UDP through the selected managed WireGuard runtime.

### WireGuard Inner Address Entry

Inner address entry is advanced.

The user connects to a fixed service address inside the WireGuard profile, for example:

```text
10.8.0.1:9443
```

The L4 rule then forwards that service to its configured exit. This mode is useful for exposing a specific private service but should not be the ordinary L4 WireGuard flow.

## HTTP Rule Model

HTTP rules do not perform transparent WireGuard interception.

HTTP supports an advanced WireGuard inner entry:

- enable WireGuard inner entry.
- select or default a profile.
- listen on profile server address and port, such as `10.8.0.1:8080`.

Requests entering that inner address use the normal HTTP rule behavior: host routing, headers, TLS policy, backend selection, and Relay behavior.

## Backend Responsibilities

The control plane must:

- create default WireGuard profiles for agents when needed.
- allocate profile listen ports and address pools.
- generate client peers and system peers.
- remove or disable generated peers when references disappear.
- remove the cross-agent WireGuard Relay listener prohibition.
- include all referenced WireGuard profiles and generated peers in agent snapshots.
- materialize hidden managed profiles for L4 WireGuard URI egress.
- preserve `reserved` in URI, storage, snapshot, and runtime models.
- reject unsupported reserved runtime activation clearly.

## Frontend Responsibilities

The frontend should simplify ordinary forms:

- Relay listener form hides certificate/trust/raw WireGuard profile fields unless advanced is opened.
- WireGuard transport shows the effective profile endpoint and a "create/reuse default profile" behavior.
- WireGuard Profile page focuses on clients and sharing.
- L4 form shows entry and exit choices instead of exposing profile plumbing.
- L4 WireGuard entry defaults to transparent matching.
- L4 WireGuard inner address mode is advanced.
- L4 WireGuard egress accepts only WireGuard URI in the ordinary UI.

## Testing

Backend tests:

- default profile creation/reuse for WireGuard Relay listeners.
- cross-agent WireGuard Relay listener references generate system peer requirements instead of rejection.
- snapshots include profiles needed by mixed TLS/QUIC/WireGuard Relay chains.
- profile client `.conf`, QR source text, and `wireguard://` URI generation.
- `reserved` parse, storage, redaction, and snapshot preservation.
- L4 WireGuard transparent TCP and UDP normalization.
- L4 WireGuard URI egress creates and cleans hidden managed profiles.

Agent tests:

- mixed Relay chains with TLS/TCP, QUIC, and WireGuard hops.
- L4 WireGuard transparent TCP forwarding.
- L4 WireGuard transparent UDP forwarding.
- L4 WireGuard URI TCP and UDP egress.
- activation fails clearly when reserved is present but unsupported.

Frontend tests:

- simplified Relay listener payloads.
- WireGuard Profile client actions and sharing controls.
- L4 entry/exit payloads for WireGuard transparent TCP and UDP.
- ordinary L4 WireGuard egress sends URI, not profile ID.

Docker E2E:

- `A -> Relay A(TLS) -> Relay B(WireGuard) -> Relay C(QUIC) -> B`.
- `WG client -> A L4 transparent TCP -> Relay chain -> B`.
- `WG client -> A L4 transparent UDP -> Relay chain -> B`.
- `WG client -> A L4 transparent -> WireGuard URI egress`.
