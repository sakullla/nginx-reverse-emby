# Agent Package Sync And System Info Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `join-agent.sh` safe to rerun on systemd hosts, let agents auto-update by SHA when `desired_version` is empty, and surface agent package metadata inside node system information.

**Architecture:** The change is split into four vertical slices: installer cutover, agent runtime package reporting and SHA-based update fallback, backend persistence/system-info derivation, and frontend system-info rendering. Each slice is implemented test-first and committed independently so behavior stays reviewable and bisectable.

**Tech Stack:** POSIX `sh`, Go control plane, Go agent, Vue 3 + Vite

---

## File Map

- `scripts/join-agent.sh`
  Responsibility: stage downloads to a temp path and minimize the systemd stop window during reruns.
- `panel/backend-go/internal/controlplane/http/public_test.go`
  Responsibility: verify the generated public join script contains the expected staged-download and short cutover markers.
- `go-agent/internal/model/version.go`
  Responsibility: hold package metadata shared between sync payloads and update logic.
- `go-agent/internal/sync/client.go`
  Responsibility: send runtime package metadata in heartbeat payloads and keep consuming desired package metadata from the control plane.
- `go-agent/internal/app/app.go`
  Responsibility: decide whether to update by version or by SHA fallback.
- `go-agent/internal/app/app_test.go`
  Responsibility: cover desired-version and SHA-based update decisions.
- `panel/backend-go/internal/controlplane/storage/sqlite_models.go`
  Responsibility: persist runtime package metadata for remote agents.
- `panel/backend-go/internal/controlplane/storage/schema.go`
  Responsibility: add backward-compatible schema updates for new agent metadata columns.
- `panel/backend-go/internal/controlplane/service/agents.go`
  Responsibility: accept runtime package metadata from heartbeat, persist it, derive desired package summary, and expose it in agent summaries.
- `panel/backend-go/internal/controlplane/service/system.go`
  Responsibility: extend system info with the package fields needed by the frontend.
- `panel/backend-go/internal/controlplane/service/agents_test.go`
  Responsibility: test heartbeat persistence and package sync state derivation.
- `panel/backend-go/internal/controlplane/http/router_test.go`
  Responsibility: test `/panel-api/info` payload shape and agent payload serialization with the new fields.
- `panel/frontend/src/api/index.js`
  Responsibility: mock and fetch the new system info fields.
- `panel/frontend/src/pages/AgentDetailPage.vue`
  Responsibility: render the package information inside the existing system info tab.

### Task 1: Make `join-agent.sh` safe to rerun on systemd hosts

**Files:**
- Modify: `scripts/join-agent.sh`
- Test: `panel/backend-go/internal/controlplane/http/public_test.go`

- [ ] **Step 1: Write the failing script-content test**

Add assertions to `panel/backend-go/internal/controlplane/http/public_test.go` inside `TestRouterServesJoinScriptAndHeartbeat`:

```go
	if !strings.Contains(script, "BIN_TMP_PATH=\"$BIN_PATH.tmp.$$\"") {
		t.Fatalf("join-agent.sh missing staged binary temp path: %s", script)
	}
	if !strings.Contains(script, "systemctl stop nginx-reverse-emby-agent.service") {
		t.Fatalf("join-agent.sh missing systemd stop before replace: %s", script)
	}
	if !strings.Contains(script, "mv \"$BIN_TMP_PATH\" \"$BIN_PATH\"") {
		t.Fatalf("join-agent.sh missing atomic staged binary move: %s", script)
	}
	if !strings.Contains(script, "systemctl start nginx-reverse-emby-agent.service") {
		t.Fatalf("join-agent.sh missing explicit systemd start after replace: %s", script)
	}
```

- [ ] **Step 2: Run the focused backend test and verify it fails**

Run:

```powershell
go test ./internal/controlplane/http -run TestRouterServesJoinScriptAndHeartbeat
```

Expected:

```text
FAIL
... join-agent.sh missing staged binary temp path ...
```

- [ ] **Step 3: Implement staged download and short cutover flow**

Update `scripts/join-agent.sh` so binary download always targets a temp file and systemd reruns only stop the service immediately before replacement:

```sh
copy_or_download_binary() {
    asset_name="$1"
    dest_path="$2"
    local_path=""

    if [ -n "$SCRIPT_DIR" ] && [ -f "$SCRIPT_DIR/../panel/public/agent-assets/$asset_name" ]; then
        local_path="$SCRIPT_DIR/../panel/public/agent-assets/$asset_name"
    elif [ -n "$SCRIPT_DIR" ] && [ -f "$SCRIPT_DIR/$asset_name" ]; then
        local_path="$SCRIPT_DIR/$asset_name"
    fi

    mkdir -p "$(dirname -- "$dest_path")"

    if [ -n "$local_path" ] && [ -f "$local_path" ]; then
        cp "$local_path" "$dest_path"
        chmod 755 "$dest_path"
        return 0
    fi

    if [ -n "$BINARY_URL" ]; then
        echo "[JOIN] Downloading nre-agent from $BINARY_URL ..." >&2
        curl -fsSL --connect-timeout 15 --max-time 300 "$BINARY_URL" -o "$dest_path"
        chmod 755 "$dest_path"
        return 0
    fi

    [ -n "$ASSET_BASE_URL" ] || {
        echo "Missing nre-agent binary source. Re-run with --asset-base-url URL or --binary-url URL." >&2
        exit 1
    }

    echo "[JOIN] Downloading $asset_name from $ASSET_BASE_URL ..." >&2
    curl -fsSL --connect-timeout 15 --max-time 300 "$ASSET_BASE_URL/$asset_name" -o "$dest_path"
    chmod 755 "$dest_path"
}

service_exists() {
    systemctl status nginx-reverse-emby-agent.service >/dev/null 2>&1
}

service_is_active() {
    systemctl is-active --quiet nginx-reverse-emby-agent.service
}
```

Also replace the direct install flow:

```sh
BIN_TMP_PATH="$BIN_PATH.tmp.$$"
rm -f "$BIN_TMP_PATH"
copy_or_download_binary "$ASSET_NAME" "$BIN_TMP_PATH"
```

And change the systemd branch so it stages first, then cuts over:

```sh
    SERVICE_EXISTS="0"
    SERVICE_WAS_ACTIVE="0"
    if service_exists; then
        SERVICE_EXISTS="1"
        if service_is_active; then
            SERVICE_WAS_ACTIVE="1"
        fi
    fi

    if [ "$SERVICE_WAS_ACTIVE" = "1" ]; then
        run_root_cmd systemctl stop nginx-reverse-emby-agent.service
    fi
    run_root_cmd mv "$BIN_TMP_PATH" "$BIN_PATH"
    run_root_cmd systemctl daemon-reload
    if [ "$SERVICE_EXISTS" = "1" ]; then
        run_root_cmd systemctl enable nginx-reverse-emby-agent.service
        run_root_cmd systemctl start nginx-reverse-emby-agent.service
    else
        run_root_cmd systemctl enable --now nginx-reverse-emby-agent.service
    fi
```

For the non-systemd path, finalize the staged download without downtime logic:

```sh
elif [ "$INSTALL_LAUNCHD" = "1" ]; then
    mv "$BIN_TMP_PATH" "$BIN_PATH"
```

and:

```sh
else
    mv "$BIN_TMP_PATH" "$BIN_PATH"
    echo "[JOIN] Start command:"
    echo "  set -a && . $ENV_FILE && set +a && $BIN_PATH"
fi
```

- [ ] **Step 4: Run the focused backend test and verify it passes**

Run:

```powershell
go test ./internal/controlplane/http -run TestRouterServesJoinScriptAndHeartbeat
```

Expected:

```text
ok  	github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/http	...
```

- [ ] **Step 5: Commit the installer slice**

Run:

```bash
git add scripts/join-agent.sh panel/backend-go/internal/controlplane/http/public_test.go
git commit -m "fix(agent): stage join installer updates before systemd cutover"
```

### Task 2: Add SHA-based update fallback in the agent

**Files:**
- Modify: `go-agent/internal/model/version.go`
- Modify: `go-agent/internal/sync/client.go`
- Modify: `go-agent/internal/app/app.go`
- Test: `go-agent/internal/app/app_test.go`

- [ ] **Step 1: Write the failing agent tests**

Add these tests to `go-agent/internal/app/app_test.go` near the existing updater tests:

