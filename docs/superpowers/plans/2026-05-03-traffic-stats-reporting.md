# Traffic Stats Reporting Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add persisted latest cumulative traffic stats for HTTP rules, L4 rules, and Relay listeners, with a node-level stats reporting interval.

**Architecture:** Keep the existing heartbeat latest-stats JSON path and extend the agent `traffic` package with scoped counters keyed by rule/listener ID. Thread scoped recorders into HTTP, L4, and Relay byte-copy paths, add `agent_config.traffic_stats_interval` to control heartbeat stats emission frequency, and display per-object buckets from the existing agent stats endpoint in the Vue panel.

**Tech Stack:** Go 1.26, standard Go `net/http` and `sync/atomic`, GORM/SQLite, Vue 3/Vite, TanStack Vue Query.

---

## File Structure Map

Agent traffic runtime:

- Modify `go-agent/internal/traffic/traffic.go`: add keyed scoped counters and scoped recorder constructors.
- Modify `go-agent/internal/traffic/traffic_test.go`: test snapshot shape, reset behavior, disabled behavior, and non-zero behavior for scoped counters.
- Modify `go-agent/internal/proxy/server.go`: use HTTP rule-scoped recorders for request bodies, responses, upgrades, and normal proxy copies.
- Modify `go-agent/internal/proxy/resume.go`: record resumable response chunks through the route entry's HTTP rule recorder.
- Modify `go-agent/internal/proxy/traffic_test.go`: assert `traffic.http_rules[id]` alongside aggregate HTTP stats.
- Modify `go-agent/internal/l4/server.go`: use L4 rule-scoped recorders in TCP and proxy-entry copy paths and UDP sessions.
- Modify `go-agent/internal/l4/traffic_test.go`: assert `traffic.l4_rules[id]`.
- Modify `go-agent/internal/relay/runtime.go`: pass Relay listener-scoped recorders into TCP and UDP relay copy paths.
- Modify `go-agent/internal/relay/tls_tcp_session_pool.go`: pass Relay listener-scoped recorders into muxed stream copy paths.
- Modify `go-agent/internal/relay/traffic_test.go`: assert `traffic.relay_listeners[id]`.

Agent config and sync:

- Modify `go-agent/internal/model/types.go`: add `TrafficStatsInterval string` to `AgentConfig`.
- Modify `go-agent/internal/app/app.go`: activate and persist traffic stats interval metadata, and include stats only when the interval policy allows it.
- Modify `go-agent/internal/app/app_test.go`: cover omitted interval, configured interval, elapsed interval, and disabled stats clearing.
- Modify `go-agent/internal/model/snapshot_decode_test.go`: cover snapshot decode of `agent_config.traffic_stats_interval`.

Control plane:

- Modify `panel/backend-go/internal/controlplane/storage/sqlite_models.go`: add `TrafficStatsInterval` to `AgentRow`.
- Modify `panel/backend-go/internal/controlplane/storage/schema.go`: add migration and normalization for `agents.traffic_stats_interval`.
- Modify `panel/backend-go/internal/controlplane/storage/sqlite_store_test.go`: assert persistence.
- Modify `panel/backend-go/internal/controlplane/service/agents.go`: add API models, validation, revision bumping, summaries, and heartbeat reply field.
- Modify `panel/backend-go/internal/controlplane/service/agents_test.go`: assert update validation, revision bumping, and snapshot propagation.
- Modify `panel/backend-go/internal/controlplane/http/handlers_public.go`: include `traffic_stats_interval` in `agent_config`.
- Modify `panel/backend-go/internal/controlplane/http/public_test.go` and `router_test.go`: assert public heartbeat and panel update JSON.

Frontend:

- Create `panel/frontend/src/utils/trafficStats.js`: normalize buckets, format bytes, and look up per-object traffic maps.
- Modify `panel/frontend/src/pages/AgentDetailPage.vue`: reuse traffic helpers and add traffic stats interval setting.
- Modify `panel/frontend/src/pages/RulesPage.vue`: fetch agent stats and pass per-rule traffic to cards.
- Modify `panel/frontend/src/pages/L4RulesPage.vue`: fetch agent stats and pass per-rule traffic to cards.
- Modify `panel/frontend/src/pages/RelayListenersPage.vue`: fetch agent stats and pass per-listener traffic to cards.
- Modify `panel/frontend/src/components/rules/RuleCard.vue`: render HTTP rule traffic.
- Modify `panel/frontend/src/components/l4/L4RuleItem.vue`: render L4 rule traffic.
- Modify `panel/frontend/src/components/relay/RelayCard.vue`: render Relay listener traffic.
- Modify `panel/frontend/src/hooks/useAgents.js`: invalidate agent stats when updating agent settings.
- Modify `panel/frontend/src/api/devMocks/data.js`: add per-object traffic maps and mock `traffic_stats_interval`.
- Add or update focused frontend tests in `panel/frontend/src/pages/AgentDetailPage.test.js`, `panel/frontend/src/pages/AgentsPage.test.js`, and component smoke tests where present.

Docs:

- Modify `README.md`: document the node-level traffic stats interval once the field name and behavior are implemented.

---

## Task 1: Scoped Traffic Counters

**Files:**
- Modify: `go-agent/internal/traffic/traffic.go`
- Test: `go-agent/internal/traffic/traffic_test.go`

- [ ] **Step 1: Write failing scoped counter tests**

Add these tests to `go-agent/internal/traffic/traffic_test.go`:

