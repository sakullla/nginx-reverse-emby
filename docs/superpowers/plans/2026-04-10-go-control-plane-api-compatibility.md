# Go Control-Plane API Compatibility Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace `panel/backend/` with a Go control-plane under `panel/backend-go/` that serves the Vue app, reads the existing SQLite data in place, and exposes API-compatible `/panel-api/*` and `/agent-api/*` endpoints.

**Architecture:** Build the control-plane as a separate Go codebase under `panel/backend-go/` so it cleanly replaces the current Node backend without polluting `go-agent/`. Inside `panel/backend-go/`, use a dedicated `internal/controlplane` package tree with config, storage, service, and HTTP layers; the HTTP layer owns contract compatibility while services own normalization, revision bumps, and persistence.

**Tech Stack:** Go 1.24, stdlib `net/http`, SQLite access in Go, Vue static dist, shell-asset publishing.

---

## File Map

**Create**
- `panel/backend-go/go.mod`
- `panel/backend-go/go.sum`
- `panel/backend-go/cmd/nre-control-plane/main.go`
- `panel/backend-go/internal/controlplane/config/config.go`
- `panel/backend-go/internal/controlplane/app/app.go`
- `panel/backend-go/internal/controlplane/http/router.go`
- `panel/backend-go/internal/controlplane/http/auth.go`
- `panel/backend-go/internal/controlplane/http/handlers_info.go`
- `panel/backend-go/internal/controlplane/http/handlers_agents.go`
- `panel/backend-go/internal/controlplane/http/handlers_rules.go`
- `panel/backend-go/internal/controlplane/http/handlers_l4.go`
- `panel/backend-go/internal/controlplane/http/handlers_relay.go`
- `panel/backend-go/internal/controlplane/http/handlers_certs.go`
- `panel/backend-go/internal/controlplane/http/handlers_versions.go`
- `panel/backend-go/internal/controlplane/http/handlers_public.go`
- `panel/backend-go/internal/controlplane/http/static.go`
- `panel/backend-go/internal/controlplane/service/system.go`
- `panel/backend-go/internal/controlplane/service/agents.go`
- `panel/backend-go/internal/controlplane/service/rules.go`
- `panel/backend-go/internal/controlplane/service/l4.go`
- `panel/backend-go/internal/controlplane/service/relay.go`
- `panel/backend-go/internal/controlplane/service/certs.go`
- `panel/backend-go/internal/controlplane/service/versions.go`
- `panel/backend-go/internal/controlplane/service/public.go`
- `panel/backend-go/internal/controlplane/storage/sqlite_store.go`
- `panel/backend-go/internal/controlplane/storage/sqlite_models.go`
- `panel/backend-go/internal/controlplane/storage/sqlite_store_test.go`
- `panel/backend-go/internal/controlplane/http/router_test.go`
- `panel/backend-go/internal/controlplane/http/public_test.go`
- `panel/backend-go/internal/controlplane/service/rules_test.go`

**Modify**
- `Dockerfile`

### Task 1: Bootstrap the Go control-plane binary and config

**Files:**
- Create: `panel/backend-go/go.mod`
- Create: `panel/backend-go/cmd/nre-control-plane/main.go`
- Create: `panel/backend-go/internal/controlplane/config/config.go`
- Create: `panel/backend-go/internal/controlplane/app/app.go`
- Test: `panel/backend-go/internal/controlplane/config/config_test.go`

- [ ] **Step 1: Write the failing config test**

```go
func TestLoadFromEnvDefaultsMasterRuntime(t *testing.T) {
	t.Setenv("NRE_CONTROL_PLANE_ADDR", "0.0.0.0:8080")
	t.Setenv("NRE_CONTROL_PLANE_DATA_DIR", "/tmp/nre-data")
	t.Setenv("NRE_PANEL_TOKEN", "secret")
	t.Setenv("NRE_REGISTER_TOKEN", "register-secret")
	t.Setenv("NRE_FRONTEND_DIST_DIR", "/tmp/frontend-dist")
	t.Setenv("NRE_PUBLIC_AGENT_ASSETS_DIR", "/tmp/assets")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}
	if cfg.ListenAddr != "0.0.0.0:8080" || !cfg.EnableLocalAgent || cfg.LocalAgentID != "local" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd panel/backend-go && go test ./internal/controlplane/config -run TestLoadFromEnvDefaultsMasterRuntime -v`
Expected: FAIL with `undefined: LoadFromEnv`

- [ ] **Step 3: Write minimal config and app bootstrap**

```go
type Config struct {
	ListenAddr           string
	DataDir              string
	PanelToken           string
	RegisterToken        string
	FrontendDistDir      string
	PublicAgentAssetsDir string
	EnableLocalAgent     bool
	LocalAgentID         string
	LocalAgentName       string
	HeartbeatInterval    time.Duration
}
```

