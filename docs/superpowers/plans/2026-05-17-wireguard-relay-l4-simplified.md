# WireGuard Relay And L4 Simplified Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the simplified WireGuard model where ordinary users create clients, Relay listeners, and L4 rules while the control plane automatically manages profiles, peers, endpoints, mixed Relay transports, UDP support, sharing links, and reserved bytes.

**Architecture:** Keep the runtime centered on WireGuard profiles, but move profile/peer wiring behind backend services. Relay and L4 services request default profiles and generated system peers; snapshots expose effective profile runtime data to agents. Frontend forms become entry/exit workflows instead of raw WireGuard plumbing.

**Tech Stack:** Go control plane with GORM/SQLite storage, Go agent userspace WireGuard netstack, Vue 3/Vite frontend, Docker-based E2E tests.

---

## File Map

- `panel/backend-go/internal/controlplane/storage/sqlite_models.go`: add reserved/system-peer metadata fields if missing.
- `panel/backend-go/internal/controlplane/storage/schema.go`: migrate new storage columns.
- `panel/backend-go/internal/controlplane/storage/sqlite_store.go`: assemble effective WireGuard profiles for snapshots, including generated system peers.
- `panel/backend-go/internal/controlplane/storage/snapshot_types.go`: carry reserved bytes in snapshot peer model.
- `panel/backend-go/internal/controlplane/service/wireguard.go`: default profile creation/reuse, profile defaults, profile sharing helpers, generated peer helpers.
- `panel/backend-go/internal/controlplane/service/wireguard_clients.go`: full-tunnel client defaults, `.conf`, QR source text, and `wireguard://` sharing URI.
- `panel/backend-go/internal/controlplane/service/wireguard_uri.go`: stop rejecting reserved, preserve reserved in profile input.
- `panel/backend-go/internal/controlplane/service/relay.go`: one-click WireGuard listener creation using default profiles.
- `panel/backend-go/internal/controlplane/service/relay_chain.go`: remove cross-agent WireGuard listener rejection.
- `panel/backend-go/internal/controlplane/service/l4.go`: default WireGuard entry profile, transparent UDP normalization, URI egress profile preservation.
- `panel/backend-go/internal/controlplane/http/handlers_wireguard.go`: expose client URI and QR-source config helpers.
- `go-agent/internal/model/wireguard.go`: add peer `reserved`.
- `go-agent/internal/wireguard/config.go`: validate reserved and fail activation clearly when unsupported.
- `go-agent/internal/wireguard/runtime.go`: apply reserved or fail clearly when unsupported; add transparent UDP runtime hooks.
- `go-agent/internal/l4/server.go`: route transparent UDP flows.
- `go-agent/internal/app/l4_runtime.go`: install transparent TCP and UDP handlers for WireGuard profiles.
- `go-agent/internal/relay/*`: verify mixed TLS/QUIC/WireGuard relay chains keep working.
- `panel/frontend/src/pages/WireGuardProfilesPage.vue`: make client sharing the ordinary workflow.
- `panel/frontend/src/components/RelayListenerForm.vue`: simplify ordinary Relay form; hide raw trust/profile fields behind advanced.
- `panel/frontend/src/components/L4RuleForm.vue`: split entry/exit choices, default WG to transparent, ordinary WG egress URI only.
- `panel/frontend/src/api/runtime.js`: canonical payload updates.
- `panel/frontend/src/api/runtimeCanonicalPayloads.test.mjs`: frontend contract tests.

---

## Phase 1: Reserved Bytes And Sharing Outputs

### Task 1: Preserve Reserved Bytes In Models And URI Imports

**Files:**
- Modify: `panel/backend-go/internal/controlplane/service/wireguard_uri.go`
- Modify: `panel/backend-go/internal/controlplane/service/wireguard_uri_test.go`
- Modify: `panel/backend-go/internal/controlplane/service/wireguard.go`
- Modify: `panel/backend-go/internal/controlplane/storage/snapshot_types.go`
- Modify: `go-agent/internal/model/wireguard.go`
- Modify: `go-agent/internal/wireguard/config.go`
- Modify: `go-agent/internal/wireguard/config_test.go`

- [ ] **Step 1: Write failing backend URI import test**

Add this test to `panel/backend-go/internal/controlplane/service/wireguard_uri_test.go`:

```go
func TestWireGuardProfileInputFromURIAllowsReserved(t *testing.T) {
	raw := "wireguard://" + testWireGuardPrivateKey + "@edge.example.com:51820?publickey=" + testWireGuardPublicKey + "&psk=" + testWireGuardPresharedKey + "&address=10.44.0.2/32&reserved=1,2,3#Edge"
	parsed, err := ParseWireGuardURI(raw)
	if err != nil {
		t.Fatalf("ParseWireGuardURI() error = %v", err)
	}
	input, err := WireGuardProfileInputFromURI(parsed, "")
	if err != nil {
		t.Fatalf("WireGuardProfileInputFromURI() error = %v", err)
	}
	if len(input.Peers) != 1 {
		t.Fatalf("Peers = %+v, want one peer", input.Peers)
	}
	if got := input.Peers[0].Reserved; len(got) != 3 || got[0] != 1 || got[1] != 2 || got[2] != 3 {
		t.Fatalf("Reserved = %+v, want [1 2 3]", got)
	}
}
```

Expected compile failure: `WireGuardPeer.Reserved undefined`.

- [ ] **Step 2: Run failing test**

Run:

```powershell
cd panel/backend-go
go test ./internal/controlplane/service -run TestWireGuardProfileInputFromURIAllowsReserved -count=1
```

Expected: FAIL because `Reserved` is not present on `WireGuardPeer` and import currently rejects reserved.

- [ ] **Step 3: Add reserved to service and snapshot peer structs**

In `panel/backend-go/internal/controlplane/service/wireguard.go`, change `WireGuardPeer` to include:

```go
Reserved []byte `json:"reserved,omitempty"`
```

In `panel/backend-go/internal/controlplane/storage/snapshot_types.go`, add the same field to the snapshot `WireGuardPeer` type.

In `go-agent/internal/model/wireguard.go`, add:

```go
Reserved []byte `json:"reserved,omitempty"`
```

- [ ] **Step 4: Preserve reserved in peer normalization and row conversion**

In `normalizeWireGuardPeers`, copy reserved:

```go
normalized := WireGuardPeer{
	Name:                       strings.TrimSpace(peer.Name),
	PublicKey:                  strings.TrimSpace(peer.PublicKey),
	PresharedKey:               strings.TrimSpace(peer.PresharedKey),
	Endpoint:                   strings.TrimSpace(peer.Endpoint),
	AllowedIPs:                 normalizeStringList(peer.AllowedIPs),
	PersistentKeepaliveSeconds: peer.PersistentKeepaliveSeconds,
	Reserved:                   append([]byte(nil), peer.Reserved...),
}
if len(normalized.Reserved) > 3 {
	return nil, fmt.Errorf("%w: reserved accepts at most 3 bytes", ErrInvalidArgument)
}
```

Because peers are stored as JSON, no dedicated DB column is required for peer-level reserved.

- [ ] **Step 5: Stop rejecting reserved in URI import**

In `panel/backend-go/internal/controlplane/service/wireguard_uri.go`, remove the `wireGuardURIValueHasUnsupportedReserved` rejection from `WireGuardProfileInputFromURI`, and populate:

```go
Reserved: append([]byte(nil), parsed.Reserved...),
```

on the generated peer.

- [ ] **Step 6: Add agent normalization tests**

Add to `go-agent/internal/wireguard/config_test.go`:

```go
func TestNormalizeConfigRejectsPeerReservedUntilRuntimeSupportsIt(t *testing.T) {
	profile := validWireGuardProfile()
	profile.Peers[0].Reserved = []byte{1, 2, 3}
	_, err := NormalizeConfig(profile)
	if err == nil {
		t.Fatal("NormalizeConfig() error = nil, want unsupported reserved error")
	}
	if !strings.Contains(err.Error(), "reserved is not supported by this WireGuard runtime") {
		t.Fatalf("NormalizeConfig() error = %v, want unsupported reserved message", err)
	}
}

func TestNormalizeConfigRejectsTooManyPeerReservedBytes(t *testing.T) {
	profile := validWireGuardProfile()
	profile.Peers[0].Reserved = []byte{1, 2, 3, 4}
	_, err := NormalizeConfig(profile)
	if err == nil {
		t.Fatal("NormalizeConfig() error = nil, want reserved length error")
	}
	if !strings.Contains(err.Error(), "reserved accepts at most 3 bytes") {
		t.Fatalf("NormalizeConfig() error = %v, want reserved length message", err)
	}
}
```

