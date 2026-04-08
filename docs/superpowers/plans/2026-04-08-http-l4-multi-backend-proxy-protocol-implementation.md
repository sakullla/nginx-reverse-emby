# HTTP/L4 Multi-Backend + PROXY Protocol Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver shared multi-backend runtime support for HTTP and L4 rules, fixed 30-second DNS caching, adaptive per-`IP:port` failure backoff, TCP PROXY Protocol decode/send, and hot-path performance improvements without breaking legacy single-backend rules.

**Architecture:** Extend the control-plane rule models so HTTP and L4 both emit normalized `backends + load_balancing` data while still mirroring legacy `backend_url` / `upstream_*` fields for compatibility. In the Go agent, add a shared backend-selection module that owns DNS cache, failure cache, and per-rule strategy state, then plug it into the HTTP proxy runtime and L4 server. Keep HTTP/L4 frontends aligned with the actual runtime capability set by removing unsupported strategy UI and surfacing the new multi-backend model.

**Tech Stack:** Node.js backend (`panel/backend/server.js`, Prisma + JSON storage, `node:test`), Vue 3 frontend (`panel/frontend/src`), Go agent (`go-agent/internal/...`), Go stdlib networking (`net`, `net/http`, `httputil`, `sync`, `time`).

---

## File Structure

### Control Plane

- Modify: `panel/backend/server.js`
  - Add HTTP `backends` / `load_balancing` normalization helpers.
  - Tighten L4 normalization to only expose `round_robin` / `random`.
  - Reject UDP `proxy_protocol`.
  - Keep legacy mirror fields in API payloads.
- Modify: `panel/backend/storage-json.js`
  - Sanitize/persist HTTP `backends` / `load_balancing`.
- Modify: `panel/backend/storage-prisma-core.js`
  - Read/write HTTP `backends` / `load_balancing` JSON columns.
- Modify: `panel/backend/prisma/schema.prisma`
  - Add `backends` and `load_balancing` columns to `Rule`.
- Modify: `panel/backend/tests/property-roundtrip.test.js`
  - Cover HTTP/L4 new fields and legacy mirror behavior.
- Modify: `panel/backend/tests/property-compatibility.test.js`
  - Keep JSON/Prisma parity for HTTP/L4 new fields.
- Modify: `panel/backend/tests/property-isolation.test.js`
  - Ensure per-agent isolation with HTTP `backends`.
- Modify: `panel/backend/tests/prisma-migration-flow.test.js`
  - Verify Prisma migration includes new HTTP columns.
- Modify: `panel/backend/tests/go-agent-heartbeat.test.js`
  - Verify sync payloads include HTTP/L4 `backends` / `load_balancing`.

### Go Agent

- Modify: `go-agent/internal/model/http.go`
  - Add typed HTTP backend and load-balancing structs.
- Modify: `go-agent/internal/model/l4.go`
  - Add typed L4 backend, load-balancing, and `proxy_protocol` structs.
- Modify: `go-agent/internal/runtime/runtime.go`
  - Deep-copy new slices/struct fields in snapshot clone logic.
- Create: `go-agent/internal/backends/types.go`
  - Shared endpoint/candidate config types.
- Create: `go-agent/internal/backends/cache.go`
  - Shared DNS cache, failure cache, and round-robin state.
- Create: `go-agent/internal/backends/cache_test.go`
  - Unit tests for DNS cache TTL, failure backoff, and strategy selection.
- Modify: `go-agent/internal/proxy/server.go`
  - Replace fixed backend target proxying with per-request selection + retry.
- Modify: `go-agent/internal/proxy/server_test.go`
  - Add HTTP multi-backend, DNS/failure cache, and performance-oriented behavior tests.
- Modify: `go-agent/internal/l4/server.go`
  - Add shared backend selection, TCP retry, UDP session reuse, and PROXY Protocol hooks.
- Create: `go-agent/internal/l4/proxy_protocol.go`
  - Parse/build PROXY Protocol v1/v2 frames.
- Create: `go-agent/internal/l4/proxy_protocol_test.go`
  - Unit tests for PROXY frame parsing/serialization.
- Modify: `go-agent/internal/l4/server_test.go`
  - Add multi-backend, hostname, UDP-session, and PROXY Protocol integration tests.
- Modify: `go-agent/internal/app/local_runtime.go`
  - Reuse shared backend caches across runtime restarts when listeners overlap.
- Modify: `go-agent/internal/app/local_runtime_test.go`
  - Cover runtime reapply behavior with shared transports/caches.

### Frontend

- Modify: `panel/frontend/src/components/RuleForm.vue`
  - Convert HTTP rule form to backend list UI.
- Modify: `panel/frontend/src/components/rules/RuleTable.vue`
  - Show first backend plus count.
- Modify: `panel/frontend/src/pages/RulesPage.vue`
  - Search and display HTTP backend arrays.
- Modify: `panel/frontend/src/components/GlobalSearch.vue`
  - Search/render HTTP and L4 new backend structures.
- Modify: `panel/frontend/src/pages/AgentDetailPage.vue`
  - Display HTTP and L4 first backend from new shape.
- Modify: `panel/frontend/src/api/index.js`
  - Mock data and client payload helpers for HTTP `backends`.
- Modify: `panel/frontend/src/hooks/useRules.js`
  - Preserve new HTTP payload shape.
- Modify: `panel/frontend/src/components/L4RuleForm.vue`
  - Remove unsupported L4 strategy/weight UI.
- Modify: `panel/frontend/src/components/l4/L4RuleItem.vue`
  - Keep L4 list display aligned with trimmed strategy set.
- Modify: `panel/frontend/src/pages/L4RulesPage.vue`
  - Search new backend list and display trimmed strategy labels.

## Task 1: Extend Control-Plane HTTP Rule Model and Persistence

**Files:**
- Modify: `panel/backend/server.js`
- Modify: `panel/backend/storage-json.js`
- Modify: `panel/backend/storage-prisma-core.js`
- Modify: `panel/backend/prisma/schema.prisma`
- Test: `panel/backend/tests/property-roundtrip.test.js`
- Test: `panel/backend/tests/property-compatibility.test.js`
- Test: `panel/backend/tests/property-isolation.test.js`
- Test: `panel/backend/tests/prisma-migration-flow.test.js`

