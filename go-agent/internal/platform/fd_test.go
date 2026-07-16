//go:build integration

package platform

import (
	"net"
	"os"
	"runtime"
	"testing"
)

func TestProcessIdentityIsStableForCurrentIncarnation(t *testing.T) {
	first, ok := ProcessIdentity(os.Getpid())
	if !ok || first == "" {
		t.Fatal("current process identity is unavailable")
	}
	second, ok := ProcessIdentity(os.Getpid())
	if !ok || second != first {
		t.Fatalf("process identity changed from %q to %q", first, second)
	}
}

func TestListenerFileRoundTrip(t *testing.T) {
	if !SupportsHotRestart() {
		if runtime.GOOS == "linux" && (runtime.GOARCH == "amd64" || runtime.GOARCH == "arm64") {
			t.Fatal("supported linux platform reported hot restart unavailable")
		}
		return
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	file, err := ListenerFile(listener)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	inherited, err := ListenerFromFile(file)
	if err != nil {
		t.Fatal(err)
	}
	defer inherited.Close()
	if inherited.Addr().Network() != listener.Addr().Network() || inherited.Addr().String() != listener.Addr().String() {
		t.Fatalf("inherited address = %s/%s, want %s/%s", inherited.Addr().Network(), inherited.Addr(), listener.Addr().Network(), listener.Addr())
	}
}
