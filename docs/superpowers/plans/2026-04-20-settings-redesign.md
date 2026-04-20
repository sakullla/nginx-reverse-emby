# Settings Page Redesign — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Redesign the settings page from a single-page scroll to a left-sidebar tab navigation layout with enhanced data management (selective export, import preview) and a rich About page.

**Architecture:** Split `SettingsPage.vue` into a parent shell + 3 child tab components. Backend extends `GET /info` with build/runtime metadata and adds selective export + import preview endpoints.

**Tech Stack:** Go 1.23, Vue 3 Composition API, existing CSS variable theme system

---

## File Structure

### Frontend (create)
- `panel/frontend/src/components/settings/SettingsNav.vue` — left sidebar navigation
- `panel/frontend/src/components/settings/SettingsGeneral.vue` — theme + deploy mode
- `panel/frontend/src/components/settings/SettingsDataMgmt.vue` — export/import with selective export and 3-step import
- `panel/frontend/src/components/settings/SettingsAbout.vue` — version, links, system status

### Frontend (modify)
- `panel/frontend/src/pages/SettingsPage.vue` — replace single-page content with tab shell
- `panel/frontend/src/api/runtime.js` — add `exportBackupSelective`, `importBackupPreview`, `fetchExtendedSystemInfo`
- `panel/frontend/src/api/index.js` — add new API exports
- `panel/frontend/src/api/devMocks/data.js` — add mock implementations

### Backend (modify)
- `panel/backend-go/internal/controlplane/service/system.go` — extend `SystemInfo` with build/runtime fields, add `AgentService` dependency for counts
- `panel/backend-go/internal/controlplane/http/handlers_info.go` — include new fields in `/info` response
- `panel/backend-go/internal/controlplane/service/backup.go` — add `ExportSelective` and `Preview` methods
- `panel/backend-go/internal/controlplane/http/handlers_backup.go` — add handlers for new endpoints
- `panel/backend-go/internal/controlplane/http/router.go` — register new routes, extend `BackupService` interface
- `panel/backend-go/internal/controlplane/config/config.go` — add `ProjectURL`, `AppVersion`, `BuildTime`, `GoVersion` fields

---

### Task 1: Extend backend config with build info and project URL

**Files:**
- Modify: `panel/backend-go/internal/controlplane/config/config.go`

- [ ] **Step 1: Add build info and project URL fields to Config struct**

In `panel/backend-go/internal/controlplane/config/config.go`, add these fields to the `Config` struct (after the `ManagedDNSCertificatesEnabled` field):

```go
AppVersion  string
BuildTime   string
GoVersion   string
ProjectURL  string
```

In `LoadFromEnv()`, add env var loading for `ProjectURL`:

```go
cfg.ProjectURL = strings.TrimSpace(os.Getenv("NRE_PROJECT_URL"))
```

The `AppVersion`, `BuildTime`, and `GoVersion` fields will be injected at build time. In `LoadFromEnv()`, set defaults so they work without ldflags:

```go
if cfg.AppVersion == "" {
    cfg.AppVersion = "dev"
}
if cfg.BuildTime == "" {
    cfg.BuildTime = time.Now().UTC().Format(time.RFC3339)
}
if cfg.GoVersion == "" {
    cfg.GoVersion = "dev"
}
```

The actual ldflags injection goes in the Dockerfile / build script. Add a comment:

```go
// AppVersion, BuildTime, and GoVersion are set via -ldflags at build time.
// Example: go build -ldflags "-X main.appVersion=1.0.0" ./cmd/nre-control-plane
```

Wait — the ldflags target needs to be a package-level var in `main.go`. Add to `panel/backend-go/cmd/nre-control-plane/main.go`:

```go
var (
    appVersion = "dev"
    buildTime  = "dev"
    goVersion  = "dev"
)
```

Then in `main()`, after `config.LoadFromEnv()`, inject them:

```go
cfg.AppVersion = appVersion
cfg.BuildTime = buildTime
cfg.GoVersion = goVersion
```

- [ ] **Step 2: Run backend tests**

Run: `cd panel/backend-go && go test ./...`
Expected: All existing tests pass (no behavior change yet).

- [ ] **Step 3: Commit**

```bash
git add panel/backend-go/internal/controlplane/config/config.go panel/backend-go/cmd/nre-control-plane/main.go
git commit -m "feat(backend): add build info and project URL config fields"
```

---

### Task 2: Extend SystemInfo with build info and runtime status

**Files:**
- Modify: `panel/backend-go/internal/controlplane/service/system.go`
- Modify: `panel/backend-go/internal/controlplane/http/handlers_info.go`

- [ ] **Step 1: Extend SystemInfo struct**

In `panel/backend-go/internal/controlplane/service/system.go`, extend `SystemInfo`:

```go
type SystemInfo struct {
    Role                         string
    LocalApplyRuntime            string
    DefaultAgentID               string
    LocalAgentEnabled            bool
    ProxyHeadersGloballyDisabled bool
    AppVersion                   string
    BuildTime                    string
    GoVersion                    string
    ProjectURL                   string
    DataDir                      string
    StartedAt                    time.Time
    OnlineAgents                 int
    TotalAgents                  int
}
```

Add a `systemStore` interface dependency to `systemService` for agent counts:

```go
type systemStore interface {
    ListAgents(ctx context.Context) ([]storage.AgentRow, error)
}

type systemService struct {
    cfg   config.Config
    store systemStore
    startedAt time.Time
}
```

Update `NewSystemService` to accept an optional store:

```go
func NewSystemService(cfg config.Config, store ...systemStore) systemService {
    svc := systemService{
        cfg:       cfg,
        startedAt: time.Now(),
    }
    if len(store) > 0 {
        svc.store = store[0]
    }
    return svc
}
```

Update `Info()` to populate all fields:

```go
func (s systemService) Info(ctx context.Context) SystemInfo {
    defaultAgentID := ""
    if s.cfg.EnableLocalAgent {
        defaultAgentID = s.cfg.LocalAgentID
    }

    info := SystemInfo{
        Role:                         "master",
        LocalApplyRuntime:            "go-agent",
        DefaultAgentID:               defaultAgentID,
        LocalAgentEnabled:            s.cfg.EnableLocalAgent,
        ProxyHeadersGloballyDisabled: false,
        AppVersion:                   s.cfg.AppVersion,
        BuildTime:                    s.cfg.BuildTime,
        GoVersion:                    s.cfg.GoVersion,
        ProjectURL:                   s.cfg.ProjectURL,
        DataDir:                      s.cfg.DataDir,
        StartedAt:                    s.startedAt,
    }

    if s.store != nil {
        agents, err := s.store.ListAgents(ctx)
        if err == nil {
            info.TotalAgents = len(agents)
            onlineThreshold := time.Now().Add(-s.cfg.HeartbeatInterval * 2)
            for _, a := range agents {
                if !a.LastSeen.IsZero() && a.LastSeen.After(onlineThreshold) {
                    info.OnlineAgents++
                }
            }
        }
    }

    return info
}
```

- [ ] **Step 2: Update Info() signature to accept context**

The current `Info` method takes only `context.Context`. It already does. No signature change needed.

- [ ] **Step 3: Update handlers_info.go to include new fields**

In `panel/backend-go/internal/controlplane/http/handlers_info.go`, add the new fields to the payload map:

