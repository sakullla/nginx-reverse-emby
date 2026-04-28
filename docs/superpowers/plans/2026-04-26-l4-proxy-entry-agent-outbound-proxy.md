# L4 Proxy Entry And Agent Outbound Proxy Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add L4 proxy-entry listeners that accept SOCKS4/SOCKS4a/SOCKS5/HTTP CONNECT and add agent-level outbound proxy support for Relay `tls_tcp` dialing.

**Architecture:** Extend the control-plane L4 and agent models with explicit proxy-entry and outbound-proxy fields, then propagate them through snapshots to the Go agent. Implement reusable proxy protocol helpers in `go-agent/internal/proxyproto`, use them from L4 proxy-entry handling and Relay `tls_tcp` outbound dialing, and add focused Vue controls for the new L4/agent settings.

**Tech Stack:** Go 1.26, GORM/SQLite, standard `net/http`, Vue 3/Vite/Vitest, SQLite JSON columns.

---

## File Structure Map

Backend model and storage:

- Modify `panel/backend-go/internal/controlplane/storage/sqlite_models.go`: add `OutboundProxyURL` to `AgentRow`; add proxy-entry fields to `L4RuleRow`.
- Modify `panel/backend-go/internal/controlplane/storage/schema.go`: add SQLite column migrations and normalization for new agent/L4 columns.
- Modify `panel/backend-go/internal/controlplane/storage/sqlite_store.go`: persist and load new columns in agent and L4 row flows.
- Modify `panel/backend-go/internal/controlplane/storage/snapshot_types.go`: include new snapshot fields if storage snapshot types are explicit.
- Modify `panel/backend-go/internal/controlplane/service/agents.go`: add outbound proxy fields to agent service models and validation.
- Modify `panel/backend-go/internal/controlplane/service/l4.go`: add `listen_mode`, proxy entry auth, proxy egress mode, and proxy egress URL to models, inputs, normalization, and validation.
- Modify `panel/backend-go/internal/controlplane/http/handlers_agents.go`: accept outbound proxy URL updates and redact secrets in responses where applicable.
- Modify `panel/backend-go/internal/controlplane/http/handlers_public.go`: include agent outbound proxy configuration in heartbeat sync payload.

Go agent model and runtime:

- Modify `go-agent/internal/model/types.go`: add `AgentConfig` snapshot field for node-level outbound proxy.
- Modify `go-agent/internal/model/l4.go`: add L4 proxy-entry fields.
- Create `go-agent/internal/proxyproto/url.go`: parse proxy URLs and redact secrets.
- Create `go-agent/internal/proxyproto/server.go`: parse SOCKS4/SOCKS4a/SOCKS5/HTTP CONNECT requests from accepted client connections.
- Create `go-agent/internal/proxyproto/dialer.go`: create outbound tunnels through SOCKS4/SOCKS4a/SOCKS5/HTTP CONNECT proxies.
- Modify `go-agent/internal/l4/server.go`: branch `listen_mode=proxy` TCP connections through proxy-entry parsing, then egress through Relay or upstream proxy.
- Modify `go-agent/internal/relay/tls_tcp_session_pool.go`: dial Relay `tls_tcp` through the agent outbound proxy when configured.
- Modify `go-agent/internal/relay/runtime.go`: thread outbound proxy configuration into Relay dialing options.
- Modify `go-agent/internal/app/local_runtime.go` and `go-agent/internal/runtime/runtime.go`: pass snapshot agent outbound proxy settings into L4 and Relay runtime construction.

Frontend:

- Modify `panel/frontend/src/components/L4RuleForm.vue`: add proxy-entry controls and egress mode fields for TCP rules.
- Modify `panel/frontend/src/components/l4/tuningState.js`: preserve existing tuning behavior while keeping proxy-entry fields outside tuning.
- Modify `panel/frontend/src/api/index.js`, `panel/frontend/src/api/runtime.js`, `panel/frontend/src/api/devRuntime.js`, `panel/frontend/src/api/devMocks/index.js`, and `panel/frontend/src/api/devMocks/data.js`: include new L4 and agent fields.
- Modify `panel/frontend/src/hooks/useAgents.js`: add `useUpdateAgent`.
- Modify `panel/frontend/src/pages/AgentDetailPage.vue`: expose node-level outbound proxy URL in the system information tab.

Tests:

- Backend: `panel/backend-go/internal/controlplane/service/l4_test.go`, `agents_test.go`, `storage/sqlite_store_test.go`, `http/router_test.go`, `http/public_test.go`.
- Agent: new `go-agent/internal/proxyproto/*_test.go`, plus `go-agent/internal/l4/server_test.go`, `go-agent/internal/relay/runtime_test.go`, `go-agent/internal/relay/tls_tcp_session_pool_test.go`.
- Frontend: existing L4 form tests or new focused tests under `panel/frontend/src/components`.

---

## Task 1: Backend Storage Columns

**Files:**
- Modify: `panel/backend-go/internal/controlplane/storage/sqlite_models.go`
- Modify: `panel/backend-go/internal/controlplane/storage/schema.go`
- Modify: `panel/backend-go/internal/controlplane/storage/sqlite_store.go`
- Test: `panel/backend-go/internal/controlplane/storage/sqlite_store_test.go`

- [ ] **Step 1: Write failing storage tests**

Add tests that save/load new columns:

