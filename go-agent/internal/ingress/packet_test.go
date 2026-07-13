package ingress

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPacketBrokerPinsAssociationAcrossCutover(t *testing.T) {
	broker, err := ListenPacket(context.Background(), "udp", "127.0.0.1:0", 8)
	if err != nil {
		t.Fatalf("ListenPacket() error = %v", err)
	}
	defer broker.Close()
	oldEndpoint := broker.NewEndpoint("generation-1", 8)
	newEndpoint := broker.NewEndpoint("generation-2", 8)
	if _, err := broker.Activate(oldEndpoint); err != nil {
		t.Fatalf("Activate(old) error = %v", err)
	}
	oldClient := dialUDP(t, broker.LocalAddr().String())
	defer oldClient.Close()
	writeUDP(t, oldClient, "old-1")
	readPacket(t, oldEndpoint, "old-1")

	if _, err := broker.Activate(newEndpoint); err != nil {
		t.Fatalf("Activate(new) error = %v", err)
	}
	writeUDP(t, oldClient, "old-2")
	readPacket(t, oldEndpoint, "old-2")
	newClient := dialUDP(t, broker.LocalAddr().String())
	defer newClient.Close()
	writeUDP(t, newClient, "new")
	readPacket(t, newEndpoint, "new")

	key := FiveTupleAssociationKey("udp", broker.LocalAddr(), oldClient.LocalAddr())
	if !broker.Release(key) {
		t.Fatalf("Release(%q) = false, want pinned association", key)
	}
	writeUDP(t, oldClient, "reassigned")
	readPacket(t, newEndpoint, "reassigned")
	if got := broker.AssociationCount(); got != 2 {
		t.Fatalf("AssociationCount() = %d, want 2 active tuples", got)
	}
}

func TestPacketBrokerUsesPluggableClassifier(t *testing.T) {
	classifier := ClassifierFunc(func(payload []byte, _ PacketMetadata) (AssociationKey, bool) {
		if len(payload) == 0 {
			return "", false
		}
		return AssociationKey(payload[:1]), true
	})
	metadata := PacketMetadata{Network: "udp", LocalAddr: testPacketAddr("local"), RemoteAddr: testPacketAddr("remote")}
	if key := classifyPacket([]PacketClassifier{classifier}, []byte("quic-cid"), metadata); key != "q" {
		t.Fatalf("classifyPacket() = %q, want q", key)
	}
	if key := classifyPacket(nil, []byte("fallback"), metadata); key != FiveTupleAssociationKey("udp", metadata.LocalAddr, metadata.RemoteAddr) {
		t.Fatalf("fallback key = %q", key)
	}
}

func TestPacketBrokerPublishesWriteGateWithActiveTarget(t *testing.T) {
	broker, err := ListenPacket(context.Background(), "udp", "127.0.0.1:0", 2)
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	oldEndpoint, candidate := broker.NewEndpoint("old", 2), broker.NewEndpoint("new", 2)
	if _, err := broker.Activate(oldEndpoint); err != nil {
		t.Fatal(err)
	}
	receiver, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Close()
	boundary, release := make(chan struct{}), make(chan struct{})
	broker.beforePacketPublish = func(endpoint *PacketEndpoint) {
		if endpoint == candidate {
			close(boundary)
			<-release
		}
	}
	activated := make(chan error, 1)
	go func() { _, err := broker.Activate(candidate); activated <- err }()
	<-boundary
	written := make(chan error, 1)
	go func() { _, err := candidate.WriteTo([]byte("published"), receiver.LocalAddr()); written <- err }()
	_ = receiver.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
	if _, _, err := receiver.ReadFrom(make([]byte, 32)); err == nil {
		t.Fatal("candidate wrote before active publication")
	}
	if broker.Active() != oldEndpoint {
		t.Fatal("active target changed before publication boundary")
	}
	close(release)
	if err := <-activated; err != nil {
		t.Fatal(err)
	}
	if err := <-written; err != nil {
		t.Fatal(err)
	}
	_ = receiver.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 32)
	n, _, err := receiver.ReadFrom(buf)
	if err != nil || string(buf[:n]) != "published" {
		t.Fatalf("published write = %q/%v", buf[:n], err)
	}
}

