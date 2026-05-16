# WireGuard Profile Clients And URI Egress Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let L4 rules use WireGuard as a transparent client-traffic inbound, let L4 rules use a pasted WireGuard URI as an outbound egress, and make WireGuard Profiles manage ordinary client access without exposing raw peer editing to normal users.

**Architecture:** Keep the agent runtime shape centered on WireGuard profiles. WireGuard inbound L4 rules consume flows captured from profile client traffic instead of requiring users to connect to a special `server_address:port`; L4 matching decides which backend, Relay path, or egress handles each flow. Direct L4 URI egress is parsed by the control plane and materialized as a managed hidden profile referenced by the L4 rule; reusable profiles and human clients use the same runtime peer model.

**Tech Stack:** Go control plane with GORM/SQLite storage, Go agent WireGuard netstack runtime, Vue 3/Vite frontend, standard Go tests and frontend build/test commands.

---

## File Map

- `panel/backend-go/internal/controlplane/storage/sqlite_models.go`: add profile metadata fields and client rows.
- `panel/backend-go/internal/controlplane/storage/schema.go`: migrate new columns/tables/indexes.
- `panel/backend-go/internal/controlplane/storage/sqlite_store.go`: list/save profile clients; hide managed profiles from normal profile lists; include effective peers in snapshots.
- `panel/backend-go/internal/controlplane/storage/snapshot_types.go`: extend WireGuard peer with optional reserved bytes if needed by runtime.
- `panel/backend-go/internal/controlplane/service/wireguard_uri.go`: new URI parser and redacted preview helpers.
- `panel/backend-go/internal/controlplane/service/wireguard_clients.go`: new client CRUD/config generation helpers.
- `panel/backend-go/internal/controlplane/service/wireguard.go`: profile defaults, endpoint fields, client/system peer assembly, URI import integration.
- `panel/backend-go/internal/controlplane/service/l4.go`: accept direct WireGuard URI egress and materialize managed profiles.
- `panel/backend-go/internal/controlplane/http/handlers_wireguard.go`: client routes, config download, URI parse/import routes.
- `panel/backend-go/internal/controlplane/http/handlers_l4.go`: wire L4 URI egress payloads.
- `go-agent/internal/model/wireguard.go`: add optional peer `reserved`.
- `go-agent/internal/wireguard/config.go`: normalize reserved bytes.
- `go-agent/internal/wireguard/runtime.go`: pass reserved bytes to IPC if supported by the current netstack API; otherwise keep parsed data in fingerprint only if no IPC support exists; expose captured TCP/UDP flow hooks for transparent inbound.
- `go-agent/internal/app/l4_runtime.go`: wire transparent WireGuard inbound profiles into the L4 server.
- `go-agent/internal/l4/server.go`: route captured WireGuard flows through existing L4 backend/Relay/egress handling.
- `panel/frontend/src/api/runtime.js`: add WireGuard URI parse/import/client APIs and canonical L4 payload fields.
- `panel/frontend/src/api/devMocks/data.js`: mock profile endpoint fields, clients, URI parse/import, and L4 direct URI egress.
- `panel/frontend/src/pages/WireGuardProfilesPage.vue`: hide raw peers in default flow; add profile endpoint fields, client table, config download/QR entry points, system connection placeholder.
- `panel/frontend/src/components/L4RuleForm.vue`: add transparent WireGuard inbound matching fields and WireGuard egress source selector: Profile vs URI.
- `panel/frontend/src/api/runtimeCanonicalPayloads.test.mjs`: cover canonical frontend payloads and raw-source checks.

## Phase 1: WireGuard URI Parser

### Task 1: Backend URI Parser

**Files:**
- Create: `panel/backend-go/internal/controlplane/service/wireguard_uri.go`
- Test: `panel/backend-go/internal/controlplane/service/wireguard_uri_test.go`

- [ ] **Step 1: Write parser tests**

Create `panel/backend-go/internal/controlplane/service/wireguard_uri_test.go`:

```go
package service

import "testing"

func TestParseWireGuardURIParsesOutboundProfile(t *testing.T) {
	parsed, err := ParseWireGuardURI("wireguard://client-private@example.com:51820?publickey=server-public&psk=shared&address=10.8.0.2%2F32%2Cfd00%3A%3A2%2F128&allowedips=0.0.0.0%2F0%2C%3A%3A%2F0&dns=1.1.1.1%2C2606%3A4700%3A4700%3A%3A1111&mtu=1280&reserved=1,2,3#warp")
	if err != nil {
		t.Fatalf("ParseWireGuardURI() error = %v", err)
	}
	if parsed.Name != "warp" || parsed.PrivateKey != "client-private" {
		t.Fatalf("parsed identity = %+v", parsed)
	}
	if parsed.Endpoint != "example.com:51820" || parsed.PeerPublicKey != "server-public" || parsed.PresharedKey != "shared" {
		t.Fatalf("parsed peer = %+v", parsed)
	}
	if len(parsed.Addresses) != 2 || parsed.Addresses[0] != "10.8.0.2/32" || parsed.Addresses[1] != "fd00::2/128" {
		t.Fatalf("addresses = %+v", parsed.Addresses)
	}
	if len(parsed.AllowedIPs) != 2 || parsed.AllowedIPs[0] != "0.0.0.0/0" || parsed.AllowedIPs[1] != "::/0" {
		t.Fatalf("allowed_ips = %+v", parsed.AllowedIPs)
	}
	if len(parsed.DNS) != 2 || parsed.DNS[0] != "1.1.1.1" || parsed.DNS[1] != "2606:4700:4700::1111" {
		t.Fatalf("dns = %+v", parsed.DNS)
	}
	if parsed.MTU != 1280 {
		t.Fatalf("mtu = %d", parsed.MTU)
	}
	if len(parsed.Reserved) != 3 || parsed.Reserved[0] != 1 || parsed.Reserved[1] != 2 || parsed.Reserved[2] != 3 {
		t.Fatalf("reserved = %+v", parsed.Reserved)
	}
}

func TestParseWireGuardURIDefaultsAllowedIPs(t *testing.T) {
	parsed, err := ParseWireGuardURI("wireguard://private@example.com:51820?publickey=server&address=10.8.0.2%2F32")
	if err != nil {
		t.Fatalf("ParseWireGuardURI() error = %v", err)
	}
	if len(parsed.AllowedIPs) != 2 || parsed.AllowedIPs[0] != "0.0.0.0/0" || parsed.AllowedIPs[1] != "::/0" {
		t.Fatalf("allowed_ips = %+v", parsed.AllowedIPs)
	}
}

func TestParseWireGuardURIRejectsMissingRequiredFields(t *testing.T) {
	tests := []string{
		"http://private@example.com:51820?publickey=server&address=10.8.0.2%2F32",
		"wireguard://example.com:51820?publickey=server&address=10.8.0.2%2F32",
		"wireguard://private@example.com:51820?address=10.8.0.2%2F32",
		"wireguard://private@example.com:51820?publickey=server",
		"wireguard://private@example.com?publickey=server&address=10.8.0.2%2F32",
		"wireguard://private@example.com:51820?publickey=server&address=10.8.0.2%2F32&reserved=256",
	}
	for _, raw := range tests {
		if _, err := ParseWireGuardURI(raw); err == nil {
			t.Fatalf("ParseWireGuardURI(%q) error = nil, want error", raw)
		}
	}
}

func TestWireGuardURIPreviewRedactsSecrets(t *testing.T) {
	parsed, err := ParseWireGuardURI("wireguard://private@example.com:51820?publickey=server&psk=shared&address=10.8.0.2%2F32#name")
	if err != nil {
		t.Fatalf("ParseWireGuardURI() error = %v", err)
	}
	preview := RedactWireGuardURIPreview(parsed)
	if preview.PrivateKey == "private" || preview.PresharedKey == "shared" {
		t.Fatalf("preview did not redact secrets: %+v", preview)
	}
	if preview.Endpoint != "example.com:51820" || preview.Name != "name" {
		t.Fatalf("preview = %+v", preview)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd panel/backend-go && go test ./internal/controlplane/service -run 'TestParseWireGuardURI|TestWireGuardURIPreview' -count=1`

Expected: FAIL because `ParseWireGuardURI` and `RedactWireGuardURIPreview` do not exist.

- [ ] **Step 3: Implement parser**

Create `panel/backend-go/internal/controlplane/service/wireguard_uri.go`:

```go
package service

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

type WireGuardURIProfile struct {
	Name          string   `json:"name"`
	PrivateKey    string   `json:"private_key,omitempty"`
	Endpoint      string   `json:"endpoint"`
	PeerPublicKey string   `json:"peer_public_key"`
	PresharedKey  string   `json:"preshared_key,omitempty"`
	Addresses     []string `json:"addresses"`
	AllowedIPs    []string `json:"allowed_ips"`
	DNS           []string `json:"dns"`
	MTU           int      `json:"mtu,omitempty"`
	Reserved      []byte   `json:"reserved,omitempty"`
}

func ParseWireGuardURI(raw string) (WireGuardURIProfile, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return WireGuardURIProfile{}, fmt.Errorf("%w: invalid wireguard uri", ErrInvalidArgument)
	}
	if !strings.EqualFold(parsed.Scheme, "wireguard") {
		return WireGuardURIProfile{}, fmt.Errorf("%w: wireguard uri scheme must be wireguard", ErrInvalidArgument)
	}
	privateKey := ""
	if parsed.User != nil {
		privateKey = strings.TrimSpace(parsed.User.Username())
	}
	if privateKey == "" {
		return WireGuardURIProfile{}, fmt.Errorf("%w: wireguard uri private key is required", ErrInvalidArgument)
	}
	host := strings.TrimSpace(parsed.Hostname())
	port := strings.TrimSpace(parsed.Port())
	if host == "" || port == "" {
		return WireGuardURIProfile{}, fmt.Errorf("%w: wireguard uri endpoint host and port are required", ErrInvalidArgument)
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return WireGuardURIProfile{}, fmt.Errorf("%w: wireguard uri endpoint port is invalid", ErrInvalidArgument)
	}
	values := parsed.Query()
	peerPublicKey := strings.TrimSpace(values.Get("publickey"))
	if peerPublicKey == "" {
		peerPublicKey = strings.TrimSpace(values.Get("peer_public_key"))
	}
	if peerPublicKey == "" {
		return WireGuardURIProfile{}, fmt.Errorf("%w: wireguard uri publickey is required", ErrInvalidArgument)
	}
	addresses := splitWireGuardURIList(values.Get("address"))
	if len(addresses) == 0 {
		addresses = splitWireGuardURIList(values.Get("addresses"))
	}
	if len(addresses) == 0 {
		return WireGuardURIProfile{}, fmt.Errorf("%w: wireguard uri address is required", ErrInvalidArgument)
	}
	allowedIPs := splitWireGuardURIList(values.Get("allowedips"))
	if len(allowedIPs) == 0 {
		allowedIPs = splitWireGuardURIList(values.Get("allowed_ips"))
	}
	if len(allowedIPs) == 0 {
		allowedIPs = []string{"0.0.0.0/0", "::/0"}
	}
	mtu := 0
	if rawMTU := strings.TrimSpace(values.Get("mtu")); rawMTU != "" {
		mtu, err = strconv.Atoi(rawMTU)
		if err != nil || mtu < 0 {
			return WireGuardURIProfile{}, fmt.Errorf("%w: wireguard uri mtu is invalid", ErrInvalidArgument)
		}
	}
	reserved, err := parseWireGuardURIReserved(values.Get("reserved"))
	if err != nil {
		return WireGuardURIProfile{}, err
	}
	return WireGuardURIProfile{
		Name:          strings.TrimSpace(parsed.Fragment),
		PrivateKey:    privateKey,
		Endpoint:      net.JoinHostPort(host, port),
		PeerPublicKey: peerPublicKey,
		PresharedKey:  strings.TrimSpace(values.Get("psk")),
		Addresses:     addresses,
		AllowedIPs:    allowedIPs,
		DNS:           splitWireGuardURIList(values.Get("dns")),
		MTU:           mtu,
		Reserved:      reserved,
	}, nil
}

func RedactWireGuardURIPreview(profile WireGuardURIProfile) WireGuardURIProfile {
	profile.PrivateKey = redactedProxyPassword
	if profile.PresharedKey != "" {
		profile.PresharedKey = redactedProxyPassword
	}
	return profile
}

func splitWireGuardURIList(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	})
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func parseWireGuardURIReserved(raw string) ([]byte, error) {
	parts := splitWireGuardURIList(raw)
	if len(parts) == 0 {
		return nil, nil
	}
	if len(parts) > 3 {
		return nil, fmt.Errorf("%w: wireguard uri reserved accepts at most 3 bytes", ErrInvalidArgument)
	}
	out := make([]byte, 0, len(parts))
	for _, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 || value > 255 {
			return nil, fmt.Errorf("%w: wireguard uri reserved byte is invalid", ErrInvalidArgument)
		}
		out = append(out, byte(value))
	}
	return out, nil
}
```

- [ ] **Step 4: Run parser tests**

Run: `cd panel/backend-go && go test ./internal/controlplane/service -run 'TestParseWireGuardURI|TestWireGuardURIPreview' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add panel/backend-go/internal/controlplane/service/wireguard_uri.go panel/backend-go/internal/controlplane/service/wireguard_uri_test.go
git commit -m "feat(wireguard): parse outbound URI profiles"
```

## Phase 2: L4 Direct URI Egress

### Task 2: Extend L4 Input And Managed Profile Materialization

**Files:**
- Modify: `panel/backend-go/internal/controlplane/service/l4.go`
- Modify: `panel/backend-go/internal/controlplane/storage/sqlite_models.go`
- Modify: `panel/backend-go/internal/controlplane/storage/schema.go`
- Modify: `panel/backend-go/internal/controlplane/storage/sqlite_store.go`
- Test: `panel/backend-go/internal/controlplane/service/l4_test.go`
- Test: `panel/backend-go/internal/controlplane/storage/sqlite_store_test.go`

