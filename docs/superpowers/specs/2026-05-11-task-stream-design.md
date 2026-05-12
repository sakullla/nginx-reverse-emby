# Task Stream NDJSON Design

## Goal

Replace the agent task channel's SSE dependency with an MCP-like message stream for remote agents. The stream should carry task dispatches and task lifecycle updates over one long-lived HTTP connection using newline-delimited JSON (NDJSON).

## Current State

Remote agents currently open `GET /api/agents/task-session`, receive `text/event-stream` frames containing `event: task`, and report task state through `POST /api/agent-tasks/{taskID}/updates`. The local in-process agent does not use HTTP; it implements the shared `service.TaskSession` interface directly.

The agent package already defines a protocol envelope in `go-agent/internal/task/protocol.go` with `hello`, `task`, `update`, and `ping` message shapes. The current HTTP client only uses the `task` payload shape while parsing SSE.

## Proposed Protocol

Add `POST /api/agents/task-stream` under both `/api` and `/panel-api`.

The request is authenticated with the existing `X-Agent-Token` header. After authentication, the server resolves the agent from the token and ignores any spoofable agent id supplied in the body or query string.

The request body and response body are both NDJSON streams. Each line is a complete JSON object using the existing message envelope:

```json
{"type":"hello","hello":{"agent_id":"edge-a","session_id":"edge-a-1","version":"1.0.0","capabilities":["diagnose_http_rule"]}}
{"type":"task","task":{"task_id":"task-1","task_type":"diagnose_http_rule","deadline":"2026-05-11T10:00:00Z","payload":{"rule_id":7}}}
{"type":"update","update":{"task_id":"task-1","state":"running"}}
{"type":"update","update":{"task_id":"task-1","state":"completed"}}
{"type":"ping","ping":{"sent_at":"2026-05-11T09:59:00Z"}}
```

Direction rules:

- Agent to server: `hello`, `update`, and optional `ping`.
- Server to agent: `task` and optional `ping`.
- Unknown message types are ignored.
- Malformed JSON or invalid task updates end the stream with an error.

The server registers the stream as a `service.TaskSession` once the HTTP stream is established. When `TaskService.CreateAndDispatch` calls `SendTask`, the stream session writes one NDJSON `type=task` message to the response and flushes it. The same handler reads request-body lines concurrently and applies `type=update` messages through `TaskService.ApplyUpdate`.

## Compatibility

Keep the existing SSE endpoint and update endpoint for rolling upgrades:

- `GET /api/agents/task-session` remains available.
- `POST /api/agent-tasks/{taskID}/updates` remains available.
- The agent tries `POST /api/agents/task-stream` first.
- If stream setup fails because the endpoint is unavailable or incompatible, the agent falls back to the old SSE session.

The local in-process agent remains unchanged because it already uses the service-level session interface and does not depend on HTTP framing.

## Error Handling

The server returns normal JSON errors before stream registration for authentication, method, or service setup failures. After streaming starts, connection closure is the error signal. A failed write closes and unregisters the session through existing `TaskService.CreateAndDispatch` behavior.

Agent update validation reuses `TaskService.ApplyUpdate`, so missing `task_id`, missing `state`, wrong agent ownership, and unknown tasks keep the same service semantics as the existing update endpoint.

## Testing

Backend HTTP tests should cover:

- `POST /api/agents/task-stream` authenticates by `X-Agent-Token`.
- A registered stream receives a task as one NDJSON `type=task` line.
- A streamed `type=update` line calls `TaskService.ApplyUpdate` with the authenticated agent id.
- The existing SSE endpoint still behaves for compatibility.

Agent tests should cover:

- The client attempts `task-stream` before SSE.
- The client handles a streamed `type=task` message and writes `running` and terminal `type=update` messages on the same connection.
- The client falls back to SSE when `task-stream` is unavailable.

Verification commands:

- `cd panel/backend-go && go test ./internal/controlplane/http`
- `cd go-agent && go test ./internal/task`
- `cd panel/backend-go && go test ./...`
- `cd go-agent && go test ./...`
