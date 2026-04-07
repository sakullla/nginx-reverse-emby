# Go Agent Linux Runtime Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver a Linux-only, pure-Go execution plane that replaces agent-side Node/shell/nginx with a single `nre-agent` capable of heartbeat pull, persisted snapshots, HTTP/HTTPS proxying, L4 TCP/UDP direct proxying, TCP relay, local certificate issuance/renewal, self-update, and local-agent mode.

**Architecture:** Keep the existing Node/Vue control plane and finish the Go execution plane in dependency order: backend heartbeat/schema alignment first, then Go app/config/store/sync, then runtime orchestration, then HTTP, L4, relay, certs, updater, and finally local-agent integration plus full verification. Every runtime apply must be atomic against the last applied snapshot and must not report false success.

**Tech Stack:** Node.js backend, Vue frontend, Prisma/SQLite, Go 1.24+, Go standard library (`net`, `net/http`, `crypto/tls`, `os/exec`, `syscall`), Docker multi-stage builds

---

## File Map

### Existing files to modify

- `panel/backend/server.js`
  - Extend heartbeat payloads, tighten local-apply semantics, serve version package metadata needed by Go updater, and keep `/panel-api/*` compatibility.
- `panel/backend/storage.js`
  - Add any missing accessors used by Go runtime rollout state.
- `panel/backend/storage-json.js`
  - Persist runtime-facing fields required by versioning/local agent state.
- `panel/backend/storage-sqlite.js`
  - Same as JSON backend for SQLite-backed state.
- `panel/backend/storage-prisma-core.js`
  - Ensure schema/runtime persistence supports desired/apply/update state.
- `panel/backend/prisma/schema.prisma`
  - Add any missing agent/local state fields needed by full runtime rollout.
- `panel/backend/tests/go-agent-heartbeat.test.js`
  - Extend payload expectations for runtime/update state.
- `panel/backend/tests/runtime-packaging.test.js`
  - Verify the control-plane image continues to package and expose Go runtime assets.
- `panel/frontend/src/pages/AgentsPage.vue`
  - Surface runtime/update state as needed.
- `panel/frontend/src/pages/VersionsPage.vue`
  - Support rollout management for package URLs/hash data.
- `go-agent/internal/config/config.go`
  - Replace bootstrap-only config with real env/file configuration.
- `go-agent/internal/app/app.go`
  - Replace context-wait stub with the real long-running app loop.
- `go-agent/internal/sync/client.go`
  - Replace no-op sync client with real heartbeat pull logic.
- `go-agent/internal/store/store.go`
  - Replace in-memory-only semantics with persistent snapshot/runtime state.
- `go-agent/internal/runtime/runtime.go`
  - Replace empty runtime with orchestration logic.
- `go-agent/internal/proxy/http_engine.go`
  - Replace helper-only behavior with a runnable HTTP/HTTPS proxy runtime.
- `go-agent/internal/l4/engine.go`
  - Replace validation-only logic with runnable TCP/UDP direct runtime plus TCP relay entry integration.
- `go-agent/internal/relay/runtime.go`
  - Replace validation-only logic with runnable relay listeners/tunnels.
- `go-agent/internal/certs/manager.go`
  - Extend fingerprint helper into cert store/loader/issuer manager logic.
- `go-agent/internal/update/updater.go`
  - Replace version comparator with staged self-update flow.
- `go-agent/internal/platform/linux/service.go`
  - Add Linux process handoff/drain helpers as needed.
- `go-agent/cmd/nre-agent/main.go`
  - Wire full config, app startup, and graceful shutdown.
- `README.md`
  - Keep operator docs aligned with the finished runtime.

### New Go files to create

- `go-agent/internal/model/http.go`
  - HTTP rule/runtime-facing models derived from control-plane payloads.
- `go-agent/internal/model/l4.go`
  - L4 rule/runtime-facing models.
- `go-agent/internal/model/version.go`
  - Version/update-specific payload models.
- `go-agent/internal/store/filesystem.go`
  - Persistent snapshot/runtime-state store implementation.
