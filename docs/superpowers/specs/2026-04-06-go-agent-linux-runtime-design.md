# Go Agent Linux Runtime Design

- Date: 2026-04-06
- Status: draft approved in chat, written for review
- Scope: Linux-only complete Go execution plane, pure-Go agent runtime, HTTP/L4 direct proxy, TCP relay, local certificate issuance/renewal, self-update

## 1. Goal

Replace the current agent-side execution plane with a pure Go runtime on Linux.

The control plane remains:

- `panel/backend` (Node.js)
- `panel/frontend` (Vue)

The execution plane becomes:

- a single Go agent binary (`nre-agent`)

The Linux agent must directly own:

- heartbeat pull
- persisted snapshots
- HTTP/HTTPS proxy runtime
- L4 TCP/UDP direct runtime
- TCP relay runtime with multi-hop support
- certificate issuance / renewal / hot reload
- self-update with download, verify, replace, and process handoff

Agent-side Node, shell apply scripts, and nginx must no longer be required.

## 2. Non-Goals

This design does not include:

- migrating `panel/backend` to Go
- migrating `panel/frontend` to Go
- preserving nginx directive compatibility
- supporting UDP relay
- building a generic nginx replacement outside the current product scope
- implementing Windows runtime/service in this phase

## 3. Architecture Summary

The system is split into two layers.

### 3.1 Control Plane

The existing Node/Vue control plane continues to manage:

- agents
- rules
- L4 rules
- relay listeners
- managed certificates
- version policy / `desired_version`
- heartbeat payload generation

### 3.2 Execution Plane

Linux agents run one Go binary:

- `nre-agent`

That binary contains the following modules:

- `app`: process lifecycle and top-level orchestration
- `config`: local startup config and environment loading
- `sync`: heartbeat pull client
- `store`: persistent local state and snapshots
- `runtime`: hot-reload orchestration across sub-runtimes
- `proxy`: HTTP/HTTPS runtime
- `l4`: TCP/UDP direct runtime
- `relay`: TCP relay runtime with multi-hop tunnel forwarding
- `certs`: certificate loading, issuance, renewal, hot reload
- `update`: self-update flow

## 4. Pure-Go Agent Constraint

The agent side may only run Go.

That means:

- no Node.js runtime dependency
- no shell-based apply chain
- no bundled nginx requirement
- no `acme.sh` dependency

If a Linux node runs as an agent or local agent, every execution-plane capability must be implemented inside the Go agent runtime.

## 5. State Model

The agent maintains three kinds of local state.

### 5.1 Desired Snapshot

The most recent desired state received from heartbeat sync:

- HTTP rules
- L4 rules
- relay listeners
- certificates / policies
- `desired_revision`
- `desired_version`
- version package metadata

### 5.2 Applied Snapshot

The last snapshot that was successfully activated.

This is the recovery baseline for:

- agent restart
- master temporarily unavailable
- failed apply rollback

### 5.3 Runtime State

Live execution status that is not itself configuration:

- active listeners
- listener health
- cert load state
- relay state
- update state
- `current_revision`
- `last_apply_status`
- `last_apply_message`

## 6. Local Persistence Layout

The Go agent stores its state under a private data directory, for example:

- `data/agent.json`
- `data/desired-snapshot.json`
- `data/applied-snapshot.json`
- `data/runtime-state.json`
- `data/certs/123.pem`
- `data/certs/123.key`
- `data/update/`

Requirements:

- the agent must restart from `applied-snapshot.json` without contacting the master
- the runtime must tolerate master unavailability while continuing to serve existing traffic

## 7. Heartbeat Pull Model

The system keeps the current heartbeat pull model.

The Go agent periodically sends heartbeat data containing:

- agent identity
- platform
- current version
- current revision
- runtime health summaries
- last apply result
- last update result

The control plane responds with:

- desired state snapshot
- desired revision
- desired version
- version package metadata

Heartbeat remains the only control-plane-to-agent control channel.

## 8. Snapshot Apply Flow

When the agent receives a new desired snapshot:

1. validate schema and business rules
2. persist the desired snapshot
3. build a runtime change plan
4. start or prepare new listeners / cert bindings / relay sessions
5. if activation succeeds, promote desired snapshot to applied snapshot
6. update runtime state and `current_revision`

If activation fails:

- keep the old applied snapshot
- keep old listeners active
- set runtime state to error
- do not advance `current_revision`

## 9. HTTP Runtime

The Go HTTP runtime directly serves:

- HTTP
- HTTPS
- host/path routing
- reverse proxying
- request header overrides
- redirect / location rewrite behavior
- websocket upgrade
- TLS certificate selection

### 9.1 Internal Layers

The HTTP runtime is split into:

1. listener layer
2. TLS/router layer
3. proxy layer

### 9.2 Hot Reload Rules