- [ ] **Step 1: Write the failing backend tests for HTTP `backends` + legacy mirrors**

```js
it("HTTP rules round-trip preserves backends/load_balancing and mirrors backend_url", () => {
  const rule = {
    id: 7,
    frontend_url: "https://edge.example.com",
    backend_url: "http://app-a.internal:8096",
    backends: [
      { url: "http://app-a.internal:8096" },
      { url: "http://app-b.internal:8096" },
    ],
    load_balancing: { strategy: "round_robin" },
    enabled: true,
    tags: ["prod"],
    proxy_redirect: true,
    pass_proxy_headers: true,
    user_agent: "",
    custom_headers: [],
    relay_chain: [],
    revision: 3,
  };

  storage.saveRulesForAgent("agent-http", [rule]);
  const [loaded] = storage.loadRulesForAgent("agent-http");

  assert.deepEqual(loaded.backends, rule.backends);
  assert.deepEqual(loaded.load_balancing, { strategy: "round_robin" });
  assert.equal(loaded.backend_url, "http://app-a.internal:8096");
});

it("legacy HTTP rules backfill a single backend entry", () => {
  const legacy = {
    id: 8,
    frontend_url: "https://legacy.example.com",
    backend_url: "http://legacy.internal:8096",
    enabled: true,
    tags: [],
    proxy_redirect: true,
    relay_chain: [],
    revision: 4,
  };

  storage.saveRulesForAgent("agent-http", [legacy]);
  const [loaded] = storage.loadRulesForAgent("agent-http");

  assert.deepEqual(loaded.backends, [{ url: "http://legacy.internal:8096" }]);
  assert.deepEqual(loaded.load_balancing, { strategy: "round_robin" });
});
```

- [ ] **Step 2: Run the targeted backend tests and verify they fail**

Run: `node --test tests/property-roundtrip.test.js tests/property-compatibility.test.js tests/property-isolation.test.js tests/prisma-migration-flow.test.js`

Expected: FAIL with missing `backends` / `load_balancing` on HTTP rules or missing Prisma columns for `Rule`.

- [ ] **Step 3: Implement HTTP backend normalization, storage, and schema changes**

```js
function normalizeHTTPBackends(backends, fallbackBackendUrl) {
  const valid = [];
  for (const entry of Array.isArray(backends) ? backends : []) {
    const rawUrl = String(entry?.url || "").trim();
    if (!validateUrl(rawUrl)) continue;
    valid.push({ url: rawUrl });
  }
  if (valid.length === 0 && validateUrl(fallbackBackendUrl)) {
    valid.push({ url: String(fallbackBackendUrl).trim() });
  }
  return valid;
}

function normalizeHTTPLoadBalancing(value, fallback = "round_robin") {
  const strategy = String(value?.strategy || fallback).trim().toLowerCase();
  return {
    strategy: strategy === "random" ? "random" : "round_robin",
  };
}

function normalizeRulePayload(body, fallback = {}, suggestedId = null) {
  const frontend =
    body.frontend_url !== undefined
      ? String(body.frontend_url).trim()
      : fallback.frontend_url;
  const legacyBackend =
    body.backend_url !== undefined
      ? String(body.backend_url).trim()
      : fallback.backend_url;
  const backends = normalizeHTTPBackends(
    body.backends !== undefined ? body.backends : fallback.backends,
    legacyBackend,
  );
  if (!validateUrl(frontend) || backends.length === 0) {
    throw new Error("frontend_url and at least one valid backend are required");
  }
  const loadBalancing = normalizeHTTPLoadBalancing(
    body.load_balancing !== undefined ? body.load_balancing : fallback.load_balancing,
  );

  return {
    id: Number.isFinite(Number(body.id ?? fallback.id ?? suggestedId)) ? Number(body.id ?? fallback.id ?? suggestedId) : Number(suggestedId) || 1,
    frontend_url: frontend,
    backend_url: backends[0].url,
    backends,
    load_balancing: loadBalancing,
    enabled: body.enabled !== undefined ? !!body.enabled : fallback.enabled !== false,
    tags: body.tags !== undefined ? normalizeTags(body.tags) : normalizeTags(fallback.tags || []),
    proxy_redirect: body.proxy_redirect !== undefined ? !!body.proxy_redirect : fallback.proxy_redirect !== false,
    relay_chain: normalizeRelayChainPayload(body.relay_chain !== undefined ? body.relay_chain : fallback.relay_chain, { protocol: "tcp" }),
    ...normalizeRuleRequestHeaders(body, fallback),
  };
}
```

```prisma
model Rule {
  id               Int
  agentId          String  @map("agent_id")
  frontendUrl      String  @map("frontend_url")
  backendUrl       String  @map("backend_url")
  backends         String  @default("[]")
  loadBalancing    String  @default("{}") @map("load_balancing")
  enabled          Boolean @default(true)
  tags             String  @default("[]")
  proxyRedirect    Boolean @default(true) @map("proxy_redirect")
  relayChain       String  @default("[]") @map("relay_chain")
  passProxyHeaders Boolean @default(true) @map("pass_proxy_headers")
  userAgent        String  @default("") @map("user_agent")
  customHeaders    String  @default("[]") @map("custom_headers")
  revision         Int     @default(0)

  @@id([agentId, id])
  @@index([agentId], map: "idx_rules_agent")
  @@map("rules")
}
```

```js
function sanitizeRuleForStorage(rule) {
  if (!rule || typeof rule !== "object") return rule;
  return {
    ...rule,
    backends: Array.isArray(rule.backends) ? rule.backends.map((b) => ({ url: String(b?.url || "").trim() })).filter((b) => validateUrl(b.url)) : normalizeHTTPBackends([], rule.backend_url),
    load_balancing: normalizeHTTPLoadBalancing(rule.load_balancing),
    backend_url: Array.isArray(rule.backends) && rule.backends[0]?.url ? String(rule.backends[0].url) : String(rule.backend_url || ""),
    relay_chain: normalizeRelayChainIds(rule.relay_chain),
    custom_headers: sanitizeStoredCustomHeaders(rule.custom_headers),
  };
}
```

- [ ] **Step 4: Regenerate Prisma client and rerun the backend tests**

Run: `npm run prisma:generate`

Expected: PASS with Prisma client regenerated and no schema errors.

