# Relay L4 Throughput And Latency Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove the extra first-byte RTT from `L4 TCP over tls_tcp relay` and raise `tls_tcp` relay throughput at the same CPU budget, then extend the same version to HTTP, UDP, and QUIC without changing control-plane configuration.

**Architecture:** Extend the relay open handshake so a client can send `OPEN + initial_data`, then rework the `tls_tcp` logical-stream data path around pooled large chunks and `io.Copy` fast paths. Keep UDP packet semantics separate, and treat QUIC latency/allocation work as a transport-specific follow-up instead of forcing it into the `tls_tcp` framing model.

**Tech Stack:** Go, `net`, `io`, `sync.Pool`, existing relay runtime tests, Go benchmarks, Docker image build, docker compose validation.

---

## File Structure

- Modify: `go-agent/internal/relay/protocol.go`
  Purpose: add `InitialData` to `relayOpenFrame` and keep JSON marshal/unmarshal helpers aligned.
- Modify: `go-agent/internal/relay/mux_protocol.go`
  Purpose: keep mux framing helpers compatible with larger bulk frames and chunk-aware payload helpers.
- Modify: `go-agent/internal/relay/runtime.go`
  Purpose: extend `DialOptions`, thread initial payload into `Dial`, and keep generic `pipeBothWays` able to exploit `ReadFrom`/`WriteTo`.
- Modify: `go-agent/internal/relay/tls_tcp_session_pool.go`
  Purpose: implement `OPEN + initial_data`, chunk-backed logical stream buffering, and bulk `ReadFrom`/`WriteTo` paths.
- Create: `go-agent/internal/relay/chunk_pool.go`
  Purpose: define a small pooled-chunk abstraction shared by `tls_tcp` read/write paths and UDP packet reuse.
- Modify: `go-agent/internal/relay/uot.go`
  Purpose: let UDP packet helpers reuse pooled buffers without losing packet boundaries.
- Modify: `go-agent/internal/relay/quic_runtime.go`
  Purpose: add QUIC-side dial options plumbing and reduce stream-open / packet-path overhead where relay measurements show waste.
- Modify: `go-agent/internal/l4/server.go`
  Purpose: prefetch the first downstream TCP bytes before relay dial, pass them through `relay.DialOptions`, and keep proxy-protocol handling correct.
- Modify: `go-agent/internal/proxy/server.go`
  Purpose: reuse the new relay dial options and bulk data path in HTTP relay transport.
- Modify: `go-agent/internal/relay/protocol_test.go`
  Purpose: cover `relayOpenFrame` JSON round-trip with `InitialData`.
- Modify: `go-agent/internal/relay/mux_protocol_test.go`
  Purpose: cover mux frame round-trip with large open/data payloads.
- Modify: `go-agent/internal/relay/tls_tcp_session_pool_test.go`
  Purpose: cover first-chunk handling, chunk queue behavior, `ReadFrom`, and `WriteTo`.
- Modify: `go-agent/internal/relay/runtime_test.go`
  Purpose: cover one-hop and multi-hop TCP/UDP relay behavior after the protocol rewrite.
- Modify: `go-agent/internal/relay/quic_runtime_test.go`
  Purpose: cover QUIC stream open success/fallback after dial option changes.
- Modify: `go-agent/internal/l4/server_test.go`
  Purpose: prove L4 relay now forwards the first payload without the old empty RTT and still preserves proxy-protocol semantics.
- Modify: `go-agent/internal/proxy/server_test.go`
  Purpose: prove HTTP relay keeps working on the new `tls_tcp` path for streaming downloads.
- Create: `go-agent/internal/relay/perf_bench_test.go`
  Purpose: add large-stream and UDP packet benchmarks so CPU/throughput regressions are measurable in CI and locally.

## Task 1: Extend Relay Open Handshake And Dial Options

**Files:**
- Modify: `go-agent/internal/relay/protocol.go`
- Modify: `go-agent/internal/relay/runtime.go`
- Test: `go-agent/internal/relay/protocol_test.go`

