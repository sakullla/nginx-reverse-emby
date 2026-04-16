# Adaptive HTTP And L4 Load Balancing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `adaptive` as the default HTTP/L4 load-balancing strategy, extend ranking to backend and DNS/IP layers, and expose stability, latency, estimated bandwidth, and combined performance in diagnostics.

**Architecture:** Reuse the existing backend cache as the shared observation engine, but expand it from per-resolved-IP memory into two scopes: backend-level and resolved-candidate-level. Control-plane defaults and snapshot parsing will normalize to `adaptive`, the HTTP and L4 runtimes will rank configured backends first and resolved candidates second, and diagnostics will serialize the same runtime scores through to the frontend diagnostic modal.

**Tech Stack:** Go, Vue 3, Vite, existing `go-agent/internal/backends` cache, `panel/backend-go` control-plane services/storage, existing diagnostics task/report plumbing.

---

## File Structure

- Modify: `panel/backend-go/internal/controlplane/service/rules.go`
  Purpose: normalize HTTP load-balancing input/defaults to `adaptive`.
- Modify: `panel/backend-go/internal/controlplane/service/l4.go`
  Purpose: normalize L4 load-balancing input/defaults to `adaptive`.
- Modify: `panel/backend-go/internal/controlplane/service/agents.go`
  Purpose: parse stored HTTP strategies so `adaptive` survives reads.
- Modify: `panel/backend-go/internal/controlplane/storage/sqlite_store.go`
  Purpose: parse snapshot `load_balancing.strategy` as `adaptive` by default and preserve explicit legacy values.
- Modify: `panel/backend-go/internal/controlplane/storage/snapshot_types.go`
  Purpose: keep snapshot model compatible with the new strategy.
- Modify: `panel/backend-go/internal/controlplane/localagent/runtime.go`
  Purpose: project `adaptive` into embedded go-agent runtime snapshots.
- Modify: `panel/backend-go/internal/controlplane/service/rules_test.go`
  Purpose: lock HTTP defaulting and normalization behavior.
- Modify: `panel/backend-go/internal/controlplane/service/l4_test.go`
  Purpose: lock L4 defaulting and normalization behavior.
- Modify: `panel/backend-go/internal/controlplane/service/agents_test.go`
  Purpose: verify stored rule parsing preserves `adaptive`.
- Modify: `panel/backend-go/internal/controlplane/storage/sqlite_store_test.go`
  Purpose: verify snapshot parsing/defaults preserve `adaptive`.
- Modify: `go-agent/internal/backends/types.go`
  Purpose: add the `adaptive` strategy constant and any observation-key helpers.
- Modify: `go-agent/internal/backends/cache.go`
  Purpose: add backend-scope observations, 24h aggregation, and combined performance scoring.
- Modify: `go-agent/internal/backends/cache_test.go`
  Purpose: lock stability window, combined performance, backend aggregation, and strategy ordering semantics.
- Modify: `go-agent/internal/proxy/server.go`
  Purpose: rank configured HTTP backends by adaptive score and record backend/resolved observations from live traffic.
- Modify: `go-agent/internal/proxy/server_test.go`
  Purpose: verify adaptive backend selection, resolved candidate selection, and bandwidth-aware preference.
- Modify: `go-agent/internal/diagnostics/http.go`
  Purpose: apply adaptive backend + resolved ranking and emit diagnostics score metadata.
- Modify: `go-agent/internal/diagnostics/http_test.go`
  Purpose: verify HTTP diagnostics expose backend layer, DNS/IP layer, and adaptive factors.
- Modify: `go-agent/internal/l4/server.go`
  Purpose: apply adaptive backend + resolved ranking and learn passive TCP/UDP observations.
- Modify: `go-agent/internal/l4/server_test.go`
  Purpose: verify adaptive backend selection for TCP and UDP rules.
- Modify: `go-agent/internal/diagnostics/l4tcp.go`
  Purpose: rank TCP diagnostics with adaptive selection and emit factor metadata.
- Modify: `go-agent/internal/diagnostics/l4tcp_test.go`
  Purpose: verify TCP diagnostics expose adaptive factor data.
