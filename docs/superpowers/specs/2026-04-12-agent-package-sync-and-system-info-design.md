# Agent Package Sync And System Info Design

## Context

The current remote-agent flow has three separate gaps:

1. Re-running `scripts/join-agent.sh --install-systemd` on a host with an already running agent fails because the script writes directly to the live executable path while systemd is still running the old process.
2. Automatic agent package switching is currently driven only by `desired_version`. When the control plane wants to roll out a different package but leaves `desired_version` empty, the agent has no SHA-based fallback decision.
3. Node management does not expose enough runtime package metadata to explain what package an agent is actually running versus what package the panel expects it to run.

The user wants these behaviors to work together:

- repeated `join-agent.sh` executions must be safe
- package rollout should happen automatically when the running package differs from the panel package
- package details should appear in node management system information
- package mismatch should fall back to SHA comparison when `desired_version` is empty

## Goals

- make repeated systemd installs safe and minimize service interruption
- keep the existing version-driven update behavior when `desired_version` is set
- add a SHA-driven update fallback when `desired_version` is empty
- persist and display agent runtime package metadata in system information
- let operators see whether an agent is already aligned with the panel package

## Non-Goals

- redesigning launchd behavior on macOS
- changing version policy concepts or version policy UI structure
- adding a separate package-details page or a separate package-details card outside system information
- introducing zero-downtime binary handoff across process generations

## Current State

### Installer

`scripts/join-agent.sh` currently:

- downloads directly into `BIN_PATH`
- writes the environment file
- writes the systemd unit file
- calls `systemctl enable --now nginx-reverse-emby-agent.service`

This works on a first install but fails on rerun when the target file is still the executable of a running service.

### Agent update behavior

`go-agent/internal/app/app.go` currently:

- triggers the updater only when `NeedsUpdate(currentVersion, desiredVersion)` returns true
- requires a valid `VersionPackage`
- stages and activates the new binary through the existing update manager

This means package identity is currently version-string-driven, not runtime-package-driven.

### Node metadata

The control plane already stores and returns:

- agent version
- combined platform string
- desired version

But it does not currently store or expose:

- current runtime package SHA
- split architecture field
- desired package SHA summary for the current platform
- package sync status derived from current runtime package versus desired package

## Options Considered

### Option A: Hybrid version-or-SHA update logic with system-info visibility

- keep version-based behavior when `desired_version` is present
- fall back to SHA comparison when `desired_version` is empty
- add runtime package metadata to heartbeat, persistence, and system information
- make `join-agent.sh` use staged downloads and a short service-stop window

Pros:

- preserves existing version policy semantics
- supports package-only rollouts
- gives operators direct visibility into package drift
- keeps the installer fix local and low risk

Cons:

- requires coordinated changes across script, agent, backend, and frontend

### Option B: Convert all update decisions to SHA-only

- ignore `desired_version` for update triggering
- always compare package SHA

Pros:

- simple runtime rule

Cons:

- breaks current version policy meaning
- would surprise existing operators who use `desired_version` as the rollout trigger

### Option C: Installer-only fix

- fix `join-agent.sh` rerun behavior
- leave automatic update and UI visibility unchanged

Pros:

- smallest implementation

Cons:

- does not satisfy the requested package-sync workflow

## Chosen Approach

Choose Option A.

This is the smallest change set that solves the actual operating workflow:

- the installer becomes safe to rerun
- the agent can self-update either by version intent or package SHA intent
- node system information can explain both the running package and the desired package

## Design

### 1. Safe systemd reinstall flow in `join-agent.sh`

The Linux systemd path will switch to a staged replacement flow:

1. resolve the final binary destination as today
2. download or copy the binary to a temporary file in the same directory
3. set permissions on the temporary file
4. write the env file and service unit while the current service keeps running
5. only if `--install-systemd` is enabled and replacement is ready:
   - detect whether the service exists
   - detect whether it is active
   - if active, stop it only immediately before replacement
   - atomically move the staged binary into place
   - run `systemctl daemon-reload`
   - start the service again
6. if the service did not previously exist, continue with the normal first-install enable/start flow

Important behavior:

- the service interruption window excludes download time
- failed downloads or failed staging never touch the running binary
- launchd remains unchanged in this task

### 2. Runtime package identity reported by the agent

The agent will compute and report runtime package metadata for the currently running executable.

The reported shape should include:

- `version`
- `platform`
- `arch`
- `sha256`

Compatibility note:

- keep the existing combined platform string used elsewhere
- add a split `arch` field so the UI does not need to parse `linux-amd64`

The agent should determine the SHA from the current executable on startup and reuse it for heartbeat payloads during the process lifetime.

### 3. Update trigger rules

The updater decision becomes:

- if `desired_version` is non-empty:
  - keep the existing version-based rule
  - use `NeedsUpdate(currentVersion, desiredVersion)`
- if `desired_version` is empty:
  - require a valid desired package payload
  - compare the desired package `sha256` with the running package `sha256`
  - trigger update only when they differ

This preserves current semantics for version-managed rollouts while enabling SHA-managed rollouts for package-only replacements.

The updater activation path itself does not need a conceptual redesign. It can continue to:

- stage the desired package
- replace the executable
- restart through the existing activation path

### 4. Control-plane persistence and system information

The control plane will persist the runtime package fields reported by heartbeat so they are available to:

- agent summary queries
- node system information queries

System information should be extended in-place, not split into a separate package info view.

For each node, system information should return:

- current agent version
- current platform
- current architecture
- current runtime package SHA
- desired version
- desired package SHA for the node platform, when a package exists
- package sync status derived by the backend

The backend should compute the sync status so the frontend only renders it. The derived status can be represented as a boolean or a small string state, but it must distinguish:

- package aligned
- package update pending

When no desired package exists for the node platform, the desired package fields may be empty and the status should not incorrectly claim an update is pending.

### 5. Frontend system information display

Node management will surface the new package fields inside the existing system information area.

Display requirements:

- do not add a separate package-details block outside system information
- keep list cards compact
- show the detailed package values in system information

Recommended presentation:

- version
- platform
- architecture
- current package SHA
- desired package SHA
- package status

Usability details:

- render SHA values as shortened prefixes by default
- expose the full SHA through tooltip text or a copy-friendly affordance
- use clear labels such as `已同步` and `待更新`

### 6. Data flow summary

1. the panel publishes version policy package metadata as today
2. the agent heartbeat reports current runtime package identity
3. the control plane stores both the current runtime package identity and the desired package summary for that platform context
4. on sync, the agent decides whether to update:
   - by version when `desired_version` is set
   - by SHA when `desired_version` is empty
5. node system information renders the current-versus-desired package state

## Files In Scope

- `scripts/join-agent.sh`
- `go-agent/internal/app/app.go`
- `go-agent/internal/sync/client.go`
- `go-agent/internal/model/...` for heartbeat/runtime package structs
- `panel/backend-go/internal/controlplane/service/agents.go`
- `panel/backend-go/internal/controlplane/service/system.go`
- `panel/backend-go/internal/controlplane/http/...` for heartbeat and info payload wiring
- `panel/backend-go/internal/controlplane/storage/...` for persistence changes
- `panel/frontend/src/pages/AgentsPage.vue`
- any existing node system information component used by the agents UI

## Testing Plan

Minimum verification:

1. script-focused backend tests covering the generated public `join-agent.sh` content
2. `go-agent` tests covering:
   - update by desired version
   - no update when desired version matches
   - SHA-driven update when desired version is empty and package SHA differs
   - no SHA-driven update when desired version is empty and SHA already matches
3. backend tests covering:
   - heartbeat persistence of runtime package metadata
   - system information response shape for the new fields
   - package status derivation for aligned and pending states
4. frontend build verification that the system information UI renders the new fields

Expected verification commands:

- `cd panel/backend-go && go test ./...`
- `cd go-agent && go test ./...`
- `cd panel/frontend && npm run build`

If image packaging is touched as part of implementation follow-up, also run:

- `docker build -t nginx-reverse-emby .`

## Risks

- schema changes for new runtime package fields must remain backward-compatible with existing agent rows
- platform-to-package resolution must not report false pending states when no package exists for a given platform
- SHA comparison must use the currently running executable identity, not only a staged or desired artifact
- installer lifecycle logic must distinguish rerun updates from first-time installs so first-time installs still auto-start correctly

## Open Decisions Resolved In This Spec

- package rollout uses version comparison when `desired_version` is set
- package rollout uses SHA comparison when `desired_version` is empty
- package fields are shown inside the existing system information area, not in a separate UI section
- systemd reinstall prioritizes a short cutover window by staging downloads before stopping the service
