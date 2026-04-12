# Relay Obfs Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a default-off `relay_obfs` rule flag for HTTP and L4 TCP rules, expose it only in the Relay tab UI, enforce fail-closed relay capability negotiation, and obfuscate the first segment of relay tunnel payloads from the first hop onward.

**Architecture:** The change stays rule-scoped. Control-plane, snapshot types, and agent models gain a `relay_obfs` boolean; frontend forms and API payload normalization surface it only under Relay configuration. The relay transport protocol gains a small `transport.mode` declaration plus a first-segment frame layer that runs only when `mode=first_segment_v1`, then falls back to the existing transparent `io.Copy` path.

**Tech Stack:** Go control-plane, Go relay/runtime code, Vue 3 SPA, Node `node:test`, Go `testing`

---

## File Map

### Control-plane data and validation

- Modify: `panel/backend-go/internal/controlplane/service/agents.go`
  Responsibility: API-facing `HTTPRule` struct returned by handlers and embedded in snapshots.
- Modify: `panel/backend-go/internal/controlplane/service/rules.go`
  Responsibility: HTTP rule input normalization and validation.
- Modify: `panel/backend-go/internal/controlplane/service/rules_test.go`
  Responsibility: HTTP rule create/update validation tests.
- Modify: `panel/backend-go/internal/controlplane/service/l4.go`
  Responsibility: L4 rule input normalization and validation.
- Modify: `panel/backend-go/internal/controlplane/service/l4_test.go`
  Responsibility: L4 rule validation tests.
- Modify: `panel/backend-go/internal/controlplane/storage/sqlite_models.go`
  Responsibility: persisted SQLite columns for HTTP/L4 rules.
- Modify: `panel/backend-go/internal/controlplane/storage/snapshot_types.go`
  Responsibility: snapshot JSON models sent to agents.
- Modify: `panel/backend-go/internal/controlplane/storage/schema.go`
  Responsibility: legacy SQLite backfill/default normalization for the new columns.
- Modify: `panel/backend-go/internal/controlplane/storage/sqlite_store_test.go`
  Responsibility: legacy backfill coverage.

### Frontend

- Modify: `panel/frontend/src/api/index.js`
  Responsibility: normalize `relay_obfs` in fetched data, mocks, and create/update payloads.
- Modify: `panel/frontend/src/components/RuleForm.vue`
  Responsibility: HTTP Relay tab toggle UI and payload submission.
- Modify: `panel/frontend/src/components/L4RuleForm.vue`
  Responsibility: L4 Relay tab toggle UI and payload submission.
- Create: `panel/frontend/src/components/relayObfsForm.test.mjs`
  Responsibility: source-level assertions that both forms expose the toggle in the Relay tab and wire the payload field.

### Go agent model and relay transport

- Modify: `go-agent/internal/model/http.go`
  Responsibility: HTTP rule snapshot model.
- Modify: `go-agent/internal/model/l4.go`
  Responsibility: L4 rule snapshot model.
- Modify: `go-agent/internal/relay/protocol.go`
  Responsibility: control-plane request schema for relay transport mode.
- Modify: `go-agent/internal/relay/protocol_test.go`
  Responsibility: request encode/decode coverage including transport mode.
- Modify: `go-agent/internal/relay/runtime.go`
  Responsibility: fail-closed negotiation and first-segment runtime wiring.
- Modify: `go-agent/internal/relay/runtime_test.go`
  Responsibility: relay mode negotiation, unsupported-hop failure, and end-to-end obfs round trips.
- Create: `go-agent/internal/relay/obfs.go`
  Responsibility: isolated first-segment framing encoder/decoder and tunnel wrapper.
- Create: `go-agent/internal/relay/obfs_test.go`
  Responsibility: frame codec/state-machine tests.
- Modify: `go-agent/internal/proxy/server.go`
  Responsibility: pass HTTP rule `relay_obfs` into relay dialing.
- Modify: `go-agent/internal/proxy/server_test.go`
  Responsibility: HTTP relay transport test coverage for `relay_obfs`.