```go
func TestSQLiteStorePersistsAgentOutboundProxyURL(t *testing.T) {
	ctx := context.Background()
	store := newTestSQLiteStore(t)
	agent := storage.AgentRow{
		ID:               "edge-a",
		Name:             "Edge A",
		CapabilitiesJSON: `["l4_rules","relay"]`,
		OutboundProxyURL: "socks://user:pass@127.0.0.1:1080",
	}
	if err := store.SaveAgent(ctx, agent); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	got, ok, err := store.GetAgent(ctx, "edge-a")
	if err != nil {
		t.Fatalf("GetAgent() error = %v", err)
	}
	if !ok {
		t.Fatal("GetAgent() ok = false")
	}
	if got.OutboundProxyURL != agent.OutboundProxyURL {
		t.Fatalf("OutboundProxyURL = %q, want %q", got.OutboundProxyURL, agent.OutboundProxyURL)
	}
}

func TestSQLiteStorePersistsL4ProxyEntryFields(t *testing.T) {
	ctx := context.Background()
	store := newTestSQLiteStore(t)
	row := storage.L4RuleRow{
		ID:               10,
		AgentID:          "edge-a",
		Name:             "proxy-entry",
		Protocol:         "tcp",
		ListenHost:       "127.0.0.1",
		ListenPort:       1080,
		UpstreamHost:     "",
		UpstreamPort:     0,
		BackendsJSON:     `[]`,
		LoadBalancingJSON: `{"strategy":"round_robin"}`,
		TuningJSON:       `{"proxy_protocol":{"decode":false,"send":false}}`,
		RelayChainJSON:   `[101]`,
		RelayLayersJSON:  `[[101]]`,
		ListenMode:       "proxy",
		ProxyEntryAuthJSON: `{"enabled":true,"username":"u","password":"p"}`,
		ProxyEgressMode:  "relay",
		ProxyEgressURL:   "",
		Enabled:          true,
		TagsJSON:         `[]`,
		Revision:         1,
	}
	if err := store.SaveL4Rules(ctx, "edge-a", []storage.L4RuleRow{row}); err != nil {
		t.Fatalf("SaveL4Rules() error = %v", err)
	}
	rows, err := store.ListL4Rules(ctx, "edge-a")
	if err != nil {
		t.Fatalf("ListL4Rules() error = %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("ListL4Rules() len = %d", len(rows))
	}
	got := rows[0]
	if got.ListenMode != "proxy" || got.ProxyEgressMode != "relay" || got.ProxyEntryAuthJSON == "" {
		t.Fatalf("proxy fields not persisted: %+v", got)
	}
}
```

- [ ] **Step 2: Run tests to verify failure**

Run: `cd panel/backend-go && go test ./internal/controlplane/storage -run "TestSQLiteStorePersists(AgentOutboundProxyURL|L4ProxyEntryFields)" -count=1`

Expected: compile failure because new row fields do not exist.

- [ ] **Step 3: Add row fields and schema migrations**

Add to `AgentRow`:

```go
OutboundProxyURL string `gorm:"column:outbound_proxy_url"`
```

Add to `L4RuleRow`:

```go
ListenMode         string `gorm:"column:listen_mode"`
ProxyEntryAuthJSON string `gorm:"column:proxy_entry_auth"`
ProxyEgressMode    string `gorm:"column:proxy_egress_mode"`
ProxyEgressURL     string `gorm:"column:proxy_egress_url"`
```

In `BootstrapSQLiteSchema`, add column migrations:

```go
agentColumnMigrations := []struct {
	column string
	sql    string
}{
	{column: "outbound_proxy_url", sql: `ALTER TABLE agents ADD COLUMN outbound_proxy_url TEXT NOT NULL DEFAULT ''`},
}
for _, migration := range agentColumnMigrations {
	if tx.Migrator().HasColumn(&AgentRow{}, migration.column) {
		continue
	}
	if err := tx.Exec(migration.sql).Error; err != nil {
		return err
	}
}

l4ColumnMigrations := []struct {
	column string
	sql    string
}{
	{column: "listen_mode", sql: `ALTER TABLE l4_rules ADD COLUMN listen_mode TEXT NOT NULL DEFAULT 'tcp'`},
	{column: "proxy_entry_auth", sql: `ALTER TABLE l4_rules ADD COLUMN proxy_entry_auth TEXT NOT NULL DEFAULT '{}'`},
	{column: "proxy_egress_mode", sql: `ALTER TABLE l4_rules ADD COLUMN proxy_egress_mode TEXT NOT NULL DEFAULT ''`},
	{column: "proxy_egress_url", sql: `ALTER TABLE l4_rules ADD COLUMN proxy_egress_url TEXT NOT NULL DEFAULT ''`},
}
for _, migration := range l4ColumnMigrations {
	if tx.Migrator().HasColumn(&L4RuleRow{}, migration.column) {
		continue
	}
	if err := tx.Exec(migration.sql).Error; err != nil {
		return err
	}
}
```

Add normalization statements:

```go
`UPDATE agents SET outbound_proxy_url = '' WHERE outbound_proxy_url IS NULL`,
`UPDATE l4_rules SET listen_mode = 'tcp' WHERE listen_mode IS NULL OR trim(listen_mode) = ''`,
`UPDATE l4_rules SET proxy_entry_auth = '{}' WHERE proxy_entry_auth IS NULL OR trim(proxy_entry_auth) = ''`,
`UPDATE l4_rules SET proxy_egress_mode = '' WHERE proxy_egress_mode IS NULL`,
`UPDATE l4_rules SET proxy_egress_url = '' WHERE proxy_egress_url IS NULL`,
```

- [ ] **Step 4: Run storage tests**

Run: `cd panel/backend-go && go test ./internal/controlplane/storage -run "TestSQLiteStorePersists(AgentOutboundProxyURL|L4ProxyEntryFields)" -count=1`

Expected: pass.

- [ ] **Step 5: Commit**

```bash
git add panel/backend-go/internal/controlplane/storage/sqlite_models.go panel/backend-go/internal/controlplane/storage/schema.go panel/backend-go/internal/controlplane/storage/sqlite_store_test.go
git commit -m "feat(storage): add proxy entry and outbound proxy columns"
```

## Task 2: Backend Service Models And Validation

**Files:**
- Modify: `panel/backend-go/internal/controlplane/service/l4.go`
- Modify: `panel/backend-go/internal/controlplane/service/l4_test.go`
- Modify: `panel/backend-go/internal/controlplane/service/agents.go`
- Modify: `panel/backend-go/internal/controlplane/service/agents_test.go`

- [ ] **Step 1: Write failing L4 service tests**

Add tests:

```go
func TestNormalizeL4RuleInputAcceptsProxyEntryRelayEgress(t *testing.T) {
	protocol := "tcp"
	listenMode := "proxy"
	egressMode := "relay"
	relayLayers := [][]int{{101}}
	input := L4RuleInput{
		Protocol:       &protocol,
		ListenHost:     stringPtr("127.0.0.1"),
		ListenPort:     intPtr(1080),
		ListenMode:     &listenMode,
		ProxyEntryAuth: &L4ProxyEntryAuth{Enabled: true, Username: "u", Password: "p"},
		ProxyEgressMode: &egressMode,
		RelayLayers:    &relayLayers,
	}
	rule, err := normalizeL4RuleInput(input, L4Rule{}, 1)
	if err != nil {
		t.Fatalf("normalizeL4RuleInput() error = %v", err)
	}
	if rule.ListenMode != "proxy" || rule.ProxyEgressMode != "relay" {
		t.Fatalf("proxy entry fields = %+v", rule)
	}
	if !rule.ProxyEntryAuth.Enabled || rule.ProxyEntryAuth.Username != "u" || rule.ProxyEntryAuth.Password != "p" {
		t.Fatalf("ProxyEntryAuth = %+v", rule.ProxyEntryAuth)
	}
}

func TestNormalizeL4RuleInputRejectsProxyEntryWithoutEgress(t *testing.T) {
	protocol := "tcp"
	listenMode := "proxy"
	input := L4RuleInput{
		Protocol:   &protocol,
		ListenHost: stringPtr("127.0.0.1"),
		ListenPort: intPtr(1080),
		ListenMode: &listenMode,
	}
	_, err := normalizeL4RuleInput(input, L4Rule{}, 1)
	if err == nil || !strings.Contains(err.Error(), "proxy_egress_mode") {
		t.Fatalf("error = %v, want proxy_egress_mode validation", err)
	}
}

func TestNormalizeL4RuleInputAcceptsProxyEntryProxyEgress(t *testing.T) {
	protocol := "tcp"
	listenMode := "proxy"
	egressMode := "proxy"
	egressURL := "http://user:pass@127.0.0.1:8080"
	input := L4RuleInput{
		Protocol:        &protocol,
		ListenHost:      stringPtr("127.0.0.1"),
		ListenPort:      intPtr(1080),
		ListenMode:      &listenMode,
		ProxyEgressMode: &egressMode,
		ProxyEgressURL:  &egressURL,
	}
	rule, err := normalizeL4RuleInput(input, L4Rule{}, 1)
	if err != nil {
		t.Fatalf("normalizeL4RuleInput() error = %v", err)
	}
	if rule.ProxyEgressURL != egressURL {
		t.Fatalf("ProxyEgressURL = %q", rule.ProxyEgressURL)
	}
}
```

- [ ] **Step 2: Run L4 service tests to verify failure**

Run: `cd panel/backend-go && go test ./internal/controlplane/service -run "TestNormalizeL4RuleInput.*ProxyEntry" -count=1`

Expected: compile failure for missing types and fields.

- [ ] **Step 3: Add L4 service types**

Add:

```go
type L4ProxyEntryAuth struct {
	Enabled  bool   `json:"enabled"`
	Username string `json:"username"`
	Password string `json:"password,omitempty"`
}
```

Add fields to `L4Rule` and `L4RuleInput`:

```go
ListenMode       string           `json:"listen_mode"`
ProxyEntryAuth   L4ProxyEntryAuth `json:"proxy_entry_auth"`
ProxyEgressMode  string           `json:"proxy_egress_mode"`
ProxyEgressURL   string           `json:"proxy_egress_url"`
```

```go
ListenMode       *string           `json:"listen_mode,omitempty"`
ProxyEntryAuth   *L4ProxyEntryAuth `json:"proxy_entry_auth,omitempty"`
ProxyEgressMode  *string           `json:"proxy_egress_mode,omitempty"`
ProxyEgressURL   *string           `json:"proxy_egress_url,omitempty"`
```

- [ ] **Step 4: Implement L4 normalization**

In `normalizeL4RuleInput`, after protocol normalization:

```go
listenMode := strings.ToLower(strings.TrimSpace(defaultString(pointerString(input.ListenMode), fallback.ListenMode)))
if listenMode == "" {
	listenMode = "tcp"
}
if listenMode != "tcp" && listenMode != "proxy" {
	return L4Rule{}, fmt.Errorf("%w: listen_mode must be tcp or proxy", ErrInvalidArgument)
}
if listenMode == "proxy" && protocol != "tcp" {
	return L4Rule{}, fmt.Errorf("%w: listen_mode=proxy requires protocol tcp", ErrInvalidArgument)
}
```

After relay fields:

```go
proxyEntryAuth := fallback.ProxyEntryAuth
if input.ProxyEntryAuth != nil {
	proxyEntryAuth = normalizeL4ProxyEntryAuth(*input.ProxyEntryAuth)
}
proxyEgressMode := strings.ToLower(strings.TrimSpace(defaultString(pointerString(input.ProxyEgressMode), fallback.ProxyEgressMode)))
proxyEgressURL := strings.TrimSpace(defaultString(pointerString(input.ProxyEgressURL), fallback.ProxyEgressURL))
if listenMode != "proxy" {
	proxyEntryAuth = L4ProxyEntryAuth{}
	proxyEgressMode = ""
	proxyEgressURL = ""
} else {
	if proxyEgressMode != "relay" && proxyEgressMode != "proxy" {
		return L4Rule{}, fmt.Errorf("%w: proxy_egress_mode must be relay or proxy", ErrInvalidArgument)
	}
	if proxyEgressMode == "relay" && len(flattenRelayLayers(relayLayers)) == 0 && len(relayChain) == 0 {
		return L4Rule{}, fmt.Errorf("%w: proxy relay egress requires relay_chain or relay_layers", ErrInvalidArgument)
	}
	if proxyEgressMode == "proxy" && proxyEgressURL == "" {
		return L4Rule{}, fmt.Errorf("%w: proxy_egress_url is required for proxy egress", ErrInvalidArgument)
	}
}
```

Add helper:

```go
func normalizeL4ProxyEntryAuth(auth L4ProxyEntryAuth) L4ProxyEntryAuth {
	return L4ProxyEntryAuth{
		Enabled:  auth.Enabled,
		Username: strings.TrimSpace(auth.Username),
		Password: auth.Password,
	}
}
```

Set fields in returned `L4Rule`.

- [ ] **Step 5: Implement row conversion**

In `l4RuleFromRow`, parse `ProxyEntryAuthJSON` and default `ListenMode`:

```go
rule.ListenMode = defaultString(row.ListenMode, "tcp")
rule.ProxyEntryAuth = parseL4ProxyEntryAuth(row.ProxyEntryAuthJSON)
rule.ProxyEgressMode = strings.TrimSpace(row.ProxyEgressMode)
rule.ProxyEgressURL = strings.TrimSpace(row.ProxyEgressURL)
```

In `l4RuleToRow`, marshal the fields:

```go
ListenMode:         defaultString(rule.ListenMode, "tcp"),
ProxyEntryAuthJSON: marshalJSON(rule.ProxyEntryAuth, "{}"),
ProxyEgressMode:    rule.ProxyEgressMode,
ProxyEgressURL:     rule.ProxyEgressURL,
```

Add:

```go
func parseL4ProxyEntryAuth(raw string) L4ProxyEntryAuth {
	var auth L4ProxyEntryAuth
	if strings.TrimSpace(raw) == "" {
		return auth
	}
	if err := json.Unmarshal([]byte(raw), &auth); err != nil {
		return L4ProxyEntryAuth{}
	}
	return normalizeL4ProxyEntryAuth(auth)
}
```

- [ ] **Step 6: Run L4 service tests**