- keep listeners when listen address is unchanged and only routes/certs changed
- rebuild listeners when listen socket identity changes
- cert reload must not require full agent restart

## 10. L4 Runtime

The Go L4 runtime directly serves:

- TCP direct proxy
- UDP direct proxy
- TCP relay entry

It must reject:

- UDP relay

### 10.1 TCP Direct

Responsibilities:

- accept client connection
- dial upstream
- bidirectional stream forwarding
- timeout / keepalive / half-close handling

### 10.2 UDP Direct

Responsibilities:

- session map per source address
- datagram forwarding
- idle timeout cleanup

## 11. Relay Runtime

Relay is implemented as a Go-native TCP tunnel subsystem.

### 11.1 Supported Flows

- one-hop relay
- multi-hop relay
- HTTP over relay
- TCP L4 over relay

### 11.2 Unsupported Flow

- UDP relay

### 11.3 Multi-Hop Model

Rules reference an ordered relay chain, for example:

- `[relayA]`
- `[relayA, relayB]`
- `[relayA, relayB, relayC]`

The runtime uses the first hop, passes remaining hops forward, and lets the last hop connect to the final upstream/backend.

### 11.4 Security Model

Each hop uses TLS with one of:

- pin
- CA
- pin-or-CA
- pin-and-CA

Relay listener trust material must not be empty.

### 11.5 Data Model Relation

- HTTP rules may specify `relay_chain`
- TCP L4 rules may specify `relay_chain`
- UDP L4 rules must not specify `relay_chain`

## 12. Certificate Runtime

Certificates are managed by Go.

The agent must support both:

1. loading control-plane-provided certificate material
2. locally issuing or renewing certificates

### 12.1 Certificate Subsystems

- cert store
- cert loader
- issuer manager
- cert binding manager

### 12.2 Required Capabilities

- PEM/key validation
- fingerprint calculation
- listener binding
- hot reload after certificate update
- renewal scheduling and retry with backoff

### 12.3 Certificate Uses

The control plane certificate model must support uses such as:

- edge HTTPS
- relay tunnel server cert
- relay CA / trust material

## 13. Self-Update

The agent must support self-update driven by:

- `desired_version`
- `version_package`
- `version_sha256`

### 13.1 Update Flow

1. compare current version vs desired version
2. download package to local update directory
3. verify hash
4. stage new binary
5. launch new process and validate startup
6. hand over and terminate old process
7. report success/failure through heartbeat

### 13.2 Failure Rule

If the new binary fails validation:

- old process must continue serving
- update status must become failed
- failure reason must be reported

### 13.3 First Version Behavior

The first Linux version does not need zero-loss FD handoff.

Short reconnect windows during agent self-restart are acceptable for v1, as long as:

- failure is recoverable
- old process does not falsely report success

## 14. Local Agent Model

The local agent on the master host must also be Go.

There must no longer be a separate Node-side execution path for local apply.

The local Go agent uses the same:

- heartbeat model
- snapshot model
- runtime apply flow
- update flow

as remote Linux agents.

## 15. Validation Rules

### 15.1 Relay Listener Validation

- valid listen host
- valid listen port
- valid certificate binding for relay tunnel use
- trust material present
- valid TLS mode

### 15.2 Relay Chain Validation

- all listeners must exist
- all listeners must belong to registered agents
- all listeners must be enabled
- no duplicate listener in a single chain
- TCP-only for relay-enabled L4 rules

### 15.3 Version Policy Validation

- `desired_version` must map to a valid package for the agent platform
- unknown versions must not be assigned

## 16. Failure Handling

### 16.1 Master Unreachable

- continue serving from applied snapshot
- retry heartbeat

### 16.2 Apply Failure

- preserve old runtime
- keep old revision active
- report error state

### 16.3 Listener Failure

- fail only the affected listener
- keep unrelated listeners alive

### 16.4 Certificate Failure

- retain previous valid certificate material where possible
- report renewal or load failure

### 16.5 Update Failure

- old process continues
- failed update is reported

## 17. Acceptance Criteria

Linux complete Go execution plane is considered complete only when all of the following are true:

1. Linux agent runs as a pure Go execution plane
2. heartbeat pull works end-to-end
3. snapshots persist locally and recover after restart
4. HTTP and HTTPS runtime work without nginx
5. L4 TCP and UDP direct runtime work without nginx
6. TCP relay one-hop and multi-hop work
7. UDP relay is rejected
8. certificates can be loaded, issued, renewed, and hot-reloaded
9. `desired_version` self-update works
10. local Go agent uses the same runtime path as remote Go agents

## 18. Delivery Order

Implementation should follow dependency order:

1. control-plane schema/API alignment
2. Go agent app/config/store/sync bootstrap and real heartbeat/store
3. runtime orchestrator
4. HTTP runtime
5. L4 direct runtime
6. relay runtime
7. certificate runtime
8. self-update
9. local-agent integration and full-system verification