- [ ] **Step 1: Write the failing protocol and dial-option tests**

```go
func TestRelayOpenFrameRoundTripsInitialData(t *testing.T) {
	payload, err := marshalMuxOpenPayload(relayOpenFrame{
		Kind:        "tcp",
		Target:      "127.0.0.1:9000",
		InitialData: []byte("hello"),
	})
	if err != nil {
		t.Fatalf("marshalMuxOpenPayload() error = %v", err)
	}

	frame, err := readMuxOpenPayload(payload)
	if err != nil {
		t.Fatalf("readMuxOpenPayload() error = %v", err)
	}

	if got := string(frame.InitialData); got != "hello" {
		t.Fatalf("InitialData = %q, want %q", got, "hello")
	}
}

func TestDialOptionsCloneInitialPayload(t *testing.T) {
	opts := DialOptions{InitialPayload: []byte("abc")}
	clone := opts.clone()
	opts.InitialPayload[0] = 'z'

	if got := string(clone.InitialPayload); got != "abc" {
		t.Fatalf("clone.InitialPayload = %q, want %q", got, "abc")
	}
}
```

- [ ] **Step 2: Run the targeted tests to confirm the gap**

Run: `cd go-agent && go test ./internal/relay -run "TestRelayOpenFrameRoundTripsInitialData|TestDialOptionsCloneInitialPayload" -count=1`
Expected: FAIL with unknown `InitialData` field / missing `DialOptions.clone`.

- [ ] **Step 3: Add the protocol field and dial-options plumbing**

```go
type relayOpenFrame struct {
	Kind        string         `json:"kind"`
	Target      string         `json:"target"`
	Chain       []Hop          `json:"chain,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	InitialData []byte         `json:"initial_data,omitempty"`
}

type DialOptions struct {
	InitialPayload []byte
}

func (o DialOptions) clone() DialOptions {
	if len(o.InitialPayload) == 0 {
		return DialOptions{}
	}
	return DialOptions{InitialPayload: append([]byte(nil), o.InitialPayload...)}
}
```

- [ ] **Step 4: Thread `DialOptions` through relay entry points**

```go
func Dial(ctx context.Context, network, target string, chain []Hop, provider TLSMaterialProvider, opts ...DialOptions) (net.Conn, error) {
	options := DialOptions{}
	if len(opts) > 0 {
		options = opts[0].clone()
	}
	if provider == nil {
		return nil, fmt.Errorf("tls material provider is required")
	}
	if len(chain) == 0 {
		return nil, fmt.Errorf("relay chain is required")
	}
	return dialTLSTCPMux(ctx, network, target, chain, provider, options)
}

func dialTLSTCPMux(ctx context.Context, network, target string, chain []Hop, provider TLSMaterialProvider, options DialOptions) (net.Conn, error) {
	return tunnel.openStream(ctx, relayOpenFrame{
		Kind:        network,
		Target:      target,
		Chain:       append([]Hop(nil), chain[1:]...),
		InitialData: options.InitialPayload,
	})
}
```

- [ ] **Step 5: Run the targeted tests again**

Run: `cd go-agent && go test ./internal/relay -run "TestRelayOpenFrameRoundTripsInitialData|TestDialOptionsCloneInitialPayload" -count=1`
Expected: PASS.

- [ ] **Step 6: Commit the handshake-plumbing slice**

```bash
git add go-agent/internal/relay/protocol.go go-agent/internal/relay/runtime.go go-agent/internal/relay/protocol_test.go
git commit -m "feat(relay): add initial payload dial options"
```

## Task 2: Rebuild `tls_tcp` Logical Streams Around Pooled Chunks

**Files:**
- Create: `go-agent/internal/relay/chunk_pool.go`
- Modify: `go-agent/internal/relay/mux_protocol.go`
- Modify: `go-agent/internal/relay/tls_tcp_session_pool.go`
- Test: `go-agent/internal/relay/mux_protocol_test.go`
- Test: `go-agent/internal/relay/tls_tcp_session_pool_test.go`

- [ ] **Step 1: Write failing tests for initial-data delivery and bulk streaming**

```go
type noopDeadlineConn struct{ net.Conn }