Run: `cd panel/backend-go && go test ./internal/controlplane/service -run "TestNormalizeL4RuleInput.*ProxyEntry" -count=1`

Expected: pass.

- [ ] **Step 7: Add agent outbound proxy service tests**

Add a focused test around the existing agent service update path:

```go
func TestAgentServiceUpdatePersistsOutboundProxyURL(t *testing.T) {
	ctx := context.Background()
	store := newFakeAgentStore()
	if err := store.SaveAgent(ctx, storage.AgentRow{
		ID:               "edge-a",
		Name:             "Edge A",
		AgentToken:       "token-a",
		CapabilitiesJSON: `["http_rules","l4","relay"]`,
		LastApplyStatus:  "success",
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	svc := NewAgentService(config.Config{}, store)
	input := UpdateAgentRequest{
		OutboundProxyURL: stringPtr("socks://user:pass@127.0.0.1:1080"),
	}
	agent, err := svc.Update(ctx, "edge-a", input)
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if agent.OutboundProxyURL != "socks://user:pass@127.0.0.1:1080" {
		t.Fatalf("OutboundProxyURL = %q", agent.OutboundProxyURL)
	}
}
```

- [ ] **Step 8: Implement agent outbound proxy fields**

Add `OutboundProxyURL string` to `AgentSummary` and `OutboundProxyURL *string` to `UpdateAgentRequest`. Map it to `storage.AgentRow.OutboundProxyURL` and trim whitespace on update. Validate only that a non-empty value contains `://`; the Go agent runtime parser performs scheme-specific validation in Task 4.

- [ ] **Step 9: Run service tests**

Run: `cd panel/backend-go && go test ./internal/controlplane/service -count=1`

Expected: pass.

- [ ] **Step 10: Commit**

```bash
git add panel/backend-go/internal/controlplane/service/l4.go panel/backend-go/internal/controlplane/service/l4_test.go panel/backend-go/internal/controlplane/service/agents.go panel/backend-go/internal/controlplane/service/agents_test.go
git commit -m "feat(backend): validate l4 proxy entry settings"
```

## Task 3: Snapshot And Agent Model Propagation

**Files:**
- Modify: `panel/backend-go/internal/controlplane/http/handlers_public.go`
- Modify: `panel/backend-go/internal/controlplane/http/public_test.go`
- Modify: `go-agent/internal/model/types.go`
- Modify: `go-agent/internal/model/l4.go`
- Modify: `go-agent/internal/model/snapshot_decode_test.go`

- [ ] **Step 1: Write failing public heartbeat test**

Add/update a heartbeat test to assert `agent_config.outbound_proxy_url` and L4 proxy fields are present:

```go
func TestHeartbeatResponseIncludesProxyEntryAndOutboundProxy(t *testing.T) {
	// Use existing public heartbeat test harness in public_test.go.
	// Configure fake AgentService reply with:
	// - one L4Rule containing listen_mode=proxy and proxy_egress_mode=relay
	// - OutboundProxyURL "socks://127.0.0.1:1080" on the heartbeat reply
	// Then decode response JSON and assert:
	// sync.agent_config.outbound_proxy_url == "socks://127.0.0.1:1080"
	// sync.l4_rules[0].listen_mode == "proxy"
	// sync.l4_rules[0].proxy_egress_mode == "relay"
}
```

- [ ] **Step 2: Run test to verify failure**

Run: `cd panel/backend-go && go test ./internal/controlplane/http -run TestHeartbeatResponseIncludesProxyEntryAndOutboundProxy -count=1`

Expected: compile or assertion failure.

- [ ] **Step 3: Add heartbeat agent config payload**

Add service reply field if needed:

```go
type AgentRuntimeConfig struct {
	OutboundProxyURL string `json:"outbound_proxy_url"`
}
```

In `heartbeatSyncPayload`, include:

```go
payload["agent_config"] = service.AgentRuntimeConfig{
	OutboundProxyURL: reply.OutboundProxyURL,
}
```

Keep this payload scoped to runtime settings needed by the agent.

- [ ] **Step 4: Add agent model fields**

In `go-agent/internal/model/types.go`:

```go
type AgentConfig struct {
	OutboundProxyURL string `json:"outbound_proxy_url,omitempty"`
}

type Snapshot struct {
	// existing fields...
	AgentConfig AgentConfig `json:"agent_config,omitempty"`
}
```

In `go-agent/internal/model/l4.go`, add:

```go
type L4ProxyEntryAuth struct {
	Enabled  bool   `json:"enabled,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}
```

Add to `L4Rule`:

```go
ListenMode      string           `json:"listen_mode,omitempty"`
ProxyEntryAuth  L4ProxyEntryAuth `json:"proxy_entry_auth,omitempty"`
ProxyEgressMode string           `json:"proxy_egress_mode,omitempty"`
ProxyEgressURL  string           `json:"proxy_egress_url,omitempty"`
```

- [ ] **Step 5: Add snapshot decode test**

In `snapshot_decode_test.go`, extend the fixture JSON with:

```json
"agent_config":{"outbound_proxy_url":"socks://127.0.0.1:1080"},
"l4_rules":[{
  "id":1,
  "protocol":"tcp",
  "listen_host":"127.0.0.1",
  "listen_port":1080,
  "listen_mode":"proxy",
  "proxy_entry_auth":{"enabled":true,"username":"u","password":"p"},
  "proxy_egress_mode":"relay",
  "relay_chain":[101]
}]
```

Assert decoded fields.

- [ ] **Step 6: Run model and HTTP tests**

Run:

```bash
cd go-agent && go test ./internal/model -count=1
cd ../panel/backend-go && go test ./internal/controlplane/http -run TestHeartbeatResponseIncludesProxyEntryAndOutboundProxy -count=1
```

Expected: pass.

- [ ] **Step 7: Commit**

```bash
git add panel/backend-go/internal/controlplane/http/handlers_public.go panel/backend-go/internal/controlplane/http/public_test.go go-agent/internal/model/types.go go-agent/internal/model/l4.go go-agent/internal/model/snapshot_decode_test.go
git commit -m "feat(sync): propagate proxy runtime settings"
```

## Task 4: Proxy URL Parser And Redaction

**Files:**
- Create: `go-agent/internal/proxyproto/url.go`
- Test: `go-agent/internal/proxyproto/url_test.go`

- [ ] **Step 1: Write parser tests**

Create tests:

```go
package proxyproto

import "testing"

