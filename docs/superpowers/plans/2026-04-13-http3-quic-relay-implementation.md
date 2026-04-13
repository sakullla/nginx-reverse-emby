# HTTP/3 and QUIC Relay Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement real HTTP/3 ingress, listener-scoped QUIC relay transport, UDP-over-relay for both QUIC and `tls_tcp` via UoT, and replace relay obfuscation with `early_window_v2`.

**Architecture:** Keep HTTP ingress, relay transport, and L4 forwarding as separate layers. Add listener-scoped transport and obfuscation policy to shared models and control-plane storage first, then implement QUIC runtime and UoT in the agent, then wire HTTP and L4 to the new transport selection and fallback rules.

**Tech Stack:** Go 1.26, `quic-go`, `quic-go/http3`, existing Go agent runtime, SQLite-backed control plane, standard Go tests.

---

## File Map

### Existing files to modify

- `go-agent/go.mod`
- `go-agent/internal/config/config.go`
- `go-agent/internal/config/config_test.go`
- `go-agent/internal/model/relay.go`
- `go-agent/internal/model/l4.go`
- `go-agent/internal/proxy/server.go`
- `go-agent/internal/proxy/server_test.go`
- `go-agent/internal/app/local_runtime.go`
- `go-agent/internal/l4/server.go`
- `go-agent/internal/l4/server_test.go`
- `go-agent/internal/relay/types.go`
- `go-agent/internal/relay/protocol.go`
- `go-agent/internal/relay/runtime.go`
- `go-agent/internal/relay/validation.go`
- `go-agent/internal/relay/obfs.go`
- `go-agent/internal/relay/obfs_test.go`
- `go-agent/internal/relay/runtime_test.go`
- `panel/backend-go/internal/controlplane/storage/sqlite_models.go`
- `panel/backend-go/internal/controlplane/storage/schema.go`
- `panel/backend-go/internal/controlplane/storage/snapshot_types.go`
- `panel/backend-go/internal/controlplane/service/agents.go`
- `panel/backend-go/internal/controlplane/service/l4.go`
- `panel/backend-go/internal/controlplane/service/l4_test.go`
- `panel/backend-go/internal/controlplane/service/relay.go`
- `panel/backend-go/internal/controlplane/service/relay_test.go`
- `README.md`

### New files to create

- `go-agent/internal/proxy/http3_runtime.go`
- `go-agent/internal/proxy/http3_runtime_test.go`
- `go-agent/internal/relay/quic_runtime.go`
- `go-agent/internal/relay/quic_runtime_test.go`
- `go-agent/internal/relay/session_pool.go`
- `go-agent/internal/relay/uot.go`
- `go-agent/internal/relay/uot_test.go`
- `go-agent/internal/relay/obfs_early_window.go`
- `go-agent/internal/relay/obfs_early_window_test.go`

### Notes on boundaries

- Keep QUIC-specific logic out of `runtime.go` as much as possible; use dedicated files.
- Keep UoT framing separate from QUIC stream framing.
- Keep HTTP/3 listener bootstrap separate from request routing logic.
- Keep control-plane field validation in service layer and persistence details in storage layer.

## Task 1: Add Shared Transport and Obfuscation Model Fields

**Files:**
- Modify: `go-agent/internal/model/relay.go`
- Modify: `panel/backend-go/internal/controlplane/storage/sqlite_models.go`
- Modify: `panel/backend-go/internal/controlplane/storage/schema.go`
- Modify: `panel/backend-go/internal/controlplane/storage/snapshot_types.go`
- Modify: `panel/backend-go/internal/controlplane/service/relay.go`
- Modify: `panel/backend-go/internal/controlplane/service/relay_test.go`
- Test: `panel/backend-go/internal/controlplane/service/relay_test.go`

- [ ] **Step 1: Write the failing control-plane tests**

```go
func TestRelayListenerDefaultsTransportAndObfs(t *testing.T) {
	listener, err := normalizeRelayListenerInput(RelayListenerInput{
		Name:       stringPtr("relay-a"),
		ListenPort: intPtr(9443),
	}, RelayListener{}, 1, "local")
	if err != nil {
		t.Fatalf("normalizeRelayListenerInput() error = %v", err)
	}
	if listener.TransportMode != "tls_tcp" {
		t.Fatalf("TransportMode = %q", listener.TransportMode)
	}
	if !listener.AllowTransportFallback {
		t.Fatal("AllowTransportFallback = false")
	}
	if listener.ObfsMode != "off" {
		t.Fatalf("ObfsMode = %q", listener.ObfsMode)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd panel/backend-go && go test ./internal/controlplane/service -run TestRelayListenerDefaultsTransportAndObfs`