- Modify: `go-agent/internal/diagnostics/diagnostics.go`
  Purpose: expand report types with adaptive factor fields and per-layer candidate summaries.
- Modify: `go-agent/internal/task/diagnostics.go`
  Purpose: serialize adaptive factor fields to task result payloads for the frontend.
- Modify: `panel/frontend/src/components/RuleDiagnosticModal.vue`
  Purpose: render backend-level and DNS/IP-level adaptive factors and preferred-candidate reasons.
- Modify: `panel/frontend/src/api/index.js`
  Purpose: update mock/fixture diagnostic payload shapes used by the frontend.
- Modify: `panel/frontend/src/pages/RulesPage.vue`
  Purpose: pass HTTP diagnostic labels cleanly when multiple configured backends exist.
- Modify: `panel/frontend/src/pages/L4RulesPage.vue`
  Purpose: pass L4 diagnostic labels cleanly when multiple configured backends exist.

### Task 1: Normalize `adaptive` As The Default Control-Plane Strategy

**Files:**
- Modify: `panel/backend-go/internal/controlplane/service/rules.go`
- Modify: `panel/backend-go/internal/controlplane/service/l4.go`
- Modify: `panel/backend-go/internal/controlplane/service/agents.go`
- Modify: `panel/backend-go/internal/controlplane/storage/sqlite_store.go`
- Modify: `panel/backend-go/internal/controlplane/localagent/runtime.go`
- Test: `panel/backend-go/internal/controlplane/service/rules_test.go`
- Test: `panel/backend-go/internal/controlplane/service/l4_test.go`
- Test: `panel/backend-go/internal/controlplane/service/agents_test.go`
- Test: `panel/backend-go/internal/controlplane/storage/sqlite_store_test.go`

- [ ] **Step 1: Write the failing HTTP/L4 control-plane tests**

```go
func TestNormalizeHTTPLoadBalancingDefaultsToAdaptive(t *testing.T) {
	got := normalizeHTTPLoadBalancing(HTTPLoadBalancing{})
	if got.Strategy != "adaptive" {
		t.Fatalf("Strategy = %q", got.Strategy)
	}
}

func TestNormalizeHTTPLoadBalancingPreservesLegacyStrategies(t *testing.T) {
	if got := normalizeHTTPLoadBalancing(HTTPLoadBalancing{Strategy: "random"}); got.Strategy != "random" {
		t.Fatalf("random Strategy = %q", got.Strategy)
	}
	if got := normalizeHTTPLoadBalancing(HTTPLoadBalancing{Strategy: "round_robin"}); got.Strategy != "round_robin" {
		t.Fatalf("round_robin Strategy = %q", got.Strategy)
	}
}

func TestNormalizeL4LoadBalancingDefaultsToAdaptive(t *testing.T) {
	got := normalizeL4LoadBalancingInput(nil, L4LoadBalancing{})
	if got.Strategy != "adaptive" {
		t.Fatalf("Strategy = %q", got.Strategy)
	}
}

func TestParseLoadBalancingStrategyDefaultsToAdaptive(t *testing.T) {
	if got := parseLoadBalancingStrategy(`{}`); got.Strategy != "adaptive" {
		t.Fatalf("Strategy = %q", got.Strategy)
	}
}
```

- [ ] **Step 2: Run the targeted control-plane tests to verify they fail**

Run: `go test ./internal/controlplane/service ./internal/controlplane/storage -run "Test(NormalizeHTTPLoadBalancingDefaultsToAdaptive|NormalizeHTTPLoadBalancingPreservesLegacyStrategies|NormalizeL4LoadBalancingDefaultsToAdaptive|ParseLoadBalancingStrategyDefaultsToAdaptive)" -count=1`

Expected: FAIL because `round_robin` is still the fallback/default.

- [ ] **Step 3: Change HTTP/L4 normalization defaults to `adaptive`**

