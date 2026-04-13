# HTTP H2 H3 Listener Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `go-agent` HTTPS listeners explicitly negotiate HTTP/2 by default while adding an environment-variable-backed HTTP/3 toggle that remains disabled by default and does not start QUIC listeners yet.

**Architecture:** Extend agent config with a parsed `HTTP3Enabled` boolean so runtime intent is represented explicitly. Update the HTTPS TLS listener setup to advertise `h2` and `http/1.1` via ALPN, while leaving `h3` out of advertised protocols until a future QUIC runtime is added. Verify behavior with focused config and proxy tests.

**Tech Stack:** Go, standard library `crypto/tls`, existing `go-agent` config loader and proxy runtime tests

---

## File Structure

- Modify: `go-agent/internal/config/config.go`
  - Add `HTTP3Enabled` to agent config and parse `NRE_HTTP3_ENABLED`.
- Modify: `go-agent/internal/config/config_test.go`
  - Cover default false, explicit true, and invalid boolean parsing for `NRE_HTTP3_ENABLED`.
- Modify: `go-agent/internal/proxy/server.go`
  - Set HTTPS listener ALPN `NextProtos` explicitly to `h2` and `http/1.1`.
- Modify: `go-agent/internal/proxy/server_test.go`
  - Add a focused test that inspects the TLS listener handshake result and verifies negotiated ALPN behavior.

### Task 1: Add HTTP/3 Config Toggle

**Files:**
- Modify: `go-agent/internal/config/config.go`
- Test: `go-agent/internal/config/config_test.go`

- [ ] **Step 1: Write the failing config tests**

Add these tests to `go-agent/internal/config/config_test.go`:

```go
func TestLoadFromEnvHTTP3EnabledDefaultsFalse(t *testing.T) {
	t.Setenv("NRE_MASTER_URL", "https://master.example.com")
	t.Setenv("NRE_AGENT_TOKEN", "secret")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}
	if cfg.HTTP3Enabled {
		t.Fatal("expected HTTP3Enabled to default to false")
	}
}

func TestLoadFromEnvHTTP3EnabledParsesTrue(t *testing.T) {
	t.Setenv("NRE_MASTER_URL", "https://master.example.com")
	t.Setenv("NRE_AGENT_TOKEN", "secret")
	t.Setenv("NRE_HTTP3_ENABLED", "true")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}
	if !cfg.HTTP3Enabled {
		t.Fatal("expected HTTP3Enabled to be true")
	}
}

func TestLoadFromEnvRejectsInvalidHTTP3Enabled(t *testing.T) {
	t.Setenv("NRE_MASTER_URL", "https://master.example.com")
	t.Setenv("NRE_AGENT_TOKEN", "secret")
	t.Setenv("NRE_HTTP3_ENABLED", "maybe")

	_, err := LoadFromEnv()
	if err == nil {
		t.Fatal("expected invalid NRE_HTTP3_ENABLED error")
	}
	if !strings.Contains(err.Error(), "NRE_HTTP3_ENABLED") {
		t.Fatalf("unexpected error: %v", err)
	}
}
```

- [ ] **Step 2: Run config tests to verify they fail**

Run:

```bash
go test ./internal/config -run "TestLoadFromEnvHTTP3Enabled|TestLoadFromEnvRejectsInvalidHTTP3Enabled"
```

Expected:
- FAIL because `Config` has no `HTTP3Enabled` field and `LoadFromEnv` does not parse `NRE_HTTP3_ENABLED`.

- [ ] **Step 3: Implement the minimal config parsing**

Update `go-agent/internal/config/config.go` to add the field and parse the env var:

```go
type Config struct {
	AgentID              string
	AgentName            string
	AgentToken           string
	MasterURL            string
	DataDir              string
	HeartbeatInterval    time.Duration
	HTTP3Enabled         bool
	CurrentVersion       string
	RuntimePackageSHA256 string
}
```

```go
if val := strings.TrimSpace(os.Getenv("NRE_HTTP3_ENABLED")); val != "" {
	enabled, err := strconv.ParseBool(val)
	if err != nil {
		return Config{}, fmt.Errorf("invalid NRE_HTTP3_ENABLED: %w", err)
	}
	cfg.HTTP3Enabled = enabled
}
```

