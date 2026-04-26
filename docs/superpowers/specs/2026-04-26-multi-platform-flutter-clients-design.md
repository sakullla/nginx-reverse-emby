# Multi-platform Flutter Clients Design

## Goal

Add first-class client support for Windows, Android, macOS, and Cloudflare Worker.

Windows and macOS get a Flutter GUI that manages a local `nre-agent` runtime. Android uses the same Flutter application as a light management client in the first release, without running local proxy or relay workloads. Cloudflare Worker does not use Flutter; it is configured through a control-plane deployment wizard and a generated Worker script.

## Scope

In scope:

- Add a new `clients/flutter/` Flutter application for Windows, macOS, and Android.
- Reuse the existing Go `nre-agent` for Windows and macOS execution-plane work.
- Let the Flutter desktop GUI register with the Master, write local configuration, start and stop the local agent, display status and logs, and coordinate updates.
- Let the Android app connect to the Master, show registered clients and agent status, view diagnostics, and open the control panel.
- Add control-plane metadata for client release packages across GUI clients, Go agent packages, and Worker scripts.
- Add a Cloudflare Worker deployment wizard in the panel that generates script/config instructions.

Out of scope for the first release:

- Android local proxy, VPNService, or relay execution.
- A native GUI for Cloudflare Worker.
- Replacing the existing Go agent protocol or proxy engine.
- Shipping Windows client packages from the control-plane container image.

## Architecture

The shared Flutter GUI lives under `clients/flutter/` and targets Windows, macOS, and Android.

On Windows and macOS, Flutter is a supervisory GUI for the local Go agent. It owns user interaction, local configuration, service or background-process control, log viewing, and update orchestration. The Go agent continues to own heartbeats, rule synchronization, HTTP proxying, L4 proxying, relay behavior, certificates, diagnostics, and runtime state.

On Android, the same Flutter app runs in light-management mode. It stores Master connection information and calls control-plane APIs to inspect clients, agents, versions, and diagnostics. It hides local runtime controls because the first release does not run `nre-agent` on Android.

Cloudflare Worker support is delivered through the existing panel. The panel provides a deployment wizard that collects Worker name, Master URL, token or secret values, and target script version, then renders the Worker script, environment variables, deployment commands, and validation steps.

## GUI Structure

The Flutter app uses one shared information architecture:

- Register: Master URL, register token, client/agent name, and tags.
- Overview: Master connection state, agent or client ID, runtime state, last sync, installed versions, and update prompts.
- Agent Runtime: Windows/macOS only. Start, stop, restart, install autostart/service integration, and inspect local config.
- Logs: Desktop reads local agent logs. Android shows control-plane status, recent errors, and diagnostics summaries.
- Updates: GUI client version, managed `nre-agent` version, download status, checksum verification, and rollback-safe failure display.
- Settings: Master URL, token reset, data directory, log level, startup behavior, and advanced paths.
- About: Version, platform, build information, and GitHub Release links.

Windows uses a tray-first background model. macOS uses a menu-bar-first background model. Closing the main window leaves the client running unless the user explicitly exits.

## Release Package Model

The control plane stores client release package metadata separately from hard-coded platform logic while staying compatible with existing agent version policy behavior.

Each package has:

- `version`
- `platform`: `windows`, `macos`, `android`, or `cloudflare_worker`
- `arch`: `amd64`, `arm64`, `universal`, or `script`
- `kind`: `flutter_gui`, `go_agent`, or `worker_script`
- `download_url`
- `sha256`
- `notes`
- `created_at`

The control plane exposes APIs to list matching packages, select latest compatible packages, and return checksum information for client-side verification.

## Data Flow

Desktop registration:

1. User enters Master URL, register token, client/agent name, and tags in Flutter.
2. Flutter calls the Master registration endpoint.
3. Master returns agent identity, runtime token, and initial package/update metadata.
4. Flutter writes local config for `nre-agent`.
5. Flutter starts or installs the local agent.
6. `nre-agent` continues with the existing heartbeat pull model.
7. Flutter displays status by combining local process state, local logs, and Master-reported state.

Android registration:

1. User enters Master URL and token.
2. Flutter validates the connection and stores the profile.
3. The app calls panel APIs to show agents, versions, and diagnostics.
4. No local execution-plane node is created.

Cloudflare Worker deployment:

1. User opens the Worker wizard in the panel.
2. Panel validates Worker name, Master URL, token/secret fields, and target script package.
3. Panel renders the script, environment variables, deploy command, and verification steps.
4. User deploys the Worker outside the panel. The first release does not call the Cloudflare API directly.

## Error Handling

Flutter local states:

- Unconfigured
- Pending registration
- Registered but offline
- Agent not running
- Agent running
- Updating
- Update failed

Registration failures show the server-provided reason when available. Update failures keep the previous working version. Desktop agent crashes show exit code, recent log lines, and a restart action. Missing local binaries and checksum mismatches are blocking errors.

The Worker wizard validates missing environment variables, empty tokens, malformed Master URLs, unsupported script versions, and package checksum mismatches when a package record includes a checksum.

## Testing

Backend tests:

- Release package CRUD.
- Platform and architecture matching.
- Latest compatible package selection.
- Compatibility with existing agent desired-version behavior.

Flutter tests:

- Registration form validation.
- Runtime state machine behavior.
- Platform capability switches.
- Update checksum and failure handling.
- Basic widget coverage for Overview, Runtime, Logs, Updates, and Settings.

Frontend panel tests:

- Cloudflare Worker wizard validation.
- Release package display and filtering.

Go agent tests:

- Only add or adjust tests when the desktop GUI introduces new config or startup contracts.
- Do not rewrite existing proxy, relay, or heartbeat behavior for the GUI.

Minimum verification commands:

```bash
cd panel/backend-go && go test ./...
cd go-agent && go test ./...
cd panel/frontend && npm run build
cd clients/flutter && flutter test
```

`flutter test` applies once the Flutter project exists.