func TestPacketBrokerStopsAfterPermanentReadError(t *testing.T) {
	physical := newPermanentErrorPacketConn()
	broker := NewPacketBroker(physical, "udp")
	endpoint := broker.NewEndpoint("generation", 1)
	if _, err := broker.Activate(endpoint); err != nil {
		t.Fatal(err)
	}
	read := make(chan error, 1)
	go func() { _, _, err := endpoint.ReadFrom(make([]byte, 1)); read <- err }()
	close(physical.fail)
	waited := make(chan struct{})
	go func() {
		broker.Wait()
		close(waited)
	}()
	select {
	case <-waited:
	case <-time.After(time.Second):
		t.Fatal("broker read loop spun after permanent error")
	}
	if err := <-read; !errors.Is(err, net.ErrClosed) {
		t.Fatalf("endpoint ReadFrom error = %v", err)
	}
	if !physical.isClosed() {
		t.Fatal("owned packet socket was not closed")
	}
	if err := broker.Close(); !errors.Is(err, errPermanentPacket) {
		t.Fatalf("Close() error = %v, want terminal cause", err)
	}
}

func TestPacketBrokerBacksOffRetryableReadErrors(t *testing.T) {
	physical := newTemporaryErrorPacketConn()
	broker := NewPacketBroker(physical, "udp")
	time.Sleep(30 * time.Millisecond)
	if calls := physical.calls.Load(); calls > 20 {
		t.Fatalf("ReadFrom calls = %d, want bounded backoff", calls)
	}
	if err := broker.Close(); err != nil {
		t.Fatal(err)
	}
}

func dialUDP(t *testing.T, address string) *net.UDPConn {
	t.Helper()
	remote, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		t.Fatalf("ResolveUDPAddr() error = %v", err)
	}
	conn, err := net.DialUDP("udp", nil, remote)
	if err != nil {
		t.Fatalf("DialUDP() error = %v", err)
	}
	return conn
}

func writeUDP(t *testing.T, conn *net.UDPConn, payload string) {
	t.Helper()
	if _, err := conn.Write([]byte(payload)); err != nil {
		t.Fatalf("Write(%q) error = %v", payload, err)
	}
}

func readPacket(t *testing.T, endpoint *PacketEndpoint, want string) {
	t.Helper()
	if err := endpoint.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	buf := make([]byte, 64)
	n, _, err := endpoint.ReadFrom(buf)
	if err != nil {
		t.Fatalf("ReadFrom() error = %v", err)
	}
	if got := string(buf[:n]); got != want {
		t.Fatalf("ReadFrom() payload = %q, want %q", got, want)
	}
}

type testPacketAddr string

func (a testPacketAddr) Network() string { return "test" }
func (a testPacketAddr) String() string  { return string(a) }

var errPermanentPacket = errors.New("permanent packet failure")

type permanentErrorPacketConn struct {
	fail, closed chan struct{}
	once         sync.Once
}

func newPermanentErrorPacketConn() *permanentErrorPacketConn {
	return &permanentErrorPacketConn{fail: make(chan struct{}), closed: make(chan struct{})}
}
func (c *permanentErrorPacketConn) ReadFrom([]byte) (int, net.Addr, error) {
	<-c.fail
	return 0, nil, errPermanentPacket
}
func (*permanentErrorPacketConn) WriteTo(payload []byte, _ net.Addr) (int, error) {
	return len(payload), nil
}
func (c *permanentErrorPacketConn) Close() error                   { c.once.Do(func() { close(c.closed) }); return nil }
func (*permanentErrorPacketConn) LocalAddr() net.Addr              { return testPacketAddr("packet") }
func (*permanentErrorPacketConn) SetDeadline(time.Time) error      { return nil }
func (*permanentErrorPacketConn) SetReadDeadline(time.Time) error  { return nil }
func (*permanentErrorPacketConn) SetWriteDeadline(time.Time) error { return nil }
func (c *permanentErrorPacketConn) isClosed() bool {
	select {
	case <-c.closed:
		return true
	default:
		return false
	}
}

type temporaryErrorPacketConn struct {
	calls  atomic.Int64
	closed chan struct{}
	once   sync.Once
}

func newTemporaryErrorPacketConn() *temporaryErrorPacketConn {
	return &temporaryErrorPacketConn{closed: make(chan struct{})}
}
func (c *temporaryErrorPacketConn) ReadFrom([]byte) (int, net.Addr, error) {
	select {
	case <-c.closed:
		return 0, nil, net.ErrClosed
	default:
		c.calls.Add(1)
		return 0, nil, temporaryNetError{}
	}
}
func (*temporaryErrorPacketConn) WriteTo(p []byte, _ net.Addr) (int, error) { return len(p), nil }
func (c *temporaryErrorPacketConn) Close() error                            { c.once.Do(func() { close(c.closed) }); return nil }
func (*temporaryErrorPacketConn) LocalAddr() net.Addr                       { return testPacketAddr("temporary-packet") }
func (*temporaryErrorPacketConn) SetDeadline(time.Time) error               { return nil }
func (*temporaryErrorPacketConn) SetReadDeadline(time.Time) error           { return nil }
func (*temporaryErrorPacketConn) SetWriteDeadline(time.Time) error          { return nil }