func TestParseProxyURLAcceptsSupportedSchemes(t *testing.T) {
	cases := []string{
		"socks://user:pass@127.0.0.1:1080",
		"socks4://user@127.0.0.1:1080",
		"socks4a://user@proxy.local:1080",
		"socks5://user:pass@127.0.0.1:1080",
		"socks5h://user:pass@proxy.local:1080",
		"http://user:pass@proxy.local:8080",
	}
	for _, raw := range cases {
		cfg, err := ParseProxyURL(raw)
		if err != nil {
			t.Fatalf("ParseProxyURL(%q) error = %v", raw, err)
		}
		if cfg.Address == "" || cfg.Scheme == "" {
			t.Fatalf("ParseProxyURL(%q) = %+v", raw, cfg)
		}
	}
}

func TestParseProxyURLRejectsUnsupportedScheme(t *testing.T) {
	_, err := ParseProxyURL("ftp://proxy.local:21")
	if err == nil {
		t.Fatal("expected unsupported scheme error")
	}
}

func TestRedactProxyURL(t *testing.T) {
	got := RedactProxyURL("socks://user:pass@127.0.0.1:1080")
	want := "socks://user:xxxxx@127.0.0.1:1080"
	if got != want {
		t.Fatalf("RedactProxyURL() = %q, want %q", got, want)
	}
}
```

- [ ] **Step 2: Run tests to verify failure**

Run: `cd go-agent && go test ./internal/proxyproto -run "Test(ParseProxyURL|RedactProxyURL)" -count=1`

Expected: package missing failure.

- [ ] **Step 3: Implement URL parser**

Create `url.go`:

```go
package proxyproto

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

type ProxyURL struct {
	Scheme          string
	Address         string
	Username        string
	Password        string
	RemoteDNS       bool
	SOCKSVersion    int
	HTTPConnect     bool
	Original        string
}

func ParseProxyURL(raw string) (ProxyURL, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ProxyURL{}, fmt.Errorf("proxy URL is required")
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return ProxyURL{}, err
	}
	scheme := strings.ToLower(strings.TrimSpace(u.Scheme))
	host := strings.TrimSpace(u.Host)
	if scheme == "" || host == "" {
		return ProxyURL{}, fmt.Errorf("proxy URL must include scheme and host")
	}
	if _, _, err := net.SplitHostPort(host); err != nil {
		return ProxyURL{}, fmt.Errorf("proxy URL host must include port: %w", err)
	}
	cfg := ProxyURL{Scheme: scheme, Address: host, Original: trimmed}
	if u.User != nil {
		cfg.Username = u.User.Username()
		cfg.Password, _ = u.User.Password()
	}
	switch scheme {
	case "socks":
		cfg.SOCKSVersion = 5
		cfg.RemoteDNS = true
	case "socks4":
		cfg.SOCKSVersion = 4
	case "socks4a":
		cfg.SOCKSVersion = 4
		cfg.RemoteDNS = true
	case "socks5":
		cfg.SOCKSVersion = 5
	case "socks5h":
		cfg.SOCKSVersion = 5
		cfg.RemoteDNS = true
	case "http":
		cfg.HTTPConnect = true
	default:
		return ProxyURL{}, fmt.Errorf("unsupported proxy URL scheme %q", scheme)
	}
	return cfg, nil
}

func RedactProxyURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.User == nil {
		return raw
	}
	username := u.User.Username()
	if _, hasPassword := u.User.Password(); hasPassword {
		u.User = url.UserPassword(username, "xxxxx")
	} else {
		u.User = url.User(username)
	}
	return u.String()
}
```

- [ ] **Step 4: Run parser tests**

Run: `cd go-agent && go test ./internal/proxyproto -run "Test(ParseProxyURL|RedactProxyURL)" -count=1`

Expected: pass.

- [ ] **Step 5: Commit**

```bash
git add go-agent/internal/proxyproto/url.go go-agent/internal/proxyproto/url_test.go
git commit -m "feat(agent): parse proxy URLs"
```

## Task 5: Proxy Entry Request Parser

**Files:**
- Create: `go-agent/internal/proxyproto/server.go`
- Test: `go-agent/internal/proxyproto/server_test.go`

- [ ] **Step 1: Write server parser tests**

Create table tests for SOCKS4, SOCKS4a, SOCKS5, and HTTP CONNECT:

```go
func TestReadClientRequestSOCKS4a(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	go func() {
		client.Write([]byte{0x04, 0x01, 0x01, 0xbb, 0, 0, 0, 1})
		client.Write([]byte("user\x00example.com\x00"))
	}()
	req, err := ReadClientRequest(context.Background(), server, EntryAuth{})
	if err != nil {
		t.Fatalf("ReadClientRequest() error = %v", err)
	}
	if req.Target != "example.com:443" {
		t.Fatalf("Target = %q", req.Target)
	}
}

func TestReadClientRequestSOCKS5PasswordAuth(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	go func() {
		client.Write([]byte{0x05, 0x01, 0x02})
		buf := make([]byte, 2)
		io.ReadFull(client, buf)
		client.Write([]byte{0x01, 0x01, 'u', 0x01, 'p'})
		io.ReadFull(client, buf)
		client.Write([]byte{0x05, 0x01, 0x00, 0x03, 11})
		client.Write([]byte("example.com"))
		client.Write([]byte{0x01, 0xbb})
		io.ReadFull(client, make([]byte, 10))
	}()
	req, err := ReadClientRequest(context.Background(), server, EntryAuth{Enabled: true, Username: "u", Password: "p"})
	if err != nil {
		t.Fatalf("ReadClientRequest() error = %v", err)
	}
	if req.Target != "example.com:443" {
		t.Fatalf("Target = %q", req.Target)
	}
}

func TestReadClientRequestHTTPConnect(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	go func() {
		fmt.Fprint(client, "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n")
		reply := make([]byte, 64)
		client.Read(reply)
	}()
	req, err := ReadClientRequest(context.Background(), server, EntryAuth{})
	if err != nil {
		t.Fatalf("ReadClientRequest() error = %v", err)
	}
	if req.Target != "example.com:443" || req.Protocol != "http" {
		t.Fatalf("request = %+v", req)
	}
}
```

- [ ] **Step 2: Run tests to verify failure**

Run: `cd go-agent && go test ./internal/proxyproto -run TestReadClientRequest -count=1`

Expected: compile failure for missing parser.

- [ ] **Step 3: Implement request parser API**

Create types:

```go
type EntryAuth struct {
	Enabled  bool
	Username string
	Password string
}