- [ ] **Step 1: Write failing L4 service tests**

Add tests to `panel/backend-go/internal/controlplane/service/l4_test.go`:

```go
func TestL4RuleServiceCreateMaterializesWireGuardURIEgressProfile(t *testing.T) {
	store := &fakeL4Store{
		agents: []storage.AgentRow{{ID: "local", Name: "local"}},
		l4RulesByID: map[string][]storage.L4RuleRow{},
		wireGuardByAgentID: map[string][]storage.WireGuardProfileRow{},
	}
	svc := NewL4RuleService(config.Config{LocalAgentID: "local"}, store)

	rule, err := svc.Create(context.Background(), "local", L4RuleInput{
		Protocol:          stringPtrL4("tcp"),
		ListenMode:        stringPtrL4("proxy"),
		ListenPort:        intPtrL4(1080),
		ProxyEgressMode:   stringPtrL4("wireguard"),
		WireGuardEgressURI: stringPtrL4("wireguard://private@example.com:51820?publickey=server&address=10.8.0.2%2F32#warp"),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if rule.WireGuardProfileID == nil || *rule.WireGuardProfileID <= 0 {
		t.Fatalf("WireGuardProfileID = %v, want managed profile id", rule.WireGuardProfileID)
	}
	rows := store.wireGuardByAgentID["local"]
	if len(rows) != 1 {
		t.Fatalf("managed profiles = %+v", rows)
	}
	if rows[0].Name != "warp" || rows[0].PrivateKey != "private" || rows[0].ListenPort != 0 {
		t.Fatalf("managed profile = %+v", rows[0])
	}
	if rows[0].PeersJSON == "" || !strings.Contains(rows[0].PeersJSON, "example.com:51820") {
		t.Fatalf("managed profile peers = %s", rows[0].PeersJSON)
	}
}
```

