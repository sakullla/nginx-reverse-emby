package egress

import (
	"context"
	"net"
	"slices"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay"
)

type Module struct {
	mu       sync.RWMutex
	profiles []model.EgressProfile
	resolver Resolver
}

func NewModule(_ ...any) *Module {
	return &Module{}
}

func (m *Module) Name() string {
	return "egress"
}

func (m *Module) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{
		Name:     m.Name(),
		Provides: []module.ProviderRef{module.ProviderFinalHopDialer, module.ProviderEgressResolver},
	}
}

func (m *Module) RegisterProviders(reg module.ProviderRegistry) error {
	if err := reg.Provide(module.ProviderFinalHopDialer, moduleFinalHopDialer{module: m}); err != nil {
		return err
	}
	return reg.Provide(module.ProviderEgressResolver, m)
}

func (m *Module) Capabilities(module.SnapshotView) []module.Capability {
	return []module.Capability{{Name: "egress_profiles", Enabled: true}}
}

func (m *Module) Apply(ctx context.Context, req module.ApplyRequest) error {
	transaction, err := m.Prepare(ctx, req)
	if err != nil || transaction == nil {
		return err
	}
	return transaction.Commit()
}

func (m *Module) Prepare(_ context.Context, req module.ApplyRequest) (module.ModuleTransaction, error) {
	if m == nil {
		return nil, nil
	}
	m.mu.RLock()
	previousProfiles := CloneProfiles(m.profiles)
	previousResolver := m.resolver
	m.mu.RUnlock()

	profiles := CloneProfiles(req.Next.EgressProfiles)
	return &egressTransaction{
		module:           m,
		profiles:         profiles,
		resolver:         NewResolver(profiles),
		previousProfiles: previousProfiles,
		previousResolver: previousResolver,
	}, nil
}

type egressTransaction struct {
	module           *Module
	profiles         []model.EgressProfile
	resolver         Resolver
	previousProfiles []model.EgressProfile
	previousResolver Resolver
	committed        bool
	destroyed        bool
}

func (t *egressTransaction) RegisterProviders(reg module.ProviderRegistry) error {
	if t == nil {
		return nil
	}
	if err := reg.Provide(module.ProviderFinalHopDialer, preparedFinalHopDialer{
		dialer:         Dialer{Resolver: t.resolver},
		rollbackDialer: Dialer{Resolver: t.previousResolver},
	}); err != nil {
		return err
	}
	return reg.Provide(module.ProviderEgressResolver, t.resolver)
}

func (*egressTransaction) Ready(context.Context) error { return nil }

func (t *egressTransaction) Publish() {
	if t == nil || t.module == nil || t.committed {
		return
	}
	t.module.mu.Lock()
	t.module.profiles = t.profiles
	t.module.resolver = t.resolver
	t.committed = true
	t.module.mu.Unlock()
}

func (t *egressTransaction) Destroy(context.Context) error {
	if t != nil {
		t.destroyed = true
	}
	return nil
}

func (t *egressTransaction) Commit() error {
	if err := t.Ready(context.Background()); err != nil {
		return err
	}
	t.Publish()
	return nil
}

func (t *egressTransaction) Rollback() error {
	if t == nil || t.destroyed {
		return nil
	}
	if t.module != nil {
		t.module.mu.Lock()
		if t.committed {
			t.module.profiles = CloneProfiles(t.previousProfiles)
			t.module.resolver = t.previousResolver
		}
		t.module.mu.Unlock()
	}
	t.committed = false
	return t.Destroy(context.Background())
}

func (m *Module) Stop(context.Context) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	m.profiles = nil
	m.resolver = Resolver{}
	m.mu.Unlock()
	return nil
}

func (m *Module) Resolve(id *int, network string) (model.EgressProfile, bool, error) {
	if m == nil {
		return NewResolver(nil).Resolve(id, network)
	}
	m.mu.RLock()
	resolver := m.resolver
	m.mu.RUnlock()
	return resolver.Resolve(id, network)
}

func (m *Module) DialTCP(ctx context.Context, target string, id *int) (net.Conn, error) {
	return m.currentDialer().DialTCP(ctx, target, id)
}

func (m *Module) OpenUDP(ctx context.Context, target string, id *int) (relay.UDPPacketPeer, error) {
	conn, err := m.currentDialer().DialUDP(ctx, target, id)
	if err != nil {
		return nil, err
	}
	return udpPacketConn{conn: conn, target: target}, nil
}

type moduleFinalHopDialer struct {
	module *Module
}

func (d moduleFinalHopDialer) DialTCP(ctx context.Context, target string, id *int) (net.Conn, error) {
	return d.module.DialTCP(ctx, target, id)
}

func (d moduleFinalHopDialer) OpenUDP(ctx context.Context, target string, id *int) (module.UDPPeer, error) {
	return d.module.OpenUDP(ctx, target, id)
}

type preparedFinalHopDialer struct {
	dialer         Dialer
	rollbackDialer Dialer
}

func (d preparedFinalHopDialer) DialTCP(ctx context.Context, target string, id *int) (net.Conn, error) {
	return d.dialer.DialTCP(ctx, target, id)
}

func (d preparedFinalHopDialer) OpenUDP(ctx context.Context, target string, id *int) (module.UDPPeer, error) {
	conn, err := d.dialer.DialUDP(ctx, target, id)
	if err != nil {
		return nil, err
	}
	return udpPacketConn{conn: conn, target: target}, nil
}

func (d preparedFinalHopDialer) PreviousFinalHopDialerForRollback() any {
	return preparedFinalHopDialer{dialer: d.rollbackDialer}
}

func (m *Module) currentDialer() Dialer {
	if m == nil {
		return Dialer{Resolver: NewResolver(nil)}
	}
	m.mu.RLock()
	dialer := Dialer{Resolver: m.resolver}
	m.mu.RUnlock()
	return dialer
}

func (m *Module) FinalHopDialer(profiles []model.EgressProfile) relay.FinalHopDialer {
	return NewFinalHopDialer(profiles)
}

func NewFinalHopDialer(profiles []model.EgressProfile) relay.FinalHopDialer {
	return finalHopDialer{dialer: Dialer{Resolver: NewResolver(profiles)}}
}

type finalHopDialer struct {
	dialer Dialer
}

func (d finalHopDialer) DialTCP(ctx context.Context, target string, id *int) (net.Conn, error) {
	return d.dialer.DialTCP(ctx, target, id)
}

func (d finalHopDialer) OpenUDP(ctx context.Context, target string, id *int) (relay.UDPPacketPeer, error) {
	conn, err := d.dialer.DialUDP(ctx, target, id)
	if err != nil {
		return nil, err
	}
	return udpPacketConn{conn: conn, target: target}, nil
}

type udpPacketConn struct {
	conn   model.UDPPacketConn
	target string
}

func (c udpPacketConn) Close() error { return c.conn.Close() }

func (c udpPacketConn) SetReadDeadline(deadline time.Time) error {
	return c.conn.SetReadDeadline(deadline)
}

func (c udpPacketConn) SetWriteDeadline(deadline time.Time) error {
	return c.conn.SetWriteDeadline(deadline)
}

func (c udpPacketConn) ReadPacket() ([]byte, error) {
	_, payload, err := c.conn.ReadPacket()
	return payload, err
}

func (c udpPacketConn) WritePacket(payload []byte) error {
	return c.conn.WritePacket(c.target, payload)
}

func CloneProfiles(profiles []model.EgressProfile) []model.EgressProfile {
	return slices.Clone(profiles)
}

var _ module.GenerationTransaction = (*egressTransaction)(nil)