```go
func (d Dependencies) handleInfo(w http.ResponseWriter, r *http.Request) {
    info := d.SystemService.Info(r.Context())
    payload := map[string]any{
        "ok":                              true,
        "role":                            info.Role,
        "local_apply_runtime":             info.LocalApplyRuntime,
        "default_agent_id":                info.DefaultAgentID,
        "local_agent_enabled":             info.LocalAgentEnabled,
        "proxy_headers_globally_disabled": info.ProxyHeadersGloballyDisabled,
        "app_version":                     info.AppVersion,
        "build_time":                      info.BuildTime,
        "go_version":                      info.GoVersion,
        "project_url":                     info.ProjectURL,
        "data_dir":                        info.DataDir,
        "started_at":                      info.StartedAt.Format(time.RFC3339),
        "online_agents":                   info.OnlineAgents,
        "total_agents":                    info.TotalAgents,
    }
    if d.isPanelAuthorized(r) && d.Config.RegisterToken != "" {
        payload["master_register_token"] = d.Config.RegisterToken
    }
    writeJSON(w, http.StatusOK, payload)
}
```

- [ ] **Step 4: Wire store into SystemService**

In `panel/backend-go/cmd/nre-control-plane/main.go`, update the two places `NewSystemService` is called:

In `newControlPlaneApp`:
```go
systemSvc := service.NewSystemService(cfg, serviceStore)
```

- [ ] **Step 5: Fix tests — update NewSystemService callers**

In `panel/backend-go/internal/controlplane/http/router.go`, update `withDefaults()`:
```go
d.SystemService = service.NewSystemService(d.Config)
```
This stays as-is (no store), so the info endpoint just returns zero agent counts in the no-store path.

Search for all `NewSystemService` calls in test files and update accordingly. Since we made store a variadic parameter, existing calls without a store still compile.

- [ ] **Step 6: Run tests**

Run: `cd panel/backend-go && go test ./...`
Expected: All tests pass.

- [ ] **Step 7: Commit**

```bash
git add panel/backend-go/internal/controlplane/service/system.go panel/backend-go/internal/controlplane/http/handlers_info.go panel/backend-go/cmd/nre-control-plane/main.go
git commit -m "feat(backend): extend /info with build info, project URL, and runtime status"
```

---

### Task 3: Add selective export to backend

**Files:**
- Modify: `panel/backend-go/internal/controlplane/service/backup.go`
- Modify: `panel/backend-go/internal/controlplane/http/handlers_backup.go`
- Modify: `panel/backend-go/internal/controlplane/http/router.go`

- [ ] **Step 1: Add ExportSelective method to backup service**

In `panel/backend-go/internal/controlplane/service/backup.go`, add:

```go
type BackupExportOptions struct {
    Agents          bool `json:"agents"`
    HTTPRules       bool `json:"http_rules"`
    L4Rules         bool `json:"l4_rules"`
    RelayListeners  bool `json:"relay_listeners"`
    Certificates    bool `json:"certificates"`
    VersionPolicies bool `json:"version_policies"`
}

func AllExportOptions() BackupExportOptions {
    return BackupExportOptions{
        Agents:          true,
        HTTPRules:       true,
        L4Rules:         true,
        RelayListeners:  true,
        Certificates:    true,
        VersionPolicies: true,
    }
}
```

Add the `ExportSelective` method:

```go
func (s *backupService) ExportSelective(ctx context.Context, opts BackupExportOptions) ([]byte, string, error) {
    bundle, err := s.exportBundle(ctx)
    if err != nil {
        return nil, "", err
    }

    if !opts.Agents {
        bundle.Agents = nil
    }
    if !opts.HTTPRules {
        bundle.HTTPRules = nil
    }
    if !opts.L4Rules {
        bundle.L4Rules = nil
    }
    if !opts.RelayListeners {
        bundle.RelayListeners = nil
    }
    if !opts.Certificates {
        bundle.Certificates = nil
        bundle.Materials = nil
    }
    if !opts.VersionPolicies {
        bundle.VersionPolicies = nil
    }

    bundle.Manifest.Counts = BackupCounts{
        Agents:          len(bundle.Agents),
        HTTPRules:       len(bundle.HTTPRules),
        L4Rules:         len(bundle.L4Rules),
        RelayListeners:  len(bundle.RelayListeners),
        Certificates:    len(bundle.Certificates),
        VersionPolicies: len(bundle.VersionPolicies),
    }
    bundle.Manifest.IncludesCertificates = len(bundle.Materials) > 0

    archive, err := encodeBackupBundle(bundle)
    if err != nil {
        return nil, "", err
    }
    filename := fmt.Sprintf("nre-backup-%s.tar.gz", bundle.Manifest.ExportedAt.UTC().Format("20060102T150405Z"))
    return archive, filename, nil
}
```

- [ ] **Step 2: Add BackupCounts endpoint for resource counts**

Add a method to return current resource counts for the frontend checklist:

```go
func (s *backupService) ResourceCounts(ctx context.Context) (BackupCounts, error) {
    bundle, err := s.exportBundle(ctx)
    if err != nil {
        return BackupCounts{}, err
    }
    return bundle.Manifest.Counts, nil
}
```

- [ ] **Step 3: Update BackupService interface and handler**

In `panel/backend-go/internal/controlplane/http/router.go`, extend the `BackupService` interface:

```go
type BackupService interface {
    Export(context.Context) ([]byte, string, error)
    ExportSelective(context.Context, service.BackupExportOptions) ([]byte, string, error)
    Import(context.Context, []byte) (service.BackupImportResult, error)
    ResourceCounts(context.Context) (service.BackupCounts, error)
    Preview(context.Context, []byte) (service.BackupImportResult, error)
}
```

Update `unavailableBackupService` to satisfy the interface:

```go
func (unavailableBackupService) ExportSelective(context.Context, service.BackupExportOptions) ([]byte, string, error) {
    return nil, "", fmt.Errorf("backup service unavailable")
}
func (unavailableBackupService) ResourceCounts(context.Context) (service.BackupCounts, error) {
    return service.BackupCounts{}, fmt.Errorf("backup service unavailable")
}
func (unavailableBackupService) Preview(context.Context, []byte) (service.BackupImportResult, error) {
    return service.BackupImportResult{}, fmt.Errorf("backup service unavailable")
}
```

- [ ] **Step 4: Add handler for selective export and resource counts**

In `panel/backend-go/internal/controlplane/http/handlers_backup.go`, update `handleBackupExport` to parse `include` query param:

```go
func (d Dependencies) handleBackupExport(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodGet {
        http.NotFound(w, r)
        return
    }

    includeParam := r.URL.Query().Get("include")
    var body []byte
    var filename string
    var err error

    if includeParam != "" {
        opts := parseExportOptions(includeParam)
        body, filename, err = d.BackupService.ExportSelective(r.Context(), opts)
    } else {
        body, filename, err = d.BackupService.Export(r.Context())
    }

    if err != nil {
        status, payload := mapServiceError(err)
        writeJSON(w, status, payload)
        return
    }
    w.Header().Set("Content-Type", "application/gzip")
    w.Header().Set("Content-Disposition", backupContentDisposition(filename))
    w.WriteHeader(http.StatusOK)
    _, _ = w.Write(body)
}

func parseExportOptions(include string) service.BackupExportOptions {
    parts := map[string]bool{}
    for _, p := range strings.Split(include, ",") {
        parts[strings.TrimSpace(p)] = true
    }
    return service.BackupExportOptions{
        Agents:          parts["agents"],
        HTTPRules:       parts["http_rules"],
        L4Rules:         parts["l4_rules"],
        RelayListeners:  parts["relay_listeners"],
        Certificates:    parts["certificates"],
        VersionPolicies: parts["version_policies"],
    }
}

func (d Dependencies) handleBackupResourceCounts(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodGet {
        http.NotFound(w, r)
        return
    }
    counts, err := d.BackupService.ResourceCounts(r.Context())
    if err != nil {
        status, payload := mapServiceError(err)
        writeJSON(w, status, payload)
        return
    }
    writeJSON(w, http.StatusOK, map[string]any{
        "ok":    true,
        "counts": counts,
    })
}
```