Also add `WireGuardEgressURI *string` to the fake input compile errors by defining it in `L4RuleInput` during implementation.

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd panel/backend-go && go test ./internal/controlplane/service -run TestL4RuleServiceCreateMaterializesWireGuardURIEgressProfile -count=1`

Expected: FAIL because `WireGuardEgressURI` does not exist.

- [ ] **Step 3: Add storage metadata fields**

Add to `WireGuardProfileRow` in `panel/backend-go/internal/controlplane/storage/sqlite_models.go`:

```go
Direction     string `gorm:"column:direction;not null;default:'bidirectional'"`
Managed       bool   `gorm:"column:managed;not null;default:false"`
ManagedSource string `gorm:"column:managed_source;not null;default:''"`
```

Add normalizers in `normalizeWireGuardProfileRow`:

```go
row.Direction = defaultString(row.Direction, "bidirectional")
row.ManagedSource = defaultString(row.ManagedSource, "")
```

Ensure `BootstrapSQLiteSchema` auto-migrates these fields through existing model migration.

- [ ] **Step 4: Add L4 input field and materialization helper**

In `panel/backend-go/internal/controlplane/service/l4.go`, add:

```go
WireGuardEgressURI *string `json:"wireguard_egress_uri,omitempty"`
```

to `L4RuleInput`.

Add a helper near L4 normalization:

```go
func (s *l4Service) materializeWireGuardEgressURI(ctx context.Context, agentID string, ruleID int, rawURI string) (*int, error) {
	parsed, err := ParseWireGuardURI(rawURI)
	if err != nil {
		return nil, err
	}
	rows, err := s.store.ListWireGuardProfiles(ctx, agentID)
	if err != nil {
		return nil, err
	}
	profileID := nextWireGuardProfileID(rows)
	name := parsed.Name
	if strings.TrimSpace(name) == "" {
		name = fmt.Sprintf("l4-rule-%d-wireguard-egress", ruleID)
	}
	profile := WireGuardProfile{
		ID:         profileID,
		AgentID:    agentID,
		Name:       name,
		Mode:       "generic_wireguard",
		PrivateKey: parsed.PrivateKey,
		ListenPort: 0,
		Addresses:  parsed.Addresses,
		Peers: []WireGuardPeer{{
			Name:       "egress",
			PublicKey:  parsed.PeerPublicKey,
			PresharedKey: parsed.PresharedKey,
			Endpoint:   parsed.Endpoint,
			AllowedIPs: parsed.AllowedIPs,
		}},
		DNS:      parsed.DNS,
		MTU:      parsed.MTU,
		Enabled:  true,
		Revision: maxWireGuardProfileRevision(rows) + 1,
	}
	rows = append(rows, wireGuardProfileToRow(profile))
	if err := s.store.SaveWireGuardProfiles(ctx, agentID, rows); err != nil {
		return nil, err
	}
	return &profileID, nil
}
```

If `nextWireGuardProfileID` does not exist, add it:

```go
func nextWireGuardProfileID(rows []storage.WireGuardProfileRow) int {
	next := 1
	for _, row := range rows {
		if row.ID >= next {
			next = row.ID + 1
		}
	}
	return next
}
```

Then call materialization before validating WireGuard profile reference when `proxy_egress_mode=wireguard`, `wireguard_profile_id` is missing, and `wireguard_egress_uri` is non-empty.

- [ ] **Step 5: Run the L4 service test**

Run: `cd panel/backend-go && go test ./internal/controlplane/service -run TestL4RuleServiceCreateMaterializesWireGuardURIEgressProfile -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add panel/backend-go/internal/controlplane/service/l4.go panel/backend-go/internal/controlplane/service/l4_test.go panel/backend-go/internal/controlplane/storage/sqlite_models.go panel/backend-go/internal/controlplane/storage/schema.go panel/backend-go/internal/controlplane/storage/sqlite_store.go
git commit -m "feat(l4): materialize WireGuard URI egress profiles"
```

## Phase 3: HTTP API For URI Parse And Import

### Task 3: Add URI Preview And Import Endpoints

**Files:**
- Modify: `panel/backend-go/internal/controlplane/http/router.go`
- Modify: `panel/backend-go/internal/controlplane/http/handlers_wireguard.go`
- Test: `panel/backend-go/internal/controlplane/http/handlers_wireguard_test.go`

- [ ] **Step 1: Write HTTP tests**

Add tests to `handlers_wireguard_test.go`:

```go
func TestWireGuardParseURIEndpointRedactsSecrets(t *testing.T) {
	deps := testDependencies(t)
	handler := NewRouter(deps)
	req := httptest.NewRequest(http.MethodPost, "/panel-api/wireguard/parse-uri", strings.NewReader(`{"uri":"wireguard://private@example.com:51820?publickey=server&psk=shared&address=10.8.0.2%2F32#warp"}`))
	req.Header.Set("Authorization", "Bearer "+deps.Config.PanelToken)
	resp := httptest.NewRecorder()

	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("POST parse-uri = %d body=%s", resp.Code, resp.Body.String())
	}
	if strings.Contains(resp.Body.String(), "private") || strings.Contains(resp.Body.String(), "shared") {
		t.Fatalf("parse response leaked secret: %s", resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), `"endpoint":"example.com:51820"`) {
		t.Fatalf("parse response = %s", resp.Body.String())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd panel/backend-go && go test ./internal/controlplane/http -run TestWireGuardParseURIEndpointRedactsSecrets -count=1`

Expected: FAIL with 404.

- [ ] **Step 3: Implement handler**

Add to `router.go` route setup:

```go
mux.Handle(prefix+"/wireguard/parse-uri", resolved.requirePanelToken(http.HandlerFunc(resolved.handleWireGuardParseURI)))
```

Add to `handlers_wireguard.go`:

```go
func (s *Server) handleWireGuardParseURI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	var input struct {
		URI string `json:"uri"`
	}
	if err := readJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, errorPayload("invalid json body"))
		return
	}
	parsed, err := service.ParseWireGuardURI(input.URI)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"profile": service.RedactWireGuardURIPreview(parsed)})
}
```

- [ ] **Step 4: Run HTTP test**

Run: `cd panel/backend-go && go test ./internal/controlplane/http -run TestWireGuardParseURIEndpointRedactsSecrets -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add panel/backend-go/internal/controlplane/http/router.go panel/backend-go/internal/controlplane/http/handlers_wireguard.go panel/backend-go/internal/controlplane/http/handlers_wireguard_test.go
git commit -m "feat(wireguard): expose URI preview endpoint"
```

## Phase 4: Profile Clients

### Task 4: Add Profile Client Storage And Service

**Files:**
- Modify: `panel/backend-go/internal/controlplane/storage/sqlite_models.go`
- Modify: `panel/backend-go/internal/controlplane/storage/schema.go`
- Modify: `panel/backend-go/internal/controlplane/storage/sqlite_store.go`
- Create: `panel/backend-go/internal/controlplane/service/wireguard_clients.go`
- Test: `panel/backend-go/internal/controlplane/service/wireguard_clients_test.go`

- [ ] **Step 1: Write client service tests**

Create `wireguard_clients_test.go` with tests for:

```go
func TestWireGuardClientCreateAllocatesAddressAndGeneratesConfig(t *testing.T) {
	// Use SQLite store, create one profile with address_pool 10.8.0.0/24 and public endpoint wg.example.com:51820.
	// Call CreateClient.
	// Assert client address is 10.8.0.2/32.
	// Call ClientConfig.
	// Assert config contains Endpoint = wg.example.com:51820 and Address = 10.8.0.2/32.
}