- `go-agent/internal/store/filesystem_test.go`
  - Persistence and restart recovery tests.
- `go-agent/internal/sync/client_test.go`
  - Heartbeat client tests with httptest server.
- `go-agent/internal/runtime/runtime_test.go`
  - Runtime apply/rollback orchestration tests.
- `go-agent/internal/proxy/server.go`
  - HTTP listener/router runtime.
- `go-agent/internal/proxy/server_test.go`
  - HTTP runtime end-to-end tests.
- `go-agent/internal/l4/server.go`
  - TCP/UDP direct runtime.
- `go-agent/internal/l4/server_test.go`
  - L4 runtime end-to-end tests.
- `go-agent/internal/relay/server.go`
  - Relay listener/tunnel implementation.
- `go-agent/internal/relay/server_test.go`
  - One-hop and multi-hop relay tests.
- `go-agent/internal/certs/store.go`
  - Cert/key persistence and loading helpers.
- `go-agent/internal/certs/issuer.go`
  - Local issuance/renewal logic.
- `go-agent/internal/certs/store_test.go`
  - Certificate load/reload tests.
- `go-agent/internal/certs/issuer_test.go`
  - Renewal scheduling/issuance tests.
- `go-agent/internal/update/manager.go`
  - Update staging and process handoff logic.
- `go-agent/internal/update/manager_test.go`
  - Self-update tests.

### Existing files to remove after replacement is complete

- None in this plan beyond already-deleted legacy runtime files.

## Task 1: Finish control-plane runtime contract for full Go execution plane

**Files:**
- Modify: `panel/backend/server.js`
- Modify: `panel/backend/storage.js`
- Modify: `panel/backend/storage-json.js`
- Modify: `panel/backend/storage-sqlite.js`
- Modify: `panel/backend/storage-prisma-core.js`
- Modify: `panel/backend/prisma/schema.prisma`
- Test: `panel/backend/tests/go-agent-heartbeat.test.js`

- [ ] **Step 1: Write the failing backend test for runtime/update payload completeness**

```js
// Add to panel/backend/tests/go-agent-heartbeat.test.js
it("returns runtime-facing sync payload with packages, revision, and relay data", async () => {
  const payload = await requestHeartbeatSync({
    agent: { id: "go-edge", version: "1.0.0", platform: "linux-amd64" },
    policies: [{
      channel: "stable",
      desired_version: "1.1.0",
      packages: [{ platform: "linux-amd64", url: "https://example.com/nre-agent", sha256: "abc123" }]
    }],
  });

  assert.equal(payload.sync.desired_version, "1.1.0");
  assert.equal(payload.sync.version_package.url, "https://example.com/nre-agent");
  assert.equal(typeof payload.sync.desired_revision, "number");
  assert.ok(Array.isArray(payload.sync.relay_listeners));
});
```

- [ ] **Step 2: Run the targeted backend test to verify it fails**

Run: `cd panel/backend && node --test tests/go-agent-heartbeat.test.js`
Expected: FAIL because the payload is missing one or more required fields.

- [ ] **Step 3: Implement the minimal backend contract changes**

```js
// panel/backend/server.js - shape sketch inside heartbeat response builder
return {
  ok: true,
  sync: {
    desired_revision: desiredRevision,
    current_revision: agent.current_revision,
    rules: loadNormalizedRulesForAgent(agent.id),
    l4_rules: storage.loadL4RulesForAgent(agent.id),
    relay_listeners: storage.loadRelayListenersForAgent(agent.id),
    certificates: loadManagedCertificatesForAgent(agent.id),
    desired_version: agent.desired_version || null,
    version_package: resolveVersionPackageForAgent(agent),
    version_sha256: resolveVersionShaForAgent(agent),
  },
};
```

- [ ] **Step 4: Run the targeted backend test to verify it passes**