type ClientRequest struct {
	Protocol string
	Target   string
	Host     string
	Port     int
}
```

Implement:

```go
func ReadClientRequest(ctx context.Context, conn net.Conn, auth EntryAuth) (ClientRequest, error)
```

The implementation reads the first byte:

- `0x04`: parse SOCKS4/4a CONNECT and write SOCKS4 success/failure.
- `0x05`: parse SOCKS5 methods, auth, CONNECT, and write SOCKS5 success/failure.
- `C`: use `bufio.Reader` and `http.ReadRequest` for HTTP CONNECT.
- any other byte: return unsupported protocol.

Use a `prefixedConn` or `io.MultiReader` for HTTP so the first byte is not lost.

- [ ] **Step 4: Run parser tests**

Run: `cd go-agent && go test ./internal/proxyproto -run TestReadClientRequest -count=1`

Expected: pass.

- [ ] **Step 5: Commit**

```bash
git add go-agent/internal/proxyproto/server.go go-agent/internal/proxyproto/server_test.go
git commit -m "feat(agent): parse l4 proxy entry requests"
```

## Task 6: Outbound Proxy Dialer

**Files:**
- Create: `go-agent/internal/proxyproto/dialer.go`
- Test: `go-agent/internal/proxyproto/dialer_test.go`

- [ ] **Step 1: Write outbound dialer tests**

Add tests using local fake proxies:

```go
func TestDialViaHTTPConnectProxy(t *testing.T) {
	target := startTCPGreetingServer(t, "ok")
	proxy := startHTTPConnectProxy(t)
	conn, err := Dial(context.Background(), proxy.URL, target)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()
	if got := readGreeting(t, conn); got != "ok" {
		t.Fatalf("greeting = %q", got)
	}
}

func TestDialViaSOCKS5Proxy(t *testing.T) {
	target := startTCPGreetingServer(t, "ok")
	proxyAddr := startSOCKS5Proxy(t)
	conn, err := Dial(context.Background(), "socks5://"+proxyAddr, target)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()
	if got := readGreeting(t, conn); got != "ok" {
		t.Fatalf("greeting = %q", got)
	}
}
```

- [ ] **Step 2: Run tests to verify failure**

Run: `cd go-agent && go test ./internal/proxyproto -run TestDialVia -count=1`

Expected: missing `Dial`.

- [ ] **Step 3: Implement outbound dialer**

Implement:

```go
func Dial(ctx context.Context, proxyURL string, target string) (net.Conn, error)
```

Use `ParseProxyURL`, `net.Dialer.DialContext(ctx, "tcp", cfg.Address)`, then handshake:

- HTTP: write `CONNECT target HTTP/1.1`, `Host`, and optional `Proxy-Authorization: Basic`.
- SOCKS5: method selection, optional username/password, CONNECT with domain/IPv4/IPv6 target.
- SOCKS4: IPv4 target only.
- SOCKS4a: domain target.
- `socks://`: use SOCKS5 behavior.

- [ ] **Step 4: Run dialer tests**

Run: `cd go-agent && go test ./internal/proxyproto -run TestDialVia -count=1`

Expected: pass.

- [ ] **Step 5: Commit**

```bash
git add go-agent/internal/proxyproto/dialer.go go-agent/internal/proxyproto/dialer_test.go
git commit -m "feat(agent): dial through socks and http proxies"
```

## Task 7: L4 Proxy Entry Runtime

**Files:**
- Modify: `go-agent/internal/l4/server.go`
- Modify: `go-agent/internal/l4/engine.go`
- Test: `go-agent/internal/l4/server_test.go`
- Test: `go-agent/internal/l4/engine_test.go`

- [ ] **Step 1: Write validation tests**

Add:

```go
func TestValidateRuleAcceptsProxyEntryWithRelayEgress(t *testing.T) {
	rule := model.L4Rule{
		Protocol:       "tcp",
		ListenHost:     "127.0.0.1",
		ListenPort:     1080,
		ListenMode:     "proxy",
		ProxyEgressMode: "relay",
		RelayChain:     []int{101},
	}
	if err := ValidateRule(rule); err != nil {
		t.Fatalf("ValidateRule() error = %v", err)
	}
}

func TestValidateRuleRejectsProxyEntryUDP(t *testing.T) {
	rule := model.L4Rule{Protocol: "udp", ListenHost: "127.0.0.1", ListenPort: 1080, ListenMode: "proxy"}
	err := ValidateRule(rule)
	if err == nil || !strings.Contains(err.Error(), "listen_mode=proxy") {
		t.Fatalf("error = %v", err)
	}
}
```

- [ ] **Step 2: Run validation tests**

Run: `cd go-agent && go test ./internal/l4 -run "TestValidateRule.*ProxyEntry" -count=1`

Expected: fail until validation supports new fields.

- [ ] **Step 3: Update validation**

In `ValidateRule`, default empty `ListenMode` to `tcp`, accept `proxy` only for `protocol=tcp`, require relay/proxy egress for proxy mode.

- [ ] **Step 4: Write runtime tests**

Add:

```go
func TestL4ProxyEntrySOCKS5RelayEgress(t *testing.T) {
	backend := newTCPEchoListener(t)
	defer backend.Close()
	relayListener := newL4RelayListenerFixture(t, 101)
	relayServer, provider := startL4RelayRuntimeForBackend(t, relayListener, backend.Addr())
	defer relayServer.Close()
	listenPort := pickFreeTCPPort(t)
	rule := model.L4Rule{
		Protocol:       "tcp",
		ListenHost:     "127.0.0.1",
		ListenPort:     listenPort,
		ListenMode:     "proxy",
		ProxyEgressMode: "relay",
		RelayChain:     []int{101},
	}
	srv, err := NewServer(context.Background(), []model.L4Rule{rule}, []model.RelayListener{relayListener}, provider)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	defer srv.Close()
	conn := dialSOCKS5ThroughEntry(t, fmt.Sprintf("127.0.0.1:%d", listenPort), backend.Addr())
	defer conn.Close()
	assertRoundTrip(t, conn, []byte("proxy-relay"))
}
```

Also add HTTP CONNECT proxy-entry test using direct proxy egress fake upstream proxy.

- [ ] **Step 5: Implement L4 proxy-entry branch**

In `handleTCPConnection`:

```go
if strings.EqualFold(rule.ListenMode, "proxy") {
	s.handleProxyEntryConnection(client, rule)
	return
}
```

Implement `handleProxyEntryConnection`:

```go
func (s *Server) handleProxyEntryConnection(client net.Conn, rule model.L4Rule) {
	auth := proxyproto.EntryAuth{
		Enabled:  rule.ProxyEntryAuth.Enabled,
		Username: rule.ProxyEntryAuth.Username,
		Password: rule.ProxyEntryAuth.Password,
	}
	req, err := proxyproto.ReadClientRequest(s.ctx, client, auth)
	if err != nil {
		return
	}
	upstream, err := s.dialProxyEntryUpstream(rule, req.Target)
	if err != nil {
		return
	}
	defer upstream.Close()
	copyBidirectional(client, upstream)
}
```