Add `strings` to imports in `handlers_backup.go` if not already present.

- [ ] **Step 5: Register new route**

In `panel/backend-go/internal/controlplane/http/router.go`, inside the `for _, prefix := range []string{"/panel-api", "/api"}` loop, add:

```go
mux.Handle(prefix+"/system/backup/counts", resolved.requirePanelToken(http.HandlerFunc(resolved.handleBackupResourceCounts)))
```

- [ ] **Step 6: Run tests**

Run: `cd panel/backend-go && go test ./...`
Expected: All tests pass. Fix any compilation errors from the interface change.

- [ ] **Step 7: Commit**

```bash
git add panel/backend-go/internal/controlplane/service/backup.go panel/backend-go/internal/controlplane/http/handlers_backup.go panel/backend-go/internal/controlplane/http/router.go
git commit -m "feat(backend): add selective export and resource counts endpoint"
```

---

### Task 4: Add import preview to backend

**Files:**
- Modify: `panel/backend-go/internal/controlplane/service/backup.go`
- Modify: `panel/backend-go/internal/controlplane/http/handlers_backup.go`
- Modify: `panel/backend-go/internal/controlplane/http/router.go`

- [ ] **Step 1: Add Preview method to backup service**

In `panel/backend-go/internal/controlplane/service/backup.go`, add:

```go
func (s *backupService) Preview(ctx context.Context, archive []byte) (BackupImportResult, error) {
    bundle, err := decodeBackupBundle(archive)
    if err != nil {
        return BackupImportResult{}, err
    }
    if bundle.Manifest.PackageVersion != BackupPackageVersion {
        return BackupImportResult{}, fmt.Errorf("%w: unsupported backup package version %d", ErrInvalidArgument, bundle.Manifest.PackageVersion)
    }

    result, err := s.previewBundle(ctx, bundle)
    if err != nil {
        return BackupImportResult{}, err
    }
    return result, nil
}
```

The `previewBundle` method reuses existing conflict detection logic without writing to the database. Extract the conflict-detection portion from `importBundle` into a shared helper. The simplest approach is to call `importBundle` with a dry-run store that records but doesn't commit. But since the existing import logic writes directly, we'll create a lighter preview:

```go
func (s *backupService) previewBundle(ctx context.Context, bundle BackupBundle) (BackupImportResult, error) {
    result := newBackupImportResult(bundle.Manifest)

    agentRows, err := s.store.ListAgents(ctx)
    if err != nil {
        return BackupImportResult{}, err
    }

    agentIDMap, _ := s.previewAgents(agentRows, bundle.Agents, &result)

    certRows, err := s.store.ListManagedCertificates(ctx)
    if err != nil {
        return BackupImportResult{}, err
    }

    certIDMap, _ := s.previewCertificates(certRows, bundle.Certificates, &result)

    listenerRows, err := s.store.ListRelayListeners(ctx, "")
    if err != nil {
        return BackupImportResult{}, err
    }

    listenerIDMap, _ := s.previewRelayListeners(listenerRows, bundle.RelayListeners, &result)

    _ = s.previewVersionPolicies(bundle.VersionPolicies, &result)

    _ = s.previewHTTPRules(bundle.HTTPRules, agentIDMap, listenerIDMap, &result)

    _ = s.previewL4Rules(bundle.L4Rules, agentIDMap, listenerIDMap, &result)

    return result, nil
}
```

Each `preview*` method mirrors the conflict detection from its `import*` counterpart but skips database writes. The key conflict checks are:
- Agent: name or ID already exists → `skipped_conflict`
- Certificate: domain already exists → `skipped_conflict`
- RelayListener: same agent + listen address → `skipped_conflict`
- VersionPolicy: same name → `skipped_conflict`
- HTTP Rule: same agent + frontend_url → `skipped_conflict`
- L4 Rule: same agent + protocol + port → `skipped_conflict`
- Invalid references (unknown agent, unknown cert, unknown listener) → `skipped_invalid`
- Missing certificate material → `skipped_missing_material`

Implement each preview method by copying the relevant portion of the corresponding `import*` method but replacing the `s.store.Save*` calls with no-ops. For brevity, here's `previewAgents` as a template:

```go
func (s *backupService) previewAgents(existing []storage.AgentRow, incoming []BackupAgent, result *BackupImportResult) (map[string]string, map[string]string) {
    existingByName := map[string]bool{}
    existingByID := map[string]bool{}
    for _, a := range existing {
        existingByName[a.Name] = true
        existingByID[a.ID] = true
    }

    agentIDMap := map[string]string{}
    for _, a := range incoming {
        if existingByName[a.Name] || existingByID[a.ID] {
            result.Report.SkippedConflict = append(result.Report.SkippedConflict, BackupImportItem{
                Kind: "agent", Key: a.Name, Reason: "agent name or ID already exists",
            })
            result.Summary.SkippedConflict.Agents++
            continue
        }
        result.Report.Imported = append(result.Report.Imported, BackupImportItem{
            Kind: "agent", Key: a.Name,
        })
        result.Summary.Imported.Agents++
    }
    return agentIDMap, nil
}
```

Follow the same pattern for the other preview methods, mirroring the conflict checks from `importCertificates`, `importRelayListeners`, `importVersionPolicies`, `importHTTPRules`, and `importL4Rules`.

- [ ] **Step 2: Add handler and route for preview**

In `panel/backend-go/internal/controlplane/http/handlers_backup.go`, add:

```go
func (d Dependencies) handleBackupImportPreview(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodPost {
        http.NotFound(w, r)
        return
    }
    r.Body = http.MaxBytesReader(w, r.Body, backupImportMaxBytes)
    file, _, err := r.FormFile("file")
    if err != nil {
        if isBackupImportTooLarge(err) {
            writeJSON(w, http.StatusRequestEntityTooLarge, errorPayload("backup file too large"))
            return
        }
        writeJSON(w, http.StatusBadRequest, errorPayload("missing backup file"))
        return
    }
    defer file.Close()

    body, err := io.ReadAll(file)
    if err != nil {
        if isBackupImportTooLarge(err) {
            writeJSON(w, http.StatusRequestEntityTooLarge, errorPayload("backup file too large"))
            return
        }
        writeJSON(w, http.StatusBadRequest, errorPayload("failed to read backup file"))
        return
    }

    result, err := d.BackupService.Preview(r.Context(), body)
    if err != nil {
        status, payload := mapServiceError(err)
        writeJSON(w, status, payload)
        return
    }
    writeJSON(w, http.StatusOK, map[string]any{
        "ok":       true,
        "manifest": result.Manifest,
        "summary":  result.Summary,
        "report":   result.Report,
    })
}
```