```go
func normalizeHTTPLoadBalancing(value HTTPLoadBalancing) HTTPLoadBalancing {
	switch strings.ToLower(strings.TrimSpace(value.Strategy)) {
	case "random":
		return HTTPLoadBalancing{Strategy: "random"}
	case "round_robin":
		return HTTPLoadBalancing{Strategy: "round_robin"}
	case "adaptive":
		return HTTPLoadBalancing{Strategy: "adaptive"}
	default:
		return HTTPLoadBalancing{Strategy: "adaptive"}
	}
}

func normalizeL4LoadBalancingInput(input *L4LoadBalancing, fallback L4LoadBalancing) L4LoadBalancing {
	strategy := strings.TrimSpace(fallback.Strategy)
	if input != nil {
		strategy = input.Strategy
	}
	switch strings.ToLower(strings.TrimSpace(strategy)) {
	case "random", "round_robin", "adaptive":
		return L4LoadBalancing{Strategy: strings.ToLower(strings.TrimSpace(strategy))}
	default:
		return L4LoadBalancing{Strategy: "adaptive"}
	}
}
```

- [ ] **Step 4: Update stored rule parsing and snapshot defaults**

```go
func parseLoadBalancing(raw string) HTTPLoadBalancing {
	var value struct {
		Strategy string `json:"strategy"`
	}
	if err := json.Unmarshal([]byte(defaultString(raw, "{}")), &value); err != nil {
		return HTTPLoadBalancing{Strategy: "adaptive"}
	}
	return normalizeHTTPLoadBalancing(HTTPLoadBalancing{Strategy: value.Strategy})
}

func parseLoadBalancingStrategy(raw string) LoadBalancing {
	var value LoadBalancing
	if err := json.Unmarshal([]byte(defaultString(raw, "{}")), &value); err != nil {
		return LoadBalancing{Strategy: "adaptive"}
	}
	switch strings.ToLower(strings.TrimSpace(value.Strategy)) {
	case "random", "round_robin", "adaptive":
		value.Strategy = strings.ToLower(strings.TrimSpace(value.Strategy))
	default:
		value.Strategy = "adaptive"
	}
	return value
}
```

- [ ] **Step 5: Update write-side defaults and local runtime projection**

```go
if loadBalancing.Strategy == "" {
	loadBalancing = HTTPLoadBalancing{Strategy: "adaptive"}
}

LoadBalancingJSON: marshalJSON(rule.LoadBalancing, `{"strategy":"adaptive"}`),
```

- [ ] **Step 6: Run the focused control-plane tests to verify they pass**

Run: `go test ./internal/controlplane/service ./internal/controlplane/storage -run "Test(NormalizeHTTPLoadBalancingDefaultsToAdaptive|NormalizeHTTPLoadBalancingPreservesLegacyStrategies|NormalizeL4LoadBalancingDefaultsToAdaptive|ParseLoadBalancingStrategyDefaultsToAdaptive)" -count=1`

Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add panel/backend-go/internal/controlplane/service/rules.go panel/backend-go/internal/controlplane/service/l4.go panel/backend-go/internal/controlplane/service/agents.go panel/backend-go/internal/controlplane/storage/sqlite_store.go panel/backend-go/internal/controlplane/localagent/runtime.go panel/backend-go/internal/controlplane/service/rules_test.go panel/backend-go/internal/controlplane/service/l4_test.go panel/backend-go/internal/controlplane/service/agents_test.go panel/backend-go/internal/controlplane/storage/sqlite_store_test.go
git commit -m "feat(controlplane): default http and l4 load balancing to adaptive"
```

### Task 2: Expand Backend Cache Into A Shared Adaptive Scoring Engine

**Files:**
- Modify: `go-agent/internal/backends/types.go`
- Modify: `go-agent/internal/backends/cache.go`
- Test: `go-agent/internal/backends/cache_test.go`

- [ ] **Step 1: Write the failing cache tests for backend-level scoring**

```go
func TestCachePreferBackendsUsesRecent24hStabilityBeforePerformance(t *testing.T) {
	base := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	now := base
	cache := NewCache(Config{Now: func() time.Time { return now }})

	backends := []Candidate{
		{Address: "backend-a"},
		{Address: "backend-b"},
	}

	now = base.Add(-2 * time.Hour)
	cache.ObserveBackendFailure("rule:http:backend-b")
	cache.ObserveBackendSuccess("rule:http:backend-a", 40*time.Millisecond, 200*time.Millisecond, 2*1024*1024)
	cache.ObserveBackendSuccess("rule:http:backend-b", 8*time.Millisecond, 200*time.Millisecond, 64*1024)
	now = base

	got := cache.PreferBackendCandidates("rule:http", backends)
	if !reflect.DeepEqual(addresses(got), []string{"backend-a", "backend-b"}) {
		t.Fatalf("unexpected order: %v", addresses(got))
	}
}

