# Flutter Daily CRUD Client Design

Date: 2026-05-01

## Summary

Implement the first complete Flutter client milestone as a daily operations client, not a full Vue panel replacement. The client will support management mode for common control-plane CRUD and agent mode for local runtime registration/control. This milestone finishes the existing Flutter navigation surface: Dashboard, Rules, Agents, Certificates, Relay, and Settings.

L4 rules, version policies, client package management, backup import/export, and other full-admin maintenance pages are out of scope for this milestone.

## Goals

- Turn the current Flutter shell into a usable desktop/mobile client for daily operations.
- Support both management and agent connection profiles.
- Use a typed API-first layer that mirrors the Vue panel's `runtime.js` contract for the included features.
- Complete CRUD flows for HTTP/HTTPS rules, remote agents, certificates, and relay listeners.
- Preserve the Windows local agent runtime controls already present in the Flutter client.
- Add focused tests for model normalization, API requests, provider behavior, and critical widgets.

## Non-Goals

- Do not implement L4 rule management in this milestone.
- Do not implement version policies, client packages, backup import/export, or worker deployment.
- Do not replace the Vue panel's full administrative surface.
- Do not redesign the established glassmorphism visual system unless a component must change to support the workflow.

## Connection Modes

The connect flow will support two modes.

### Management Mode

Management mode stores:

- `masterUrl`
- `panelToken`
- display name or local profile label
- active mode: `management`

Management mode calls `/panel-api/*` endpoints using the panel-token authentication expected by the Go control plane. The Flutter network layer will send `X-Panel-Token: <panelToken>` for panel APIs. Management mode unlocks Dashboard, Rules, Agents, Certificates, Relay, and Settings CRUD workflows.

### Agent Mode

Agent mode stores:

- `masterUrl`
- `agentId`
- `agentToken`
- display name
- active mode: `agent`

Agent mode keeps the existing register-token based flow for registering this client as an agent and starting/stopping the local `nre-agent.exe` on Windows. Agent mode does not grant management CRUD by itself.

### Mixed Profile Behavior

The client can keep both management credentials and an agent profile for the same control plane. Invalid management credentials must not automatically delete the local agent profile. Disconnect actions should make clear which profile is being cleared.

## Architecture

Use an API-first design.

```text
Connect Wizard
  -> Auth/Profile Store
    -> PanelApiClient / AgentRegistrationApi / LocalAgentController
      -> Riverpod feature stores
        -> Screens and dialogs
```

### Network Layer

Add or refactor toward a typed panel API client that owns:

- Base URL normalization.
- `X-Panel-Token` authentication for management endpoints.
- Register-token and agent-token headers for registration.
- Long-running request options for apply, rule changes, relay changes, and certificate issue/renew operations.
- Response envelope normalization for backend shapes such as `{ ok, rules }`, `{ ok, agents }`, `{ ok, certificate }`, and error payloads.
- Consistent exception mapping for 401, 403, 404, validation errors, network errors, and server errors.

Screens and providers should not assemble endpoint paths or parse backend envelopes directly.

### Data Models

Replace overly thin models with typed models that cover the fields used by the included Vue panel workflows.

Rules should cover HTTP/HTTPS operational fields:

- `id`
- `frontend_url`
- `backend_url`
- `backends`
- `enabled`
- `tags`
- `proxy_redirect`
- `pass_proxy_headers`
- `user_agent`
- `custom_headers`
- `load_balancing.strategy`
- `relay_chain`
- `relay_layers`
- `relay_obfs`

Agents should cover:

- `id`
- `name`
- `status`
- `mode`
- `platform`
- `version`
- `last_seen`
- `current_revision`
- `target_revision`
- `tags`
- capability metadata used for display and filtering

Certificates should cover:

- `id`
- `domain`
- `scope`
- `issuer_mode`
- `certificate_type`
- `status`
- `expires_at`
- `issued_at`
- `self_signed`
- `fingerprint`
- `target_agent_ids`
- available association/display fields returned by the backend

