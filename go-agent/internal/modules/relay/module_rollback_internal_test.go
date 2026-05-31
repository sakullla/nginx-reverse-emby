package relay

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
)

func TestModuleRollbackRestoresPreviousRuntimeWithCommittedFinalHopProvider(t *testing.T) {
	provider := newFakeTLSMaterialProvider()
	listener, _ := newRelayEndpoint(t, provider, 301, "rollback-finalhop", "pin_only", true, false)
	listener.AgentID = "agent-a"
	listener.AgentName = "node-a"

	previousDialer := &labeledFinalHopDialer{}
	nextDialer := &labeledFinalHopDialer{}
	finalHop := &transactionalFinalHopProviderModule{
		committed: previousDialer,
		next:      rollbackAwareFinalHopDialer{current: nextDialer, previous: previousDialer},
	}
	relayModule := NewModule(Config{AgentID: "agent-a", AgentName: "node-a"})
	registry := module.NewRegistry()
	mustRegisterInternal(t, registry, internalStaticProviderModule{name: "certs", provides: module.ProviderTLSMaterial, provider: provider})
	mustRegisterInternal(t, registry, finalHop)
	mustRegisterInternal(t, registry, relayModule)

	previous := model.Snapshot{
		RelayListeners: []model.RelayListener{listener},
		EgressProfiles: []model.EgressProfile{{ID: 1, Name: "previous", Type: "direct", Enabled: true}},
	}
	if err := registry.Apply(context.Background(), model.Snapshot{}, previous); err != nil {
		t.Fatalf("initial Apply() error = %v", err)
	}

	failErr := errors.New("later commit failed")
	mustRegisterInternal(t, registry, internalCommitFailingModule{name: "later-transaction", err: failErr})
	nextListener := listener
	nextListener.ListenPort = pickFreeTCPPort(t)
	nextListener.Revision++
	next := model.Snapshot{
		RelayListeners: []model.RelayListener{nextListener},
		EgressProfiles: []model.EgressProfile{{ID: 2, Name: "next", Type: "direct", Enabled: true}},
	}
	err := registry.Apply(context.Background(), previous, next)
	if !errors.Is(err, failErr) {
		t.Fatalf("Apply() error = %v, want later commit failure", err)
	}

	relayModule.mu.Lock()
	restored := relayModule.runtime
	relayModule.mu.Unlock()
	if restored == nil {
		t.Fatal("relay runtime was not restored")
	}
	profileID := 1
	conn, _, err := restored.finalHopSelector.dialTCP(context.Background(), "127.0.0.1:1", DialOptions{EgressProfileID: &profileID})
	if err != nil {
		t.Fatalf("restored final hop DialTCP() error = %v", err)
	}
	_ = conn.Close()
	if previousDialer.calls != 1 {
		t.Fatalf("previous final-hop calls = %d, want 1", previousDialer.calls)
	}
	if nextDialer.calls != 0 {
		t.Fatalf("next final-hop calls = %d, want 0", nextDialer.calls)
	}
}

type labeledFinalHopDialer struct {
	calls int
}

func (d *labeledFinalHopDialer) DialTCP(context.Context, string, *int) (net.Conn, error) {
	d.calls++
	left, right := net.Pipe()
	_ = right.Close()
	return left, nil
}

func (*labeledFinalHopDialer) OpenUDP(context.Context, string, *int) (UDPPacketPeer, error) {
	return nil, errors.New("unexpected udp final hop")
}

type rollbackAwareFinalHopDialer struct {
	current  *labeledFinalHopDialer
	previous *labeledFinalHopDialer
}

func (d rollbackAwareFinalHopDialer) DialTCP(ctx context.Context, target string, id *int) (net.Conn, error) {
	return d.current.DialTCP(ctx, target, id)
}

func (d rollbackAwareFinalHopDialer) OpenUDP(ctx context.Context, target string, id *int) (UDPPacketPeer, error) {
	return d.current.OpenUDP(ctx, target, id)
}