func TestCachePreferBackendsUsesCombinedPerformanceNotLatencyOnly(t *testing.T) {
	cache := NewCache(Config{Now: func() time.Time { return time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC) }})
	backends := []Candidate{{Address: "backend-a"}, {Address: "backend-b"}}

	cache.ObserveBackendSuccess("rule:http:backend-a", 12*time.Millisecond, 200*time.Millisecond, 64*1024)
	cache.ObserveBackendSuccess("rule:http:backend-b", 30*time.Millisecond, 200*time.Millisecond, 4*1024*1024)

	got := cache.PreferBackendCandidates("rule:http", backends)
	if got[0].Address != "backend-b" {
		t.Fatalf("unexpected order: %+v", got)
	}
}
```

- [ ] **Step 2: Run the cache tests to verify they fail**

Run: `go test ./internal/backends -run "TestCachePreferBackends(UsesRecent24hStabilityBeforePerformance|UsesCombinedPerformanceNotLatencyOnly)" -count=1`

Expected: FAIL because backend-scope observation methods do not exist yet.

- [ ] **Step 3: Add `adaptive` strategy constant and backend observation APIs**

```go
const (
	StrategyAdaptive   = "adaptive"
	StrategyRoundRobin = "round_robin"
	StrategyRandom     = "random"
)

func (c *Cache) ObserveBackendSuccess(scope string, latency time.Duration, totalDuration time.Duration, bytesTransferred int64) {
	key := strings.TrimSpace(scope)
	if key == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.backendObserved[key]
	entry.recordSuccess(c.now(), latency, totalDuration, bytesTransferred)
	c.backendObserved[key] = entry
}

func (c *Cache) ObserveBackendFailure(scope string) {
	key := strings.TrimSpace(scope)
	if key == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.backendObserved[key]
	entry.recordFailure(c.now())
	c.backendObserved[key] = entry
}
```

- [ ] **Step 4: Implement backend preference with stability and combined performance**

```go
func (c *Cache) PreferBackendCandidates(scope string, candidates []Candidate) []Candidate {
	ordered := append([]Candidate(nil), candidates...)
	now := c.now()

	sort.SliceStable(ordered, func(i, j int) bool {
		left := c.backendPreference(scope, ordered[i].Address, now)
		right := c.backendPreference(scope, ordered[j].Address, now)
		if left.stability != right.stability {
			return left.stability > right.stability
		}
		if left.performance != right.performance {
			return left.performance > right.performance
		}
		return false
	})
	return ordered
}

func combinedPerformance(latency time.Duration, bandwidth float64) float64 {
	if latency <= 0 || bandwidth <= 0 {
		return 0
	}
	latencyScore := 1000.0 / float64(latency/time.Millisecond+1)
	bandwidthScore := math.Log1p(bandwidth / 1024.0)
	return latencyScore*0.45 + bandwidthScore*0.55
}
```

- [ ] **Step 5: Reuse the same observation model for resolved candidates**

```go
func (c *Cache) PreferResolvedCandidates(candidates []Candidate) []Candidate {
	ordered := append([]Candidate(nil), candidates...)
	now := c.now()
	sort.SliceStable(ordered, func(i, j int) bool {
		left := c.resolvedPreference(ordered[i].Address, now)
		right := c.resolvedPreference(ordered[j].Address, now)
		if left.inBackoff != right.inBackoff {
			return !left.inBackoff
		}
		if left.stability != right.stability {
			return left.stability > right.stability
		}
		if left.performance != right.performance {
			return left.performance > right.performance
		}
		return false
	})
	return ordered
}
```

- [ ] **Step 6: Run the focused cache tests to verify they pass**

Run: `go test ./internal/backends -run "TestCache(PreferBackendsUsesRecent24hStabilityBeforePerformance|PreferBackendsUsesCombinedPerformanceNotLatencyOnly|PreferResolvedCandidatesUsesOnlyRecent24hStability|PreferResolvedCandidatesUsesBandwidthAfterStabilityAndLatency)" -count=1`

Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add go-agent/internal/backends/types.go go-agent/internal/backends/cache.go go-agent/internal/backends/cache_test.go
git commit -m "feat(backends): add adaptive backend and dns scoring"
```

