package moduleutil

import (
	"errors"
	"net"
	"sync"
	"testing"
)

func TestVirtualListenerGatesDeliveryAndSurvivesConcurrentClose(t *testing.T) {
	listener := NewVirtualListener(testAddr("virtual-stream"), 1)
	server, client := net.Pipe()
	defer client.Close()
	if err := listener.Deliver(server); !errors.Is(err, ErrVirtualEndpointInactive) {
		t.Fatalf("Deliver before Activate error = %v, want ErrVirtualEndpointInactive", err)
	}
	_ = server.Close()

	listener.Activate()
	server, client = net.Pipe()
	defer client.Close()
	if err := listener.Deliver(server); err != nil {
		t.Fatalf("Deliver after Activate error = %v", err)
	}
	accepted, err := listener.Accept()
	if err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	_ = accepted.Close()

	var workers sync.WaitGroup
	for i := 0; i < 32; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			left, right := net.Pipe()
			if err := listener.Deliver(left); err != nil {
				_ = left.Close()
			}
			_ = right.Close()
		}()
	}
	if err := listener.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	workers.Wait()
	if _, err := listener.Accept(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Accept after Close error = %v, want net.ErrClosed", err)
	}
}

type testAddr string

func (a testAddr) Network() string { return "test" }
func (a testAddr) String() string  { return string(a) }