```go
func main() {
	cfg, err := config.LoadFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	handler, err := httpapi.NewRouter(httpapi.Dependencies{Config: cfg})
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := app.New(cfg, handler, nil).Run(ctx); err != nil {
		log.Fatal(err)
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd panel/backend-go && go test ./internal/controlplane/config ./cmd/nre-control-plane -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add panel/backend-go/go.mod panel/backend-go/cmd/nre-control-plane/main.go panel/backend-go/internal/controlplane/config/config.go panel/backend-go/internal/controlplane/app/app.go panel/backend-go/internal/controlplane/config/config_test.go
git commit -m "feat(control-plane): add go control-plane bootstrap"
```

### Task 2: Implement SQLite compatibility store for agents, rules, and local state

**Files:**
- Create: `panel/backend-go/internal/controlplane/storage/sqlite_store.go`
- Create: `panel/backend-go/internal/controlplane/storage/sqlite_models.go`
- Test: `panel/backend-go/internal/controlplane/storage/sqlite_store_test.go`

- [ ] **Step 1: Write the failing persistence test**

```go
func TestStoreLoadsAgentsAndRulesFromExistingSQLite(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join("testdata", "panel-data"), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	agents, err := store.ListAgents(t.Context())
	if err != nil || len(agents) == 0 {
		t.Fatalf("ListAgents() = %v, %v", agents, err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd panel/backend-go && go test ./internal/controlplane/storage -run TestStoreLoadsAgentsAndRulesFromExistingSQLite -v`
Expected: FAIL with `undefined: NewSQLiteStore`

- [ ] **Step 3: Write minimal SQLite-backed store**

```go
type Store interface {
	ListAgents(context.Context) ([]AgentRow, error)
	ListHTTPRules(context.Context, string) ([]HTTPRuleRow, error)
	LoadLocalAgentState(context.Context) (LocalAgentStateRow, error)
}

func NewSQLiteStore(dataRoot string, localAgentID string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s/panel.sqlite", dataRoot))
	if err != nil {
		return nil, err
	}
	return &SQLiteStore{db: db, localAgentID: localAgentID}, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd panel/backend-go && go test ./internal/controlplane/storage -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add panel/backend-go/internal/controlplane/storage/sqlite_store.go panel/backend-go/internal/controlplane/storage/sqlite_models.go panel/backend-go/internal/controlplane/storage/sqlite_store_test.go panel/backend-go/go.mod panel/backend-go/go.sum
git commit -m "feat(control-plane): add sqlite compatibility store"
```

### Task 3: Expose auth, system info, agents, and read-only rule APIs

**Files:**
- Create: `panel/backend-go/internal/controlplane/http/router.go`
- Create: `panel/backend-go/internal/controlplane/http/auth.go`
- Create: `panel/backend-go/internal/controlplane/http/handlers_info.go`
- Create: `panel/backend-go/internal/controlplane/http/handlers_agents.go`
- Create: `panel/backend-go/internal/controlplane/http/handlers_rules.go`
- Create: `panel/backend-go/internal/controlplane/service/system.go`
- Create: `panel/backend-go/internal/controlplane/service/agents.go`
- Test: `panel/backend-go/internal/controlplane/http/router_test.go`

- [ ] **Step 1: Write the failing HTTP contract test**

```go
func TestRouterServesPanelAuthAndInfoEndpoints(t *testing.T) {
	type fakeSystemService struct{ info SystemInfo }
	func (f fakeSystemService) Info(context.Context) SystemInfo { return f.info }
	type fakeAgentService struct{ agents []AgentSummary }
	func (f fakeAgentService) List(context.Context) ([]AgentSummary, error) { return f.agents, nil }

	router, err := NewRouter(Dependencies{
		Config: config.Config{PanelToken: "secret"},
		SystemService: fakeSystemService{info: SystemInfo{Role: "master", LocalApplyRuntime: "go-agent", DefaultAgentID: "local", LocalAgentEnabled: true}},
		AgentService: fakeAgentService{agents: []AgentSummary{{ID: "local", Name: "local"}}},
	})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}
	server := httptest.NewServer(router)
	defer server.Close()
	req, _ := http.NewRequest(http.MethodGet, server.URL+"/panel-api/info", nil)
	req.Header.Set("X-Panel-Token", "secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /panel-api/info = %v, %v", resp.StatusCode, err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd panel/backend-go && go test ./internal/controlplane/http -run TestRouterServesPanelAuthAndInfoEndpoints -v`
Expected: FAIL with `undefined: NewRouter`

