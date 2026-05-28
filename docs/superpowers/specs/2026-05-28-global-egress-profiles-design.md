# Global Egress Profiles Design

## Purpose

Add a global "egress profiles" infrastructure feature that HTTP rules and L4 rules can reference when traffic leaves the system. The profile describes how the actual egress node reaches the real backend or target.

This replaces the not-yet-released per-rule `proxy_egress_url` / relay `FinalHopProxyURL` direction. Relay metadata must not carry proxy URLs, proxy passwords, WireGuard private keys, or other egress secrets.

## Design Principles

- Egress profiles are global infrastructure resources, similar to relay listeners.
- HTTP and L4 rules may reference an egress profile by ID.
- If a rule does not reference an egress profile, egress defaults to direct.
- The actual egress node executes the profile locally.
- `outbound_proxy_url` remains separate. It is only for an agent's own outbound control-plane or relay-hop connectivity, not business traffic egress.
- Secrets are included only in snapshots for agents that may actually execute the profile.

This follows the same separation used by systems such as Cilium egress gateway and Kubernetes node-local datapaths: control-plane policy selects behavior, while the final node performs the local egress action.

## Resource Model

Add a global `egress_profiles` resource.

Fields:

- `id`
- `name`
- `type`: `direct`, `socks`, `http`, or `wireguard`
- `proxy_url`: used by `socks` and `http`
- `wireguard_config`: used by `wireguard`
- `enabled`
- `description`
- `revision`

Validation:

- `direct` must not require `proxy_url` or WireGuard settings.
- `socks` requires a SOCKS-family URL: `socks://`, `socks5://`, or `socks5h://`.
- `http` requires `http://` or `https://`.
- `wireguard` owns its egress tunnel configuration as part of the egress profile model. It must not depend on a node-owned WireGuard profile, because egress profiles are global.
- Disabled profiles cannot be selected by rules.
- Proxy URLs with credentials are stored securely in the existing database model and redacted in panel API responses.

## Rule Model

HTTP rules gain:

- `egress_profile_id`

L4 rules gain:

- `egress_profile_id`

No egress profile selected means direct egress. This is a first-class default, not an implicit reference to a default profile.

Remove the not-yet-released per-rule proxy egress URL path:

- Remove `proxy_egress_url` as the long-term rule field.
- Remove relay `FinalHopProxyURL`.
- Do not support migration for the discarded fields because the feature has not shipped.

## Execution Model

The actual egress node is determined by the rule topology.

HTTP without relay:

```text
HTTP listener agent -> egress profile -> backend URL
```

HTTP with relay:

```text
HTTP listener agent -> relay path -> final hop agent -> egress profile -> backend URL
```

L4 without relay:

```text
L4 listener agent -> egress profile -> target host:port
```

L4 with relay:

```text
L4 listener agent -> relay path -> final hop agent -> egress profile -> target host:port
```

Relay metadata may include:

- target address
- `egress_profile_id`
- traffic class or other non-secret routing metadata

Relay metadata must not include:

- proxy URL
- proxy username/password
- WireGuard private key
- resolved profile body

Final hop handling:

1. Receive the relay open request with target and optional `egress_profile_id`.
2. If no profile ID is present, dial target directly.
3. Resolve the profile ID from the local snapshot.
4. Apply the profile locally to reach the target.
5. Return a clear error if the profile is missing, disabled, unsupported for the protocol, or malformed.

## Snapshot Scoping

Because egress profiles are global but may contain secrets, snapshots must include only the profiles an agent may execute.

An agent may execute a profile when:

- It owns an HTTP or L4 rule that references the profile and does not use relay.
- It is a possible final hop for an HTTP or L4 rule that references the profile through `relay_layers`.

For relay rules with multiple possible paths, every possible final hop agent receives the referenced profile. Agents that are not possible egress executors do not receive the profile secret.

The control plane should still expose redacted profile data to the panel UI, but sync snapshots for executing agents receive full secret material.

## Protocol Support

Supported:

- HTTP rule with `direct`
- HTTP rule with SOCKS proxy
- HTTP rule with HTTP/HTTPS proxy
- HTTP rule with WireGuard egress
- L4 TCP with `direct`
- L4 TCP with SOCKS proxy
- L4 TCP with HTTP/HTTPS proxy via CONNECT
- L4 TCP with WireGuard egress
- L4 UDP with `direct`
- L4 UDP with SOCKS5 UDP associate
- L4 UDP with WireGuard egress

Unsupported:

- L4 UDP with HTTP/HTTPS proxy
- UDP through SOCKS variants that do not support UDP associate

Unsupported combinations should be rejected at save time when enough information is available. Runtime must still return clear errors because snapshots can be stale or manually modified.

## Frontend

Add "Egress Profiles" under the infrastructure menu.

List view:

- name
- type
- enabled state
- description
- revision or updated time if available

Create/edit form:

- name
- type selector
- proxy URL for `socks` and `http`
- WireGuard egress settings for `wireguard`
- enabled toggle
- description

HTTP rule form:

- Add an optional egress profile selector.
- Empty selection means direct.

L4 rule form:

- Add an optional egress profile selector.
- Empty selection means direct.
- Prevent selecting HTTP proxy for UDP rules.

`outbound_proxy_url` remains where it is today and should be labeled as agent outbound connectivity, not business egress.

## Backend API

Add panel APIs for egress profiles:

- list profiles
- get profile
- create profile
- update profile
- delete profile

Deletion rules:

- Reject deleting a profile referenced by an HTTP or L4 rule.
- Allow deletion after references are removed.

Rule APIs must validate `egress_profile_id`:

- Empty is valid and means direct.
- Non-empty must reference an enabled profile.
- Protocol/profile incompatibilities are rejected.

Public sync APIs must include scoped egress profile data for each agent snapshot.

## Agent Runtime

Add an agent model type for scoped egress profiles.

HTTP runtime:

- Build transports using direct, SOCKS, HTTP proxy, or WireGuard egress based on the selected profile.
- For relay HTTP traffic, pass only the `egress_profile_id` to relay dial options.
- Final hop resolves the profile from local runtime state.

L4 runtime:

- Direct local egress remains the default.
- Local TCP/UDP egress uses the selected profile.
- Relay TCP/UDP passes only `egress_profile_id` to the relay open request.
- Final hop resolves and applies the profile locally.

Relay runtime:

- Replace `FinalHopProxyURL` with an `EgressProfileID` option.
- Encode only the profile ID in relay metadata.
- Final hop selector uses a local profile resolver.

## Error Handling

Configuration errors:

- Unknown profile ID.
- Disabled profile selected.
- Profile type not supported for the rule protocol.
- Invalid proxy URL.
- Missing WireGuard configuration.
- Referenced profile deleted while a rule still exists.

Runtime errors:

- Profile not present in local snapshot.
- Proxy connection failure.
- HTTP CONNECT failure.
- SOCKS negotiation failure.
- WireGuard tunnel unavailable.

Errors should include rule/profile identity but must not include secrets.

## Testing

Backend:

- CRUD validation for egress profiles.
- Redaction of proxy credentials in panel responses.
- Full secrets included only in scoped sync snapshots.
- HTTP and L4 rule validation for missing, disabled, and incompatible profiles.
- Deletion is rejected when profiles are referenced.

Agent:

- HTTP without profile defaults to direct.
- L4 without profile defaults to direct.
- HTTP rule can use SOCKS and HTTP proxy profiles.
- L4 TCP can use SOCKS and HTTP proxy profiles.
- L4 UDP rejects HTTP proxy profiles.
- Relay metadata contains `egress_profile_id` and no proxy URL or secret.
- Final hop resolves profile from local snapshot.
- Missing final-hop profile returns a clear runtime error.

Frontend:

- Egress profile list/create/edit behavior.
- Credential redaction and re-entry behavior.
- HTTP rule selector payload.
- L4 rule selector payload and UDP/HTTP-proxy guard.

## Out of Scope

- Migrating the discarded `proxy_egress_url` implementation.
- Deleting `outbound_proxy_url`.
- Per-node egress profile ownership.
- Dynamic gateway selection policies beyond existing relay path selection.