### Task 3: Apply Adaptive Ranking To HTTP Backend Selection And Diagnostics

**Files:**
- Modify: `go-agent/internal/proxy/server.go`
- Modify: `go-agent/internal/diagnostics/http.go`
- Modify: `go-agent/internal/diagnostics/http_test.go`
- Modify: `go-agent/internal/proxy/server_test.go`

- [ ] **Step 1: Write the failing HTTP adaptive ranking tests**

```go
func TestRouteEntryCandidatesAdaptivePrefersBackendBeforeResolvedCandidate(t *testing.T) {
	cache := backends.NewCache(backends.Config{
		Resolver: resolverFunc(func(ctx context.Context, host string) ([]net.IPAddr, error) {
			switch host {
			case "fast.example":
				return []net.IPAddr{{IP: net.ParseIP("127.0.0.10")}}, nil
			case "bigpipe.example":
				return []net.IPAddr{{IP: net.ParseIP("127.0.0.11")}}, nil
			default:
				return nil, fmt.Errorf("unexpected host %q", host)
			}
		}),
	})

	cache.ObserveBackendSuccess("http:edge.example.test:0", 30*time.Millisecond, 200*time.Millisecond, 4*1024*1024)
	cache.ObserveBackendSuccess("http:edge.example.test:1", 10*time.Millisecond, 200*time.Millisecond, 64*1024)

	entry := &routeEntry{
		rule: model.HTTPRule{
			FrontendURL: "http://edge.example.test",
			LoadBalancing: model.LoadBalancing{Strategy: "adaptive"},
		},
		backends: []httpBackend{
			{target: mustParseBackendURL(t, "http://bigpipe.example:8096"), backendHost: "bigpipe.example:8096"},
			{target: mustParseBackendURL(t, "http://fast.example:8096"), backendHost: "fast.example:8096"},
		},
		backendCache: cache,
		selectionScope: "edge.example.test",
	}

	candidates, err := entry.candidates(context.Background())
	if err != nil {
		t.Fatalf("candidates() error = %v", err)
	}
	if candidates[0].backendHost != "bigpipe.example:8096" {
		t.Fatalf("unexpected first candidate: %+v", candidates)
	}
}
```

- [ ] **Step 2: Run the focused HTTP proxy/diagnostic tests to verify they fail**

Run: `go test ./internal/proxy ./internal/diagnostics -run "Test(RouteEntryCandidatesAdaptivePrefersBackendBeforeResolvedCandidate|HTTPProberDiagnoseSplitsHostnameBackendsByResolvedAddress)" -count=1`

Expected: FAIL because HTTP backend ranking still starts from `cache.Order(...)`.

- [ ] **Step 3: Add backend identity and adaptive backend ordering in the HTTP proxy**

```go
func (e *routeEntry) backendObservationKey(index int) string {
	return fmt.Sprintf("http:%s:%d", e.selectionScope, index)
}

func (e *routeEntry) orderedBackends(strategy string, placeholders []backends.Candidate) []backends.Candidate {
	if strings.EqualFold(strings.TrimSpace(strategy), backends.StrategyAdaptive) {
		return e.backendCache.PreferBackendCandidates("http:"+e.selectionScope, placeholders)
	}
	return e.backendCache.Order(e.selectionScope, strategy, placeholders)
}
```

