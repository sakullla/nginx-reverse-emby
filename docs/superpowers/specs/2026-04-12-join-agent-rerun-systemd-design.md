# Join Agent Rerun Systemd Design

## Context

The public installation script `scripts/join-agent.sh` supports:

- downloading the agent binary
- registering the agent
- optionally installing and starting a systemd service on Linux
- optionally installing and loading a launchd service on macOS

The current Linux `--install-systemd` flow downloads directly into the active binary path:

- binary destination: `BIN_PATH`
- service command: `ExecStart=$BIN_PATH`

This causes failures when the script is re-run on a host where the systemd-managed agent is already running. The observed failure is:

- `curl: (23) client returned ERROR on write of 4096 bytes`

The root cause is that the script tries to overwrite the currently executing binary before stopping or restarting the service.

## Goals

- make repeated `join-agent.sh --install-systemd` executions safe
- ensure the latest downloaded binary replaces the old one reliably
- avoid partial binary writes at the live executable path
- preserve the existing registration and install behavior as much as possible

## Non-Goals

- redesigning the launchd path
- changing registration semantics
- introducing version comparison logic
- changing the public HTTP API

## Options Considered

### Option A: Stop, stage, replace, restart

For the systemd path:

1. detect whether the service already exists
2. if it is active, stop it before replacing the binary
3. download or copy the binary to a temporary file
4. atomically move the temporary file into place
5. reload systemd and start or restart the service

Pros:

- directly fixes the observed rerun failure
- avoids partial writes to the live executable path
- small, local script change

Cons:

- introduces a short service interruption during reinstall

### Option B: Side-by-side binary replacement

Download to a new filename and then re-point the service or rename during restart.

Pros:

- also avoids writing the active file directly

Cons:

- more moving parts
- more script complexity for little extra value

### Option C: Documentation-only workaround

Require users to stop the systemd service manually before rerunning the installer.

Pros:

- no code change

Cons:

- does not actually fix the problem
- fragile and easy for users to miss

## Chosen Approach

Choose Option A.

This is the smallest reliable fix. The service may briefly stop during reinstall, but that is acceptable because the script is already an installation/update path rather than a zero-downtime deploy mechanism.

## Design

### Binary staging

Change the binary installation flow so that downloads and local copies target a temporary path first, for example:

- `BIN_PATH.tmp`

After a successful write and `chmod`, move the temporary file into place using `mv`.

This prevents the active executable from being truncated or partially overwritten mid-download.

### Systemd rerun behavior

For `--install-systemd` on Linux:

1. determine whether `nginx-reverse-emby-agent.service` already exists
2. determine whether it is currently active
3. if active, stop it before replacing the binary
4. write or update the unit file as today
5. after replacement, run `systemctl daemon-reload`
6. if the service previously existed, bring it back with an explicit lifecycle action
7. if the service did not previously exist, continue with first-time install behavior

The important behavior change is that reruns become explicit update operations instead of relying on `systemctl enable --now` to refresh an already-running process.

### Launchd behavior

Do not change the launchd branch in this task.

The observed failure is specific to the Linux systemd reinstall path, and expanding scope into launchd would add risk without evidence it is needed.

### Files in scope

- `scripts/join-agent.sh`
- `panel/backend-go/internal/controlplane/http/public_test.go`

## Testing Plan

Minimum validation:

1. verify the generated public `join-agent.sh` still contains the expected base URL substitutions
2. add or update tests so the script content includes the new temporary-binary and systemd stop/restart flow markers
3. run:
   - `cd panel/backend-go && go test ./internal/controlplane/http`

If script behavior changes require broader validation, run the full backend test suite afterward.

## Risks

- stopping the service before replacement introduces a brief interruption window during rerun
- shell portability must remain POSIX-compatible
- service state detection must avoid turning first-time installs into restart-only flows

These are manageable with a minimal change focused only on the Linux systemd branch.
