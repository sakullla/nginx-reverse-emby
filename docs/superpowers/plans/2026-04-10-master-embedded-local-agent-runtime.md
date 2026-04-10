# Master Embedded Local-Agent Runtime Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the Docker master process start with built-in local agent capability so master can serve traffic without a second user-visible agent process.

**Architecture:** Keep the existing remote `nre-agent` binary unchanged as the edge runtime. Add a control-plane-local runtime bridge that instantiates the existing `internal/app` runtime with a local sync source backed by the control-plane storage facade and writes local runtime state back into the shared control-plane data.

**Tech Stack:** Go 1.24, existing `go-agent/internal/app`, local snapshot bridge, shared SQLite-backed control-plane store.

---

## File Map

**Create**
- `go-agent/internal/controlplane/localagent/runtime.go`
- `go-agent/internal/controlplane/localagent/sync_source.go`
- `go-agent/internal/controlplane/localagent/state_sink.go`
- `go-agent/internal/controlplane/localagent/runtime_test.go`

**Modify**
- `go-agent/internal/controlplane/app/app.go`
- `go-agent/internal/controlplane/config/config.go`
- `go-agent/internal/controlplane/http/handlers_info.go`
- `go-agent/internal/controlplane/service/agents.go`
- `docker-compose.yaml`

### Task 1: Add local-agent config and lifecycle orchestration to the control-plane app

**Files:**
- Modify: `go-agent/internal/controlplane/config/config.go`
- Modify: `go-agent/internal/controlplane/app/app.go`
- Test: `go-agent/internal/controlplane/localagent/runtime_test.go`

- [ ] **Step 1: Write the failing lifecycle test**

```go
func TestAppStartsEmbeddedLocalAgentWhenEnabled(t *testing.T) {
	var started bool
	app := New(config.Config{ListenAddr: "127.0.0.1:0", EnableLocalAgent: true}, http.NewServeMux(), func(context.Context) error {
		started = true
		return nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = app.Run(ctx)
	if !started {
		t.Fatal("embedded local agent did not start")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd go-agent && go test ./internal/controlplane/localagent -run TestAppStartsEmbeddedLocalAgentWhenEnabled -v`
Expected: FAIL with `undefined: New`

- [ ] **Step 3: Update app orchestration**

```go
type LocalAgentStarter func(context.Context) error

type App struct {
	server          *http.Server
	startLocalAgent LocalAgentStarter
}

func New(cfg config.Config, handler http.Handler, startLocalAgent LocalAgentStarter) *App {
	return &App{server: &http.Server{Addr: cfg.ListenAddr, Handler: handler}, startLocalAgent: startLocalAgent}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd go-agent && go test ./internal/controlplane/app ./internal/controlplane/localagent -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add go-agent/internal/controlplane/app/app.go go-agent/internal/controlplane/config/config.go go-agent/internal/controlplane/localagent/runtime_test.go
git commit -m "feat(control-plane): add embedded local-agent lifecycle"
```

### Task 2: Build the local snapshot source and runtime-state sink

**Files:**
- Create: `go-agent/internal/controlplane/localagent/sync_source.go`
- Create: `go-agent/internal/controlplane/localagent/state_sink.go`
- Create: `go-agent/internal/controlplane/localagent/runtime.go`
- Test: `go-agent/internal/controlplane/localagent/runtime_test.go`

- [ ] **Step 1: Write the failing bridge test**

```go
func TestLocalSyncSourceReturnsSnapshotFromControlPlaneStore(t *testing.T) {
	type fakeStore struct{ snapshot model.Snapshot }
	func (f fakeStore) LoadLocalSnapshot(context.Context, string) (model.Snapshot, error) { return f.snapshot, nil }
	func (f fakeStore) SaveLocalRuntimeState(context.Context, string, store.RuntimeState) error { return nil }

	store := fakeStore{snapshot: model.Snapshot{Revision: 15}}
	source := NewSyncSource(store, "local")
	got, err := source.Sync(t.Context(), agentsync.SyncRequest{CurrentRevision: 14})
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if got.Revision != 15 {
		t.Fatalf("Revision = %d", got.Revision)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd go-agent && go test ./internal/controlplane/localagent -run TestLocalSyncSourceReturnsSnapshotFromControlPlaneStore -v`