- [ ] **Step 7: Reject reserved clearly in agent config**

In `go-agent/internal/wireguard/config.go`, validate `Reserved` in `normalizePeers` before the `PeerConfig` is appended:

```go
reserved := append([]byte(nil), peer.Reserved...)
if len(reserved) > 3 {
	return nil, fmt.Errorf("peers[%d].reserved accepts at most 3 bytes", i)
}
if len(reserved) > 0 {
	return nil, fmt.Errorf("peers[%d].reserved is not supported by this WireGuard runtime", i)
}
```

Do not add `reserved` to `ipcConfig` in this task. The control plane preserves reserved for URI/import/export/runtime snapshots, and the agent rejects activation with the message above until the userspace WireGuard library has a supported IPC field for reserved bytes.

- [ ] **Step 8: Run focused tests**

Run:

```powershell
cd panel/backend-go
go test ./internal/controlplane/service -run 'TestParseWireGuardURI|TestWireGuardProfileInputFromURIAllowsReserved' -count=1
cd ../../go-agent
go test ./internal/wireguard -run 'TestNormalizeConfigRejectsPeerReservedUntilRuntimeSupportsIt|TestNormalizeConfigRejectsTooManyPeerReservedBytes|TestNormalizeConfig' -count=1
```

Expected: PASS.

- [ ] **Step 9: Commit**

```powershell
git add panel/backend-go/internal/controlplane/service/wireguard.go panel/backend-go/internal/controlplane/service/wireguard_uri.go panel/backend-go/internal/controlplane/service/wireguard_uri_test.go panel/backend-go/internal/controlplane/storage/snapshot_types.go go-agent/internal/model/wireguard.go go-agent/internal/wireguard/config.go go-agent/internal/wireguard/config_test.go
git commit -m "feat(wireguard): preserve reserved peer bytes"
```

### Task 2: Add Client Sharing URI And QR Source Endpoint

**Files:**
- Modify: `panel/backend-go/internal/controlplane/service/wireguard_clients.go`
- Modify: `panel/backend-go/internal/controlplane/service/wireguard_clients_test.go`
- Modify: `panel/backend-go/internal/controlplane/http/handlers_wireguard.go`
- Modify: `panel/backend-go/internal/controlplane/http/handlers_wireguard_test.go`
- Modify: `panel/frontend/src/api/runtime.js`
- Modify: `panel/frontend/src/pages/WireGuardProfilesPage.vue`
- Modify: `panel/frontend/src/api/runtimeCanonicalPayloads.test.mjs`

- [ ] **Step 1: Add service tests for full-tunnel defaults and URI**

Add to `wireguard_clients_test.go`:

```go
func TestWireGuardClientConfigDefaultsAllowedIPsToFullTunnel(t *testing.T) {
	_, svc, agentID, profileID := newTestWireGuardClientServiceWithProfile(t)
	client, err := svc.CreateClient(t.Context(), agentID, profileID, WireGuardClientInput{Name: "phone"})
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}
	config, err := svc.ClientConfig(t.Context(), agentID, profileID, client.ID)
	if err != nil {
		t.Fatalf("ClientConfig() error = %v", err)
	}
	if !strings.Contains(config, "AllowedIPs = 0.0.0.0/0, ::/0") {
		t.Fatalf("config missing full tunnel AllowedIPs:\n%s", config)
	}
}

func TestWireGuardClientURIIncludesReservedWhenPresent(t *testing.T) {
	_, svc, agentID, profileID := newTestWireGuardClientServiceWithProfile(t)
	client, err := svc.CreateClient(t.Context(), agentID, profileID, WireGuardClientInput{Name: "phone"})
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}
	uri, err := svc.ClientURI(t.Context(), agentID, profileID, client.ID, []byte{1, 2, 3})
	if err != nil {
		t.Fatalf("ClientURI() error = %v", err)
	}
	for _, want := range []string{"wireguard://", "publickey=", "address=", "allowedips=0.0.0.0%2F0%2C%3A%3A%2F0", "reserved=1%2C2%2C3"} {
		if !strings.Contains(uri, want) {
			t.Fatalf("uri %q missing %q", uri, want)
		}
	}
}
```

- [ ] **Step 2: Run failing service tests**

Run:

```powershell
cd panel/backend-go
go test ./internal/controlplane/service -run 'TestWireGuardClient(ConfigDefaultsAllowedIPsToFullTunnel|URIIncludesReserved)' -count=1
```

Expected: FAIL because `ClientURI` does not exist and `CreateClient` currently defaults allowed IPs to profile addresses.

- [ ] **Step 3: Change client default AllowedIPs**

In `CreateClient`, replace the default allowed IPs branch with:

```go
allowedIPs := normalizeStringList(input.AllowedIPs)
if input.AllowedIPs == nil || len(allowedIPs) == 0 {
	allowedIPs = []string{"0.0.0.0/0", "::/0"}
}
```

Keep explicit empty lists invalid if `validateWireGuardPrefixes` rejects them; otherwise add:

```go
if len(allowedIPs) == 0 {
	return state, fmt.Errorf("%w: allowed_ips is required", ErrInvalidArgument)
}
```

- [ ] **Step 4: Implement ClientURI**

Add to `wireguard_clients.go`:

```go
func (s *wireGuardClientService) ClientURI(ctx context.Context, agentID string, profileID int, clientID int, reserved []byte) (string, error) {
	resolvedID, err := s.profileService.ensureAgentExists(ctx, agentID)
	if err != nil {
		return "", err
	}
	_, profile, _, err := s.loadProfile(ctx, resolvedID, profileID)
	if err != nil {
		return "", err
	}
	clients, err := s.store.ListWireGuardClients(ctx, resolvedID, profileID)
	if err != nil {
		return "", err
	}
	var client storage.WireGuardClientRow
	found := false
	for _, row := range clients {
		if row.ID == clientID {
			client = row
			found = true
			break
		}
	}
	if !found {
		return "", ErrWireGuardClientNotFound
	}
	endpoint := strings.TrimSpace(profile.PublicEndpoint)
	if endpoint == "" {
		return "", fmt.Errorf("%w: wireguard profile public endpoint is required", ErrInvalidArgument)
	}
	serverPublicKey, err := wireGuardPublicKeyFromPrivateKey(profile.PrivateKey)
	if err != nil {
		return "", fmt.Errorf("%w: profile private_key must be a WireGuard key", ErrInvalidArgument)
	}
	if len(reserved) > 3 {
		return "", fmt.Errorf("%w: reserved accepts at most 3 bytes", ErrInvalidArgument)
	}
	u := url.URL{Scheme: "wireguard", Host: endpoint, User: url.User(client.PrivateKey), Fragment: client.Name}
	q := u.Query()
	q.Set("publickey", serverPublicKey)
	if strings.TrimSpace(client.PresharedKey) != "" {
		q.Set("psk", client.PresharedKey)
	}
	q.Set("address", client.Address)
	q.Set("allowedips", strings.Join(parseStringArray(client.AllowedIPsJSON), ","))
	if dns := parseStringArray(client.DNSJSON); len(dns) > 0 {
		q.Set("dns", strings.Join(dns, ","))
	}
	if profile.MTU > 0 {
		q.Set("mtu", strconv.Itoa(profile.MTU))
	}
	if len(reserved) > 0 {
		parts := make([]string, 0, len(reserved))
		for _, b := range reserved {
			parts = append(parts, strconv.Itoa(int(b)))
		}
		q.Set("reserved", strings.Join(parts, ","))
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}
```

Add imports `net/url` and `strconv` if missing.

- [ ] **Step 5: Add HTTP endpoint tests**

Add to `handlers_wireguard_test.go`:

```go
func TestWireGuardClientURIEndpointReturnsText(t *testing.T) {
	router, cleanup := newWireGuardHTTPTestRouter(t)
	defer cleanup()
	profile := createWireGuardHTTPClientProfile(t, router, "/panel-api", 51820)
	basePath := "/panel-api/agents/local/wireguard-profiles/" + strconv.Itoa(profile.ID) + "/clients"
	createReq := httptest.NewRequest(http.MethodPost, basePath, bytes.NewBufferString(`{"name":"phone"}`))
	createReq.Header.Set("X-Panel-Token", "secret")
	createReq.Header.Set("Content-Type", "application/json")
	createResp := httptest.NewRecorder()
	router.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("POST client = %d, body=%s", createResp.Code, createResp.Body.String())
	}
	client := decodeWireGuardHTTPClientResponse(t, createResp.Body.Bytes())

	uriReq := httptest.NewRequest(http.MethodGet, basePath+"/"+strconv.Itoa(client.ID)+"/uri?reserved=1,2,3", nil)
	uriReq.Header.Set("X-Panel-Token", "secret")
	uriResp := httptest.NewRecorder()
	router.ServeHTTP(uriResp, uriReq)
	if uriResp.Code != http.StatusOK {
		t.Fatalf("GET client uri = %d, body=%s", uriResp.Code, uriResp.Body.String())
	}
	if got := uriResp.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/plain") {
		t.Fatalf("Content-Type = %q, want text/plain", got)
	}
	body := strings.TrimSpace(uriResp.Body.String())
	for _, want := range []string{"wireguard://", "publickey=", "address=", "allowedips=0.0.0.0%2F0%2C%3A%3A%2F0", "reserved=1%2C2%2C3"} {
		if !strings.Contains(body, want) {
			t.Fatalf("uri %q missing %q", body, want)
		}
	}
}
```

- [ ] **Step 6: Wire HTTP route**

In `router.go`, add:

```go
mux.Handle(prefix+"/agents/{agentID}/wireguard-profiles/{profileID}/clients/{clientID}/uri", resolved.requirePanelToken(http.HandlerFunc(resolved.handleWireGuardProfileClientURI)))
```

In `handlers_wireguard.go`, add:

```go
func (d Dependencies) handleWireGuardProfileClientURI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	agentID := strings.TrimSpace(r.PathValue("agentID"))
	profileID, err := parsePositivePathInt(r.PathValue("profileID"), "profile_id")
	if err != nil {
		writeError(w, err)
		return
	}
	clientID, err := parsePositivePathInt(r.PathValue("clientID"), "client_id")
	if err != nil {
		writeError(w, err)
		return
	}
	reserved, err := parseWireGuardClientURIReserved(r.URL.Query().Get("reserved"))
	if err != nil {
		writeError(w, err)
		return
	}
	uriText, err := d.WireGuardClientService.ClientURI(r.Context(), agentID, profileID, clientID, reserved)
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(uriText))
}

func parseWireGuardClientURIReserved(raw string) ([]byte, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	if len(parts) > 3 {
		return nil, fmt.Errorf("%w: reserved accepts at most 3 bytes", service.ErrInvalidArgument)
	}
	reserved := make([]byte, 0, len(parts))
	for _, part := range parts {
		value, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || value < 0 || value > 255 {
			return nil, fmt.Errorf("%w: reserved bytes must be between 0 and 255", service.ErrInvalidArgument)
		}
		reserved = append(reserved, byte(value))
	}
	return reserved, nil
}
```

Ensure `handlers_wireguard.go` imports `fmt` and `strconv`.

- [ ] **Step 7: Frontend sharing controls**

In `runtime.js`, add:

```js
export async function fetchWireGuardClientURI(agentId, profileId, clientId, reserved = '') {
  const suffix = reserved ? `?reserved=${encodeURIComponent(reserved)}` : ''
  const response = await api.get(`/agents/${encodeURIComponent(agentId)}/wireguard-profiles/${encodeURIComponent(profileId)}/clients/${encodeURIComponent(clientId)}/uri${suffix}`, { responseType: 'text' })
  return response.data
}
```

In `WireGuardProfilesPage.vue`, add a "复制 URI" action next to "下载配置". Do not add mihomo YAML controls.

- [ ] **Step 8: Run tests**

Run:

```powershell
cd panel/backend-go
go test ./internal/controlplane/service -run 'TestWireGuardClient(ConfigDefaultsAllowedIPsToFullTunnel|URIIncludesReserved)' -count=1
go test ./internal/controlplane/http -run TestWireGuardClientURIEndpointReturnsText -count=1
cd ../frontend
npm run test -- runtimeCanonicalPayloads
```

Expected: PASS.

- [ ] **Step 9: Commit**

```powershell
git add panel/backend-go/internal/controlplane/service/wireguard_clients.go panel/backend-go/internal/controlplane/service/wireguard_clients_test.go panel/backend-go/internal/controlplane/http/router.go panel/backend-go/internal/controlplane/http/handlers_wireguard.go panel/backend-go/internal/controlplane/http/handlers_wireguard_test.go panel/frontend/src/api/runtime.js panel/frontend/src/pages/WireGuardProfilesPage.vue panel/frontend/src/api/runtimeCanonicalPayloads.test.mjs
git commit -m "feat(wireguard): add client sharing links"
```

---

## Phase 2: Default Profiles And WireGuard Relay

### Task 3: Add Default WireGuard Profile Creation Service

**Files:**
- Modify: `panel/backend-go/internal/controlplane/service/wireguard.go`
- Modify: `panel/backend-go/internal/controlplane/service/wireguard_test.go`

- [ ] **Step 1: Write default profile tests**

Add to `wireguard_test.go`:

```go
func TestWireGuardProfileServiceEnsureDefaultCreatesProfile(t *testing.T) {
	store, svc := newTestWireGuardProfileService(t)
	agentID := "local"
	profile, err := svc.EnsureDefault(t.Context(), agentID)
	if err != nil {
		t.Fatalf("EnsureDefault() error = %v", err)
	}
	if profile.ID <= 0 || profile.Name != "Default WireGuard" || profile.ListenPort != 51820 || len(profile.Addresses) != 1 || profile.Enabled != true {
		t.Fatalf("profile = %+v", profile)
	}
	rows, err := store.ListWireGuardProfiles(t.Context(), agentID)
	if err != nil {
		t.Fatalf("ListWireGuardProfiles() error = %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want one default profile", rows)
	}
}

func TestWireGuardProfileServiceEnsureDefaultReusesExistingDefault(t *testing.T) {
	store, svc := newTestWireGuardProfileService(t)
	first, err := svc.EnsureDefault(t.Context(), "local")
	if err != nil {
		t.Fatalf("EnsureDefault(first) error = %v", err)
	}
	second, err := svc.EnsureDefault(t.Context(), "local")
	if err != nil {
		t.Fatalf("EnsureDefault(second) error = %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("default profile IDs = %d and %d, want reuse", first.ID, second.ID)
	}
	rows, _ := store.ListWireGuardProfiles(t.Context(), "local")
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want one reused default profile", rows)
	}
}
```

- [ ] **Step 2: Run failing tests**

Run:

```powershell
cd panel/backend-go
go test ./internal/controlplane/service -run TestWireGuardProfileServiceEnsureDefault -count=1
```

Expected: FAIL because `EnsureDefault` does not exist.

- [ ] **Step 3: Implement EnsureDefault**

Add public service method:

```go
func (s *wireGuardProfileService) EnsureDefault(ctx context.Context, agentID string) (WireGuardProfile, error) {
	resolvedID, err := s.ensureAgentExists(ctx, agentID)
	if err != nil {
		return WireGuardProfile{}, err
	}
	if err := ensureAgentSupportsWireGuardCapability(ctx, s.cfg, s.store, resolvedID); err != nil {
		return WireGuardProfile{}, err
	}
	rows, err := s.store.ListWireGuardProfiles(ctx, resolvedID)
	if err != nil {
		return WireGuardProfile{}, err
	}
	for _, row := range rows {
		profile := wireGuardProfileFromRow(row)
		if profile.Enabled && hasTag(profile.Tags, "system:default-wireguard") {
			return redactWireGuardProfile(profile), nil
		}
	}
	privateKey, _, err := generateWireGuardKeyPair()
	if err != nil {
		return WireGuardProfile{}, err
	}
	listenPort := nextAvailableWireGuardListenPort(rows, 51820)
	input := WireGuardProfileInput{
		Name:       "Default WireGuard",
		Mode:       "generic_wireguard",
		PrivateKey: privateKey,
		ListenPort: listenPort,
		Addresses:  []string{allocateWireGuardProfileAddress(rows)},
		MTU:        1280,
		Enabled:    boolPtr(true),
		Tags:       []string{"system:default-wireguard"},
	}
	return s.Create(ctx, resolvedID, input)
}
```

