# Go Agent Runtime Replaces Nginx Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the Go runtime the only HTTP/L4/relay/TLS data plane for both the embedded master runtime and remote agents.

**Architecture:** Reuse the existing `internal/runtime`, `internal/proxy`, `internal/l4`, and `internal/relay` packages rather than building a second runtime. Expand the current runtime to cover the current control-plane rule semantics, then wire snapshot activation, rollback, and runtime-state persistence so Nginx is no longer required.

**Tech Stack:** Go 1.24, stdlib `net/http`, existing `internal/runtime`, `internal/proxy`, `internal/l4`, `internal/relay`, cert material provider.

---

## File Map

**Create**
- `go-agent/internal/proxy/runtime_integration_test.go`
- `go-agent/internal/runtime/activation_test.go`
- `go-agent/internal/runtime/state_test.go`

**Modify**
- `go-agent/internal/proxy/server.go`
- `go-agent/internal/proxy/http_engine.go`
- `go-agent/internal/l4/server.go`
- `go-agent/internal/relay/runtime.go`
- `go-agent/internal/runtime/runtime.go`
- `go-agent/internal/app/app.go`

### Task 1: Cover HTTP rule semantics used by the panel API

**Files:**
- Modify: `go-agent/internal/proxy/server.go`
- Modify: `go-agent/internal/proxy/http_engine.go`
- Test: `go-agent/internal/proxy/runtime_integration_test.go`

- [ ] **Step 1: Write the failing runtime integration test**

```go
func TestHTTPRuntimeAppliesHostHeadersProxyRedirectAndRoundRobin(t *testing.T) {
	backend1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Backend", "one")
		w.WriteHeader(http.StatusOK)
	}))
	defer backend1.Close()
	backend2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Backend", "two")
		w.WriteHeader(http.StatusOK)
	}))
	defer backend2.Close()
	rules := []model.HTTPRule{{
		ID:               1,
		FrontendURL:      "https://media.example.com",
		Enabled:          true,
		ProxyRedirect:    true,
		PassProxyHeaders: true,
		Backends: []model.HTTPBackend{{URL: backend1.URL}, {URL: backend2.URL}},
		LoadBalancing:    model.LoadBalancingPolicy{Strategy: "round_robin"},
	}}
	server, baseURL := startHTTPRuntimeForTest(t, rules, nil) // define this helper in the same test file using httptest.NewServer
	defer server.Close()
	req1, _ := http.NewRequest(http.MethodGet, baseURL, nil)
	req1.Host = "media.example.com"
	resp1, _ := http.DefaultClient.Do(req1)
	req2, _ := http.NewRequest(http.MethodGet, baseURL, nil)
	req2.Host = "media.example.com"
	resp2, _ := http.DefaultClient.Do(req2)
	if resp1.Header.Get("X-Backend") == resp2.Header.Get("X-Backend") {
		t.Fatalf("load balancer did not rotate backends")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd go-agent && go test ./internal/proxy -run TestHTTPRuntimeAppliesHostHeadersProxyRedirectAndRoundRobin -v`
Expected: FAIL because host matching or balancing is incomplete

- [ ] **Step 3: Implement missing HTTP runtime behavior**

```go
func (s *Server) applyRules(rules []model.HTTPRule, provider TLSMaterialProvider) error {
	index := make(map[string]compiledRule, len(rules))
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		host := HostFromRule(rule)
		if host == "" {
			continue
		}
		index[host] = compileRule(rule, provider)
	}
	s.mu.Lock()
	s.rules = index
	s.mu.Unlock()
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd go-agent && go test ./internal/proxy -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add go-agent/internal/proxy/server.go go-agent/internal/proxy/http_engine.go go-agent/internal/proxy/runtime_integration_test.go
git commit -m "feat(runtime): cover panel http rule semantics"
```

### Task 2: Integrate relay listeners and L4 rules into the same snapshot activation flow

