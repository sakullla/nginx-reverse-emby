package ingress

import (
	"context"
	"io"
	"net"
	"runtime"
	"testing"
	"time"
)

func TestProcessStreamHandoffGatesNewAcceptsAndKeepsOldConnection(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("stream FD handoff is supported on linux")
	}
	const bindingID = "http:http:handoff"
	parentRegistry := NewProcessStreamRegistry()
	parent, err := parentRegistry.NewBroker(context.Background(), bindingID, func(context.Context) (net.Listener, error) {
		return net.Listen("tcp", "127.0.0.1:0")
	})
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	parentEndpoint := parent.NewEndpoint("parent-generation", 8)
	if _, err := parent.Activate(parentEndpoint); err != nil {
		t.Fatal(err)
	}

	bundle, err := parentRegistry.Export()
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Close()
	childRegistry := NewProcessStreamRegistry()
	streamSet, err := childRegistry.Import(bundle.Descriptors, bundle.Files)
	if err != nil {
		t.Fatal(err)
	}
	defer streamSet.Close()
	defer childRegistry.Close()
	rebound := false
	child, err := childRegistry.NewBroker(context.Background(), bindingID, func(context.Context) (net.Listener, error) {
		rebound = true
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer child.Close()
	if rebound {
		t.Fatal("child rebound the inherited listener address")
	}
	childEndpoint := child.NewEndpoint("child-generation", 8)
	if _, err := child.Activate(childEndpoint); err != nil {
		t.Fatal(err)
	}
	if err := childRegistry.ValidateImported(); err != nil {
		t.Fatal(err)
	}
	reexported, err := childRegistry.Export()
	if err != nil {
		t.Fatal(err)
	}
	defer reexported.Close()
	if len(reexported.Descriptors) != 1 || reexported.Descriptors[0].ID != bindingID {
		t.Fatalf("re-exported descriptors = %+v", reexported.Descriptors)
	}

	oldClient, err := net.Dial("tcp", parent.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer oldClient.Close()
	oldServer := acceptProcessStream(t, parentEndpoint)
	defer oldServer.Close()

	if err := parentRegistry.Pause(); err != nil {
		t.Fatal(err)
	}
	if err := childRegistry.ActivateImported(); err != nil {
		t.Fatal(err)
	}
	if err := childRegistry.ActivateImported(); err != nil {
		t.Fatal(err)
	}

	newClient, err := net.Dial("tcp", parent.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer newClient.Close()
	newServer := acceptProcessStream(t, childEndpoint)
	defer newServer.Close()

	if _, err := oldClient.Write([]byte("old")); err != nil {
		t.Fatal(err)
	}
	oldPayload := make([]byte, 3)
	if _, err := io.ReadFull(oldServer, oldPayload); err != nil {
		t.Fatal(err)
	}
	if string(oldPayload) != "old" {
		t.Fatalf("old connection payload = %q", oldPayload)
	}

	if err := child.Close(); err != nil {
		t.Fatal(err)
	}
	if err := parentRegistry.Resume(); err != nil {
		t.Fatal(err)
	}
	rollbackClient, err := net.Dial("tcp", parent.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackClient.Close()
	rollbackServer := acceptProcessStream(t, parentEndpoint)
	_ = rollbackServer.Close()
}

func acceptProcessStream(t *testing.T, endpoint *StreamEndpoint) net.Conn {
	t.Helper()
	type result struct {
		conn net.Conn
		err  error
	}
	accepted := make(chan result, 1)
	go func() {
		conn, err := endpoint.Accept()
		accepted <- result{conn: conn, err: err}
	}()
	select {
	case result := <-accepted:
		if result.err != nil {
			t.Fatalf("Accept() error = %v", result.err)
		}
		return result.conn
	case <-time.After(5 * time.Second):
		t.Fatal("Accept() timed out")
		return nil
	}
}
