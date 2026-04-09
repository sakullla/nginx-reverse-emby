# Relay Listener Bind/Public Endpoint Design

Date: 2026-04-09

## Goal

Support relay listeners whose local bind address differs from the address other agents use to reach them.

This design separates:

- local bind addresses used by the relay process on the host
- public endpoint used by HTTP and L4 relay chains to dial that listener

The change must remain backward-compatible with existing `listen_host`/`listen_port` data and existing clients.

## Scope

In scope:

- relay listener data model changes
- backend normalization and storage compatibility
- Go agent runtime and relay-hop dialing changes
- frontend relay listener form and display changes
- sync payload compatibility between panel and agent

Out of scope:

- changing the previously agreed HTTP/L4 multi-backend retry, DNS cache, or failure-cache behavior
- adding new relay discovery mechanisms
- changing relay TLS trust semantics beyond selecting the correct dial target and server name

## Chosen Approach

Use a compatibility boundary approach:

- internal model standardizes on `bind_hosts + listen_port + public_host + public_port`
- read APIs always return the expanded new structure
- `listen_host` remains as a compatibility mirror of `bind_hosts[0]`
- old payloads using only `listen_host/listen_port` continue to work

This avoids keeping two first-class listener models alive across the codebase while preserving compatibility at storage and API boundaries.

## Data Model

Normalized relay listener fields:

- `bind_hosts: string[]`
- `listen_port: number`
- `public_host: string`
- `public_port: number`
- compatibility mirror: `listen_host = bind_hosts[0]`

Existing fields remain:

- `id`
- `agent_id`
- `name`
- `enabled`
- `certificate_id`
- `tls_mode`
- `pin_set`
- `trusted_ca_certificate_ids`
- `allow_self_signed`
- `tags`
- `revision`

## Compatibility Rules

### Write Compatibility

Incoming payloads are normalized as follows:

1. Legacy payload with only `listen_host/listen_port`
   - `bind_hosts = [listen_host]`
   - `public_host = listen_host`
   - `public_port = listen_port`

2. New payload with `bind_hosts + listen_port`, but no `public_*`
   - `public_host = bind_hosts[0]`
   - `public_port = listen_port`

3. New payload with `public_host` but no `public_port`
   - `public_port = listen_port`

4. Empty or duplicate bind hosts
   - trim, deduplicate, drop empty entries
   - reject if no bind hosts remain

### Read Compatibility

Relay listener reads from CRUD APIs and sync payloads always return:

- `bind_hosts`
- `listen_port`
- `public_host`
- `public_port`
- `listen_host` as compatibility mirror of `bind_hosts[0]`

The returned `public_host/public_port` values are always the effective values after defaulting.

## Runtime Behavior

### Relay Server Binding

For each relay listener:

- the agent starts one TCP listener per `bind_hosts[i]:listen_port`
- one logical relay listener may therefore own multiple `net.Listener` instances
- `listen_host` is not used internally except as a compatibility mirror

### Relay Hop Dialing

HTTP relay chains and L4 relay chains use:

- dial target: `public_host:public_port`
- TLS verification server name: `public_host`

They do not use `listen_host:listen_port` for cross-agent dialing once the new model is available.

If `public_host/public_port` were omitted by the user, the normalized defaults make runtime behavior equivalent to the old model.

### Auto-Issued Relay Listener Certificates

When the panel auto-creates a relay listener certificate, the certificate identity is derived from the listener’s external identity:

- prefer `public_host`
- fallback to `bind_hosts[0]` only when `public_host` is not explicitly provided and normalization would make them equal

This ensures the generated certificate matches the address peers actually use.

## Conflict Rules

Listener conflicts are checked against actual bound sockets:

- for each listener, expand all `bind_host:listen_port`
- if any expanded bind tuple overlaps another relay listener on the same agent, reject the change

`public_host/public_port` do not participate in bind conflict detection because they are dial targets, not local sockets.

