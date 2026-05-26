package relay

import (
	"bytes"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/traffic"
)

func TestPipeBothWaysRecordsRelayTraffic(t *testing.T) {
	traffic.Reset()
	defer traffic.Reset()

	left, clientPeer := net.Pipe()
	right, upstreamPeer := net.Pipe()
	defer left.Close()
	defer clientPeer.Close()
	defer right.Close()
	defer upstreamPeer.Close()

	done := make(chan struct{})
	go func() {
		pipeBothWays(left, right, nil)
		close(done)
	}()

	if _, err := clientPeer.Write([]byte("relay-inbound")); err != nil {
		t.Fatalf("client write error: %v", err)
	}
	readRelayExact(t, upstreamPeer, len("relay-inbound"))

	if _, err := upstreamPeer.Write([]byte("relay-outbound")); err != nil {
		t.Fatalf("upstream write error: %v", err)
	}
	readRelayExact(t, clientPeer, len("relay-outbound"))

	_ = clientPeer.Close()
	_ = upstreamPeer.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("pipeBothWays did not exit")
	}

	stats := traffic.Snapshot()["traffic"].(map[string]any)
	relayStats := stats["relay"].(map[string]uint64)
	if relayStats["rx_bytes"] != uint64(len("relay-inbound")) {
		t.Fatalf("relay rx_bytes = %d, want %d", relayStats["rx_bytes"], len("relay-inbound"))
	}
	if relayStats["tx_bytes"] != uint64(len("relay-outbound")) {
		t.Fatalf("relay tx_bytes = %d, want %d", relayStats["tx_bytes"], len("relay-outbound"))
	}
}

func TestPipeBothWaysRecordsRelayListenerTraffic(t *testing.T) {
	traffic.Reset()
	traffic.SetEnabled(true)
	defer traffic.Reset()

	left, clientPeer := net.Pipe()
	right, upstreamPeer := net.Pipe()
	defer left.Close()
	defer clientPeer.Close()
	defer right.Close()
	defer upstreamPeer.Close()

	done := make(chan struct{})
	go func() {
		pipeBothWays(left, right, traffic.NewRelayListenerRecorder(99))
		close(done)
	}()

	if _, err := clientPeer.Write([]byte("relay-inbound")); err != nil {
		t.Fatalf("client write error: %v", err)
	}
	readRelayExact(t, upstreamPeer, len("relay-inbound"))

	if _, err := upstreamPeer.Write([]byte("relay-outbound")); err != nil {
		t.Fatalf("upstream write error: %v", err)
	}
	readRelayExact(t, clientPeer, len("relay-outbound"))

	_ = clientPeer.Close()
	_ = upstreamPeer.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("pipeBothWays did not exit")
	}

	stats := traffic.Snapshot()["traffic"].(map[string]any)
	listeners := stats["relay_listeners"].(map[string]map[string]uint64)
	got := listeners["99"]
	if got["rx_bytes"] != uint64(len("relay-inbound")) {
		t.Fatalf("relay_listeners[99].rx_bytes = %d, want %d", got["rx_bytes"], len("relay-inbound"))
	}
	if got["tx_bytes"] != uint64(len("relay-outbound")) {
		t.Fatalf("relay_listeners[99].tx_bytes = %d, want %d", got["tx_bytes"], len("relay-outbound"))
	}
}

func TestPipeBothWaysReportsRelayTrafficBeforeStreamsClose(t *testing.T) {
	traffic.Reset()
	defer traffic.Reset()

	left, clientPeer := net.Pipe()
	right, upstreamPeer := net.Pipe()
	defer left.Close()
	defer clientPeer.Close()
	defer right.Close()
	defer upstreamPeer.Close()

	done := make(chan struct{})
	go func() {
		pipeBothWays(left, right, nil)
		close(done)
	}()

	if _, err := clientPeer.Write([]byte("active-inbound")); err != nil {
		t.Fatalf("client write error: %v", err)
	}
	readRelayExact(t, upstreamPeer, len("active-inbound"))

	if _, err := upstreamPeer.Write([]byte("active-outbound")); err != nil {
		t.Fatalf("upstream write error: %v", err)
	}
	readRelayExact(t, clientPeer, len("active-outbound"))

	relayStats := waitForRelayTraffic(t, len("active-inbound"), len("active-outbound"))
	if relayStats["rx_bytes"] != uint64(len("active-inbound")) {
		t.Fatalf("relay rx_bytes while stream active = %d", relayStats["rx_bytes"])
	}
	if relayStats["tx_bytes"] != uint64(len("active-outbound")) {
		t.Fatalf("relay tx_bytes while stream active = %d", relayStats["tx_bytes"])
	}

	_ = clientPeer.Close()
	_ = upstreamPeer.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("pipeBothWays did not exit")
	}
}