Add helper:

```go
func nextAvailableWireGuardListenPort(rows []storage.WireGuardProfileRow, start int) int {
	used := map[int]struct{}{}
	for _, row := range rows {
		if row.Enabled && row.ListenPort > 0 {
			used[row.ListenPort] = struct{}{}
		}
	}
	for port := start; port <= 65535; port++ {
		if _, ok := used[port]; !ok {
			return port
		}
	}
	return 0
}
```

Add this helper near the existing WireGuard profile helpers, or reuse the same function name if it already exists in `wireguard.go`:

```go
func hasTag(tags []string, target string) bool {
	for _, tag := range tags {
		if strings.EqualFold(strings.TrimSpace(tag), target) {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run tests**

Run:

```powershell
cd panel/backend-go
go test ./internal/controlplane/service -run TestWireGuardProfileServiceEnsureDefault -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add panel/backend-go/internal/controlplane/service/wireguard.go panel/backend-go/internal/controlplane/service/wireguard_test.go
git commit -m "feat(wireguard): create default profiles on demand"
```

### Task 4: Make WireGuard Relay Listener One-Click

**Files:**
- Modify: `panel/backend-go/internal/controlplane/service/relay.go`
- Modify: `panel/backend-go/internal/controlplane/service/relay_test.go`
- Modify: `panel/backend-go/internal/controlplane/service/relay_chain.go`
- Modify: `panel/backend-go/internal/controlplane/service/l4_test.go`
- Modify: `panel/backend-go/internal/controlplane/service/rules_test.go`

- [ ] **Step 1: Write Relay service test**

Add to `relay_test.go`:

```go
func TestRelayListenerCreateWireGuardUsesDefaultProfile(t *testing.T) {
	store, svc := newTestRelayListenerService(t)
	agentID := "local"
	listener, err := svc.Create(t.Context(), agentID, RelayListenerInput{
		Name:          stringPtrRelay("wg-relay"),
		TransportMode: stringPtrRelay("wireguard"),
		ListenPort:    intPtrRelay(19001),
		Enabled:       boolPtrRelay(true),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if listener.WireGuardProfileID == nil || *listener.WireGuardProfileID <= 0 {
		t.Fatalf("WireGuardProfileID = %v, want default profile", listener.WireGuardProfileID)
	}
	if listener.ListenHost != "10.8.0.1" && listener.ListenHost == "" {
		t.Fatalf("ListenHost = %q, want generated WG inner host", listener.ListenHost)
	}
	rows, err := store.ListWireGuardProfiles(t.Context(), agentID)
	if err != nil {
		t.Fatalf("ListWireGuardProfiles() error = %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("profiles = %+v, want generated default profile", rows)
	}
}
```

Adjust helper names to match existing `relay_test.go` pointer helpers.

- [ ] **Step 2: Run failing test**

Run:

```powershell
cd panel/backend-go
go test ./internal/controlplane/service -run TestRelayListenerCreateWireGuardUsesDefaultProfile -count=1
```

Expected: FAIL because Relay service currently requires explicit `wireguard_profile_id` and certificate material.

- [ ] **Step 3: Inject default profile service into Relay creation**

In `relay.go`, inside `prepareRelayListener` or before normalization, if `TransportMode == "wireguard"` and `WireGuardProfileID == nil`:

```go
profileSvc := NewWireGuardProfileService(s.cfg, s.store)
profile, err := profileSvc.EnsureDefault(ctx, agentID)
if err != nil {
	return relayPreparation{}, err
}
id := profile.ID
input.WireGuardProfileID = &id
if input.ListenHost == nil || strings.TrimSpace(*input.ListenHost) == "" {
	host := firstWireGuardAddressHost(profile.Addresses)
	input.ListenHost = &host
}
```

Add:

```go
func firstWireGuardAddressHost(addresses []string) string {
	for _, raw := range addresses {
		if prefix, err := netip.ParsePrefix(strings.TrimSpace(raw)); err == nil {
			return prefix.Addr().String()
		}
	}
	return "0.0.0.0"
}
```

WireGuard transport must not require TLS certificate fields at service validation time. Keep certificate fields for compatibility when rows already contain materialized certificate references, and skip certificate requirement for `transport_mode=wireguard`.

- [ ] **Step 4: Remove cross-agent WireGuard listener rejection**

In `relay_chain.go`, remove this block:

```go
if isWireGuardRelayTransport(listener.TransportMode) && strings.TrimSpace(opts.RuleAgentID) != "" && strings.TrimSpace(listener.AgentID) != strings.TrimSpace(opts.RuleAgentID) {
	return fmt.Errorf(...)
}
```

Replace tests that expected rejection:

- `TestL4RuleServiceCreateRejectsCrossAgentWireGuardRelayListener`
- `TestRuleServiceCreateRejectsCrossAgentWireGuardRelayListener`

with tests named:

- `TestL4RuleServiceCreateAllowsCrossAgentWireGuardRelayListener`
- `TestRuleServiceCreateAllowsCrossAgentWireGuardRelayListener`

and assert create succeeds.

- [ ] **Step 5: Run focused tests**

Run:

```powershell
cd panel/backend-go
go test ./internal/controlplane/service -run 'TestRelayListenerCreateWireGuardUsesDefaultProfile|CrossAgentWireGuardRelayListener' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```powershell
git add panel/backend-go/internal/controlplane/service/relay.go panel/backend-go/internal/controlplane/service/relay_test.go panel/backend-go/internal/controlplane/service/relay_chain.go panel/backend-go/internal/controlplane/service/l4_test.go panel/backend-go/internal/controlplane/service/rules_test.go
git commit -m "feat(relay): auto-wire WireGuard listeners"
```

### Task 5: Generate Cross-Agent System Peers For WireGuard Relay References

**Files:**
- Modify: `panel/backend-go/internal/controlplane/storage/sqlite_store.go`
- Modify: `panel/backend-go/internal/controlplane/storage/sqlite_store_test.go`
- Modify: `panel/backend-go/internal/controlplane/service/wireguard.go`
- Modify: `panel/backend-go/internal/controlplane/service/wireguard_test.go`

- [ ] **Step 1: Write snapshot graph test**

Add to `sqlite_store_test.go`:

```go
func TestStoreLoadAgentSnapshotIncludesSystemPeersForCrossAgentWireGuardRelay(t *testing.T) {
	store := newTestSQLiteStore(t)
	ctx := t.Context()
	ruleAgentID := "edge-a"
	relayAgentID := "relay-b"
	if err := store.SaveAgent(ctx, storage.AgentRow{ID: ruleAgentID, Name: "edge-a", CapabilitiesJSON: `["l4","wireguard"]`}); err != nil {
		t.Fatalf("SaveAgent(rule) error = %v", err)
	}
	if err := store.SaveAgent(ctx, storage.AgentRow{ID: relayAgentID, Name: "relay-b", CapabilitiesJSON: `["l4","wireguard"]`}); err != nil {
		t.Fatalf("SaveAgent(relay) error = %v", err)
	}
	ruleProfileID := 10
	relayProfileID := 20
	if err := store.SaveWireGuardProfiles(ctx, ruleAgentID, []storage.WireGuardProfileRow{{
		ID: ruleProfileID, AgentID: ruleAgentID, Name: "Default WireGuard", Mode: "generic_wireguard",
		PrivateKey: testWireGuardPrivateKey, ListenPort: 51820, AddressesJSON: `["10.80.0.1/24"]`, Enabled: true,
	}}); err != nil {
		t.Fatalf("SaveWireGuardProfiles(rule) error = %v", err)
	}
	if err := store.SaveWireGuardProfiles(ctx, relayAgentID, []storage.WireGuardProfileRow{{
		ID: relayProfileID, AgentID: relayAgentID, Name: "Default WireGuard", Mode: "generic_wireguard",
		PrivateKey: testWireGuardPrivateKey2, ListenPort: 51821, PublicEndpoint: "relay-b.example.test:51821", AddressesJSON: `["10.81.0.1/24"]`, Enabled: true,
	}}); err != nil {
		t.Fatalf("SaveWireGuardProfiles(relay) error = %v", err)
	}
	if err := store.SaveRelayListeners(ctx, relayAgentID, []storage.RelayListenerRow{{
		ID: 7, AgentID: relayAgentID, Name: "wg-relay", ListenHost: "10.81.0.1", ListenPort: 19001,
		PublicHost: "10.81.0.1", PublicPort: 19001, Enabled: true, TransportMode: "wireguard", WireGuardProfileID: &relayProfileID,
	}}); err != nil {
		t.Fatalf("SaveRelayListeners() error = %v", err)
	}
	if err := store.SaveL4Rules(ctx, ruleAgentID, []storage.L4RuleRow{{
		ID: 1, AgentID: ruleAgentID, Name: "through wg relay", Protocol: "tcp", ListenHost: "0.0.0.0", ListenPort: 18080,
		BackendsJSON: `[{"host":"backend","port":80}]`, RelayLayersJSON: `[[7]]`, Enabled: true,
	}}); err != nil {
		t.Fatalf("SaveL4Rules() error = %v", err)
	}
	snapshot, err := store.LoadAgentSnapshot(ctx, ruleAgentID)
	if err != nil {
		t.Fatalf("LoadAgentSnapshot(rule) error = %v", err)
	}
	if len(snapshot.WireGuardProfiles) == 0 {
		t.Fatalf("WireGuardProfiles = empty, want dialing profile with generated relay peer")
	}
	foundPeer := false
	for _, profile := range snapshot.WireGuardProfiles {
		for _, peer := range profile.Peers {
			if peer.Endpoint == "relay-b.example.test:51821" {
				foundPeer = true
			}
		}
	}
	if !foundPeer {
		t.Fatalf("snapshot profiles = %+v, want generated peer to relay endpoint", snapshot.WireGuardProfiles)
	}
}
```

Add these local constants near the storage snapshot tests when equivalent constants are not already visible in `sqlite_store_test.go`:

```go
const (
	testWireGuardPrivateKey  = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	testWireGuardPrivateKey2 = "AQAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	testWireGuardPublicKey   = "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB="
)
```

- [ ] **Step 2: Run failing test**

Run:

```powershell
cd panel/backend-go
go test ./internal/controlplane/storage -run TestStoreLoadAgentSnapshotIncludesSystemPeersForCrossAgentWireGuardRelay -count=1
```

Expected: FAIL because generated system peers do not exist.

- [ ] **Step 3: Add generated system peer assembly**

In `sqlite_store.go`, before `SnapshotWireGuardProfiles(wireGuardRows)`, add a step:

```go
wireGuardRows = appendGeneratedWireGuardSystemPeers(resolvedAgentID, wireGuardRows, relayRows, l4Rows, httpRows)
```

Implement a helper that:

- finds WireGuard relay listeners referenced by L4/HTTP relay layers.
- for remote WireGuard relay listeners, identifies the local default profile.
- adds a peer to the local profile pointing at the remote profile public endpoint.
- uses allowed IPs covering the remote profile server address, for example `10.81.0.1/32`.
- preserves existing peers and avoids duplicate public keys.

Do not persist generated peers to DB in this task. Generate them during snapshot assembly.

- [ ] **Step 4: Include remote listener owner profiles safely**

Modify snapshot graph loading so the rule agent snapshot has enough redacted/non-secret listener metadata but only includes private key material for profiles owned by the snapshot agent. The dialing agent needs its own private key and remote peer public key/endpoint, not the remote private key.

The relay listener owner snapshot must include its own profile private key and generated peer back to the dialing agent.

- [ ] **Step 5: Run storage tests**

Run:

```powershell
cd panel/backend-go
go test ./internal/controlplane/storage -run 'WireGuard|Snapshot' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```powershell
git add panel/backend-go/internal/controlplane/storage/sqlite_store.go panel/backend-go/internal/controlplane/storage/sqlite_store_test.go panel/backend-go/internal/controlplane/service/wireguard.go panel/backend-go/internal/controlplane/service/wireguard_test.go
git commit -m "feat(storage): generate wireguard relay peers"
```

---

## Phase 3: L4 Transparent UDP And WireGuard URI Egress

### Task 6: Backend L4 Defaults For WireGuard Transparent TCP/UDP

**Files:**
- Modify: `panel/backend-go/internal/controlplane/service/l4.go`
- Modify: `panel/backend-go/internal/controlplane/service/l4_test.go`

- [ ] **Step 1: Write backend tests**

Add to `l4_test.go`:

```go
func TestL4RuleServiceWireGuardDefaultsToTransparentForTCPAndUDP(t *testing.T) {
	for _, protocol := range []string{"tcp", "udp"} {
		t.Run(protocol, func(t *testing.T) {
			store := newFakeL4StoreWithWireGuardProfile("local", 7, `["10.8.0.1/24"]`)
			svc := NewL4RuleService(config.Config{LocalAgentID: "local"}, store)
			rule, err := svc.Create(t.Context(), "local", L4RuleInput{
				Protocol:           stringPtrL4(protocol),
				ListenMode:         stringPtrL4("wireguard"),
				WireGuardProfileID: intPtrL4(7),
				ListenHost:         stringPtrL4("0.0.0.0"),
				ListenPort:         intPtrL4(443),
				Backends:           &[]L4Backend{{Host: "backend", Port: 8443}},
			})
			if err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			if rule.WireGuardInboundMode != "transparent" {
				t.Fatalf("WireGuardInboundMode = %q, want transparent", rule.WireGuardInboundMode)
			}
			if rule.WireGuardListenHost != "" {
				t.Fatalf("WireGuardListenHost = %q, want empty for transparent", rule.WireGuardListenHost)
			}
		})
	}
}
```

- [ ] **Step 2: Run failing tests**

Run:

```powershell
cd panel/backend-go
go test ./internal/controlplane/service -run TestL4RuleServiceWireGuardDefaultsToTransparentForTCPAndUDP -count=1
```

Expected: FAIL because current default is address and UDP transparent is rejected.

- [ ] **Step 3: Normalize WireGuard transparent default**

In `normalizeL4RuleInput`, change WireGuard default inbound mode:

```go
if wireGuardInboundMode == "" {
	wireGuardInboundMode = "transparent"
}
```

Remove the validation that rejects transparent UDP. Keep address mode valid for both TCP and UDP if runtime supports it; if address UDP is not supported, reject with a clear message only for address UDP.

- [ ] **Step 4: Auto default profile when omitted**

In `Create` and `Update`, before `validateWireGuardProfileReference`, if `listen_mode=wireguard` and `wireguard_profile_id` is nil:

```go
profileSvc := NewWireGuardProfileService(s.cfg, s.store)
profile, err := profileSvc.EnsureDefault(ctx, resolvedID)
if err != nil {
	return L4Rule{}, err
}
rule.WireGuardProfileID = &profile.ID
```

- [ ] **Step 5: Run focused tests**

Run:

```powershell
cd panel/backend-go
go test ./internal/controlplane/service -run 'TestL4RuleServiceWireGuardDefaultsToTransparentForTCPAndUDP|WireGuard' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```powershell
git add panel/backend-go/internal/controlplane/service/l4.go panel/backend-go/internal/controlplane/service/l4_test.go
git commit -m "feat(l4): default wireguard entry to transparent"
```

### Task 7: Agent Runtime Transparent UDP

**Files:**
- Modify: `go-agent/internal/wireguard/runtime.go`
- Modify: `go-agent/internal/app/l4_runtime.go`
- Modify: `go-agent/internal/l4/server.go`
- Modify: `go-agent/internal/l4/udp.go`
- Modify: `go-agent/internal/app/local_runtime_test.go`
- Modify: `go-agent/internal/l4/server_test.go`

- [ ] **Step 1: Add runtime interface tests**

Add to `go-agent/internal/app/local_runtime_test.go`:

```go
func TestL4RuntimeManagerInstallsWireGuardTransparentUDPHandler(t *testing.T) {
	profileID := 41
	runtime := &testAppWireGuardRuntime{}
	manager := newL4RuntimeManagerWithWireGuardFactory(func(context.Context, wireguard.Config) (wireguard.Runtime, error) {
		return runtime, nil
	})
	defer manager.Close()
	err := manager.ApplyWithRelayAndWireGuardProfiles(context.Background(), []model.L4Rule{{
		ID: 1, Protocol: "udp", ListenMode: "wireguard", WireGuardInboundMode: "transparent",
		WireGuardProfileID: &profileID, ListenHost: "0.0.0.0", ListenPort: 53,
		Backends: []model.L4Backend{{Host: "127.0.0.1", Port: 5353}},
	}}, nil, []model.WireGuardProfile{validAppWireGuardProfile(profileID)})
	if err != nil {
		t.Fatalf("ApplyWithRelayAndWireGuardProfiles() error = %v", err)
	}
	if runtime.udpFlowHandler == nil {
		t.Fatal("udpFlowHandler = nil, want installed handler")
	}
}
```

- [ ] **Step 2: Run failing test**

Run:

```powershell
cd go-agent
go test ./internal/app -run TestL4RuntimeManagerInstallsWireGuardTransparentUDPHandler -count=1
```

Expected: FAIL because transparent UDP handler does not exist.

- [ ] **Step 3: Define UDP transparent runtime interface**

In `go-agent/internal/wireguard/runtime.go`, add:

```go
type UDPFlowHandler func(ctx context.Context, source string, destination string, payload []byte, respond func([]byte) error)

type TransparentUDPRuntime interface {
	Runtime
	SetUDPFlowHandler(handler UDPFlowHandler)
}
```

Leave the existing TCP transparent runtime interface unchanged.

- [ ] **Step 4: Install UDP handlers in L4 runtime**

In `l4_runtime.go`, when rules include `Protocol == "udp"` and `ListenMode == "wireguard"` and `WireGuardInboundMode == "transparent"`, resolve runtime and call:

```go
transparent.SetUDPFlowHandler(func(ctx context.Context, source string, destination string, payload []byte, respond func([]byte) error) {
	server.HandleWireGuardUDPFlow(ctx, source, destination, payload, respond)
})
```

- [ ] **Step 5: Add L4 server UDP flow entrypoint**

In `go-agent/internal/l4/server.go`, add:

```go
func (s *Server) HandleWireGuardUDPFlow(ctx context.Context, source string, destination string, payload []byte, respond func([]byte) error) {
	rule, ok := s.matchWireGuardUDPFlow(destination)
	if !ok {
		return
	}
	s.forwardUDPDatagram(ctx, rule, source, destination, payload, respond)
}
```

Implement `matchWireGuardUDPFlow` by matching destination host/port against transparent UDP rules. Extract the current UDP listener datagram forwarding body from `udp.go` into:

```go
func (s *Server) forwardUDPDatagram(ctx context.Context, rule model.L4Rule, source string, destination string, payload []byte, respond func([]byte) error) {
	// Move the existing socket UDP backend/relay selection and response write path here.
}
```

Then call `forwardUDPDatagram` from both the socket UDP listener path and `HandleWireGuardUDPFlow`.

- [ ] **Step 6: Add server behavior test**

Add to `go-agent/internal/l4/server_test.go`:

```go
func TestServerWireGuardTransparentUDPForwardsToBackend(t *testing.T) {
	backendAddr, stopBackend := startUDPBackend(t, func(payload []byte) []byte {
		if string(payload) == "ping" {
			return []byte("pong")
		}
		return []byte("bad")
	})
	defer stopBackend()
	host, portText, _ := net.SplitHostPort(backendAddr)
	port, _ := strconv.Atoi(portText)
	profileID := 7
	srv, err := NewServer(context.Background(), []model.L4Rule{{
		ID: 1, Protocol: "udp", ListenMode: "wireguard", WireGuardInboundMode: "transparent",
		WireGuardProfileID: &profileID, ListenHost: "0.0.0.0", ListenPort: 53,
		Backends: []model.L4Backend{{Host: host, Port: port}},
	}}, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	defer srv.Close()
	var response []byte
	srv.HandleWireGuardUDPFlow(context.Background(), "10.8.0.2:53000", "1.1.1.1:53", []byte("ping"), func(payload []byte) error {
		response = append([]byte(nil), payload...)
		return nil
	})
	if string(response) != "pong" {
		t.Fatalf("response = %q, want pong", response)
	}
}
```

Add this helper to `server_test.go` when no same-name helper already exists:

```go
func startUDPBackend(t *testing.T, handler func([]byte) []byte) (string, func()) {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket() error = %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 2048)
		for {
			n, addr, err := conn.ReadFrom(buf)
			if err != nil {
				return
			}
			response := handler(append([]byte(nil), buf[:n]...))
			_, _ = conn.WriteTo(response, addr)
		}
	}()
	return conn.LocalAddr().String(), func() {
		_ = conn.Close()
		<-done
	}
}
```

- [ ] **Step 7: Run agent tests**

Run:

```powershell
cd go-agent
go test ./internal/app -run TestL4RuntimeManagerInstallsWireGuardTransparentUDPHandler -count=1
go test ./internal/l4 -run TestServerWireGuardTransparentUDPForwardsToBackend -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit**

```powershell
git add go-agent/internal/wireguard/runtime.go go-agent/internal/app/l4_runtime.go go-agent/internal/l4/server.go go-agent/internal/l4/udp.go go-agent/internal/app/local_runtime_test.go go-agent/internal/l4/server_test.go
git commit -m "feat(l4): route transparent wireguard udp"
```

---

## Phase 4: Frontend Simplification

### Task 8: Simplify Relay Listener Form

**Files:**
- Modify: `panel/frontend/src/components/RelayListenerForm.vue`
- Modify: `panel/frontend/src/api/runtimeCanonicalPayloads.test.mjs`

- [ ] **Step 1: Add frontend source tests**

Add to `runtimeCanonicalPayloads.test.mjs`:

```js
it('Relay listener form hides WireGuard profile selection from ordinary flow', async () => {
  const form = await import('../components/RelayListenerForm.vue?raw')
  const source = form.default
  expect(source).toContain('高级设置')
  const ordinaryStart = source.indexOf('Relay Transport')
  const advancedStart = source.indexOf('advanced-panel')
  expect(source.slice(ordinaryStart, advancedStart)).not.toContain('WireGuard Profile')
  expect(source).toContain('自动复用或创建默认 WireGuard Profile')
})
```

- [ ] **Step 2: Run failing frontend test**

Run:

```powershell
cd panel/frontend
npm run test -- runtimeCanonicalPayloads
```

Expected: FAIL because profile selection is visible in ordinary flow.

- [ ] **Step 3: Update RelayListenerForm UI**

Move the existing `WireGuard Profile` selector into `advanced-panel`. In ordinary `transport_mode === "wireguard"` UI, show:

```vue
<p class='form-hint'>系统会自动复用或创建默认 WireGuard Profile，并使用该 Profile 的 Endpoint 作为公网 UDP 入口。</p>
```

Submission should omit `wireguard_profile_id` when no advanced override is selected:

```js
if (form.value.transport_mode === 'wireguard' && selectedWireGuardProfileID.value != null) {
  payload.wireguard_profile_id = selectedWireGuardProfileID.value
}
```

- [ ] **Step 4: Run frontend test**

Run:

```powershell
cd panel/frontend
npm run test -- runtimeCanonicalPayloads
```

Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add panel/frontend/src/components/RelayListenerForm.vue panel/frontend/src/api/runtimeCanonicalPayloads.test.mjs
git commit -m "feat(panel): simplify wireguard relay listener form"
```

### Task 9: Simplify L4 Entry/Exit Form

**Files:**
- Modify: `panel/frontend/src/components/L4RuleForm.vue`
- Modify: `panel/frontend/src/api/runtime.js`
- Modify: `panel/frontend/src/api/runtimeCanonicalPayloads.test.mjs`

- [ ] **Step 1: Add payload tests**

Add to `runtimeCanonicalPayloads.test.mjs`:

```js
it('L4 WireGuard transparent UDP payload omits ordinary profile selection', async () => {
  const runtime = await import('../api/runtime.js')
  const payload = runtime.buildL4RulePayload({
    protocol: 'udp',
    listen_mode: 'wireguard',
    wireguard_inbound_mode: 'transparent',
    listen_host: '0.0.0.0',
    listen_port: 53,
    backends: [{ host: '8.8.8.8', port: 53 }],
    relay_layers: []
  })
  expect(payload.protocol).toBe('udp')
  expect(payload.listen_mode).toBe('wireguard')
  expect(payload.wireguard_inbound_mode).toBe('transparent')
  expect(payload).not.toHaveProperty('wireguard_profile_id')
})

it('L4 ordinary WireGuard egress sends URI not profile id', async () => {
  const runtime = await import('../api/runtime.js')
  const payload = runtime.buildL4RulePayload({
    protocol: 'tcp',
    listen_mode: 'proxy',
    listen_port: 1080,
    proxy_egress_mode: 'wireguard',
    wireguard_egress_uri: 'wireguard://private@example.com:51820?publickey=server&address=10.8.0.2%2F32'
  })
  expect(payload.proxy_egress_mode).toBe('wireguard')
  expect(payload.wireguard_egress_uri).toContain('wireguard://')
  expect(payload).not.toHaveProperty('wireguard_profile_id')
})
```

- [ ] **Step 2: Run failing tests**

Run:

```powershell
cd panel/frontend
npm run test -- runtimeCanonicalPayloads
```

Expected: FAIL because current canonical payload still sends profile IDs for ordinary WG cases and disallows transparent UDP in the form.

- [ ] **Step 3: Update L4RuleForm state**

Change initial defaults:

```js
wireguard_inbound_mode: initialData?.wireguard_inbound_mode || 'transparent',
wireguard_egress_source: 'uri',
wireguard_profile_id: initialData?.wireguard_profile_id == null ? '' : Number(initialData.wireguard_profile_id),
```

Remove watchers that force UDP transparent back to address:

```js
// Delete logic equivalent to:
// if (inbound && form.value.protocol === 'udp' && form.value.wireguard_inbound_mode === 'transparent') {
//   form.value.wireguard_inbound_mode = 'address'
// }
```

- [ ] **Step 4: Update ordinary UI copy**

In the protocol tab, replace the raw `WireGuard Profile` ordinary selector with:

```vue
<div v-if="isWireGuardInbound" class="form-help">
  WireGuard 透明入口会匹配已接入默认 Profile 的客户端流量；高级配置中可改为内网地址入口或指定 Profile。
</div>
```

Keep profile selector only in advanced UI or when editing existing rules with a profile ID.

- [ ] **Step 5: Update payload builder**

In `runtime.js` and `L4RuleForm.vue`, only emit `wireguard_profile_id` when advanced override is active:

```js
if (isWireGuardAdvancedProfileOverride.value && selectedWireGuardProfileID.value != null) {
  payload.wireguard_profile_id = selectedWireGuardProfileID.value
}
```

For ordinary WireGuard egress, require URI:

```js
if (form.value.proxy_egress_mode === 'wireguard') {
  payload.wireguard_egress_uri = form.value.wireguard_egress_uri.trim()
}
```

- [ ] **Step 6: Run frontend tests**

Run:

```powershell
cd panel/frontend
npm run test -- runtimeCanonicalPayloads
npm run build
```

Expected: PASS.

- [ ] **Step 7: Commit**

```powershell
git add panel/frontend/src/components/L4RuleForm.vue panel/frontend/src/api/runtime.js panel/frontend/src/api/runtimeCanonicalPayloads.test.mjs
git commit -m "feat(panel): simplify l4 wireguard flow"
```

### Task 10: Focus WireGuard Profile Page On Clients

**Files:**
- Modify: `panel/frontend/src/pages/WireGuardProfilesPage.vue`
- Modify: `panel/frontend/src/api/runtime.js`
- Modify: `panel/frontend/src/api/runtimeCanonicalPayloads.test.mjs`
- Modify: `panel/frontend/package.json`
- Modify: `panel/frontend/package-lock.json`

- [ ] **Step 1: Add frontend source test**

Add to `runtimeCanonicalPayloads.test.mjs`:

```js
it('WireGuard Profiles page offers conf qr and uri but not mihomo yaml', async () => {
  const page = await import('../pages/WireGuardProfilesPage.vue?raw')
  const source = page.default
  expect(source).toContain('下载配置')
  expect(source).toContain('二维码')
  expect(source).toContain('复制 URI')
  expect(source).not.toMatch(/mihomo|YAML/i)
  expect(source.indexOf('高级 Legacy Peers')).toBeGreaterThan(source.indexOf('Clients'))
})
```

- [ ] **Step 2: Run failing test**

Run:

```powershell
cd panel/frontend
npm run test -- runtimeCanonicalPayloads
```

Expected: FAIL because QR and URI controls are missing.

- [ ] **Step 3: Add QR and URI actions**

In `WireGuardProfilesPage.vue` client row actions, add buttons:

```vue
<button class="btn btn--secondary btn--sm" :disabled="isClientRowPending(client)" @click="showClientQRCode(client)">二维码</button>
<button class="btn btn--secondary btn--sm" :disabled="isClientRowPending(client)" @click="copyClientURI(client)">复制 URI</button>
```

Implement `copyClientURI` with `fetchWireGuardClientURI`:

```js
async function copyClientURI(client) {
  const uri = await fetchWireGuardClientURI(selectedAgentId.value, selectedProfile.value.id, client.id)
  await navigator.clipboard.writeText(uri)
}
```

Install a local QR renderer so client private keys never leave the browser:

```powershell
cd panel/frontend
npm install qrcode
```

Add QR rendering helpers:

```js
import QRCode from 'qrcode'

const qrModal = ref({ open: false, title: '', imageUrl: '', configText: '' })

async function showClientQRCode(client) {
  const configText = await fetchWireGuardClientConfig(selectedAgentId.value, selectedProfile.value.id, client.id)
  const imageUrl = await QRCode.toDataURL(configText, { width: 260, margin: 1 })
  qrModal.value = {
    open: true,
    title: client.name || `Client ${client.id}`,
    imageUrl,
    configText
  }
}
```

Render a modal with an `<img :src="qrModal.imageUrl" alt="WireGuard client QR code">` and a readonly `<textarea>` containing `qrModal.configText` as fallback. Do not add mihomo YAML.

- [ ] **Step 4: Run frontend tests**

Run:

```powershell
cd panel/frontend
npm run test -- runtimeCanonicalPayloads
npm run build
```

Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add panel/frontend/src/pages/WireGuardProfilesPage.vue panel/frontend/src/api/runtime.js panel/frontend/src/api/runtimeCanonicalPayloads.test.mjs panel/frontend/package.json panel/frontend/package-lock.json
git commit -m "feat(panel): add wireguard client sharing actions"
```

---

## Phase 5: Verification And Docker E2E

### Task 11: Full Test Suite

**Files:**
- No source changes are planned in this task. If a command fails, fix the failing source or test in the subsystem that produced the failure and commit that focused fix before continuing.

- [ ] **Step 1: Run backend tests**

Run:

```powershell
cd panel/backend-go
go test ./... -count=1
```

Expected: PASS.

- [ ] **Step 2: Run agent tests**

Run:

```powershell
cd go-agent
go test ./... -count=1
```

Expected: PASS.

- [ ] **Step 3: Run frontend tests and build**

Run:

```powershell
cd panel/frontend
npm run test
npm run build
```

Expected: PASS.

- [ ] **Step 4: Build Docker image**

Run from repo root:

```powershell
docker build -t nre-wg-simplified:e2e .
```

Expected: PASS.

- [ ] **Step 5: Commit verification fixes**

If any test-only fixes were needed:

```powershell
git add <changed-files>
git commit -m "test(wireguard): stabilize simplified flow coverage"
```

If no files changed, skip this step.

### Task 12: Docker E2E For Mixed Relay And WG UDP

**Files:**
- Create: `scripts/e2e/wireguard-relay-l4.ps1`

- [ ] **Step 1: Write reusable E2E script**

Create `scripts/e2e/wireguard-relay-l4.ps1`:

```powershell
param(
  [string]$Image = "nre-wg-simplified:e2e",
  [string]$Network = "nre-wg-e2e-net"
)

$ErrorActionPreference = "Stop"
$PanelToken = "e2e-panel-token"
$ContainerPrefix = "nre-e2e"

function Invoke-PanelJson {
  param([string]$Method, [string]$Path, [object]$Body = $null)
  $bodyArg = @()
  if ($null -ne $Body) {
    $json = $Body | ConvertTo-Json -Depth 20 -Compress
    $bodyArg = @("-H", "Content-Type: application/json", "-d", $json)
  }
  $output = docker run --rm --network $Network curlimages/curl:8.15.0 -fsS `
    -X $Method -H "X-Panel-Token: $PanelToken" @bodyArg `
    "http://$ContainerPrefix-master:8080/panel-api$Path"
  if ([string]::IsNullOrWhiteSpace($output)) { return $null }
  return $output | ConvertFrom-Json
}

function Reset-E2E {
  docker ps -a --filter "name=$ContainerPrefix-" --format "{{.Names}}" | ForEach-Object {
    docker rm -f $_ | Out-Null
  }
  docker network inspect $Network *> $null
  if ($LASTEXITCODE -ne 0) {
    docker network create $Network | Out-Null
  }
}

function Start-Master {
  docker run -d --name "$ContainerPrefix-master" --network $Network `
    -e NRE_PANEL_TOKEN=$PanelToken `
    -e NRE_TRAFFIC_STATS_ENABLED=false `
    -e NRE_AGENT_MODE=embedded `
    $Image | Out-Null
  for ($i = 0; $i -lt 60; $i++) {
    try {
      docker run --rm --network $Network curlimages/curl:8.15.0 -fsS "http://$ContainerPrefix-master:8080/health" | Out-Null
      return
    } catch {
      Start-Sleep -Seconds 1
    }
  }
  throw "master did not become healthy"
}

function Register-Agent {
  param([string]$AgentID)
  $token = (Invoke-PanelJson POST "/agents/register-token" @{
    agent_id = $AgentID
    name = $AgentID
    capabilities = @("http", "l4", "relay", "wireguard")
  }).token
  docker run -d --name "$ContainerPrefix-$AgentID" --network $Network `
    -e NRE_CONTROL_PLANE_URL="http://$ContainerPrefix-master:8080" `
    -e NRE_AGENT_ID=$AgentID `
    -e NRE_REGISTER_TOKEN=$token `
    $Image ./go-agent | Out-Null
}

function Start-EchoBackends {
  docker run -d --name "$ContainerPrefix-backend-tcp" --network $Network alpine/socat:1.8.0.3 `
    -v TCP-LISTEN:18080,fork,reuseaddr SYSTEM:"printf tcp-ok" | Out-Null
  docker run -d --name "$ContainerPrefix-backend-udp" --network $Network alpine/socat:1.8.0.3 `
    -v UDP-LISTEN:18081,fork,reuseaddr SYSTEM:"cat" | Out-Null
}

function Configure-RelayChain {
  $relayA = Invoke-PanelJson POST "/agents/relay-a/relay-listeners" @{
    name = "relay-a-tls"
    transport_mode = "tls_tcp"
    listen_host = "0.0.0.0"
    listen_port = 19001
    public_host = "$ContainerPrefix-relay-a"
    public_port = 19001
  }
  $relayB = Invoke-PanelJson POST "/agents/relay-b/relay-listeners" @{
    name = "relay-b-wg"
    transport_mode = "wireguard"
    listen_port = 19002
    public_host = "$ContainerPrefix-relay-b"
    public_port = 51820
  }
  $relayC = Invoke-PanelJson POST "/agents/relay-c/relay-listeners" @{
    name = "relay-c-quic"
    transport_mode = "quic"
    listen_host = "0.0.0.0"
    listen_port = 19003
    public_host = "$ContainerPrefix-relay-c"
    public_port = 19003
  }
  return @(
    @{ listener_id = $relayA.listener.id },
    @{ listener_id = $relayB.listener.id },
    @{ listener_id = $relayC.listener.id }
  )
}

function Configure-L4Rules {
  param([array]$RelayLayers)
  Invoke-PanelJson POST "/agents/a/l4-rules" @{
    name = "wg-transparent-tcp"
    protocol = "tcp"
    listen_mode = "wireguard"
    wireguard_inbound_mode = "transparent"
    listen_host = "0.0.0.0"
    listen_port = 18080
    relay_layers = $RelayLayers
    backends = @(@{ host = "$ContainerPrefix-backend-tcp"; port = 18080 })
  } | Out-Null
  Invoke-PanelJson POST "/agents/a/l4-rules" @{
    name = "wg-transparent-udp"
    protocol = "udp"
    listen_mode = "wireguard"
    wireguard_inbound_mode = "transparent"
    listen_host = "0.0.0.0"
    listen_port = 18081
    relay_layers = $RelayLayers
    backends = @(@{ host = "$ContainerPrefix-backend-udp"; port = 18081 })
  } | Out-Null
}

function Create-WireGuardClientConfig {
  $profiles = Invoke-PanelJson GET "/agents/a/wireguard-profiles"
  $profile = $profiles.profiles | Select-Object -First 1
  $client = Invoke-PanelJson POST "/agents/a/wireguard-profiles/$($profile.id)/clients" @{ name = "docker-client" }
  docker run --rm --network $Network curlimages/curl:8.15.0 -fsS `
    -H "X-Panel-Token: $PanelToken" `
    "http://$ContainerPrefix-master:8080/panel-api/agents/a/wireguard-profiles/$($profile.id)/clients/$($client.client.id)/config" `
    > "$PSScriptRoot\wireguard-relay-l4-client.conf"
  return "$PSScriptRoot\wireguard-relay-l4-client.conf"
}

function Assert-Path {
  param([string]$Name, [scriptblock]$Probe)
  try {
    & $Probe
    Write-Host "PASS $Name"
  } catch {
    Write-Host "FAIL $Name"
    throw
  }
}

Reset-E2E
Start-Master
foreach ($agent in @("a", "relay-a", "relay-b", "relay-c", "b")) { Register-Agent $agent }
Start-EchoBackends
Start-Sleep -Seconds 5
$layers = Configure-RelayChain
Configure-L4Rules -RelayLayers $layers
$clientConfig = Create-WireGuardClientConfig

Assert-Path "tcp wg-client -> A transparent -> RelayA TLS -> RelayB WG -> RelayC QUIC -> B" {
  docker run --rm --network $Network --cap-add NET_ADMIN --device /dev/net/tun `
    -v "${clientConfig}:/etc/wireguard/wg0.conf:ro" ghcr.io/linuxserver/wireguard:latest `
    sh -lc "wg-quick up wg0 && nc -w 5 203.0.113.10 18080 | grep -q tcp-ok"
}

Assert-Path "udp wg-client -> A transparent -> RelayA TLS -> RelayB WG -> RelayC QUIC -> B" {
  docker run --rm --network $Network --cap-add NET_ADMIN --device /dev/net/tun `
    -v "${clientConfig}:/etc/wireguard/wg0.conf:ro" ghcr.io/linuxserver/wireguard:latest `
    sh -lc "wg-quick up wg0 && printf udp-ok | nc -u -w 5 203.0.113.11 18081 | grep -q udp-ok"
}

$agents = Invoke-PanelJson GET "/agents"
foreach ($agent in $agents.agents) {
  if ($agent.id -in @("a", "relay-a", "relay-b", "relay-c", "b") -and $agent.last_apply_status -ne "success") {
    throw "agent $($agent.id) last_apply_status=$($agent.last_apply_status)"
  }
}
```

The implementation worker must adjust endpoint paths or container entrypoint arguments only to match the actual current API/image behavior. It must keep the printed PASS names and avoid storing real tokens or secrets in the script.

- [ ] **Step 2: Run E2E script**

Run:

```powershell
.\scripts\e2e\wireguard-relay-l4.ps1 -Image nre-wg-simplified:e2e
```

Expected:

```text
PASS tcp wg-client -> A transparent -> RelayA TLS -> RelayB WG -> RelayC QUIC -> B
PASS udp wg-client -> A transparent -> RelayA TLS -> RelayB WG -> RelayC QUIC -> B
```

- [ ] **Step 3: Commit reusable E2E script**

```powershell
git add scripts/e2e/wireguard-relay-l4.ps1
git commit -m "test(wireguard): add docker relay l4 e2e"
```

---

## Final Verification Checklist

- [ ] `cd panel/backend-go && go test ./... -count=1`
- [ ] `cd go-agent && go test ./... -count=1`
- [ ] `cd panel/frontend && npm run test`
- [ ] `cd panel/frontend && npm run build`
- [ ] `docker build -t nre-wg-simplified:e2e .`
- [ ] `.\scripts\e2e\wireguard-relay-l4.ps1 -Image nre-wg-simplified:e2e`
- [ ] `git diff --check`
- [ ] `git status --short`
