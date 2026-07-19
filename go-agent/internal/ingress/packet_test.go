package ingress

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/moduleutil"
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

func TestPacketBrokerSelectorPinsAssociationsAndKeepsCandidateInvisible(t *testing.T) {
	broker, err := ListenPacket(context.Background(), "udp", "127.0.0.1:0", 8)
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()

	oldEndpoint := broker.NewEndpoint("old", 8)
	candidate := broker.NewEndpoint("candidate", 8)
	var selected atomic.Pointer[PacketEndpoint]
	selected.Store(oldEndpoint)
	broker.SetSelector(selected.Load)
	if _, err := candidate.WriteTo([]byte("prepublish"), testPacketAddr("remote")); !errors.Is(err, moduleutil.ErrVirtualEndpointInactive) {
		t.Fatalf("candidate WriteTo() error = %v, want inactive", err)
	}

	oldClient := dialUDP(t, broker.LocalAddr().String())
	defer oldClient.Close()
	writeUDP(t, oldClient, "old-1")
	readPacket(t, oldEndpoint, "old-1")

	selected.Store(candidate)
	writeUDP(t, oldClient, "old-2")
	readPacket(t, oldEndpoint, "old-2")
	newClient := dialUDP(t, broker.LocalAddr().String())
	defer newClient.Close()
	writeUDP(t, newClient, "new")
	readPacket(t, candidate, "new")

	oldKey := FiveTupleAssociationKey("udp", broker.LocalAddr(), oldClient.LocalAddr())
	if !broker.Release(oldKey) {
		t.Fatal("old association was not pinned")
	}
	writeUDP(t, oldClient, "reselected")
	readPacket(t, candidate, "reselected")
	if err := candidate.Close(); err != nil {
		t.Fatal(err)
	}
	if got := broker.AssociationCount(); got != 0 {
		t.Fatalf("AssociationCount() = %d after selected endpoint close", got)
	}
}

func TestPacketBrokerSelectorNilAndClosedEndpointDoNotCreateAssociations(t *testing.T) {
	broker, err := ListenPacket(context.Background(), "udp", "127.0.0.1:0", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()

	client := dialUDP(t, broker.LocalAddr().String())
	defer client.Close()
	broker.SetSelector(func() *PacketEndpoint { return nil })
	writeUDP(t, client, "nil")
	waitPacketDrops(t, broker, 1)
	if got := broker.AssociationCount(); got != 0 {
		t.Fatalf("AssociationCount() = %d after nil selection", got)
	}

	closed := broker.NewEndpoint("closed", 4)
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}
	broker.SetSelector(func() *PacketEndpoint { return closed })
	writeUDP(t, client, "closed")
	waitPacketDrops(t, broker, 2)
	if got := broker.AssociationCount(); got != 0 {
		t.Fatalf("AssociationCount() = %d after closed selection", got)
	}
}

func TestPacketBrokerSelectorSwapIsRaceSafe(t *testing.T) {
	physical := newDeadlineBoundaryPacketConn()
	broker := NewPacketBroker(physical, "udp")
	defer broker.Close()
	first := broker.NewEndpoint("first", 128)
	second := broker.NewEndpoint("second", 128)

	firstSelector := func() *PacketEndpoint { return first }
	secondSelector := func() *PacketEndpoint { return second }
	broker.SetSelector(firstSelector)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for index := 0; index < 1000; index++ {
			if index%2 == 0 {
				broker.SetSelector(secondSelector)
			} else {
				broker.SetSelector(firstSelector)
			}
		}
	}()
	for index := 0; index < 1000; index++ {
		endpoint, created := broker.endpointFor(AssociationKey(fmt.Sprintf("key-%d", index)))
		if !created || (endpoint != first && endpoint != second) {
			t.Fatalf("endpointFor(%d) = %p/%v", index, endpoint, created)
		}
	}
	<-done
}

