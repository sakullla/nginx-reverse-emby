package wireguard

import (
	"context"
	"net"
	"sync"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
)

type Module struct {
	mu      sync.Mutex
	runtime *Runtime
	restore *Transaction
}

func NewModule(runtime *Runtime) *Module {
	return &Module{runtime: runtime}
}

func NewManagedModule(factory Factory) *Module {
	return NewModule(NewRuntime(factory))
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
	if err := transaction.Commit(); err != nil {
		return err
	}
	if finalizer, ok := transaction.(interface {
		FinalizeCommit() error
	}); ok {
		return finalizer.FinalizeCommit()
	}
	return nil
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
	profiles := CloneWireGuardProfiles(req.Next.WireGuardProfiles)
	previousProfiles := m.runtime.Profiles()
	if transaction.HasCloseFirstReplacements() {
		m.setRestoreTransaction(transaction)
	} else {
		m.clearRestoreTransaction(nil)
	}
	return &moduleTransaction{
		module:           m,
		transaction:      transaction,
		profiles:         profiles,
		previousProfiles: previousProfiles,
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
	runtime, ok := m.runtime.RuntimeForAgent(agentID, profileID)
	if !ok {
		return nil, net.ErrClosed
	}
	return runtime, nil
}

func (m *Module) restorePreviousRuntimeForRollback(ctx context.Context) error {
	if m == nil {
		return nil
	}
	transaction := m.restoreTransaction()
	if transaction == nil {
		return nil
	}
	return transaction.RestorePrevious(ctx)
}

func (m *Module) setRestoreTransaction(transaction *Transaction) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.restore = transaction
	m.mu.Unlock()
}

func (m *Module) clearRestoreTransaction(transaction *Transaction) {
	if m == nil {
		return
	}
	m.mu.Lock()
	if transaction == nil || m.restore == transaction {
		m.restore = nil
	}
	m.mu.Unlock()
}

func (m *Module) restoreTransaction() *Transaction {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.restore
}

type moduleTransaction struct {
	module           *Module
	transaction      *Transaction
	profiles         []model.WireGuardProfile
	previousProfiles []model.WireGuardProfile
	finalized        bool
}

func (t *moduleTransaction) RegisterProviders(reg module.ProviderRegistry) error {
	if t == nil || t.transaction == nil {
		return nil
	}
	state := &transactionProviderState{}
	if err := reg.Provide(module.ProviderOverlayRuntime, transactionOverlayProvider{module: t.module, transaction: t.transaction, state: state}); err != nil {
		return err
	}
	return reg.Provide(module.ProviderTransparentListener, transactionTransparentListenerProvider{module: t.module, transaction: t.transaction, state: state})
}

func (t *moduleTransaction) Commit() error {
	return nil
}

func (t *moduleTransaction) FinalizeCommit() error {
	if t == nil || t.module == nil || t.module.runtime == nil || t.transaction == nil {
		return nil
	}
	t.module.runtime.Commit(t.transaction, t.profiles)
	t.finalized = true
	t.module.clearRestoreTransaction(t.transaction)
	return nil
}

func (t *moduleTransaction) Rollback() error {
	if t == nil || t.transaction == nil {
		return nil
	}
	t.transaction.Rollback()
	if t.finalized && t.module != nil && t.module.runtime != nil {
		t.module.runtime.storeProfiles(t.previousProfiles)
	}
	if t.module != nil {
		t.module.clearRestoreTransaction(t.transaction)
	}
	return nil
}

type moduleOverlayProvider struct {
	module *Module
}

func (p moduleOverlayProvider) RestorePreviousRuntimeForRollback(ctx context.Context) error {
	if p.module == nil {
		return nil
	}
	return p.module.restorePreviousRuntimeForRollback(ctx)
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

func (p moduleTransparentListenerProvider) RestorePreviousRuntimeForRollback(ctx context.Context) error {
	if p.module == nil {
		return nil
	}
	return p.module.restorePreviousRuntimeForRollback(ctx)
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

type transactionOverlayProvider struct {
	module      *Module
	transaction *Transaction
	state       *transactionProviderState
}

type transactionProviderState struct {
	mu               sync.Mutex
	previousRestored bool
}

func (s *transactionProviderState) restorePrevious() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.previousRestored = true
	s.mu.Unlock()
}

func (s *transactionProviderState) restoredPrevious() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.previousRestored
}

func (p transactionOverlayProvider) RestorePreviousRuntimeForRollback(ctx context.Context) error {
	if p.module == nil || p.transaction == nil {
		return nil
	}
	if p.module.restoreTransaction() == p.transaction {
		if err := p.transaction.RestorePrevious(ctx); err != nil {
			return err
		}
	}
	p.state.restorePrevious()
	return nil
}

func (p transactionOverlayProvider) DialContext(ctx context.Context, agentID string, profileID int, network string, address string) (net.Conn, error) {
	runtime, err := p.runtimeForAgent(agentID, profileID)
	if err != nil {
		return nil, err
	}
	return runtime.DialContext(ctx, network, address)
}

func (p transactionOverlayProvider) ListenTCP(ctx context.Context, agentID string, profileID int, address string) (net.Listener, error) {
	runtime, err := p.runtimeForAgent(agentID, profileID)
	if err != nil {
		return nil, err
	}
	return runtime.ListenTCP(ctx, address)
}

func (p transactionOverlayProvider) ListenUDP(ctx context.Context, agentID string, profileID int, address string) (net.PacketConn, error) {
	runtime, err := p.runtimeForAgent(agentID, profileID)
	if err != nil {
		return nil, err
	}
	return runtime.ListenUDP(ctx, address)
}

func (p transactionOverlayProvider) runtimeForAgent(agentID string, profileID int) (RuntimeHandle, error) {
	if p.state.restoredPrevious() && p.module != nil {
		return p.module.runtimeForAgent(agentID, profileID)
	}
	if p.transaction == nil {
		return nil, net.ErrClosed
	}
	runtime, ok := p.transaction.RuntimeForAgent(agentID, profileID)
	if !ok {
		return nil, net.ErrClosed
	}
	return runtime, nil
}

type transactionTransparentListenerProvider struct {
	module      *Module
	transaction *Transaction
	state       *transactionProviderState
}

func (p transactionTransparentListenerProvider) RestorePreviousRuntimeForRollback(ctx context.Context) error {
	if p.module == nil {
		return nil
	}
	return transactionOverlayProvider{module: p.module, transaction: p.transaction, state: p.state}.RestorePreviousRuntimeForRollback(ctx)
}

func (p transactionTransparentListenerProvider) ListenTransparentTCP(ctx context.Context, agentID string, profileID int) (net.Listener, error) {
	runtime, err := transactionOverlayProvider{module: p.module, transaction: p.transaction, state: p.state}.runtimeForAgent(agentID, profileID)
	if err != nil {
		return nil, err
	}
	return runtime.ListenTransparentTCP(ctx)
}

func (p transactionTransparentListenerProvider) ListenTransparentUDP(ctx context.Context, agentID string, profileID int, address string) (module.TransparentUDPConn, error) {
	runtime, err := transactionOverlayProvider{module: p.module, transaction: p.transaction, state: p.state}.runtimeForAgent(agentID, profileID)
	if err != nil {
		return nil, err
	}
	conn, err := runtime.ListenTransparentUDP(ctx, address)
	if err != nil {
		return nil, err
	}
	return transparentUDPConnAdapter{conn: conn}, nil
}