Run: `cd panel/backend && node --test tests/go-agent-heartbeat.test.js`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add panel/backend/server.js panel/backend/storage.js panel/backend/storage-json.js panel/backend/storage-sqlite.js panel/backend/storage-prisma-core.js panel/backend/prisma/schema.prisma panel/backend/tests/go-agent-heartbeat.test.js
git commit -m "feat(backend): finalize go runtime heartbeat contract"
```

## Task 2: Replace bootstrap config with real Linux agent configuration

**Files:**
- Modify: `go-agent/internal/config/config.go`
- Modify: `go-agent/internal/config/config_test.go`
- Modify: `go-agent/cmd/nre-agent/main.go`

- [ ] **Step 1: Write the failing config test**

```go
// go-agent/internal/config/config_test.go
func TestLoadFromEnv(t *testing.T) {
	t.Setenv("NRE_MASTER_URL", "https://master.example.com")
	t.Setenv("NRE_AGENT_TOKEN", "secret")
	t.Setenv("NRE_DATA_DIR", "/tmp/nre-data")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv returned error: %v", err)
	}
	if cfg.MasterURL != "https://master.example.com" {
		t.Fatalf("unexpected MasterURL: %q", cfg.MasterURL)
	}
	if cfg.DataDir != "/tmp/nre-data" {
		t.Fatalf("unexpected DataDir: %q", cfg.DataDir)
	}
}
```

- [ ] **Step 2: Run the config test to verify it fails**

Run: `cd go-agent && go test ./internal/config -run TestLoadFromEnv -v`
Expected: FAIL because `LoadFromEnv` and the new fields do not exist.

- [ ] **Step 3: Implement the minimal real config loader**

```go
// go-agent/internal/config/config.go
type Config struct {
	AgentID           string
	AgentName         string
	AgentToken        string
	MasterURL         string
	DataDir           string
	HeartbeatInterval time.Duration
	CurrentVersion    string
}

func LoadFromEnv() (Config, error) {
	cfg := Config{
		AgentID:           strings.TrimSpace(os.Getenv("NRE_AGENT_ID")),
		AgentName:         strings.TrimSpace(os.Getenv("NRE_AGENT_NAME")),
		AgentToken:        strings.TrimSpace(os.Getenv("NRE_AGENT_TOKEN")),
		MasterURL:         strings.TrimRight(strings.TrimSpace(os.Getenv("NRE_MASTER_URL")), "/"),
		DataDir:           defaultString(os.Getenv("NRE_DATA_DIR"), "./agent-data"),
		HeartbeatInterval: 10 * time.Second,
		CurrentVersion:    defaultString(os.Getenv("NRE_AGENT_VERSION"), "dev"),
	}
	if cfg.MasterURL == "" {
		return Config{}, fmt.Errorf("NRE_MASTER_URL is required")
	}
	if cfg.AgentToken == "" {
		return Config{}, fmt.Errorf("NRE_AGENT_TOKEN is required")
	}
	return cfg, nil
}
```

- [ ] **Step 4: Run the config test to verify it passes**

Run: `cd go-agent && go test ./internal/config -run TestLoadFromEnv -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add go-agent/internal/config/config.go go-agent/internal/config/config_test.go go-agent/cmd/nre-agent/main.go
git commit -m "feat(go-agent): load runtime config from env"
```

## Task 3: Add persistent snapshot/runtime store

**Files:**
- Create: `go-agent/internal/store/filesystem.go`
- Create: `go-agent/internal/store/filesystem_test.go`
- Modify: `go-agent/internal/store/store.go`
- Modify: `go-agent/internal/model/types.go`

- [ ] **Step 1: Write the failing persistence test**

```go
// go-agent/internal/store/filesystem_test.go
func TestFilesystemStorePersistsAppliedSnapshot(t *testing.T) {
	dir := t.TempDir()
	s, err := NewFilesystem(dir)
	if err != nil {
		t.Fatalf("NewFilesystem returned error: %v", err)
	}

	err = s.SaveAppliedSnapshot(model.Snapshot{DesiredVersion: "1.2.3"})
	if err != nil {
		t.Fatalf("SaveAppliedSnapshot returned error: %v", err)
	}

	s2, err := NewFilesystem(dir)
	if err != nil {
		t.Fatalf("NewFilesystem second init returned error: %v", err)
	}
	got, err := s2.LoadAppliedSnapshot()
	if err != nil {
		t.Fatalf("LoadAppliedSnapshot returned error: %v", err)
	}
	if got.DesiredVersion != "1.2.3" {
		t.Fatalf("expected desired version to persist, got %q", got.DesiredVersion)
	}
}
```

- [ ] **Step 2: Run the store test to verify it fails**

Run: `cd go-agent && go test ./internal/store -run TestFilesystemStorePersistsAppliedSnapshot -v`
Expected: FAIL because filesystem store methods do not exist.

- [ ] **Step 3: Implement the persistent store**

```go
// go-agent/internal/store/store.go
type Store interface {
	SaveDesiredSnapshot(snapshot model.Snapshot) error
	LoadDesiredSnapshot() (model.Snapshot, error)
	SaveAppliedSnapshot(snapshot model.Snapshot) error
	LoadAppliedSnapshot() (model.Snapshot, error)
	SaveRuntimeState(state model.RuntimeState) error
	LoadRuntimeState() (model.RuntimeState, error)
}
```

```go
// go-agent/internal/store/filesystem.go
type Filesystem struct { root string }

