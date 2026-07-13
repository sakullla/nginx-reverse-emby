package ingress

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

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

func TestStreamBrokerStopsAfterPermanentAcceptError(t *testing.T) {
	broker := NewStreamBroker(permanentErrorListener{})
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
	if err := broker.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
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

type permanentErrorListener struct{}

func (permanentErrorListener) Accept() (net.Conn, error) {
	return nil, errors.New("permanent accept failure")
}
func (permanentErrorListener) Close() error   { return nil }
func (permanentErrorListener) Addr() net.Addr { return testPacketAddr("stream") }
