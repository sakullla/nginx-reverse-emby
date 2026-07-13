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

func TestStreamBrokerLinearizesSelectedDispatchBeforeActivation(t *testing.T) {
	broker, err := ListenStream(context.Background(), "tcp", "127.0.0.1:0", 4)
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	oldEndpoint := broker.NewEndpoint("old", 4)
	newEndpoint := broker.NewEndpoint("new", 4)
	if _, err := broker.Activate(oldEndpoint); err != nil {
		t.Fatal(err)
	}
	selected, release := make(chan struct{}), make(chan struct{})
	broker.mu.Lock()
	broker.beforeStreamDeliver = func(endpoint *StreamEndpoint) {
		if endpoint == oldEndpoint {
			close(selected)
			<-release
		}
	}
	broker.mu.Unlock()
	client := dialTCP(t, broker.Addr().String())
	defer client.Close()
	<-selected
	activated := make(chan error, 1)
	go func() { _, err := broker.Activate(newEndpoint); activated <- err }()
	select {
	case <-activated:
		t.Fatal("activation completed before selected dispatch")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	_ = acceptStream(t, oldEndpoint).Close()
	if err := <-activated; err != nil {
		t.Fatal(err)
	}
	if got := broker.Stats().Dropped; got != 0 {
		t.Fatalf("Dropped = %d, want 0", got)
	}
}

func TestStreamBrokerSingleBindGateAndAtomicTargetSwap(t *testing.T) {
	broker, err := ListenStream(context.Background(), "tcp", "127.0.0.1:0", 8)
	if err != nil {
		t.Fatalf("ListenStream() error = %v", err)
	}
	defer broker.Close()
	if duplicate, err := net.Listen("tcp", broker.Addr().String()); err == nil {
		_ = duplicate.Close()
		t.Fatal("second OS bind succeeded, want broker to remain sole owner")
	}

	oldEndpoint := broker.NewEndpoint("generation-1", 8)
	newEndpoint := broker.NewEndpoint("generation-2", 8)
	if _, err := broker.Activate(oldEndpoint); err != nil {
		t.Fatalf("Activate(old) error = %v", err)
	}
	oldClient := dialTCP(t, broker.Addr().String())
	defer oldClient.Close()
	oldConn := acceptStream(t, oldEndpoint)
	defer oldConn.Close()

	if _, err := broker.Activate(newEndpoint); err != nil {
		t.Fatalf("Activate(new) error = %v", err)
	}
	newClient := dialTCP(t, broker.Addr().String())
	defer newClient.Close()
	newConn := acceptStream(t, newEndpoint)
	defer newConn.Close()

	if oldEndpoint.Generation() != "generation-1" || newEndpoint.Generation() != "generation-2" {
		t.Fatalf("endpoint generations = %q/%q", oldEndpoint.Generation(), newEndpoint.Generation())
	}
	if _, err := oldClient.Write([]byte("old-session")); err != nil {
		t.Fatalf("old client Write() error = %v", err)
	}
	buf := make([]byte, len("old-session"))
	if _, err := oldConn.Read(buf); err != nil {
		t.Fatalf("old accepted connection Read() error = %v", err)
	}
	if string(buf) != "old-session" {
		t.Fatalf("old accepted payload = %q", buf)
	}
}

func TestStreamBrokerDropsWhenActiveEndpointBackpressures(t *testing.T) {
	broker, err := ListenStream(context.Background(), "tcp", "127.0.0.1:0", 1)
	if err != nil {
		t.Fatalf("ListenStream() error = %v", err)
	}
	defer broker.Close()
	endpoint := broker.NewEndpoint("generation-1", 1)
	if _, err := broker.Activate(endpoint); err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	first := dialTCP(t, broker.Addr().String())
	defer first.Close()
	second := dialTCP(t, broker.Addr().String())
	defer second.Close()
	deadline := time.Now().Add(2 * time.Second)
	for broker.Stats().Dropped == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if broker.Stats().Dropped == 0 {
		t.Fatal("broker did not account for endpoint backpressure")
	}
}

func TestStreamBrokerRejectsClosedEndpointActivation(t *testing.T) {
	broker, err := ListenStream(context.Background(), "tcp", "127.0.0.1:0", 1)
	if err != nil {
		t.Fatalf("ListenStream() error = %v", err)
	}
	defer broker.Close()
	endpoint := broker.NewEndpoint("closed", 1)
	if err := endpoint.Close(); err != nil {
		t.Fatalf("endpoint Close() error = %v", err)
	}
	if _, err := broker.Activate(endpoint); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Activate(closed) error = %v, want net.ErrClosed", err)
	}
	if broker.Active() != nil {
		t.Fatal("closed endpoint became active")
	}
}