```go
func TestHandlePendingUpdateUsesSHAFallbackWhenDesiredVersionEmpty(t *testing.T) {
	ctx := context.Background()
	mem := store.NewInMemory()
	app := newAppWithDeps(Config{
		CurrentVersion: "",
	}, mem, newTestSyncClient(nil), nil, nil, nil)
	updater := &testUpdater{}
	app.updater = updater
	app.cfg.RuntimePackageSHA256 = "old-sha"

	err := app.handlePendingUpdate(ctx, Snapshot{
		DesiredVersion: "",
		VersionPackage: &model.VersionPackage{
			URL:    "https://example.com/nre-agent",
			SHA256: "new-sha",
		},
	})
	if !errors.Is(err, agentupdate.ErrRestartRequested) {
		t.Fatalf("expected restart request, got %v", err)
	}
	if len(updater.calls) != 1 {
		t.Fatalf("expected one updater call, got %d", len(updater.calls))
	}
}

func TestHandlePendingUpdateSkipsSHAFallbackWhenSHAAlreadyMatches(t *testing.T) {
	ctx := context.Background()
	mem := store.NewInMemory()
	app := newAppWithDeps(Config{
		CurrentVersion: "",
	}, mem, newTestSyncClient(nil), nil, nil, nil)
	updater := &testUpdater{}
	app.updater = updater
	app.cfg.RuntimePackageSHA256 = "same-sha"

	err := app.handlePendingUpdate(ctx, Snapshot{
		DesiredVersion: "",
		VersionPackage: &model.VersionPackage{
			URL:    "https://example.com/nre-agent",
			SHA256: "same-sha",
		},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(updater.calls) != 0 {
		t.Fatalf("expected no updater call, got %d", len(updater.calls))
	}
}
```

- [ ] **Step 2: Run the focused agent tests and verify they fail**

Run:

```powershell
go test ./internal/app -run "TestHandlePendingUpdateUsesSHAFallbackWhenDesiredVersionEmpty|TestHandlePendingUpdateSkipsSHAFallbackWhenSHAAlreadyMatches"
```

Expected:

```text
FAIL
... app.cfg.RuntimePackageSHA256 undefined ...
```

- [ ] **Step 3: Add runtime package metadata types and heartbeat payload fields**

Extend `go-agent/internal/model/version.go`:

```go
type RuntimePackage struct {
	Version string `json:"version,omitempty"`
	Platform string `json:"platform,omitempty"`
	Arch string `json:"arch,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
}
```

Update the heartbeat payload in `go-agent/internal/sync/client.go`:

```go
type ClientConfig struct {
	MasterURL          string
	AgentToken         string
	AgentID            string
	AgentName          string
	CurrentVersion     string
	Platform           string
	RuntimePackageMeta model.RuntimePackage
}
```

and send it:

```go
		RuntimePackage model.RuntimePackage `json:"runtime_package"`
	}{
		Name:           c.cfg.AgentName,
		AgentID:        c.cfg.AgentID,
		Version:        c.cfg.CurrentVersion,
		Platform:       c.cfg.Platform,
		RuntimePackage: c.cfg.RuntimePackageMeta,
	}
