# Rule Diagnostics Task Channel Design

## Summary

This design introduces a dedicated task channel between `panel/backend-go` and remote `go-agent` instances so the master can actively dispatch short-lived diagnostic work without changing the existing pull-based configuration sync model.

The first feature on top of this channel is rule diagnostics for:

- HTTP/HTTPS rules
- L4 TCP rules

The first phase explicitly does not cover:

- L4 UDP diagnostics
- persistent diagnostic history
- background continuous probing
- smart routing or topology optimization
- BBR rollout

## Goals

- Allow the master to actively dispatch interactive tasks to pull-mode agents.
- Keep heartbeat-based desired state sync unchanged.
- Provide on-demand rule diagnostics that measure the actual egress path from the selected agent to the rule target.
- Expose a unified diagnostic result model to the panel so HTTP and L4 diagnostics can share one modal UI.

## Non-Goals

- Replacing heartbeat or revision-based config sync
- Building a generic remote shell or arbitrary execution channel
- Diagnosing arbitrary user-supplied targets
- Supporting UDP diagnostics in phase one
- Persisting results in SQL or building a long-term observability pipeline

## Context

The current architecture already supports:

- heartbeat/pull sync from agent to master
- revisioned HTTP, L4, relay listener, and certificate snapshots
- relay chaining for HTTP and L4 traffic
- QUIC and TLS/TCP relay transports

The current architecture does not support:

- master-initiated active task dispatch to pull-mode agents
- interactive rule-level diagnostics
- a runtime task/result channel independent from heartbeat

## Design Overview

The system is split into two independent planes:

- Config sync plane
  - Existing heartbeat + desired snapshot + revision apply flow
  - Remains pull-only
- Task plane
  - New long-lived reverse control session from agent to master
  - Carries only bounded interactive tasks

This preserves the safety and simplicity of the current pull model while allowing the master to react immediately to user-triggered actions.

## Architecture

### 1. Task Gateway on the Master

`panel/backend-go` adds a new `Task Gateway` endpoint that accepts a long-lived connection from each remote agent.

Responsibilities:

- authenticate the agent with the existing `X-Agent-Token`
- register one active task session per `agent_id`
- maintain session liveness
- route short-lived tasks to the correct agent session
- store short-lived in-memory task state and result payloads

The gateway is not a new config sync path. It only accepts agent registration and task/result messages.

### 2. Task Client on the Agent

Each remote `go-agent` opens an additional long-lived connection to the master and keeps it alive independently from heartbeat.

Responsibilities:

- authenticate as the current agent
- advertise supported task capabilities
- receive bounded task messages
- execute task handlers locally
- report task progress and final results

If the task connection drops, the agent reconnects with backoff. Heartbeat continues to run as before.

### 3. Rule Diagnostic Executor

`go-agent` adds a local diagnostic executor that runs rule-specific probes using the same egress logic the agent already uses for normal forwarding.

Responsibilities:

- resolve the requested rule from the current local rule set
- validate that the rule type is supported in phase one
- reuse current backend resolution and relay-chain logic
- collect repeated samples
- summarize latency, failure ratio, and quality grade

### 4. Panel Integration

The panel adds a `诊断` action to:

- HTTP rule cards
- L4 TCP rule cards

The panel requests a diagnostic task from the master, then displays a unified modal that transitions through:

- dispatching
- running
- completed or failed

## Why This Approach

### Recommended Approach

Use a dedicated reverse task session from agent to master, while keeping heartbeat as the config sync mechanism.

Why this is preferred:

- preserves pull-mode agents
- avoids requiring public `agent_url` on every agent
- supports NAT-constrained agents
- gives immediate reaction time for interactive actions
- cleanly separates desired config sync from imperative tasks

### Rejected Approach: Heartbeat Task Queue

Carry diagnostic tasks inside heartbeat replies.

Why rejected:

- interactive latency depends on heartbeat interval
- mixes imperative tasks with desired state delivery
- awkward timeout and retry semantics

### Rejected Approach: Master Directly Calls Agent URL

Require every pull-mode agent to expose a reachable task endpoint.

Why rejected:

- conflicts with the current pull-first deployment model
- introduces additional public reachability requirements
- is weaker for NAT-restricted agents

## Task Session Protocol

The transport can be implemented as any bidirectional stream that is easy to support in the current stack. The design assumes a single long-lived session with framed JSON messages.

### Session Semantics

- one active task session per remote agent
- a newer session replaces an older one for the same agent
- session disconnect marks the agent task channel offline
- task dispatch requires an active session

### Message Types

#### `hello`

Sent by agent after the task session is established.

Fields:

- `agent_id`
- `version`
- `capabilities`
- `session_id`

#### `task`

