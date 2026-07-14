package hotrestart

import (
	"errors"
	"net"
	"runtime"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/platform"
)

func TestStreamHandoffKeepsInheritedListenerGatedUntilActivation(t *testing.T) {
	requireStreamHandoff(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	oldClient, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	oldServer, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer oldClient.Close()
	defer oldServer.Close()

	bundle, err := ExportStreamListeners(map[string]net.Listener{"http:443": listener})
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Close()
	set, err := ImportStreamListeners(bundle.Descriptors, bundle.Files)
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close()
	for index, file := range bundle.Files {
		if file != nil {
			t.Fatalf("source listener file %d was not consumed", index)
		}
	}
	if err := bundle.Close(); err != nil {
		t.Fatal(err)
	}
	inherited := set.Listeners["http:443"]

	accepted := make(chan net.Conn, 1)
	acceptErr := make(chan error, 1)
	go func() {
		conn, err := inherited.Accept()
		if err != nil {
			acceptErr <- err
			return
		}
		accepted <- conn
	}()
	newClient, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer newClient.Close()
	select {
	case <-accepted:
		t.Fatal("inherited listener accepted before activation")
	case err := <-acceptErr:
		t.Fatalf("inherited listener failed before activation: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	set.ActivateAll()
	var newServer net.Conn
	select {
	case newServer = <-accepted:
	case err := <-acceptErr:
		t.Fatal(err)
	case <-time.After(5 * time.Second):
		t.Fatal("inherited listener did not accept after activation")
	}
	defer newServer.Close()
	if _, err := oldClient.Write([]byte("old")); err != nil {
		t.Fatalf("old connection was interrupted by handoff: %v", err)
	}
	buffer := make([]byte, 3)
	if _, err := oldServer.Read(buffer); err != nil || string(buffer) != "old" {
		t.Fatalf("old connection read = %q, %v", buffer, err)
	}
}

func TestImportStreamListenersRejectsDescriptorIdentityErrors(t *testing.T) {
	requireStreamHandoff(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	bundle, err := ExportStreamListeners(map[string]net.Listener{"one": listener})
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Close()

	wrongAddress := append([]StreamDescriptor(nil), bundle.Descriptors...)
	wrongAddress[0].Address = "127.0.0.1:1"
	if _, err := ImportStreamListeners(wrongAddress, bundle.Files); err == nil {
		t.Fatal("ImportStreamListeners() accepted mismatched address")
	}
	duplicate := append(append([]StreamDescriptor(nil), bundle.Descriptors...), bundle.Descriptors[0])
	if _, err := ImportStreamListeners(duplicate, bundle.Files); err == nil {
		t.Fatal("ImportStreamListeners() accepted duplicate descriptor/file")
	}
}

func TestGatedListenerCloseUnblocksPreActivationAccept(t *testing.T) {
	left, right := net.Pipe()
	_ = left.Close()
	_ = right.Close()
	listener := &blockingTestListener{closed: make(chan struct{})}
	gated := newGatedListener(listener)
	done := make(chan error, 1)
	go func() {
		_, err := gated.Accept()
		done <- err
	}()
	if err := gated.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("Accept() error = %v, want net.ErrClosed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close() did not unblock gated Accept")
	}
}

type blockingTestListener struct{ closed chan struct{} }

func (l *blockingTestListener) Accept() (net.Conn, error) { <-l.closed; return nil, net.ErrClosed }
func (l *blockingTestListener) Close() error              { close(l.closed); return nil }
func (*blockingTestListener) Addr() net.Addr              { return testAddr("blocking") }

type testAddr string

func (a testAddr) Network() string { return string(a) }
func (a testAddr) String() string  { return string(a) }

func requireStreamHandoff(t *testing.T) {
	t.Helper()
	if !platform.SupportsHotRestart() {
		t.Skipf("stream FD handoff is unsupported on %s/%s", runtime.GOOS, runtime.GOARCH)
	}
}