func (d rollbackAwareFinalHopDialer) PreviousFinalHopDialerForRollback() any {
	return d.previous
}

type transactionalFinalHopProviderModule struct {
	committed FinalHopDialer
	next      FinalHopDialer
}

func (*transactionalFinalHopProviderModule) Name() string { return "egress" }

func (m *transactionalFinalHopProviderModule) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{Name: m.Name(), Provides: []module.ProviderRef{module.ProviderFinalHopDialer}}
}

func (m *transactionalFinalHopProviderModule) RegisterProviders(reg module.ProviderRegistry) error {
	return reg.Provide(module.ProviderFinalHopDialer, m.committed)
}

func (*transactionalFinalHopProviderModule) Capabilities(module.SnapshotView) []module.Capability {
	return nil
}

func (*transactionalFinalHopProviderModule) Apply(context.Context, module.ApplyRequest) error {
	return nil
}

func (m *transactionalFinalHopProviderModule) Prepare(_ context.Context, req module.ApplyRequest) (module.ModuleTransaction, error) {
	if len(req.Next.EgressProfiles) == 0 || req.Next.EgressProfiles[0].Name != "next" {
		return nil, nil
	}
	previous := m.committed
	return &transactionalFinalHopProviderTransaction{module: m, previous: previous, next: m.next}, nil
}

func (*transactionalFinalHopProviderModule) Stop(context.Context) error { return nil }

type transactionalFinalHopProviderTransaction struct {
	module    *transactionalFinalHopProviderModule
	previous  FinalHopDialer
	next      FinalHopDialer
	committed bool
}

func (t *transactionalFinalHopProviderTransaction) RegisterProviders(reg module.ProviderRegistry) error {
	return reg.Provide(module.ProviderFinalHopDialer, t.next)
}

func (t *transactionalFinalHopProviderTransaction) Commit() error {
	t.module.committed = t.next
	t.committed = true
	return nil
}

func (t *transactionalFinalHopProviderTransaction) Rollback() error {
	if t.committed {
		t.module.committed = t.previous
	}
	return nil
}

type internalStaticProviderModule struct {
	name     string
	provides module.ProviderRef
	provider any
}

func (m internalStaticProviderModule) Name() string { return m.name }

func (m internalStaticProviderModule) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{Name: m.name, Provides: []module.ProviderRef{m.provides}}
}

func (m internalStaticProviderModule) RegisterProviders(reg module.ProviderRegistry) error {
	return reg.Provide(m.provides, m.provider)
}

func (internalStaticProviderModule) Capabilities(module.SnapshotView) []module.Capability {
	return nil
}

func (internalStaticProviderModule) Apply(context.Context, module.ApplyRequest) error { return nil }

func (internalStaticProviderModule) Stop(context.Context) error { return nil }

type internalCommitFailingModule struct {
	name string
	err  error
}

func (m internalCommitFailingModule) Name() string { return m.name }

func (m internalCommitFailingModule) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{Name: m.name}
}

func (internalCommitFailingModule) RegisterProviders(module.ProviderRegistry) error { return nil }

func (internalCommitFailingModule) Capabilities(module.SnapshotView) []module.Capability {
	return nil
}

func (internalCommitFailingModule) Apply(context.Context, module.ApplyRequest) error { return nil }

func (m internalCommitFailingModule) Prepare(context.Context, module.ApplyRequest) (module.ModuleTransaction, error) {
	return module.TransactionFuncs{CommitFunc: func() error { return m.err }}, nil
}

func (internalCommitFailingModule) Stop(context.Context) error { return nil }

func mustRegisterInternal(t *testing.T, registry *module.Registry, mod any) {
	t.Helper()
	if err := registry.Register(mod); err != nil {
		t.Fatalf("Register(%T) error = %v", mod, err)
	}
}