func TestPacketBrokerNilSelectorRestoresLegacyActivation(t *testing.T) {
	physical := newDeadlineBoundaryPacketConn()
	broker := NewPacketBroker(physical, "udp")
	defer broker.Close()
	legacy := broker.NewEndpoint("legacy", 2)
	candidate := broker.NewEndpoint("candidate", 2)
	if _, err := broker.Activate(legacy); err != nil {
		t.Fatal(err)
	}
	broker.SetSelector(func() *PacketEndpoint { return candidate })
	if endpoint, created := broker.endpointFor("selector"); !created || endpoint != candidate {
		t.Fatalf("selector endpoint = %p/%v, want candidate", endpoint, created)
	}
	broker.SetSelector(nil)
	if endpoint, created := broker.endpointFor("legacy"); !created || endpoint != legacy {
		t.Fatalf("legacy endpoint = %p/%v, want active endpoint", endpoint, created)
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

func TestPacketBrokerDeadlineUpdateCannotMissWritePublication(t *testing.T) {
	physical := newDeadlineBoundaryPacketConn()
	broker := NewPacketBroker(physical, "udp")
	defer broker.Close()
	endpoint := broker.NewEndpoint("generation", 1)
	if _, err := broker.Activate(endpoint); err != nil {
		t.Fatal(err)
	}
	boundary, release := make(chan struct{}), make(chan struct{})
	broker.beforeWritePublish = func(target *PacketEndpoint) {
		if target == endpoint {
			close(boundary)
			<-release
		}
	}
	written := make(chan error, 1)
	go func() { _, err := endpoint.WriteTo([]byte("blocked"), testPacketAddr("remote")); written <- err }()
	<-boundary
	deadlineSet := make(chan error, 1)
	go func() { deadlineSet <- endpoint.SetWriteDeadline(time.Now().Add(10 * time.Millisecond)) }()
	select {
	case <-deadlineSet:
		t.Fatal("deadline update escaped before publication")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err := <-deadlineSet; err != nil {
		t.Fatal(err)
	}
	if err := <-written; !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("WriteTo error = %v", err)
	}
}

func TestPacketEndpointCloseInterruptsBlockedPhysicalWrite(t *testing.T) {
	physical := newDeadlineBoundaryPacketConn()
	broker := NewPacketBroker(physical, "udp")
	defer broker.Close()
	endpoint := broker.NewEndpoint("generation", 1)
	if _, err := broker.Activate(endpoint); err != nil {
		t.Fatal(err)
	}
	written := make(chan error, 1)
	go func() { _, err := endpoint.WriteTo([]byte("blocked"), testPacketAddr("remote")); written <- err }()
	<-physical.writeStarted
	closed := make(chan error, 1)
	go func() { closed <- endpoint.Close() }()
	waitEndpointClosing(t, endpoint)
	if err := endpoint.SetWriteDeadline(time.Time{}); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("deadline override during Close error = %v, want net.ErrClosed", err)
	}
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("Close error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("endpoint Close did not interrupt physical write")
	}
	if err := <-written; !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("WriteTo error = %v", err)
	}
	if _, err := endpoint.WriteTo([]byte("late"), testPacketAddr("remote")); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("late WriteTo error = %v", err)
	}
}

func TestPacketEndpointCloseWinsBeforeWriteOwnershipPublication(t *testing.T) {
	physical := newDeadlineBoundaryPacketConn()
	broker := NewPacketBroker(physical, "udp")
	defer broker.Close()
	endpoint := broker.NewEndpoint("generation", 1)
	if _, err := broker.Activate(endpoint); err != nil {
		t.Fatal(err)
	}
	boundary, release := make(chan struct{}), make(chan struct{})
	broker.beforeWritePublish = func(target *PacketEndpoint) {
		if target == endpoint {
			close(boundary)
			<-release
		}
	}
	written := make(chan error, 1)
	go func() { _, err := endpoint.WriteTo([]byte("blocked"), testPacketAddr("remote")); written <- err }()
	<-boundary
	closed := make(chan error, 1)
	go func() { closed <- endpoint.Close() }()
	waitEndpointClosing(t, endpoint)
	close(release)
	if err := <-written; !errors.Is(err, net.ErrClosed) {
		t.Fatalf("WriteTo error = %v", err)
	}
	if err := <-closed; err != nil {
		t.Fatalf("Close error = %v", err)
	}
	select {
	case <-physical.writeStarted:
		t.Fatal("closing endpoint reached physical WriteTo")
	default:
	}
}

func waitEndpointClosing(t *testing.T, endpoint *PacketEndpoint) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !endpoint.closing.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !endpoint.closing.Load() {
		t.Fatal("endpoint did not enter closing state")
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

func waitPacketDrops(t *testing.T, broker *PacketBroker, want uint64) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for broker.Stats().Dropped < want && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := broker.Stats().Dropped; got < want {
		t.Fatalf("Dropped = %d, want at least %d", got, want)
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

type deadlineBoundaryPacketConn struct {
	closed, writeStarted, writeRelease chan struct{}
	closeOnce, startOnce               sync.Once
}

func newDeadlineBoundaryPacketConn() *deadlineBoundaryPacketConn {
	return &deadlineBoundaryPacketConn{closed: make(chan struct{}), writeStarted: make(chan struct{}), writeRelease: make(chan struct{})}
}
func (c *deadlineBoundaryPacketConn) ReadFrom([]byte) (int, net.Addr, error) {
	<-c.closed
	return 0, nil, net.ErrClosed
}
func (c *deadlineBoundaryPacketConn) WriteTo([]byte, net.Addr) (int, error) {
	c.startOnce.Do(func() { close(c.writeStarted) })
	<-c.writeRelease
	return 0, os.ErrDeadlineExceeded
}
func (c *deadlineBoundaryPacketConn) Close() error {
	c.closeOnce.Do(func() {
		close(c.closed)
		select {
		case <-c.writeRelease:
		default:
			close(c.writeRelease)
		}
	})
	return nil
}
func (*deadlineBoundaryPacketConn) LocalAddr() net.Addr             { return testPacketAddr("deadline") }
func (c *deadlineBoundaryPacketConn) SetDeadline(d time.Time) error { return c.SetWriteDeadline(d) }
func (*deadlineBoundaryPacketConn) SetReadDeadline(time.Time) error { return nil }
func (c *deadlineBoundaryPacketConn) SetWriteDeadline(d time.Time) error {
	if !d.IsZero() {
		select {
		case <-c.writeRelease:
		default:
			close(c.writeRelease)
		}
	}
	return nil
}