func TestCopyRelayTrafficPreservesTLSTCPDestinationReadFrom(t *testing.T) {
	traffic.Reset()
	defer traffic.Reset()

	var wire bytes.Buffer
	tunnel := &tlsTCPTunnel{
		rawConn:    noopDeadlineConn{},
		writer:     &wire,
		closeOuter: func() error { return nil },
		streams:    make(map[uint32]*tlsTCPLogicalStream),
		closed:     make(chan struct{}),
	}
	dst := wrapIdleConn(&tlsTCPLogicalStream{
		tunnel:       tunnel,
		streamID:     7,
		readCh:       make(chan struct{}, 1),
		openResultCh: make(chan muxOpenResult, 1),
	})
	src := &relayWriterToSource{payload: []byte("payload")}
	recorder := traffic.NewRelayRecorder()

	n, err := copyRelayTraffic(dst, src, true, recorder)
	if err != nil {
		t.Fatalf("copyRelayTraffic() error = %v", err)
	}
	if n != int64(len("payload")) {
		t.Fatalf("copyRelayTraffic() = %d, want %d", n, len("payload"))
	}
	if src.usedWriterTo {
		t.Fatal("source WriterTo was used instead of TLS TCP destination ReadFrom fast path")
	}
	frame, err := readMuxFrame(bytes.NewReader(wire.Bytes()))
	if err != nil {
		t.Fatalf("readMuxFrame() error = %v", err)
	}
	if frame.Type != muxFrameTypeData {
		t.Fatalf("frame.Type = %v, want %v", frame.Type, muxFrameTypeData)
	}
	if got := string(frame.Payload); got != "payload" {
		t.Fatalf("mux payload = %q, want payload", got)
	}

	stats := traffic.Snapshot()["traffic"].(map[string]any)
	relayStats := stats["relay"].(map[string]uint64)
	if relayStats["rx_bytes"] != uint64(len("payload")) || relayStats["tx_bytes"] != 0 {
		t.Fatalf("relay counters = %+v, want rx=%d tx=0", relayStats, len("payload"))
	}
}

func TestCopyRelayTrafficReportsTLSTCPFastPathTrafficBeforeSourceEOF(t *testing.T) {
	traffic.Reset()
	defer traffic.Reset()

	var wire bytes.Buffer
	tunnel := &tlsTCPTunnel{
		rawConn:    noopDeadlineConn{},
		writer:     &wire,
		closeOuter: func() error { return nil },
		streams:    make(map[uint32]*tlsTCPLogicalStream),
		closed:     make(chan struct{}),
	}
	dst := wrapIdleConn(&tlsTCPLogicalStream{
		tunnel:       tunnel,
		streamID:     11,
		readCh:       make(chan struct{}, 1),
		openResultCh: make(chan muxOpenResult, 1),
	})
	src := newBlockingRelayReader([]byte("active-tls-payload"))
	defer src.close()
	recorder := traffic.NewRelayRecorder()

	done := make(chan error, 1)
	go func() {
		_, err := copyRelayTraffic(dst, src, true, recorder)
		done <- err
	}()

	src.waitFirstRead(t, time.Second)
	relayStats := waitForRelayTraffic(t, len("active-tls-payload"), 0)
	if relayStats["rx_bytes"] != uint64(len("active-tls-payload")) {
		t.Fatalf("relay rx_bytes while TLS fast-path copy is active = %d, want %d", relayStats["rx_bytes"], len("active-tls-payload"))
	}

	src.close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("copyRelayTraffic() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("copyRelayTraffic did not exit after source close")
	}
}

func TestPipeBothWaysIncludesInitialPayloadTraffic(t *testing.T) {
	traffic.Reset()
	defer traffic.Reset()

	initial := []byte("initial-payload")
	left, clientPeer := net.Pipe()
	right, upstreamPeer := net.Pipe()
	defer left.Close()
	defer clientPeer.Close()
	defer right.Close()
	defer upstreamPeer.Close()

	initialWrite := make(chan error, 1)
	go func() {
		_, err := right.Write(initial)
		initialWrite <- err
	}()
	readRelayExact(t, upstreamPeer, len(initial))
	if err := <-initialWrite; err != nil {
		t.Fatalf("initial write error: %v", err)
	}

	done := make(chan struct{})
	go func() {
		pipeBothWaysWithInitialRelayRX(left, right, int64(len(initial)), nil)
		close(done)
	}()

	relayStats := waitForRelayTraffic(t, len(initial), 0)
	if relayStats["rx_bytes"] != uint64(len(initial)) {
		t.Fatalf("relay rx_bytes with initial payload = %d", relayStats["rx_bytes"])
	}
	if relayStats["tx_bytes"] != 0 {
		t.Fatalf("relay tx_bytes with initial payload = %d, want 0", relayStats["tx_bytes"])
	}

	_ = clientPeer.Close()
	_ = upstreamPeer.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("pipeBothWays did not exit")
	}
}