Implement `dialProxyEntryUpstream`:

- Relay mode: use existing relay path dialer with target.
- Proxy mode: call `proxyproto.Dial(ctx, rule.ProxyEgressURL, target)`.

- [ ] **Step 6: Run L4 tests**

Run: `cd go-agent && go test ./internal/l4 -run "Test(L4ProxyEntry|ValidateRule.*ProxyEntry)" -count=1`

Expected: pass.

- [ ] **Step 7: Commit**

```bash
git add go-agent/internal/l4/server.go go-agent/internal/l4/engine.go go-agent/internal/l4/server_test.go go-agent/internal/l4/engine_test.go
git commit -m "feat(agent): add l4 proxy entry runtime"
```

## Task 8: Relay tls_tcp Agent Outbound Proxy

**Files:**
- Modify: `go-agent/internal/relay/runtime.go`
- Modify: `go-agent/internal/relay/tls_tcp_session_pool.go`
- Modify: `go-agent/internal/app/local_runtime.go`
- Modify: `go-agent/internal/runtime/runtime.go`
- Test: `go-agent/internal/relay/runtime_test.go`

- [ ] **Step 1: Write Relay outbound proxy test**

Add a test that starts an HTTP CONNECT proxy in front of a Relay listener and verifies `DialWithResult` reaches through it:

```go
func TestDialTLSTCPUsesOutboundProxy(t *testing.T) {
	listener, hop := newRelayEndpoint(t, provider, 701, "relay-proxy", "pin_only", true, false)
	server, err := Start(context.Background(), []Listener{listener}, provider)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer server.Close()
	proxy := startRelayHTTPConnectProxy(t)
	options := DialOptions{OutboundProxyURL: proxy.URL}
	conn, result, err := DialWithResult(context.Background(), "tcp", echoTargetAddr, []Hop{hop}, provider, options)
	if err != nil {
		t.Fatalf("DialWithResult() error = %v", err)
	}
	defer conn.Close()
	if result.TransportMode != ListenerTransportModeTLSTCP {
		t.Fatalf("TransportMode = %q", result.TransportMode)
	}
	if !proxy.SawConnectTo(hop.Address) {
		t.Fatalf("proxy did not see CONNECT to %s", hop.Address)
	}
}
```

- [ ] **Step 2: Run Relay test to verify failure**

Run: `cd go-agent && go test ./internal/relay -run TestDialTLSTCPUsesOutboundProxy -count=1`

Expected: compile failure for `OutboundProxyURL`.

- [ ] **Step 3: Extend relay DialOptions**

Add:

```go
OutboundProxyURL string
```

to `DialOptions` and clone it in `clone()`.

- [ ] **Step 4: Use proxy dialer for tls_tcp**

In `dialNewTLSTCPTunnel`, replace:

```go
rawConn, err := dialRelayTCP(ctx, hop.Address)
```

with:

```go
rawConn, err := dialRelayTCPWithProxy(ctx, hop.Address, hop.Listener, options.OutboundProxyURL)
```

If `dialNewTLSTCPTunnel` does not currently receive options, thread `DialOptions` down from `dialTLSTCPMuxWithResult` and session pool acquisition. Add outbound proxy URL to the session pool key so proxied and direct tunnels are not reused interchangeably.

Implement:

```go
func dialRelayTCPWithProxy(ctx context.Context, address string, _ Listener, proxyURL string) (net.Conn, error) {
	if strings.TrimSpace(proxyURL) == "" {
		return dialRelayTCP(ctx, address)
	}
	return proxyproto.Dial(ctx, proxyURL, address)
}
```

- [ ] **Step 5: Thread snapshot agent config into runtime**

Where relay path dialers are created in app/runtime packages, pass `snapshot.AgentConfig.OutboundProxyURL` into `relay.DialOptions` for agent-initiated Relay dials. Keep direct L4 backend dialing unchanged in this task.

- [ ] **Step 6: Run Relay tests**

Run: `cd go-agent && go test ./internal/relay -run TestDialTLSTCPUsesOutboundProxy -count=1`

Expected: pass.

- [ ] **Step 7: Commit**

```bash
git add go-agent/internal/relay/runtime.go go-agent/internal/relay/tls_tcp_session_pool.go go-agent/internal/relay/runtime_test.go go-agent/internal/app/local_runtime.go go-agent/internal/runtime/runtime.go
git commit -m "feat(agent): route relay tls tcp through outbound proxy"
```

## Task 9: Backend HTTP API And Redaction

**Files:**
- Modify: `panel/backend-go/internal/controlplane/http/handlers_agents.go`
- Modify: `panel/backend-go/internal/controlplane/http/router_test.go`
- Modify: `panel/backend-go/internal/controlplane/service/agents.go`
- Test: `panel/backend-go/internal/controlplane/http/router_test.go`

- [ ] **Step 1: Write API tests**

Add/update router tests so agent update accepts:

```json
{"outbound_proxy_url":"socks://user:pass@127.0.0.1:1080"}
```

Assert service receives the full value, while response redacts password if the API response includes the value:

```json
"outbound_proxy_url":"socks://user:xxxxx@127.0.0.1:1080"
```

- [ ] **Step 2: Run API tests**

Run: `cd panel/backend-go && go test ./internal/controlplane/http -run "TestRouter.*Agent.*OutboundProxy" -count=1`

Expected: fail before handler wiring.

- [ ] **Step 3: Implement handler wiring and redaction**

Ensure JSON decoding maps `outbound_proxy_url` into `service.UpdateAgentRequest`. Add response redaction helper in service or HTTP layer:

```go
func redactProxyURL(raw string) string {
	// Use net/url, replace password with xxxxx.
}
```

Do not redact values before persistence or heartbeat sync; agents need the real credential.

- [ ] **Step 4: Run API tests**

Run: `cd panel/backend-go && go test ./internal/controlplane/http -run "TestRouter.*Agent.*OutboundProxy" -count=1`

Expected: pass.

- [ ] **Step 5: Commit**

```bash
git add panel/backend-go/internal/controlplane/http/handlers_agents.go panel/backend-go/internal/controlplane/http/router_test.go panel/backend-go/internal/controlplane/service/agents.go
git commit -m "feat(backend): expose agent outbound proxy setting"
```

## Task 10: Frontend L4 And Agent Controls