```

- [ ] **Step 4: Implement SHA fallback in update decision**

In `go-agent/internal/app/app.go`, change `handlePendingUpdate`:

```go
func (a *App) handlePendingUpdate(ctx context.Context, snapshot Snapshot) error {
	if !agentupdate.HasValidPackage(snapshot.VersionPackage) {
		return nil
	}
	shouldUpdate := false
	if snapshot.DesiredVersion != "" {
		shouldUpdate = agentupdate.NeedsUpdate(a.cfg.CurrentVersion, snapshot.DesiredVersion)
	} else {
		desiredSHA := strings.TrimSpace(snapshot.VersionPackage.SHA256)
		currentSHA := strings.TrimSpace(a.cfg.RuntimePackageSHA256)
		shouldUpdate = desiredSHA != "" && !strings.EqualFold(currentSHA, desiredSHA)
	}
	if !shouldUpdate {
		return nil
	}
	if a.updater == nil {
		return a.recordRuntimeError(errors.New("updater unavailable"))
	}
	stagedPath, err := a.updater.Stage(ctx, *snapshot.VersionPackage)
	if err != nil {
		return a.recordRuntimeError(err)
	}
	if err := a.updater.Activate(stagedPath, snapshot.DesiredVersion); err != nil {
		if errors.Is(err, agentupdate.ErrRestartRequested) {
			return err
		}
		return a.recordRuntimeError(err)
	}
	return agentupdate.ErrRestartRequested
}
```

Also add `RuntimePackageSHA256 string` to the agent config and wire it into `NewClient(...)`.

- [ ] **Step 5: Run the focused agent tests and verify they pass**

Run:

```powershell
go test ./internal/app -run "TestHandlePendingUpdateUsesSHAFallbackWhenDesiredVersionEmpty|TestHandlePendingUpdateSkipsSHAFallbackWhenSHAAlreadyMatches"
```

Expected:

```text
ok  	github.com/sakullla/nginx-reverse-emby/go-agent/internal/app	...
```

- [ ] **Step 6: Commit the agent SHA-fallback slice**

Run:

```bash
git add go-agent/internal/model/version.go go-agent/internal/sync/client.go go-agent/internal/app/app.go go-agent/internal/app/app_test.go
git commit -m "feat(agent): support sha-based package update fallback"
```

### Task 3: Persist runtime package metadata and derive package state in the backend

**Files:**
- Modify: `panel/backend-go/internal/controlplane/storage/sqlite_models.go`
- Modify: `panel/backend-go/internal/controlplane/storage/schema.go`
- Modify: `panel/backend-go/internal/controlplane/service/agents.go`
- Modify: `panel/backend-go/internal/controlplane/service/system.go`
- Test: `panel/backend-go/internal/controlplane/service/agents_test.go`
- Test: `panel/backend-go/internal/controlplane/http/router_test.go`

- [ ] **Step 1: Write the failing backend service tests**

Add a heartbeat persistence test in `panel/backend-go/internal/controlplane/service/agents_test.go`:

```go
func TestAgentServiceHeartbeatPersistsRuntimePackageMetadata(t *testing.T) {
	store := &fakeStore{
		agents: []storage.AgentRow{{
			ID:         "edge-1",
			Name:       "edge-1",
			AgentToken: "agent-token",
			Platform:   "linux-amd64",
		}},
	}
	svc := NewAgentService(config.Config{}, store)

	_, err := svc.Heartbeat(context.Background(), HeartbeatRequest{
		Version:  "1.2.3",
		Platform: "linux-amd64",
		RuntimePackage: RuntimePackageInfo{
			Version:  "1.2.3",
			Platform: "linux",
			Arch:     "amd64",
			SHA256:   "runtime-sha",
		},
	}, "agent-token")
	if err != nil {
		t.Fatalf("Heartbeat() error = %v", err)
	}
	if store.savedAgent.RuntimePackageSHA256 != "runtime-sha" {
		t.Fatalf("saved runtime sha = %q", store.savedAgent.RuntimePackageSHA256)
	}
	if store.savedAgent.RuntimePackageArch != "amd64" {
		t.Fatalf("saved runtime arch = %q", store.savedAgent.RuntimePackageArch)
	}
}
```

Add a router test in `panel/backend-go/internal/controlplane/http/router_test.go`:

```go
	if payload["runtime_package_sha256"] != "runtime-sha" {
		t.Fatalf("runtime_package_sha256 = %v", payload["runtime_package_sha256"])
	}
	if payload["desired_package_sha256"] != "desired-sha" {
		t.Fatalf("desired_package_sha256 = %v", payload["desired_package_sha256"])
	}
	if payload["package_sync_status"] != "pending" {
		t.Fatalf("package_sync_status = %v", payload["package_sync_status"])
	}
```

- [ ] **Step 2: Run the focused backend tests and verify they fail**

Run:

```powershell
go test ./internal/controlplane/service -run TestAgentServiceHeartbeatPersistsRuntimePackageMetadata
go test ./internal/controlplane/http -run TestRouterServesPanelAuthAndInfoEndpoints
```

Expected:

```text
FAIL
... RuntimePackage undefined ...
... runtime_package_sha256 missing ...
```

- [ ] **Step 3: Add storage fields and service types for runtime package metadata**

Extend `panel/backend-go/internal/controlplane/storage/sqlite_models.go`:

```go
	RuntimePackageVersion string `gorm:"column:runtime_package_version"`
	RuntimePackagePlatform string `gorm:"column:runtime_package_platform"`
	RuntimePackageArch string `gorm:"column:runtime_package_arch"`
	RuntimePackageSHA256 string `gorm:"column:runtime_package_sha256"`
