package relay

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/upstream"
)

func TestTLSTCPLogicalStreamReadConsumesQueuedChunksInOrder(t *testing.T) {
	stream := &tlsTCPLogicalStream{readCh: make(chan struct{}, 1)}
	stream.appendData([]byte("hello"))
	stream.appendData([]byte("world"))
	if got := len(stream.readChunks); got != 2 {
		t.Fatalf("len(readChunks) = %d, want 2", got)
	}

	buf := make([]byte, 7)
	n, err := stream.Read(buf)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if got := string(buf[:n]); got != "hellowo" {
		t.Fatalf("Read() = %q, want %q", got, "hellowo")
	}
	if got := len(stream.readChunks); got != 1 {
		t.Fatalf("len(readChunks) after first read = %d, want 1", got)
	}

	buf = make([]byte, 3)
	n, err = stream.Read(buf)
	if err != nil {
		t.Fatalf("Read() second error = %v", err)
	}
	if got := string(buf[:n]); got != "rld" {
		t.Fatalf("Read() second = %q, want %q", got, "rld")
	}
	if got := len(stream.readChunks); got != 0 {
		t.Fatalf("len(readChunks) after second read = %d, want 0", got)
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

func TestTLSTCPLogicalStreamReadDoesNotReturnZeroNilForEmptyDataFrame(t *testing.T) {
	stream := &tlsTCPLogicalStream{readCh: make(chan struct{}, 1)}
	stream.appendData(nil)
	stream.setReadError(io.EOF)

	buf := make([]byte, 1)
	n, err := stream.Read(buf)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Read() error = %v, want EOF", err)
	}
	if n != 0 {
		t.Fatalf("Read() n = %d, want 0", n)
	}
}

func TestTLSTCPLogicalStreamReadFromSplitsLargePayloadIntoMuxFrames(t *testing.T) {
	var wire bytes.Buffer
	tunnel := &tlsTCPTunnel{
		rawConn:    noopDeadlineConn{},
		writer:     &wire,
		closeOuter: func() error { return nil },
		streams:    make(map[uint32]*tlsTCPLogicalStream),
		closed:     make(chan struct{}),
	}
	stream := &tlsTCPLogicalStream{
		tunnel:       tunnel,
		streamID:     7,
		readCh:       make(chan struct{}, 1),
		openResultCh: make(chan muxOpenResult, 1),
	}
	src := bytes.NewReader(bytes.Repeat([]byte("a"), 150000))

	n, err := stream.ReadFrom(src)
	if err != nil {
		t.Fatalf("ReadFrom() error = %v", err)
	}
	if n != 150000 {
		t.Fatalf("ReadFrom() = %d, want %d", n, 150000)
	}

	frameReader := bytes.NewReader(wire.Bytes())
	frames := 0
	var payload bytes.Buffer
	for frameReader.Len() > 0 {
		frame, err := readMuxFrame(frameReader)
		if err != nil {
			t.Fatalf("readMuxFrame() error = %v", err)
		}
		if frame.Type != muxFrameTypeData {
			t.Fatalf("frame.Type = %v, want %v", frame.Type, muxFrameTypeData)
		}
		frames++
		payload.Write(frame.Payload)
	}
	if frames < 2 {
		t.Fatalf("data frame count = %d, want at least 2", frames)
	}
	if got := payload.Len(); got != 150000 {
		t.Fatalf("payload len = %d, want %d", got, 150000)
	}
}

func TestTLSTCPLogicalStreamReadFromFitsSmallPayloadIntoSingleMuxFrame(t *testing.T) {
	var wire bytes.Buffer
	tunnel := &tlsTCPTunnel{
		rawConn:    noopDeadlineConn{},
		writer:     &wire,
		closeOuter: func() error { return nil },
		streams:    make(map[uint32]*tlsTCPLogicalStream),
		closed:     make(chan struct{}),
	}
	stream := &tlsTCPLogicalStream{
		tunnel:       tunnel,
		streamID:     8,
		readCh:       make(chan struct{}, 1),
		openResultCh: make(chan muxOpenResult, 1),
	}
	src := bytes.NewReader(bytes.Repeat([]byte("b"), 60000))

	n, err := stream.ReadFrom(src)
	if err != nil {
		t.Fatalf("ReadFrom() error = %v", err)
	}
	if n != 60000 {
		t.Fatalf("ReadFrom() = %d, want %d", n, 60000)
	}

	frameReader := bytes.NewReader(wire.Bytes())
	frames := 0
	for frameReader.Len() > 0 {
		frame, err := readMuxFrame(frameReader)
		if err != nil {
			t.Fatalf("readMuxFrame() error = %v", err)
		}
		if frame.Type != muxFrameTypeData {
			t.Fatalf("frame.Type = %v, want %v", frame.Type, muxFrameTypeData)
		}
		frames++
	}
	if frames != 1 {
		t.Fatalf("data frame count = %d, want 1", frames)
	}
}

func TestTLSTCPLogicalStreamReadFromDoesNotWaitToCoalesceImmediateSourceChunks(t *testing.T) {
	var wire bytes.Buffer
	tunnel := &tlsTCPTunnel{
		rawConn:    noopDeadlineConn{},
		writer:     &wire,
		closeOuter: func() error { return nil },
		streams:    make(map[uint32]*tlsTCPLogicalStream),
		closed:     make(chan struct{}),
	}
	stream := &tlsTCPLogicalStream{
		tunnel:       tunnel,
		streamID:     10,
		readCh:       make(chan struct{}, 1),
		openResultCh: make(chan muxOpenResult, 1),
	}
	src := &idleDeadlineConn{
		Conn: &markingConn{
			chunks: [][]byte{
				bytes.Repeat([]byte("c"), 16*1024),
				bytes.Repeat([]byte("d"), 16*1024),
				bytes.Repeat([]byte("e"), 16*1024),
				bytes.Repeat([]byte("f"), 16*1024),
			},
		},
		timeout: time.Minute,
	}

	n, err := stream.ReadFrom(src)
	if err != nil {
		t.Fatalf("ReadFrom() error = %v", err)
	}
	if n != 64*1024 {
		t.Fatalf("ReadFrom() = %d, want %d", n, 64*1024)
	}

	frameReader := bytes.NewReader(wire.Bytes())
	frames := 0
	for frameReader.Len() > 0 {
		frame, err := readMuxFrame(frameReader)
		if err != nil {
			t.Fatalf("readMuxFrame() error = %v", err)
		}
		if frame.Type != muxFrameTypeData {
			t.Fatalf("frame.Type = %v, want %v", frame.Type, muxFrameTypeData)
		}
		frames++
	}
	if frames != 4 {
		t.Fatalf("data frame count = %d, want 4", frames)
	}
}

func TestTLSTCPTunnelWriteFrameReusesRecentWriteDeadline(t *testing.T) {
	withRelayTimeouts(time.Second, time.Second, time.Second, time.Second, func() {
		conn := &countingDeadlineConn{}
		tunnel := &tlsTCPTunnel{
			rawConn:    conn,
			writer:     conn,
			closeOuter: func() error { return nil },
			streams:    make(map[uint32]*tlsTCPLogicalStream),
			closed:     make(chan struct{}),
		}

		for i := 0; i < 2; i++ {
			if err := tunnel.writeFrame(context.Background(), muxFrame{
				Type:     muxFrameTypeData,
				StreamID: uint32(i + 1),
				Payload:  []byte("payload"),
			}); err != nil {
				t.Fatalf("writeFrame(%d) error = %v", i, err)
			}
		}

		if conn.writeDeadlineCalls != 1 {
			t.Fatalf("SetWriteDeadline calls = %d, want 1", conn.writeDeadlineCalls)
		}
	})
}

func TestTLSTCPLogicalStreamReadFromSingleStreamQueuesAheadOfSlowWriter(t *testing.T) {
	writer := newBlockingFirstWrite()
	tunnel := &tlsTCPTunnel{
		rawConn:    noopDeadlineConn{},
		writer:     writer,
		closeOuter: func() error { return nil },
		streams:    make(map[uint32]*tlsTCPLogicalStream),
		closed:     make(chan struct{}),
	}
	tunnel.startWritePump()
	stream := &tlsTCPLogicalStream{
		tunnel:       tunnel,
		streamID:     12,
		readCh:       make(chan struct{}, 1),
		openResultCh: make(chan muxOpenResult, 1),
	}
	tunnel.registerStream(stream)

	src := &countingChunkConn{
		chunks: [][]byte{
			bytes.Repeat([]byte("a"), 16*1024),
			bytes.Repeat([]byte("b"), 16*1024),
			bytes.Repeat([]byte("c"), 16*1024),
			bytes.Repeat([]byte("d"), 16*1024),
			bytes.Repeat([]byte("e"), 16*1024),
		},
	}
	done := make(chan error, 1)
	go func() {
		_, err := stream.ReadFrom(&idleDeadlineConn{Conn: src, timeout: time.Minute})
		done <- err
	}()

	<-writer.started
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if src.readCalls >= 4 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if src.readCalls < 4 {
		close(writer.release)
		<-done
		t.Fatalf("source read calls = %d, want at least 4 queued frames before backpressure", src.readCalls)
	}

	close(writer.release)
	if err := <-done; err != nil {
		t.Fatalf("ReadFrom() error = %v", err)
	}
}

func TestTLSTCPLogicalStreamReadReleasesConsumedChunk(t *testing.T) {
	released := 0
	stream := &tlsTCPLogicalStream{
		readCh: make(chan struct{}, 1),
		readChunks: []tlsTCPReadChunk{{
			payload: []byte("payload"),
			release: func() { released++ },
		}},
	}

	buf := make([]byte, len("payload"))
	n, err := stream.Read(buf)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if got := string(buf[:n]); got != "payload" {
		t.Fatalf("Read() = %q, want %q", got, "payload")
	}
	if released != 1 {
		t.Fatalf("release calls = %d, want 1", released)
	}
}

func TestTLSTCPLogicalStreamWriteToReleasesConsumedChunk(t *testing.T) {
	released := 0
	stream := &tlsTCPLogicalStream{
		tunnel: &tlsTCPTunnel{
			closed: make(chan struct{}),
		},
		readCh: make(chan struct{}, 1),
		readChunks: []tlsTCPReadChunk{{
			payload: []byte("payload"),
			release: func() { released++ },
		}},
		readErr:    io.EOF,
		readErrSet: true,
	}

	var dst bytes.Buffer
	n, err := stream.WriteTo(&dst)
	if err != nil {
		t.Fatalf("WriteTo() error = %v", err)
	}
	if n != int64(len("payload")) {
		t.Fatalf("WriteTo() = %d, want %d", n, len("payload"))
	}
	if got := dst.String(); got != "payload" {
		t.Fatalf("WriteTo() payload = %q, want %q", got, "payload")
	}
	if released != 1 {
		t.Fatalf("release calls = %d, want 1", released)
	}
}

func TestIdleDeadlineConnCopyToWrappedTLSTCPStreamUsesReadFromFastPath(t *testing.T) {
	var wire bytes.Buffer
	tunnel := &tlsTCPTunnel{
		rawConn:    noopDeadlineConn{},
		writer:     &wire,
		closeOuter: func() error { return nil },
		streams:    make(map[uint32]*tlsTCPLogicalStream),
		closed:     make(chan struct{}),
	}
	stream := &tlsTCPLogicalStream{
		tunnel:       tunnel,
		streamID:     9,
		readCh:       make(chan struct{}, 1),
		openResultCh: make(chan muxOpenResult, 1),
	}
	source := &markingConn{
		onRead: func() {
			stream.readMu.Lock()
			stream.writeClosed = true
			stream.readMu.Unlock()
		},
		chunks: [][]byte{[]byte("fast-path-payload")},
	}

	n, err := io.Copy(&idleDeadlineConn{Conn: stream, timeout: time.Minute}, &idleDeadlineConn{Conn: source, timeout: time.Minute})
	if err != nil {
		t.Fatalf("io.Copy() error = %v", err)
	}
	if n != int64(len("fast-path-payload")) {
		t.Fatalf("io.Copy() = %d, want %d", n, len("fast-path-payload"))
	}

	frame, err := readMuxFrame(bytes.NewReader(wire.Bytes()))
	if err != nil {
		t.Fatalf("readMuxFrame() error = %v", err)
	}
	if got := string(frame.Payload); got != "fast-path-payload" {
		t.Fatalf("frame payload = %q, want %q", got, "fast-path-payload")
	}
}

func TestTLSTCPLogicalStreamWriteToDrainsQueuedChunks(t *testing.T) {
	stream := &tlsTCPLogicalStream{readCh: make(chan struct{}, 1)}
	stream.appendData([]byte("hello"))
	stream.appendData([]byte("world"))
	stream.setReadError(io.EOF)

	var dst bytes.Buffer
	n, err := stream.WriteTo(&dst)
	if err != nil {
		t.Fatalf("WriteTo() error = %v", err)
	}
	if n != int64(len("helloworld")) {
		t.Fatalf("WriteTo() = %d, want %d", n, len("helloworld"))
	}
	if got := dst.String(); got != "helloworld" {
		t.Fatalf("WriteTo() payload = %q, want %q", got, "helloworld")
	}
}

func TestTLSTCPLogicalStreamWriteToDoesNotHoldReadMuWhileWriting(t *testing.T) {
	stream := &tlsTCPLogicalStream{
		tunnel: &tlsTCPTunnel{
			closed: make(chan struct{}),
		},
		readCh: make(chan struct{}, 1),
	}
	stream.appendData([]byte("blocked"))
	writer := newBlockingFirstWrite()
	done := make(chan error, 1)

	go func() {
		_, err := stream.WriteTo(writer)
		done <- err
	}()

	<-writer.started
	appendDone := make(chan struct{})
	go func() {
		stream.appendData([]byte("next"))
		close(appendDone)
	}()

	select {
	case <-appendDone:
	case <-time.After(100 * time.Millisecond):
		close(writer.release)
		<-appendDone
		stream.setReadError(io.EOF)
		<-done
		t.Fatal("appendData blocked while WriteTo was writing to a slow destination")
	}

	stream.setReadError(io.EOF)
	close(writer.release)
	if err := <-done; err != nil {
		t.Fatalf("WriteTo() error = %v", err)
	}
	if got := writer.String(); got != "blockednext" {
		t.Fatalf("WriteTo() payload = %q, want %q", got, "blockednext")
	}
}

func TestTLSTCPLogicalStreamAppendDataChunkBackpressuresSlowReader(t *testing.T) {
	withTLSTCPBufferedReadLimitForTest(8, func() {
		stream := &tlsTCPLogicalStream{
			tunnel: &tlsTCPTunnel{
				closed: make(chan struct{}),
			},
			readCh: make(chan struct{}, 1),
		}

		stream.appendData([]byte("1234"))
		stream.appendData([]byte("5678"))

		blockedAppendDone := make(chan struct{})
		go func() {
			stream.appendData([]byte("abcd"))
			close(blockedAppendDone)
		}()

		select {
		case <-blockedAppendDone:
			t.Fatal("appendData() completed before queued bytes were drained")
		case <-time.After(50 * time.Millisecond):
		}

		buf := make([]byte, 4)
		n, err := stream.Read(buf)
		if err != nil {
			t.Fatalf("Read() error = %v", err)
		}
		if got := string(buf[:n]); got != "1234" {
			t.Fatalf("Read() = %q, want %q", got, "1234")
		}

		select {
		case <-blockedAppendDone:
		case <-time.After(200 * time.Millisecond):
			t.Fatal("appendData() did not resume after queued bytes were drained")
		}
	})
}

func TestTLSTCPSessionPoolStripesBusySessions(t *testing.T) {
	pool := newTLSTCPSessionPool()
	dials := 0
	var releases []func()
	defer func() {
		for _, release := range releases {
			release()
		}
		for _, tunnel := range pool.allTunnelsForTest() {
			_ = tunnel.close()
		}
	}()

	for i := 0; i < 5; i++ {
		tunnel, release, err := pool.getOrDial(context.Background(), "relay-key", upstream.TrafficClassUnknown, func(context.Context) (*tlsTCPTunnel, error) {
			dials++
			return &tlsTCPTunnel{
				key:        "relay-key",
				rawConn:    noopDeadlineConn{},
				closeOuter: func() error { return nil },
				streams:    make(map[uint32]*tlsTCPLogicalStream),
				closed:     make(chan struct{}),
			}, nil
		})
		if err != nil {
			t.Fatalf("getOrDial(%d) error = %v", i, err)
		}
		if tunnel == nil {
			t.Fatalf("getOrDial(%d) tunnel = nil", i)
		}
		releases = append(releases, release)
	}

	if dials < 3 {
		t.Fatalf("dials = %d, want at least 3 busy striped sessions", dials)
	}
}

func TestTLSTCPTunnelRejectsInteractiveAdmissionWhenCongested(t *testing.T) {
	tunnel := &tlsTCPTunnel{
		writeReqCh: make(chan *tlsTCPWriteRequest, 8),
		closed:     make(chan struct{}),
	}
	tunnel.queuedWrites.Store(5)
	tunnel.bufferedBytes.Store(600 << 10)

	if tunnel.canAcceptTrafficClass(upstream.TrafficClassInteractive) {
		t.Fatal("canAcceptTrafficClass(interactive) = true, want false")
	}
	if !tunnel.canAcceptTrafficClass(upstream.TrafficClassBulk) {
		t.Fatal("canAcceptTrafficClass(bulk) = false, want true")
	}
}

func TestTLSTCPSessionPoolAvoidsCongestedTunnelForInteractiveTraffic(t *testing.T) {
	pool := newTLSTCPSessionPool()
	congested := &tlsTCPTunnel{
		key:        "relay-key",
		rawConn:    noopDeadlineConn{},
		closeOuter: func() error { return nil },
		streams:    make(map[uint32]*tlsTCPLogicalStream),
		closed:     make(chan struct{}),
	}
	idle := &tlsTCPTunnel{
		key:        "relay-key",
		rawConn:    noopDeadlineConn{},
		closeOuter: func() error { return nil },
		streams:    make(map[uint32]*tlsTCPLogicalStream),
		closed:     make(chan struct{}),
	}
	congested.queuedWrites.Store(5)
	congested.bufferedBytes.Store(600 << 10)
	pool.sessions["relay-key"] = []*tlsTCPTunnel{congested, idle}

	selected, release, err := pool.getOrDial(context.Background(), "relay-key", upstream.TrafficClassInteractive, func(context.Context) (*tlsTCPTunnel, error) {
		t.Fatal("unexpected dial for available non-congested tunnel")
		return nil, nil
	})
	if err != nil {
		t.Fatalf("getOrDial() error = %v", err)
	}
	defer release()

	if selected != idle {
		t.Fatalf("selected tunnel = %p, want idle tunnel %p", selected, idle)
	}
}

func TestTLSTCPSessionPoolReusesLeastLoadedInteractiveWhenCappedTunnelsAreCongested(t *testing.T) {
	pool := newTLSTCPSessionPool()
	var leastLoaded *tlsTCPTunnel
	for i := 0; i < tlsTCPMuxSessionsPerKey; i++ {
		tunnel := &tlsTCPTunnel{
			key:        "relay-key",
			rawConn:    noopDeadlineConn{},
			closeOuter: func() error { return nil },
			streams:    make(map[uint32]*tlsTCPLogicalStream),
			closed:     make(chan struct{}),
		}
		tunnel.queuedWrites.Store(tlsTCPInteractiveAdmissionQueuedWrites + int64(i))
		tunnel.bufferedBytes.Store(tlsTCPInteractiveAdmissionBufferedBytes + int64(i))
		pool.sessions["relay-key"] = append(pool.sessions["relay-key"], tunnel)
		if i == 0 {
			leastLoaded = tunnel
		}
	}

	selected, release, err := pool.getOrDial(context.Background(), "relay-key", upstream.TrafficClassInteractive, func(context.Context) (*tlsTCPTunnel, error) {
		t.Fatal("unexpected dial beyond session cap")
		return nil, nil
	})
	if err != nil {
		t.Fatalf("getOrDial() error = %v", err)
	}
	if selected != leastLoaded {
		t.Fatalf("selected tunnel = %p, want least-loaded tunnel %p", selected, leastLoaded)
	}
	if release != nil {
		release()
	}
}

func TestTLSTCPSessionPoolAllowsUnknownWhenCappedTunnelsAreCongested(t *testing.T) {
	pool := newTLSTCPSessionPool()
	for i := 0; i < tlsTCPMuxSessionsPerKey; i++ {
		tunnel := &tlsTCPTunnel{
			key:        "relay-key",
			rawConn:    noopDeadlineConn{},
			closeOuter: func() error { return nil },
			streams:    make(map[uint32]*tlsTCPLogicalStream),
			closed:     make(chan struct{}),
		}
		tunnel.queuedWrites.Store(tlsTCPInteractiveAdmissionQueuedWrites)
		tunnel.bufferedBytes.Store(tlsTCPInteractiveAdmissionBufferedBytes)
		pool.sessions["relay-key"] = append(pool.sessions["relay-key"], tunnel)
	}

	selected, release, err := pool.getOrDial(context.Background(), "relay-key", upstream.TrafficClassUnknown, func(context.Context) (*tlsTCPTunnel, error) {
		t.Fatal("unexpected dial beyond session cap")
		return nil, nil
	})
	if err != nil {
		t.Fatalf("getOrDial() error = %v", err)
	}
	if selected == nil {
		t.Fatal("selected tunnel = nil, want existing tunnel reuse for unknown class")
	}
	if release != nil {
		release()
	}
}

func TestWireGuardSessionPoolKeyIncludesTLSMaterial(t *testing.T) {
	profileID := 9
	certID := 10
	base := Hop{
		Address:    "10.0.0.2:7443",
		ServerName: "relay-a.example.com",
		Listener: Listener{
			ID:                 1,
			AgentID:            "agent-a",
			Revision:           3,
			TransportMode:      ListenerTransportModeWireGuard,
			WireGuardProfileID: &profileID,
			CertificateID:      &certID,
			TLSMode:            "pin_only",
			PinSet: []model.RelayPin{{
				Type:  "spki_sha256",
				Value: "pin-a",
			}},
			TrustedCACertificateIDs: []int{100},
		},
	}
	changedTLSMaterial := base
	changedTLSMaterial.ServerName = "relay-b.example.com"
	changedTLSMaterial.Listener.CertificateID = nil
	changedTLSMaterial.Listener.TLSMode = "ca_only"
	changedTLSMaterial.Listener.PinSet = []model.RelayPin{{
		Type:  "spki_sha256",
		Value: "pin-b",
	}}
	changedTLSMaterial.Listener.TrustedCACertificateIDs = []int{200, 300}

	keyA, err := tlsTCPSessionPoolKey(base, "socks5://127.0.0.1:1080")
	if err != nil {
		t.Fatalf("tlsTCPSessionPoolKey(base) error = %v", err)
	}
	keyB, err := tlsTCPSessionPoolKey(changedTLSMaterial, "socks5://127.0.0.1:1080")
	if err != nil {
		t.Fatalf("tlsTCPSessionPoolKey(changedTLSMaterial) error = %v", err)
	}
	if keyA == keyB {
		t.Fatalf("WireGuard session key did not change for TLS material:\n%s", keyA)
	}

	changedAddress := base
	changedAddress.Address = "10.0.0.3:7443"
	keyC, err := tlsTCPSessionPoolKey(changedAddress, "socks5://127.0.0.1:1080")
	if err != nil {
		t.Fatalf("tlsTCPSessionPoolKey(changedAddress) error = %v", err)
	}
	if keyA == keyC {
		t.Fatal("WireGuard session key did not change when address changed")
	}

	changedProfile := base
	nextProfileID := 10
	changedProfile.Listener.WireGuardProfileID = &nextProfileID
	keyD, err := tlsTCPSessionPoolKey(changedProfile, "socks5://127.0.0.1:1080")
	if err != nil {
		t.Fatalf("tlsTCPSessionPoolKey(changedProfile) error = %v", err)
	}
	if keyA == keyD {
		t.Fatal("WireGuard session key did not change when profile changed")
	}

	changedAgent := base
	changedAgent.Listener.AgentID = "agent-b"
	keyE, err := tlsTCPSessionPoolKey(changedAgent, "socks5://127.0.0.1:1080")
	if err != nil {
		t.Fatalf("tlsTCPSessionPoolKey(changedAgent) error = %v", err)
	}
	if keyA == keyE {
		t.Fatal("WireGuard session key did not change when agent changed")
	}
}

func TestTLSTCPTunnelKeepsCongestionCountersUntilBlockedWriteFinishes(t *testing.T) {
	writer := newBlockingFirstWrite()
	tunnel := &tlsTCPTunnel{
		rawConn:    noopDeadlineConn{},
		writer:     writer,
		closeOuter: func() error { return nil },
		streams:    make(map[uint32]*tlsTCPLogicalStream),
		closed:     make(chan struct{}),
	}

	payload := bytes.Repeat([]byte("x"), tlsTCPInteractiveAdmissionBufferedBytes)
	req, err := tunnel.enqueueWriteFrame(context.Background(), muxFrame{
		Type:     muxFrameTypeData,
		StreamID: 1,
		Payload:  payload,
	})
	if err != nil {
		t.Fatalf("enqueueWriteFrame() error = %v", err)
	}

	<-writer.started
	if tunnel.canAcceptTrafficClass(upstream.TrafficClassInteractive) {
		close(writer.release)
		_ = waitTLSTCPWriteRequest(context.Background(), req, tunnel)
		t.Fatal("canAcceptTrafficClass(interactive) = true while write batch is blocked")
	}

	close(writer.release)
	if err := waitTLSTCPWriteRequest(context.Background(), req, tunnel); err != nil {
		t.Fatalf("waitTLSTCPWriteRequest() error = %v", err)
	}
	if tunnel.queuedWrites.Load() != 0 {
		t.Fatalf("queuedWrites = %d, want 0 after write completes", tunnel.queuedWrites.Load())
	}
	if tunnel.bufferedBytes.Load() != 0 {
		t.Fatalf("bufferedBytes = %d, want 0 after write completes", tunnel.bufferedBytes.Load())
	}
}

func TestTLSTCPLogicalStreamReadFromReusesWriteRequests(t *testing.T) {
	payload := bytes.Repeat([]byte("x"), 1<<20)

	allocs := testing.AllocsPerRun(20, func() {
		var wire bytes.Buffer
		tunnel := &tlsTCPTunnel{
			rawConn:    noopDeadlineConn{},
			writer:     &wire,
			closeOuter: func() error { return nil },
			streams:    make(map[uint32]*tlsTCPLogicalStream),
			closed:     make(chan struct{}),
		}
		defer tunnel.close()
		stream := &tlsTCPLogicalStream{
			tunnel:       tunnel,
			streamID:     1,
			readCh:       make(chan struct{}, 1),
			openResultCh: make(chan muxOpenResult, 1),
		}
		if _, err := stream.ReadFrom(bytes.NewReader(payload)); err != nil {
			t.Fatalf("ReadFrom() error = %v", err)
		}
	})

	if allocs > 100 {
		t.Fatalf("ReadFrom() allocations = %.0f, want <= 100", allocs)
	}
}

func TestWrapIdleConnPreservesTLSTCPBulkInterfaces(t *testing.T) {
	stream := &tlsTCPLogicalStream{readCh: make(chan struct{}, 1)}
	wrapped := wrapIdleConn(stream)

	if _, ok := wrapped.(io.ReaderFrom); !ok {
		t.Fatalf("wrapped tls tcp stream does not implement io.ReaderFrom")
	}
	if _, ok := wrapped.(io.WriterTo); !ok {
		t.Fatalf("wrapped tls tcp stream does not implement io.WriterTo")
	}
}

func TestWrapIdleConnTLSTCPStreamReadHonorsIdleDeadline(t *testing.T) {
	withRelayTimeouts(getRelayDialTimeout(), getRelayHandshakeTimeout(), getRelayFrameTimeout(), 20*time.Millisecond, func() {
		stream := &tlsTCPLogicalStream{
			tunnel: &tlsTCPTunnel{
				closed: make(chan struct{}),
			},
			readCh:       make(chan struct{}, 1),
			openResultCh: make(chan muxOpenResult, 1),
		}
		wrapped := wrapIdleConn(stream)

		done := make(chan error, 1)
		go func() {
			buf := make([]byte, 1)
			_, err := wrapped.Read(buf)
			done <- err
		}()

		select {
		case err := <-done:
			if err == nil {
				t.Fatal("Read() error = nil, want timeout")
			}
			netErr, ok := err.(net.Error)
			if !ok || !netErr.Timeout() {
				t.Fatalf("Read() error = %v, want timeout net.Error", err)
			}
		case <-time.After(200 * time.Millisecond):
			t.Fatal("Read() did not honor idle deadline")
		}
	})
}

func TestTLSTCPLogicalStreamWriteHonorsWriteDeadline(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	tunnel := &tlsTCPTunnel{
		rawConn:    clientConn,
		writer:     clientConn,
		closeOuter: clientConn.Close,
		streams:    make(map[uint32]*tlsTCPLogicalStream),
		closed:     make(chan struct{}),
	}
	defer tunnel.close()

	stream := &tlsTCPLogicalStream{
		tunnel:       tunnel,
		streamID:     1,
		readCh:       make(chan struct{}, 1),
		openResultCh: make(chan muxOpenResult, 1),
	}
	if err := stream.SetWriteDeadline(time.Now().Add(20 * time.Millisecond)); err != nil {
		t.Fatalf("SetWriteDeadline() error = %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := stream.Write([]byte("payload"))
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Write() error = nil, want timeout")
		}
		netErr, ok := err.(net.Error)
		if !ok || !netErr.Timeout() {
			t.Fatalf("Write() error = %v, want timeout net.Error", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Write() did not honor write deadline")
	}
}

type noopDeadlineConn struct{ net.Conn }

func (noopDeadlineConn) SetDeadline(time.Time) error      { return nil }
func (noopDeadlineConn) SetReadDeadline(time.Time) error  { return nil }
func (noopDeadlineConn) SetWriteDeadline(time.Time) error { return nil }

func withTLSTCPBufferedReadLimitForTest(limit int, fn func()) {
	previousLimit := tlsTCPMaxBufferedReadBytes
	previousResume := tlsTCPResumeBufferedReadBytes
	tlsTCPMaxBufferedReadBytes = limit
	tlsTCPResumeBufferedReadBytes = limit / 2
	defer func() {
		tlsTCPMaxBufferedReadBytes = previousLimit
		tlsTCPResumeBufferedReadBytes = previousResume
	}()
	fn()
}

type blockingFirstWrite struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
	mu      sync.Mutex
	buf     bytes.Buffer
}

func newBlockingFirstWrite() *blockingFirstWrite {
	return &blockingFirstWrite{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (w *blockingFirstWrite) Write(p []byte) (int, error) {
	blocked := false
	w.once.Do(func() {
		close(w.started)
		<-w.release
		blocked = true
	})
	if !blocked {
		select {
		case <-w.release:
		default:
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *blockingFirstWrite) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

type markingConn struct {
	net.Conn
	onRead func()
	chunks [][]byte
}

func (c *markingConn) Read(p []byte) (int, error) {
	if len(c.chunks) == 0 {
		return 0, io.EOF
	}
	if c.onRead != nil {
		c.onRead()
		c.onRead = nil
	}
	chunk := c.chunks[0]
	c.chunks = c.chunks[1:]
	return copy(p, chunk), nil
}

func (c *markingConn) Write(p []byte) (int, error) { return len(p), nil }
func (c *markingConn) Close() error                { return nil }
func (c *markingConn) LocalAddr() net.Addr         { return nil }
func (c *markingConn) RemoteAddr() net.Addr        { return nil }
func (c *markingConn) SetDeadline(time.Time) error { return nil }
func (c *markingConn) SetReadDeadline(time.Time) error {
	return nil
}
func (c *markingConn) SetWriteDeadline(time.Time) error {
	return nil
}

type countingChunkConn struct {
	net.Conn
	readCalls int
	chunks    [][]byte
}

func (c *countingChunkConn) Read(p []byte) (int, error) {
	if len(c.chunks) == 0 {
		return 0, io.EOF
	}
	chunk := c.chunks[0]
	c.chunks = c.chunks[1:]
	c.readCalls++
	return copy(p, chunk), nil
}

func (c *countingChunkConn) Write(p []byte) (int, error) { return len(p), nil }
func (c *countingChunkConn) Close() error                { return nil }
func (c *countingChunkConn) LocalAddr() net.Addr         { return nil }
func (c *countingChunkConn) RemoteAddr() net.Addr        { return nil }
func (c *countingChunkConn) SetDeadline(time.Time) error { return nil }
func (c *countingChunkConn) SetReadDeadline(time.Time) error {
	return nil
}
func (c *countingChunkConn) SetWriteDeadline(time.Time) error {
	return nil
}

type countingDeadlineConn struct {
	bytes.Buffer
	writeDeadlineCalls int
}

func (c *countingDeadlineConn) Read([]byte) (int, error)    { return 0, io.EOF }
func (c *countingDeadlineConn) Close() error                { return nil }
func (c *countingDeadlineConn) LocalAddr() net.Addr         { return nil }
func (c *countingDeadlineConn) RemoteAddr() net.Addr        { return nil }
func (c *countingDeadlineConn) SetDeadline(time.Time) error { return nil }
func (c *countingDeadlineConn) SetReadDeadline(time.Time) error {
	return nil
}
func (c *countingDeadlineConn) SetWriteDeadline(time.Time) error {
	c.writeDeadlineCalls++
	return nil
}