Run: `node --test tests/property-roundtrip.test.js tests/property-compatibility.test.js tests/property-isolation.test.js tests/prisma-migration-flow.test.js`

Expected: PASS with HTTP `backends` / `load_balancing` round-tripping in both JSON and Prisma storage.

- [ ] **Step 5: Commit the HTTP control-plane model work**

```bash
git add panel/backend/server.js panel/backend/storage-json.js panel/backend/storage-prisma-core.js panel/backend/prisma/schema.prisma panel/backend/tests/property-roundtrip.test.js panel/backend/tests/property-compatibility.test.js panel/backend/tests/property-isolation.test.js panel/backend/tests/prisma-migration-flow.test.js
git commit -m "feat(backend): add http multi-backend rule model"
```

## Task 2: Tighten L4 Control-Plane Shape and Sync Payloads

**Files:**
- Modify: `panel/backend/server.js`
- Modify: `panel/backend/tests/go-agent-heartbeat.test.js`
- Modify: `panel/backend/tests/property-roundtrip.test.js`
- Modify: `panel/backend/tests/property-compatibility.test.js`

- [ ] **Step 1: Write failing tests for trimmed L4 strategy support and sync payloads**

```js
it("rejects unsupported L4 load balancing strategies", async () => {
  const response = await fetch(`${baseUrl}/api/agents/agent-l4/l4-rules`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({
      protocol: "tcp",
      listen_host: "0.0.0.0",
      listen_port: 25565,
      backends: [{ host: "127.0.0.1", port: 25566 }],
      load_balancing: { strategy: "hash" },
    }),
  });

  assert.equal(response.status, 400);
  const payload = await response.json();
  assert.match(payload.error, /round_robin|random/);
});

it("sync payload includes L4 backends, load_balancing, and proxy_protocol tuning", async () => {
  assert.deepEqual(payload.sync.l4_rules[0].backends, [
    { host: "127.0.0.1", port: 9001 },
    { host: "service.ddns.test", port: 9002 },
  ]);
  assert.deepEqual(payload.sync.l4_rules[0].load_balancing, { strategy: "random" });
  assert.deepEqual(payload.sync.l4_rules[0].tuning.proxy_protocol, { decode: true, send: true });
});
```

- [ ] **Step 2: Run the targeted L4 backend tests and verify they fail**

Run: `node --test tests/go-agent-heartbeat.test.js tests/property-roundtrip.test.js tests/property-compatibility.test.js`

Expected: FAIL because control-plane still accepts unsupported strategies or omits normalized payload fields.

- [ ] **Step 3: Restrict L4 normalization to the supported runtime shape**

```js
function normalizeL4LoadBalancing(lb, defaultStrategy = "round_robin") {
  const strategy = String(lb?.strategy ?? defaultStrategy).trim().toLowerCase();
  if (!["round_robin", "random"].includes(strategy)) {
    throw new Error("load_balancing.strategy must be round_robin or random");
  }
  return { strategy };
}

function normalizeL4Backends(backends, fallbackUpstreamHost, fallbackUpstreamPort) {
  const validBackends = [];
  for (const entry of Array.isArray(backends) ? backends : []) {
    const host = normalizeHost(entry?.host || "");
    const port = Number(entry?.port);
    if (!validateNetworkHost(host) || !validatePort(port)) continue;
    validBackends.push({ host, port });
  }
  if (validBackends.length === 0 && validateNetworkHost(fallbackUpstreamHost) && validatePort(fallbackUpstreamPort)) {
    validBackends.push({ host: normalizeHost(fallbackUpstreamHost), port: Number(fallbackUpstreamPort) });
  }
  return validBackends;
}

function normalizeL4RulePayload(body, fallback = {}, suggestedId = null) {
  const protocol = String(body.protocol ?? fallback.protocol ?? "tcp").trim().toLowerCase();
  const listenHost = normalizeHost(body.listen_host ?? fallback.listen_host ?? "0.0.0.0");
  const listenPort = Number(body.listen_port ?? fallback.listen_port);
  const legacyUpstreamHost = normalizeHost(body.upstream_host ?? fallback.upstream_host ?? "");
  const legacyUpstreamPort = Number(body.upstream_port ?? fallback.upstream_port);
  const backends = normalizeL4Backends(body.backends ?? fallback.backends, legacyUpstreamHost, legacyUpstreamPort);
  const loadBalancing = normalizeL4LoadBalancing(body.load_balancing ?? fallback.load_balancing);
  const tuning = normalizeL4Tuning(body.tuning ?? fallback.tuning, protocol);
  const relayChain = normalizeRelayChainPayload(body.relay_chain ?? fallback.relay_chain, { protocol });
  if (protocol === "udp" && (tuning.proxy_protocol?.decode || tuning.proxy_protocol?.send)) {
    throw new Error("proxy_protocol is only supported for tcp protocol");
  }
  if (!backends.length) {
    throw new Error("at least one valid backend is required");
  }
  return {
    id: Number.isFinite(Number(body.id ?? fallback.id ?? suggestedId)) ? Number(body.id ?? fallback.id ?? suggestedId) : Number(suggestedId) || 1,
    name: String(body.name ?? fallback.name ?? `${protocol.toUpperCase()} ${listenPort}`).trim(),
    protocol,
    listen_host: listenHost,
    listen_port: listenPort,
    upstream_host: backends[0].host,
    upstream_port: backends[0].port,
    backends,
    load_balancing: loadBalancing,
    tuning,
    relay_chain: relayChain,
    enabled: body.enabled !== undefined ? !!body.enabled : fallback.enabled !== false,
    tags: body.tags !== undefined ? normalizeTags(body.tags) : normalizeTags(fallback.tags || []),
  };
}
```

- [ ] **Step 4: Rerun the targeted L4 backend tests**

Run: `node --test tests/go-agent-heartbeat.test.js tests/property-roundtrip.test.js tests/property-compatibility.test.js`

Expected: PASS with `sync.l4_rules[*]` carrying normalized `backends`, `load_balancing`, and `tuning.proxy_protocol` fields and unsupported strategies rejected.

- [ ] **Step 5: Commit the L4 control-plane normalization work**