func (noopDeadlineConn) SetDeadline(time.Time) error      { return nil }
func (noopDeadlineConn) SetReadDeadline(time.Time) error  { return nil }
func (noopDeadlineConn) SetWriteDeadline(time.Time) error { return nil }

func TestTLSTCPLogicalStreamQueuesInitialDataBeforeOpenResult(t *testing.T) {
	stream := &tlsTCPLogicalStream{readCh: make(chan struct{}, 1)}
	stream.appendDataChunk(&relayChunk{buf: []byte("hello"), n: len("hello")})
	stream.deliverOpenResult(nil)

	buf := make([]byte, 5)
	n, err := stream.Read(buf)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if got := string(buf[:n]); got != "hello" {
		t.Fatalf("Read() = %q, want %q", got, "hello")
	}
}

func TestTLSTCPLogicalStreamReadFromSplitsLargePayloadIntoMuxFrames(t *testing.T) {
	var wire bytes.Buffer
	tunnel := &tlsTCPTunnel{rawConn: noopDeadlineConn{}, writer: &wire, streams: map[uint32]*tlsTCPLogicalStream{}, closed: make(chan struct{})}
	stream := &tlsTCPLogicalStream{tunnel: tunnel, streamID: 7, readCh: make(chan struct{}, 1)}
	src := bytes.NewReader(bytes.Repeat([]byte("a"), relayChunkSize*2+17))

	n, err := stream.ReadFrom(src)
	if err != nil {
		t.Fatalf("ReadFrom() error = %v", err)
	}
	if n != int64(relayChunkSize*2+17) {
		t.Fatalf("ReadFrom() = %d", n)
	}
	frameReader := bytes.NewReader(wire.Bytes())
	frames := 0
	for frameReader.Len() > 0 {
		frame, err := readMuxFrame(frameReader)
		if err != nil {
			t.Fatalf("readMuxFrame() error = %v", err)
		}
		if frame.Type == muxFrameTypeData {
			frames++
		}
	}
	if got := frames; got != 3 {
		t.Fatalf("data frame count = %d, want 3", got)
	}
}
```

- [ ] **Step 2: Run the targeted relay tests**

Run: `cd go-agent && go test ./internal/relay -run "TestTLSTCPLogicalStreamQueuesInitialDataBeforeOpenResult|TestTLSTCPLogicalStreamReadFromSplitsLargePayloadIntoMuxFrames" -count=1`
Expected: FAIL because chunk helpers and `ReadFrom` do not exist.

- [ ] **Step 3: Add the pooled chunk abstraction**

```go
const relayChunkSize = 128 << 10

type relayChunk struct {
	buf  []byte
	n    int
	pool *sync.Pool
}

func newRelayChunkPool() *sync.Pool {
	pool := &sync.Pool{}
	pool.New = func() any {
		return &relayChunk{buf: make([]byte, relayChunkSize), pool: pool}
	}
	return pool
}

var relayChunkPool = newRelayChunkPool()

func acquireRelayChunk() *relayChunk {
	return relayChunkPool.Get().(*relayChunk)
}

func (c *relayChunk) Bytes() []byte {
	return c.buf[:c.n]
}

func (c *relayChunk) Release() {
	if c == nil || c.pool == nil {
		return
	}
	c.n = 0
	c.pool.Put(c)
}
```

- [ ] **Step 4: Rework mux reads and logical-stream buffering to use chunks**

```go
type muxFrame struct {
	Version  byte
	Type     muxFrameType
	Flags    muxFrameFlags
	StreamID uint32
	Payload  []byte
	Chunk    *relayChunk
}