func NewFilesystem(root string) (*Filesystem, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	return &Filesystem{root: root}, nil
}
```

- [ ] **Step 4: Run the store test to verify it passes**

Run: `cd go-agent && go test ./internal/store -run TestFilesystemStorePersistsAppliedSnapshot -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add go-agent/internal/store/store.go go-agent/internal/store/filesystem.go go-agent/internal/store/filesystem_test.go go-agent/internal/model/types.go
git commit -m "feat(go-agent): persist snapshots and runtime state"
```

## Task 4: Implement real heartbeat pull and app loop

**Files:**
- Modify: `go-agent/internal/sync/client.go`
- Create: `go-agent/internal/sync/client_test.go`
- Modify: `go-agent/internal/app/app.go`
- Modify: `go-agent/internal/app/app_test.go`

- [ ] **Step 1: Write the failing sync test**

```go
// go-agent/internal/sync/client_test.go
func TestClientSyncLoadsSnapshot(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"ok":true,"sync":{"desired_revision":7,"desired_version":"1.2.3","rules":[],"l4_rules":[],"relay_listeners":[]}}`)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "token")
	snap, err := c.Sync(context.Background(), model.RuntimeState{})
	if err != nil {
		t.Fatalf("Sync returned error: %v", err)
	}
	if snap.DesiredVersion != "1.2.3" {
		t.Fatalf("expected desired version 1.2.3, got %q", snap.DesiredVersion)
	}
}
```

- [ ] **Step 2: Run the sync test to verify it fails**

Run: `cd go-agent && go test ./internal/sync -run TestClientSyncLoadsSnapshot -v`
Expected: FAIL because `Sync` does not return a snapshot.

- [ ] **Step 3: Implement the sync client and long-running loop**

```go
// go-agent/internal/sync/client.go
func (c *Client) Sync(ctx context.Context, state model.RuntimeState) (model.Snapshot, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.masterURL+"/panel-api/agents/heartbeat", bytes.NewReader(body))
	// decode response into model.Snapshot and return it
}
```

```go
// go-agent/internal/app/app.go
func (a *App) Run(ctx context.Context) error {
	ticker := time.NewTicker(a.cfg.HeartbeatInterval)
	defer ticker.Stop()

	if err := a.syncOnce(ctx); err != nil && a.storeIsEmpty() {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := a.syncOnce(ctx); err != nil {
				a.recordSyncError(err)
			}
		}
	}
}
```

- [ ] **Step 4: Run the sync and app tests to verify they pass**

Run: `cd go-agent && go test ./internal/sync ./internal/app -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add go-agent/internal/sync/client.go go-agent/internal/sync/client_test.go go-agent/internal/app/app.go go-agent/internal/app/app_test.go
git commit -m "feat(go-agent): add heartbeat pull loop"
```