- [ ] **Step 4: Record backend-scope observations from successful and failed HTTP traffic**

```go
backendKey := e.backendObservationKey(candidate.backendIndex)
if err != nil {
	e.backendCache.ObserveBackendFailure(backendKey)
	e.backendCache.MarkFailure(actualDialAddress)
}

e.observeSuccessfulBackend(actualDialAddress, headerLatency, time.Since(start), written)
e.backendCache.ObserveBackendSuccess(backendKey, headerLatency, time.Since(start), written)
```

- [ ] **Step 5: Extend HTTP diagnostics to emit backend-level and resolved-candidate factor metadata**

```go
type scoredBackend struct {
	Backend          string             `json:"backend"`
	Scope            string             `json:"scope"`
	Summary          BackendSummary     `json:"summary"`
	Adaptive         AdaptiveSummary    `json:"adaptive"`
	ResolvedChildren []ResolvedCandidate `json:"resolved_candidates,omitempty"`
}
```

- [ ] **Step 6: Run the focused HTTP tests to verify they pass**

Run: `go test ./internal/proxy ./internal/diagnostics -run "Test(RouteEntryCandidatesAdaptivePrefersBackendBeforeResolvedCandidate|RouteEntryObserveSuccessfulBackendUsesBandwidthForFutureRanking|HTTPProberDiagnoseSplitsHostnameBackendsByResolvedAddress)" -count=1`

Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add go-agent/internal/proxy/server.go go-agent/internal/proxy/server_test.go go-agent/internal/diagnostics/http.go go-agent/internal/diagnostics/http_test.go
git commit -m "feat(proxy): apply adaptive backend ranking to http rules"
```

### Task 4: Apply Adaptive Ranking To L4 Backend Selection And Passive Learning

**Files:**
- Modify: `go-agent/internal/l4/server.go`
- Modify: `go-agent/internal/l4/server_test.go`
- Modify: `go-agent/internal/diagnostics/l4tcp.go`
- Modify: `go-agent/internal/diagnostics/l4tcp_test.go`

- [ ] **Step 1: Write the failing L4 adaptive tests**

```go
func TestL4CandidatesAdaptivePrefersBackendByCombinedPerformance(t *testing.T) {
	cache := backends.NewCache(backends.Config{
		Resolver: resolverFunc(func(ctx context.Context, host string) ([]net.IPAddr, error) {
			switch host {
			case "bulk.example":
				return []net.IPAddr{{IP: net.ParseIP("127.0.0.21")}}, nil
			case "fast.example":
				return []net.IPAddr{{IP: net.ParseIP("127.0.0.22")}}, nil
			default:
				return nil, fmt.Errorf("unexpected host %q", host)
			}
		}),
	})

	cache.ObserveBackendSuccess("tcp:0.0.0.0:9443:0", 25*time.Millisecond, 500*time.Millisecond, 8*1024*1024)
	cache.ObserveBackendSuccess("tcp:0.0.0.0:9443:1", 5*time.Millisecond, 500*time.Millisecond, 128*1024)

	candidates, err := l4Candidates(context.Background(), cache, model.L4Rule{
		Protocol: "tcp",
		ListenHost: "0.0.0.0",
		ListenPort: 9443,
		LoadBalancing: model.LoadBalancing{Strategy: "adaptive"},
		Backends: []model.L4Backend{
			{Host: "bulk.example", Port: 9001},
			{Host: "fast.example", Port: 9001},
		},
	})
	if err != nil {
		t.Fatalf("l4Candidates() error = %v", err)
	}
	if candidates[0].Address != "127.0.0.21:9001" {
		t.Fatalf("unexpected order: %+v", candidates)
	}
}
```

- [ ] **Step 2: Run the focused L4 tests to verify they fail**

Run: `go test ./internal/l4 ./internal/diagnostics -run "Test(L4CandidatesAdaptivePrefersBackendByCombinedPerformance|TCPProberDiagnose.*)" -count=1`

Expected: FAIL because L4 still uses `cache.Order(...)` and no backend observations.

- [ ] **Step 3: Add backend identity and adaptive ordering to L4 candidate selection**

```go
func l4BackendObservationKey(rule model.L4Rule, index int) string {
	return fmt.Sprintf("%s:%s:%d:%d", strings.ToLower(rule.Protocol), rule.ListenHost, rule.ListenPort, index)
}