func readMuxFrameWithPool(r io.Reader, pool *sync.Pool) (muxFrame, error) {
	frame, err := readMuxFrame(r)
	if err != nil {
		return muxFrame{}, err
	}
	if len(frame.Payload) == 0 {
		return frame, nil
	}
	chunk := pool.Get().(*relayChunk)
	chunk.n = copy(chunk.buf, frame.Payload)
	frame.Payload = chunk.Bytes()
	frame.Chunk = chunk
	return frame, nil
}

func releaseMuxFrame(frame muxFrame) {
	if frame.Chunk != nil {
		frame.Chunk.Release()
	}
}

type tlsTCPLogicalStream struct {
	tunnel       *tlsTCPTunnel
	streamID     uint32
	readMu       sync.Mutex
	readChunks   []*relayChunk
	readCh       chan struct{}
	readErr      error
	readErrSet   bool
	openResultCh chan error
}

func (s *tlsTCPLogicalStream) appendDataChunk(chunk *relayChunk) {
	s.readMu.Lock()
	s.readChunks = append(s.readChunks, chunk)
	s.readMu.Unlock()
	s.notifyReadable()
}
```

- [ ] **Step 5: Implement `ReadFrom` and `WriteTo` so `io.Copy` uses the bulk path**

```go
func (s *tlsTCPLogicalStream) ReadFrom(r io.Reader) (int64, error) {
	var total int64
	for {
		chunk := acquireRelayChunk()
		n, err := r.Read(chunk.buf)
		if n > 0 {
			chunk.n = n
			if writeErr := s.writeChunk(context.Background(), chunk); writeErr != nil {
				chunk.Release()
				return total, writeErr
			}
			total += int64(n)
		} else {
			chunk.Release()
		}
		if err == io.EOF {
			return total, nil
		}
		if err != nil {
			return total, err
		}
	}
}

func (s *tlsTCPLogicalStream) writeChunk(ctx context.Context, chunk *relayChunk) error {
	return s.tunnel.writeFrame(ctx, muxFrame{
		Type:     muxFrameTypeData,
		StreamID: s.streamID,
		Payload:  chunk.Bytes(),
	})
}

func (s *tlsTCPLogicalStream) readChunk() (*relayChunk, error) {
	for {
		s.readMu.Lock()
		if len(s.readChunks) > 0 {
			chunk := s.readChunks[0]
			s.readChunks[0] = nil
			s.readChunks = s.readChunks[1:]
			s.readMu.Unlock()
			return chunk, nil
		}
		if s.readErrSet {
			err := s.readErr
			s.readMu.Unlock()
			return nil, err
		}
		s.readMu.Unlock()

		select {
		case <-s.readCh:
		case <-s.tunnel.closed:
			return nil, io.EOF
		}
	}
}

func (s *tlsTCPLogicalStream) WriteTo(w io.Writer) (int64, error) {
	var total int64
	for {
		chunk, err := s.readChunk()
		if err != nil {
			return total, err
		}
		n, writeErr := w.Write(chunk.Bytes())
		total += int64(n)
		chunk.Release()
		if writeErr != nil {
			return total, writeErr
		}
	}
}
```

- [ ] **Step 6: Apply the new open/data flow on both client and server tunnel loops**

```go
func (t *tlsTCPTunnel) readLoop() {
	for {
		frame, err := readMuxFrameWithPool(t.reader, relayChunkPool)
		if err != nil {
			t.failAllStreams(err)
			_ = t.close()
			return
		}
		stream := t.getStream(frame.StreamID)
		if stream == nil {
			releaseMuxFrame(frame)
			continue
		}
		switch frame.Type {
		case muxFrameTypeData:
			stream.appendDataChunk(frame.Chunk)
		case muxFrameTypeOpenResult:
			result, decodeErr := readMuxOpenResultPayload(frame.Payload)
			releaseMuxFrame(frame)
			if decodeErr != nil {
				stream.deliverOpenResult(decodeErr)
				continue
			}
			if !result.OK {
				stream.deliverOpenResult(fmt.Errorf("relay connection failed: %s", result.Error))
				continue
			}
			stream.deliverOpenResult(nil)
		}
	}
}