## Task 5: Add runtime orchestrator with atomic apply/rollback

**Files:**
- Modify: `go-agent/internal/runtime/runtime.go`
- Create: `go-agent/internal/runtime/runtime_test.go`
- Modify: `go-agent/internal/model/types.go`

- [ ] **Step 1: Write the failing runtime rollback test**

```go
// go-agent/internal/runtime/runtime_test.go
func TestApplyKeepsPreviousRuntimeOnFailure(t *testing.T) {
	rt := New()
	previous := model.Snapshot{DesiredVersion: "1.0.0"}
	next := model.Snapshot{DesiredVersion: "1.1.0"}

	rt.SetFailNextApply(errors.New("bind failed"))
	err := rt.Apply(context.Background(), previous, next)
	if err == nil {
		t.Fatal("expected apply failure")
	}
	if got := rt.ActiveSnapshot(); got.DesiredVersion != "1.0.0" {
		t.Fatalf("expected previous snapshot to stay active, got %q", got.DesiredVersion)
	}
}
```

- [ ] **Step 2: Run the runtime test to verify it fails**

Run: `cd go-agent && go test ./internal/runtime -run TestApplyKeepsPreviousRuntimeOnFailure -v`
Expected: FAIL because runtime orchestration methods do not exist.

- [ ] **Step 3: Implement the runtime orchestrator**

```go
// go-agent/internal/runtime/runtime.go
type Runtime struct {
	mu     sync.RWMutex
	active model.Snapshot
	http   *proxy.Server
	l4     *l4.Server
	relay  *relay.Server
}

func (r *Runtime) Apply(ctx context.Context, previous, next model.Snapshot) error {
	// prepare new listeners, swap only on success, keep previous active on error
	return nil
}
```

- [ ] **Step 4: Run the runtime test to verify it passes**

Run: `cd go-agent && go test ./internal/runtime -run TestApplyKeepsPreviousRuntimeOnFailure -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add go-agent/internal/runtime/runtime.go go-agent/internal/runtime/runtime_test.go go-agent/internal/model/types.go
git commit -m "feat(go-agent): add atomic runtime apply orchestration"
```

## Task 6: Build the HTTP/HTTPS runtime

**Files:**
- Create: `go-agent/internal/proxy/server.go`
- Create: `go-agent/internal/proxy/server_test.go`
- Modify: `go-agent/internal/proxy/http_engine.go`
- Modify: `go-agent/internal/model/http.go`
- Modify: `go-agent/internal/model/types.go`

- [ ] **Step 1: Write the failing HTTP runtime test**

```go
// go-agent/internal/proxy/server_test.go
func TestServerRoutesByHostAndRewritesLocation(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "http://backend.internal/next")
		w.WriteHeader(http.StatusFound)
	}))
	defer backend.Close()

	srv := NewServer(model.HTTPListener{
		FrontendOrigin: "https://edge.example.com",
		Routes: []model.HTTPRoute{{Host: "edge.example.com", BackendURL: backend.URL}},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "https://edge.example.com/", nil)
	req.Host = "edge.example.com"
	srv.ServeHTTP(rec, req)

	if got := rec.Header().Get("Location"); got != "https://edge.example.com/next" {
		t.Fatalf("unexpected location rewrite: %q", got)
	}
}
```

- [ ] **Step 2: Run the HTTP runtime test to verify it fails**

Run: `cd go-agent && go test ./internal/proxy -run TestServerRoutesByHostAndRewritesLocation -v`
Expected: FAIL because the runtime server does not exist.

- [ ] **Step 3: Implement the HTTP runtime**

```go
// go-agent/internal/proxy/server.go
type Server struct {
	handler atomic.Pointer[http.Handler]
}

func NewServer(listener model.HTTPListener) *Server {
	s := &Server{}
	h := buildHandler(listener)
	s.handler.Store(&h)
	return s
}
```

- [ ] **Step 4: Run the HTTP runtime tests to verify they pass**