**Files:**
- Modify: `go-agent/internal/l4/server.go`
- Modify: `go-agent/internal/relay/runtime.go`
- Modify: `go-agent/internal/runtime/runtime.go`
- Test: `go-agent/internal/runtime/activation_test.go`

- [ ] **Step 1: Write the failing snapshot activation test**

```go
func TestRuntimeActivatesHTTPRelayAndL4FromOneSnapshot(t *testing.T) {
	runtime := NewWithActivator(func(context.Context, store.Snapshot, store.Snapshot) error { return nil })
	next := store.Snapshot{
		Revision: 2,
		Rules: []model.HTTPRule{{ID: 1, FrontendURL: "https://media.example.com", Enabled: true, RelayChain: []int{10}}},
		L4Rules: []model.L4Rule{{ID: 2, Protocol: "tcp", ListenPort: 8443, Enabled: true}},
		RelayListeners: []model.RelayListener{{ID: 10, AgentID: "local", Enabled: true, ListenPort: 7443, BindHosts: []string{"127.0.0.1"}}},
	}
	if err := runtime.Apply(t.Context(), store.Snapshot{}, next); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd go-agent && go test ./internal/runtime -run TestRuntimeActivatesHTTPRelayAndL4FromOneSnapshot -v`
Expected: FAIL because relay/L4 activation is not coordinated enough

- [ ] **Step 3: Implement coordinated activation**

```go
func (r *Runtime) Apply(ctx context.Context, previous, next store.Snapshot) error {
	if err := r.activator(ctx, previous, next); err != nil {
		return err
	}
	r.mu.Lock()
	r.active = next
	r.state.CurrentRevision = next.Revision
	r.state.Status = "ready"
	r.mu.Unlock()
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd go-agent && go test ./internal/runtime ./internal/relay ./internal/l4 -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add go-agent/internal/l4/server.go go-agent/internal/relay/runtime.go go-agent/internal/runtime/runtime.go go-agent/internal/runtime/activation_test.go
git commit -m "feat(runtime): activate relay and l4 in unified snapshot flow"
```

### Task 3: Persist runtime state and rollback on activation failures

**Files:**
- Modify: `go-agent/internal/app/app.go`
- Modify: `go-agent/internal/runtime/runtime.go`
- Test: `go-agent/internal/runtime/state_test.go`

- [ ] **Step 1: Write the failing rollback test**

```go
func TestAppRollsBackRuntimeAndPersistsLastSyncError(t *testing.T) {
	store := newMemoryStore() // define an in-memory store test double in this file
	runtime := newStubHTTPApplier(errors.New("apply failed")) // define an HTTP applier test double in this file
	client := stubSyncClient{snapshot: store.Snapshot{}} // define a sync client test double in this file
	app := newAppWithHTTPDeps(config.Default(), store, client, runtime, nil, nil, nil)
	err := app.syncOnce(t.Context(), sync.SyncRequest{})
	if err == nil {
		t.Fatal("syncOnce() error = nil")
	}
	state, _ := store.LoadRuntimeState()
	if state.Metadata["last_sync_error"] == "" {
		t.Fatal("last_sync_error not persisted")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd go-agent && go test ./internal/app ./internal/runtime -run TestAppRollsBackRuntimeAndPersistsLastSyncError -v`
Expected: FAIL because runtime state persistence is incomplete

- [ ] **Step 3: Harden rollback and state persistence**

```go
if err := a.runtime.Apply(ctx, previousApplied, candidateApplied); err != nil {
	return a.recordRuntimeError(err)
}
if err := a.store.SaveAppliedSnapshot(candidateApplied); err != nil {
	a.rollbackRuntime(ctx, previousApplied)
	return a.recordPersistedRuntimeError(err)
}
return a.persistRuntimeState(true)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd go-agent && go test ./internal/app ./internal/runtime -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add go-agent/internal/app/app.go go-agent/internal/runtime/runtime.go go-agent/internal/runtime/state_test.go
git commit -m "fix(runtime): persist state and rollback on activation failures"
```