```go
func TestScopedRecordersPopulatePerObjectBuckets(t *testing.T) {
	Reset()
	SetEnabled(true)
	defer Reset()

	NewHTTPRuleRecorder(11).Add(100, 200)
	NewL4RuleRecorder(21).Add(300, 400)
	NewRelayListenerRecorder(31).Add(500, 600)

	stats := Snapshot()["traffic"].(map[string]any)
	total := stats["total"].(map[string]uint64)
	if total["rx_bytes"] != 900 || total["tx_bytes"] != 1200 {
		t.Fatalf("total = %+v", total)
	}
	httpRules := stats["http_rules"].(map[string]map[string]uint64)
	l4Rules := stats["l4_rules"].(map[string]map[string]uint64)
	relayListeners := stats["relay_listeners"].(map[string]map[string]uint64)
	if httpRules["11"]["rx_bytes"] != 100 || httpRules["11"]["tx_bytes"] != 200 {
		t.Fatalf("http_rules[11] = %+v", httpRules["11"])
	}
	if l4Rules["21"]["rx_bytes"] != 300 || l4Rules["21"]["tx_bytes"] != 400 {
		t.Fatalf("l4_rules[21] = %+v", l4Rules["21"])
	}
	if relayListeners["31"]["rx_bytes"] != 500 || relayListeners["31"]["tx_bytes"] != 600 {
		t.Fatalf("relay_listeners[31] = %+v", relayListeners["31"])
	}
}

func TestResetClearsScopedBuckets(t *testing.T) {
	Reset()
	SetEnabled(true)
	NewHTTPRuleRecorder(11).Add(1, 2)
	NewL4RuleRecorder(21).Add(3, 4)
	NewRelayListenerRecorder(31).Add(5, 6)

	Reset()

	stats := Snapshot()["traffic"].(map[string]any)
	if got := len(stats["http_rules"].(map[string]map[string]uint64)); got != 0 {
		t.Fatalf("http_rules len = %d", got)
	}
	if got := len(stats["l4_rules"].(map[string]map[string]uint64)); got != 0 {
		t.Fatalf("l4_rules len = %d", got)
	}
	if got := len(stats["relay_listeners"].(map[string]map[string]uint64)); got != 0 {
		t.Fatalf("relay_listeners len = %d", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd go-agent && go test ./internal/traffic -run 'Test(ScopedRecordersPopulatePerObjectBuckets|ResetClearsScopedBuckets)' -count=1`

Expected: compile failure because `NewHTTPRuleRecorder`, `NewL4RuleRecorder`, and `NewRelayListenerRecorder` do not exist.

- [ ] **Step 3: Implement keyed counters and scoped recorders**

In `go-agent/internal/traffic/traffic.go`, extend `Recorder` and add keyed counter storage:

```go
type Recorder struct {
	counter *counters
	scoped  *counters
	rx      atomic.Uint64
	tx      atomic.Uint64
}

type keyedCounters struct {
	mu   sync.RWMutex
	byID map[int]*counters
}
```

Add a helper:

```go
func (k *keyedCounters) counterFor(id int) *counters {
	if id <= 0 {
		return nil
	}
	k.mu.RLock()
	counter := k.byID[id]
	k.mu.RUnlock()
	if counter != nil {
		return counter
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.byID == nil {
		k.byID = make(map[int]*counters)
	}
	if counter = k.byID[id]; counter == nil {
		counter = &counters{}
		k.byID[id] = counter
	}
	return counter
}
```

Add package variables:

```go
httpRuleCounters      keyedCounters
l4RuleCounters        keyedCounters
relayListenerCounters keyedCounters
```

Add constructors:

```go
func NewHTTPRuleRecorder(ruleID int) *Recorder {
	return &Recorder{counter: &httpCounters, scoped: httpRuleCounters.counterFor(ruleID)}
}

func NewL4RuleRecorder(ruleID int) *Recorder {
	return &Recorder{counter: &l4Counters, scoped: l4RuleCounters.counterFor(ruleID)}
}

func NewRelayListenerRecorder(listenerID int) *Recorder {
	return &Recorder{counter: &relayCounters, scoped: relayListenerCounters.counterFor(listenerID)}
}
```

Change `Flush` so it adds to both aggregate and scoped counters:

```go
func (r *Recorder) Flush() {
	if r == nil || r.counter == nil || !Enabled() {
		return
	}
	rx := r.rx.Swap(0)
	tx := r.tx.Swap(0)
	addUint64(r.counter, rx, tx)
	if r.scoped != nil {
		addUint64(r.scoped, rx, tx)
	}
}
```

Add snapshot helpers:

```go
func snapshotKeyedCounters(k *keyedCounters) map[string]map[string]uint64 {
	out := map[string]map[string]uint64{}
	k.mu.RLock()
	defer k.mu.RUnlock()
	for id, counter := range k.byID {
		bucket := snapshotCounters(counter)
		if bucket["rx_bytes"] == 0 && bucket["tx_bytes"] == 0 {
			continue
		}
		out[strconv.Itoa(id)] = bucket
	}
	return out
}
```

Include `http_rules`, `l4_rules`, and `relay_listeners` in `snapshot()`. Update `Reset()` to clear the keyed maps with locked helper methods.

- [ ] **Step 4: Run traffic tests**

Run: `cd go-agent && go test ./internal/traffic -count=1`

Expected: pass.

- [ ] **Step 5: Commit**

```bash
git add go-agent/internal/traffic/traffic.go go-agent/internal/traffic/traffic_test.go
git commit -m "feat(agent): add scoped traffic counters"
```

## Task 2: HTTP Rule Traffic Wiring

**Files:**
- Modify: `go-agent/internal/proxy/server.go`
- Modify: `go-agent/internal/proxy/resume.go`
- Test: `go-agent/internal/proxy/traffic_test.go`

- [ ] **Step 1: Write failing HTTP rule stats tests**

Add this test to `go-agent/internal/proxy/traffic_test.go`:

```go
func TestRouteEntryRecordsHTTPRuleTraffic(t *testing.T) {
	traffic.Reset()
	traffic.SetEnabled(true)
	defer traffic.Reset()

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			t.Fatalf("backend read body: %v", err)
		}
		_, _ = w.Write([]byte("response-body"))
	}))
	defer backend.Close()

	server := NewServer(model.HTTPListener{Rules: []model.HTTPRule{{
		ID:          77,
		FrontendURL: "http://frontend.example",
		BackendURL:  backend.URL,
		Enabled:     true,
	}}})
	req := httptest.NewRequest(http.MethodPost, "http://frontend.example/upload", bytes.NewBufferString("request-body"))
	req.Host = "frontend.example"
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q", rec.Code, rec.Body.String())
	}

	stats := traffic.Snapshot()["traffic"].(map[string]any)
	httpRules := stats["http_rules"].(map[string]map[string]uint64)
	got := httpRules["77"]
	if got["rx_bytes"] != uint64(len("request-body")) {
		t.Fatalf("http_rules[77].rx_bytes = %d", got["rx_bytes"])
	}
	if got["tx_bytes"] != uint64(len("response-body")) {
		t.Fatalf("http_rules[77].tx_bytes = %d", got["tx_bytes"])
	}
}
```

- [ ] **Step 2: Run test to verify failure**

Run: `cd go-agent && go test ./internal/proxy -run TestRouteEntryRecordsHTTPRuleTraffic -count=1`