In `panel/backend-go/internal/controlplane/http/router.go`, add route:

```go
mux.Handle(prefix+"/system/backup/import/preview", resolved.requirePanelToken(http.HandlerFunc(resolved.handleBackupImportPreview)))
```

- [ ] **Step 3: Run tests**

Run: `cd panel/backend-go && go test ./...`
Expected: All tests pass.

- [ ] **Step 4: Commit**

```bash
git add panel/backend-go/internal/controlplane/service/backup.go panel/backend-go/internal/controlplane/http/handlers_backup.go panel/backend-go/internal/controlplane/http/router.go
git commit -m "feat(backend): add import preview endpoint"
```

---

### Task 5: Add new frontend API functions and mocks

**Files:**
- Modify: `panel/frontend/src/api/runtime.js`
- Modify: `panel/frontend/src/api/index.js`
- Modify: `panel/frontend/src/api/devMocks/data.js`

- [ ] **Step 1: Add API functions to runtime.js**

In `panel/frontend/src/api/runtime.js`, add:

```javascript
export async function fetchExtendedSystemInfo() {
  const { data } = await api.get('/info')
  return data
}

export async function exportBackupSelective(include) {
  const params = new URLSearchParams()
  params.set('include', include.join(','))
  const response = await api.get(`/system/backup/export?${params.toString()}`, {
    responseType: 'blob',
    timeout: 0
  })
  return {
    blob: response.data,
    filename: parseDownloadFilename(response.headers['content-disposition'])
  }
}

export async function importBackupPreview(file) {
  const formData = new FormData()
  formData.append('file', file)
  const { data } = await api.post('/system/backup/import/preview', formData, {
    timeout: 0
  })
  return data
}

export async function fetchBackupResourceCounts() {
  const { data } = await api.get('/system/backup/counts')
  return data
}
```

Note: `fetchExtendedSystemInfo` is a rename for clarity; existing `fetchSystemInfo` continues to work. We can either reuse `fetchSystemInfo` or add the new one. Since `/info` now returns more fields, the existing `fetchSystemInfo` already gets them — no new endpoint needed. So skip `fetchExtendedSystemInfo` and just use the existing `fetchSystemInfo`.

Remove `fetchExtendedSystemInfo` and just add the other three.

- [ ] **Step 2: Add exports to index.js**

In `panel/frontend/src/api/index.js`, add:

```javascript
export const exportBackupSelective = (...args) => call('exportBackupSelective', ...args)
export const importBackupPreview = (...args) => call('importBackupPreview', ...args)
export const fetchBackupResourceCounts = (...args) => call('fetchBackupResourceCounts', ...args)
```

- [ ] **Step 3: Add mock implementations to data.js**

In `panel/frontend/src/api/devMocks/data.js`, add:

```javascript
export async function exportBackupSelective(include) {
  if (isDev) {
    await sleep()
    return exportBackup()
  }
  // ... prod handled by runtime.js
}

export async function importBackupPreview(file) {
  if (isDev) {
    await sleep(600)
    return importBackup(file)
  }
}

export async function fetchBackupResourceCounts() {
  if (isDev) {
    await sleep(200)
    return {
      ok: true,
      counts: {
        agents: 3,
        http_rules: 12,
        l4_rules: 4,
        relay_listeners: 2,
        certificates: 5,
        version_policies: 1
      }
    }
  }
}
```

- [ ] **Step 4: Commit**

```bash
git add panel/frontend/src/api/runtime.js panel/frontend/src/api/index.js panel/frontend/src/api/devMocks/data.js
git commit -m "feat(frontend): add selective export, import preview, and resource counts API"
```

---

### Task 6: Create SettingsNav component

**Files:**
- Create: `panel/frontend/src/components/settings/SettingsNav.vue`

- [ ] **Step 1: Create SettingsNav.vue**

```vue
<template>
  <nav class="settings-nav">
    <div class="settings-nav__label">设置</div>
    <button
      v-for="tab in tabs"
      :key="tab.id"
      class="settings-nav__item"
      :class="{ active: activeTab === tab.id }"
      @click="$emit('update:activeTab', tab.id)"
    >
      <span class="settings-nav__icon">{{ tab.icon }}</span>
      <span class="settings-nav__text">{{ tab.label }}</span>
    </button>
  </nav>
</template>

<script setup>
defineProps({
  activeTab: { type: String, required: true }
})

defineEmits(['update:activeTab'])

const tabs = [
  { id: 'general', icon: '⚙️', label: '通用' },
  { id: 'data', icon: '💾', label: '数据管理' },
  { id: 'about', icon: 'ℹ️', label: '关于' }
]
</script>

<style scoped>
.settings-nav {
  display: flex;
  flex-direction: column;
  gap: 0;
  padding: 1.5rem 0;
  min-width: 160px;
  flex-shrink: 0;
}
.settings-nav__label {
  padding: 0 1.25rem 1rem;
  font-size: 0.8rem;
  color: var(--color-text-tertiary);
  text-transform: uppercase;
  letter-spacing: 0.05em;
  font-weight: 600;
}
.settings-nav__item {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.6rem 1.25rem;
  border: none;
  background: none;
  cursor: pointer;
  font-size: 0.9rem;
  color: var(--color-text-secondary);
  border-left: 3px solid transparent;
  transition: all 0.15s var(--ease-default);
  width: 100%;
  text-align: left;
}
.settings-nav__item:hover {
  color: var(--color-text-primary);
  background: var(--color-bg-subtle);
}
.settings-nav__item.active {
  color: var(--color-text-primary);
  font-weight: 600;
  border-left-color: var(--color-primary);
  background: var(--color-primary-subtle);
}
.settings-nav__icon { font-size: 1rem; }

@media (max-width: 767px) {
  .settings-nav {
    flex-direction: row;
    padding: 0;
    min-width: unset;
    border-bottom: 1px solid var(--color-border-default);
    overflow-x: auto;
    gap: 0;
  }
  .settings-nav__label { display: none; }
  .settings-nav__item {
    padding: 0.75rem 1.25rem;
    border-left: none;
    border-bottom: 2px solid transparent;
    white-space: nowrap;
  }
  .settings-nav__item.active {
    border-left-color: transparent;
    border-bottom-color: var(--color-primary);
  }
}
</style>
```

- [ ] **Step 2: Commit**

```bash
git add panel/frontend/src/components/settings/SettingsNav.vue
git commit -m "feat(frontend): add SettingsNav sidebar component"
```

---

### Task 7: Create SettingsGeneral component

**Files:**
- Create: `panel/frontend/src/components/settings/SettingsGeneral.vue`

- [ ] **Step 1: Create SettingsGeneral.vue**

Extract the theme and deploy mode sections from `SettingsPage.vue` into a standalone component. This is a direct extraction with no logic changes:

```vue
<template>
  <div class="settings-general">
    <section class="settings-section">
      <div class="settings-section__header">
        <h2 class="settings-section__title">外观主题</h2>
        <p class="settings-section__desc">选择面板的外观风格</p>
      </div>
      <div class="settings-section__body">
        <div class="theme-grid">
          <button
            v-for="theme in themes"
            :key="theme.id"
            class="theme-option"
            :class="{ active: currentTheme === theme.id }"
            @click="setTheme(theme.id)"
          >
            <span class="theme-option__label">{{ theme.label }}</span>
            <svg v-if="currentTheme === theme.id" class="theme-option__check" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3">
              <polyline points="20 6 9 17 4 12"/>
            </svg>
          </button>
        </div>
      </div>
    </section>

    <section class="settings-section">
      <div class="settings-section__header">
        <h2 class="settings-section__title">部署模式</h2>
        <p class="settings-section__desc">当前面板的运行模式</p>
      </div>
      <div class="settings-section__body">
        <div class="info-row">
          <span class="info-label">角色</span>
          <span class="info-value">{{ systemInfo?.role || '—' }}</span>
        </div>
        <div class="info-row">
          <span class="info-label">本地 Agent</span>
          <span class="info-value">{{ systemInfo?.local_agent_enabled ? '已启用' : '未启用' }}</span>
        </div>
      </div>
    </section>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { useTheme } from '../../context/ThemeContext'
import { fetchSystemInfo } from '../../api'

const { currentThemeId: currentTheme, setTheme, themes } = useTheme()
const systemInfo = ref(null)

onMounted(() => {
  fetchSystemInfo().then(i => { systemInfo.value = i }).catch(() => {})
})
</script>

<style scoped>
.settings-general { display: flex; flex-direction: column; gap: 1.25rem; }
.settings-section {
  background: var(--color-bg-surface);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-2xl);
  overflow: hidden;
}
.settings-section__header { padding: 1rem 1.25rem; border-bottom: 1px solid var(--color-border-subtle); }
.settings-section__title { font-size: 1rem; font-weight: 600; margin: 0 0 0.25rem; color: var(--color-text-primary); }
.settings-section__desc { font-size: 0.875rem; color: var(--color-text-tertiary); margin: 0; }
.settings-section__body { padding: 1.25rem; display: flex; flex-direction: column; gap: 1rem; }

.theme-grid { display: flex; gap: 0.5rem; flex-wrap: wrap; }
.theme-option {
  display: flex; align-items: center; gap: 0.5rem;
  padding: 0.5rem 0.75rem;
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  background: var(--color-bg-subtle);
  cursor: pointer;
  transition: all 0.2s var(--ease-default);
}
.theme-option:hover { border-color: var(--color-primary); transform: translateY(-1px); }
.theme-option.active {
  border-color: var(--color-primary);
  background: var(--color-primary-subtle);
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--color-primary) 20%, transparent);
  transform: translateY(-1px);
}
.theme-option__label { font-size: 0.875rem; font-weight: 500; color: var(--color-text-primary); }
.theme-option__check { color: var(--color-primary); animation: checkPop 0.3s var(--ease-bounce); }
@keyframes checkPop {
  0% { transform: scale(0); opacity: 0; }
  100% { transform: scale(1); opacity: 1; }
}

.info-row { display: flex; align-items: center; justify-content: space-between; padding: 0.5rem 0; border-bottom: 1px solid var(--color-border-subtle); }
.info-row:last-child { border-bottom: none; }
.info-label { font-size: 0.875rem; color: var(--color-text-secondary); }
.info-value { font-size: 0.875rem; color: var(--color-text-primary); font-weight: 500; }
</style>
```

- [ ] **Step 2: Commit**

```bash
git add panel/frontend/src/components/settings/SettingsGeneral.vue
git commit -m "feat(frontend): add SettingsGeneral component"
```

---

### Task 8: Create SettingsDataMgmt component

**Files:**
- Create: `panel/frontend/src/components/settings/SettingsDataMgmt.vue`

- [ ] **Step 1: Create SettingsDataMgmt.vue**

This is the most complex component. It implements selective export and the 3-step import flow:

```vue
<template>
  <div class="settings-data-mgmt">
    <!-- Export Section -->
    <section class="settings-section">
      <div class="settings-section__header">
        <h2 class="settings-section__title">导出备份</h2>
        <p class="settings-section__desc">选择要导出的资源类型</p>
      </div>
      <div class="settings-section__body">
        <div class="export-checklist">
          <label
            v-for="item in exportItems"
            :key="item.key"
            class="export-checklist__item"
          >
            <input type="checkbox" v-model="exportSelection[item.key]" class="export-checklist__input">
            <span class="export-checklist__label">{{ item.label }}</span>
            <span class="export-checklist__count">{{ counts[item.key] ?? 0 }} 项</span>
          </label>
        </div>
        <button class="action-button" :disabled="exporting || !hasAnySelection" @click="handleExport">
          {{ exporting ? '导出中...' : '导出备份' }}
        </button>
      </div>
    </section>

    <!-- Import Section -->
    <section class="settings-section">
      <div class="settings-section__header">
        <h2 class="settings-section__title">导入备份</h2>
        <p class="settings-section__desc">从备份文件恢复配置</p>
      </div>
      <div class="settings-section__body">
        <!-- Step indicator -->
        <div class="import-steps">
          <span class="import-step" :class="{ active: importStep >= 1, done: importStep > 1 }">1. 选择文件</span>
          <span class="import-step" :class="{ active: importStep >= 2, done: importStep > 2 }">2. 预览确认</span>
          <span class="import-step" :class="{ active: importStep >= 3 }">3. 导入结果</span>
        </div>

        <!-- Step 1: File select -->
        <template v-if="importStep === 1">
          <input ref="fileInputRef" type="file" accept=".tar.gz,.tgz,.gz,application/gzip" class="backup-file-input" @change="handleFileChange">
          <div class="backup-hint">
            <span class="info-label">当前文件</span>
            <span class="info-value">{{ selectedFileName || '未选择备份文件' }}</span>
          </div>
          <div class="import-actions">
            <button class="action-button action-button--secondary" @click="fileInputRef?.click()">选择备份文件</button>
            <button v-if="selectedFileName" class="action-button" :disabled="previewing" @click="handlePreview">
              {{ previewing ? '分析中...' : '预览导入' }}
            </button>
          </div>
        </template>

        <!-- Step 2: Preview -->
        <template v-if="importStep === 2 && previewResult">
          <div class="preview-meta">
            <div class="preview-meta__item">
              <span class="info-label">来源架构</span>
              <span class="info-value">{{ previewResult.manifest?.source_architecture || '—' }}</span>
            </div>
            <div class="preview-meta__item">
              <span class="info-label">导出时间</span>
              <span class="info-value">{{ formatTimestamp(previewResult.manifest?.exported_at) }}</span>
            </div>
          </div>

          <div class="preview-table">
            <div class="preview-table__header">
              <span>资源</span>
              <span>操作</span>
            </div>
            <div v-for="section in previewSections" :key="section.key" class="preview-table__row">
              <span>{{ section.label }} ({{ section.total }})</span>
              <span :class="section.statusClass">{{ section.statusText }}</span>
            </div>
          </div>

          <div class="import-actions">
            <button class="action-button action-button--secondary" @click="resetImport">取消</button>
            <button class="action-button" :disabled="importing" @click="handleConfirmImport">
              {{ importing ? '导入中...' : '确认导入' }}
            </button>
          </div>
        </template>

        <!-- Step 3: Results -->
        <template v-if="importStep === 3 && importResult">
          <div class="backup-report__summary">
            <div class="summary-card">
              <span class="summary-card__label">已导入</span>
              <span class="summary-card__value">{{ summaryTotal(importResult.summary?.imported) }}</span>
            </div>
            <div class="summary-card">
              <span class="summary-card__label">冲突跳过</span>
              <span class="summary-card__value">{{ summaryTotal(importResult.summary?.skipped_conflict) }}</span>
            </div>
            <div class="summary-card">
              <span class="summary-card__label">无效跳过</span>
              <span class="summary-card__value">{{ summaryTotal(importResult.summary?.skipped_invalid) }}</span>
            </div>
            <div class="summary-card">
              <span class="summary-card__label">缺少证书跳过</span>
              <span class="summary-card__value">{{ summaryTotal(importResult.summary?.skipped_missing_material) }}</span>
            </div>
          </div>

          <div class="backup-report__meta">
            <div class="info-row">
              <span class="info-label">来源架构</span>
              <span class="info-value">{{ importResult.manifest?.source_architecture || '—' }}</span>
            </div>
            <div class="info-row">
              <span class="info-label">导出时间</span>
              <span class="info-value">{{ formatTimestamp(importResult.manifest?.exported_at) }}</span>
            </div>
          </div>

          <div class="report-group">
            <h3 class="report-group__title">导入报告</h3>
            <div v-for="section in reportSections" :key="section.key" class="report-block">
              <div class="report-block__header">
                <span>{{ section.label }}</span>
                <span class="report-block__count">{{ section.items.length }}</span>
              </div>
              <ul v-if="section.items.length" class="report-list">
                <li v-for="item in section.items" :key="`${section.key}-${item.kind}-${item.key}`" class="report-list__item">
                  <span class="report-list__kind">{{ item.kind }}</span>
                  <span class="report-list__key">{{ item.key }}</span>
                  <span v-if="item.reason" class="report-list__reason">{{ item.reason }}</span>
                </li>
              </ul>
              <div v-else class="report-empty">无</div>
            </div>
          </div>

          <button class="action-button action-button--secondary" @click="resetImport">完成</button>
        </template>
      </div>
    </section>
  </div>
</template>

<script setup>
import { computed, ref, onMounted } from 'vue'
import { exportBackup, exportBackupSelective, importBackup, importBackupPreview, fetchBackupResourceCounts } from '../../api'
import { messageStore } from '../../stores/messages'

const counts = ref({ agents: 0, http_rules: 0, l4_rules: 0, relay_listeners: 0, certificates: 0, version_policies: 0 })
const exportSelection = ref({ agents: true, http_rules: true, l4_rules: true, relay_listeners: true, certificates: true, version_policies: true })
const exporting = ref(false)

const importStep = ref(1)
const previewing = ref(false)
const importing = ref(false)
const previewResult = ref(null)
const importResult = ref(null)
const selectedFileName = ref('')
const fileInputRef = ref(null)
let selectedFile = null

const exportItems = [
  { key: 'agents', label: '节点 (Agents)' },
  { key: 'http_rules', label: 'HTTP 规则' },
  { key: 'l4_rules', label: 'L4 规则' },
  { key: 'relay_listeners', label: '中继监听' },
  { key: 'certificates', label: '证书' },
  { key: 'version_policies', label: '版本策略' }
]

const hasAnySelection = computed(() => Object.values(exportSelection.value).some(Boolean))

const previewSections = computed(() => {
  if (!previewResult.value) return []
  const s = previewResult.value.summary || {}
  const types = [
    { key: 'agents', label: '节点', importKey: 'agents' },
    { key: 'http_rules', label: 'HTTP 规则', importKey: 'http_rules' },
    { key: 'l4_rules', label: 'L4 规则', importKey: 'l4_rules' },
    { key: 'relay_listeners', label: '中继监听', importKey: 'relay_listeners' },
    { key: 'certificates', label: '证书', importKey: 'certificates' },
    { key: 'version_policies', label: '版本策略', importKey: 'version_policies' }
  ]
  return types.map(t => {
    const imported = s.imported?.[t.importKey] || 0
    const conflict = s.skipped_conflict?.[t.importKey] || 0
    const invalid = s.skipped_invalid?.[t.importKey] || 0
    const missing = s.skipped_missing_material?.[t.importKey] || 0
    const total = imported + conflict + invalid + missing
    const parts = []
    if (imported) parts.push(`新增 ${imported}`)
    if (conflict) parts.push(`跳过 ${conflict} (冲突)`)
    if (invalid) parts.push(`跳过 ${invalid} (无效)`)
    if (missing) parts.push(`跳过 ${missing} (缺证书)`)
    return {
      ...t,
      total,
      statusText: total === 0 ? '无' : parts.join(' / '),
      statusClass: imported > 0 ? 'preview-status--ok' : 'preview-status--skip'
    }
  })
})

const reportSections = computed(() => {
  const report = importResult.value?.report || {}
  return [
    { key: 'imported', label: '已导入', items: report.imported || [] },
    { key: 'skipped_conflict', label: '冲突跳过', items: report.skipped_conflict || [] },
    { key: 'skipped_invalid', label: '无效跳过', items: report.skipped_invalid || [] },
    { key: 'skipped_missing_material', label: '缺少证书材料跳过', items: report.skipped_missing_material || [] }
  ]
})

onMounted(() => {
  fetchBackupResourceCounts()
    .then(d => { counts.value = d.counts })
    .catch(() => {})
})

function summaryTotal(group = {}) {
  return Object.values(group || {}).reduce((sum, v) => sum + Number(v || 0), 0)
}

function formatTimestamp(value) {
  if (!value) return '—'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}

function downloadBlob(blob, filename) {
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = filename
  document.body.appendChild(link)
  link.click()
  link.remove()
  URL.revokeObjectURL(url)
}

async function handleExport() {
  const selected = Object.entries(exportSelection.value)
    .filter(([, v]) => v)
    .map(([k]) => k)
  const allSelected = selected.length === exportItems.length

  exporting.value = true
  try {
    const result = allSelected
      ? await exportBackup()
      : await exportBackupSelective(selected)
    downloadBlob(result.blob, result.filename)
    messageStore.success('备份已导出')
  } catch (error) {
    messageStore.error(error, '导出备份失败')
  } finally {
    exporting.value = false
  }
}

function handleFileChange(event) {
  const file = event.target.files?.[0]
  if (!file) return
  selectedFile = file
  selectedFileName.value = file.name
  importStep.value = 1
}

async function handlePreview() {
  if (!selectedFile) return
  previewing.value = true
  try {
    previewResult.value = await importBackupPreview(selectedFile)
    importStep.value = 2
  } catch (error) {
    messageStore.error(error, '预览失败')
  } finally {
    previewing.value = false
  }
}

async function handleConfirmImport() {
  if (!selectedFile) return
  importing.value = true
  try {
    importResult.value = await importBackup(selectedFile)
    messageStore.success('备份导入完成')
    importStep.value = 3
  } catch (error) {
    importResult.value = null
    messageStore.error(error, '导入备份失败')
  } finally {
    importing.value = false
  }
}

function resetImport() {
  importStep.value = 1
  previewResult.value = null
  importResult.value = null
  selectedFile = null
  selectedFileName.value = ''
  if (fileInputRef.value) fileInputRef.value.value = ''
}
</script>

<style scoped>
.settings-data-mgmt { display: flex; flex-direction: column; gap: 1.25rem; }
.settings-section {
  background: var(--color-bg-surface);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-2xl);
  overflow: hidden;
}
.settings-section__header { padding: 1rem 1.25rem; border-bottom: 1px solid var(--color-border-subtle); }
.settings-section__title { font-size: 1rem; font-weight: 600; margin: 0 0 0.25rem; color: var(--color-text-primary); }
.settings-section__desc { font-size: 0.875rem; color: var(--color-text-tertiary); margin: 0; }
.settings-section__body { padding: 1.25rem; display: flex; flex-direction: column; gap: 1rem; }

/* Export checklist */
.export-checklist {
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  overflow: hidden;
}
.export-checklist__item {
  display: flex;
  align-items: center;
  gap: 0.6rem;
  padding: 0.65rem 1rem;
  cursor: pointer;
  transition: background 0.1s;
}
.export-checklist__item:not(:last-child) { border-bottom: 1px solid var(--color-border-subtle); }
.export-checklist__item:hover { background: var(--color-bg-subtle); }
.export-checklist__input { width: 16px; height: 16px; cursor: pointer; }
.export-checklist__label { font-size: 0.9rem; flex: 1; }
.export-checklist__count { font-size: 0.8rem; color: var(--color-text-tertiary); }

/* Import steps */
.import-steps {
  display: flex;
  gap: 0.5rem;
  flex-wrap: wrap;
}
.import-step {
  padding: 0.3rem 0.8rem;
  border-radius: 20px;
  font-size: 0.75rem;
  font-weight: 500;
  background: var(--color-bg-subtle);
  color: var(--color-text-tertiary);
}
.import-step.active { background: var(--color-primary); color: #fff; font-weight: 600; }
.import-step.done { background: var(--color-primary-subtle); color: var(--color-primary); }

.backup-file-input { display: none; }

.backup-hint {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0.75rem 0.9rem;
  border: 1px dashed var(--color-border-default);
  border-radius: var(--radius-lg);
  background: var(--color-bg-subtle);
}

.import-actions { display: flex; gap: 0.75rem; flex-wrap: wrap; }

/* Preview */
.preview-meta {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 0.6rem;
}
.preview-meta__item {
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  padding: 0.6rem 0.9rem;
}
.preview-table {
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  overflow: hidden;
}
.preview-table__header {
  display: flex;
  justify-content: space-between;
  padding: 0.5rem 1rem;
  background: var(--color-bg-subtle);
  border-bottom: 1px solid var(--color-border-default);
  font-size: 0.85rem;
  font-weight: 600;
}
.preview-table__row {
  display: flex;
  justify-content: space-between;
  padding: 0.45rem 1rem;
  font-size: 0.85rem;
}
.preview-table__row:not(:last-child) { border-bottom: 1px solid var(--color-border-subtle); }
.preview-status--ok { color: #16a34a; font-size: 0.8rem; }
.preview-status--skip { color: var(--color-text-tertiary); font-size: 0.8rem; }

/* Results */
.backup-report__summary {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(120px, 1fr));
  gap: 0.75rem;
}
.summary-card {
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  background: var(--color-bg-subtle);
  padding: 0.85rem 1rem;
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
}
.summary-card__label { font-size: 0.8rem; color: var(--color-text-secondary); }
.summary-card__value { font-size: 1.35rem; font-weight: 700; color: var(--color-text-primary); }

.backup-report__meta {
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-xl);
  padding: 0.25rem 1rem;
}

.report-group {
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-xl);
  overflow: hidden;
}
.report-group__title {
  margin: 0; padding: 0.9rem 1rem;
  font-size: 0.95rem; font-weight: 600;
  border-bottom: 1px solid var(--color-border-subtle);
  background: var(--color-bg-subtle);
}
.report-block + .report-block { border-top: 1px solid var(--color-border-subtle); }
.report-block__header {
  display: flex; justify-content: space-between; gap: 1rem;
  padding: 0.8rem 1rem; font-size: 0.875rem; font-weight: 600;
}
.report-block__count { color: var(--color-text-secondary); }
.report-list {
  list-style: none; margin: 0; padding: 0 1rem 1rem;
  display: flex; flex-direction: column; gap: 0.6rem;
}
.report-list__item {
  display: grid; grid-template-columns: 120px 1fr;
  gap: 0.5rem 0.75rem; align-items: start;
}
.report-list__kind { font-size: 0.75rem; color: var(--color-text-secondary); text-transform: uppercase; letter-spacing: 0.04em; }
.report-list__key { color: var(--color-text-primary); word-break: break-all; }
.report-list__reason { grid-column: 2; font-size: 0.8rem; color: var(--color-text-tertiary); }
.report-empty { padding: 0 1rem 1rem; font-size: 0.85rem; color: var(--color-text-tertiary); }

/* Action buttons */
.action-button {
  border: 1.5px solid var(--color-primary);
  background: var(--color-primary);
  color: white;
  border-radius: var(--radius-lg);
  padding: 0.7rem 1rem;
  font-size: 0.95rem;
  font-weight: 600;
  cursor: pointer;
  transition: transform 0.2s var(--ease-default), opacity 0.2s var(--ease-default);
}
.action-button:hover:not(:disabled) { transform: translateY(-1px); }
.action-button:disabled { opacity: 0.6; cursor: not-allowed; }
.action-button--secondary {
  background: var(--color-bg-subtle);
  color: var(--color-text-primary);
  border-color: var(--color-border-default);
}

/* Info rows */
.info-row { display: flex; align-items: center; justify-content: space-between; padding: 0.5rem 0; border-bottom: 1px solid var(--color-border-subtle); }
.info-row:last-child { border-bottom: none; }
.info-label { font-size: 0.875rem; color: var(--color-text-secondary); }
.info-value { font-size: 0.875rem; color: var(--color-text-primary); font-weight: 500; }

@media (max-width: 640px) {
  .preview-meta { grid-template-columns: 1fr; }
  .import-actions { flex-direction: column; }
  .backup-hint { flex-direction: column; align-items: flex-start; }
}
</style>
```