```

Add backward-compatible schema updates in `panel/backend-go/internal/controlplane/storage/schema.go` similar to the existing normalizers:

```go
		`ALTER TABLE agents ADD COLUMN runtime_package_version TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE agents ADD COLUMN runtime_package_platform TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE agents ADD COLUMN runtime_package_arch TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE agents ADD COLUMN runtime_package_sha256 TEXT NOT NULL DEFAULT ''`,
```

In `panel/backend-go/internal/controlplane/service/agents.go`, add:

```go
type RuntimePackageInfo struct {
	Version string `json:"version"`
	Platform string `json:"platform"`
	Arch string `json:"arch"`
	SHA256 string `json:"sha256"`
}
```

and include it on heartbeat request plus summaries/system info payload structs.

- [ ] **Step 4: Persist heartbeat metadata and derive desired package summary**

In `HeartbeatRequest`:

```go
	RuntimePackage RuntimePackageInfo `json:"runtime_package"`
```

In `Heartbeat(...)` persist it:

```go
	row.RuntimePackageVersion = strings.TrimSpace(request.RuntimePackage.Version)
	row.RuntimePackagePlatform = strings.TrimSpace(request.RuntimePackage.Platform)
	row.RuntimePackageArch = strings.TrimSpace(request.RuntimePackage.Arch)
	row.RuntimePackageSHA256 = strings.TrimSpace(request.RuntimePackage.SHA256)
```

Add a helper in `agents.go` to resolve package state for an agent:

```go
func derivePackageSyncStatus(row storage.AgentRow, pkg *storage.VersionPackage) string {
	if pkg == nil || strings.TrimSpace(pkg.SHA256) == "" {
		return ""
	}
	if strings.EqualFold(strings.TrimSpace(row.RuntimePackageSHA256), strings.TrimSpace(pkg.SHA256)) {
		return "aligned"
	}
	return "pending"
}
```

Use `storage.LoadAgentSnapshot(... Platform: row.Platform ...)` or the existing platform-aware package resolution path to fill:

```go
type AgentSummary struct {
	...
	RuntimePackageVersion string `json:"runtime_package_version"`
	RuntimePackagePlatform string `json:"runtime_package_platform"`
	RuntimePackageArch string `json:"runtime_package_arch"`
	RuntimePackageSHA256 string `json:"runtime_package_sha256"`
	DesiredPackageSHA256 string `json:"desired_package_sha256"`
	PackageSyncStatus string `json:"package_sync_status"`
}
```

Extend `service.SystemInfo` the same way so `/panel-api/info` can return them for the selected node context.

- [ ] **Step 5: Run the focused backend tests and verify they pass**

Run:

```powershell
go test ./internal/controlplane/service -run TestAgentServiceHeartbeatPersistsRuntimePackageMetadata
go test ./internal/controlplane/http -run TestRouterServesPanelAuthAndInfoEndpoints
```

Expected:

```text
ok  	github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service	...
ok  	github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/http	...
```

- [ ] **Step 6: Commit the backend metadata slice**

Run:

```bash
git add panel/backend-go/internal/controlplane/storage/sqlite_models.go panel/backend-go/internal/controlplane/storage/schema.go panel/backend-go/internal/controlplane/service/agents.go panel/backend-go/internal/controlplane/service/system.go panel/backend-go/internal/controlplane/service/agents_test.go panel/backend-go/internal/controlplane/http/router_test.go
git commit -m "feat(backend): expose agent package sync metadata"
```

### Task 4: Render package details inside the existing system info tab

**Files:**
- Modify: `panel/frontend/src/api/index.js`
- Modify: `panel/frontend/src/pages/AgentDetailPage.vue`

- [ ] **Step 1: Add the failing frontend mock shape**

Update the mock agent and system info data in `panel/frontend/src/api/index.js` so the page expects the new fields:

```js
    runtime_package_version: '1.2.3',
    runtime_package_platform: 'linux',
    runtime_package_arch: 'amd64',
    runtime_package_sha256: 'runtime-sha-1234567890',
    desired_package_sha256: 'desired-sha-abcdef1234',
    package_sync_status: 'pending'
