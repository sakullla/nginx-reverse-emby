package ingress

import (
	"context"
	"errors"
	"net"
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

func TestPacketBrokerStopsAfterPermanentReadError(t *testing.T) {
	broker := NewPacketBroker(permanentErrorPacketConn{}, "udp")
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
	if err := broker.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
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

type permanentErrorPacketConn struct{}

func (permanentErrorPacketConn) ReadFrom([]byte) (int, net.Addr, error) {
	return 0, nil, errors.New("permanent packet failure")
}
func (permanentErrorPacketConn) WriteTo(payload []byte, _ net.Addr) (int, error) {
	return len(payload), nil
}
func (permanentErrorPacketConn) Close() error                     { return nil }
func (permanentErrorPacketConn) LocalAddr() net.Addr              { return testPacketAddr("packet") }
func (permanentErrorPacketConn) SetDeadline(time.Time) error      { return nil }
func (permanentErrorPacketConn) SetReadDeadline(time.Time) error  { return nil }
func (permanentErrorPacketConn) SetWriteDeadline(time.Time) error { return nil }
