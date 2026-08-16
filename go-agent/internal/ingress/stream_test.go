package ingress

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestStreamBrokerHandsNewConnectionsToActivatedEndpoint(t *testing.T) {
	broker, err := ListenStream(context.Background(), "tcp", "127.0.0.1:0", 2)
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	oldEndpoint := broker.NewEndpoint("old", 1)
	newEndpoint := broker.NewEndpoint("new", 1)
	if _, err := broker.Activate(oldEndpoint); err != nil {
		t.Fatal(err)
	}
	oldClient := dialStreamContract(t, broker.Addr().String())
	defer oldClient.Close()
	defer acceptStreamContract(t, oldEndpoint).Close()
	if _, err := broker.Activate(newEndpoint); err != nil {
		t.Fatal(err)
	}
	newClient := dialStreamContract(t, broker.Addr().String())
	defer newClient.Close()
	defer acceptStreamContract(t, newEndpoint).Close()
}

func dialStreamContract(t *testing.T, address string) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatalf("Dial(%s) error = %v", address, err)
	}
	return conn
}

func acceptStreamContract(t *testing.T, endpoint *StreamEndpoint) net.Conn {
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
	case got := <-resultCh:
		if got.err != nil {
			t.Fatalf("Accept() error = %v", got.err)
		}
		return got.conn
	case <-time.After(2 * time.Second):
		t.Fatal("Accept() timed out")
		return nil
	}
}