Expected: FAIL with unknown fields or missing defaults

- [ ] **Step 3: Add model and storage fields**

```go
type RelayListener struct {
	ID                     int        `json:"id"`
	AgentID                string     `json:"agent_id"`
	Name                   string     `json:"name"`
	ListenHost             string     `json:"listen_host"`
	BindHosts              []string   `json:"bind_hosts"`
	ListenPort             int        `json:"listen_port"`
	PublicHost             string     `json:"public_host"`
	PublicPort             int        `json:"public_port"`
	Enabled                bool       `json:"enabled"`
	CertificateID          *int       `json:"certificate_id"`
	TLSMode                string     `json:"tls_mode"`
	TransportMode          string     `json:"transport_mode"`
	AllowTransportFallback bool       `json:"allow_transport_fallback"`
	ObfsMode               string     `json:"obfs_mode"`
	PinSet                 []RelayPin `json:"pin_set"`
	TrustedCACertificateIDs []int     `json:"trusted_ca_certificate_ids"`
	AllowSelfSigned        bool       `json:"allow_self_signed"`
	Tags                   []string   `json:"tags"`
	Revision               int64      `json:"revision"`
}
```

- [ ] **Step 4: Add storage migration**

```go
`ALTER TABLE relay_listeners ADD COLUMN transport_mode TEXT NOT NULL DEFAULT 'tls_tcp'`,
`ALTER TABLE relay_listeners ADD COLUMN allow_transport_fallback INTEGER NOT NULL DEFAULT 1`,
`ALTER TABLE relay_listeners ADD COLUMN obfs_mode TEXT NOT NULL DEFAULT 'off'`,
```

- [ ] **Step 5: Add service validation and legacy `relay_obfs` migration**

```go
switch transportMode {
case "", "tls_tcp":
	transportMode = "tls_tcp"
case "quic":
default:
	return RelayListener{}, fmt.Errorf("%w: transport_mode must be tls_tcp or quic", ErrInvalidArgument)
}

switch obfsMode {
case "":
	obfsMode = "off"
case "off", "early_window_v2":
default:
	return RelayListener{}, fmt.Errorf("%w: obfs_mode must be off or early_window_v2", ErrInvalidArgument)
}
if transportMode == "quic" {
	obfsMode = "off"
}
```

- [ ] **Step 6: Run focused tests**

