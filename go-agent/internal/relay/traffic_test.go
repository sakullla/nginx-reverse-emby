package relay

import (
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
		pipeBothWays(left, right)
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
		t.Fatalf("relay rx_bytes = %d", relayStats["rx_bytes"])
	}
	if relayStats["tx_bytes"] != uint64(len("relay-outbound")) {
		t.Fatalf("relay tx_bytes = %d", relayStats["tx_bytes"])
	}
}

func TestPipeUDPPacketsFlushesTrafficAfterBothDirectionsFinish(t *testing.T) {
	traffic.Reset()
	defer traffic.Reset()

	clientConn := newScriptedUDPRelayConn([]byte("initial-request"))
	upstream := newAsymmetricShutdownUDPPeer([]byte("late-final-reply"))

	done := make(chan struct{})
	go func() {
		pipeUDPPackets(clientConn, upstream)
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
		t.Fatalf("relay rx_bytes = %d", relayStats["rx_bytes"])
	}
	if relayStats["tx_bytes"] != uint64(len("late-final-reply")) {
		t.Fatalf("relay tx_bytes = %d", relayStats["tx_bytes"])
	}
}

func readRelayExact(t *testing.T, r io.Reader, size int) {
	t.Helper()

	buf := make([]byte, size)
	if _, err := io.ReadFull(r, buf); err != nil {
		t.Fatalf("read error: %v", err)
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