## Frontend Design

### Form Inputs

The relay listener form exposes:

- `bind_hosts`
- `listen_port`
- `public_endpoint`

`public_endpoint` is a single aggregated input that accepts:

- empty string
- `host`
- `host:port`

The frontend parses `public_endpoint` into request payload fields:

- empty: omit `public_host/public_port`
- `host`: send `public_host`, omit `public_port`
- `host:port`: send both

The backend remains the source of truth for defaulting behavior.

### Edit Backfill

When editing existing data:

- if effective public endpoint equals the default derived from `bind_hosts[0]` and `listen_port`, the form shows an empty `public_endpoint`
- if only host differs, show `host`
- if port also differs, show `host:port`

### Display

Relay listener list and relay-chain selection display:

- primary address: `public_host:public_port`
- secondary detail: aggregated `bind_hosts`

This reduces operator confusion in NAT or private-IP deployments.

## Backend and Storage Changes

### Normalization

Backend normalization must:

- accept old and new payloads
- persist normalized new fields
- emit expanded new fields on read
- preserve `listen_host` as a mirror field for compatibility

### Storage

JSON, Prisma, and SQLite storage paths must:

- round-trip the new fields
- read old stored entries that only contain `listen_host`
- normalize them on load or save without requiring a manual migration step for correctness

## Sync Contract

Heartbeat responses and relay listener API reads return the expanded new structure.

This guarantees:

- old agents that only consume `listen_host` still have a usable mirror field
- updated agents can consume `bind_hosts/public_*` directly

## Error Handling

Reject invalid configurations for:

- empty normalized `bind_hosts`
- invalid bind host values
- invalid `listen_port`
- invalid explicit `public_host`
- invalid explicit `public_port`
- bind conflicts across expanded `bind_host:listen_port` tuples

Runtime does not silently fall back from `public_host/public_port` to `listen_host` when an explicit public endpoint is configured but unreachable. That remains a configuration error.

## Testing

Minimum coverage:

- relay listener normalization for old and new payloads
- read-path expansion of `bind_hosts/public_*`
- storage round-trip in JSON and Prisma-backed paths
- sync payload decode compatibility in Go
- relay runtime starts one listener per bind host
- HTTP and L4 relay-hop dialing use `public_host:public_port`
- auto-issued relay listener certificates prefer `public_host`
- frontend parsing/backfill of `public_endpoint`

Minimum verification commands after implementation:

- `cd panel/backend && npm test`
- `cd panel/backend && node --check server.js`
- `cd panel/frontend && npm run build`
- `cd go-agent && go test ./...`

## Implementation Notes

Files expected to change:

- `panel/backend/relay-listener-normalize.js`
- `panel/backend/server.js`
- `panel/backend/storage-json.js`
- `panel/backend/storage-sqlite.js`
- `panel/backend/storage-prisma-core.js`
- `panel/backend/prisma/schema.prisma`
- relay listener backend tests
- `go-agent/internal/model/relay.go`
- `go-agent/internal/relay/runtime.go`
- `go-agent/internal/relay/validation.go`
- `go-agent/internal/proxy/server.go`
- `go-agent/internal/l4/server.go`
- sync/model tests
- `panel/frontend/src/components/RelayListenerForm.vue`
- `panel/frontend/src/pages/RelayListenersPage.vue`
- `panel/frontend/src/components/RelayChainInput.vue`
- `panel/frontend/src/api/index.js`

## Acceptance Criteria

- A relay listener can bind to one or more local addresses while advertising a different public address.
- Leaving public endpoint blank preserves old behavior.
- CRUD reads always return expanded new fields plus compatibility `listen_host`.
- HTTP and L4 relay chains dial the public endpoint instead of the bind address.
- Multi-bind listeners start and stop cleanly in the Go runtime.
- Existing listener data using only `listen_host/listen_port` continues to work without manual repair.