Expected: fail because aggregate HTTP stats exist but `http_rules["77"]` remains empty.

- [ ] **Step 3: Thread rule-scoped recorder through HTTP request and response copy**

In `routeEntry.serveHTTP`, create a recorder once:

```go
recorder := traffic.NewHTTPRuleRecorder(e.rule.ID)
```

Pass it to request clone and response copy:

```go
attemptReq, err := cloneProxyRequest(req, body, candidate, e.rule, e.frontendPath, recorder)
```

Change `cloneProxyRequest` signature:

```go
func cloneProxyRequest(req *http.Request, body *reusableRequestBody, candidate httpCandidate, rule model.HTTPRule, frontendPath string, recorder *traffic.Recorder) (*http.Request, error)
```

Change `newTrafficReadCloser` to accept a recorder:

```go
func newTrafficReadCloser(delegate io.ReadCloser, recorder *traffic.Recorder) io.ReadCloser {
	return &trafficReadCloser{ReadCloser: delegate, recorder: recorder}
}
```

Update response copy calls:

```go
written, err := copyResponse(w, resp, recorder)
written, err := e.copyResumableResponse(w, attemptReq, resp, state, recorder)
if err := handleUpgradeResponse(w, attemptReq, resp, recorder); err != nil { ... }
```

Change `copyResponse`, `copyResumableResponse`, `handleUpgradeResponse`, and `switchProtocolCopier` to call `recorder.Add(...)` instead of `traffic.AddHTTP(...)`.

Keep backwards-compatible behavior by making nil recorders fall back to aggregate HTTP where helper-level tests still call `copyResponse` directly:

```go
func httpRecorderOrAggregate(recorder *traffic.Recorder) *traffic.Recorder {
	if recorder != nil {
		return recorder
	}
	return traffic.NewHTTPRecorder()
}
```

- [ ] **Step 4: Update existing direct helper tests**

Update existing `copyResponse` and `cloneProxyRequest` test calls in `go-agent/internal/proxy/traffic_test.go` and `server_test.go` to pass `nil` as the new recorder argument:

```go
copyResponse(recorder, resp, nil)
cloneProxyRequest(req, body, candidate, model.HTTPRule{}, "/", nil)
```

- [ ] **Step 5: Run HTTP proxy tests**

Run: `cd go-agent && go test ./internal/proxy -run 'Traffic|RouteEntryRecordsHTTPRuleTraffic|CloneProxyRequest|CopyResponse' -count=1`

Expected: pass.

- [ ] **Step 6: Commit**

```bash
git add go-agent/internal/proxy/server.go go-agent/internal/proxy/resume.go go-agent/internal/proxy/traffic_test.go go-agent/internal/proxy/server_test.go
git commit -m "feat(agent): record traffic per HTTP rule"
```

## Task 3: L4 Rule Traffic Wiring

**Files:**
- Modify: `go-agent/internal/l4/server.go`
- Test: `go-agent/internal/l4/traffic_test.go`

- [ ] **Step 1: Write failing L4 rule stats test**

Add this test to `go-agent/internal/l4/traffic_test.go`:

```go
func TestCopyBidirectionalTCPRecordsL4RuleTraffic(t *testing.T) {
	traffic.Reset()
	traffic.SetEnabled(true)
	defer traffic.Reset()

	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()

	done := make(chan struct{})
	go func() {
		copyBidirectionalTCP(left, right, traffic.NewL4RuleRecorder(42))
		close(done)
	}()

	if _, err := left.Write([]byte("client-to-upstream")); err != nil {
		t.Fatalf("left write error: %v", err)
	}
	readExact(t, right, len("client-to-upstream"))

	if _, err := right.Write([]byte("upstream-to-client")); err != nil {
		t.Fatalf("right write error: %v", err)
	}
	readExact(t, left, len("upstream-to-client"))

	_ = left.Close()
	_ = right.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("copyBidirectionalTCP did not exit")
	}

	stats := traffic.Snapshot()["traffic"].(map[string]any)
	l4Rules := stats["l4_rules"].(map[string]map[string]uint64)
	got := l4Rules["42"]
	if got["rx_bytes"] != uint64(len("client-to-upstream")) {
		t.Fatalf("l4_rules[42].rx_bytes = %d", got["rx_bytes"])
	}
	if got["tx_bytes"] != uint64(len("upstream-to-client")) {
		t.Fatalf("l4_rules[42].tx_bytes = %d", got["tx_bytes"])
	}
}
```

- [ ] **Step 2: Run test to verify failure**

Run: `cd go-agent && go test ./internal/l4 -run TestCopyBidirectionalTCPRecordsL4RuleTraffic -count=1`

Expected: compile failure because `copyBidirectionalTCP` has no recorder parameter.

- [ ] **Step 3: Thread L4 rule recorder through TCP paths**

In `handleTCPConnection`, create:

```go
recorder := traffic.NewL4RuleRecorder(rule.ID)
```

Use it in normal TCP copy goroutines:

```go
recorder.Add(n+int64(len(initialPayload)), 0)
recorder.Flush()
```

```go
recorder.Add(0, n)
recorder.Flush()
```

Change proxy entry call:

```go
s.handleProxyEntryConnection(client, rule, recorder)
```

Change `copyBidirectionalTCP` signature and implementation:

```go
func copyBidirectionalTCP(a net.Conn, b net.Conn, recorder *traffic.Recorder) {
	recorder = l4RecorderOrAggregate(recorder)
	...
	recorder.Add(n, 0)
	recorder.Flush()
	...
	recorder.Add(0, n)
	recorder.Flush()
}
```

Add helper:

```go
func l4RecorderOrAggregate(recorder *traffic.Recorder) *traffic.Recorder {
	if recorder != nil {
		return recorder
	}
	return traffic.NewL4Recorder()
}
```

For UDP sessions in `sessionForPeer`, replace `traffic.NewL4Recorder()` with `traffic.NewL4RuleRecorder(rule.ID)`.

- [ ] **Step 4: Update existing L4 tests**

Update existing `copyBidirectionalTCP(left, right)` calls in `go-agent/internal/l4/traffic_test.go` to `copyBidirectionalTCP(left, right, nil)` where they are only checking aggregate behavior.

- [ ] **Step 5: Run L4 tests**

Run: `cd go-agent && go test ./internal/l4 -run 'Traffic|CopyBidirectionalTCPRecordsL4RuleTraffic' -count=1`