func (s *serverTLSTCPSession) handleStream(listener Listener, stream *tlsTCPLogicalStream, request relayOpenFrame) {
	upstream, err := s.server.openUpstream(request.Kind, request.Target, request.Chain, DialOptions{})
	if err != nil {
		_ = s.writeOpenResult(stream.streamID, muxOpenResult{OK: false, Error: err.Error()})
		s.tunnel.removeStream(stream.streamID)
		return
	}
	defer upstream.Close()
	if len(request.InitialData) > 0 {
		if _, err := upstream.Write(request.InitialData); err != nil {
			_ = s.writeOpenResult(stream.streamID, muxOpenResult{OK: false, Error: err.Error()})
			return
		}
	}
	_ = s.writeOpenResult(stream.streamID, muxOpenResult{OK: true})
	pipeBothWays(wrapIdleConn(stream), wrapIdleConn(upstream))
}
```

- [ ] **Step 7: Run the focused relay package tests**

Run: `cd go-agent && go test ./internal/relay -run "TestMuxFrameRoundTrip|TestTLSTCPLogicalStream" -count=1`
Expected: PASS.

- [ ] **Step 8: Commit the `tls_tcp` bulk-path slice**

```bash
git add go-agent/internal/relay/chunk_pool.go go-agent/internal/relay/mux_protocol.go go-agent/internal/relay/tls_tcp_session_pool.go go-agent/internal/relay/mux_protocol_test.go go-agent/internal/relay/tls_tcp_session_pool_test.go
git commit -m "feat(relay): add pooled tls tcp stream data path"
```

## Task 3: Remove The L4 First-Byte RTT And Reuse The Path In HTTP

**Files:**
- Modify: `go-agent/internal/l4/server.go`
- Modify: `go-agent/internal/proxy/server.go`
- Test: `go-agent/internal/l4/server_test.go`
- Test: `go-agent/internal/proxy/server_test.go`

- [ ] **Step 1: Write failing L4 and HTTP integration tests**

```go
type l4RelayTestRequest struct {
	Network     string      `json:"network"`
	Target      string      `json:"target"`
	Chain       []relay.Hop `json:"chain,omitempty"`
	InitialData []byte      `json:"initial_data,omitempty"`
}

type relayTestRequest struct {
	Network     string      `json:"network"`
	Target      string      `json:"target"`
	Chain       []relay.Hop `json:"chain,omitempty"`
	InitialData []byte      `json:"initial_data,omitempty"`
}

if _, err := client.Write([]byte("ping")); err != nil {
	t.Fatalf("write relay payload: %v", err)
}

select {
case relayReq := <-relayRequests:
	if got := string(relayReq.InitialData); got != "ping" {
		t.Fatalf("initial relay payload = %q, want %q", got, "ping")
	}
case <-time.After(2 * time.Second):
	t.Fatal("expected initial relay payload to be captured")
}

if got := len(body); got != len(payload) {
	t.Fatalf("body len = %d, want %d", got, len(payload))
}
select {
case relayReq := <-relayAccepted:
	if relayReq.Target != backendURL.Host {
		t.Fatalf("unexpected relay target %q", relayReq.Target)
	}
case <-time.After(2 * time.Second):
	t.Fatal("expected request to traverse relay listener")
}
```

- [ ] **Step 2: Run the focused L4 and HTTP tests**

Run: `cd go-agent && go test ./internal/l4 ./internal/proxy -run "TestTCPRelayProxySendsInitialPayloadInOpenFrame|TestStartStreamsLargeHTTPDownloadThroughRelayChainUsesSharedTLSTCPFastPath" -count=1`
Expected: FAIL because L4 does not prefetch downstream bytes and HTTP still only uses the old generic relay dial path.

- [ ] **Step 3: Prefetch the first downstream TCP bytes before relay dial**

```go
const relayPrefetchBytes = 32 << 10

