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

For L4 WireGuard inbound:

1. User selects `Listen Mode = WireGuard`.
2. Form selects the current agent's default enabled profile, with an option to choose another profile.
3. `wireguard_listen_host` defaults to the profile server address, such as `10.8.0.1`.
4. User configures only service port and backend targets.
5. A normal client created under the same profile connects with WireGuard and accesses `server_address:listen_port`.

Example:

- Profile `wg-main`: server address `10.8.0.1/24`.
- L4 rule: WireGuard listen port `9443`, backend `127.0.0.1:443`.
- Client `iphone`: address `10.8.0.2/32`.
- User imports config and opens `10.8.0.1:9443`.

For Relay over WireGuard, the rule only references Relay listeners. Any required agent-to-agent peer is generated automatically from that reference.

## API Shape

Profile CRUD remains under agent scope and gains the new profile fields.

Client routes:

- `GET /api/agents/{agentID}/wireguard-profiles/{profileID}/clients`
- `POST /api/agents/{agentID}/wireguard-profiles/{profileID}/clients`
- `PUT /api/agents/{agentID}/wireguard-profiles/{profileID}/clients/{clientID}`
- `DELETE /api/agents/{agentID}/wireguard-profiles/{profileID}/clients/{clientID}`
- `GET /api/agents/{agentID}/wireguard-profiles/{profileID}/clients/{clientID}/config`

The config endpoint returns `text/plain` WireGuard config. QR rendering can be done in the frontend from that config text.

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

## Validation

Backend rejects:

- invalid WireGuard keys.
- invalid address pools and addresses.
- client address outside the profile pool.
- duplicate client addresses within a profile.
- duplicate enabled listen ports on one agent.
- client config download without a public endpoint host.
- public endpoint port outside `1..65535`.

Profile creation can succeed without a public endpoint, but the UI should label the profile as "client config unavailable" until the endpoint is set.

## Migration And Compatibility

Existing profiles with raw peers continue to load. Imported or existing peers are shown in an advanced legacy peers section until they are migrated.

New client-created peers are marked as managed clients. System-generated peers are marked as managed system connections. The runtime snapshot continues to expose the same WireGuard profile shape to the agent, so the agent can remain mostly unchanged.

## Testing

Backend tests:

- default profile endpoint and listen-port allocation.
- client address allocation.
- generated config content.
- config endpoint rejects missing public endpoint.
- snapshot profiles include client and system peers.
- L4 WireGuard inbound defaults to profile server address.

Agent tests:

- existing WireGuard runtime accepts generated peers unchanged.
- L4 WireGuard inbound works with generated client peers in snapshot.

Frontend tests:

- profile payload includes endpoint fields.
- client creation payload is minimal.
- config/QR actions require endpoint.
- L4 WireGuard form defaults to enabled profile and server address.
