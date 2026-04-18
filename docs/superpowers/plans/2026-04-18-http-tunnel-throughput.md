# HTTP Tunnel Throughput Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Improve sustained HTTP download throughput for single-hop `tls_tcp` relay rules with `relay_obfs` enabled, without changing control-plane configuration or relay protocol compatibility.

**Architecture:** Keep the current relay protocol intact and optimize the hot data path inside the Go agent. The implementation combines best-effort TCP socket tuning on raw relay connections with a chunk-queue read buffer for `tlsTCPLogicalStream`, then locks the behavior down with relay and proxy regression tests.

**Tech Stack:** Go, `go test`, Go `net`/`tls` stack, existing relay/proxy integration tests

---

### Task 1: Replace Logical Stream Whole-Buffer Appends With Chunked Reads

**Files:**
- Create: `go-agent/internal/relay/tls_tcp_session_pool_test.go`
- Modify: `go-agent/internal/relay/tls_tcp_session_pool.go`
- Test: `go-agent/internal/relay/tls_tcp_session_pool_test.go`

- [x] **Step 1: Write the failing tests**

```go
func TestTLSTCPLogicalStreamReadConsumesQueuedChunksInOrder(t *testing.T) {
	stream := &tlsTCPLogicalStream{readCh: make(chan struct{}, 1)}
	stream.appendData([]byte("hello"))
	stream.appendData([]byte("world"))

	buf := make([]byte, 7)
	n, err := stream.Read(buf)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if got := string(buf[:n]); got != "hellowo" {
		t.Fatalf("Read() = %q, want %q", got, "hellowo")
	}

	buf = make([]byte, 3)
	n, err = stream.Read(buf)
	if err != nil {
		t.Fatalf("Read() second error = %v", err)
	}
	if got := string(buf[:n]); got != "rld" {
		t.Fatalf("Read() second = %q, want %q", got, "rld")
	}
}

func TestTLSTCPLogicalStreamReadReturnsQueuedDataBeforeEOF(t *testing.T) {
	stream := &tlsTCPLogicalStream{readCh: make(chan struct{}, 1)}
	stream.appendData([]byte("payload"))
	stream.setReadError(io.EOF)

	buf := make([]byte, 7)
	n, err := stream.Read(buf)
	if err != nil {
		t.Fatalf("Read() first error = %v", err)
	}
	if got := string(buf[:n]); got != "payload" {
		t.Fatalf("Read() first = %q, want %q", got, "payload")
	}

	n, err = stream.Read(buf)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Read() second error = %v, want EOF", err)
	}
	if n != 0 {
		t.Fatalf("Read() second n = %d, want 0", n)
	}
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `cd go-agent && go test ./internal/relay -run 'TestTLSTCPLogicalStreamRead'`
Expected: FAIL because the current implementation still relies on `readBuf []byte` and does not expose the chunk-queue behavior these tests assert.

- [x] **Step 3: Write minimal implementation**

```go
type tlsTCPReadChunk struct {
	data []byte
}

type tlsTCPLogicalStream struct {
	// ...
	readChunks []*tlsTCPReadChunk
	readOffset int
}

func (s *tlsTCPLogicalStream) Read(p []byte) (int, error) {
	for {
		s.readMu.Lock()
		if len(s.readChunks) > 0 {
			head := s.readChunks[0]
			n := copy(p, head.data[s.readOffset:])
			s.readOffset += n
			if s.readOffset >= len(head.data) {
				s.readChunks[0] = nil
				s.readChunks = s.readChunks[1:]
				s.readOffset = 0
			}
			s.readMu.Unlock()
			return n, nil
		}
		if s.readErrSet {
			err := s.readErr
			s.readMu.Unlock()
			return 0, err
		}
		s.readMu.Unlock()

		select {
		case <-s.readCh:
		case <-s.tunnel.closed:
			return 0, io.EOF
		}
	}
}