if strings.EqualFold(strings.TrimSpace(rule.LoadBalancing.Strategy), backends.StrategyAdaptive) {
	orderedBackends = cache.PreferBackendCandidates(scope, placeholders)
} else {
	orderedBackends = cache.Order(scope, rule.LoadBalancing.Strategy, placeholders)
}
```

- [ ] **Step 4: Learn backend-level and resolved-candidate metrics from TCP/UDP traffic**

```go
start := s.now()
upstream, err := (&net.Dialer{}).DialContext(s.ctx, "tcp", target)
if err != nil {
	s.cache.ObserveBackendFailure(backendKey)
	s.cache.MarkFailure(target)
}

headerLatency := s.now().Sub(start)
defer func(start time.Time, transferred int64) {
	s.cache.ObserveBackendSuccess(backendKey, headerLatency, s.now().Sub(start), transferred)
	s.cache.ObserveTransferSuccess(target, headerLatency, s.now().Sub(start), transferred)
}(start, bytesRelayed)
```

- [ ] **Step 5: Extend TCP diagnostics to serialize adaptive factor summaries**

```go
return BuildReport("l4_tcp", rule.ID, samples, WithAdaptiveCandidates(candidates)), nil
```

- [ ] **Step 6: Run the focused L4 tests to verify they pass**

Run: `go test ./internal/l4 ./internal/diagnostics -run "Test(L4CandidatesAdaptivePrefersBackendByCombinedPerformance|TCPProberDiagnose.*)" -count=1`

Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add go-agent/internal/l4/server.go go-agent/internal/l4/server_test.go go-agent/internal/diagnostics/l4tcp.go go-agent/internal/diagnostics/l4tcp_test.go
git commit -m "feat(l4): apply adaptive backend and dns ranking"
```

### Task 5: Expand Diagnostic Payloads And Frontend Rendering

**Files:**
- Modify: `go-agent/internal/diagnostics/diagnostics.go`
- Modify: `go-agent/internal/task/diagnostics.go`
- Modify: `panel/frontend/src/components/RuleDiagnosticModal.vue`
- Modify: `panel/frontend/src/api/index.js`
- Modify: `panel/frontend/src/pages/RulesPage.vue`
- Modify: `panel/frontend/src/pages/L4RulesPage.vue`

- [ ] **Step 1: Write the failing payload serialization tests**

```go
func TestReportToMapIncludesAdaptiveFactors(t *testing.T) {
	report := diagnostics.Report{
		Kind: "http",
		RuleID: 7,
		Backends: []diagnostics.BackendReport{{
			Backend: "http://backend-a",
			Adaptive: diagnostics.AdaptiveSummary{
				Preferred: true,
				Reason: "performance_higher",
				Stability: 0.95,
				LatencyMS: 24,
				EstimatedBandwidthBps: 4 * 1024 * 1024,
				PerformanceScore: 0.82,
			},
		}},
	}

	payload := reportToMap(report)
	backends := payload["backends"].([]map[string]any)
	if backends[0]["adaptive"] == nil {
		t.Fatalf("payload = %+v", payload)
	}
}
```

- [ ] **Step 2: Run the diagnostics task tests to verify they fail**

Run: `go test ./internal/task -run TestReportToMapIncludesAdaptiveFactors -count=1`

Expected: FAIL because `reportToMap` does not serialize adaptive fields.

- [ ] **Step 3: Extend diagnostics report structs and task serialization**

```go
type AdaptiveSummary struct {
	Preferred             bool    `json:"preferred"`
	Reason                string  `json:"reason,omitempty"`
	Stability             float64 `json:"stability,omitempty"`
	RecentSucceeded       int     `json:"recent_succeeded,omitempty"`
	RecentFailed          int     `json:"recent_failed,omitempty"`
	LatencyMS             float64 `json:"latency_ms,omitempty"`
	EstimatedBandwidthBps float64 `json:"estimated_bandwidth_bps,omitempty"`
	PerformanceScore      float64 `json:"performance_score,omitempty"`
}
```

