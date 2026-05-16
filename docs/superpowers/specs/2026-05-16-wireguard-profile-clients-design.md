# WireGuard Profile Clients Design

## Goal

Make WireGuard usable without asking users to manually author peers. A WireGuard Profile becomes the agent's WireGuard network/interface and owns both human client access and system-generated agent-to-agent peers.

## Product Model

WireGuard Profile is the primary object users manage. It represents one WireGuard network on one agent:

- server identity: private/public key.
- local interface: listen host, listen port, address pool, server address.
- public endpoint: host and port used in generated client configs.
- clients: user-facing peers with downloadable configs and QR codes.
- system connections: read-only peers derived from Relay/L4 references.

The raw `peers` editor is removed from the normal flow. Peers are still stored and sent to the agent, but they are produced by higher-level client/system connection records.

## Profile Fields

Profile keeps existing fields and adds:

- `listen_host`, default `0.0.0.0`.
- `public_endpoint_host`, optional.
- `public_endpoint_port`, default follows `listen_port`.
- `address_pool`, default allocated from `10.8.0.0/16`, one `/24` per profile.
- `server_address`, default first usable address in the pool, such as `10.8.0.1/24`.
- `public_key`, derived from `private_key` and safe to expose.

`listen_port` remains editable. New profiles default to `51820`; if that port is already used by an enabled profile on the same agent, the backend allocates the next available port. Existing duplicate enabled listen-port validation remains.

`public_endpoint_port` is separate from `listen_port` to support NAT and Docker port mapping, for example local `51820` with public `443`.

## Client Access

Each profile can create client entries:

- `id`
- `profile_id`
- `name`
- `private_key`
- `public_key`
- `preshared_key`
- `address`
- `allowed_ips`
- `dns`
- `enabled`
- `created_at`
- `updated_at`

Creating a client automatically:

1. allocates the next free `/32` address from the profile pool.
2. generates client key material and a preshared key.
3. adds an enabled peer to the profile runtime configuration.
4. returns a client record with enough information to download a `.conf` file or render a QR code.

Generated client configs use:

- client private key in `[Interface]`.
- client address and optional DNS in `[Interface]`.
- profile public key in `[Peer]`.
- client preshared key in `[Peer]`.
- `public_endpoint_host:public_endpoint_port` in `[Peer]`.
- allowed IPs defaulting to the profile server address, such as `10.8.0.1/32`.

If `public_endpoint_host` is empty, the profile can be saved but client config download is blocked with a clear validation error.

## System Connections

System peers are generated from references:

- Relay listener with `transport_mode=wireguard` and a profile.
- HTTP/L4 rules that use Relay layers pointing at WireGuard relay listeners.
- L4 rules with `listen_mode=wireguard` or `proxy_egress_mode=wireguard`.

Users do not edit system peers directly. The UI shows them as a read-only "System Connections" section with source object, target agent, assigned address, and status.

The control plane assembles system peers before snapshot generation. Agent snapshots receive only the effective profile runtime: profile identity plus generated client and system peers.

## L4 User Flow

For L4 WireGuard inbound, WireGuard is a transparent traffic entry, not a service bound to `server_address:listen_port`.

1. User selects `Listen Mode = WireGuard`.
2. Form selects the current agent's default enabled profile, with an option to choose another profile.
3. User configures traffic matching fields: protocol, destination port, and optionally destination CIDR/domain in later iterations.
4. User configures the target: direct backends, Relay layers, SOCKS/HTTP egress, WireGuard URI egress, or WireGuard Profile egress.
5. A normal client created under the same profile connects with WireGuard and sends traffic normally. The agent captures that WireGuard client traffic and routes matching flows through the L4 rule.

Example:

- Profile `wg-main`: client `iphone` gets address `10.8.0.2/32`.
- L4 rule: WireGuard inbound, match TCP destination port `443`, egress through Relay listener `tokyo-relay`.
- User imports the WireGuard config and opens `https://example.com`.
- The agent receives the client's TCP flow from the WireGuard runtime, matches destination port `443`, then forwards through the configured Relay path.

This is similar to a TUN inbound plus route rule in proxy tools: WireGuard brings traffic into the agent, L4 rules decide where that traffic goes. Users do not need to access a special `10.8.0.1:9443` address unless an explicit rule chooses to expose that address.

For Relay over WireGuard, the rule only references Relay listeners. Any required agent-to-agent peer is generated automatically from that reference.

## WireGuard URI Egress

L4 WireGuard egress can be configured directly from a URI, without requiring the user to create a reusable profile first. This mirrors SOCKS/HTTP URI entry and is the fastest path for one-off outbound WireGuard connections.

The project supports its own WireGuard URI dialect because WireGuard does not define an official URI sharing standard. The parser accepts:

```text
wireguard://<private_key>@<endpoint_host>:<endpoint_port>?publickey=<peer_public_key>&psk=<preshared_key>&address=<cidr,cidr>&allowedips=<cidr,cidr>&dns=<ip,ip>&mtu=<mtu>&reserved=<byte,byte,byte>#<name>
```

Required fields:

- private key in the URI userinfo.
- endpoint host and port.
- peer public key as `publickey`.
- at least one local address as `address`.

Optional fields:

- `psk` for preshared key.
- `allowedips`, defaulting to `0.0.0.0/0,::/0` for outbound egress.
- `dns`.
- `mtu`.
- `reserved`, parsed as one to three bytes for WireGuard implementations that need reserved bytes.
- URI fragment as display name.

L4 egress UI offers:

- `WireGuard URI`: paste a URI directly on the rule.
- `WireGuard Profile`: select a reusable profile.
- "Save as Profile" action after parsing a URI.

Internally, direct URI egress should be materialized as a managed hidden profile owned by the rule, for example `l4-rule-12-wireguard-egress`. This keeps the agent runtime path unchanged: snapshots still contain WireGuard profiles and L4 rules still reference `wireguard_profile_id`.

When a rule switches away from direct URI egress, the managed profile is deleted or disabled if no longer referenced. If multiple rules should share the same URI, users should save it as a normal Profile and select that Profile from each rule.

## API Shape

Profile CRUD remains under agent scope and gains the new profile fields.

Client routes:

- `GET /api/agents/{agentID}/wireguard-profiles/{profileID}/clients`
- `POST /api/agents/{agentID}/wireguard-profiles/{profileID}/clients`
- `PUT /api/agents/{agentID}/wireguard-profiles/{profileID}/clients/{clientID}`
- `DELETE /api/agents/{agentID}/wireguard-profiles/{profileID}/clients/{clientID}`
- `GET /api/agents/{agentID}/wireguard-profiles/{profileID}/clients/{clientID}/config`

The config endpoint returns `text/plain` WireGuard config. QR rendering can be done in the frontend from that config text.

URI helper routes:

- `POST /api/wireguard/parse-uri` validates a URI and returns a redacted preview.
- `POST /api/agents/{agentID}/wireguard-profiles/import-uri` creates a reusable outbound Profile from a URI.

L4 rule create/update accepts direct WireGuard egress URI when `proxy_egress_mode=wireguard`. The backend parses the URI, creates or updates the managed hidden profile, and stores the resulting `wireguard_profile_id` on the rule.

## UI

WireGuard Profile page shows:

- profile summary cards: agent, server address, listen endpoint, public endpoint, enabled state.
- client table: name, address, enabled, actions.
- client actions: create, disable, delete, reset key, download config, show QR.
- system connections table: source object, target agent, assigned address, generated peer state.
- advanced section for raw runtime fields, collapsed by default.

The create/edit profile modal exposes:

- name.
- enabled.
- listen host.
- listen port.
- public endpoint host.
- public endpoint port.
- address pool.
- server address.
- MTU/DNS as advanced fields.

It does not expose raw peer editing in the default view.

L4 rule form adds a WireGuard egress source selector:

- `Profile`: existing behavior, choose a reusable profile.
- `URI`: paste `wireguard://...`, preview parsed endpoint/name/address, and optionally save as Profile.

Secrets in parsed URI previews are redacted after validation.

## Validation

Backend rejects:

- invalid WireGuard keys.
- invalid address pools and addresses.
- client address outside the profile pool.
- duplicate client addresses within a profile.
- duplicate enabled listen ports on one agent.
- client config download without a public endpoint host.
- public endpoint port outside `1..65535`.
- invalid WireGuard URI schemes or missing URI fields.
- invalid URI reserved bytes outside `0..255`.
- direct URI egress on non-TCP proxy-entry modes that cannot use WireGuard egress.

Profile creation can succeed without a public endpoint, but the UI should label the profile as "client config unavailable" until the endpoint is set.

## Migration And Compatibility

Existing profiles with raw peers continue to load. Imported or existing peers are shown in an advanced legacy peers section until they are migrated.

New client-created peers are marked as managed clients. System-generated peers are marked as managed system connections. The runtime snapshot continues to expose the same WireGuard profile shape to the agent, so the agent can remain mostly unchanged.

Managed profiles created from direct L4 URI egress are hidden from the main Profile list by default, but can be shown in an advanced "managed profiles" filter for debugging.

## Testing

Backend tests:

- default profile endpoint and listen-port allocation.
- client address allocation.
- generated config content.
- config endpoint rejects missing public endpoint.
- WireGuard URI parser accepts full URI and redacts secrets in previews.
- L4 direct WireGuard URI egress creates and updates a managed profile.
- switching L4 egress away from direct URI cleans up the managed profile.
- snapshot profiles include client and system peers.
- L4 WireGuard inbound stores transparent traffic matching fields and does not require `wireguard_listen_host`.

Agent tests:

- existing WireGuard runtime accepts generated peers unchanged.
- L4 WireGuard inbound captures generated client traffic and forwards matching flows through L4 routing.
- L4 WireGuard egress works with profiles generated from URI imports.

Frontend tests:

- profile payload includes endpoint fields.
- client creation payload is minimal.
- config/QR actions require endpoint.
- L4 WireGuard form defaults to enabled profile and shows traffic matching fields instead of special address fields.
- L4 WireGuard egress URI payloads are generated and previews redact secrets.
