# Go Dependencies Aggressive Upgrade Design

## Context

The repository contains two Go modules:

- `go-agent`
- `panel/backend-go`

The backend module depends on the agent module through a local `replace` directive:

- `github.com/sakullla/nginx-reverse-emby/go-agent => ../../go-agent`

The requested change is to aggressively upgrade Go dependencies while preserving the current Go language and toolchain versions:

- keep `go 1.26.0`
- keep `toolchain go1.26.2`

## Goals

- upgrade both Go modules to the latest compatible dependency graph available through Go modules
- include direct and transitively upgraded indirect dependencies
- keep runtime behavior changes limited to dependency upgrades only
- verify both modules still pass their test suites after the upgrade

## Non-Goals

- changing the Go language version
- changing the Go toolchain version
- refactoring application code unless required for dependency compatibility
- changing Docker images as part of this task

## Current State

The main directly declared dependencies currently include:

- `go-agent`
  - `github.com/go-acme/lego/v4 v4.31.0`
- `panel/backend-go`
  - `github.com/glebarez/sqlite v1.11.0`
  - `github.com/sakullla/nginx-reverse-emby/go-agent v0.0.0`
  - `gorm.io/gorm v1.31.1`

Both modules also carry a substantial indirect dependency graph, with many available updates reported by `go list -m -u all`.

## Options Considered

### Option A: Aggressive dependency-only upgrade

Upgrade dependencies to latest compatible versions while keeping the existing `go` and `toolchain` directives unchanged.

Pros:

- matches the requested aggressive dependency update
- avoids mixing compiler/toolchain changes with dependency changes
- easier to isolate regressions

Cons:

- may still introduce dependency-driven API or behavior changes

### Option B: Aggressive dependency plus toolchain upgrade

Upgrade dependencies and also move the Go version and toolchain forward.

Pros:

- maximum freshness

Cons:

- combines two separate migration axes
- makes regressions harder to diagnose
- larger compatibility surface

### Option C: Direct-dependencies-only upgrade

Upgrade only explicitly required modules and accept only minimal indirect changes.

Pros:

- lower risk

Cons:

- does not satisfy the requested aggressive update intent
- leaves a large amount of stale indirect dependencies behind

## Chosen Approach

Choose Option A.

This satisfies the request for an aggressive upgrade while keeping the migration bounded to the dependency graph. The local module replace relationship means the correct execution order is:

1. upgrade `go-agent`
2. run `go mod tidy` in `go-agent`
3. upgrade `panel/backend-go`
4. run `go mod tidy` in `panel/backend-go`
5. run full test suites in both modules

## Design

### Upgrade scope

For `go-agent`:

- upgrade the direct requirement `github.com/go-acme/lego/v4`
- allow `go get -u ./...` to update indirect dependencies pulled by the package graph
- normalize the module files with `go mod tidy`

For `panel/backend-go`:

- upgrade direct dependencies such as `github.com/glebarez/sqlite` and `gorm.io/gorm`
- allow the local `go-agent` replace target to contribute its updated dependency graph
- normalize the module files with `go mod tidy`

### Execution order

The agent module must be upgraded first because the backend module imports it locally. Upgrading in the opposite order risks churn or duplicate graph resolution work in the backend module.

### Compatibility handling

If an upgraded dependency introduces compile or test failures:

- inspect the exact breakage
- make the minimum required application code changes to restore compatibility
- keep those fixes tightly scoped to upgrade compatibility

No speculative refactors are in scope.

### Files in scope

- `go-agent/go.mod`
- `go-agent/go.sum`
- `panel/backend-go/go.mod`
- `panel/backend-go/go.sum`

Application source files are only in scope if required to fix concrete compatibility breaks caused by upgraded dependencies.

## Verification Plan

Minimum verification:

1. `cd go-agent && go test ./...`
2. `cd panel/backend-go && go test ./...`

If compatibility changes are needed in source files, rerun the affected module test suites after each fix and finish with both full module test runs green.

## Risks

- indirect dependency upgrades may change transitive behavior even when direct APIs remain stable
- SQLite-related upgrades may pull newer `modernc` packages and affect backend persistence behavior
- ACME-related upgrades may introduce API or behavior changes in `lego`

These risks are addressed by full module test runs after the upgrade.