Expected: pass.

- [ ] **Step 6: Commit**

```bash
git add go-agent/internal/l4/server.go go-agent/internal/l4/traffic_test.go
git commit -m "feat(agent): record traffic per L4 rule"
```

## Task 4: Relay Listener Traffic Wiring

**Files:**
- Modify: `go-agent/internal/relay/runtime.go`
- Modify: `go-agent/internal/relay/tls_tcp_session_pool.go`
- Test: `go-agent/internal/relay/traffic_test.go`

- [ ] **Step 1: Write failing Relay listener stats test**

Add this test to `go-agent/internal/relay/traffic_test.go`:

```go
func TestPipeBothWaysRecordsRelayListenerTraffic(t *testing.T) {
	traffic.Reset()
	traffic.SetEnabled(true)
	defer traffic.Reset()

	left, clientPeer := net.Pipe()
	right, upstreamPeer := net.Pipe()
	defer left.Close()
	defer clientPeer.Close()
	defer right.Close()
	defer upstreamPeer.Close()

	done := make(chan struct{})
	go func() {
		pipeBothWays(left, right, traffic.NewRelayListenerRecorder(99))
		close(done)
	}()

	if _, err := clientPeer.Write([]byte("relay-inbound")); err != nil {
		t.Fatalf("client write error: %v", err)
	}
	readRelayExact(t, upstreamPeer, len("relay-inbound"))

	if _, err := upstreamPeer.Write([]byte("relay-outbound")); err != nil {
		t.Fatalf("upstream write error: %v", err)
	}
	readRelayExact(t, clientPeer, len("relay-outbound"))

	_ = clientPeer.Close()
	_ = upstreamPeer.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("pipeBothWays did not exit")
	}

	stats := traffic.Snapshot()["traffic"].(map[string]any)
	listeners := stats["relay_listeners"].(map[string]map[string]uint64)
	got := listeners["99"]
	if got["rx_bytes"] != uint64(len("relay-inbound")) {
		t.Fatalf("relay_listeners[99].rx_bytes = %d", got["rx_bytes"])
	}
	if got["tx_bytes"] != uint64(len("relay-outbound")) {
		t.Fatalf("relay_listeners[99].tx_bytes = %d", got["tx_bytes"])
	}
}
```

- [ ] **Step 2: Run test to verify failure**

Run: `cd go-agent && go test ./internal/relay -run TestPipeBothWaysRecordsRelayListenerTraffic -count=1`

Expected: compile failure because `pipeBothWays` has no recorder parameter.

- [ ] **Step 3: Thread Relay listener recorder through non-mux copy paths**

Change function signatures:

```go
func pipeBothWays(left, right net.Conn, recorder *traffic.Recorder) {
	pipeBothWaysWithInitialRelayRX(left, right, 0, recorder)
}

func pipeBothWaysWithInitialRelayRX(left, right net.Conn, initialRX int64, recorder *traffic.Recorder) {
	recorder = relayRecorderOrAggregate(recorder)
	...
}

func pipeUDPPackets(clientConn net.Conn, upstream udpPacketPeer, recorder *traffic.Recorder) {
	recorder = relayRecorderOrAggregate(recorder)
	...
}
```

Add helper:

```go
func relayRecorderOrAggregate(recorder *traffic.Recorder) *traffic.Recorder {
	if recorder != nil {
		return recorder
	}
	return traffic.NewRelayRecorder()
}
```

In Relay handlers, pass listener-scoped recorders:

```go
recorder := traffic.NewRelayListenerRecorder(listener.ID)
pipeBothWaysWithInitialRelayRX(wrapIdleConn(clientConn), wrapIdleConn(upstream), int64(len(request.InitialData)), recorder)
pipeUDPPackets(relayClientConn, upstream, traffic.NewRelayListenerRecorder(listener.ID))
```

- [ ] **Step 4: Thread Relay listener recorder through muxed stream paths**

In `go-agent/internal/relay/tls_tcp_session_pool.go`, update stream handlers that receive `listener Listener`:

```go
recorder := traffic.NewRelayListenerRecorder(listener.ID)
pipeBothWaysWithInitialRelayRX(wrapIdleConn(stream), wrapIdleConn(upstream), int64(len(request.InitialData)), recorder)
pipeUDPPackets(stream, upstream, traffic.NewRelayListenerRecorder(listener.ID))
```

- [ ] **Step 5: Update existing Relay tests**

Update existing calls in `go-agent/internal/relay/traffic_test.go`:

```go
pipeBothWays(left, right, nil)
pipeBothWaysWithInitialRelayRX(left, right, int64(len(initial)), nil)
pipeUDPPackets(clientConn, upstream, nil)
```

- [ ] **Step 6: Run Relay tests**

Run: `cd go-agent && go test ./internal/relay -run 'Traffic|PipeBothWaysRecordsRelayListenerTraffic' -count=1`

Expected: pass.

- [ ] **Step 7: Commit**

```bash
git add go-agent/internal/relay/runtime.go go-agent/internal/relay/tls_tcp_session_pool.go go-agent/internal/relay/traffic_test.go
git commit -m "feat(agent): record traffic per relay listener"
```

## Task 5: Agent Stats Reporting Interval

**Files:**
- Modify: `go-agent/internal/model/types.go`
- Modify: `go-agent/internal/model/snapshot_decode_test.go`
- Modify: `go-agent/internal/app/app.go`
- Test: `go-agent/internal/app/app_test.go`

- [ ] **Step 1: Write failing model decode test**

Add to `go-agent/internal/model/snapshot_decode_test.go`:

```go
func TestSnapshotDecodePreservesTrafficStatsInterval(t *testing.T) {
	var snapshot Snapshot
	if err := json.Unmarshal([]byte(`{
		"desired_version":"next",
		"desired_revision":4,
		"agent_config":{"traffic_stats_interval":"30s"}
	}`), &snapshot); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if !snapshot.HasAgentConfig() {
		t.Fatal("HasAgentConfig() = false")
	}
	if snapshot.AgentConfig.TrafficStatsInterval != "30s" {
		t.Fatalf("TrafficStatsInterval = %q", snapshot.AgentConfig.TrafficStatsInterval)
	}
}
```

- [ ] **Step 2: Run model test to verify failure**

Run: `cd go-agent && go test ./internal/model -run TestSnapshotDecodePreservesTrafficStatsInterval -count=1`