func (s *tlsTCPLogicalStream) appendData(payload []byte) {
	s.readMu.Lock()
	s.readChunks = append(s.readChunks, &tlsTCPReadChunk{data: payload})
	s.readMu.Unlock()
	s.notifyReadable()
}
```

- [x] **Step 4: Run test to verify it passes**

Run: `cd go-agent && go test ./internal/relay -run 'TestTLSTCPLogicalStreamRead'`
Expected: PASS

- [x] **Step 5: Commit**

```bash
git add go-agent/internal/relay/tls_tcp_session_pool.go go-agent/internal/relay/tls_tcp_session_pool_test.go
git commit -m "fix(relay): reduce tls tcp stream copy overhead"
```

### Task 2: Add Best-Effort Relay TCP Socket Tuning

**Files:**
- Modify: `go-agent/internal/relay/timeouts.go`
- Modify: `go-agent/internal/relay/timeouts_test.go`
- Test: `go-agent/internal/relay/timeouts_test.go`

- [x] **Step 1: Write the failing tests**

```go
func TestTuneBulkRelayConnAppliesReadAndWriteBuffers(t *testing.T) {
	conn := &fakeRelayTCPBufferConn{}

	tuneBulkRelayConn(conn)

	if conn.readBuffer != relayBulkSocketBufferBytes {
		t.Fatalf("readBuffer = %d, want %d", conn.readBuffer, relayBulkSocketBufferBytes)
	}
	if conn.writeBuffer != relayBulkSocketBufferBytes {
		t.Fatalf("writeBuffer = %d, want %d", conn.writeBuffer, relayBulkSocketBufferBytes)
	}
}