```bash
git add panel/backend/server.js panel/backend/tests/go-agent-heartbeat.test.js panel/backend/tests/property-roundtrip.test.js panel/backend/tests/property-compatibility.test.js
git commit -m "feat(backend): align l4 payloads with shared backend runtime"
```

## Task 3: Add Typed Go Models and Shared Backend Cache Module

**Files:**
- Modify: `go-agent/internal/model/http.go`
- Modify: `go-agent/internal/model/l4.go`
- Modify: `go-agent/internal/runtime/runtime.go`
- Create: `go-agent/internal/backends/types.go`
- Create: `go-agent/internal/backends/cache.go`
- Create: `go-agent/internal/backends/cache_test.go`
- Modify: `go-agent/internal/sync/client_test.go`

- [ ] **Step 1: Write failing Go tests for model decoding, DNS cache, and failure backoff**

```go
func TestSnapshotHTTPAndL4RulesDecodeBackendArrays(t *testing.T) {
	var snap model.Snapshot
	payload := []byte(`{
		"rules":[{"frontend_url":"https://edge.example.com","backend_url":"http://a.internal:8080","backends":[{"url":"http://a.internal:8080"},{"url":"http://b.internal:8080"}],"load_balancing":{"strategy":"round_robin"}}],
		"l4_rules":[{"protocol":"tcp","listen_host":"0.0.0.0","listen_port":25565,"upstream_host":"127.0.0.1","upstream_port":25566,"backends":[{"host":"127.0.0.1","port":25566}],"load_balancing":{"strategy":"random"},"tuning":{"proxy_protocol":{"decode":true,"send":true}}}]
	}`)
	if err := json.Unmarshal(payload, &snap); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}
	if len(snap.Rules[0].Backends) != 2 {
		t.Fatalf("expected 2 http backends, got %d", len(snap.Rules[0].Backends))
	}
	if !snap.L4Rules[0].Tuning.ProxyProtocol.Decode || !snap.L4Rules[0].Tuning.ProxyProtocol.Send {
		t.Fatalf("expected proxy protocol tuning to decode")
	}
}

func TestCacheMarksFailuresWithExponentialBackoff(t *testing.T) {
	cache := backends.NewCache(backends.Options{
		Now: func() time.Time { return time.Unix(1000, 0) },
		LookupHost: func(context.Context, string) ([]string, error) { return []string{"203.0.113.10"}, nil },
	})
	cache.MarkFailure("203.0.113.10:443")
	first := cache.FailureExpiry("203.0.113.10:443")
	cache.MarkFailure("203.0.113.10:443")
	second := cache.FailureExpiry("203.0.113.10:443")
	if !second.After(first) {
		t.Fatalf("expected failure expiry to grow exponentially")
	}
}
```

- [ ] **Step 2: Run the Go tests and verify they fail**

Run: `go test ./internal/model ./internal/runtime ./internal/backends ./internal/sync`

Expected: FAIL because the new structs/package do not exist yet and snapshot cloning ignores the new fields.

- [ ] **Step 3: Add typed model fields and the shared cache package**

```go
type HTTPBackend struct {
	URL string `json:"url"`
}

type LoadBalancing struct {
	Strategy string `json:"strategy"`
}

type HTTPRule struct {
	FrontendURL      string         `json:"frontend_url"`
	BackendURL       string         `json:"backend_url"`
	Backends         []HTTPBackend  `json:"backends,omitempty"`
	LoadBalancing    LoadBalancing  `json:"load_balancing,omitempty"`
	ProxyRedirect    bool           `json:"proxy_redirect,omitempty"`
	PassProxyHeaders bool           `json:"pass_proxy_headers,omitempty"`
	UserAgent        string         `json:"user_agent,omitempty"`
	CustomHeaders    []HTTPHeader   `json:"custom_headers,omitempty"`
	RelayChain       []int          `json:"relay_chain,omitempty"`
	Revision         int64          `json:"revision,omitempty"`
}
```

```go
type L4Backend struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

type L4ProxyProtocol struct {
	Decode bool `json:"decode"`
	Send   bool `json:"send"`
}

type L4Tuning struct {
	ProxyProtocol L4ProxyProtocol `json:"proxy_protocol"`
}

type L4Rule struct {
	Protocol      string        `json:"protocol"`
	ListenHost    string        `json:"listen_host"`
	ListenPort    int           `json:"listen_port"`
	UpstreamHost  string        `json:"upstream_host"`
	UpstreamPort  int           `json:"upstream_port"`
	Backends      []L4Backend   `json:"backends,omitempty"`
	LoadBalancing LoadBalancing `json:"load_balancing,omitempty"`
	Tuning        L4Tuning      `json:"tuning,omitempty"`
	RelayChain    []int         `json:"relay_chain,omitempty"`
	Revision      int64         `json:"revision,omitempty"`
}
```

```go
package backends

type Endpoint struct {
	BackendIndex int
	ScopeKey     string
	Host         string
	Port         int
}

type Candidate struct {
	Endpoint Endpoint
	IP       string
}

type Options struct {
	Now        func() time.Time
	LookupHost func(context.Context, string) ([]string, error)
	RandomIntn func(int) int
}

type Cache struct {
	mu         sync.Mutex
	now        func() time.Time
	lookupHost func(context.Context, string) ([]string, error)
	randomIntn func(int) int
	dns        map[string]dnsEntry
	failures   map[string]failureEntry
	roundRobin map[string]uint64
}

func NewCache(opts Options) *Cache {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	lookup := opts.LookupHost
	if lookup == nil {
		lookup = func(ctx context.Context, host string) ([]string, error) {
			addrs, err := net.DefaultResolver.LookupHost(ctx, host)
			if err != nil {
				return nil, err
			}
			return addrs, nil
		}
	}
	randomIntn := opts.RandomIntn
	if randomIntn == nil {
		rng := rand.New(rand.NewSource(now().UnixNano()))
		randomIntn = rng.Intn
	}
	return &Cache{
		now:        now,
		lookupHost: lookup,
		randomIntn: randomIntn,
		dns:        map[string]dnsEntry{},
		failures:   map[string]failureEntry{},
		roundRobin: map[string]uint64{},
	}
}

func (c *Cache) Candidates(ctx context.Context, scopeKey string, strategy string, endpoints []Endpoint) ([]Candidate, error) {
	ordered := orderEndpoints(endpoints, strategy, c.roundRobin, c.randomIntn)
	out := make([]Candidate, 0, len(ordered))
	for _, endpoint := range ordered {
		for _, ip := range c.lookupWithTTL(ctx, endpoint.Host) {
			address := net.JoinHostPort(ip, strconv.Itoa(endpoint.Port))
			if c.isFailed(address) {
				continue
			}
			out = append(out, Candidate{Endpoint: endpoint, IP: ip})
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no healthy backend candidates for %s", scopeKey)
	}
	return out, nil
}

func (c *Cache) MarkFailure(address string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.failures[address]
	entry.Count++
	if entry.Count < 1 {
		entry.Count = 1
	}
	delay := time.Second << (entry.Count - 1)
	if delay > 60*time.Second {
		delay = 60 * time.Second
	}
	entry.Until = c.now().Add(delay)
	c.failures[address] = entry
}

func (c *Cache) MarkSuccess(address string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.failures, address)
}

func (c *Cache) FailureExpiry(address string) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.failures[address].Until
}
```

