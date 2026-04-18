# Backup, Import, and Main-Agent Migration Design

## Goal

Add a complete migration path from the old `main` architecture to the new pure-Go architecture with these boundaries:

- Old `main` control plane supports `导出备份` and `导入备份`.
- New pure-Go control plane supports `导出备份` and `导入备份`.
- The backup package is portable across old and new control-plane architectures.
- Old `main` Agent nodes are migrated separately by running `join-agent.sh migrate-from-main`.
- Daily backup export and backup import remain available in both control-plane architectures.

The design must support a low-friction migration flow, include certificate material, skip conflicting items with a report, and clean up the old Agent-side runtime after a successful migration.

## Scope

### In Scope

- System settings UI for backup export and backup import.
- Control-plane backend APIs and services for portable backup package export/import.
- Backup package support for old `main` control-plane data and new control-plane data.
- Old `main` control-plane implementation of backup export/import.
- New pure-Go control-plane implementation of backup export/import.
- Agent-side `join-agent.sh migrate-from-main` flow for old `main` lightweight agents.
- Agent token reuse during Agent migration.
- Migration of certificate material required for no-touch cutover.
- Cleanup of old lightweight-agent services, old runtime files, old nginx generated config, and old ACME-managed records after a successful Agent migration.
- Directory default normalization for new deployments.

### Out of Scope

- One-click Master migration from a host-side script.
- Migration of runtime heartbeat state, apply status history, or transient health snapshots as authoritative data.
- Keeping the old and new Agent runtimes active at the same time.
- Direct import of arbitrary `.acme.sh` internal state into the new architecture.

## User Flow

### Control Plane Migration

1. User opens the old-version system settings page.
2. User clicks `导出备份` and downloads a backup package.
3. User upgrades the Master to the new version.
4. User opens the new-version system settings page.
5. User imports the backup package.
6. System imports non-conflicting data and shows an import report.
7. User visits each old Agent node and runs the new `join-agent.sh migrate-from-main` command.

### Daily Backup and Restore

Both old and new control planes support the same operational pattern:

1. User opens system settings.
2. User clicks `导出备份`.
3. User may import a previously exported backup package into the same architecture or the other architecture.
4. System skips conflicting items and shows a detailed report.

### Agent Migration

1. User runs `join-agent.sh migrate-from-main --master-url ... --register-token ... --source-dir ...` on an old lightweight-Agent node.
2. Script reads the old local runtime files and environment.
3. Script installs the new `go-agent` into the standardized directory.
4. Script reuses the old `agent_token`.
5. Script stops the old lightweight-Agent runtime before starting the new `go-agent`.
6. Script verifies the new Agent is up.
7. Script removes old runtime files, generated nginx config, and old ACME-managed records.

## Architecture

### Shared Backup Package Format

Both old-control-plane export/import and new-control-plane export/import use a single tarball format, for example:

- `manifest.json`
- `agents.json`
- `http_rules.json`
- `l4_rules.json`
- `relay_listeners.json`
- `certificates.json`
- `version_policies.json`
- `certificate_material/<domain>/cert.pem`
- `certificate_material/<domain>/key.pem`

`manifest.json` includes:

- package version
- source architecture (`main-legacy` or `pure-go`)
- export timestamp
- source app version if available
- counts by resource type
- whether certificate material is included

This keeps import logic centralized and lets both control-plane architectures accept both old and new export packages without maintaining two unrelated import pipelines.

### Control Plane Layers

- Old `main` HTTP handlers expose export/import endpoints.
- New pure-Go HTTP handlers expose export/import endpoints.
- A shared backup service contract produces and consumes the tarball package.
- Translators normalize old `main` exported records into the new internal service/storage model and normalize new exports into the portable package model.
- Import execution writes through existing service/storage layers where possible so validation remains consistent.

### Agent Migration Layers

- `join-agent.sh` adds a `migrate-from-main` subcommand.
- Migration helpers read old lightweight-Agent files from the old runtime directory.
- The script installs the new `nre-agent` binary and environment into `/var/lib/nre-agent` by default.
- The script performs cutover in a strict order: stop old runtime, start new runtime, verify, clean old runtime.

## Data Model and Mapping

### Migrated Control-Plane Data

The control-plane backup/import includes, in both old and new architectures:

- Agent inventory records: name, tags, version, mode, display URL where applicable
- HTTP rules
- L4 rules
- Relay listeners
- Managed certificate configuration
- Managed certificate material
- Version policies

The import intentionally does not treat runtime heartbeat state as canonical migrated data.

### Conflict Policy

The import policy is fixed:

- conflicting items are skipped
- the import continues
- a report is returned and shown in the settings UI

Conflict keys:

- Agent: `name`
- HTTP rule: `frontend_url`
- L4 rule: `protocol + listen_host + listen_port`
- Relay listener: `agent + name`
- Certificate: `domain`
- Version policy: `id`

Report categories:

- imported
- skipped_conflict
- skipped_invalid
- skipped_missing_material

### Certificate Material

The backup package includes certificate PEM and private key material. This is required for the requested no-touch migration path.

For old `main` exports:

- certificate configuration comes from the old stored certificate metadata
- certificate material comes from the old managed certificate paths on disk

For Agent-side migration from old lightweight-Agent nodes:

- local direct certs come from the old `certs/<domain>/cert` and `certs/<domain>/key`
- `.acme.sh` is not imported as a new source of truth
- `.acme.sh` may be inspected for cleanup targets and optional metadata only