func TestTuneBulkRelayConnIgnoresUnsupportedConnections(t *testing.T) {
	tuneBulkRelayConn(struct{}{})
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `cd go-agent && go test ./internal/relay -run 'TestTuneBulkRelayConn'`
Expected: FAIL because `tuneBulkRelayConn` and `relayBulkSocketBufferBytes` do not exist yet.

- [x] **Step 3: Write minimal implementation**

```go
const relayBulkSocketBufferBytes = 1 << 20

type relayTCPBufferTuner interface {
	SetReadBuffer(bytes int) error
	SetWriteBuffer(bytes int) error
}

func tuneBulkRelayConn(conn any) {
	tuner, ok := conn.(relayTCPBufferTuner)
	if !ok {
		return
	}
	_ = tuner.SetReadBuffer(relayBulkSocketBufferBytes)
	_ = tuner.SetWriteBuffer(relayBulkSocketBufferBytes)
}

func dialTCP(ctx context.Context, address string) (net.Conn, error) {
	// ...
	conn, err := dialer.DialContext(dialCtx, "tcp", address)
	if err != nil {
		return nil, err
	}
	tuneBulkRelayConn(conn)
	return conn, nil
}
```

- [x] **Step 4: Run test to verify it passes**

Run: `cd go-agent && go test ./internal/relay -run 'TestTuneBulkRelayConn'`
Expected: PASS

- [x] **Step 5: Commit**

```bash
git add go-agent/internal/relay/timeouts.go go-agent/internal/relay/timeouts_test.go
git commit -m "fix(relay): tune tcp sockets for bulk relay traffic"
```

### Task 3: Add Relay-Backed Download Regression Coverage

**Files:**
- Modify: `go-agent/internal/proxy/server_test.go`
- Test: `go-agent/internal/proxy/server_test.go`

- [x] **Step 1: Write the failing test**

```go
func TestStartStreamsLargeHTTPDownloadThroughRelayChainWithObfsMode(t *testing.T) {
	payload := bytes.Repeat([]byte("abcdefghijklmnopqrstuvwxyz012345"), 4096)
	frontendPort := pickFreePort(t)
	backendPort := pickFreePort(t)
	backendAddress := fmt.Sprintf("127.0.0.1:%d", backendPort)
	reqHost := fmt.Sprintf("edge.example.test:%d", frontendPort)

	backend := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		_, _ = w.Write(payload)
	}))
	listener, err := net.Listen("tcp", backendAddress)
	if err != nil {
		t.Fatalf("failed to listen for backend: %v", err)
	}
	backend.Listener = listener
	backend.Start()
	defer backend.Close()

	relayCert := mustIssueProxyTLSCertificate(t, "relay.internal.test")
	relayPublicPort := pickFreePort(t)
	relayAccepted := make(chan relayTestRequest, 1)
	relayStop := startTestRelayServer(t, fmt.Sprintf("127.0.0.1:%d", relayPublicPort), relayCert, relayAccepted, relay.RelayObfsModeEarlyWindowV2)
	defer relayStop()

	runtime, err := Start(context.Background(), []model.HTTPRule{{
		FrontendURL: fmt.Sprintf("http://edge.example.test:%d", frontendPort),
		BackendURL:  "http://" + backendAddress,
		RelayChain:  []int{41},
		RelayObfs:   true,
	}}, []model.RelayListener{{
		ID:         41,
		AgentID:    "remote-relay-agent",
		Name:       "relay-hop",
		ListenHost: "127.0.0.2",
		BindHosts:  []string{"127.0.0.2"},
		ListenPort: pickFreePort(t),
		PublicHost: "127.0.0.1",
		PublicPort: relayPublicPort,
		ObfsMode:   relay.RelayObfsModeEarlyWindowV2,
		Enabled:    true,
		TLSMode:    "pin_only",
		PinSet: []model.RelayPin{{
			Type:  "sha256",
			Value: mustSPKIPin(t, relayCert),
		}},
	}}, Providers{Relay: &testRuntimeMaterialProvider{}})
	if err != nil {
		t.Fatalf("failed to start relay-backed runtime: %v", err)
	}
	defer runtime.Close()

	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/download", frontendPort), nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Host = reqHost

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("relay-backed request failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read proxied body: %v", err)
	}
	if !bytes.Equal(body, payload) {
		t.Fatal("proxied download payload mismatch")
	}
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `cd go-agent && go test ./internal/proxy -run TestStartStreamsLargeHTTPDownloadThroughRelayChainWithObfsMode`
Expected: FAIL because the new large-download obfs relay regression test does not exist yet.

- [x] **Step 3: Write minimal implementation**

```go
func TestStartStreamsLargeHTTPDownloadThroughRelayChainWithObfsMode(t *testing.T) {
	// Copy the existing relay-backed setup from the smaller obfs test,
	// but replace the backend 204 responder with a real HTTP server that
	// returns a multi-chunk payload large enough to exercise sustained reads.
	// Keep:
	//   RelayChain: []int{41}
	//   RelayObfs: true
	//   relay listener ObfsMode: relay.RelayObfsModeEarlyWindowV2
	// Then read the whole proxied response body and compare it to payload.
}
```

- [x] **Step 4: Run test to verify it passes**

Run: `cd go-agent && go test ./internal/proxy -run TestStartStreamsLargeHTTPDownloadThroughRelayChainWithObfsMode`
Expected: PASS

- [x] **Step 5: Commit**

```bash
git add go-agent/internal/proxy/server_test.go
git commit -m "test(proxy): cover large obfs relay downloads"
```

### Task 4: Full Verification

**Files:**
- Modify: `docs/superpowers/plans/2026-04-18-http-tunnel-throughput.md`
- Test: `go-agent/internal/relay/...`
- Test: `go-agent/internal/proxy/...`

- [x] **Step 1: Run targeted relay and proxy verification**

Run: `cd go-agent && go test ./internal/relay ./internal/proxy`
Expected: PASS

- [x] **Step 2: Run the full Go agent suite**

Run: `cd go-agent && go test ./...`
Expected: PASS

- [x] **Step 3: Mark the completed items in this plan**

```md
- [x] Step 1: ...
- [x] Step 2: ...
```

- [x] **Step 4: Commit the finished implementation**

```bash
git add go-agent/internal/relay/tls_tcp_session_pool.go \
        go-agent/internal/relay/tls_tcp_session_pool_test.go \
        go-agent/internal/relay/timeouts.go \
        go-agent/internal/relay/timeouts_test.go \
        go-agent/internal/proxy/server_test.go \
        docs/superpowers/plans/2026-04-18-http-tunnel-throughput.md
git commit -m "fix(go-agent): improve http relay download throughput"
```