- [ ] **Step 4: Deep-copy the new model fields and rerun Go tests**

Run: `go test ./internal/model ./internal/runtime ./internal/backends ./internal/sync`

Expected: PASS with snapshot clones preserving `Backends`, `LoadBalancing`, and `Tuning.ProxyProtocol`, and the shared cache tests proving DNS TTL + failure backoff behavior.

- [ ] **Step 5: Commit the shared backend cache foundation**

```bash
git add go-agent/internal/model/http.go go-agent/internal/model/l4.go go-agent/internal/runtime/runtime.go go-agent/internal/backends/types.go go-agent/internal/backends/cache.go go-agent/internal/backends/cache_test.go go-agent/internal/sync/client_test.go
git commit -m "feat(go-agent): add shared backend cache primitives"
```

## Task 4: Implement HTTP Multi-Backend Runtime with Connection Reuse

**Files:**
- Modify: `go-agent/internal/proxy/server.go`
- Modify: `go-agent/internal/proxy/http_engine.go`
- Modify: `go-agent/internal/proxy/server_test.go`
- Modify: `go-agent/internal/app/local_runtime.go`
- Modify: `go-agent/internal/app/local_runtime_test.go`

- [ ] **Step 1: Write failing HTTP runtime tests for retry, DNS cache, and transport reuse**

```go
func TestStartRetriesHTTPRequestsAcrossBackends(t *testing.T) {
	failures := 0
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		failures++
		hj, _ := w.(http.Hijacker)
		conn, _, _ := hj.Hijack()
		_ = conn.Close()
	}))
	defer bad.Close()

	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer good.Close()

	runtime, err := Start(context.Background(), []model.HTTPRule{{
		FrontendURL: "http://edge.example.test:18080",
		BackendURL:  bad.URL,
		Backends: []model.HTTPBackend{
			{URL: bad.URL},
			{URL: good.URL},
		},
		LoadBalancing: model.LoadBalancing{Strategy: "round_robin"},
	}}, nil, Providers{})
	if err != nil {
		t.Fatalf("start runtime: %v", err)
	}
	defer runtime.Close()

	resp, err := http.Get("http://127.0.0.1:18080/")
	if err != nil {
		t.Fatalf("proxy request: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" || failures == 0 {
		t.Fatalf("expected retry to good backend; failures=%d body=%q", failures, string(body))
	}
}

func TestStartReusesHTTPTransportPoolAcrossRequests(t *testing.T) {
	server := NewServer(model.HTTPListener{Rules: []model.HTTPRule{{FrontendURL: "http://edge.example.test:18080", BackendURL: "http://127.0.0.1:8081", Backends: []model.HTTPBackend{{URL: "http://127.0.0.1:8081"}}}}})
	entry := server.routes["edge.example.test"]
	if entry.transport == nil {
		t.Fatal("expected shared transport on route entry")
	}
}
```

- [ ] **Step 2: Run the HTTP Go tests and verify they fail**

Run: `go test ./internal/proxy ./internal/app -run "TestStart|TestRun|TestStartRetriesHTTPRequestsAcrossBackends|TestStartReusesHTTPTransportPoolAcrossRequests"`

Expected: FAIL because the proxy runtime still binds each route to a single fixed target and has no shared transport/cache layer.

- [ ] **Step 3: Replace fixed-backend routing with per-request selection + retry**

```go
type routeEntry struct {
	rule       model.HTTPRule
	backendSet *backends.Cache
	transport  *http.Transport
	relay      RelayMaterialProvider
}

func (s *Server) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	host := normalizeHost(req.Host)
	entry, ok := s.routes[host]
	if !ok {
		http.NotFound(w, req)
		return
	}
	if err := entry.serveHTTP(w, req); err != nil {
		http.Error(w, "bad gateway", http.StatusBadGateway)
	}
}

func (e *routeEntry) serveHTTP(w http.ResponseWriter, req *http.Request) error {
	bodyBytes, err := readReusableBody(req)
	if err != nil {
		return err
	}
	candidates, err := httpCandidates(req.Context(), e.backendSet, e.rule)
	if err != nil {
		return err
	}
	for _, candidate := range candidates {
		attemptReq, err := cloneProxyRequest(req, bodyBytes, candidate, e.rule)
		if err != nil {
			return err
		}
		resp, err := e.transport.RoundTrip(attemptReq)
		if err != nil {
			e.backendSet.MarkFailure(candidate.Address())
			continue
		}
		e.backendSet.MarkSuccess(candidate.Address())
		defer resp.Body.Close()
		copyResponse(w, resp, FrontendOriginFromRule(e.rule), e.rule.ProxyRedirect, candidate.BackendHost())
		return nil
	}
	return fmt.Errorf("all backends failed for %s", e.rule.FrontendURL)
}
```

```go
func sharedTransport() *http.Transport {
	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		MaxIdleConns:          256,
		MaxIdleConnsPerHost:   64,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ForceAttemptHTTP2:     true,
	}
}
```

- [ ] **Step 4: Reuse backend caches/transports across runtime reapply**