- [ ] **Step 2: Commit**

```bash
git add panel/frontend/src/components/settings/SettingsDataMgmt.vue
git commit -m "feat(frontend): add SettingsDataMgmt with selective export and import preview"
```

---

### Task 9: Create SettingsAbout component

**Files:**
- Create: `panel/frontend/src/components/settings/SettingsAbout.vue`

- [ ] **Step 1: Create SettingsAbout.vue**

```vue
<template>
  <div class="settings-about">
    <!-- Project identity -->
    <div class="about-identity">
      <h2 class="about-identity__name">Nginx Reverse Emby</h2>
      <p class="about-identity__tagline">Nginx 反向代理 &amp; Emby 媒体管理控制面板</p>
    </div>

    <!-- Version info -->
    <section class="settings-section">
      <div class="settings-section__header">
        <h3 class="settings-section__title">版本信息</h3>
      </div>
      <div class="settings-section__body">
        <div class="info-row">
          <span class="info-label">当前版本</span>
          <span class="info-value">{{ info?.app_version || 'dev' }}</span>
        </div>
        <div class="info-row">
          <span class="info-label">构建时间</span>
          <span class="info-value">{{ info?.build_time || '—' }}</span>
        </div>
        <div class="info-row">
          <span class="info-label">架构</span>
          <span class="info-value">{{ info?.local_apply_runtime || '—' }}</span>
        </div>
        <div class="info-row">
          <span class="info-label">Go 版本</span>
          <span class="info-value">{{ info?.go_version || '—' }}</span>
        </div>
      </div>
    </section>

    <!-- Project links -->
    <section v-if="info?.project_url" class="settings-section">
      <div class="settings-section__header">
        <h3 class="settings-section__title">项目地址</h3>
      </div>
      <div class="settings-section__body">
        <div class="info-row">
          <span class="info-label">GitHub</span>
          <a :href="info.project_url" target="_blank" rel="noopener" class="info-link">{{ info.project_url }} ↗</a>
        </div>
        <div class="info-row">
          <span class="info-label">问题反馈</span>
          <a :href="info.project_url + '/issues'" target="_blank" rel="noopener" class="info-link">Issues ↗</a>
        </div>
      </div>
    </section>

    <!-- System status -->
    <section class="settings-section">
      <div class="settings-section__header">
        <h3 class="settings-section__title">系统状态</h3>
      </div>
      <div class="settings-section__body">
        <div class="info-row">
          <span class="info-label">角色</span>
          <span class="info-value">{{ info?.role || '—' }}</span>
        </div>
        <div class="info-row">
          <span class="info-label">本地 Agent</span>
          <span class="info-value" :class="info?.local_agent_enabled ? 'status-ok' : ''">
            {{ info?.local_agent_enabled ? '● 已启用' : '未启用' }}
          </span>
        </div>
        <div class="info-row">
          <span class="info-label">在线节点</span>
          <span class="info-value">{{ info?.online_agents ?? '—' }} / {{ info?.total_agents ?? '—' }}</span>
        </div>
        <div class="info-row">
          <span class="info-label">运行时长</span>
          <span class="info-value">{{ formatUptime(info?.started_at) }}</span>
        </div>
        <div class="info-row">
          <span class="info-label">数据目录</span>
          <span class="info-value info-value--mono">{{ info?.data_dir || '—' }}</span>
        </div>
      </div>
    </section>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { fetchSystemInfo } from '../../api'

const info = ref(null)

onMounted(() => {
  fetchSystemInfo().then(d => { info.value = d }).catch(() => {})
})

function formatUptime(startedAt) {
  if (!startedAt) return '—'
  const start = new Date(startedAt)
  if (Number.isNaN(start.getTime())) return '—'
  const diff = Date.now() - start.getTime()
  if (diff < 0) return '—'
  const seconds = Math.floor(diff / 1000)
  const days = Math.floor(seconds / 86400)
  const hours = Math.floor((seconds % 86400) / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  if (days > 0) return `${days} 天 ${hours} 小时`
  if (hours > 0) return `${hours} 小时 ${minutes} 分钟`
  return `${minutes} 分钟`
}
</script>

<style scoped>
.settings-about { display: flex; flex-direction: column; gap: 1.25rem; }

.about-identity {
  text-align: center;
  padding: 1.5rem 0;
}
.about-identity__name {
  font-size: 1.8rem;
  font-weight: 700;
  margin: 0 0 0.3rem;
  color: var(--color-text-primary);
}
.about-identity__tagline {
  font-size: 0.85rem;
  color: var(--color-text-tertiary);
  margin: 0;
}

.settings-section {
  background: var(--color-bg-surface);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-2xl);
  overflow: hidden;
}
.settings-section__header { padding: 1rem 1.25rem; border-bottom: 1px solid var(--color-border-subtle); }
.settings-section__title { font-size: 1rem; font-weight: 600; margin: 0; color: var(--color-text-primary); }
.settings-section__body { padding: 0.25rem 1.25rem; }

.info-row { display: flex; align-items: center; justify-content: space-between; padding: 0.5rem 0; border-bottom: 1px solid var(--color-border-subtle); }
.info-row:last-child { border-bottom: none; }
.info-label { font-size: 0.875rem; color: var(--color-text-secondary); }
.info-value { font-size: 0.875rem; color: var(--color-text-primary); font-weight: 500; }
.info-value--mono { font-family: monospace; font-size: 0.8rem; }
.info-link {
  font-size: 0.85rem;
  color: var(--color-primary);
  text-decoration: none;
  word-break: break-all;
}
.info-link:hover { text-decoration: underline; }
.status-ok { color: #16a34a; }
</style>
```