Run: `cd go-agent && go test ./internal/proxy -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add go-agent/internal/proxy/server.go go-agent/internal/proxy/server_test.go go-agent/internal/proxy/http_engine.go go-agent/internal/model/http.go go-agent/internal/model/types.go
git commit -m "feat(go-agent): add http and https runtime"
```

## Task 7: Build the L4 direct runtime

**Files:**
- Create: `go-agent/internal/l4/server.go`
- Create: `go-agent/internal/l4/server_test.go`
- Modify: `go-agent/internal/l4/engine.go`
- Modify: `go-agent/internal/model/l4.go`
- Modify: `go-agent/internal/model/types.go`

- [ ] **Step 1: Write the failing TCP direct test**

```go
// go-agent/internal/l4/server_test.go
func TestTCPDirectProxyForwardsData(t *testing.T) {
	upstream, upstreamAddr := newTCPEchoServer(t)
	defer upstream.Close()

	srv, addr := newDirectTCPServer(t, model.L4Rule{Protocol: "tcp", UpstreamAddr: upstreamAddr})
	defer srv.Close()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatalf("Write returned error: %v", err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("ReadFull returned error: %v", err)
	}
	if string(buf) != "ping" {
		t.Fatalf("unexpected payload: %q", string(buf))
	}
}
```

- [ ] **Step 2: Run the L4 test to verify it fails**

Run: `cd go-agent && go test ./internal/l4 -run TestTCPDirectProxyForwardsData -v`
Expected: FAIL because direct runtime does not exist.

- [ ] **Step 3: Implement TCP and UDP direct runtime**

```go
// go-agent/internal/l4/server.go
type Server struct {
	listeners []io.Closer
}

func (s *Server) StartTCP(rule model.L4Rule) error {
	// listen, accept, dial upstream, bidirectional copy
	return nil
}
```

- [ ] **Step 4: Run the L4 tests to verify they pass**

Run: `cd go-agent && go test ./internal/l4 -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add go-agent/internal/l4/server.go go-agent/internal/l4/server_test.go go-agent/internal/l4/engine.go go-agent/internal/model/l4.go go-agent/internal/model/types.go
git commit -m "feat(go-agent): add l4 direct runtime"
```

## Task 8: Build the TCP relay runtime

**Files:**
- Create: `go-agent/internal/relay/server.go`
- Create: `go-agent/internal/relay/server_test.go`
- Modify: `go-agent/internal/relay/runtime.go`
- Modify: `go-agent/internal/model/types.go`

- [ ] **Step 1: Write the failing one-hop relay test**

```go
// go-agent/internal/relay/server_test.go
func TestOneHopRelayForwardsTCP(t *testing.T) {
	target, targetAddr := newTCPEchoServer(t)
	defer target.Close()

	relaySrv, relayAddr := newRelayServer(t, model.RelayListener{
		TLSMode: "pin_or_ca",
		PinSet: []string{"pin"},
	})
	defer relaySrv.Close()

	err := relaySrv.Forward("trace-1", []string{}, targetAddr)
	if err != nil {
		t.Fatalf("Forward returned error: %v", err)
	}
	_ = relayAddr
}
```

- [ ] **Step 2: Run the relay test to verify it fails**

Run: `cd go-agent && go test ./internal/relay -run TestOneHopRelayForwardsTCP -v`
Expected: FAIL because relay runtime does not exist.

- [ ] **Step 3: Implement one-hop and multi-hop relay**

```go
// go-agent/internal/relay/server.go
type TunnelRequest struct {
	TraceID       string   `json:"trace_id"`
	RemainingHops []string `json:"remaining_hops"`
	TargetAddr    string   `json:"target_addr"`
}
```

- [ ] **Step 4: Run the relay tests to verify they pass**

Run: `cd go-agent && go test ./internal/relay -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add go-agent/internal/relay/server.go go-agent/internal/relay/server_test.go go-agent/internal/relay/runtime.go go-agent/internal/model/types.go
git commit -m "feat(go-agent): add tcp relay runtime"
```

## Task 9: Build certificate store, loader, issuer, and renewal loop

