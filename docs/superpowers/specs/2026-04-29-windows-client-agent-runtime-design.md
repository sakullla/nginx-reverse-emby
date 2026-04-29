# Windows Client Agent Runtime Design

## Goal

Implement production-mode local agent control in the Flutter Windows client. A registered client can start and stop a local `nre-agent.exe` process, pass the registered agent configuration through environment variables, and show actionable runtime state.

## Scope

This design covers the first production-mode runtime slice:

- Windows desktop client support.
- Managing an already-installed `nre-agent.exe` binary from a local application data directory.
- Starting and stopping the agent as a child process without administrator rights.
- Reporting registered, not installed, stopped, running, and error states in the Runtime screen.
- Building a Windows `nre-agent.exe` artifact locally for manual placement during testing.

This design does not cover Windows service installation, automatic GitHub Release downloads, update orchestration, or installer packaging. Those are follow-up stages that should build on the interfaces introduced here.

## Current State

The Flutter client has a `LocalAgentController` interface and an unsupported stub. The Runtime screen is static: it shows `Local agent`, `Not installed`, and disabled Start/Stop buttons. The registration screen already registers an agent with the control plane and stores the profile in app state, including `masterUrl`, `displayName`, `agentId`, and `token`.

The Go execution plane starts from `go-agent/cmd/nre-agent`. It reads configuration from environment variables:

- `NRE_MASTER_URL`
- `NRE_AGENT_ID`
- `NRE_AGENT_NAME`
- `NRE_AGENT_TOKEN`
- `NRE_DATA_DIR`
- `NRE_AGENT_VERSION` when available

## Architecture

Add a real Windows implementation of `LocalAgentController` behind a conditional import. The app injects this controller into `RuntimeScreen`, along with the current `ClientState`. The controller owns process lifecycle operations and a small filesystem layout under local app data.

Default Windows layout:

- Binary: `%LOCALAPPDATA%\NRE Client\agent\nre-agent.exe`
- Data directory: `%LOCALAPPDATA%\NRE Client\agent-data`
- Runtime directory: `%LOCALAPPDATA%\NRE Client\runtime`
- PID file: `%LOCALAPPDATA%\NRE Client\runtime\nre-agent.pid`
- Log file: `%LOCALAPPDATA%\NRE Client\logs\nre-agent.log`

The Runtime screen is responsible for UI state, validation, and user actions. The controller is responsible for filesystem checks, process start, process stop, and status detection.

## Runtime Behavior

Status resolution:

1. If the client profile is not registered, report `unavailable`.
2. If `nre-agent.exe` is missing, report `unavailable` with an install message.
3. If the PID file points to a live `nre-agent.exe` process, report `running`.
4. If the PID file is missing or stale, report `stopped`.
5. If filesystem or process inspection fails, report `unavailable` with the error message.

Start behavior:

1. Require a registered profile.
2. Require the binary to exist at the configured binary path.
3. Create data, runtime, and log directories.
4. Start `nre-agent.exe` with the environment variables derived from the profile.
5. Redirect stdout and stderr to the log file.
6. Write the process ID to the PID file.
7. Return `running` on success.

Stop behavior:

1. Read the PID file.
2. If the process is live, terminate it.
3. Remove a stale or terminated PID file.
4. Return `stopped` on success.

The initial implementation can use normal child-process termination. Service-grade shutdown, restart policies, and privilege elevation are explicitly out of scope.

## UI Design

Replace the static Runtime screen with a stateful screen:

- Show the local agent status.
- Show the binary path so testers know where to place `nre-agent.exe`.
- Show Start and Stop buttons based on status.
- Disable Start when the profile is not registered or the binary is missing.
- Show a concise error message when start, stop, or status checks fail.
- Preserve the existing simple Material UI style.

The screen should refresh status when opened and after each Start or Stop action. It should not poll continuously in this first slice.

## Build And Manual Install

Provide a documented local build command for the Windows agent binary:

```powershell
cd go-agent
$env:GOOS='windows'
$env:GOARCH='amd64'
go build -o ..\clients\flutter\build\agent\nre-agent.exe .\cmd\nre-agent
```

For client testing, copy the built binary to:

```text
%LOCALAPPDATA%\NRE Client\agent\nre-agent.exe
```

The client will not download or copy this file automatically in this slice.

## Error Handling

The controller returns structured status rather than throwing raw process errors to the UI. User-facing messages should distinguish:

- Not registered.
- Agent binary not installed.
- Start failed.
- Stop failed.
- Status check failed.

Sensitive values, especially the agent token, must not be rendered in the UI or logs created by the client.

## Testing

Add unit coverage for controller behavior without launching the real Go agent:

- Environment variable construction from `ClientProfile`.
- Status is unavailable when the profile is not registered.
- Status is unavailable when the binary is missing.
- A stale PID file resolves to stopped and is cleaned up.
- Start refuses to run when not registered or not installed.

Add widget coverage for the Runtime screen:

- Unregistered state disables Start and Stop.
- Missing binary shows the install path.
- Stopped state enables Start.
- Running state enables Stop.
- Controller errors are displayed.

Manual verification:

1. Build `nre-agent.exe`.
2. Copy it to `%LOCALAPPDATA%\NRE Client\agent\nre-agent.exe`.
3. Register the Windows client with a control plane.
4. Click Start Agent.
5. Confirm the control plane receives agent heartbeats.
6. Click Stop Agent.
7. Confirm the local process exits.

## Future Work

Later production hardening can add:

- Windows service install and uninstall with administrator elevation.
- Automatic download and sha256 verification from GitHub Releases or the control-plane package API.
- Restart-on-crash behavior.
- A logs page backed by the agent log file.
- Version and update management through the existing desired-version model.
