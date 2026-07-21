package relay

import (
	"context"
	"runtime"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/ingress"
)

func TestRelayIngressClaimsInheritedProcessStreamBinding(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("stream FD handoff is supported on linux")
	}
	listener := Listener{ID: 37, Enabled: true, ListenPort: 0, TransportMode: ListenerTransportModeTLSTCP}
	parentRegistry := ingress.NewProcessStreamRegistry()
	parent := newRelayIngressManager(nil)
	parent.processStreams = parentRegistry
	parentLease, err := parent.acquire(context.Background(), "parent", listener, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	defer parentLease.release()
	defer parent.close()

	bundle, err := parentRegistry.Export()
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Close()
	childRegistry := ingress.NewProcessStreamRegistry()
	set, err := childRegistry.Import(bundle.Descriptors, bundle.Files)
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close()
	child := newRelayIngressManager(nil)
	child.processStreams = childRegistry
	childLease, err := child.acquire(context.Background(), "child", listener, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	defer childLease.release()
	defer child.close()
	if err := childRegistry.ValidateImported(); err != nil {
		t.Fatal(err)
	}
	if childLease.binding.stream.Addr().String() != parentLease.binding.stream.Addr().String() {
		t.Fatalf("child address = %s, parent address = %s", childLease.binding.stream.Addr(), parentLease.binding.stream.Addr())
	}
}