func prefetchDownstream(r io.Reader) ([]byte, io.Reader, error) {
	buf := make([]byte, relayPrefetchBytes)
	n, err := r.Read(buf)
	if n > 0 {
		return append([]byte(nil), buf[:n]...), io.MultiReader(bytes.NewReader(buf[:n]), r), nil
	}
	if err == io.EOF {
		return nil, r, nil
	}
	return nil, r, err
}
```

- [ ] **Step 4: Pass the prefetched bytes into `relay.DialOptions` and preserve proxy-protocol behavior**

```go
downstreamSource, downstreamProxyInfo, err := s.prepareTCPDownstream(client, rule)
if err != nil {
	return
}
initialPayload, replayReader, err := prefetchDownstream(downstreamSource)
if err != nil {
	return
}
downstreamSource = replayReader

upstream, candidate, connectDuration, err := s.dialTCPUpstream(rule, relay.DialOptions{
	InitialPayload: initialPayload,
})
```

- [ ] **Step 5: Keep HTTP transport wiring on the same optimized relay path**

```go
transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
	return relay.Dial(ctx, network, dialAddressFromContext(ctx, addr), hops, provider, relay.DialOptions{})
}
```

- [ ] **Step 6: Run the affected package tests**

Run: `cd go-agent && go test ./internal/l4 ./internal/proxy -run "TestTCPRelayProxy|TestStartServesHTTPRulesThroughRelayChain|TestStartStreamsLargeHTTPDownloadThroughRelayChainWithObfsMode|TestTCPRelayProxySendsInitialPayloadInOpenFrame" -count=1`
Expected: PASS.

- [ ] **Step 7: Commit the L4/HTTP integration slice**

```bash
git add go-agent/internal/l4/server.go go-agent/internal/proxy/server.go go-agent/internal/l4/server_test.go go-agent/internal/proxy/server_test.go
git commit -m "feat(l4): remove relay first byte latency"
```

## Task 4: Tune UDP Over `tls_tcp` And QUIC Relay Paths

**Files:**
- Modify: `go-agent/internal/relay/uot.go`
- Modify: `go-agent/internal/relay/quic_runtime.go`
- Modify: `go-agent/internal/relay/runtime.go`
- Test: `go-agent/internal/relay/runtime_test.go`
- Test: `go-agent/internal/relay/quic_runtime_test.go`
- Test: `go-agent/internal/l4/server_test.go`

- [ ] **Step 1: Write failing UDP and QUIC regression tests**

```go
func TestUDPRelayRoundTripReusesPacketBuffers(t *testing.T) {
	var buf bytes.Buffer

	payload := bytes.Repeat([]byte("u"), 1400)
	if err := WriteUOTPacket(&buf, payload); err != nil {
		t.Fatalf("WriteUOTPacket() error = %v", err)
	}
	chunk, err := readUOTPacketIntoChunk(&buf)
	if err != nil {
		t.Fatalf("readUOTPacketIntoChunk() error = %v", err)
	}
	if got := string(chunk.Bytes()); got != string(payload) {
		t.Fatalf("payload = %q, want %q", got, payload)
	}
	chunk.Release()
}