func TestPipeUDPPacketsFlushesTrafficAfterBothDirectionsFinish(t *testing.T) {
	traffic.Reset()
	defer traffic.Reset()

	clientConn := newScriptedUDPRelayConn([]byte("initial-request"))
	upstream := newAsymmetricShutdownUDPPeer([]byte("late-final-reply"))

	done := make(chan struct{})
	go func() {
		pipeUDPPackets(clientConn, upstream, nil)
		close(done)
	}()

	upstream.waitForWrite(t, time.Second)
	clientConn.closeReads()
	upstream.allowFinalReply()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("pipeUDPPackets did not exit")
	}
	clientConn.assertWrotePacket(t, []byte("late-final-reply"))

	stats := traffic.Snapshot()["traffic"].(map[string]any)
	relayStats := stats["relay"].(map[string]uint64)
	if relayStats["rx_bytes"] != uint64(len("initial-request")) {
		t.Fatalf("relay rx_bytes = %d, want %d", relayStats["rx_bytes"], len("initial-request"))
	}
	if relayStats["tx_bytes"] != uint64(len("late-final-reply")) {
		t.Fatalf("relay tx_bytes = %d, want %d", relayStats["tx_bytes"], len("late-final-reply"))
	}
}

func TestPipeUDPPacketsReportsTrafficBeforePipeCloses(t *testing.T) {
	traffic.Reset()
	defer traffic.Reset()

	clientConn := newScriptedUDPRelayConn([]byte("active-request"))
	upstream := newAsymmetricShutdownUDPPeer([]byte("active-reply"))

	done := make(chan struct{})
	go func() {
		pipeUDPPackets(clientConn, upstream, nil)
		close(done)
	}()

	upstream.waitForWrite(t, time.Second)
	upstream.allowFinalReply()

	deadline := time.Now().Add(time.Second)
	for {
		stats := traffic.SnapshotNonZero()
		if stats != nil {
			relayStats := stats["traffic"].(map[string]any)["relay"].(map[string]uint64)
			if relayStats["rx_bytes"] == uint64(len("active-request")) {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("active UDP relay traffic was not visible before pipe closed")
		}
		time.Sleep(10 * time.Millisecond)
	}

	clientConn.closeReads()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("pipeUDPPackets did not exit")
	}
}

func readRelayExact(t *testing.T, r io.Reader, size int) {
	t.Helper()

	buf := make([]byte, size)
	if _, err := io.ReadFull(r, buf); err != nil {
		t.Fatalf("read error: %v", err)
	}
}

