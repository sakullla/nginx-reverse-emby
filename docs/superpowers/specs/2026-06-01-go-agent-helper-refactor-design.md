# go-agent Helper Refactor Design

## Context

The `go-agent` module has recently folded several narrowly scoped packages into
`internal/model`, leaving the execution plane with a smaller package graph but
some low-value compatibility and helper code still spread across packages.
Current `go test ./...` passes before this refactor.

This work is a behavior-preserving cleanup. It may remove unused internal
compatibility paths and merge low-value files, but it must not change runtime
protocols, snapshot semantics, traffic accounting, certificate handling, or
relay behavior.

## Goals

- Delete unused internal compatibility code in the module registry.
- Extract repeated cross-package helpers into a small shared helper package.
- Keep traffic-copy helpers owned by the traffic module rather than a generic
  utility package.
- Merge single-purpose thin files when doing so improves readability.
- Keep package boundaries explicit and avoid creating a broad dumping ground.

## Non-Goals

- No protocol, wire format, or API compatibility changes outside internal
  Go package contracts.
- No rewrite of HTTP, L4, relay, WireGuard, ACME, or sync behavior.
- No repo-wide formatting or dependency churn.
- No consolidation of large tests merely to reduce file count.

## Architecture

Add `go-agent/internal/agentutil` for helpers that are:

- independent of business packages,
- used by multiple top-level internal packages,
- small enough to keep stable without becoming a generic framework.

Initial candidates:

- `CloneStringMap(map[string]string) map[string]string`
- `EnsureStringMap(map[string]string) map[string]string`
- `ParseInt64Default(string, int64) int64`

The package must not import app, core, module, model, or module-specific
packages. Future additions should follow the same dependency direction.

Traffic-oriented copy helpers remain in `internal/modules/traffic` because they
encode recorder and flush behavior. L4, relay, and HTTP should call traffic
helpers directly where wrapper functions add no local behavior.

The module registry should accept only the current `Module` interface. Remove
`LegacyModule`, `legacyModuleAdapter`, and `Registry.StartAll`. Tests that only
verify legacy adaptation should be deleted or rewritten around `Apply`.

## Components

### agentutil

`agentutil` owns small dependency-free helper functions. It replaces duplicated
metadata map cloning helpers in app, core, traffic, and embedded code where the
types match directly.

### traffic copy helpers

`traffic.CopyPreferReaderFrom`, `traffic.CopyGeneric`, and `TrafficWriter`
remain the shared copy surface. Thin local wrappers in relay and l4 should be
removed unless they express local behavior such as relay-specific TLS stream
fast-path selection.

### module registry

`Registry.Register` changes from accepting `any` plus adaptation to accepting
`Module`. Existing production modules already implement `Module`; test helpers
should be updated to register current modules directly. Error messages should
stay clear for nil and invalid module descriptors.

### low-value file merges

Merge only files that contain thin wrappers or narrowly coupled helpers and are
already edited for this cleanup. Do not make large domain files larger unless
the removed file has no independent reason to exist.

## Data Flow

No runtime data flow changes are expected. Metadata maps are still cloned before
being stored or exposed, but clone calls route through `agentutil`. Traffic copy
paths continue to record RX/TX bytes through the existing traffic recorder.
Module activation continues through `Registry.Apply`.

## Error Handling

Helper extraction must preserve nil behavior. `CloneStringMap(nil)` returns nil;
`EnsureStringMap(nil)` returns an empty map. Registry errors should continue to
wrap existing sentinel errors such as `ErrInvalidModule`, `ErrDuplicateModule`,
`ErrMissingProvider`, and `ErrProviderCycle`.

## Testing

Minimum verification:

- `cd go-agent && go test ./...`

Targeted updates:

- Adjust registry tests to remove legacy-only coverage and keep coverage for
  registration validation, ordering, provider resolution, apply rollback, and
  transaction behavior.
- Keep traffic copy tests focused on behavior, not wrappers.
- Add direct helper tests for `agentutil` only if behavior is not already fully
  covered by existing package tests.

## Risks

The main risk is over-extracting helpers into `agentutil`. Keep additions small
and dependency-free. Another risk is accidentally changing copy fast paths while
removing wrappers; relay-specific copy behavior must stay local where it has
special destination handling.