## Control Plane API Design

### Endpoints

The backup API surface is added to both the old `main` control plane and the new pure-Go control plane:

- `GET /panel-api/system/backup/export`
- `POST /panel-api/system/backup/import`
- alias routes under `/api/...` where existing compatibility patterns require them

### Export Response

- file download with tarball body
- manifest generated at export time

### Import Request

- multipart upload containing the backup tarball

### Import Response

- package manifest summary
- imported counts
- skipped counts
- detailed conflict and validation report

## Settings UI Design

The system settings page gains a new data-management section:

- `导出备份`
- `导入备份`
- import result summary
- downloadable or copyable conflict report

This section sits alongside existing theme/system information, not as a separate page, because migration and backup are administrative system actions.

There is no separate `备份` button with a different format or different behavior. The product surface is intentionally reduced to:

- one export action: `导出备份`
- one import action: `导入备份`

The UI behavior is implemented in both old and new control-plane frontends:

- `导出备份` is a direct download action
- import accepts a selected tarball
- import shows progress state
- import result renders a concise summary first, then the detailed report

## Agent Migration Design

### Command Shape

The new script supports:

```sh
join-agent.sh migrate-from-main \
  --master-url http://master.example.com:8080 \
  --register-token change-this-register-token \
  --source-dir /opt/nginx-reverse-emby-agent
```

Optional flags may include:

- `--data-dir` to override the new installation directory, defaulting to `/var/lib/nre-agent`

### Old Runtime Inputs

The migration reads old lightweight-Agent state from:

- `agent.env`
- `proxy_rules.json`
- `l4_rules.json`
- `managed_certificates.json`
- `managed_certificates.policy.json`
- `agent-state.json`
- `certs/`
- `.acme.sh/`

### Agent Identity Handling

The migrated Agent reuses:

- old `agent_token`
- old agent name
- old tags
- old agent version if available as display metadata

Reusing `agent_token` enables the intended low-friction migration, but only if cutover is serialized. The script must never leave old and new runtimes active together.

### Cutover Sequence

1. Validate required old files exist.
2. Load old environment and derive the new configuration.
3. Install the new binary and new env files into `/var/lib/nre-agent` by default.
4. Stop and disable old lightweight-Agent services.
5. Start the new `go-agent`.
6. Verify the new Agent process starts and the service is active.
7. Verify the new Agent can use the reused `agent_token` without dual-runtime conflict.
8. Cleanup old runtime files and services.

If the new Agent fails before verification completes:

- old cleanup does not run
- the script reports failure
- the user can inspect or rerun after fixing the issue

### Old Runtime Cleanup

After successful verification, the script cleans up:

- old lightweight-Agent service units
- old renew service units
- old runtime files
- old rules/state files
- old generated nginx dynamic config
- old nginx include/status helper config created by the lightweight-Agent runtime

For `.acme.sh` cleanup:

- the script must remove managed certificates through `.acme.sh` commands first
- then remove old renewal automation
- then remove remaining old `.acme.sh` data if it still belongs to the old runtime

The cleanup must not delete `.acme.sh` records by blindly removing directories before the corresponding `acme.sh --remove` commands run.

## Directory Normalization

### New Defaults

- control plane data directory default: `/var/lib/nginx-reverse-emby`
- standalone Agent data directory default: `/var/lib/nre-agent`

### Compatibility

- explicit env var overrides remain supported
- old defaults remain readable for migration detection only
- documentation, compose examples, and script defaults move to the new normalized directories

## Error Handling

### Import Errors

- malformed tarball: reject with validation error
- unsupported manifest version: reject with clear upgrade message
- partial record failures: continue and report skipped items
- missing certificate material: skip affected certificate item and report it

### Agent Migration Errors

- missing old source files: fail before mutating services
- old service stop failure: fail and do not start cleanup
- new service start failure: fail and preserve old runtime files if cleanup has not started
- `.acme.sh --remove` failure: report explicitly and stop destructive final cleanup for certificate state
- nginx generated-config cleanup failure: report explicitly and fail the migration cleanup step

## Testing Strategy

### Control Plane Tests

- old `main` export package generation
- old `main` import package parsing
- new pure-Go export package generation
- new pure-Go import package parsing
- export package generation for pure-Go data
- import package parsing for old `main` manifests
- conflict skip behavior
- import report generation
- certificate material round-trip tests
- settings API handler tests

### Agent Migration Tests

- old file discovery from a source directory
- env translation into the new Agent env file
- `agent_token` reuse
- service stop/start sequencing
- cleanup of old nginx generated config
- `.acme.sh` remove-command invocation ordering
- failure-path tests that prove cleanup does not run too early

### End-to-End Verification

- old control plane export -> old control plane import
- old control plane export -> new control plane import
- new control plane export -> new control plane import
- new control plane export -> old control plane import where the old architecture supports the imported resource set
- old Agent node migration to new `go-agent`
- certificate still usable after migration
- no old renew service remains active
- no old lightweight-Agent nginx helper config remains active

## Rollout Notes

- Old and new control-plane import/export support should land together with the settings UI changes so users can create portable backups before upgrading.
- The Agent migration command should ship with updated documentation that explains the full sequence: export old control-plane data, upgrade Master, import backup, migrate Agents.
- The startup guard that rejects old control-plane storage remains valid; users are expected to import before relying on the new control plane.