func TestDialQUICWithInitialPayloadKeepsRoundTripWorking(t *testing.T) {
	provider := &fakeTLSMaterialProvider{}
	listener, hop := newRelayEndpoint(t, provider, 1, "relay-quic-initial", "pin_only", true, false)
	listener.TransportMode = "quic"
	target, cleanup := startTCPEchoServer(t)
	defer cleanup()

	server, err := Start(context.Background(), []Listener{listener}, provider)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer server.Close()

	conn, err := Dial(context.Background(), "tcp", target, []Hop{hop}, provider, DialOptions{InitialPayload: []byte("hello")})
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()
}
```

- [ ] **Step 2: Run the focused UDP and QUIC tests**

Run: `cd go-agent && go test ./internal/relay ./internal/l4 -run "TestUDPRelayRoundTripReusesPacketBuffers|TestDialQUICWithInitialPayloadKeepsRoundTripWorking|TestUDPRelayOverTLSTCPWithRelayRuntime|TestUDPRelayOverQUIC" -count=1`
Expected: FAIL because packet-buffer reuse helpers and QUIC dial-option support do not exist.

- [ ] **Step 3: Reuse pooled buffers in the UOT packet helpers**

```go
func readUOTPacketIntoChunk(r io.Reader) (*relayChunk, error) {
	var header [2]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, err
	}
	size := int(binary.BigEndian.Uint16(header[:]))
	chunk := acquireRelayChunk()
	chunk.n = size
	if _, err := io.ReadFull(r, chunk.buf[:size]); err != nil {
		chunk.Release()
		return nil, err
	}
	return chunk, nil
}
```

- [ ] **Step 4: Keep QUIC dial/request handling aligned with the new open frame**

```go
func dialQUIC(ctx context.Context, network, target string, chain []Hop, provider TLSMaterialProvider, options DialOptions) (net.Conn, error) {
	firstHop := chain[0]
	tlsConfig, err := clientQUICTLSConfig(ctx, provider, firstHop.Listener, firstHop.Address, firstHop.ServerName)
	if err != nil {
		return nil, err
	}
	sessionKey, err := quicSessionPoolKey(firstHop)
	if err != nil {
		return nil, err
	}
	session, stream, err := openQUICStream(ctx, sessionKey, func(dialCtx context.Context) (*quic.Conn, error) {
		return quicDialAddr(dialCtx, firstHop.Address, tlsConfig, newRelayQUICConfig())
	})
	if err != nil {
		return nil, err
	}
	request := relayOpenFrame{
		Kind:        network,
		Target:      target,
		Chain:       append([]Hop(nil), chain[1:]...),
		InitialData: options.InitialPayload,
	}
	conn := &quicStreamConn{conn: session, stream: stream}
	if err := withFrameDeadline(conn, func() error { return writeRelayOpenFrame(conn, request) }); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

func (s *Server) handleQUICStream(conn *quic.Conn, stream *quic.Stream, listener Listener) {
	clientConn := &quicStreamConn{conn: conn, stream: stream}
	var request relayOpenFrame
	if err := withFrameDeadline(clientConn, func() error {
		var readErr error
		request, readErr = readRelayOpenFrame(clientConn)
		return readErr
	}); err != nil {
		return
	}
	upstream, err := s.openUpstream(request.Kind, request.Target, request.Chain, DialOptions{})
	if err != nil {
		_ = withFrameDeadline(clientConn, func() error {
			return writeRelayResponse(clientConn, relayResponse{OK: false, Error: err.Error()})
		})
		return
	}
	if len(request.InitialData) > 0 {
		if _, err := upstream.Write(request.InitialData); err != nil {
			_ = withFrameDeadline(clientConn, func() error {
				return writeRelayResponse(clientConn, relayResponse{OK: false, Error: err.Error()})
			})
			return
		}
	}
	pipeBothWays(wrapIdleConn(clientConn), wrapIdleConn(upstream))
}
```

- [ ] **Step 5: Run the transport regression tests**

Run: `cd go-agent && go test ./internal/relay ./internal/l4 -run "TestOneHopRelayUDPDataFlow|TestMultiHopRelayUDPDataFlow|TestDialQUICRoundTripTCP|TestUDPRelayOverQUIC|TestUDPRelayOverTLSTCPWithRelayRuntime" -count=1`
Expected: PASS.

- [ ] **Step 6: Commit the UDP/QUIC slice**

```bash
git add go-agent/internal/relay/uot.go go-agent/internal/relay/quic_runtime.go go-agent/internal/relay/runtime.go go-agent/internal/relay/runtime_test.go go-agent/internal/relay/quic_runtime_test.go go-agent/internal/l4/server_test.go
git commit -m "feat(relay): tune udp and quic data paths"
```

## Task 5: Add Benchmarks And Run Full Verification

**Files:**
- Create: `go-agent/internal/relay/perf_bench_test.go`
- Modify: `README.md`

- [ ] **Step 1: Add relay benchmarks for bulk TCP and UDP**

```go
type noopDeadlineConn struct{ net.Conn }

func (noopDeadlineConn) SetDeadline(time.Time) error      { return nil }
func (noopDeadlineConn) SetReadDeadline(time.Time) error  { return nil }
func (noopDeadlineConn) SetWriteDeadline(time.Time) error { return nil }

func BenchmarkTLSTCPLogicalStreamReadFrom1MiB(b *testing.B) {
	payload := bytes.Repeat([]byte("x"), 1<<20)
	for i := 0; i < b.N; i++ {
		var wire bytes.Buffer
		tunnel := &tlsTCPTunnel{rawConn: noopDeadlineConn{}, writer: &wire, streams: map[uint32]*tlsTCPLogicalStream{}, closed: make(chan struct{})}
		stream := &tlsTCPLogicalStream{tunnel: tunnel, streamID: 1, readCh: make(chan struct{}, 1)}
		if _, err := stream.ReadFrom(bytes.NewReader(payload)); err != nil {
			b.Fatalf("ReadFrom() error = %v", err)
		}
	}
}

func BenchmarkUOTPacketRoundTrip1400B(b *testing.B) {
	payload := bytes.Repeat([]byte("u"), 1400)
	for i := 0; i < b.N; i++ {
		var buf bytes.Buffer
		if err := WriteUOTPacket(&buf, payload); err != nil {
			b.Fatalf("WriteUOTPacket() error = %v", err)
		}
		if _, err := ReadUOTPacket(&buf); err != nil {
			b.Fatalf("ReadUOTPacket() error = %v", err)
		}
	}
}
```

- [ ] **Step 2: Document the verification commands in `README.md`**

```md
### Relay performance spot checks

- `cd go-agent && go test ./internal/relay -bench "TLSTCPLogicalStream|UOTPacket" -benchmem -run ^$`
- `cd go-agent && go test ./internal/l4 ./internal/proxy ./internal/relay`
- `docker build -t nginx-reverse-emby .`
- `docker compose up -d`
```

- [ ] **Step 3: Run the relay benchmarks once for a baseline artifact**

Run: `cd go-agent && go test ./internal/relay -bench "TLSTCPLogicalStream|UOTPacket" -benchmem -run ^$`
Expected: benchmark output for the new bulk-path and packet-path helpers.

- [ ] **Step 4: Run the full Go test suites touched by the rewrite**

Run: `cd go-agent && go test ./...`
Expected: PASS.

- [ ] **Step 5: Run the image build and compose validation**

Run: `docker build -t nginx-reverse-emby .`
Expected: successful multi-stage image build.

Run: `docker compose up -d`
Expected: relay-enabled stack starts without new configuration errors.

- [ ] **Step 6: Commit the verification slice**

```bash
git add go-agent/internal/relay/perf_bench_test.go README.md
git commit -m "test(relay): add relay performance benchmarks"
```

## Spec Coverage Check

- `OPEN + initial_data` is covered by Task 1 and Task 2.
- `tls_tcp` pooled large-chunk transfer is covered by Task 2.
- `L4 TCP` first-byte RTT removal is covered by Task 3.
- `HTTP` reuse of the improved `tls_tcp` path is covered by Task 3.
- `UDP over tls_tcp` packet-preserving optimization is covered by Task 4.
- `TCP/UDP over QUIC` follow-up tuning is covered by Task 4.
- Performance verification and Docker validation are covered by Task 5.

## Self-Review

- Placeholder scan: no `TODO`, `TBD`, or “similar to above” references remain.
- Type consistency: `DialOptions.InitialPayload`, `relayOpenFrame.InitialData`, and the chunk helper names are used consistently across tasks.
- Scope check: the plan keeps the highest-value `tls_tcp / L4 TCP` work first, then extends into HTTP, UDP, and QUIC in separate slices.