- [ ] **Step 2: Commit**

```bash
git add panel/frontend/src/components/settings/SettingsAbout.vue
git commit -m "feat(frontend): add SettingsAbout component"
```

---

### Task 10: Rewrite SettingsPage.vue as tab shell

**Files:**
- Modify: `panel/frontend/src/pages/SettingsPage.vue`

- [ ] **Step 1: Replace SettingsPage.vue with tab shell**

Replace the entire content of `panel/frontend/src/pages/SettingsPage.vue` with:

```vue
<template>
  <div class="settings-page">
    <div class="settings-page__header">
      <h1 class="settings-page__title">系统设置</h1>
    </div>
    <div class="settings-layout">
      <SettingsNav v-model:activeTab="activeTab" />
      <div class="settings-content">
        <SettingsGeneral v-if="activeTab === 'general'" />
        <SettingsDataMgmt v-else-if="activeTab === 'data'" />
        <SettingsAbout v-else-if="activeTab === 'about'" />
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref } from 'vue'
import SettingsNav from '../components/settings/SettingsNav.vue'
import SettingsGeneral from '../components/settings/SettingsGeneral.vue'
import SettingsDataMgmt from '../components/settings/SettingsDataMgmt.vue'
import SettingsAbout from '../components/settings/SettingsAbout.vue'

const activeTab = ref('general')
</script>

<style scoped>
.settings-page {
  max-width: 900px;
  margin: 0 auto;
}
.settings-page__header { margin-bottom: 2rem; }
.settings-page__title { font-size: 1.5rem; font-weight: 700; margin: 0; color: var(--color-text-primary); }

.settings-layout {
  display: flex;
  gap: 0;
  background: var(--color-bg-surface);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-2xl);
  overflow: hidden;
}

.settings-content {
  flex: 1;
  padding: 1.5rem 2rem;
  min-width: 0;
}

@media (max-width: 767px) {
  .settings-page { max-width: 100%; }
  .settings-layout { flex-direction: column; }
  .settings-content { padding: 1.25rem; }
}

@media (min-width: 2560px) {
  .settings-page { max-width: 1100px; }
  .settings-page__title { font-size: 1.75rem; }
}
</style>
```

- [ ] **Step 2: Build frontend to verify**

Run: `cd panel/frontend && npm run build`
Expected: Build succeeds with no errors.

- [ ] **Step 3: Commit**

```bash
git add panel/frontend/src/pages/SettingsPage.vue
git commit -m "feat(frontend): rewrite SettingsPage with tab navigation shell"
```

---

### Task 11: Build and verify

**Files:** None (verification only)

- [ ] **Step 1: Run backend tests**

Run: `cd panel/backend-go && go test ./...`
Expected: All tests pass.

- [ ] **Step 2: Run frontend build**

Run: `cd panel/frontend && npm run build`
Expected: Build succeeds.

- [ ] **Step 3: Start backend and frontend dev server together**

Run backend: `cd panel/backend-go && go run ./cmd/nre-control-plane`
Run frontend: `cd panel/frontend && npm run dev`

Open the app in browser, navigate to Settings page, verify:
- Left sidebar shows 3 tabs
- Clicking tabs switches content
- General tab shows theme selection and deploy mode
- Data Management tab shows export checklist and import flow
- About tab shows version info, project links (if configured), system status

- [ ] **Step 4: Final commit if any fixes needed**

```bash
git add -A
git commit -m "fix: address issues found during integration testing"
```
