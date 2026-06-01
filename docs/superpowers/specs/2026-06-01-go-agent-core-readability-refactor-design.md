# go-agent Core Readability Refactor Design

## Context

The previous helper refactor removed several legacy registry and wrapper paths,
but it also introduced `internal/agentutil`. That package is too small and too
generic for this codebase: it has only primitive helpers and causes extra jumps
for simple local operations. The next cleanup should favor locality over broad
utility extraction.

The current readability hotspots are:

- `app/sync_runtime_state.go`, which contains wrappers that mostly forward to
  `core` or `traffic` for test access.
- `core/sync_controller.go`, where request planning, sync execution, update
  activation, runtime metadata persistence, and error recording live in one
  file with several indirect method hops.
- HTTP, L4, and Relay module entry files, where `Apply`/`Prepare` lifecycle
  logic is mixed with provider resolution, binding-key parsing, rollback state,
  clone helpers, and adapter types.

This is a behavior-preserving refactor. It must improve code locality and make
the main runtime paths easier to read without changing protocols, sync payloads,
traffic accounting, rollback semantics, or listener/runtime behavior.

## Goals

- Delete `go-agent/internal/agentutil`.
- Move simple helpers back to the packages that use them when that reduces
  navigation.
- Remove app-layer forwarding helpers that exist only to reach lower-level
  package functions from tests.
- Reshape `core.SyncController` so the public sync flow reads as a small number
  of business steps.
- Reduce HTTP/L4/Relay `module.go` density by moving low-level details into
  same-package files with clear names.
- Preserve the existing module lifecycle contracts and test behavior.

## Non-Goals

- No feature work.
- No protocol or wire-format changes.
- No changes to snapshot merge semantics.
- No changes to traffic counters, block-state behavior, or recorder flushing.
- No broad new utility package.
- No large test-file split in this pass.
- No unrelated formatting or dependency churn.

## Architecture

### Local Helpers Instead of `agentutil`

Delete `internal/agentutil`. Package-local helpers should be used where the
operation is simple and the call sites are few:

- `core` keeps its own metadata map helpers and integer parsing for sync state.
- `app` either removes forwarding helpers entirely or keeps only package-local
  helpers that are genuinely used by app code.
- `embedded` and `traffic` use local map clone helpers where needed.

The rule is: a helper must either express domain meaning in its package or be
used broadly enough to justify a shared package. Primitive helpers with a few
call sites do not qualify.

### App/Core Sync Flow

Keep `core.SyncController` as the sync orchestration type, but make its flow
read more directly:

1. Build the request from applied snapshot and runtime state.
2. Add optional reports such as traffic and managed certificates.
3. Fetch the next snapshot.
4. Persist desired snapshot payload.
5. Handle pending update package.
6. Apply runtime and persist applied/runtime state.
7. Record errors consistently.

Where private functions exist only to expose a single lower-level call through
`app`, remove them and update tests to call the real owning package.

### HTTP/L4/Relay Module Entry Files

Keep `Module.Apply` and `Module.Prepare` in `module.go`. Their job is to show
the lifecycle: detect no-op, resolve inputs, build or close runtime, commit, and
rollback.

Move supporting details into same-package files:

- Provider resolution and provider adapters.
- Runtime snapshot/rollback state helpers.
- Binding-key parsing and overlap checks.
- Clone helpers where they are not part of the lifecycle story.

This should reduce jumps inside the main flow because callers reading
`Prepare` see named concepts instead of low-level implementation detail. The
details remain nearby in the same package, not in a generic utility package.

## Components

### app package

`app` should own application wiring and runtime coordination only. Tests that
currently rely on app forwarding helpers should be moved to the package that
owns the behavior, or updated to call exported core/traffic functions directly.
`syncController()` may remain if it is the cleanest way to construct the core
controller from app dependencies, but repeated short-lived controller creation
should be reviewed for readability.

### core package

`sync_controller.go` remains the public surface for sync orchestration. Helper
logic may be moved into focused files such as sync metadata, snapshot merge, or
runtime persistence if that makes the primary flow easier to scan. Public
methods already covered by tests should keep their names unless a rename clearly
reduces confusion and all internal callers are updated.

### HTTP/L4/Relay modules

The module entry files should keep module lifecycle methods and high-level
runtime decisions. New same-package files should be named by domain concept,
not by generic categories. Examples:

- `providers.go` for resolving module providers and adapter types.
- `runtime_state.go` for committed state snapshots and restore helpers.
- `bindings.go` for binding-key parsing and overlap.

Do not introduce cross-package sharing between HTTP/L4/Relay solely because
similar helper names exist. These modules differ in protocol behavior, rollback
needs, and listener semantics.

## Error Handling

Error wrapping must preserve current user-facing context. In particular:

- Sync errors should still be logged and recorded in runtime metadata.
- Runtime apply failure should still attempt rollback and record the candidate
  revision.
- Persisted applied snapshot failures should still record persisted runtime
  errors after rollback.
- Runtime bind-conflict retry behavior must remain unchanged.
- Relay restore failures should remain joined or wrapped with the original
  failure as they are today.

## Testing

Minimum verification:

- `cd go-agent && go test ./...`

Focused verification during implementation:

- `go test ./internal/app ./internal/core`
- `go test ./internal/modules/http ./internal/modules/l4 ./internal/modules/relay`
- `go test ./embedded ./internal/modules/traffic` when removing `agentutil`
  call sites.

Regression tests should be added only when behavior is made more explicit or a
previously untested edge is discovered during the refactor. For pure relocation,
existing tests should be enough.

## Risks

The main risk is mistaking movement for readability. Moving code out of
`module.go` is useful only if the main lifecycle path becomes clearer and the
new file name points to a real domain concept. Another risk is deleting app test
wrappers without preserving equivalent coverage in the owning package. Each
wrapper removal should be paired with either existing direct coverage or an
updated test.

## Acceptance Criteria

- `internal/agentutil` is gone.
- No production imports reference `internal/agentutil`.
- App forwarding helpers that only call core/traffic are removed or justified.
- `core.SyncController` reads as a clear sync workflow with fewer unrelated
  helper responsibilities in the main file.
- HTTP/L4/Relay `module.go` files focus on lifecycle orchestration; low-level
  provider, binding, clone, and runtime-state details move to same-package
  concept files where useful.
- `go test ./...` passes from `go-agent`.