Relay listeners should cover:

- `id`
- `agent_id`
- `agent_name`
- `name`
- `listen_port`
- `bind_hosts`
- `protocol` or equivalent display mode
- `enabled`
- TLS/certificate source fields used by create/edit flows
- trust mode fields used by create/edit flows

## Feature Scope

### Dashboard

Dashboard should show real data instead of placeholders where APIs exist:

- Rule count and disabled count.
- Agent count and online/offline summary.
- Certificate count and expiring summary.
- Relay listener count and active summary.
- Local agent runtime status on platforms that support it.
- Quick actions routing to create or manage daily resources.

### Rules

Rules covers HTTP/HTTPS rules only in this milestone.

Required workflows:

- List rules for the selected/default agent or local/global context supported by the backend.
- Search and filter by status/type.
- Create a rule with the backend-compatible HTTP payload.
- Edit an existing rule.
- Toggle enabled state with optimistic update and rollback on failure.
- Delete with confirmation and rollback on failure.
- Expose apply/diagnose entry points if the backend endpoints are already available; detailed diagnostic UI can stay minimal in this milestone.

### Agents

Required workflows:

- Replace the remote-agents empty state with real data.
- Show agent name, status, platform/version, mode, revision/sync state, and last-seen metadata.
- Search/filter agents.
- Rename an agent.
- Delete/unregister an agent with confirmation.
- Apply config to an agent.
- Keep local agent runtime controls for Windows start/stop/restart/status.

### Certificates

Required workflows:

- List certificates with expiry/status display.
- Filter by status.
- Create/request certificate metadata using backend-compatible payloads.
- Edit certificate metadata where supported.
- Issue/renew certificates through the existing issue endpoint.
- Delete certificates with confirmation.
- Show details for fields returned by the backend.

File picker based certificate upload can be limited to metadata and text-entry flows if platform file handling would make the first milestone too large.

### Relay

Required workflows:

- List relay listeners across available agents with agent attribution.
- Search/filter listeners.
- Create relay listener.
- Edit relay listener.
- Toggle enabled state with optimistic update and rollback on failure.
- Delete with confirmation.
- Support the certificate/trust fields required by backend payloads at a pragmatic level for daily operations.

### Settings

Required workflows:

- Show active connection mode and stored profile details.
- Switch or clear management credentials.
- Preserve and display agent profile details separately.
- Keep theme/accent controls.
- Show local agent binary, data, and log paths where known.
- Avoid placeholder messages for settings actions.

## Error Handling

- Parse backend error envelopes and surface meaningful messages in list pages, dialogs, and snackbars.
- 401 and 403 in management mode should tell the user the panel token is invalid or lacks permission.
- Management auth failures should not automatically remove local agent credentials.
- Long-running actions should show loading state and use longer request timeouts.
- Optimistic updates for toggles/deletes must restore previous state on failure.
- Lists should provide retry affordances when loading fails.

## Testing Strategy

Use test-driven development for implementation.

Test layers:

- Model tests for JSON parsing and payload normalization.
- API tests for headers, endpoints, response envelopes, error mapping, and long-running request options.
- Provider tests for loading, success, error, optimistic update, and rollback behavior.
- Widget tests for connection mode selection, rules form behavior, agent list rendering/actions, and relay/certificate action entry points.

Minimum verification for the implementation milestone:

```powershell
cd clients/flutter
flutter test
```

If shared backend contracts are touched, also run:

```powershell
cd panel/backend-go
go test ./...
```

## Implementation Notes

- Keep the existing Flutter visual language and component library.
- Prefer typed request/response objects over `Map<String, dynamic>` in feature code.
- It is acceptable for the API layer to contain small normalization helpers that adapt legacy or alias backend fields.
- Regenerate Riverpod generated files consistently after provider changes.
- Keep patches staged by layer: auth/profile, API/models, providers, screens, then polish/tests.