**Files:**
- Create: `go-agent/internal/certs/store.go`
- Create: `go-agent/internal/certs/store_test.go`
- Create: `go-agent/internal/certs/issuer.go`
- Create: `go-agent/internal/certs/issuer_test.go`
- Modify: `go-agent/internal/certs/manager.go`
- Modify: `go-agent/internal/model/types.go`

- [ ] **Step 1: Write the failing certificate reload test**

```go
// go-agent/internal/certs/store_test.go
func TestStoreLoadsCertificateBundle(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore returned error: %v", err)
	}
	err = store.SaveMaterial("edge-cert", []byte(testCertPEM), []byte(testKeyPEM))
	if err != nil {
		t.Fatalf("SaveMaterial returned error: %v", err)
	}
	_, err = store.LoadTLSCertificate("edge-cert")
	if err != nil {
		t.Fatalf("LoadTLSCertificate returned error: %v", err)
	}
}
```

- [ ] **Step 2: Run the cert test to verify it fails**

Run: `cd go-agent && go test ./internal/certs -run TestStoreLoadsCertificateBundle -v`
Expected: FAIL because cert store methods do not exist.

- [ ] **Step 3: Implement cert store and issuer primitives**

```go
// go-agent/internal/certs/store.go
type Store struct { root string }

func (s *Store) SaveMaterial(id string, certPEM, keyPEM []byte) error {
	// persist cert and key under data/certs
	return nil
}
```

```go
// go-agent/internal/certs/issuer.go
func (m *Manager) RenewDueCertificates(ctx context.Context, now time.Time) error {
	// scan due certs, renew with backoff, save material, notify bindings
	return nil
}
```

- [ ] **Step 4: Run the cert tests to verify they pass**

Run: `cd go-agent && go test ./internal/certs -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add go-agent/internal/certs/store.go go-agent/internal/certs/store_test.go go-agent/internal/certs/issuer.go go-agent/internal/certs/issuer_test.go go-agent/internal/certs/manager.go go-agent/internal/model/types.go
git commit -m "feat(go-agent): add cert store and issuer runtime"
```

## Task 10: Build self-update with staging and process handoff

**Files:**
- Create: `go-agent/internal/update/manager.go`
- Modify: `go-agent/internal/update/updater.go`
- Modify: `go-agent/internal/update/updater_test.go`
- Create: `go-agent/internal/update/manager_test.go`
- Modify: `go-agent/internal/platform/linux/service.go`

- [ ] **Step 1: Write the failing update staging test**

```go
// go-agent/internal/update/manager_test.go
func TestStageUpdateVerifiesHash(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)
	err := mgr.Stage(model.VersionPackage{
		URL:    "file://" + filepath.Join("testdata", "nre-agent"),
		SHA256: "deadbeef",
	})
	if err == nil {
		t.Fatal("expected hash verification failure")
	}
}
```

- [ ] **Step 2: Run the update test to verify it fails**

Run: `cd go-agent && go test ./internal/update -run TestStageUpdateVerifiesHash -v`
Expected: FAIL because update manager does not exist.

- [ ] **Step 3: Implement update staging and handoff**

```go
// go-agent/internal/update/manager.go
func (m *Manager) Stage(pkg model.VersionPackage) (string, error) {
	// download, verify sha256, write staged binary
	return stagedPath, nil
}
```

```go
// go-agent/internal/platform/linux/service.go
func ExecReplacement(binary string, argv []string, env []string) error {
	return syscall.Exec(binary, argv, env)
}
```

- [ ] **Step 4: Run the update tests to verify they pass**

Run: `cd go-agent && go test ./internal/update -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add go-agent/internal/update/manager.go go-agent/internal/update/manager_test.go go-agent/internal/update/updater.go go-agent/internal/update/updater_test.go go-agent/internal/platform/linux/service.go
git commit -m "feat(go-agent): add linux self-update flow"
```

## Task 11: Integrate local Go agent mode and remove false local apply paths

**Files:**
- Modify: `panel/backend/server.js`
- Modify: `go-agent/internal/app/app.go`
- Modify: `go-agent/internal/runtime/runtime.go`
- Test: `panel/backend/tests/runtime-packaging.test.js`

