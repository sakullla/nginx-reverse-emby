package l4

import (
	"context"
	"runtime"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/ingress"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func TestL4IngressClaimsInheritedProcessStreamBinding(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("stream FD handoff is supported on linux")
	}
	rule := model.L4Rule{ID: 37, Enabled: true, Protocol: "tcp", ListenHost: "127.0.0.1", ListenPort: 0}
	parentRegistry := ingress.NewProcessStreamRegistry()
	parent := newL4IngressManager()
	parent.processStreams = parentRegistry
	parentLease, err := parent.acquire(context.Background(), "parent", rule, &Server{})
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
	child := newL4IngressManager()
	child.processStreams = childRegistry
	childLease, err := child.acquire(context.Background(), "child", rule, &Server{})
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