func TestStreamBrokerTerminalErrorClosesEndpointsAndOwnedListener(t *testing.T) {
	listener := newPermanentErrorListener()
	broker := NewStreamBroker(listener)
	endpoint := broker.NewEndpoint("generation", 1)
	if _, err := broker.Activate(endpoint); err != nil {
		t.Fatal(err)
	}
	accepted := make(chan error, 1)
	go func() { _, err := endpoint.Accept(); accepted <- err }()
	close(listener.fail)
	waited := make(chan struct{})
	go func() {
		broker.Wait()
		close(waited)
	}()
	select {
	case <-waited:
	case <-time.After(time.Second):
		t.Fatal("broker accept loop spun after permanent error")
	}
	if err := <-accepted; !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Accept error = %v", err)
	}
	if !listener.isClosed() {
		t.Fatal("owned listener was not closed")
	}
	if err := broker.Close(); !errors.Is(err, errPermanentAccept) {
		t.Fatalf("Close error = %v", err)
	}
}

func TestStreamBrokerBacksOffRetryableAcceptErrors(t *testing.T) {
	listener := newTemporaryErrorListener()
	broker := NewStreamBroker(listener)
	time.Sleep(30 * time.Millisecond)
	if calls := listener.calls.Load(); calls > 20 {
		t.Fatalf("Accept calls = %d, want bounded backoff", calls)
	}
	if err := broker.Close(); err != nil {
		t.Fatal(err)
	}
}

func dialTCP(t *testing.T, address string) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatalf("Dial(%s) error = %v", address, err)
	}
	return conn
}

func acceptStream(t *testing.T, endpoint *StreamEndpoint) net.Conn {
	t.Helper()
	type result struct {
		conn net.Conn
		err  error
	}
	resultCh := make(chan result, 1)
	go func() {
		conn, err := endpoint.Accept()
		resultCh <- result{conn: conn, err: err}
	}()
	select {
	case result := <-resultCh:
		if result.err != nil {
			t.Fatalf("Accept() error = %v", result.err)
		}
		return result.conn
	case <-time.After(2 * time.Second):
		t.Fatal("Accept() timed out")
		return nil
	}
}

var errPermanentAccept = errors.New("permanent accept failure")

type permanentErrorListener struct {
	fail, closed chan struct{}
	once         sync.Once
}

func newPermanentErrorListener() *permanentErrorListener {
	return &permanentErrorListener{fail: make(chan struct{}), closed: make(chan struct{})}
}
func (l *permanentErrorListener) Accept() (net.Conn, error) { <-l.fail; return nil, errPermanentAccept }
func (l *permanentErrorListener) Close() error              { l.once.Do(func() { close(l.closed) }); return nil }
func (*permanentErrorListener) Addr() net.Addr              { return testPacketAddr("stream") }
func (l *permanentErrorListener) isClosed() bool {
	select {
	case <-l.closed:
		return true
	default:
		return false
	}
}

type temporaryErrorListener struct {
	calls  atomic.Int64
	closed chan struct{}
	once   sync.Once
}

func newTemporaryErrorListener() *temporaryErrorListener {
	return &temporaryErrorListener{closed: make(chan struct{})}
}
func (l *temporaryErrorListener) Accept() (net.Conn, error) {
	select {
	case <-l.closed:
		return nil, net.ErrClosed
	default:
		l.calls.Add(1)
		return nil, temporaryNetError{}
	}
}
func (l *temporaryErrorListener) Close() error { l.once.Do(func() { close(l.closed) }); return nil }
func (*temporaryErrorListener) Addr() net.Addr { return testPacketAddr("temporary") }

type temporaryNetError struct{}

func (temporaryNetError) Error() string   { return "temporary" }
func (temporaryNetError) Timeout() bool   { return false }
func (temporaryNetError) Temporary() bool { return true }