**Files:**
- Modify: `panel/frontend/src/components/L4RuleForm.vue`
- Modify: `panel/frontend/src/components/l4/tuningState.js`
- Modify: `panel/frontend/src/api/index.js`
- Modify: `panel/frontend/src/api/runtime.js`
- Modify: `panel/frontend/src/api/devRuntime.js`
- Modify: `panel/frontend/src/api/devMocks/index.js`
- Modify: `panel/frontend/src/api/devMocks/data.js`
- Modify: `panel/frontend/src/hooks/useAgents.js`
- Modify: `panel/frontend/src/pages/AgentDetailPage.vue`
- Test: `panel/frontend/src/components/l4ProxyEntryForm.test.mjs`

- [ ] **Step 1: Write L4 form test**

Create a lightweight source/assertion test matching existing frontend test style:

```js
import { readFileSync } from 'node:fs'
import { strict as assert } from 'node:assert'
import { describe, it } from 'node:test'

const source = readFileSync(new URL('./L4RuleForm.vue', import.meta.url), 'utf8')

describe('L4 proxy entry form', () => {
  it('contains proxy entry controls and payload fields', () => {
    assert.match(source, /listen_mode/)
    assert.match(source, /proxy_entry_auth/)
    assert.match(source, /proxy_egress_mode/)
    assert.match(source, /proxy_egress_url/)
  })
})
```

- [ ] **Step 2: Run frontend test to verify failure**

Run: `cd panel/frontend && node --test src/components/l4ProxyEntryForm.test.mjs`

Expected: fail because fields are not present.

- [ ] **Step 3: Add L4 form state and controls**

In `createForm`, add:

```js
listen_mode: initialData?.listen_mode || 'tcp',
proxy_entry_auth: {
  enabled: initialData?.proxy_entry_auth?.enabled === true,
  username: initialData?.proxy_entry_auth?.username || '',
  password: initialData?.proxy_entry_auth?.password || ''
},
proxy_egress_mode: initialData?.proxy_egress_mode || 'relay',
proxy_egress_url: initialData?.proxy_egress_url || ''
```

In the protocol tab for TCP rules, add controls:

- select `listen_mode`: `tcp` or `proxy`
- when proxy: auth enabled, username, password
- when proxy: egress mode `relay` or `proxy`
- when egress `proxy`: proxy URL input
- when egress `relay`: existing relay chain controls remain relevant

In payload:

```js
listen_mode: form.value.protocol === 'tcp' ? form.value.listen_mode : 'tcp',
proxy_entry_auth: form.value.listen_mode === 'proxy' ? {
  enabled: form.value.proxy_entry_auth.enabled,
  username: form.value.proxy_entry_auth.username.trim(),
  password: form.value.proxy_entry_auth.password
} : { enabled: false, username: '', password: '' },
proxy_egress_mode: form.value.listen_mode === 'proxy' ? form.value.proxy_egress_mode : '',
proxy_egress_url: form.value.listen_mode === 'proxy' && form.value.proxy_egress_mode === 'proxy'
  ? form.value.proxy_egress_url.trim()
  : ''
```

- [ ] **Step 4: Add frontend API and hook for agent updates**

In `panel/frontend/src/api/index.js`, export:

```js
export const updateAgent = (...args) => call('updateAgent', ...args)
```

In `panel/frontend/src/api/runtime.js`, add:

```js
export async function updateAgent(agentId, payload) {
  const { data } = await api.patch(`/agents/${encodeURIComponent(agentId)}`, payload)
  return data.agent
}
```

In `panel/frontend/src/api/devRuntime.js` and `panel/frontend/src/api/devMocks/index.js`, re-export `updateAgent`.

In `panel/frontend/src/api/devMocks/data.js`, add:

```js
export async function updateAgent(agentId, payload = {}) {
  await delay()
  const agent = agents.find((item) => item.id === agentId)
  if (!agent) throw new Error('节点不存在')
  if (Object.prototype.hasOwnProperty.call(payload, 'outbound_proxy_url')) {
    agent.outbound_proxy_url = String(payload.outbound_proxy_url || '').trim()
  }
  return { ...agent }
}
```

In `panel/frontend/src/hooks/useAgents.js`, add:

```js
export function useUpdateAgent() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ agentId, payload }) => api.updateAgent(agentId, payload),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['agents'] })
      messageStore.success('节点设置已更新')
    },
    onError: (error) => {
      messageStore.error(error, '更新节点设置失败')
    }
  })
}
```

- [ ] **Step 5: Add agent outbound proxy control**

In `panel/frontend/src/pages/AgentDetailPage.vue`, import `watch` and `useUpdateAgent`, create a local ref initialized from `agent.outbound_proxy_url`, and add an input in the `info` tab bound to `outboundProxyURL`.

Use this save function:

```js
const updateAgent = useUpdateAgent()
const outboundProxyURL = ref('')

watch(agent, (value) => {
  outboundProxyURL.value = value?.outbound_proxy_url || ''
}, { immediate: true })

async function saveOutboundProxy() {
  if (!agent.value) return
  await updateAgent.mutateAsync({
    agentId: agent.value.id,
    payload: { outbound_proxy_url: outboundProxyURL.value.trim() }
  })
}
```

Use this placeholder:

```text
socks://user:pass@127.0.0.1:1080
```

- [ ] **Step 6: Run frontend tests/build**

Run:

```bash
cd panel/frontend
node --test src/components/l4ProxyEntryForm.test.mjs
npm run build
```

Expected: test and build pass.

- [ ] **Step 7: Commit**

```bash
git add panel/frontend/src/components/L4RuleForm.vue panel/frontend/src/components/l4ProxyEntryForm.test.mjs panel/frontend/src/api/index.js panel/frontend/src/api/runtime.js panel/frontend/src/api/devRuntime.js panel/frontend/src/api/devMocks/index.js panel/frontend/src/api/devMocks/data.js panel/frontend/src/hooks/useAgents.js panel/frontend/src/pages/AgentDetailPage.vue
git commit -m "feat(panel): add proxy entry and outbound proxy controls"
```

## Task 11: Full Verification

**Files:**
- No source edits expected unless verification exposes defects.

- [ ] **Step 1: Run backend tests**

Run: `cd panel/backend-go && go test ./...`

Expected: pass.

- [ ] **Step 2: Run agent tests**

Run: `cd go-agent && go test ./...`

Expected: pass.

- [ ] **Step 3: Run frontend build**

Run: `cd panel/frontend && npm run build`

Expected: pass.

- [ ] **Step 4: Run Docker build if prior tasks touched image-impacting files**

Run: `docker build -t nginx-reverse-emby .`

Expected: pass.