Expected: compile failure because `TrafficStatsInterval` does not exist.

- [ ] **Step 3: Add model field**

In `go-agent/internal/model/types.go`:

```go
type AgentConfig struct {
	OutboundProxyURL      string `json:"outbound_proxy_url,omitempty"`
	TrafficStatsInterval string `json:"traffic_stats_interval,omitempty"`
}
```

- [ ] **Step 4: Write failing app reporting interval tests**

Add tests to `go-agent/internal/app/app_test.go` using existing `testSyncClient` and in-memory store helpers:

```go
func TestSyncRequestSuppressesStatsBeforeConfiguredTrafficStatsInterval(t *testing.T) {
	traffic.Reset()
	traffic.SetEnabled(true)
	defer traffic.Reset()
	traffic.AddHTTP(10, 20)

	st := store.NewInMemory()
	if err := st.SaveRuntimeState(store.RuntimeState{
		Metadata: map[string]string{
			"traffic_stats_interval":           "1h",
			"last_traffic_stats_report_unix":  strconv.FormatInt(time.Now().Add(-time.Minute).Unix(), 10),
			"last_apply_revision":             "1",
			"last_apply_status":               "success",
		},
	}); err != nil {
		t.Fatalf("SaveRuntimeState() error = %v", err)
	}
	app := newAppWithDeps(Config{HeartbeatInterval: time.Hour}, st, &testSyncClient{}, nil, nil, nil)
	req, err := app.syncRequest(context.Background(), Snapshot{Revision: 1})
	if err != nil {
		t.Fatalf("syncRequest() error = %v", err)
	}
	if req.Stats != nil || req.StatsPresent {
		t.Fatalf("stats included before interval: present=%v stats=%+v", req.StatsPresent, req.Stats)
	}
}

func TestSyncRequestIncludesStatsAfterConfiguredTrafficStatsInterval(t *testing.T) {
	traffic.Reset()
	traffic.SetEnabled(true)
	defer traffic.Reset()
	traffic.AddHTTP(10, 20)

	st := store.NewInMemory()
	if err := st.SaveRuntimeState(store.RuntimeState{
		Metadata: map[string]string{
			"traffic_stats_interval":          "1s",
			"last_traffic_stats_report_unix": strconv.FormatInt(time.Now().Add(-time.Hour).Unix(), 10),
			"last_apply_revision":            "1",
			"last_apply_status":              "success",
		},
	}); err != nil {
		t.Fatalf("SaveRuntimeState() error = %v", err)
	}
	app := newAppWithDeps(Config{HeartbeatInterval: time.Hour}, st, &testSyncClient{}, nil, nil, nil)
	req, err := app.syncRequest(context.Background(), Snapshot{Revision: 1})
	if err != nil {
		t.Fatalf("syncRequest() error = %v", err)
	}
	if req.Stats == nil {
		t.Fatal("Stats = nil, want traffic stats")
	}
}
```

- [ ] **Step 5: Run app tests to verify failure**

Run: `cd go-agent && go test ./internal/app -run 'TestSyncRequest(Includes|Suppresses)Stats.*TrafficStatsInterval' -count=1`

Expected: first test fails because current behavior includes non-zero stats every heartbeat.

- [ ] **Step 6: Implement interval activation and reporting gate**

In `snapshotActivationHandlers`, extend `ActivateAgentConfig`:

```go
ActivateAgentConfig: func(_ context.Context, cfg model.AgentConfig) error {
	relay.SetOutboundProxyURL(cfg.OutboundProxyURL)
	return a.persistTrafficStatsInterval(cfg.TrafficStatsInterval)
},
```

Add:

```go
const (
	runtimeMetaTrafficStatsInterval       = "traffic_stats_interval"
	runtimeMetaLastTrafficStatsReportUnix = "last_traffic_stats_report_unix"
)

func (a *App) persistTrafficStatsInterval(raw string) error {
	state, err := a.store.LoadRuntimeState()
	if err != nil {
		return err
	}
	state.Metadata = ensureMetadata(state.Metadata)
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		delete(state.Metadata, runtimeMetaTrafficStatsInterval)
	} else {
		if _, err := time.ParseDuration(trimmed); err != nil {
			return fmt.Errorf("invalid traffic_stats_interval: %w", err)
		}
		state.Metadata[runtimeMetaTrafficStatsInterval] = trimmed
	}
	return a.store.SaveRuntimeState(state)
}
```

In `syncRequest`, replace direct `traffic.SnapshotNonZero()` logic with:

```go
if !traffic.Enabled() {
	req.Stats = map[string]any{}
	req.StatsPresent = true
} else if a.shouldReportTrafficStats(meta, time.Now()) {
	if stats := traffic.SnapshotNonZero(); stats != nil {
		req.Stats = stats
		meta[runtimeMetaLastTrafficStatsReportUnix] = strconv.FormatInt(time.Now().Unix(), 10)
		_ = a.store.SaveRuntimeState(state)
	}
}
```

Add:

```go
func (a *App) shouldReportTrafficStats(meta map[string]string, now time.Time) bool {
	raw := strings.TrimSpace(meta[runtimeMetaTrafficStatsInterval])
	if raw == "" {
		return true
	}
	interval, err := time.ParseDuration(raw)
	if err != nil || interval <= 0 {
		return true
	}
	lastUnix, err := strconv.ParseInt(strings.TrimSpace(meta[runtimeMetaLastTrafficStatsReportUnix]), 10, 64)
	if err != nil || lastUnix <= 0 {
		return true
	}
	return !now.Before(time.Unix(lastUnix, 0).Add(interval))
}
```

- [ ] **Step 7: Run model and app tests**

Run:

```bash
cd go-agent && go test ./internal/model -run TestSnapshotDecodePreservesTrafficStatsInterval -count=1
cd go-agent && go test ./internal/app -run 'TestSyncRequest(Includes|Suppresses)Stats.*TrafficStatsInterval|TestPerformSyncClearsTrafficStatsWhenDisabled' -count=1
```

Expected: pass.

- [ ] **Step 8: Commit**

```bash
git add go-agent/internal/model/types.go go-agent/internal/model/snapshot_decode_test.go go-agent/internal/app/app.go go-agent/internal/app/app_test.go
git commit -m "feat(agent): support traffic stats reporting interval"
```

## Task 6: Control-Plane Traffic Stats Interval