- [ ] **Step 3: Write minimal router and handlers**

```go
func NewRouter(deps Dependencies) (http.Handler, error) {
	mux := http.NewServeMux()
	mux.HandleFunc("/panel-api/auth/verify", deps.handleVerify)
	mux.Handle("/panel-api/info", deps.requirePanelToken(http.HandlerFunc(deps.handleInfo)))
	mux.Handle("/panel-api/agents", deps.requirePanelToken(http.HandlerFunc(deps.handleAgents)))
	return mux, nil
}
```

```go
func (d Dependencies) handleInfo(w http.ResponseWriter, r *http.Request) {
	info := d.SystemService.Info(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true,
		"role": info.Role,
		"local_apply_runtime": info.LocalApplyRuntime,
		"default_agent_id": info.DefaultAgentID,
		"local_agent_enabled": info.LocalAgentEnabled,
	})
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd panel/backend-go && go test ./internal/controlplane/http ./internal/controlplane/service -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add panel/backend-go/internal/controlplane/http/router.go panel/backend-go/internal/controlplane/http/auth.go panel/backend-go/internal/controlplane/http/handlers_info.go panel/backend-go/internal/controlplane/http/handlers_agents.go panel/backend-go/internal/controlplane/http/handlers_rules.go panel/backend-go/internal/controlplane/service/system.go panel/backend-go/internal/controlplane/service/agents.go panel/backend-go/internal/controlplane/http/router_test.go
git commit -m "feat(control-plane): add auth and read-only panel apis"
```

### Task 4: Add write APIs, heartbeat sync, public assets, and static frontend serving

**Files:**
- Create: `panel/backend-go/internal/controlplane/http/handlers_l4.go`
- Create: `panel/backend-go/internal/controlplane/http/handlers_relay.go`
- Create: `panel/backend-go/internal/controlplane/http/handlers_certs.go`
- Create: `panel/backend-go/internal/controlplane/http/handlers_versions.go`
- Create: `panel/backend-go/internal/controlplane/http/handlers_public.go`
- Create: `panel/backend-go/internal/controlplane/http/static.go`
- Create: `panel/backend-go/internal/controlplane/http/public_test.go`
- Modify: `Dockerfile`

- [ ] **Step 1: Write the failing contract test for join script and heartbeat**

```go
func TestRouterServesJoinScriptAndHeartbeat(t *testing.T) {
	type fakeAgentService struct{ heartbeatReply HeartbeatReply }
	func (f fakeAgentService) Heartbeat(context.Context, HeartbeatRequest, string) (HeartbeatReply, error) {
		return f.heartbeatReply, nil
	}

	router, err := NewRouter(Dependencies{
		Config: config.Config{PanelToken: "secret", PublicAgentAssetsDir: filepath.Join("testdata", "assets"), FrontendDistDir: filepath.Join("testdata", "dist")},
		AgentService: fakeAgentService{heartbeatReply: HeartbeatReply{DesiredRevision: 12}},
	})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}
	server := httptest.NewServer(router)
	defer server.Close()
	resp, err := http.Get(server.URL + "/panel-api/public/join-agent.sh")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("GET join-agent.sh = %v, %v", resp.StatusCode, err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd panel/backend-go && go test ./internal/controlplane/http -run TestRouterServesJoinScriptAndHeartbeat -v`
Expected: FAIL with `404` or missing handler

- [ ] **Step 3: Implement heartbeat, public assets, and SPA serving**

```go
func (d Dependencies) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	var req HeartbeatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "message": "invalid JSON body"})
		return
	}
	reply, err := d.AgentService.Heartbeat(r.Context(), req, readAgentToken(r))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "sync": reply})
}
```

```dockerfile
FROM debian:bookworm-slim AS control-plane-runtime
COPY --from=backend-go-builder /out/nre-control-plane /usr/local/bin/nre-control-plane
CMD ["/usr/local/bin/nre-control-plane"]
```

- [ ] **Step 4: Run tests and image build**

Run: `cd panel/backend-go && go test ./internal/controlplane/... -v`
Expected: PASS

Run: `docker build -t nginx-reverse-emby:test --target control-plane-runtime .`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add panel/backend-go/internal/controlplane/http/handlers_l4.go panel/backend-go/internal/controlplane/http/handlers_relay.go panel/backend-go/internal/controlplane/http/handlers_certs.go panel/backend-go/internal/controlplane/http/handlers_versions.go panel/backend-go/internal/controlplane/http/handlers_public.go panel/backend-go/internal/controlplane/http/static.go panel/backend-go/internal/controlplane/http/public_test.go Dockerfile
git commit -m "feat(control-plane): add write apis and public assets"
```