func TestWireGuardClientConfigRejectsMissingEndpoint(t *testing.T) {
	// Create profile without public_endpoint_host.
	// Create client.
	// Call ClientConfig and assert ErrInvalidArgument with public endpoint message.
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd panel/backend-go && go test ./internal/controlplane/service -run TestWireGuardClient -count=1`

Expected: FAIL because client service does not exist.

- [ ] **Step 3: Add client row**

Add `WireGuardClientRow`:

```go
type WireGuardClientRow struct {
	ID               int    `gorm:"column:id;primaryKey"`
	AgentID          string `gorm:"column:agent_id;primaryKey;index:idx_wireguard_clients_agent_profile"`
	ProfileID        int    `gorm:"column:profile_id;primaryKey;index:idx_wireguard_clients_agent_profile"`
	Name             string `gorm:"column:name"`
	PrivateKey       string `gorm:"column:private_key"`
	PublicKey        string `gorm:"column:public_key"`
	PresharedKey     string `gorm:"column:preshared_key"`
	Address          string `gorm:"column:address"`
	AllowedIPsJSON   string `gorm:"column:allowed_ips"`
	DNSJSON          string `gorm:"column:dns"`
	Enabled          bool   `gorm:"column:enabled"`
	CreatedAt        string `gorm:"column:created_at"`
	UpdatedAt        string `gorm:"column:updated_at"`
}
```

Add to migration models and implement list/save methods in store.

- [ ] **Step 4: Implement service**

Create `wireguard_clients.go` with:

```go
type WireGuardClient struct {
	ID           int      `json:"id"`
	ProfileID    int      `json:"profile_id"`
	Name         string   `json:"name"`
	PublicKey    string   `json:"public_key"`
	Address      string   `json:"address"`
	AllowedIPs   []string `json:"allowed_ips"`
	DNS          []string `json:"dns"`
	Enabled      bool     `json:"enabled"`
	CreatedAt    string   `json:"created_at"`
	UpdatedAt    string   `json:"updated_at"`
}

type WireGuardClientInput struct {
	Name       string   `json:"name"`
	AllowedIPs []string `json:"allowed_ips"`
	DNS        []string `json:"dns"`
	Enabled    *bool    `json:"enabled,omitempty"`
}
```

Implement `CreateClient`, `DeleteClient`, `ClientConfig`, and allocation helpers. Key generation should use the same WireGuard key primitives already used by this repo; if no helper exists, add a focused `generateWireGuardPrivateKey()` wrapper with tests.

- [ ] **Step 5: Run client tests**

Run: `cd panel/backend-go && go test ./internal/controlplane/service -run TestWireGuardClient -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add panel/backend-go/internal/controlplane/storage/sqlite_models.go panel/backend-go/internal/controlplane/storage/schema.go panel/backend-go/internal/controlplane/storage/sqlite_store.go panel/backend-go/internal/controlplane/service/wireguard_clients.go panel/backend-go/internal/controlplane/service/wireguard_clients_test.go
git commit -m "feat(wireguard): manage profile clients"
```

## Phase 5: Transparent WireGuard Inbound

### Task 5: Route Captured WireGuard Client Traffic Through L4 Rules

**Files:**
- Modify: `go-agent/internal/wireguard/runtime.go`
- Modify: `go-agent/internal/app/l4_runtime.go`
- Modify: `go-agent/internal/l4/server.go`
- Modify: `go-agent/internal/model/l4.go`
- Test: `go-agent/internal/app/local_runtime_test.go`
- Test: `go-agent/internal/l4/server_test.go`

- [ ] **Step 1: Write failing agent behavior test**

Add to `go-agent/internal/app/local_runtime_test.go`:

```go
func TestL4RuntimeManagerRoutesTransparentWireGuardInboundTraffic(t *testing.T) {
	backendListener, backendAddr := startTCPBackend(t, func(conn net.Conn) {
		defer conn.Close()
		_, _ = conn.Write([]byte("ok"))
	})
	defer backendListener.Close()

	profileID := 41
	runtime := &testAppWireGuardRuntime{}
	manager := newL4RuntimeManagerWithWireGuardFactory(func(context.Context, wireguard.Config) (wireguard.Runtime, error) {
		return runtime, nil
	})
	defer manager.Close()

	host, portText, err := net.SplitHostPort(backendAddr)
	if err != nil {
		t.Fatalf("SplitHostPort(%q) error = %v", backendAddr, err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("Atoi(%q) error = %v", portText, err)
	}

	err = manager.ApplyWithRelayAndWireGuardProfiles(context.Background(), []model.L4Rule{{
		Protocol:           "tcp",
		ListenMode:         "wireguard",
		WireGuardProfileID: &profileID,
		ListenPort:         443,
		Backends:           []model.L4Backend{{Host: host, Port: port}},
	}}, nil, []model.WireGuardProfile{validAppWireGuardProfile(profileID)})
	if err != nil {
		t.Fatalf("ApplyWithRelayAndWireGuardProfiles() error = %v", err)
	}

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	if !runtime.DeliverTCPFlow("10.8.0.2:53100", "93.184.216.34:443", server) {
		t.Fatal("DeliverTCPFlow returned false, want matching L4 rule")
	}
	buf := make([]byte, 2)
	if _, err := io.ReadFull(client, buf); err != nil {
		t.Fatalf("ReadFull() error = %v", err)
	}
	if string(buf) != "ok" {
		t.Fatalf("response = %q, want ok", string(buf))
	}
}
```

If `testAppWireGuardRuntime` does not expose `DeliverTCPFlow`, add only the test method call first and let the test fail. This verifies the missing transparent inbound contract.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd go-agent && go test ./internal/app -run TestL4RuntimeManagerRoutesTransparentWireGuardInboundTraffic -count=1`

Expected: FAIL because the test runtime and manager do not support transparent flow delivery.

- [ ] **Step 3: Define transparent flow interfaces**

In `go-agent/internal/wireguard/runtime.go`, extend runtime support with interfaces rather than changing every runtime immediately:

```go
type TCPFlowHandler func(ctx context.Context, source string, destination string, conn net.Conn)

type TransparentTCPRuntime interface {
	Runtime
	SetTCPFlowHandler(handler TCPFlowHandler)
}
```

In test runtime, add:

```go
tcpFlowHandler wireguard.TCPFlowHandler

func (r *testAppWireGuardRuntime) SetTCPFlowHandler(handler wireguard.TCPFlowHandler) {
	r.tcpFlowHandler = handler
}

func (r *testAppWireGuardRuntime) DeliverTCPFlow(source, destination string, conn net.Conn) bool {
	if r.tcpFlowHandler == nil {
		return false
	}
	go r.tcpFlowHandler(context.Background(), source, destination, conn)
	return true
}
```

- [ ] **Step 4: Install handlers from L4 runtime**

In `go-agent/internal/app/l4_runtime.go`, after WireGuard profiles are prepared and before L4 server start, register a handler for profiles referenced by `listen_mode=wireguard` rules:

```go
func (m *l4RuntimeManager) installWireGuardTransparentInboundHandlersLocked(ctx context.Context, rules []model.L4Rule, provider relay.WireGuardRuntimeProvider, server *l4.Server) error {
	for _, rule := range rules {
		if !strings.EqualFold(strings.TrimSpace(rule.ListenMode), "wireguard") || rule.WireGuardProfileID == nil || *rule.WireGuardProfileID <= 0 {
			continue
		}
		runtime, ok := provider.WireGuardRuntime(*rule.WireGuardProfileID)
		if !ok || runtime == nil {
			return fmt.Errorf("wireguard profile %d runtime not found", *rule.WireGuardProfileID)
		}
		transparent, ok := runtime.(wireguard.TransparentTCPRuntime)
		if !ok {
			return fmt.Errorf("wireguard profile %d runtime does not support transparent tcp inbound", *rule.WireGuardProfileID)
		}
		transparent.SetTCPFlowHandler(func(ctx context.Context, source string, destination string, conn net.Conn) {
			server.HandleWireGuardTCPFlow(ctx, source, destination, conn)
		})
	}
	return nil
}
```

Adjust imports as needed.

- [ ] **Step 5: Add L4 server flow entrypoint**

In `go-agent/internal/l4/server.go`, add:

```go
func (s *Server) HandleWireGuardTCPFlow(ctx context.Context, source string, destination string, conn net.Conn) {
	rule, ok := s.matchWireGuardTCPFlow(destination)
	if !ok {
		_ = conn.Close()
		return
	}
	s.handleAcceptedTCPConn(ctx, rule, conn)
}

func (s *Server) matchWireGuardTCPFlow(destination string) (model.L4Rule, bool) {
	_, portText, err := net.SplitHostPort(destination)
	if err != nil {
		return model.L4Rule{}, false
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return model.L4Rule{}, false
	}
	for _, rule := range s.rules {
		if strings.EqualFold(strings.TrimSpace(rule.ListenMode), "wireguard") && strings.EqualFold(strings.TrimSpace(rule.Protocol), "tcp") && rule.ListenPort == port {
			return rule, true
		}
	}
	return model.L4Rule{}, false
}
```

If the existing server does not expose `rules` or `handleAcceptedTCPConn`, add narrow unexported helpers around the current TCP accept path instead of duplicating relay/backend forwarding logic.

- [ ] **Step 6: Run transparent inbound test**

Run: `cd go-agent && go test ./internal/app -run TestL4RuntimeManagerRoutesTransparentWireGuardInboundTraffic -count=1`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add go-agent/internal/wireguard/runtime.go go-agent/internal/app/l4_runtime.go go-agent/internal/app/local_runtime_test.go go-agent/internal/l4/server.go go-agent/internal/l4/server_test.go go-agent/internal/model/l4.go
git commit -m "feat(agent): route WireGuard client flows through L4"
```

## Phase 6: Frontend URI Egress

### Task 6: Add L4 WireGuard URI Form Support

**Files:**
- Modify: `panel/frontend/src/api/runtime.js`
- Modify: `panel/frontend/src/api/devMocks/data.js`
- Modify: `panel/frontend/src/components/L4RuleForm.vue`
- Test: `panel/frontend/src/api/runtimeCanonicalPayloads.test.mjs`

- [ ] **Step 1: Write frontend payload tests**

Add tests asserting:

```js
expect(payload.proxy_egress_mode).toBe('wireguard')
expect(payload.wireguard_egress_uri).toBe('wireguard://private@example.com:51820?publickey=server&address=10.8.0.2%2F32#warp')
expect(payload.wireguard_profile_id).toBeUndefined()
```

for URI mode, and existing profile mode still sends `wireguard_profile_id`.

Also add a raw-source assertion that when `listen_mode === 'wireguard'`, the form labels the profile selector as traffic entry and does not describe the user flow as connecting to `server_address:listen_port`.

- [ ] **Step 2: Run frontend tests to verify failure**

Run: `cd panel/frontend && npm run test -- runtimeCanonicalPayloads`

Expected: FAIL because the field is not emitted.

- [ ] **Step 3: Implement form changes**

In `L4RuleForm.vue`:

- add `wireguard_egress_source: 'profile' | 'uri'`.
- add `wireguard_egress_uri`.
- show profile selector when source is `profile`.
- show URI textarea/input when source is `uri`.
- submit `wireguard_egress_uri` only when `proxy_egress_mode === 'wireguard'` and source is `uri`.
- for `listen_mode === 'wireguard'`, present `listen_port` as the destination-port match used for transparent client traffic.
- hide or de-emphasize `wireguard_listen_host`; do not ask users to connect to a special WireGuard service address.

- [ ] **Step 4: Run frontend tests**

Run: `cd panel/frontend && npm run test -- runtimeCanonicalPayloads`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add panel/frontend/src/api/runtime.js panel/frontend/src/api/devMocks/data.js panel/frontend/src/components/L4RuleForm.vue panel/frontend/src/api/runtimeCanonicalPayloads.test.mjs
git commit -m "feat(panel): support WireGuard URI egress"
```

## Phase 7: Frontend Profile Clients

### Task 7: Add Client Management UI

**Files:**
- Modify: `panel/frontend/src/api/runtime.js`
- Modify: `panel/frontend/src/api/devMocks/data.js`
- Modify: `panel/frontend/src/pages/WireGuardProfilesPage.vue`

- [ ] **Step 1: Add runtime APIs**

Add functions:

```js
export async function fetchWireGuardClients(agentId, profileId) {}
export async function createWireGuardClient(agentId, profileId, payload) {}
export async function deleteWireGuardClient(agentId, profileId, clientId) {}
export async function fetchWireGuardClientConfig(agentId, profileId, clientId) {}
export async function parseWireGuardURI(uri) {}
export async function importWireGuardURIProfile(agentId, uri) {}
```

- [ ] **Step 2: Update Profile page UI**

In `WireGuardProfilesPage.vue`:

- add endpoint fields to create/edit modal.
- replace default Peers editor with Clients section.
- add "Advanced legacy peers" collapsed section for existing peers.
- add create client modal.
- add download config action that calls config endpoint and downloads text.

- [ ] **Step 3: Run frontend checks**

Run:

```bash
cd panel/frontend
npm run test
npm run build
```

Expected: both PASS.

- [ ] **Step 4: Commit**

```bash
git add panel/frontend/src/api/runtime.js panel/frontend/src/api/devMocks/data.js panel/frontend/src/pages/WireGuardProfilesPage.vue
git commit -m "feat(panel): manage WireGuard profile clients"
```

## Final Verification

- [ ] Run backend tests:

```bash
cd panel/backend-go
go test ./... -count=1
```

Expected: PASS.

- [ ] Run agent tests:

```bash
cd go-agent
go test ./... -count=1
```

Expected: PASS.

- [ ] Run frontend tests and build:

```bash
cd panel/frontend
npm run test
npm run build
```

Expected: PASS.

- [ ] Run diff checks:

```bash
git diff --check
git status --short
```

Expected: no whitespace errors; status contains only intentional files before final commit, then clean after commit.