**Files:**
- Modify: `panel/backend-go/internal/controlplane/storage/sqlite_models.go`
- Modify: `panel/backend-go/internal/controlplane/storage/schema.go`
- Test: `panel/backend-go/internal/controlplane/storage/sqlite_store_test.go`
- Modify: `panel/backend-go/internal/controlplane/service/agents.go`
- Test: `panel/backend-go/internal/controlplane/service/agents_test.go`
- Modify: `panel/backend-go/internal/controlplane/http/handlers_public.go`
- Test: `panel/backend-go/internal/controlplane/http/public_test.go`
- Test: `panel/backend-go/internal/controlplane/http/router_test.go`

- [ ] **Step 1: Write failing storage test**

Add to `panel/backend-go/internal/controlplane/storage/sqlite_store_test.go`:

```go
func TestSQLiteStorePersistsAgentTrafficStatsInterval(t *testing.T) {
	ctx := context.Background()
	store := newTestSQLiteStore(t)
	agent := AgentRow{
		ID:                   "edge-a",
		Name:                 "Edge A",
		AgentToken:           "token-a",
		TrafficStatsInterval: "30s",
	}
	if err := store.SaveAgent(ctx, agent); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	agents, err := store.ListAgents(ctx)
	if err != nil {
		t.Fatalf("ListAgents() error = %v", err)
	}
	if len(agents) != 1 {
		t.Fatalf("ListAgents() len = %d", len(agents))
	}
	if agents[0].TrafficStatsInterval != "30s" {
		t.Fatalf("TrafficStatsInterval = %q", agents[0].TrafficStatsInterval)
	}
}
```

- [ ] **Step 2: Run storage test to verify failure**

Run: `cd panel/backend-go && go test ./internal/controlplane/storage -run TestSQLiteStorePersistsAgentTrafficStatsInterval -count=1`

Expected: compile failure because `TrafficStatsInterval` does not exist on `AgentRow`.

- [ ] **Step 3: Add storage field and migration**

In `AgentRow`:

```go
TrafficStatsInterval string `gorm:"column:traffic_stats_interval;not null;default:''"`
```

In `BootstrapSQLiteSchema`, add migration:

```go
{column: "traffic_stats_interval", sql: `ALTER TABLE agents ADD COLUMN traffic_stats_interval TEXT NOT NULL DEFAULT ''`},
```

Add normalization:

```go
`UPDATE agents SET traffic_stats_interval = '' WHERE traffic_stats_interval IS NULL`,
```

- [ ] **Step 4: Write failing service and HTTP tests**

Add to `panel/backend-go/internal/controlplane/service/agents_test.go`:

```go
func TestAgentServiceUpdatePersistsTrafficStatsInterval(t *testing.T) {
	store := newFakeAgentStore()
	store.savedAgents = []storage.AgentRow{{
		ID: "edge-a", Name: "Edge A", AgentToken: "token-a", LastApplyStatus: "success",
	}}
	svc := NewAgentService(config.Config{LocalAgentID: "local"}, store)

	agent, err := svc.Update(context.Background(), "edge-a", UpdateAgentRequest{
		TrafficStatsInterval: stringPtr("30s"),
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if agent.TrafficStatsInterval != "30s" || store.savedAgent.TrafficStatsInterval != "30s" {
		t.Fatalf("TrafficStatsInterval agent=%q saved=%q", agent.TrafficStatsInterval, store.savedAgent.TrafficStatsInterval)
	}
	if store.savedAgent.DesiredRevision <= 0 {
		t.Fatalf("DesiredRevision = %d, want bumped", store.savedAgent.DesiredRevision)
	}
}

func TestAgentServiceUpdateRejectsInvalidTrafficStatsInterval(t *testing.T) {
	store := newFakeAgentStore()
	store.savedAgents = []storage.AgentRow{{
		ID: "edge-a", Name: "Edge A", AgentToken: "token-a", LastApplyStatus: "success",
	}}
	svc := NewAgentService(config.Config{LocalAgentID: "local"}, store)

	_, err := svc.Update(context.Background(), "edge-a", UpdateAgentRequest{
		TrafficStatsInterval: stringPtr("0s"),
	})
	if err == nil || !strings.Contains(err.Error(), "traffic_stats_interval") {
		t.Fatalf("Update() error = %v, want traffic_stats_interval validation", err)
	}
}
```

Add to `panel/backend-go/internal/controlplane/http/public_test.go`:

```go
func TestHeartbeatResponseIncludesTrafficStatsInterval(t *testing.T) {
	state := newTestRouterState(t)
	state.agentService.reply = service.HeartbeatReply{
		DesiredRevision:       1,
		TrafficStatsInterval:  "30s",
	}
	req := httptest.NewRequest(http.MethodPost, "/api/agents/heartbeat", strings.NewReader(`{"current_revision":0}`))
	req.Header.Set("X-Agent-Token", "token")
	rec := httptest.NewRecorder()
	state.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Sync struct {
			AgentConfig struct {
				TrafficStatsInterval string `json:"traffic_stats_interval"`
			} `json:"agent_config"`
		} `json:"sync"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if payload.Sync.AgentConfig.TrafficStatsInterval != "30s" {
		t.Fatalf("traffic_stats_interval = %q", payload.Sync.AgentConfig.TrafficStatsInterval)
	}
}
```

- [ ] **Step 5: Run service and HTTP tests to verify failure**

Run:

```bash
cd panel/backend-go && go test ./internal/controlplane/service -run 'TestAgentServiceUpdate(Persists|RejectsInvalid)TrafficStatsInterval' -count=1
cd panel/backend-go && go test ./internal/controlplane/http -run TestHeartbeatResponseIncludesTrafficStatsInterval -count=1
```

Expected: compile failure because service models do not expose the new field.

- [ ] **Step 6: Implement service models, validation, and heartbeat payload**

In `AgentSummary`:

```go
TrafficStatsInterval string `json:"traffic_stats_interval"`
```

In `UpdateAgentRequest`:

```go
TrafficStatsInterval *string `json:"traffic_stats_interval,omitempty"`
```

In `HeartbeatReply`:

```go
TrafficStatsInterval string `json:"-"`
```

In `AgentRuntimeConfig`:

```go
TrafficStatsInterval string `json:"traffic_stats_interval,omitempty"`
```

Add normalization:

```go
func normalizeTrafficStatsInterval(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}
	dur, err := time.ParseDuration(trimmed)
	if err != nil || dur <= 0 {
		return "", fmt.Errorf("traffic_stats_interval must be a positive duration")
	}
	return dur.String(), nil
}
```

In `Update`, handle field and revision bump:

```go
if input.TrafficStatsInterval != nil {
	previous := strings.TrimSpace(row.TrafficStatsInterval)
	next, err := normalizeTrafficStatsInterval(*input.TrafficStatsInterval)
	if err != nil {
		return AgentSummary{}, fmt.Errorf("%w: invalid traffic_stats_interval: %v", ErrInvalidArgument, err)
	}
	row.TrafficStatsInterval = next
	if next != previous {
		allocator, err := newConfigIdentityAllocatorFromStore(ctx, s.cfg, s.store)
		if err != nil {
			return AgentSummary{}, err
		}
		row.DesiredRevision = allocator.AllocateRevisionForAgent(row.ID, row.DesiredRevision)
	}
}
```

Populate summaries and heartbeat reply:

```go
TrafficStatsInterval: strings.TrimSpace(row.TrafficStatsInterval),
```

```go
TrafficStatsInterval: strings.TrimSpace(row.TrafficStatsInterval),
```

In `heartbeatSyncPayload`:

```go
payload["agent_config"] = service.AgentRuntimeConfig{
	OutboundProxyURL:      reply.OutboundProxyURL,
	TrafficStatsInterval: reply.TrafficStatsInterval,
}
```

- [ ] **Step 7: Run control-plane tests**

Run:

```bash
cd panel/backend-go && go test ./internal/controlplane/storage -run TestSQLiteStorePersistsAgentTrafficStatsInterval -count=1
cd panel/backend-go && go test ./internal/controlplane/service -run 'TestAgentServiceUpdate(Persists|RejectsInvalid)TrafficStatsInterval' -count=1
cd panel/backend-go && go test ./internal/controlplane/http -run 'TestHeartbeatResponseIncludesTrafficStatsInterval|TestPatchAgent' -count=1
```

Expected: pass.

- [ ] **Step 8: Commit**

```bash
git add panel/backend-go/internal/controlplane/storage/sqlite_models.go panel/backend-go/internal/controlplane/storage/schema.go panel/backend-go/internal/controlplane/storage/sqlite_store_test.go panel/backend-go/internal/controlplane/service/agents.go panel/backend-go/internal/controlplane/service/agents_test.go panel/backend-go/internal/controlplane/http/handlers_public.go panel/backend-go/internal/controlplane/http/public_test.go panel/backend-go/internal/controlplane/http/router_test.go
git commit -m "feat(backend): configure traffic stats reporting interval"
```

## Task 7: Frontend Traffic Display And Interval Setting

**Files:**
- Create: `panel/frontend/src/utils/trafficStats.js`
- Modify: `panel/frontend/src/pages/AgentDetailPage.vue`
- Modify: `panel/frontend/src/pages/RulesPage.vue`
- Modify: `panel/frontend/src/pages/L4RulesPage.vue`
- Modify: `panel/frontend/src/pages/RelayListenersPage.vue`
- Modify: `panel/frontend/src/components/rules/RuleCard.vue`
- Modify: `panel/frontend/src/components/l4/L4RuleItem.vue`
- Modify: `panel/frontend/src/components/relay/RelayCard.vue`
- Modify: `panel/frontend/src/hooks/useAgents.js`
- Modify: `panel/frontend/src/api/devMocks/data.js`
- Test: `panel/frontend/src/pages/AgentDetailPage.test.js`

- [ ] **Step 1: Write failing traffic helper tests**

Create `panel/frontend/src/utils/trafficStats.test.mjs`:

```js
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { bucketForObject, formatBytes, normalizeTrafficBucket } from './trafficStats.js'

describe('trafficStats helpers', () => {
  it('normalizes missing buckets to zero', () => {
    assert.deepEqual(normalizeTrafficBucket(null), { rx_bytes: 0, tx_bytes: 0 })
  })

  it('looks up per-object buckets by string id', () => {
    const stats = {
      traffic: {
        http_rules: {
          '11': { rx_bytes: 123, tx_bytes: 456 }
        }
      }
    }
    assert.deepEqual(bucketForObject(stats, 'http_rules', 11), { rx_bytes: 123, tx_bytes: 456 })
  })

  it('formats bytes compactly', () => {
    assert.equal(formatBytes(1536), '1.50 KiB')
  })
})
```

- [ ] **Step 2: Run helper test to verify failure**

Run: `cd panel/frontend && node --test src/utils/trafficStats.test.mjs`

Expected: fail because `trafficStats.js` does not exist.

- [ ] **Step 3: Implement traffic helper**

Create `panel/frontend/src/utils/trafficStats.js`:

```js
export function normalizeTrafficBucket(value) {
  return {
    rx_bytes: Math.max(0, Number(value?.rx_bytes || 0)),
    tx_bytes: Math.max(0, Number(value?.tx_bytes || 0))
  }
}

export function bucketForObject(stats, mapName, id) {
  const key = String(id)
  return normalizeTrafficBucket(stats?.traffic?.[mapName]?.[key])
}

export function formatBytes(value) {
  const bytes = Math.max(0, Number(value || 0))
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  let size = bytes
  let unitIndex = 0
  while (size >= 1024 && unitIndex < units.length - 1) {
    size /= 1024
    unitIndex += 1
  }
  if (unitIndex === 0) return `${Math.round(size)} ${units[unitIndex]}`
  return `${size.toFixed(size >= 10 ? 1 : 2)} ${units[unitIndex]}`
}
```

- [ ] **Step 4: Wire per-object stats into pages**

In `RulesPage.vue`, import `useQuery`, `fetchAgentStats`, and `bucketForObject`. Add:

```js
const { data: agentStatsData } = useQuery({
  queryKey: ['agent-stats', agentId],
  queryFn: () => fetchAgentStats(agentId.value),
  enabled: () => !!agentId.value,
  refetchInterval: 10_000
})

function trafficForRule(rule) {
  return bucketForObject(agentStatsData.value, 'http_rules', rule?.id)
}
```

Pass to cards:

```vue
<RuleCard
  ...
  :traffic="trafficForRule(rule)"