```go
type httpRuntimeManager struct {
	mu        sync.Mutex
	runtime   *proxy.Runtime
	provider  proxy.TLSMaterialProvider
	cache     *backends.Cache
	transport *http.Transport
}

func newHTTPRuntimeManagerWithTLS(provider proxy.TLSMaterialProvider) *httpRuntimeManager {
	return &httpRuntimeManager{
		provider:  provider,
		cache:     backends.NewCache(backends.Options{}),
		transport: proxy.NewSharedTransport(),
	}
}
```

- [ ] **Step 5: Rerun the HTTP Go tests**

Run: `go test ./internal/proxy ./internal/app`

Expected: PASS with per-request backend retry, shared DNS/failure cache, and shared `http.Transport` connection reuse.

- [ ] **Step 6: Commit the HTTP runtime work**

```bash
git add go-agent/internal/proxy/server.go go-agent/internal/proxy/http_engine.go go-agent/internal/proxy/server_test.go go-agent/internal/app/local_runtime.go go-agent/internal/app/local_runtime_test.go
git commit -m "feat(go-agent): add http multi-backend runtime"
```

## Task 5: Implement L4 Multi-Backend Runtime and UDP Session Reuse

**Files:**
- Modify: `go-agent/internal/l4/server.go`
- Modify: `go-agent/internal/l4/server_test.go`
- Modify: `go-agent/internal/app/local_runtime.go`
- Modify: `go-agent/internal/app/local_runtime_test.go`

- [ ] **Step 1: Write failing L4 tests for TCP retry, hostname backend support, and UDP session reuse**

```go
func TestTCPDirectProxyRetriesNextBackend(t *testing.T) {
	badPort := pickFreeTCPPort(t)
	good := newTCPEchoListener(t)
	defer good.Close()

	srv, err := NewServer(context.Background(), []model.L4Rule{{
		Protocol:   "tcp",
		ListenHost: "127.0.0.1",
		ListenPort: pickFreeTCPPort(t),
		Backends: []model.L4Backend{
			{Host: "127.0.0.1", Port: badPort},
			{Host: "127.0.0.1", Port: good.Port()},
		},
		LoadBalancing: model.LoadBalancing{Strategy: "round_robin"},
	}}, nil, nil)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	defer srv.Close()

	conn, err := net.Dial("tcp", srv.tcpListeners[0].Addr().String())
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close()
	_, _ = conn.Write([]byte("hello"))
	reply := make([]byte, 5)
	_, _ = io.ReadFull(conn, reply)
	if string(reply) != "hello" {
		t.Fatalf("expected retry to healthy backend, got %q", string(reply))
	}
}

func TestUDPProxyReusesSessionUpstreamSocket(t *testing.T) {
	upstreamAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("resolve upstream addr: %v", err)
	}
	upstreamConn, err := net.ListenUDP("udp", upstreamAddr)
	if err != nil {
		t.Fatalf("listen udp upstream: %v", err)
	}
	defer upstreamConn.Close()
	go func() {
		buf := make([]byte, 64)
		for {
			n, addr, err := upstreamConn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			_, _ = upstreamConn.WriteToUDP(buf[:n], addr)
		}
	}()

	listenPort := pickFreeUDPPort(t)
	srv, err := NewServer(context.Background(), []model.L4Rule{{
		Protocol:   "udp",
		ListenHost: "127.0.0.1",
		ListenPort: listenPort,
		Backends:   []model.L4Backend{{Host: "127.0.0.1", Port: upstreamConn.LocalAddr().(*net.UDPAddr).Port}},
		LoadBalancing: model.LoadBalancing{Strategy: "round_robin"},
	}}, nil, nil)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	defer srv.Close()

	client, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: listenPort})
	if err != nil {
		t.Fatalf("dial udp proxy: %v", err)
	}
	defer client.Close()
	for _, payload := range [][]byte{[]byte("one"), []byte("two")} {
		if _, err := client.Write(payload); err != nil {
			t.Fatalf("write udp payload: %v", err)
		}
		reply := make([]byte, len(payload))
		if _, err := io.ReadFull(client, reply); err != nil {
			t.Fatalf("read udp reply: %v", err)
		}
	}
	if len(srv.udpSessions) != 1 {
		t.Fatalf("expected a single reused udp session, got %d", len(srv.udpSessions))
	}
}
```

- [ ] **Step 2: Run the L4 Go tests and verify they fail**

Run: `go test ./internal/l4 ./internal/app -run "TestTCP|TestUDP|TestRun|TestTCPDirectProxyRetriesNextBackend|TestUDPProxyReusesSessionUpstreamSocket"`

Expected: FAIL because the current L4 server only dials a single upstream and creates a fresh UDP dial per packet.

- [ ] **Step 3: Add shared backend selection to the TCP/L4 path**

```go
type Server struct {
	ctx          context.Context
	cancel       context.CancelFunc
	wg           sync.WaitGroup
	cache        *backends.Cache
	tcpListeners []net.Listener
	udpConns     []*net.UDPConn
	udpMu        sync.Mutex
	udpSessions  map[string]*udpSession
	// existing relay fields stay here
}

func (s *Server) dialTCPUpstream(rule model.L4Rule) (net.Conn, error) {
	candidates, err := l4Candidates(s.ctx, s.cache, rule)
	if err != nil {
		return nil, err
	}
	for _, candidate := range candidates {
		target := net.JoinHostPort(candidate.IP, strconv.Itoa(candidate.Endpoint.Port))
		conn, err := (&net.Dialer{}).DialContext(s.ctx, "tcp", target)
		if err != nil {
			s.cache.MarkFailure(target)
			continue
		}
		s.cache.MarkSuccess(target)
		return conn, nil
	}
	return nil, fmt.Errorf("all backends failed for %s:%d", rule.ListenHost, rule.ListenPort)
}
```

- [ ] **Step 4: Replace per-packet UDP dial with session-level upstream reuse**

```go
type udpSession struct {
	peer       *net.UDPAddr
	upstream   *net.UDPConn
	lastActive time.Time
	targetAddr string
}

func (s *Server) sessionForPeer(rule model.L4Rule, peer *net.UDPAddr) (*udpSession, error) {
	key := peer.String()
	s.udpMu.Lock()
	defer s.udpMu.Unlock()
	if existing := s.udpSessions[key]; existing != nil {
		existing.lastActive = time.Now()
		return existing, nil
	}
	conn, target, err := s.dialUDPUpstream(rule)
	if err != nil {
		return nil, err
	}
	session := &udpSession{peer: peer, upstream: conn, lastActive: time.Now(), targetAddr: target}
	s.udpSessions[key] = session
	go s.pipeUDPReplies(session)
	return session, nil
}
```

