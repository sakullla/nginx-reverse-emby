# Agent Uninstall and Legacy Nginx Cleanup Design

## Goal

Tighten the agent-side migration and cleanup story for the pure-Go architecture with these boundaries:

- Successful `migrate-from-main` cleanup must also stop and clean the legacy nginx runtime remnants from the old host-side architecture.
- `join-agent.sh` must provide an `uninstall-agent` command that fully removes the local agent runtime from a VPS.
- Agent uninstall is local-only. Control-plane agent records remain managed from the control panel.
- `scripts/managed-cert-helper.sh` is removed because it is no longer used by the active architecture.

## Scope

### In Scope

- Extend `join-agent.sh` with an `uninstall-agent` subcommand.
- Reuse a shared legacy nginx cleanup path from both migration cleanup and uninstall.
- Stop and disable local agent services and legacy nginx services when present.
- Remove agent runtime files, launchd/systemd units, legacy runtime directories, and nginx-generated config/runtime remnants.
- Remove the unused `scripts/managed-cert-helper.sh` file.
- Update tests and README text to match the new behavior.

### Out of Scope

- Deleting agent records from the control plane during uninstall.
- Uninstalling nginx packages through `apt`, `yum`, `dnf`, or other package managers.
- Deleting control-plane containers, images, or control-plane data directories.
- Adding a new control-plane API for self-unregister.

## User Flow

### Legacy Agent Migration Cleanup

1. User runs `join-agent.sh migrate-from-main ...` on a host still carrying the old lightweight-agent runtime.
2. The script migrates runtime state to the Go agent and verifies the new service is healthy.
3. After verification succeeds, the script removes old lightweight-agent files and ACME remnants.
4. The same cleanup phase also stops legacy nginx, disables it when managed by systemd, and removes generated nginx config/runtime remnants left by the old architecture.

### Agent Uninstall

1. User runs `join-agent.sh uninstall-agent`.
2. The script reads the local install layout and current platform.
3. The script stops and removes the installed agent service definition for systemd or launchd.
4. The script removes the agent data directory and any migrated legacy runtime directory.
5. The script removes legacy nginx runtime remnants from the host.
6. The script prints that the local runtime has been removed and reminds the user that control-plane deletion is still manual.

## Architecture

### Shared Cleanup Helpers

`join-agent.sh` gains two focused cleanup helpers:

- `cleanup_legacy_nginx_runtime()`
- `cleanup_local_agent_runtime()`

`cleanup_legacy_runtime()` remains the migration-only wrapper that keeps ACME cleanup and old lightweight-agent cleanup, then delegates nginx cleanup to `cleanup_legacy_nginx_runtime()`.

`uninstall-agent` reuses `cleanup_local_agent_runtime()` and `cleanup_legacy_nginx_runtime()` but skips any control-plane interaction.

### Platform Boundaries

- Linux systemd installs:
  - stop and disable `nginx-reverse-emby-agent.service`
  - remove `/etc/systemd/system/nginx-reverse-emby-agent.service`
  - remove any legacy renew unit and backed-up unit artifacts created during migration
  - run `systemctl daemon-reload`
- macOS launchd installs:
  - unload `~/Library/LaunchAgents/com.nginx-reverse-emby.agent.plist`
  - remove the plist file
- Shared runtime cleanup:
  - remove the configured `DATA_DIR`
  - remove `SOURCE_DIR` if it still exists
  - remove generated nginx config/runtime remnants

### Legacy Nginx Cleanup

Legacy nginx cleanup is local host cleanup only. It does not touch package-manager state.

The shared nginx cleanup function should:

- attempt `systemctl disable --now nginx.service` on Linux when `systemctl` is available
- tolerate missing services and missing files
- remove these old generated artifacts when present:
  - `/etc/nginx/conf.d/zz-nginx-reverse-emby-agent.include.conf`
  - `/etc/nginx/conf.d/zz-nginx-reverse-emby-agent.globals.conf`
  - `/etc/nginx/conf.d/zz-nginx-reverse-emby-agent.status.conf`
  - `/etc/nginx/conf.d/dynamic`
  - `/etc/nginx/stream-conf.d/dynamic`

The cleanup must be idempotent so both migration and uninstall can call it safely.

## CLI Contract

### New Command

`join-agent.sh` adds:

- `join-agent.sh uninstall-agent [options]`

Supported options:

- `--data-dir DIR`
- `--source-dir DIR`

This command does not require:

- `--master-url`
- `--register-token`
- `--agent-token`

### Usage Text

The script usage and examples must list:

- `migrate-from-main`
- `uninstall-agent`

The uninstall help text must state that local runtime files are removed, but the control-plane agent record is not deleted automatically.

## Testing

### Script Exposure Tests

Extend `panel/backend-go/internal/controlplane/http/public_test.go` so the served `join-agent.sh` asserts:

- the `uninstall-agent` command is present in usage text
- the script contains legacy nginx cleanup logic
- the script contains local uninstall cleanup logic
- the script does not contain a control-plane unregister HTTP call for uninstall

### Verification

Minimum verification for implementation:

- `cd panel/backend-go && go test ./...`
- `C:\Program Files\Git\bin\bash.exe -n scripts/join-agent.sh`

## Documentation

- Update `README.md` migration text to say successful migration cleanup also stops and removes legacy nginx runtime remnants.
- Add `uninstall-agent` usage and explicitly note that control-plane deletion remains a manual control-panel action.
- Remove any remaining references to `scripts/managed-cert-helper.sh`.

## Risks and Constraints

- Uninstall must not remove unrelated user-managed nginx content outside the known legacy runtime paths.
- Migration cleanup must remain safe to re-run on partially cleaned hosts.
- The new uninstall path must work even when the host has only a partial install left behind.
