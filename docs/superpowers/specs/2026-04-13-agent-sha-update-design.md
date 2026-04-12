# Agent SHA Update Design

## Goal

Temporarily make the Go agent's self-update flow rely on runtime package SHA256 only, while preserving `desired_version` as a compatibility/display field on the control plane.

## Current State

The control plane maintains `desired_version` through version policies and includes it in agent snapshots. The Go agent currently prefers version comparison when `desired_version` is non-empty, and falls back to package SHA comparison only when `desired_version` is empty. This creates incorrect update behavior when version metadata drifts or is omitted while the package artifact changes.

The agent also reports `runtime_package_sha256`, but that value is currently only as trustworthy as whatever the running process was configured with. The desired behavior is for the agent to compute its own runtime binary SHA so the update decision and status reporting use the actual executable content.

## Requirements

1. The Go agent must decide whether to self-update using only `snapshot.version_package.sha256` and the currently running binary SHA.
2. `desired_version` must remain in the snapshot and control-plane data model for compatibility, but it must not affect the update decision.
3. The agent must compute the SHA256 of the current executable at startup and use that value for runtime package reporting and update comparison.
4. If the current executable SHA cannot be computed, the agent should continue running and report an empty SHA rather than failing startup.
5. Existing update staging verification must remain unchanged: downloaded packages are still verified against the expected SHA before activation.

## Approach Options

### Option A: Agent-only SHA switch with startup runtime SHA calculation

Change only the Go agent update decision logic and runtime package reporting. Keep control-plane `desired_version` behavior unchanged for now.

Pros:
- Smallest safe change.
- Preserves panel and API compatibility.
- Matches the user's temporary requirement.

Cons:
- `desired_version` remains visible in panel data even though agent update behavior ignores it.

### Option B: Remove `desired_version` from the entire sync path

Stop persisting and serving `desired_version` in the control plane and agent protocol.

Pros:
- Data model matches runtime behavior exactly.

Cons:
- Larger cross-system change.
- Risks breaking UI, APIs, tests, and existing workflows unnecessarily.

### Option C: Make update logic configurable between version-first and SHA-first

Add a feature flag to choose the update comparison strategy.

Pros:
- Flexible.

Cons:
- Extra complexity for a temporary operational workaround.
- More room for inconsistent deployments.

## Recommendation

Use Option A. It isolates the behavior change to the Go agent, keeps the control plane stable, and makes the update decision depend on the package identity that actually matters.

## Detailed Design

### Update Decision

In the Go agent app layer, `handlePendingUpdate` will stop branching on `desired_version`. If `snapshot.version_package` is missing or its `sha256` is empty, no self-update is attempted. If the desired package SHA differs from the current runtime SHA, the agent stages and activates the update. If the SHAs match, the agent skips the update.

### Runtime SHA Source of Truth

At startup, agent configuration will derive `RuntimePackageSHA256` from the executable content on disk. The source should be the real executable path, not environment state. The computation will use SHA256 over the binary bytes and store the lowercase hex digest in config for later sync requests and update comparison.

If the executable path cannot be resolved or the file cannot be read, config loading should leave `RuntimePackageSHA256` empty and continue. This avoids turning telemetry failure into startup failure.

### Control Plane Compatibility

The control plane continues to store and expose `desired_version`, `desired_package_sha256`, and related fields. No schema or API changes are required for this temporary fix. The agent simply ignores `desired_version` when deciding whether to replace itself.

## Testing Strategy

1. Add an app-level failing test proving that a non-empty `desired_version` does not trigger an update when desired package SHA equals the current runtime SHA.
2. Add an app-level failing test proving that a SHA mismatch triggers an update even when `desired_version` equals the current version or is otherwise irrelevant.
3. Add config-level failing tests proving runtime package SHA is computed from a real executable path and that failures degrade to empty SHA without returning an error.
4. Run focused `go-agent` tests for `internal/app` and `internal/config`, then the full `go-agent` suite.

## Risks

1. Some panel displays may still show `desired_version`, which could look authoritative even though updates are SHA-driven.
2. The computed runtime SHA represents the binary file content. If a deployment uses a wrapper executable instead of the actual agent binary, the computed SHA would describe the wrapper rather than the embedded payload.
3. Startup SHA calculation adds a file read and hash pass over the executable. This is acceptable because it happens once at process start.