- [ ] **Step 5: Rerun the L4 Go tests**

Run: `go test ./internal/l4 ./internal/app`

Expected: PASS with TCP retry, hostname backend resolution through the shared cache, and UDP session-level socket reuse.

- [ ] **Step 6: Commit the L4 multi-backend + performance work**

```bash
git add go-agent/internal/l4/server.go go-agent/internal/l4/server_test.go go-agent/internal/app/local_runtime.go go-agent/internal/app/local_runtime_test.go
git commit -m "feat(go-agent): add l4 multi-backend runtime"
```

## Task 6: Add TCP PROXY Protocol Decode and Send

**Files:**
- Create: `go-agent/internal/l4/proxy_protocol.go`
- Create: `go-agent/internal/l4/proxy_protocol_test.go`
- Modify: `go-agent/internal/l4/server.go`
- Modify: `go-agent/internal/l4/server_test.go`

- [ ] **Step 1: Write failing PROXY Protocol parser/serializer tests**

```go
func TestParseProxyProtocolV1(t *testing.T) {
	header := []byte("PROXY TCP4 198.51.100.10 203.0.113.20 12345 443\r\npayload")
	info, payload, err := parseProxyHeader(bytes.NewReader(header))
	if err != nil {
		t.Fatalf("parse v1: %v", err)
	}
	if info.Source.String() != "198.51.100.10:12345" {
		t.Fatalf("unexpected source: %s", info.Source.String())
	}
	if string(payload) != "payload" {
		t.Fatalf("unexpected payload: %q", string(payload))
	}
}

func TestBuildProxyProtocolV2Frame(t *testing.T) {
	frame, err := buildProxyHeader(proxyInfo{
		Source: mustTCPAddr("198.51.100.10:12345"),
		Destination: mustTCPAddr("203.0.113.20:443"),
		Version: 2,
	})
	if err != nil {
		t.Fatalf("build v2: %v", err)
	}
	if !bytes.HasPrefix(frame, []byte{0x0d, 0x0a, 0x0d, 0x0a}) {
		t.Fatalf("missing proxy v2 signature")
	}
}
```

- [ ] **Step 2: Run the PROXY Protocol tests and verify they fail**

Run: `go test ./internal/l4 -run "TestParseProxyProtocolV1|TestBuildProxyProtocolV2Frame|TestTCP.*Proxy"`

Expected: FAIL because the PROXY Protocol helpers and TCP path do not exist yet.

- [ ] **Step 3: Implement PROXY Protocol parsing/building and hook it into TCP forwarding**

```go
type proxyInfo struct {
	Source      *net.TCPAddr
	Destination *net.TCPAddr
	Version     int
}

func parseProxyHeader(r *bufio.Reader) (proxyInfo, []byte, error) {
	peek, err := r.Peek(16)
	if err != nil {
		return proxyInfo{}, nil, err
	}
	if bytes.HasPrefix(peek, []byte("PROXY ")) {
		return parseProxyV1(r)
	}
	if bytes.Equal(peek[:12], []byte{0x0d, 0x0a, 0x0d, 0x0a, 0x00, 0x0d, 0x0a, 0x51, 0x55, 0x49, 0x54, 0x0a}) {
		return parseProxyV2(r)
	}
	return proxyInfo{}, nil, fmt.Errorf("missing proxy protocol header")
}

func buildProxyHeader(info proxyInfo) ([]byte, error) {
	if info.Version == 2 {
		return buildProxyV2(info)
	}
	return buildProxyV1(info)
}

func (s *Server) handleTCPConnection(client net.Conn, rule model.L4Rule) {
	reader := bufio.NewReader(client)
	info, bufferedPayload, err := maybeDecodeProxyHeader(reader, client, rule.Tuning.ProxyProtocol.Decode)
	if err != nil {
		return
	}
	upstream, err := s.dialTCPUpstream(rule)
	if err != nil {
		return
	}
	if rule.Tuning.ProxyProtocol.Send {
		header, err := proxyHeaderForConnection(info, client, upstream)
		if err != nil {
			_ = upstream.Close()
			return
		}
		if _, err := upstream.Write(header); err != nil {
			_ = upstream.Close()
			return
		}
	}
	if len(bufferedPayload) > 0 {
		if _, err := upstream.Write(bufferedPayload); err != nil {
			_ = upstream.Close()
			return
		}
	}
	bidiCopy(client, upstream, reader)
}
```

- [ ] **Step 4: Rerun the PROXY Protocol tests**

Run: `go test ./internal/l4`

Expected: PASS with TCP PROXY Protocol v1/v2 decode, send, and decode+send relay-through behavior covered.

- [ ] **Step 5: Commit the TCP PROXY Protocol work**

```bash
git add go-agent/internal/l4/proxy_protocol.go go-agent/internal/l4/proxy_protocol_test.go go-agent/internal/l4/server.go go-agent/internal/l4/server_test.go
git commit -m "feat(go-agent): add tcp proxy protocol support"
```

## Task 7: Update Frontend HTTP/L4 Rule UI to Match Runtime Support

**Files:**
- Modify: `panel/frontend/src/components/RuleForm.vue`
- Modify: `panel/frontend/src/components/rules/RuleTable.vue`
- Modify: `panel/frontend/src/pages/RulesPage.vue`
- Modify: `panel/frontend/src/components/GlobalSearch.vue`
- Modify: `panel/frontend/src/pages/AgentDetailPage.vue`
- Modify: `panel/frontend/src/api/index.js`
- Modify: `panel/frontend/src/hooks/useRules.js`
- Modify: `panel/frontend/src/components/L4RuleForm.vue`
- Modify: `panel/frontend/src/components/l4/L4RuleItem.vue`
- Modify: `panel/frontend/src/pages/L4RulesPage.vue`

- [ ] **Step 1: Update the HTTP rule form to submit backend arrays and simple strategies**

