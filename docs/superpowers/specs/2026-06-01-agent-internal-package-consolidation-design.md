# Agent Internal Package Consolidation Design

## Goal

Reduce the number of `go-agent/internal` packages while keeping high-risk data-plane boundaries intact. The refactor should prioritize fewer package directories and less duplicated control-plane client plumbing without changing runtime behavior.

## Scope

Consolidate these package boundaries:

- Move `internal/runtime` into `internal/core`.
- Merge `internal/sync` and `internal/task` into a new `internal/control` package.
- Flatten `internal/platform/linux` into `internal/platform` with build-tagged files.

Keep these packages separate:

- `config`, because it is the agent's environment/configuration boundary.
- `store`, because it is shared by app, core, diagnostics, and embedded runtime code.
- `backends`, because it is a large shared backend selection/cache subsystem.
- `netproxyproto`, because it is a standalone protocol implementation used by egress, L4, and relay.
- `upstream`, because it is shared path classification/scoring logic across HTTP, L4, and relay.
- `stream`, because it currently wraps traffic accounting and is shared by HTTP, L4, and relay.
- `netutil`, because its callers are broad; it can be split or moved in a later targeted pass.

## Package Design

### `internal/core`

`core` becomes the owner of snapshot application state. The current `runtime.Runtime`, `Activator`, and snapshot clone/apply/rollback logic move into `core`. Existing `core.NewSnapshotActivator` should return the local `core.Activator` type. App code should construct `core.NewRuntimeWithActivator(...)` rather than importing `internal/runtime`.

This removes one package while preserving the existing controller/runtime separation inside the `core` package.

### `internal/control`

`control` becomes the agent-to-master communication package. It contains:

- Heartbeat/sync client and `SyncRequest`.
- Task stream/SSE client.
- Task protocol message types and diagnostic task constants.
- Shared master URL normalization and HTTP transport construction helpers.

This removes two packages and replaces them with one package. It also removes duplicated client setup code currently present in `sync` and `task`.

### `internal/platform`

`platform` owns OS-specific process/service helpers. The Linux `ExecReplacement` and `ServiceUnitName` functions move from `internal/platform/linux` to `internal/platform`, implemented with build tags:

- `service_linux.go` for Linux `syscall.Exec`.
- `service_default.go` for non-Linux fallback.

This removes the nested `linux` package. The current `platform/windows` helper is unused and should be removed unless a test or build path proves it is still needed.

## Compatibility

This is an internal refactor only. No public API, CLI flag, environment variable, protocol payload, or runtime behavior should change.

Import aliases such as `agentsync`, `agenttask`, and `agentruntime` should be removed or updated to the new packages as part of the refactor.

## Testing

Minimum verification:

```powershell
cd go-agent
go test ./...
```

If import changes affect embedded/container build paths, also run:

```powershell
docker build -t nginx-reverse-emby .
```
