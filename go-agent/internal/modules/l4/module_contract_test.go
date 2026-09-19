package l4

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
)

func TestModulePublishesAndRollsBackAfterLaterCommitFailure(t *testing.T) {
	port := reserveL4ContractPort(t)
	mod := NewModule(Config{})
	failer := &l4ContractFailingModule{}
	registry := module.NewRegistry()
	if err := registry.Register(mod); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(failer); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mod.Stop(context.Background()) })

	previous := model.Snapshot{L4Rules: []model.L4Rule{{
		ID: 41, Protocol: "tcp", ListenHost: "127.0.0.1", ListenPort: port,
		Backends: []model.L4Backend{{Host: "127.0.0.1", Port: 1}}, Enabled: true,
	}}}
	if err := registry.Apply(t.Context(), model.Snapshot{}, previous); err != nil {
		t.Fatalf("Apply(previous) error = %v", err)
	}
	if provider, ok := registry.Resolve(module.ProviderDiagnosticsL4Source); !ok || provider != mod {
		t.Fatalf("diagnostics provider = %T/%v", provider, ok)
	}

	failer.err = errors.New("later commit failed")
	if err := registry.Apply(t.Context(), previous, model.Snapshot{}); !errors.Is(err, failer.err) {
		t.Fatalf("Apply(failing next) error = %v", err)
	}
	mod.mu.Lock()
	defer mod.mu.Unlock()
	if len(mod.lastRules) != 1 || mod.lastRules[0].ID != 41 || mod.server == nil {
		t.Fatalf("rollback state = rules %#v server %v", mod.lastRules, mod.server)
	}
}

type l4ContractFailingModule struct{ err error }

func (*l4ContractFailingModule) Name() string { return "later-failure" }
func (*l4ContractFailingModule) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{Name: "later-failure"}
}
func (*l4ContractFailingModule) RegisterProviders(module.ProviderRegistry) error { return nil }
func (*l4ContractFailingModule) Capabilities(module.SnapshotView) []module.Capability {
	return nil
}
func (*l4ContractFailingModule) Apply(context.Context, module.ApplyRequest) error { return nil }
func (*l4ContractFailingModule) Stop(context.Context) error                       { return nil }
func (m *l4ContractFailingModule) Prepare(context.Context, module.ApplyRequest) (module.ModuleTransaction, error) {
	return module.TransactionFuncs{CommitFunc: func() error { return m.err }}, nil
}

func reserveL4ContractPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}