- Modify: `go-agent/internal/l4/server.go`
  Responsibility: pass L4 rule `relay_obfs` into relay dialing.
- Modify: `go-agent/internal/l4/server_test.go`
  Responsibility: L4 relay transport test coverage for `relay_obfs`.

## Task 1: Add `relay_obfs` to Control-Plane Rule Models and Validation

**Files:**
- Modify: `panel/backend-go/internal/controlplane/service/agents.go`
- Modify: `panel/backend-go/internal/controlplane/service/rules.go`
- Modify: `panel/backend-go/internal/controlplane/service/rules_test.go`
- Modify: `panel/backend-go/internal/controlplane/service/l4.go`
- Modify: `panel/backend-go/internal/controlplane/service/l4_test.go`

- [ ] **Step 1: Write failing control-plane tests for the new validation rules**

Add these tests before changing implementation:

```go
func TestRuleServiceCreateRejectsRelayObfsWithoutRelayChain(t *testing.T) {
	store := &fakeRuleStore{rulesByAgent: map[string][]storage.HTTPRuleRow{}}
	svc := NewRuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	_, err := svc.Create(context.Background(), "local", HTTPRuleInput{
		FrontendURL: stringPtrRule("https://relay.example.com"),
		BackendURL:  stringPtrRule("http://127.0.0.1:8096"),
		RelayObfs:   boolPtrRule(true),
	})
	if err == nil || err.Error() != "invalid argument: relay_obfs requires non-empty relay_chain" {
		t.Fatalf("Create() error = %v", err)
	}
}

func TestL4RuleServiceCreateRejectsRelayObfsWithoutRelayChain(t *testing.T) {
	store := &fakeL4Store{l4RulesByID: map[string][]storage.L4RuleRow{}}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	_, err := svc.Create(context.Background(), "local", L4RuleInput{
		Protocol:     stringPtrL4("tcp"),
		ListenPort:   intPtrL4(9000),
		UpstreamHost: stringPtrL4("upstream"),
		UpstreamPort: intPtrL4(9001),
		RelayObfs:    boolPtrL4(true),
	})
	if err == nil || err.Error() != "invalid argument: relay_obfs requires non-empty relay_chain" {
		t.Fatalf("Create() error = %v", err)
	}
}

func TestL4RuleServiceCreateRejectsRelayObfsForUDP(t *testing.T) {
	store := &fakeL4Store{l4RulesByID: map[string][]storage.L4RuleRow{}}
	svc := NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local"}, store)

	_, err := svc.Create(context.Background(), "local", L4RuleInput{
		Protocol:     stringPtrL4("udp"),
		ListenPort:   intPtrL4(9000),
		UpstreamHost: stringPtrL4("upstream"),
		UpstreamPort: intPtrL4(9001),
		RelayObfs:    boolPtrL4(true),
	})
	if err == nil || err.Error() != "invalid argument: relay_obfs is only supported for tcp protocol" {
		t.Fatalf("Create() error = %v", err)
	}
}
```

- [ ] **Step 2: Run the focused service tests and confirm they fail for missing fields**

Run:

```bash
cd panel/backend-go
go test ./internal/controlplane/service -run "TestRuleServiceCreateRejectsRelayObfsWithoutRelayChain|TestL4RuleServiceCreateRejectsRelayObfsWithoutRelayChain|TestL4RuleServiceCreateRejectsRelayObfsForUDP" -count=1
```

Expected: FAIL with compile errors because `RelayObfs` fields and pointer helpers do not exist yet.

- [ ] **Step 3: Add `relay_obfs` to HTTP/L4 API structs and normalization**

Patch the service structs and normalizers:

```go
type HTTPRule struct {
	ID               int                `json:"id"`
	AgentID          string             `json:"agent_id"`
	FrontendURL      string             `json:"frontend_url"`
	BackendURL       string             `json:"backend_url"`
	Backends         []HTTPRuleBackend  `json:"backends"`
	LoadBalancing    HTTPLoadBalancing  `json:"load_balancing"`
	Enabled          bool               `json:"enabled"`
	Tags             []string           `json:"tags"`
	ProxyRedirect    bool               `json:"proxy_redirect"`
	RelayChain       []int              `json:"relay_chain"`
	RelayObfs        bool               `json:"relay_obfs"`
	PassProxyHeaders bool               `json:"pass_proxy_headers"`
	UserAgent        string             `json:"user_agent"`
	CustomHeaders    []HTTPCustomHeader `json:"custom_headers"`
	Revision         int                `json:"revision"`
}

type HTTPRuleInput struct {
	// ...
	RelayChain *[]int `json:"relay_chain,omitempty"`
	RelayObfs  *bool  `json:"relay_obfs,omitempty"`
	// ...
}

relayObfs := false
if fallback.ID > 0 {
	relayObfs = fallback.RelayObfs
}
if input.RelayObfs != nil {
	relayObfs = *input.RelayObfs
}
if relayObfs && len(relayChain) == 0 {
	return HTTPRule{}, fmt.Errorf("%w: relay_obfs requires non-empty relay_chain", ErrInvalidArgument)
}
```

```go
type L4Rule struct {
	// ...
	RelayChain []int `json:"relay_chain"`
	RelayObfs  bool  `json:"relay_obfs"`
	Enabled    bool  `json:"enabled"`
	// ...
}

type L4RuleInput struct {
	// ...
	RelayChain *[]int `json:"relay_chain,omitempty"`
	RelayObfs  *bool  `json:"relay_obfs,omitempty"`
	// ...
}

relayObfs := false
if fallback.ID > 0 {
	relayObfs = fallback.RelayObfs
}
if input.RelayObfs != nil {
	relayObfs = *input.RelayObfs
}
if relayObfs && !strings.EqualFold(protocol, "tcp") {
	return L4Rule{}, fmt.Errorf("%w: relay_obfs is only supported for tcp protocol", ErrInvalidArgument)
}
if relayObfs && len(relayChain) == 0 {
	return L4Rule{}, fmt.Errorf("%w: relay_obfs requires non-empty relay_chain", ErrInvalidArgument)
}
```

Also add helper:

```go
func boolPtrL4(value bool) *bool { return &value }
```

- [ ] **Step 4: Run service tests and confirm they pass**

Run:

```bash
cd panel/backend-go
go test ./internal/controlplane/service -run "TestRuleServiceCreateRejectsRelayObfsWithoutRelayChain|TestL4RuleServiceCreateRejectsRelayObfsWithoutRelayChain|TestL4RuleServiceCreateRejectsRelayObfsForUDP|TestRuleServiceCreateNormalizesAndPersists|TestRuleServiceUpdateNormalizesAndPersists|TestL4RuleServiceCreateRejectsRelayChainForUDP" -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add panel/backend-go/internal/controlplane/service/agents.go panel/backend-go/internal/controlplane/service/rules.go panel/backend-go/internal/controlplane/service/rules_test.go panel/backend-go/internal/controlplane/service/l4.go panel/backend-go/internal/controlplane/service/l4_test.go
git commit -m "feat(backend): add relay obfs rule validation"
```

## Task 2: Persist `relay_obfs` in SQLite Rows and Snapshots

**Files:**
- Modify: `panel/backend-go/internal/controlplane/storage/sqlite_models.go`
- Modify: `panel/backend-go/internal/controlplane/storage/snapshot_types.go`
- Modify: `panel/backend-go/internal/controlplane/storage/schema.go`
- Modify: `panel/backend-go/internal/controlplane/service/agents.go`
- Modify: `panel/backend-go/internal/controlplane/storage/sqlite_store_test.go`

- [ ] **Step 1: Write failing storage test for legacy backfill and snapshot exposure**

Extend `TestBootstrapSQLiteSchemaUpgradesLegacySQLiteAndNormalizesBackfills`:

```go
`INSERT INTO rules (
	id, agent_id, frontend_url, backend_url, backends, load_balancing, enabled, tags, proxy_redirect,
	pass_proxy_headers, user_agent, custom_headers, relay_chain, relay_obfs, revision
) VALUES (7, 'legacy-agent', 'https://legacy.example.com', 'http://127.0.0.1:8096', NULL, NULL, 1, NULL, 1, NULL, NULL, NULL, '', NULL, 0)`,
`INSERT INTO l4_rules (
	id, agent_id, name, protocol, listen_host, listen_port, upstream_host, upstream_port, backends,
	load_balancing, tuning, relay_chain, relay_obfs, enabled, tags, revision
) VALUES (8, 'legacy-agent', 'legacy-l4', 'tcp', '0.0.0.0', 25565, '127.0.0.1', 25566, NULL, NULL, NULL, '', NULL, 1, NULL, 0)`,
```

Add assertions:

```go
if rules[0].RelayObfs {
	t.Fatalf("expected relay_obfs legacy backfill to default false: %+v", rules[0])
}

l4Rules, err := store.ListL4Rules(t.Context(), "legacy-agent")
if err != nil {
	t.Fatalf("ListL4Rules() error = %v", err)
}
if len(l4Rules) != 1 || l4Rules[0].RelayObfs {
	t.Fatalf("expected l4 relay_obfs legacy backfill to default false: %+v", l4Rules)
}
```

- [ ] **Step 2: Run the storage test and confirm it fails**

Run:

```bash
cd panel/backend-go
go test ./internal/controlplane/storage -run "TestBootstrapSQLiteSchemaUpgradesLegacySQLiteAndNormalizesBackfills" -count=1
```

Expected: FAIL because the new SQLite column and JSON field do not exist yet.

- [ ] **Step 3: Add the new persisted fields and backfill**

Apply changes like:

```go
type HTTPRuleRow struct {
	// ...
	RelayChainJSON string `gorm:"column:relay_chain"`
	RelayObfs      bool   `gorm:"column:relay_obfs"`
	// ...
}

type L4RuleRow struct {
	// ...
	RelayChainJSON string `gorm:"column:relay_chain"`
	RelayObfs      bool   `gorm:"column:relay_obfs"`
	// ...
}
```

```go
type HTTPRule struct {
	// ...
	RelayChain []int `json:"relay_chain,omitempty"`
	RelayObfs  bool  `json:"relay_obfs,omitempty"`
	Revision   int64 `json:"revision,omitempty"`
}

type L4Rule struct {
	// ...
	RelayChain []int `json:"relay_chain,omitempty"`
	RelayObfs  bool  `json:"relay_obfs,omitempty"`
	Revision   int64 `json:"revision,omitempty"`
}
```

In `schema.go`, add normalization statements:

```go
`UPDATE rules SET relay_obfs = 0 WHERE relay_obfs IS NULL`,
`UPDATE l4_rules SET relay_obfs = 0 WHERE relay_obfs IS NULL`,
```

Update row conversion functions in `agents.go`, `rules.go`, and `l4.go` so `RelayObfs` flows in both directions.

- [ ] **Step 4: Run storage and service packages again**

Run:

```bash
cd panel/backend-go
go test ./internal/controlplane/storage ./internal/controlplane/service -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add panel/backend-go/internal/controlplane/storage/sqlite_models.go panel/backend-go/internal/controlplane/storage/snapshot_types.go panel/backend-go/internal/controlplane/storage/schema.go panel/backend-go/internal/controlplane/storage/sqlite_store_test.go panel/backend-go/internal/controlplane/service/agents.go panel/backend-go/internal/controlplane/service/rules.go panel/backend-go/internal/controlplane/service/l4.go
git commit -m "feat(storage): persist relay obfs rule flag"
```

## Task 3: Expose the Toggle in Frontend Relay Tabs and API Payloads