- [ ] **Step 4: Update the frontend diagnostic modal to render backend and DNS/IP factor cards**

```vue
<div class="diagnostic-backend-card__adaptive">
  <div class="diagnostic-factor">
    <span class="diagnostic-factor__label">近 24h 稳定性</span>
    <strong class="diagnostic-factor__value">{{ formatStability(backend.adaptive) }}</strong>
  </div>
  <div class="diagnostic-factor">
    <span class="diagnostic-factor__label">延迟</span>
    <strong class="diagnostic-factor__value">{{ backend.adaptive?.latency_ms ?? 0 }} ms</strong>
  </div>
  <div class="diagnostic-factor">
    <span class="diagnostic-factor__label">评估带宽</span>
    <strong class="diagnostic-factor__value">{{ formatBandwidth(backend.adaptive?.estimated_bandwidth_bps) }}</strong>
  </div>
  <div class="diagnostic-factor">
    <span class="diagnostic-factor__label">综合性能</span>
    <strong class="diagnostic-factor__value">{{ formatPerformance(backend.adaptive?.performance_score) }}</strong>
  </div>
</div>
```

- [ ] **Step 5: Update frontend mock payloads and page labels**

```js
adaptive: {
  preferred: index === 0,
  reason: index === 0 ? 'performance_higher' : '',
  stability: 0.96,
  recent_succeeded: 12,
  recent_failed: 1,
  latency_ms: avg,
  estimated_bandwidth_bps: 4 * 1024 * 1024,
  performance_score: 0.84
}
```

- [ ] **Step 6: Run frontend and task verifications**

Run: `go test ./internal/task -run TestReportToMapIncludesAdaptiveFactors -count=1`

Expected: PASS

Run: `npm run build`

Workdir: `panel/frontend`

Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add go-agent/internal/diagnostics/diagnostics.go go-agent/internal/task/diagnostics.go panel/frontend/src/components/RuleDiagnosticModal.vue panel/frontend/src/api/index.js panel/frontend/src/pages/RulesPage.vue panel/frontend/src/pages/L4RulesPage.vue
git commit -m "feat(panel): show adaptive diagnostic factors"
```

### Task 6: Full Verification And Scope Review

**Files:**
- Modify: none unless verification reveals a defect
- Test: `panel/backend-go/internal/controlplane/service/*.go`
- Test: `go-agent/internal/backends/*.go`
- Test: `go-agent/internal/proxy/*.go`
- Test: `go-agent/internal/l4/*.go`
- Test: `panel/frontend/src/components/RuleDiagnosticModal.vue`

- [ ] **Step 1: Run focused control-plane tests**

Run: `go test ./internal/controlplane/service ./internal/controlplane/storage -count=1`

Workdir: `panel/backend-go`

Expected: PASS

- [ ] **Step 2: Run focused go-agent tests**

Run: `go test ./internal/backends ./internal/diagnostics ./internal/proxy ./internal/l4 ./internal/task -count=1`

Workdir: `go-agent`

Expected: PASS

- [ ] **Step 3: Run full backend-go suite**

Run: `go test ./...`

Workdir: `panel/backend-go`

Expected: PASS

- [ ] **Step 4: Run full go-agent suite**

Run: `go test ./...`

Workdir: `go-agent`

Expected: PASS

- [ ] **Step 5: Run frontend build**

Run: `npm run build`

Workdir: `panel/frontend`

Expected: PASS

- [ ] **Step 6: Review diff scope**

Run: `git diff --stat HEAD~5..HEAD`

Expected: changes limited to control-plane load-balancing parsing/defaults, go-agent backend/proxy/diagnostics/l4 files, and frontend diagnostics UI files.

- [ ] **Step 7: Commit any verification-only fixes**

```bash
git add panel/backend-go go-agent panel/frontend
git commit -m "test: finalize adaptive load balancing coverage"
```