/>
```

In `L4RulesPage.vue`, use `bucketForObject(agentStatsData.value, 'l4_rules', rule?.id)` and pass `:traffic`.

In `RelayListenersPage.vue`, use `bucketForObject(agentStatsData.value, 'relay_listeners', listener?.id)` and pass `:traffic`.

- [ ] **Step 5: Render traffic in cards**

In each card component, add prop:

```js
traffic: { type: Object, default: () => ({ rx_bytes: 0, tx_bytes: 0 }) },
```

Import `formatBytes`:

```js
import { formatBytes } from '../../utils/trafficStats.js'
```

For `RelayCard.vue`, the relative import is:

```js
import { formatBytes } from '../../utils/trafficStats.js'
```

Add a compact row before tags:

```vue
<div class="traffic-line">
  <span>↓ {{ formatBytes(traffic.rx_bytes) }}</span>
  <span>↑ {{ formatBytes(traffic.tx_bytes) }}</span>
</div>
```

Add shared scoped CSS to each card:

```css
.traffic-line {
  display: flex;
  gap: 0.75rem;
  color: var(--color-text-tertiary);
  font-size: 0.8125rem;
  font-variant-numeric: tabular-nums;
}
```

- [ ] **Step 6: Add traffic stats interval setting in agent detail**

In `AgentDetailPage.vue`, add state:

```js
const trafficStatsInterval = ref('')
```

Extend the existing watcher:

```js
watch(agent, (value) => {
  outboundProxyURL.value = value?.outbound_proxy_url || ''
  trafficStatsInterval.value = value?.traffic_stats_interval || ''
}, { immediate: true })
```

Add save function:

```js
async function saveTrafficStatsInterval() {
  if (!agent.value || agent.value.is_local) return
  await updateAgent.mutateAsync({
    agentId: agent.value.id,
    payload: { traffic_stats_interval: trafficStatsInterval.value.trim() }
  })
}
```

Add form block in the info tab near the outbound proxy setting:

```vue
<div v-if="!agent.is_local" class="agent-setting">
  <label class="agent-setting__label" for="agent-traffic-stats-interval">流量统计上报周期</label>
  <div class="agent-setting__control">
    <input
      id="agent-traffic-stats-interval"
      v-model="trafficStatsInterval"
      class="agent-setting__input"
      placeholder="例如 30s、1m、5m；留空表示随心跳上报"
    >
    <button
      class="btn btn-primary"
      type="button"
      :disabled="updateAgent.isPending.value"
      @click="saveTrafficStatsInterval"
    >
      保存
    </button>
  </div>
</div>
```

- [ ] **Step 7: Update query invalidation and mocks**

In `useUpdateAgent`, add:

```js
qc.invalidateQueries({ queryKey: ['agent-stats'] })
```

In `panel/frontend/src/api/devMocks/data.js`, add `traffic_stats_interval: '30s'` to at least one remote mock agent and extend mock stats:

```js
traffic: {
  total: { rx_bytes: 1200000, tx_bytes: 3400000 },
  http: { rx_bytes: 400000, tx_bytes: 900000 },
  l4: { rx_bytes: 300000, tx_bytes: 600000 },
  relay: { rx_bytes: 500000, tx_bytes: 1900000 },
  http_rules: { '1': { rx_bytes: 100000, tx_bytes: 200000 } },
  l4_rules: { '1': { rx_bytes: 300000, tx_bytes: 600000 } },
  relay_listeners: { '1': { rx_bytes: 500000, tx_bytes: 1900000 } }
}
```

- [ ] **Step 8: Run frontend tests and build**

Run:

```bash
cd panel/frontend && node --test src/utils/trafficStats.test.mjs
cd panel/frontend && npm run build
```

Expected: helper tests pass and Vite build succeeds.

- [ ] **Step 9: Commit**

```bash
git add panel/frontend/src/utils/trafficStats.js panel/frontend/src/utils/trafficStats.test.mjs panel/frontend/src/pages/AgentDetailPage.vue panel/frontend/src/pages/RulesPage.vue panel/frontend/src/pages/L4RulesPage.vue panel/frontend/src/pages/RelayListenersPage.vue panel/frontend/src/components/rules/RuleCard.vue panel/frontend/src/components/l4/L4RuleItem.vue panel/frontend/src/components/relay/RelayCard.vue panel/frontend/src/hooks/useAgents.js panel/frontend/src/api/devMocks/data.js
git commit -m "feat(panel): show traffic stats on rules and relay listeners"
```

## Task 8: Documentation And Full Verification

**Files:**
- Modify: `README.md`
- Use: repository test suites.

- [ ] **Step 1: Document traffic stats interval**

Add a short configuration note to `README.md` near agent/node settings:

```markdown
Traffic statistics are reported as latest cumulative counters per node, HTTP rule, L4 rule, and Relay listener. Remote nodes can set `traffic_stats_interval` through the panel; the value is a Go-style duration such as `30s`, `1m`, or `5m`. The interval controls heartbeat stats reporting frequency only. It does not reset counters and does not create historical buckets.
```

- [ ] **Step 2: Run agent tests**

Run: `cd go-agent && go test ./...`

Expected: pass.

- [ ] **Step 3: Run control-plane tests**

Run: `cd panel/backend-go && go test ./...`

Expected: pass.

- [ ] **Step 4: Run frontend build**

Run: `cd panel/frontend && npm run build`

Expected: pass.

- [ ] **Step 5: Run full image build if previous checks pass**

Run: `docker build -t nginx-reverse-emby .`

Expected: pass.

- [ ] **Step 6: Commit documentation**

```bash
git add README.md
git commit -m "docs: document traffic stats reporting interval"
```

---

## Self-Review

Spec coverage:

- Per HTTP rule, L4 rule, and Relay listener stats are covered by Tasks 1 through 4.
- Latest-stats persistence through existing heartbeat paths is preserved and extended by Tasks 5 and 6.
- Node-level stats reporting interval is covered by Tasks 5 through 7.
- Frontend display is covered by Task 7.
- Compatibility with aggregate-only stats is covered by helper normalization and missing-map zero rendering in Task 7.
- Historical buckets, retention, and trend charts remain out of scope.

Placeholder scan:

- No task uses `TBD`, `TODO`, or deferred edge-case language.
- Each code-changing task has a failing test, expected failure, implementation shape, verification command, and commit command.

Type consistency:

- Agent config field name is `traffic_stats_interval` in JSON and `TrafficStatsInterval` in Go.
- Runtime metadata keys are `traffic_stats_interval` and `last_traffic_stats_report_unix`.
- Per-object JSON maps are `http_rules`, `l4_rules`, and `relay_listeners`.