**Files:**
- Modify: `panel/frontend/src/api/index.js`
- Modify: `panel/frontend/src/components/RuleForm.vue`
- Modify: `panel/frontend/src/components/L4RuleForm.vue`
- Create: `panel/frontend/src/components/relayObfsForm.test.mjs`

- [ ] **Step 1: Write a failing frontend source test for the new toggle and payload field**

Create `panel/frontend/src/components/relayObfsForm.test.mjs`:

```js
import test from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const __filename = fileURLToPath(import.meta.url)
const __dirname = path.dirname(__filename)

function read(name) {
  return fs.readFileSync(path.join(__dirname, name), 'utf8')
}

test('HTTP RuleForm exposes relay obfs toggle inside relay tab', () => {
  const source = read('RuleForm.vue')
  assert.match(source, /启用 Relay 隐私增强/)
  assert.match(source, /v-model="form\.relay_obfs"/)
})

test('L4 RuleForm exposes relay obfs toggle inside relay tab', () => {
  const source = read('L4RuleForm.vue')
  assert.match(source, /启用 Relay 隐私增强/)
  assert.match(source, /v-model="form\.relay_obfs"/)
})

test('API normalization keeps relay_obfs default false', () => {
  const source = fs.readFileSync(path.resolve(__dirname, '../api/index.js'), 'utf8')
  assert.match(source, /relay_obfs:\s*payload\.relay_obfs === true/)
})
```

- [ ] **Step 2: Run the frontend source test and confirm it fails**

Run:

```bash
cd panel/frontend
node --test src/components/relayObfsForm.test.mjs
```

Expected: FAIL because neither form nor API layer mentions `relay_obfs`.

- [ ] **Step 3: Add `relay_obfs` to normalization, mocks, and Relay-tab form state**

In `panel/frontend/src/api/index.js`, normalize both fetched rules and payloads:

```js
function normalizeHttpRule(rule = {}) {
  const backends = normalizeHttpBackends(rule)
  return {
    ...rule,
    backend_url: backends[0]?.url || String(rule.backend_url || '').trim(),
    backends,
    relay_obfs: rule.relay_obfs === true,
    load_balancing: {
      strategy: rule.load_balancing?.strategy === 'random' ? 'random' : 'round_robin'
    }
  }
}

function normalizeL4Rule(rule = {}) {
  const backends = normalizeL4Backends(rule)
  return {
    ...rule,
    upstream_host: backends[0]?.host || String(rule.upstream_host || '').trim(),
    upstream_port: backends[0]?.port || Number(rule.upstream_port) || 0,
    backends,
    relay_obfs: rule.relay_obfs === true,
    load_balancing: {
      strategy: rule.load_balancing?.strategy === 'random' ? 'random' : 'round_robin'
    }
  }
}
```

In `RuleForm.vue`:

```js
function createDefaultForm() {
  return {
    // ...
    relay_chain: [],
    relay_obfs: false
  }
}
```

```js
relay_obfs: initialData.relay_obfs === true,
```

```js
relay_obfs: Array.isArray(form.value.relay_chain) && form.value.relay_chain.length > 0
  ? form.value.relay_obfs === true
  : false
```

Render under the Relay config card:

```vue
<div class="settings-card">
  <div class="section-header">
    <div>
      <h3 class="section-title">隐私增强</h3>
      <p class="section-description">降低 relay over TLS 首段特征暴露风险</p>
    </div>
  </div>
  <label class="toggle toggle--card" :class="{ 'toggle--active': form.relay_obfs, 'toggle--disabled': !form.relay_chain.length }">
    <input v-model="form.relay_obfs" type="checkbox" class="toggle__input" :disabled="!form.relay_chain.length">
    <span class="toggle__slider"></span>
    <span class="toggle__content">
      <span class="toggle__label">启用 Relay 隐私增强</span>
      <span class="toggle__desc">对 relay 隧道首段流量做混淆，默认关闭</span>
    </span>
  </label>
  <div v-if="!form.relay_chain.length" class="relay-alert relay-alert--info">
    <span>当前为直连模式，启用无效</span>
  </div>
</div>
```

