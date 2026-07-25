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

func TestRelayIngressWildcardNarrowingSurvivesProcessStreamHandoff(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("stream FD handoff is supported on linux")
	}
	listener := Listener{ID: 38, Enabled: true, ListenPort: 0, TransportMode: ListenerTransportModeTLSTCP}
	parentRegistry := ingress.NewProcessStreamRegistry()
	defer parentRegistry.Close()
	parent := newRelayIngressManager(nil)
	parent.processStreams = parentRegistry
	defer parent.close()
	wildcardLease, err := parent.acquire(context.Background(), "wildcard", listener, "0.0.0.0")
	if err != nil {
		t.Fatal(err)
	}
	defer wildcardLease.release()
	concreteLease, err := parent.acquire(context.Background(), "concrete", listener, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	defer concreteLease.release()
	secondConcreteLease, err := parent.acquire(context.Background(), "concrete", listener, "127.0.0.2")
	if err != nil {
		t.Fatal(err)
	}
	defer secondConcreteLease.release()

	processID := "relay:" + wildcardLease.binding.key
	bundle, err := parentRegistry.Export()
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Close()
	if len(bundle.Descriptors) != 1 || bundle.Descriptors[0].ID != processID {
		t.Fatalf("narrowed stream descriptors = %+v, want wildcard identity %q", bundle.Descriptors, processID)
	}

	childRegistry := ingress.NewProcessStreamRegistry()
	set, err := childRegistry.Import(bundle.Descriptors, bundle.Files)
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close()
	defer childRegistry.Close()
	child := newRelayIngressManager(nil)
	child.processStreams = childRegistry
	defer child.close()
	childLease, err := child.acquire(context.Background(), "child", listener, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	defer childLease.release()
	secondChildLease, err := child.acquire(context.Background(), "child", listener, "127.0.0.2")
	if err != nil {
		t.Fatalf("acquire second concrete child ingress after first binding %q at %s: %v", childLease.binding.key, childLease.binding.stream.Addr(), err)
	}
	defer secondChildLease.release()
	if err := childRegistry.ValidateImported(); err != nil {
		t.Fatal(err)
	}
	if childLease.binding.key != wildcardLease.binding.key || childLease.requestedKey == childLease.binding.key {
		t.Fatalf("child lease = %+v, want concrete identity backed by filtered wildcard ingress", childLease)
	}
	if secondChildLease.binding != childLease.binding || secondChildLease.requestedKey == secondChildLease.binding.key {
		t.Fatalf("second child lease = %+v, want shared filtered wildcard ingress", secondChildLease)
	}
	reexported, err := childRegistry.Export()
	if err != nil {
		t.Fatal(err)
	}
	defer reexported.Close()
	if len(reexported.Descriptors) != 1 || reexported.Descriptors[0].ID != processID {
		t.Fatalf("re-exported stream descriptors = %+v, want wildcard identity %q", reexported.Descriptors, processID)
	}
}
