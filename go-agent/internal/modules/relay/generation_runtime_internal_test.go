package relay

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/generation"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/ingress"
)

func TestRelayIngressRegisteredEndpointStaysInvisibleUntilActivation(t *testing.T) {
	manager := newRelayIngressManager(nil)
	listener := Listener{ID: 1, ListenPort: pickFreeTCPPort(t), TransportMode: ListenerTransportModeTLSTCP}
	first, err := manager.acquire(context.Background(), "generation-1", listener, "127.0.0.1", nil)
	if err != nil {
		t.Fatalf("acquire first endpoint: %v", err)
	}
	defer first.release()
	if err := first.activate(); err != nil {
		t.Fatalf("activate first endpoint: %v", err)
	}

	assertRelayEndpointReceives(t, first.stream, manager.bindings[first.binding.key].stream.Addr())
	second, err := manager.acquire(context.Background(), "generation-2", listener, "127.0.0.1", nil)
	if err != nil {
		t.Fatalf("acquire second endpoint: %v", err)
	}
	defer second.release()

	assertRelayEndpointReceives(t, first.stream, manager.bindings[first.binding.key].stream.Addr())
	if err := second.activate(); err != nil {
		t.Fatalf("activate second endpoint: %v", err)
	}
	assertRelayEndpointReceives(t, second.stream, manager.bindings[first.binding.key].stream.Addr())
}

func assertRelayEndpointReceives(t *testing.T, endpoint interface{ Accept() (net.Conn, error) }, address net.Addr) {
	t.Helper()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, _ := endpoint.Accept()
		accepted <- conn
	}()
	client, err := net.DialTimeout("tcp", address.String(), time.Second)
	if err != nil {
		t.Fatalf("dial stable relay ingress: %v", err)
	}
	defer client.Close()
	select {
	case conn := <-accepted:
		if conn == nil {
			t.Fatal("active endpoint returned nil connection")
		}
		_ = conn.Close()
	case <-time.After(time.Second):
		t.Fatal("active endpoint did not receive connection")
	}
}

func TestRelaySessionTrackerDrainsChildrenBeforeParentAndRejectsAdmission(t *testing.T) {
	registry := generation.NewSessionRegistry(nil)
	tracker := newRelaySessionTracker("generation-1", relayTestRegistrar{registry}, true)
	var parentClosed atomic.Int32
	parent := tracker.start("41", "tls-parent", true, func() error {
		parentClosed.Add(1)
		return nil
	})
	child, admitted := tracker.startChild("41", "uot-association", func() error { return nil })
	if !admitted {
		t.Fatal("active generation rejected initial child")
	}
	if got := registry.GenerationCount("generation-1"); got != 2 {
		t.Fatalf("registered sessions = %d, want 2", got)
	}

	tracker.beginDrain()
	if tracker.admit() {
		t.Fatal("draining tracker admitted a new logical stream")
	}
	if got := parentClosed.Load(); got != 0 {
		t.Fatalf("parent closed with active child, count = %d", got)
	}

	child.Finish()
	if got := parentClosed.Load(); got != 1 {
		t.Fatalf("parent close count after child drain = %d, want 1", got)
	}
	parent.Finish()
	if got := registry.GenerationCount("generation-1"); got != 0 {
		t.Fatalf("registered sessions after drain = %d, want 0", got)
	}
}

type relayTestRegistrar struct{ registry *generation.SessionRegistry }

func (r relayTestRegistrar) RegisterSession(g string, e generation.EntityKey, id string, s generation.Session) (*generation.SessionHandle, error) {
	return r.registry.Register(g, e, id, s)
}

func TestRelayGenerationPoolScopeClosesOnlyOwnedTLSTunnels(t *testing.T) {
	first := newRelayPoolScope()
	second := newRelayPoolScope()
	firstConn, firstPeer := net.Pipe()
	secondConn, secondPeer := net.Pipe()
	defer firstPeer.Close()
	defer secondPeer.Close()

	firstTunnel := newTestGenerationTunnel(firstConn)
	secondTunnel := newTestGenerationTunnel(secondConn)
	first.tls.sessions["hop"] = []*tlsTCPTunnel{firstTunnel}
	second.tls.sessions["hop"] = []*tlsTCPTunnel{secondTunnel}
	if err := first.Close(); err != nil {
		t.Fatalf("close first pool scope: %v", err)
	}
	select {
	case <-firstTunnel.closed:
	default:
		t.Fatal("first generation tunnel remained open")
	}
	select {
	case <-secondTunnel.closed:
		t.Fatal("closing first generation closed second generation tunnel")
	default:
	}
	_ = second.Close()
}

func TestRelayGenerationPoolRegistrySharesByGenerationAndReleasesByRefcount(t *testing.T) {
	first := acquireRelayPoolScope("generation-101")
	second := acquireRelayPoolScope("generation-101")
	other := acquireRelayPoolScope("generation-102")
	if first.scope != second.scope {
		t.Fatal("same generation provider did not share outbound pools")
	}
	if first.scope == other.scope {
		t.Fatal("different generation providers shared outbound pools")
	}
	if got := lookupRelayPoolScope("generation-101"); got != first.scope {
		t.Fatal("generation pool lookup did not return the owned scope")
	}
	_ = first.release()
	if got := lookupRelayPoolScope("generation-101"); got != second.scope {
		t.Fatal("first reference release destroyed a still-referenced generation pool")
	}
	_ = second.release()
	if got := lookupRelayPoolScope("generation-101"); got != nil {
		t.Fatal("last generation reference did not remove the pool scope")
	}
	_ = other.release()
}

func TestRelayQUICClassifierPinsGeneratedCIDAcrossCutoverAndMigration(t *testing.T) {
	classifier := newRelayQUICClassifier()
	remote := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 41001}
	metadata := ingress.PacketMetadata{Network: "udp", LocalAddr: &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 443}, RemoteAddr: remote}
	owner, ok := classifier.Classify(relayTestQUICLongPacket([]byte("client01")), metadata)
	if !ok || owner == "" {
		t.Fatal("initial QUIC packet was not classified")
	}
	generator, err := newRelayGenerationConnectionIDGenerator()
	if err != nil {
		t.Fatal(err)
	}
	if !classifier.bind(generator, remote) {
		t.Fatal("generation CID route did not bind to initial association")
	}
	cid, err := generator.GenerateConnectionID()
	if err != nil {
		t.Fatal(err)
	}
	shortPacket := append([]byte{0x40}, cid.Bytes()...)
	if got, _ := classifier.Classify(shortPacket, metadata); got != owner {
		t.Fatalf("generated CID owner = %q, want %q", got, owner)
	}
	migrated := metadata
	migrated.RemoteAddr = &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 41002}
	if got, _ := classifier.Classify(shortPacket, migrated); got != owner {
		t.Fatalf("migrated CID owner = %q, want %q", got, owner)
	}
	newOwner, _ := classifier.Classify(relayTestQUICLongPacket([]byte("client02")), metadata)
	if newOwner == owner {
		t.Fatal("fresh Initial on established tuple reused the retired connection association")
	}
}

func relayTestQUICLongPacket(cid []byte) []byte {
	packet := []byte{0xc0, 0, 0, 0, 1, byte(len(cid))}
	return append(packet, cid...)
}

func newTestGenerationTunnel(conn net.Conn) *tlsTCPTunnel {
	return &tlsTCPTunnel{
		rawConn: conn, reader: conn, writer: conn, closeOuter: conn.Close,
		streams: make(map[uint32]*tlsTCPLogicalStream), closed: make(chan struct{}),
	}
}