```

and in `fetchSystemInfo()` dev mode:

```js
      runtime_package_version: '1.2.3',
      runtime_package_platform: 'linux',
      runtime_package_arch: 'amd64',
      runtime_package_sha256: 'runtime-sha-1234567890',
      desired_package_sha256: 'desired-sha-abcdef1234',
      package_sync_status: 'pending'
```

- [ ] **Step 2: Build the frontend and verify the current page still lacks the new fields**

Run:

```powershell
npm run build
```

Expected:

```text
vite ... build complete
```

Then confirm `AgentDetailPage.vue` still only shows the old rows, which means the render task is still outstanding.

- [ ] **Step 3: Implement system-info rendering inside the existing tab**

Update `panel/frontend/src/pages/AgentDetailPage.vue`:

```vue
          <div class="info-row"><span>版本</span><span>{{ agent.version || agent.runtime_package_version || '—' }}</span></div>
          <div class="info-row"><span>平台</span><span>{{ agent.runtime_package_platform || agent.platform || '—' }}</span></div>
          <div class="info-row"><span>架构</span><span>{{ agent.runtime_package_arch || '—' }}</span></div>
          <div class="info-row"><span>运行包 SHA</span><span :title="agent.runtime_package_sha256 || ''">{{ shortSha(agent.runtime_package_sha256) }}</span></div>
          <div class="info-row"><span>目标包 SHA</span><span :title="agent.desired_package_sha256 || ''">{{ shortSha(agent.desired_package_sha256) }}</span></div>
          <div class="info-row"><span>包状态</span><span>{{ packageStatusLabel(agent.package_sync_status) }}</span></div>
```

Add helpers:

```js
function shortSha(value) {
  const sha = String(value || '').trim()
  if (!sha) return '—'
  return sha.length > 12 ? `${sha.slice(0, 12)}...` : sha
}

function packageStatusLabel(status) {
  if (status === 'aligned') return '已同步'
  if (status === 'pending') return '待更新'
  return '—'
}
```

- [ ] **Step 4: Run the frontend build and verify it passes**

Run:

```powershell
npm run build
```

Expected:

```text
vite ... build complete
```

- [ ] **Step 5: Commit the frontend slice**

Run:

```bash
git add panel/frontend/src/api/index.js panel/frontend/src/pages/AgentDetailPage.vue
git commit -m "feat(panel): show agent package metadata in system info"
```

### Task 5: Full verification

**Files:**
- Modify: none
- Test: `panel/backend-go/internal/controlplane/http/public_test.go`
- Test: `go-agent/internal/app/app_test.go`
- Test: `panel/backend-go/internal/controlplane/service/agents_test.go`
- Test: `panel/backend-go/internal/controlplane/http/router_test.go`

- [ ] **Step 1: Run the full backend test suite**

Run:

```powershell
go test ./...
```

Expected:

```text
ok
```

- [ ] **Step 2: Run the full agent test suite**

Run:

```powershell
go test ./...
```

Expected:

```text
ok
```

- [ ] **Step 3: Run the frontend production build**

Run:

```powershell
npm run build
```

Expected:

```text
vite ... build complete
```

- [ ] **Step 4: Create the final verification commit if any fixes were needed during suite runs**

Run:

```bash
git status --short
```

Expected:

```text
```

If verification fixes were required:

```bash
git add <fixed-files>
git commit -m "fix: address verification regressions"
```

## Self-Review Checklist

- Spec coverage:
  - safe systemd rerun flow is covered by Task 1
  - SHA fallback update logic is covered by Task 2
  - backend persistence and system-info derivation are covered by Task 3
  - frontend rendering inside existing system info is covered by Task 4
  - verification commands from the spec are covered by Task 5
- Placeholder scan:
  - no `TODO`, `TBD`, or deferred “implement later” language remains
  - each task names exact files and concrete commands
- Type consistency:
  - runtime package naming is consistent as `RuntimePackage` on agent side and `RuntimePackageInfo` on backend side
  - package state naming is consistent as `aligned` / `pending`