```vue
<div class="form-group">
  <label class="form-label">负载均衡策略</label>
  <select v-model="form.load_balancing.strategy" class="input">
    <option value="round_robin">轮询 (Round Robin)</option>
    <option value="random">随机 (Random)</option>
  </select>
</div>

<div class="form-group">
  <div class="backends-header">
    <label class="form-label form-label--required">后端服务器</label>
    <button type="button" class="btn btn--sm btn--secondary" @click="addBackend">添加后端</button>
  </div>
  <div class="backends-list">
    <div v-for="(backend, index) in form.backends" :key="backend.id" class="backend-item">
      <input v-model="backend.url" class="input backend-address-input" placeholder="http://app.internal:8096">
      <button v-if="form.backends.length > 1" type="button" class="btn btn--icon btn--danger-ghost" @click="removeBackend(index)">删除</button>
    </div>
  </div>
</div>
```

```js
function createDefaultForm() {
  return {
    frontend_url: "",
    backend_url: "",
    backends: [createBackend()],
    load_balancing: { strategy: "round_robin" },
    tags: [],
    enabled: true,
    proxy_redirect: true,
    pass_proxy_headers: false,
    user_agent: "",
    custom_headers: [],
    relay_chain: [],
  };
}

function buildPayload() {
  const validBackends = form.value.backends
    .map((entry) => ({ url: String(entry.url || "").trim() }))
    .filter((entry) => entry.url);
  return {
    frontend_url: form.value.frontend_url.trim(),
    backend_url: validBackends[0]?.url || "",
    backends: validBackends,
    load_balancing: { strategy: form.value.load_balancing.strategy || "round_robin" },
    tags: [...form.value.tags],
    enabled: form.value.enabled,
    proxy_redirect: form.value.proxy_redirect,
    pass_proxy_headers: form.value.pass_proxy_headers,
    user_agent: form.value.user_agent,
    custom_headers: normalizeCustomHeaders(form.value.custom_headers),
    relay_chain: [...form.value.relay_chain],
  };
}
```

- [ ] **Step 2: Trim L4 UI to the supported runtime strategy set**

```vue
<select v-model="form.load_balancing.strategy" class="input">
  <option value="round_robin">轮询 (Round Robin)</option>
  <option value="random">随机 (Random)</option>
</select>
```

```js
const LB_MAP = {
  round_robin: "RR",
  random: "RND",
};
```

- [ ] **Step 3: Update list/search/mock helpers to read the new backend arrays**

```js
function firstHttpBackend(rule) {
  if (Array.isArray(rule.backends) && rule.backends.length > 0) return rule.backends[0].url;
  return rule.backend_url || "-";
}

const matchedRules = (rules || []).filter((r) =>
  String(r.frontend_url || "").toLowerCase().includes(q) ||
  String(r.backend_url || "").toLowerCase().includes(q) ||
  (Array.isArray(r.backends) ? r.backends.some((b) => String(b.url || "").toLowerCase().includes(q)) : false)
);
```

- [ ] **Step 4: Build the frontend and verify it still compiles**

Run: `npm run build`

Expected: PASS with `RuleForm.vue`, `L4RuleForm.vue`, search, and list pages compiling against the new payload shapes.

- [ ] **Step 5: Commit the frontend alignment**

```bash
git add panel/frontend/src/components/RuleForm.vue panel/frontend/src/components/rules/RuleTable.vue panel/frontend/src/pages/RulesPage.vue panel/frontend/src/components/GlobalSearch.vue panel/frontend/src/pages/AgentDetailPage.vue panel/frontend/src/api/index.js panel/frontend/src/hooks/useRules.js panel/frontend/src/components/L4RuleForm.vue panel/frontend/src/components/l4/L4RuleItem.vue panel/frontend/src/pages/L4RulesPage.vue
git commit -m "feat(panel): align rule forms with multi-backend runtime"
```

## Task 8: Full Verification and Documentation Sync

**Files:**
- Modify: `docs/superpowers/specs/2026-04-08-http-l4-multi-backend-proxy-protocol-design.md`
- Modify: `docs/superpowers/plans/2026-04-08-http-l4-multi-backend-proxy-protocol-implementation.md`

- [ ] **Step 1: Run the backend verification suite**

Run: `npm test`

Expected: PASS with property tests, heartbeat tests, relay/version tests, and runtime packaging tests all green.

- [ ] **Step 2: Run the Go agent verification suite**

Run: `go test ./...`

Expected: PASS with `internal/backends`, `internal/proxy`, `internal/l4`, `internal/app`, and the rest of the agent packages green.

- [ ] **Step 3: Run integration-oriented syntax/build verification**

Run: `node --check server.js`

Expected: PASS with no syntax errors.

Run: `npm run build`

Expected: PASS with the Vue bundle emitted.

Run: `docker build -t nginx-reverse-emby .`

Expected: PASS with the multi-stage image building successfully, proving control-plane + Go agent changes still package.

- [ ] **Step 4: Commit final verification and doc touch-ups**

```bash
git add docs/superpowers/specs/2026-04-08-http-l4-multi-backend-proxy-protocol-design.md docs/superpowers/plans/2026-04-08-http-l4-multi-backend-proxy-protocol-implementation.md
git commit -m "docs(plan): add http l4 multi-backend implementation plan"
```

## Self-Review

### Spec coverage

- HTTP multi-backend model and compatibility: Task 1, Task 4, Task 7
- L4 multi-backend model and compatibility: Task 2, Task 5, Task 7
- Shared DNS cache and adaptive per-`IP:port` failure cache: Task 3, Task 4, Task 5
- TCP PROXY Protocol decode/send: Task 6
- Performance improvements:
  - shared HTTP transport reuse: Task 4
  - shared cache reuse: Task 3 and Task 4
  - UDP session-level upstream reuse: Task 5
  - failure-cache prefiltering before connect: Task 3, Task 4, Task 5
- Frontend/runtime capability alignment: Task 7
- Full verification: Task 8

### Placeholder scan

- No `TBD`, `TODO`, or “similar to previous task” placeholders remain.
- Each code-changing step includes concrete snippets, commands, and expected outcomes.

### Type consistency

- Shared `LoadBalancing` uses the same `strategy` key in backend, Go model, and frontend.
- HTTP `Backends` are `[{url}]`; L4 `Backends` are `[{host,port}]`.
- L4 `Tuning.ProxyProtocol.Decode/Send` naming is consistent across backend normalization, sync payloads, Go models, and frontend bindings.
