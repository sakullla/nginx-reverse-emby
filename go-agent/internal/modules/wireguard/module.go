package wireguard

import (
	"context"
	"net"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
)

type Module struct {
	runtime *Runtime
	pending *Transaction
}

func NewModule(runtime *Runtime) *Module {
	return &Module{runtime: runtime}
}

func (m *Module) Name() string {
	return "wireguard"
}

func (m *Module) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{
		Name:     m.Name(),
		Provides: []module.ProviderRef{module.ProviderOverlayRuntime, module.ProviderTransparentListener},
	}
}

func (m *Module) RegisterProviders(reg module.ProviderRegistry) error {
	if m == nil || m.runtime == nil {
		return nil
	}
	if err := reg.Provide(module.ProviderOverlayRuntime, moduleOverlayProvider{module: m}); err != nil {
		return err
	}
	return reg.Provide(module.ProviderTransparentListener, moduleTransparentListenerProvider{module: m})
}

func (m *Module) Capabilities(module.SnapshotView) []module.Capability {
	return []module.Capability{{Name: "wireguard", Enabled: true}}
}

func (m *Module) Health(context.Context) module.Health {
	if m == nil || m.runtime == nil {
		return module.Health{Status: "degraded", Message: "wireguard runtime is not configured"}
	}
	return module.Health{Status: "healthy"}
}

func (m *Module) Start(ctx context.Context, snapshot model.Snapshot) error {
	return m.Apply(ctx, module.ApplyRequest{Next: snapshot})
}

func (m *Module) Apply(ctx context.Context, req module.ApplyRequest) error {
	transaction, err := m.Prepare(ctx, req)
	if err != nil {
		return err
	}
	if transaction == nil {
		return nil
	}
	return transaction.Commit()
}

func (m *Module) Prepare(ctx context.Context, req module.ApplyRequest) (module.ModuleTransaction, error) {
	if m == nil || m.runtime == nil {
		return nil, nil
	}
	transaction, err := m.runtime.Prepare(ctx, req.Next.WireGuardProfiles)
	if err != nil {
		return nil, err
	}
	if transaction == nil {
		return nil, nil
	}
	m.pending = transaction
	profiles := CloneWireGuardProfiles(req.Next.WireGuardProfiles)
	return module.TransactionFuncs{
		CommitFunc: func() error {
			m.runtime.Commit(transaction, profiles)
			if m.pending == transaction {
				m.pending = nil
			}
			return nil
		},
		RollbackFunc: func() error {
			transaction.Rollback()
			if m.pending == transaction {
				m.pending = nil
			}
			return nil
		},
	}, nil
}

func (m *Module) Stop(context.Context) error {
	if m == nil || m.runtime == nil {
		return nil
	}
	return m.runtime.Close()
}

func (m *Module) runtimeForAgent(agentID string, profileID int) (RuntimeHandle, error) {
	if m == nil || m.runtime == nil {
		return nil, net.ErrClosed
	}
	if m.pending != nil {
		if runtime, ok := m.pending.RuntimeForAgent(agentID, profileID); ok {
			return runtime, nil
		}
	}
	runtime, ok := m.runtime.RuntimeForAgent(agentID, profileID)
	if !ok {
		return nil, net.ErrClosed
	}
	return runtime, nil
}

type moduleOverlayProvider struct {
	module *Module
}

func (p moduleOverlayProvider) DialContext(ctx context.Context, agentID string, profileID int, network string, address string) (net.Conn, error) {
	runtime, err := p.module.runtimeForAgent(agentID, profileID)
	if err != nil {
		return nil, err
	}
	return runtime.DialContext(ctx, network, address)
}

func (p moduleOverlayProvider) ListenTCP(ctx context.Context, agentID string, profileID int, address string) (net.Listener, error) {
	runtime, err := p.module.runtimeForAgent(agentID, profileID)
	if err != nil {
		return nil, err
	}
	return runtime.ListenTCP(ctx, address)
}

func (p moduleOverlayProvider) ListenUDP(ctx context.Context, agentID string, profileID int, address string) (net.PacketConn, error) {
	runtime, err := p.module.runtimeForAgent(agentID, profileID)
	if err != nil {
		return nil, err
	}
	return runtime.ListenUDP(ctx, address)
}

type moduleTransparentListenerProvider struct {
	module *Module
}

func (p moduleTransparentListenerProvider) ListenTransparentTCP(ctx context.Context, agentID string, profileID int) (net.Listener, error) {
	runtime, err := p.module.runtimeForAgent(agentID, profileID)
	if err != nil {
		return nil, err
	}
	return runtime.ListenTransparentTCP(ctx)
}

func (p moduleTransparentListenerProvider) ListenTransparentUDP(ctx context.Context, agentID string, profileID int, address string) (module.TransparentUDPConn, error) {
	runtime, err := p.module.runtimeForAgent(agentID, profileID)
	if err != nil {
		return nil, err
	}
	conn, err := runtime.ListenTransparentUDP(ctx, address)
	if err != nil {
		return nil, err
	}
	return transparentUDPConnAdapter{conn: conn}, nil
}