Sent by master to the agent.

Fields:

- `task_id`
- `type`
- `deadline`
- `payload`

#### `task_update`

Sent by agent to the master.

Fields:

- `task_id`
- `state`
- `progress_message`
- `result`
- `error`

Allowed states:

- `queued`
- `running`
- `completed`
- `failed`

#### `ping`

Bidirectional keepalive.

## Security Model

The task plane is intentionally narrow and non-generic.

### Authentication

- Reuse existing `X-Agent-Token`
- Master resolves the token to a known `agent_id`
- No anonymous or panel-token access to the task gateway

### Authorization

Only a fixed allowlist of task types is accepted.

Phase one allowlist:

- `diagnose_http_rule`
- `diagnose_l4_tcp_rule`

### Execution Scope

Tasks do not accept arbitrary target addresses for phase one. The payload contains a rule reference, and the agent resolves execution details from its current local state.

This prevents the task plane from becoming a general SSRF or arbitrary execution surface.

### Result Retention

Task status and results are kept in an in-memory store with a short TTL.

Phase one behavior:

- result available for immediate panel retrieval
- auto-expire after TTL
- not written to SQL

## Diagnostic API Design

The control plane adds explicit action endpoints:

- `POST /panel-api/agents/{agentID}/rules/{id}/diagnose`
- `POST /panel-api/agents/{agentID}/l4-rules/{id}/diagnose`

These endpoints:

- validate the rule exists
- validate the target agent exists
- validate the agent has an active task session
- create a short-lived task record
- dispatch the task over the agent session
- return a `task_id`

The panel then queries task state from a new task result endpoint, or subscribes to status if the UI later evolves to push updates.

Phase one can use polling for simplicity.

Suggested read endpoints:

- `GET /panel-api/agents/{agentID}/tasks/{taskID}`

## Diagnostic Task Payloads

### HTTP Rule Diagnostic Task

Payload fields:

- `rule_id`
- `rule_kind = "http"`
- `requested_by`
- `request_id`

The agent loads the resolved HTTP rule from local state and executes a real outbound request path.

### L4 TCP Rule Diagnostic Task

Payload fields:

- `rule_id`
- `rule_kind = "l4_tcp"`
- `requested_by`
- `request_id`

The agent loads the resolved L4 rule from local state and executes repeated TCP connect probes through the effective path.

## Diagnostic Execution Model

### Common Rules

- diagnostics run on the rule-owning agent
- diagnostics measure the actual egress path from that agent
- diagnostics reuse current relay-chain and backend-selection logic
- diagnostics use repeated samples
- diagnostics do not alter live runtime configuration

### HTTP/HTTPS Diagnostics

The diagnostic executor:

- resolves the HTTP rule
- resolves candidate backends using current backend ordering logic
- reuses relay-aware transport logic
- disables long-lived connection reuse for sample purity
- sends repeated real HTTP or HTTPS requests to the selected backend path

The diagnostic should not loop back through the public frontend URL. It should exercise the actual rule egress path, not the ingress listener path.

Collected sample data:

- success or failure
- total request latency in milliseconds
- selected backend address
- normalized failure reason

### L4 TCP Diagnostics

The diagnostic executor:

- resolves the L4 rule
- resolves candidate backends using current backend ordering logic
- reuses relay dialing logic where configured
- performs repeated TCP connect attempts

Collected sample data:

- success or failure
- connect latency in milliseconds
- selected backend address
- normalized failure reason

### Explicit Phase-One Exclusion: L4 UDP

UDP is excluded from phase one because there is no reliable generic notion of application-confirmed success or packet loss without protocol-specific echo semantics.

## Diagnostic Result Model

The panel uses one unified view model for both HTTP and L4 diagnostics.

Suggested response shape:

```json
{
  "task_id": "task-123",
  "state": "completed",
  "result": {
    "kind": "http",
    "rule_id": 12,
    "rule_label": "https://emby.example.com",
    "agent_id": "edge-1",
    "path_mode": "rule_actual_path",
    "summary": {
      "status": "success",
      "avg_latency_ms": 11,
      "loss_rate_pct": 0,
      "quality": "excellent"
    },
    "route": {
      "relay_chain": [
        {
          "listener_id": 2,
          "name": "relay-b",
          "transport_mode": "quic"
        }
      ],
      "selected_backend": {
        "address": "163.223.125.6:53660",
        "source": "http_backend"
      }
    },
    "samples": [
      { "seq": 1, "ok": true, "latency_ms": 10 },
      { "seq": 2, "ok": true, "latency_ms": 12 }
    ],
    "errors": [],
    "started_at": "2026-04-14T10:00:00Z",
    "finished_at": "2026-04-14T10:00:03Z"
  }
}
```