Also add the required `strconv` import.

- [ ] **Step 4: Run config tests to verify they pass**

Run:

```bash
go test ./internal/config -run "TestLoadFromEnvHTTP3Enabled|TestLoadFromEnvRejectsInvalidHTTP3Enabled"
```

Expected:
- PASS

- [ ] **Step 5: Commit**

```bash
git add go-agent/internal/config/config.go go-agent/internal/config/config_test.go
git commit -m "feat(agent): add http3 config toggle"
```

### Task 2: Explicitly Advertise HTTP/2 on HTTPS Listeners

**Files:**
- Modify: `go-agent/internal/proxy/server.go`
- Test: `go-agent/internal/proxy/server_test.go`

- [ ] **Step 1: Write the failing listener ALPN test**

Add a focused test to `go-agent/internal/proxy/server_test.go`:

```go
func TestNewTLSListenerAdvertisesHTTP2AndHTTP11Only(t *testing.T) {
	certPEM, keyPEM := mustCreateSelfSignedCertPair(t, "frontend.example.com")
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("X509KeyPair() error = %v", err)
	}

	provider := staticTLSMaterialProvider{
		byHost: map[string]tls.Certificate{
			"frontend.example.com": cert,
		},
	}

	baseListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer baseListener.Close()

	tlsListener, err := newTLSListener(context.Background(), baseListener, runtimeListenerSpec{
		bindingKey: "https:443",
		hostnames:  []string{"frontend.example.com"},
	}, provider)
	if err != nil {
		t.Fatalf("newTLSListener() error = %v", err)
	}
	defer tlsListener.Close()

	errCh := make(chan error, 1)
	go func() {
		conn, err := tlsListener.Accept()
		if err != nil {
			errCh <- err
			return
		}
		defer conn.Close()
		if tlsConn, ok := conn.(*tls.Conn); ok {
			errCh <- tlsConn.Handshake()
			return
		}
		errCh <- fmt.Errorf("accepted connection is %T", conn)
	}()

	clientConn, err := tls.Dial("tcp", baseListener.Addr().String(), &tls.Config{
		ServerName:         "frontend.example.com",
		InsecureSkipVerify: true,
		NextProtos:         []string{"h2", "http/1.1", "h3"},
	})
	if err != nil {
		t.Fatalf("tls.Dial() error = %v", err)
	}
	defer clientConn.Close()

	if err := <-errCh; err != nil {
		t.Fatalf("server handshake error = %v", err)
	}

	if got := clientConn.ConnectionState().NegotiatedProtocol; got != "h2" {
		t.Fatalf("negotiated protocol = %q", got)
	}
	for _, proto := range clientConn.ConnectionState().NegotiatedProtocolIsMutual; []string{} {
		_ = proto
	}
}
```

Then assert the server did not negotiate `h3` by checking the negotiated protocol is exactly `h2`.

- [ ] **Step 2: Run proxy test to verify it fails**

Run:

```bash
go test ./internal/proxy -run TestNewTLSListenerAdvertisesHTTP2AndHTTP11Only
```

Expected:
- FAIL because the listener does not explicitly advertise `h2`.

- [ ] **Step 3: Implement explicit ALPN advertisement**

Update the TLS config in `go-agent/internal/proxy/server.go`:

```go
config := &tls.Config{
	MinVersion: tls.VersionTLS12,
	NextProtos: []string{"h2", "http/1.1"},
	GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		// existing certificate selection logic
	},
}
```

Do not include `h3` in `NextProtos`.

- [ ] **Step 4: Run proxy test to verify it passes**

Run:

```bash
go test ./internal/proxy -run TestNewTLSListenerAdvertisesHTTP2AndHTTP11Only
```

Expected:
- PASS

- [ ] **Step 5: Run the full affected test suites**

Run:

```bash
go test ./internal/config ./internal/proxy
go test ./...
```

Expected:
- PASS with zero failures

- [ ] **Step 6: Commit**

```bash
git add go-agent/internal/proxy/server.go go-agent/internal/proxy/server_test.go
git add go-agent/internal/config/config.go go-agent/internal/config/config_test.go
git commit -m "feat(agent): enable h2 on https listeners"
```