Expected: FAIL with `undefined: NewSyncSource`

- [ ] **Step 3: Implement the local sync source and state sink**

```go
type SnapshotStore interface {
	LoadLocalSnapshot(context.Context, string) (model.Snapshot, error)
	SaveLocalRuntimeState(context.Context, string, store.RuntimeState) error
}

type SyncSource struct {
	store   SnapshotStore
	agentID string
}

func NewSyncSource(store SnapshotStore, agentID string) *SyncSource {
	return &SyncSource{store: store, agentID: agentID}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd go-agent && go test ./internal/controlplane/localagent -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add go-agent/internal/controlplane/localagent/sync_source.go go-agent/internal/controlplane/localagent/state_sink.go go-agent/internal/controlplane/localagent/runtime.go go-agent/internal/controlplane/localagent/runtime_test.go
git commit -m "feat(control-plane): bridge embedded local agent to control-plane store"
```

### Task 3: Surface the embedded local-agent state through the API and Compose defaults

**Files:**
- Modify: `go-agent/internal/controlplane/http/handlers_info.go`
- Modify: `go-agent/internal/controlplane/service/agents.go`
- Modify: `docker-compose.yaml`
- Test: `go-agent/internal/controlplane/http/router_test.go`

- [ ] **Step 1: Write the failing API test**

```go
func TestInfoAndAgentsReportEmbeddedLocalAgent(t *testing.T) {
	type fakeSystemService struct{ info SystemInfo }
	func (f fakeSystemService) Info(context.Context) SystemInfo { return f.info }
	type fakeAgentService struct{ agents []AgentSummary }
	func (f fakeAgentService) List(context.Context) ([]AgentSummary, error) { return f.agents, nil }

	router, err := NewRouter(Dependencies{
		Config: config.Config{PanelToken: "secret", EnableLocalAgent: true},
		SystemService: fakeSystemService{info: SystemInfo{LocalAgentEnabled: true, DefaultAgentID: "local"}},
		AgentService: fakeAgentService{agents: []AgentSummary{{ID: "local", Name: "local", IsLocal: true, Mode: "local", Status: "online"}}},
	})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}
	server := httptest.NewServer(router)
	defer server.Close()
	req, _ := http.NewRequest(http.MethodGet, server.URL+"/panel-api/agents", nil)
	req.Header.Set("X-Panel-Token", "secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /panel-api/agents = %v, %v", resp.StatusCode, err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd go-agent && go test ./internal/controlplane/http -run TestInfoAndAgentsReportEmbeddedLocalAgent -v`
Expected: FAIL because local runtime flags are not exposed

- [ ] **Step 3: Update handlers and compose defaults**

```go
type AgentSummary struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Mode    string `json:"mode"`
	Status  string `json:"status"`
	IsLocal bool   `json:"is_local"`
}
```

```yaml
environment:
  NRE_ENABLE_LOCAL_AGENT: "1"
  NRE_LOCAL_AGENT_ID: local
  NRE_LOCAL_AGENT_NAME: local
```

- [ ] **Step 4: Run tests and compose rendering**

Run: `cd go-agent && go test ./internal/controlplane/http ./internal/controlplane/service -v`
Expected: PASS

Run: `docker compose config`
Expected: PASS and `NRE_ENABLE_LOCAL_AGENT` is rendered

- [ ] **Step 5: Commit**

```bash
git add go-agent/internal/controlplane/http/handlers_info.go go-agent/internal/controlplane/service/agents.go docker-compose.yaml go-agent/internal/controlplane/http/router_test.go
git commit -m "feat(control-plane): enable embedded local agent by default"
```