### Semantic Definitions

- `avg_latency_ms`
  - average successful sample latency
- `loss_rate_pct`
  - percentage of failed samples in the diagnostic window
- `quality`
  - phase-one bucket derived from latency and failure ratio

Phase-one loss is explicitly defined as sample failure ratio, not ICMP packet loss.

## Quality Grading

Phase one uses deterministic buckets:

- `excellent`
  - no failed samples and low latency
- `good`
  - low failure ratio and acceptable latency
- `fair`
  - higher latency or noticeable failures
- `poor`
  - high failure ratio or almost all failures
- `failed`
  - no successful samples

The exact thresholds can be implementation constants and are not part of the external API contract for phase one.

## Error Handling

### Control Plane Dispatch Errors

The control plane fails fast when:

- the rule does not exist
- the agent does not exist
- the rule type is unsupported
- no active task session exists for the agent

These return immediate API errors and do not create a task.

### Task Execution Errors

The task is created but ends in `failed` when:

- the rule exists in control-plane storage but is not present in the agent local state
- relay material is invalid
- no healthy backend candidate is available
- TLS verification fails
- all samples fail
- the execution deadline is exceeded

### Timeout Handling

Every task gets a hard deadline. The master marks overdue tasks failed if the session dies or the agent does not return a final state in time.

## Data Flow

### Session Establishment

1. Agent starts.
2. Agent continues normal heartbeat sync.
3. Agent opens task session to master.
4. Master authenticates token and registers active session.
5. Agent sends `hello`.

### Diagnostic Request Flow

1. Panel user clicks `诊断` on a rule card.
2. Panel calls the control-plane diagnostic action endpoint.
3. Master validates rule and session availability.
4. Master creates a short-lived task record.
5. Master sends `task` over the agent session.
6. Agent replies with `task_update: running`.
7. Agent executes rule diagnostics locally.
8. Agent replies with `task_update: completed` or `failed`.
9. Panel polls task state and renders the modal.

## Frontend Design

### HTTP Rule List

Add a `诊断` action to HTTP rule cards.

### L4 Rule List

Add a `诊断` action to L4 rule cards only when `protocol == tcp`.

### Diagnostic Modal

The modal presents:

- rule label
- agent identity
- relay path summary
- selected backend
- average latency
- failure ratio
- quality badge
- failure details when applicable

States:

- dispatching
- running
- completed
- failed

## Backend Components

### `panel/backend-go`

Add:

- task gateway session manager
- in-memory task registry
- diagnostic action handlers
- task read handler

Likely new responsibilities:

- `internal/controlplane/http`
  - task-session endpoint
  - diagnostic action endpoints
  - task status endpoint
- `internal/controlplane/service`
  - task dispatch orchestration
  - session registry
  - result registry

### `go-agent`

Add:

- task session client
- task dispatcher
- rule diagnostic executor

Likely new responsibilities:

- a task-plane package or module
- rule-specific diagnostic execution helpers
- result summarization helpers

## Testing Strategy

### Control Plane Tests

- task session registration and replacement
- task dispatch to online agent
- failure when no active task session exists
- task TTL expiry
- diagnostic action endpoint validation

### Agent Tests

- task session reconnect behavior
- HTTP diagnostic success and failure aggregation
- L4 TCP diagnostic success and failure aggregation
- correct reuse of relay-chain path logic
- correct mapping from raw samples to result summary

### Frontend Tests

- HTTP rule card shows diagnostic action
- L4 TCP rule card shows diagnostic action
- UDP rule card does not show diagnostic action
- modal loading, success, and failure states

### End-to-End Integration

- agent establishes task session
- master dispatches diagnostic task
- agent returns result
- panel fetches completed task result

## Operational Notes

- This feature requires the master to accept long-lived agent task sessions.
- Pull-mode agents remain the default and do not need public inbound reachability.
- The task plane is expected to be lightweight and sparse compared with heartbeat traffic.

## Risks

### Session Lifecycle Complexity

Introducing a second control channel adds lifecycle complexity. This is mitigated by keeping it separate from heartbeat and limiting it to one session per agent.

### Divergence Between Control-Plane Rule Storage and Agent Local State

A rule may exist in control-plane storage but not yet be applied locally. The diagnostic executor must treat the agent local state as source of truth for actual-path diagnostics.

### Over-Broad Task Surface

If task payloads accept arbitrary targets, the channel becomes dangerous. Phase one explicitly avoids that by allowing only rule-reference tasks.

## Future Work

Not part of phase one, but intentionally enabled by this design:

- persistent diagnostic history
- scheduled health probes
- topology measurement between relay nodes
- smart route selection and failover
- transport tuning rollout such as BBR policy control