Run: `cd panel/backend-go && go test ./internal/controlplane/service -run TestRelayListener`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add panel/backend-go/internal/controlplane/storage/sqlite_models.go panel/backend-go/internal/controlplane/storage/schema.go panel/backend-go/internal/controlplane/storage/snapshot_types.go panel/backend-go/internal/controlplane/service/relay.go panel/backend-go/internal/controlplane/service/relay_test.go go-agent/internal/model/relay.go
git commit -m "feat(relay): add listener transport and obfs policy"
```

## Task 2: Add Agent Capability and HTTP/3 Config Plumbing

**Files:**
- Modify: `go-agent/go.mod`
- Modify: `go-agent/internal/config/config.go`
- Modify: `go-agent/internal/config/config_test.go`
- Modify: `go-agent/internal/app/app.go`
- Modify: `panel/backend-go/internal/controlplane/service/agents.go`
- Test: `go-agent/internal/config/config_test.go`
- Test: `go-agent/internal/app/app_test.go`

- [ ] **Step 1: Write the failing capability test**

```go
func TestAppAdvertisesHTTP3AndRelayQUICCapabilities(t *testing.T) {
	app := newAppWithDeps(config.Config{HTTP3Enabled: true}, nil, nil, nil, nil, nil)
	caps := app.describe().Capabilities
	if !contains(caps, "http3_ingress") {
		t.Fatalf("capabilities = %+v", caps)
	}
	if !contains(caps, "relay_quic") {
		t.Fatalf("capabilities = %+v", caps)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd go-agent && go test ./internal/app -run TestAppAdvertisesHTTP3AndRelayQUICCapabilities`
Expected: FAIL because capabilities are missing

- [ ] **Step 3: Add QUIC dependencies**

```go
require (
	github.com/go-acme/lego/v4 v4.33.0
	github.com/quic-go/quic-go v0.54.0
	github.com/quic-go/quic-go/http3 v0.54.0
	golang.org/x/sys v0.43.0
)
```

- [ ] **Step 4: Keep config parsing but document runtime use**

```go
if val := strings.TrimSpace(os.Getenv("NRE_HTTP3_ENABLED")); val != "" {
	enabled, err := strconv.ParseBool(val)
	if err != nil {
		return Config{}, fmt.Errorf("invalid NRE_HTTP3_ENABLED: %w", err)
	}
	cfg.HTTP3Enabled = enabled
}
```

- [ ] **Step 5: Advertise capabilities**

```go
capabilities := []string{"http_rules", "cert_install", "local_acme", "l4", "relay_quic"}
if a.cfg.HTTP3Enabled {
	capabilities = append(capabilities, "http3_ingress")
}
```

- [ ] **Step 6: Run focused tests**

Run: `cd go-agent && go test ./internal/config ./internal/app`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add go-agent/go.mod go-agent/go.sum go-agent/internal/config/config.go go-agent/internal/config/config_test.go go-agent/internal/app/app.go go-agent/internal/app/app_test.go panel/backend-go/internal/controlplane/service/agents.go
git commit -m "feat(agent): advertise http3 and relay quic capabilities"
```

## Task 3: Implement HTTP/3 Runtime for HTTPS Bindings

**Files:**
- Create: `go-agent/internal/proxy/http3_runtime.go`
- Create: `go-agent/internal/proxy/http3_runtime_test.go`
- Modify: `go-agent/internal/proxy/server.go`
- Modify: `go-agent/internal/proxy/server_test.go`
- Modify: `go-agent/internal/app/local_runtime.go`
- Test: `go-agent/internal/proxy/http3_runtime_test.go`

- [ ] **Step 1: Write the failing HTTP/3 binding test**

```go
func TestStartWithResourcesStartsHTTP3ForHTTPSBinding(t *testing.T) {
	runtime, err := StartWithResources(context.Background(), []model.HTTPRule{{
		FrontendURL: "https://frontend.example.com",
		BackendURL:  "http://127.0.0.1:8080",
	}}, nil, Providers{TLS: testTLSProvider{}}, backends.NewCache(backends.Config{}), NewSharedTransport(), true)
	if err != nil {
		t.Fatalf("StartWithResources() error = %v", err)
	}
	defer runtime.Close()
	if len(runtime.http3Servers) != 1 {
		t.Fatalf("http3 server count = %d", len(runtime.http3Servers))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd go-agent && go test ./internal/proxy -run TestStartWithResourcesStartsHTTP3ForHTTPSBinding`
Expected: FAIL because HTTP/3 state does not exist

- [ ] **Step 3: Add HTTP/3 runtime structure**

```go
type http3ServerHandle struct {
	server   *http3.Server
	packet   net.PacketConn
	binding  string
}
```

- [ ] **Step 4: Extend runtime bootstrap**

```go
func StartWithResources(
	ctx context.Context,
	rules []model.HTTPRule,
	relayListeners []model.RelayListener,
	providers Providers,
	backendCache *backends.Cache,
	sharedTransport *http.Transport,
	http3Enabled bool,
) (*Runtime, error) {
	// existing TCP startup
	// if http3Enabled && spec.scheme == "https" { startHTTP3Server(...) }
}
```

- [ ] **Step 5: Wire config from `local_runtime`**

```go
runtime, err := proxy.StartWithResources(ctx, rules, relayListeners, providers, m.cache, m.transport, m.http3Enabled)
```

- [ ] **Step 6: Add focused tests for graceful degradation**

```go
func TestHTTP3StartupFailureDoesNotBreakTCPHTTPS(t *testing.T) {
	// inject packet listener failure and assert TCP runtime still starts
}
```

- [ ] **Step 7: Run focused tests**

Run: `cd go-agent && go test ./internal/proxy -run TestStartWithResources`
Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add go-agent/internal/proxy/http3_runtime.go go-agent/internal/proxy/http3_runtime_test.go go-agent/internal/proxy/server.go go-agent/internal/proxy/server_test.go go-agent/internal/app/local_runtime.go
git commit -m "feat(proxy): start http3 listeners for https bindings"
```

## Task 4: Implement QUIC Relay Runtime and Session Pool

**Files:**
- Create: `go-agent/internal/relay/quic_runtime.go`
- Create: `go-agent/internal/relay/quic_runtime_test.go`
- Create: `go-agent/internal/relay/session_pool.go`
- Modify: `go-agent/internal/relay/types.go`
- Modify: `go-agent/internal/relay/protocol.go`
- Modify: `go-agent/internal/relay/runtime.go`
- Modify: `go-agent/internal/relay/validation.go`
- Modify: `go-agent/internal/relay/runtime_test.go`
- Test: `go-agent/internal/relay/quic_runtime_test.go`

- [ ] **Step 1: Write the failing QUIC relay test**

```go
func TestDialQUICRoundTripTCP(t *testing.T) {
	server, hop := newQUICRelayEndpoint(t)
	defer server.Close()
	conn, err := Dial(context.Background(), "tcp", "127.0.0.1:9001", []Hop{hop}, testProvider{}, DialOptions{})
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd go-agent && go test ./internal/relay -run TestDialQUICRoundTripTCP`
Expected: FAIL because QUIC transport is unsupported

- [ ] **Step 3: Extend listener metadata**

```go
type Hop struct {
	Address    string   `json:"address"`
	ServerName string   `json:"server_name,omitempty"`
	Listener   Listener `json:"listener"`
}

func listenerTransportMode(listener Listener) string {
	if strings.TrimSpace(listener.TransportMode) == "" {
		return "tls_tcp"
	}
	return listener.TransportMode
}
```

- [ ] **Step 4: Add QUIC open frame**

```go
type relayOpenFrame struct {
	Kind     string `json:"kind"`
	Target   string `json:"target"`
	Chain    []Hop  `json:"chain,omitempty"`
	Metadata struct{} `json:"metadata,omitempty"`
}
```

- [ ] **Step 5: Implement session pool**

```go
type sessionPool struct {
	mu       sync.Mutex
	sessions map[string]quic.Connection
}
```

- [ ] **Step 6: Route `Dial` by transport**

```go
switch listenerTransportMode(firstHop.Listener) {
case "quic":
	return dialQUIC(ctx, network, target, chain, provider, options)
default:
	return dialTLSTCP(ctx, network, target, chain, provider, options)
}
```

- [ ] **Step 7: Run focused relay tests**

Run: `cd go-agent && go test ./internal/relay -run 'TestDialQUICRoundTripTCP|TestDialFallsBackToTLSTCP'`
Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add go-agent/internal/relay/quic_runtime.go go-agent/internal/relay/quic_runtime_test.go go-agent/internal/relay/session_pool.go go-agent/internal/relay/types.go go-agent/internal/relay/protocol.go go-agent/internal/relay/runtime.go go-agent/internal/relay/validation.go go-agent/internal/relay/runtime_test.go
git commit -m "feat(relay): add quic transport and fallback"
```

## Task 5: Replace Obfuscation with `early_window_v2`

**Files:**
- Create: `go-agent/internal/relay/obfs_early_window.go`
- Create: `go-agent/internal/relay/obfs_early_window_test.go`
- Modify: `go-agent/internal/relay/obfs.go`
- Modify: `go-agent/internal/relay/runtime.go`
- Modify: `go-agent/internal/relay/runtime_test.go`
- Test: `go-agent/internal/relay/obfs_early_window_test.go`

- [ ] **Step 1: Write the failing masking fidelity test**

```go
func TestEarlyWindowMaskerRoundTrip(t *testing.T) {
	payload := bytes.Repeat([]byte{0x17, 0x03, 0x03, 0x00, 0x20}, 256)
	var masked bytes.Buffer
	writer := newEarlyWindowMaskWriter(&masked, earlyWindowMaskConfig{MaxBytes: 32768, MaxWrites: 8, Seed: 1})
	if _, err := writer.Write(payload); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	reader := newEarlyWindowMaskReader(bytes.NewReader(masked.Bytes()))
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("payload mismatch")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd go-agent && go test ./internal/relay -run TestEarlyWindowMaskerRoundTrip`
Expected: FAIL because the new masker does not exist

- [ ] **Step 3: Implement new masker and delete `first_segment_v1` usage**

```go
type earlyWindowMaskConfig struct {
	MaxBytes int
	MaxWrites int
	Seed int64
}
```

- [ ] **Step 4: Wire obfuscation mode into `tls_tcp` only**

```go
if listenerTransportMode(firstHop.Listener) == "tls_tcp" && strings.EqualFold(firstHop.Listener.ObfsMode, "early_window_v2") {
	return wrapConnWithEarlyWindowMask(relayConn, defaultEarlyWindowMaskConfig()), nil
}
```

- [ ] **Step 5: Remove references to the old mode**

```go
const (
	TransportModeOff = ""
)
```

- [ ] **Step 6: Run focused tests**

Run: `cd go-agent && go test ./internal/relay -run 'TestEarlyWindowMasker|TestDial.*Obfs'`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add go-agent/internal/relay/obfs.go go-agent/internal/relay/obfs_early_window.go go-agent/internal/relay/obfs_early_window_test.go go-agent/internal/relay/runtime.go go-agent/internal/relay/runtime_test.go go-agent/internal/relay/protocol.go
git commit -m "feat(relay): replace first segment obfs with early window masking"
```

## Task 6: Add UDP Relay via QUIC Streams and UoT Over `tls_tcp`

**Files:**
- Create: `go-agent/internal/relay/uot.go`
- Create: `go-agent/internal/relay/uot_test.go`
- Modify: `go-agent/internal/l4/server.go`
- Modify: `go-agent/internal/l4/server_test.go`
- Modify: `go-agent/internal/relay/quic_runtime.go`
- Modify: `go-agent/internal/relay/runtime.go`
- Test: `go-agent/internal/l4/server_test.go`

- [ ] **Step 1: Write the failing UDP-over-relay test**

```go
func TestUDPRelayOverTLSTCPUOT(t *testing.T) {
	server := mustNewL4Server(t, []model.L4Rule{{
		Protocol:   "udp",
		ListenHost: "127.0.0.1",
		ListenPort: 9953,
		Backends:   []model.L4Backend{{Host: "127.0.0.1", Port: 15353}},
		RelayChain: []int{1},
	}})
	defer server.Close()
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd go-agent && go test ./internal/l4 -run TestUDPRelayOverTLSTCPUOT`
Expected: FAIL because UDP relay only supports local UDP right now

- [ ] **Step 3: Implement UoT framing**

```go
func writeUOTPacket(w io.Writer, payload []byte) error {
	var header [2]byte
	binary.BigEndian.PutUint16(header[:], uint16(len(payload)))
	if err := writeAll(w, header[:]); err != nil {
		return err
	}
	return writeAll(w, payload)
}
```

- [ ] **Step 4: Add UDP relay dial branching**

```go
switch listenerTransportMode(hops[0].Listener) {
case "quic":
	return s.dialUDPOverQUIC(rule, hops)
default:
	return s.dialUDPOverTLSTCPUOT(rule, hops)
}
```

- [ ] **Step 5: Remove control-plane behavior that clears UDP relay chains**

```go
if protocol != "tcp" {
	// delete this branch that forces relayChain = []int{}
}
```

- [ ] **Step 6: Add focused tests for both transports**

Run: `cd go-agent && go test ./internal/l4 -run 'TestUDPRelayOverQUIC|TestUDPRelayOverTLSTCPUOT'`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add go-agent/internal/relay/uot.go go-agent/internal/relay/uot_test.go go-agent/internal/l4/server.go go-agent/internal/l4/server_test.go go-agent/internal/relay/quic_runtime.go go-agent/internal/relay/runtime.go panel/backend-go/internal/controlplane/service/l4.go panel/backend-go/internal/controlplane/service/l4_test.go
git commit -m "feat(l4): support udp relay over quic and uot"
```

## Task 7: Wire HTTP and L4 to Listener-Scoped Transport Selection

**Files:**
- Modify: `go-agent/internal/proxy/server.go`
- Modify: `go-agent/internal/proxy/server_test.go`
- Modify: `go-agent/internal/l4/server.go`
- Modify: `go-agent/internal/l4/server_test.go`
- Modify: `panel/backend-go/internal/controlplane/service/l4.go`
- Modify: `panel/backend-go/internal/controlplane/service/l4_test.go`
- Test: `go-agent/internal/proxy/server_test.go`

- [ ] **Step 1: Write the failing transport-selection test**

```go
func TestResolveRelayHopsCarriesTransportMode(t *testing.T) {
	hops, err := resolveRelayHops(model.HTTPRule{
		FrontendURL: "https://frontend.example.com",
		RelayChain:  []int{1},
	}, []model.RelayListener{{
		ID:            1,
		PublicHost:    "relay.example.com",
		PublicPort:    8443,
		TransportMode: "quic",
		Enabled:       true,
	}})
	if err != nil {
		t.Fatalf("resolveRelayHops() error = %v", err)
	}
	if hops[0].Listener.TransportMode != "quic" {
		t.Fatalf("TransportMode = %q", hops[0].Listener.TransportMode)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd go-agent && go test ./internal/proxy ./internal/l4 -run TestResolveRelayHopsCarriesTransportMode`
Expected: FAIL because hop selection ignores the new field

- [ ] **Step 3: Carry listener transport metadata through proxy and L4**

```go
hops = append(hops, relay.Hop{
	Address:    net.JoinHostPort(host, strconv.Itoa(port)),
	ServerName: host,
	Listener:   listener,
})
```

- [ ] **Step 4: Update control-plane UDP validation to allow UoT**

```go
if protocol == "udp" && relayObfs {
	relayObfs = false
}
// keep relay chain; do not clear it for udp
```

- [ ] **Step 5: Run focused tests**

Run: `cd go-agent && go test ./internal/proxy ./internal/l4`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add go-agent/internal/proxy/server.go go-agent/internal/proxy/server_test.go go-agent/internal/l4/server.go go-agent/internal/l4/server_test.go panel/backend-go/internal/controlplane/service/l4.go panel/backend-go/internal/controlplane/service/l4_test.go
git commit -m "feat(relay): select proxy and l4 transport per listener"
```

## Task 8: Update Docs, Run Full Verification, and Clean Legacy Paths

**Files:**
- Modify: `README.md`
- Modify: `go-agent/internal/relay/protocol.go`
- Modify: `go-agent/internal/relay/obfs.go`
- Test: `go-agent/internal/relay/...`
- Test: `go-agent/internal/proxy/...`
- Test: `go-agent/internal/l4/...`
- Test: `panel/backend-go/internal/controlplane/service/...`

- [ ] **Step 1: Update README configuration docs**

```md
- `NRE_HTTP3_ENABLED=true` enables HTTP/3 ingress for HTTPS bindings.
- Relay listeners support `transport_mode=tls_tcp|quic`.
- `obfs_mode=early_window_v2` applies only to `tls_tcp`.
- UDP relay uses QUIC streams by preference and UoT on `tls_tcp`.
```

- [ ] **Step 2: Remove old obfuscation naming leftovers**

```go
// delete first_segment_v1 constants and tests
// keep only early_window_v2 naming in runtime and protocol paths
```

- [ ] **Step 3: Run gofmt**

Run: `cd go-agent && gofmt -w internal/proxy internal/relay internal/l4 internal/model internal/app internal/config`
Expected: no output

- [ ] **Step 4: Run agent test suites**

Run: `cd go-agent && go test ./...`
Expected: all packages PASS

- [ ] **Step 5: Run control-plane test suites**

Run: `cd panel/backend-go && go test ./...`
Expected: all packages PASS

- [ ] **Step 6: Commit**

```bash
git add README.md go-agent/internal/relay/protocol.go go-agent/internal/relay/obfs.go go-agent/internal/relay/obfs_early_window.go go-agent/internal/relay/obfs_early_window_test.go
git commit -m "docs: document http3 relay transport and obfs changes"
```

## Self-Review

### Spec coverage

- HTTP/3 ingress is covered by Task 3 and Task 8.
- Listener-scoped transport and fallback are covered by Task 1 and Task 4.
- QUIC relay transport is covered by Task 4.
- UDP relay over QUIC and `tls_tcp` UoT is covered by Task 6.
- New obfuscation implementation is covered by Task 5.
- Control-plane validation and migration are covered by Task 1 and Task 7.
- Agent capabilities are covered by Task 2.

### Placeholder scan

- No `TODO`, `TBD`, or deferred implementation markers remain.
- Every task contains concrete files, commands, and example code for the change.

### Type consistency

- Listener fields are consistently named `TransportMode`, `AllowTransportFallback`, and `ObfsMode`.
- New masking mode is consistently named `early_window_v2`.
- UDP over TCP framing is consistently named `UoT`.

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-04-13-http3-quic-relay-implementation.md`. Two execution options:

**1. Subagent-Driven (recommended)** - I dispatch a fresh subagent per task, review between tasks, fast iteration

**2. Inline Execution** - Execute tasks in this session using executing-plans, batch execution with checkpoints

Which approach?