Mirror the same pattern in `L4RuleForm.vue`, but force `false` when `protocol !== 'tcp'`.

- [ ] **Step 4: Run frontend source test and production build**

Run:

```bash
cd panel/frontend
node --test src/components/relayObfsForm.test.mjs
npm run build
```

Expected: both commands PASS.

- [ ] **Step 5: Commit**

```bash
git add panel/frontend/src/api/index.js panel/frontend/src/components/RuleForm.vue panel/frontend/src/components/L4RuleForm.vue panel/frontend/src/components/relayObfsForm.test.mjs
git commit -m "feat(panel): add relay obfs toggle to relay tabs"
```

## Task 4: Add Relay Transport Mode Negotiation and Fail-Closed Errors

**Files:**
- Modify: `go-agent/internal/model/http.go`
- Modify: `go-agent/internal/model/l4.go`
- Modify: `go-agent/internal/relay/protocol.go`
- Modify: `go-agent/internal/relay/protocol_test.go`
- Modify: `go-agent/internal/relay/runtime.go`
- Modify: `go-agent/internal/relay/runtime_test.go`
- Modify: `go-agent/internal/proxy/server.go`
- Modify: `go-agent/internal/l4/server.go`

- [ ] **Step 1: Write failing relay protocol/runtime tests for transport mode**

Extend `protocol_test.go`:

```go
func TestRelayRequestRoundTripsTransportMode(t *testing.T) {
	request := relayRequest{
		Network: "tcp",
		Target:  "127.0.0.1:443",
		Transport: relayTransport{
			Mode: relayTransportModeFirstSegmentV1,
		},
	}

	var sink bytes.Buffer
	if err := writeRelayRequest(&sink, request); err != nil {
		t.Fatalf("writeRelayRequest() error = %v", err)
	}
	got, err := readRelayRequest(bytes.NewReader(sink.Bytes()))
	if err != nil {
		t.Fatalf("readRelayRequest() error = %v", err)
	}
	if got.Transport.Mode != relayTransportModeFirstSegmentV1 {
		t.Fatalf("transport mode = %q", got.Transport.Mode)
	}
}
```

Add a runtime failure test:

```go
func TestDialFailsClosedWhenHopRejectsTransportMode(t *testing.T) {
	provider, hop := startUnsupportedModeRelayFixture(t)

	_, err := Dial(context.Background(), "tcp", "127.0.0.1:443", []Hop{hop}, provider, DialOptions{
		TransportMode: relayTransportModeFirstSegmentV1,
	})
	if err == nil || !strings.Contains(err.Error(), "first_segment_v1") {
		t.Fatalf("Dial() error = %v", err)
	}
}
```

- [ ] **Step 2: Run relay tests and confirm they fail**

Run:

```bash
cd go-agent
go test ./internal/relay -run "TestRelayRequestRoundTripsTransportMode|TestDialFailsClosedWhenHopRejectsTransportMode" -count=1
```

Expected: FAIL because `Transport`, `DialOptions`, and mode constants do not exist yet.

- [ ] **Step 3: Implement mode plumbing and explicit unsupported-mode response**

Use focused additions:

```go
type relayTransportMode string

const (
	relayTransportModeOff            relayTransportMode = ""
	relayTransportModeFirstSegmentV1 relayTransportMode = "first_segment_v1"
)

type relayTransport struct {
	Mode relayTransportMode `json:"mode,omitempty"`
}

type relayRequest struct {
	Network   string         `json:"network"`
	Target    string         `json:"target"`
	Chain     []Hop          `json:"chain,omitempty"`
	Transport relayTransport `json:"transport,omitempty"`
}

type DialOptions struct {
	TransportMode relayTransportMode
}
```

Update the relay dial path:

```go
func Dial(ctx context.Context, network, target string, chain []Hop, provider TLSMaterialProvider, opts ...DialOptions) (net.Conn, error) {
	var options DialOptions
	if len(opts) > 0 {
		options = opts[0]
	}
	// ...
	request := relayRequest{
		Network:   "tcp",
		Target:    target,
		Chain:     append([]Hop(nil), chain[1:]...),
		Transport: relayTransport{Mode: options.TransportMode},
	}
```