func waitForRelayTraffic(t *testing.T, wantRX int, wantTX int) map[string]uint64 {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	for {
		stats := traffic.Snapshot()["traffic"].(map[string]any)
		relayStats := stats["relay"].(map[string]uint64)
		if relayStats["rx_bytes"] == uint64(wantRX) && relayStats["tx_bytes"] == uint64(wantTX) {
			return relayStats
		}
		if time.Now().After(deadline) {
			return relayStats
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type asymmetricShutdownUDPPeer struct {
	mu        sync.Mutex
	writes    [][]byte
	wrote     chan struct{}
	allowRead chan struct{}
	reply     []byte
	read      bool
}

func newAsymmetricShutdownUDPPeer(reply []byte) *asymmetricShutdownUDPPeer {
	return &asymmetricShutdownUDPPeer{
		wrote:     make(chan struct{}),
		allowRead: make(chan struct{}),
		reply:     append([]byte(nil), reply...),
	}
}

func (p *asymmetricShutdownUDPPeer) Close() error {
	return nil
}

func (p *asymmetricShutdownUDPPeer) SetReadDeadline(time.Time) error {
	return nil
}

func (p *asymmetricShutdownUDPPeer) SetWriteDeadline(time.Time) error {
	return nil
}

func (p *asymmetricShutdownUDPPeer) ReadPacket() ([]byte, error) {
	<-p.allowRead
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.read {
		return nil, io.EOF
	}
	p.read = true
	return append([]byte(nil), p.reply...), nil
}

func (p *asymmetricShutdownUDPPeer) WritePacket(payload []byte) error {
	p.mu.Lock()
	p.writes = append(p.writes, append([]byte(nil), payload...))
	p.mu.Unlock()
	close(p.wrote)
	return nil
}

func (p *asymmetricShutdownUDPPeer) waitForWrite(t *testing.T, timeout time.Duration) {
	t.Helper()
	select {
	case <-p.wrote:
	case <-time.After(timeout):
		t.Fatal("timed out waiting for upstream write")
	}
}

func (p *asymmetricShutdownUDPPeer) allowFinalReply() {
	close(p.allowRead)
}

type scriptedUDPRelayConn struct {
	mu       sync.Mutex
	readBuf  []byte
	writes   []byte
	readDone bool
}

func newScriptedUDPRelayConn(packet []byte) *scriptedUDPRelayConn {
	var framed []byte
	writer := appendWriter{write: func(p []byte) {
		framed = append(framed, p...)
	}}
	_ = writeUOTPacket(writer, packet)
	return &scriptedUDPRelayConn{readBuf: framed}
}

func (c *scriptedUDPRelayConn) Read(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.readBuf) == 0 || c.readDone {
		return 0, io.EOF
	}
	n := copy(p, c.readBuf)
	c.readBuf = c.readBuf[n:]
	return n, nil
}

func (c *scriptedUDPRelayConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.writes = append(c.writes, p...)
	return len(p), nil
}

func (c *scriptedUDPRelayConn) Close() error {
	return nil
}

func (c *scriptedUDPRelayConn) LocalAddr() net.Addr {
	return dummyAddr("local")
}

func (c *scriptedUDPRelayConn) RemoteAddr() net.Addr {
	return dummyAddr("remote")
}

func (c *scriptedUDPRelayConn) SetDeadline(time.Time) error {
	return nil
}

func (c *scriptedUDPRelayConn) SetReadDeadline(time.Time) error {
	return nil
}

func (c *scriptedUDPRelayConn) SetWriteDeadline(time.Time) error {
	return nil
}

func (c *scriptedUDPRelayConn) closeReads() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.readDone = true
}

func (c *scriptedUDPRelayConn) assertWrotePacket(t *testing.T, want []byte) {
	t.Helper()
	c.mu.Lock()
	writes := append([]byte(nil), c.writes...)
	c.mu.Unlock()
	reader := bytesReader(writes)
	got, err := readUOTPacket(&reader)
	if err != nil {
		t.Fatalf("read written UOT packet error = %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("written packet = %q, want %q", string(got), string(want))
	}
}

type appendWriter struct {
	write func([]byte)
}

func (w appendWriter) Write(p []byte) (int, error) {
	w.write(p)
	return len(p), nil
}

type bytesReader []byte

func (r *bytesReader) Read(p []byte) (int, error) {
	if len(*r) == 0 {
		return 0, io.EOF
	}
	n := copy(p, *r)
	*r = (*r)[n:]
	return n, nil
}

type dummyAddr string

func (a dummyAddr) Network() string { return string(a) }
func (a dummyAddr) String() string  { return string(a) }

type relayWriterToSource struct {
	payload      []byte
	usedWriterTo bool
}

func (s *relayWriterToSource) Read(p []byte) (int, error) {
	if len(s.payload) == 0 {
		return 0, io.EOF
	}
	n := copy(p, s.payload)
	s.payload = s.payload[n:]
	return n, nil
}

func (s *relayWriterToSource) WriteTo(w io.Writer) (int64, error) {
	s.usedWriterTo = true
	n, err := w.Write(s.payload)
	s.payload = s.payload[n:]
	return int64(n), err
}

type blockingRelayReader struct {
	mu        sync.Mutex
	payload   []byte
	read      bool
	firstRead chan struct{}
	release   chan struct{}
	once      sync.Once
}

func newBlockingRelayReader(payload []byte) *blockingRelayReader {
	return &blockingRelayReader{
		payload:   append([]byte(nil), payload...),
		firstRead: make(chan struct{}),
		release:   make(chan struct{}),
	}
}

func (r *blockingRelayReader) Read(p []byte) (int, error) {
	r.mu.Lock()
	if !r.read {
		r.read = true
		n := copy(p, r.payload)
		r.payload = r.payload[n:]
		close(r.firstRead)
		r.mu.Unlock()
		return n, nil
	}
	r.mu.Unlock()

	<-r.release
	return 0, io.EOF
}

func (r *blockingRelayReader) close() {
	r.once.Do(func() {
		close(r.release)
	})
}

func (r *blockingRelayReader) waitFirstRead(t *testing.T, timeout time.Duration) {
	t.Helper()
	select {
	case <-r.firstRead:
	case <-time.After(timeout):
		t.Fatal("timed out waiting for source read")
	}
}