- [ ] **Step 1: Write the failing local-agent integration test**

```js
// add to panel/backend/tests/runtime-packaging.test.js
it("exposes control-plane runtime while local execution is delegated to go agent", async () => {
  const payload = await fetchInfoPayload();
  assert.equal(payload.mode, "master");
  assert.equal(payload.local_apply_runtime, "go-agent");
});
```

- [ ] **Step 2: Run the targeted test to verify it fails**

Run: `cd panel/backend && node --test tests/runtime-packaging.test.js`
Expected: FAIL because the new runtime marker does not exist.

- [ ] **Step 3: Implement local-agent integration**

```js
// panel/backend/server.js
sendJson(res, 200, {
  ok: true,
  mode: PANEL_ROLE,
  local_apply_runtime: "go-agent",
});
```

- [ ] **Step 4: Run the targeted test to verify it passes**

Run: `cd panel/backend && node --test tests/runtime-packaging.test.js`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add panel/backend/server.js panel/backend/tests/runtime-packaging.test.js go-agent/internal/app/app.go go-agent/internal/runtime/runtime.go
git commit -m "feat(runtime): integrate local go agent execution"
```

## Task 12: Full-system verification and documentation cleanup

**Files:**
- Modify: `README.md`
- Modify: `AGENT_EXAMPLES.md`
- Modify: `CLAUDE.md`

- [ ] **Step 1: Run the backend suite**

Run: `cd panel/backend && npm test`
Expected: PASS with 0 failures.

- [ ] **Step 2: Run the backend syntax check**

Run: `cd panel/backend && node --check server.js`
Expected: PASS with no output.

- [ ] **Step 3: Run the frontend build**

Run: `cd panel/frontend && npm run build`
Expected: PASS. Existing UnoCSS shortcut warnings are acceptable unless new failures appear.

- [ ] **Step 4: Run the Go suite**

Run: `cd go-agent && go test ./...`
Expected: PASS.

- [ ] **Step 5: Run the image build**

Run: `docker build -t nginx-reverse-emby .`
Expected: PASS.

- [ ] **Step 6: Run the control-plane container**

Run: `docker compose up -d`
Expected: control-plane service starts successfully.

- [ ] **Step 7: Run smoke checks**

Run: `curl -I http://127.0.0.1:3000`
Expected: `HTTP/1.1 200 OK` or equivalent frontend success response.

Run: `curl -I http://127.0.0.1/panel-api/health`
Expected: `HTTP/1.1 200 OK`.

- [ ] **Step 8: Verify Linux runtime acceptance criteria manually**

Check all of:

- Go agent starts from persisted applied snapshot
- HTTP direct works
- HTTPS works with loaded certs
- TCP direct works
- UDP direct works
- TCP relay one-hop works
- TCP relay multi-hop works
- UDP relay is rejected
- cert issuance/renewal updates bound listeners
- desired-version self-update succeeds and reports back
- local Go agent follows the same runtime path

- [ ] **Step 9: Commit final runtime completion cleanup**

```bash
git add README.md AGENT_EXAMPLES.md CLAUDE.md
git commit -m "chore(runtime): finalize linux go execution plane"
```

## Self-Review

- Spec coverage:
  - pure-Go Linux agent: covered by Tasks 2-11
  - heartbeat pull + snapshots: covered by Tasks 3-4
  - HTTP runtime: covered by Task 6
  - L4 direct: covered by Task 7
  - TCP relay: covered by Task 8
  - certificates: covered by Task 9
  - self-update: covered by Task 10
  - local Go agent: covered by Task 11
  - verification: covered by Task 12
- Placeholder scan:
  - no TBD/TODO placeholders remain in tasks
- Type consistency:
  - `model.Snapshot`, `model.RuntimeState`, `model.VersionPackage`, `model.HTTPListener`, and `model.L4Rule` are referenced consistently and must be introduced in the earlier modeling tasks before later runtime tasks rely on them