On the server side:

```go
switch request.Transport.Mode {
case relayTransportModeOff, relayTransportModeFirstSegmentV1:
	// allowed in this binary
default:
	_ = withFrameDeadline(clientConn, func() error {
		return writeRelayResponse(clientConn, relayResponse{
			OK:    false,
			Error: fmt.Sprintf("relay transport mode %s is not supported", request.Transport.Mode),
		})
	})
	return
}
```

Update callers:

```go
return relay.Dial(ctx, network, strings.TrimSpace(addr), hops, provider, relay.DialOptions{
	TransportMode: relayTransportModeForHTTPRule(rule),
})
```

```go
upstream, err = relay.Dial(s.ctx, "tcp", target, hops, s.relayProvider, relay.DialOptions{
	TransportMode: relayTransportModeForL4Rule(rule),
})
```

- [ ] **Step 4: Run relay, HTTP, and L4 focused tests**

Run:

```bash
cd go-agent
go test ./internal/relay ./internal/proxy ./internal/l4 -run "TransportMode|FailsClosed|Relay" -count=1
```

Expected: PASS for the newly added protocol and fail-closed coverage.

- [ ] **Step 5: Commit**

```bash
git add go-agent/internal/model/http.go go-agent/internal/model/l4.go go-agent/internal/relay/protocol.go go-agent/internal/relay/protocol_test.go go-agent/internal/relay/runtime.go go-agent/internal/relay/runtime_test.go go-agent/internal/proxy/server.go go-agent/internal/l4/server.go
git commit -m "feat(agent): negotiate relay obfs transport mode"
```

## Task 5: Implement First-Segment Framing and Wire It Into HTTP/L4 Relay Paths

**Files:**
- Create: `go-agent/internal/relay/obfs.go`
- Create: `go-agent/internal/relay/obfs_test.go`
- Modify: `go-agent/internal/relay/runtime.go`
- Modify: `go-agent/internal/relay/runtime_test.go`
- Modify: `go-agent/internal/proxy/server_test.go`
- Modify: `go-agent/internal/l4/server_test.go`

- [ ] **Step 1: Write failing framing tests in isolation**

Create `go-agent/internal/relay/obfs_test.go`:

```go
func TestObfsFramesRoundTripOriginalBytes(t *testing.T) {
	payload := bytes.Repeat([]byte{0x16, 0x03, 0x01, 0x20}, 256)
	var framed bytes.Buffer

	writer := newObfsFirstSegmentWriter(&framed, obfsConfig{
		MaxDataBytes: 4096,
		MaxPadFrames: 4,
		Seed:         1,
	})
	if _, err := writer.Write(payload); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reader := newObfsFirstSegmentReader(bytes.NewReader(framed.Bytes()), obfsConfig{MaxDataBytes: 4096})
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload mismatch")
	}
}

func TestObfsReaderRejectsFrameAfterEnd(t *testing.T) {
	stream := buildInvalidObfsStreamForTest()
	reader := newObfsFirstSegmentReader(bytes.NewReader(stream), obfsConfig{MaxDataBytes: 4096})
	if _, err := io.ReadAll(reader); err == nil {
		t.Fatal("expected invalid relay obfs frame error")
	}
}
```

- [ ] **Step 2: Run the new relay framing tests and confirm they fail**

Run:

```bash
cd go-agent
go test ./internal/relay -run "TestObfsFramesRoundTripOriginalBytes|TestObfsReaderRejectsFrameAfterEnd" -count=1
```

Expected: FAIL because `obfs.go` and helpers do not exist yet.

- [ ] **Step 3: Implement isolated framing helpers and apply them from the first hop**

Create `obfs.go` with a small state machine:

```go
type obfsFrameType byte

const (
	obfsFrameData obfsFrameType = 1
	obfsFramePad  obfsFrameType = 2
	obfsFrameEnd  obfsFrameType = 3
)

type obfsConfig struct {
	MaxDataBytes int
	MaxPadFrames int
	Seed         int64
}
```

Writer behavior:

```go
// cache until MaxDataBytes or Close, emit:
// [data][pad?][data][pad?]...[end]
```

Reader behavior:

```go
// consume frames until end, rebuild only data payload, reject any frame after end
```

In `runtime.go`, after successful relay response:

```go
if request.Transport.Mode == relayTransportModeFirstSegmentV1 {
	return wrapConnWithFirstSegmentObfs(relayConn, defaultObfsConfig()), nil
}
return relayConn, nil
```

For accepted inbound connections:

```go
if request.Transport.Mode == relayTransportModeFirstSegmentV1 {
	pipeBothWays(
		wrapIdleConn(newObfsFirstSegmentReaderConn(clientConn, defaultObfsConfig())),
		wrapIdleConn(newObfsFirstSegmentWriterConn(upstream, defaultObfsConfig())),
	)
	return
}
pipeBothWays(wrapIdleConn(clientConn), wrapIdleConn(upstream))
```

Keep the default config fixed and small:

```go
func defaultObfsConfig() obfsConfig {
	return obfsConfig{
		MaxDataBytes: 4096,
		MaxPadFrames: 4,
		Seed:         time.Now().UnixNano(),
	}
}
```

- [ ] **Step 4: Run relay package tests plus HTTP/L4 integration tests**

Run:

```bash
cd go-agent
go test ./internal/relay ./internal/proxy ./internal/l4 -count=1
```

Expected: PASS, including new end-to-end cases where HTTP and L4 relay dialing still round-trips application bytes correctly with `relay_obfs=true`.

- [ ] **Step 5: Commit**

```bash
git add go-agent/internal/relay/obfs.go go-agent/internal/relay/obfs_test.go go-agent/internal/relay/runtime.go go-agent/internal/relay/runtime_test.go go-agent/internal/proxy/server_test.go go-agent/internal/l4/server_test.go
git commit -m "feat(agent): obfuscate first relay segment"
```

## Task 6: Full Verification and Documentation Sweep

**Files:**
- Modify: `docs/superpowers/specs/2026-04-12-relay-obfs-design.md` only if implementation changed a committed design detail
- No new source files expected

- [ ] **Step 1: Run the full backend and agent test suites**

Run:

```bash
cd panel/backend-go
go test ./...
cd ../../go-agent
go test ./...
```

Expected: PASS.

- [ ] **Step 2: Run the frontend checks**

Run:

```bash
cd panel/frontend
node --test src/a11ySmoke.test.mjs src/components/relay/endpointState.test.mjs src/components/relayObfsForm.test.mjs
npm run build
```

Expected: PASS.

- [ ] **Step 3: Run container build verification if source changes reached the runtime image**

Run:

```bash
cd ../..
docker build -t nginx-reverse-emby .
```

Expected: successful image build.

- [ ] **Step 4: Check git diff for only intended files**

Run:

```bash
git status --short
git diff --stat
```

Expected: only the planned control-plane, frontend, relay, and test files are modified.

- [ ] **Step 5: Commit the final verification sweep**

```bash
git add .
git commit -m "test: verify relay obfs integration"
```

## Spec Coverage Check

- Rule-scoped `relay_obfs` field: covered by Task 1 and Task 2.
- HTTP and L4 Relay-tab toggle placement: covered by Task 3.
- Default-off behavior: covered by Task 1, Task 2, and Task 3.
- No Relay Listener model changes: enforced by File Map and omitted from every task.
- Fail-closed mode negotiation: covered by Task 4.
- First-hop inclusion: covered by Task 5 runtime wrapping from the dialer side.
- First-segment-only framing with `data` / `pad` / `end`: covered by Task 5.
- Legacy compatibility when field is absent: covered by Task 2 backfill plus Task 4 mode-off default.
- Full verification: covered by Task 6.
