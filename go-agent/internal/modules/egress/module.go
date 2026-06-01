package egress

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay"
	modulewireguard "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/wireguard"
)

type Module struct {
	mu               sync.RWMutex
	wireGuardRuntime *WireGuardRuntime
	profiles         []model.EgressProfile
	resolver         Resolver
	overlayRuntime   module.OverlayRuntime
	rollback         *modulewireguard.Transaction
}

func NewModule(factory ...modulewireguard.Factory) *Module {
	var create modulewireguard.Factory
	if len(factory) > 0 {
		create = factory[0]
	}
	return &Module{wireGuardRuntime: NewWireGuardRuntime(create)}
}

func (m *Module) Name() string {
	return "egress"
}

func (m *Module) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{
		Name:     m.Name(),
		Provides: []module.ProviderRef{module.ProviderFinalHopDialer, module.ProviderEgressResolver, module.ProviderEgressOverlayRuntime},
	}
}

func (m *Module) RegisterProviders(reg module.ProviderRegistry) error {
	if err := reg.Provide(module.ProviderFinalHopDialer, moduleFinalHopDialer{module: m}); err != nil {
		return err
	}
	if err := reg.Provide(module.ProviderEgressResolver, m); err != nil {
		return err
	}
	return reg.Provide(module.ProviderEgressOverlayRuntime, egressOverlayProvider{module: m})
}

func (m *Module) Capabilities(module.SnapshotView) []module.Capability {
	return []module.Capability{{Name: "egress_profiles", Enabled: true}}
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
	if m == nil {
		return nil, nil
	}
	m.mu.RLock()
	previousProfiles := CloneProfiles(m.profiles)
	previousResolver := m.resolver
	previousOverlayRuntime := m.overlayRuntime
	previousRollback := m.rollback
	m.mu.RUnlock()

	profiles := CloneProfiles(req.Next.EgressProfiles)
	runtimeProfiles := referencedEgressProfiles(req.Next)

	var wireGuardTransaction *modulewireguard.Transaction
	if m.wireGuardRuntime != nil {
		transaction, _, err := m.wireGuardRuntime.Prepare(ctx, runtimeProfiles)
		if err != nil {
			return nil, err
		}
		wireGuardTransaction = transaction
	}

	var overlayRuntime module.OverlayRuntime
	if m.wireGuardRuntime != nil {
		overlayRuntime = m.wireGuardRuntime.Provider()
	}
	if wireGuardTransaction != nil {
		overlayRuntime = egressOverlayRuntime{transaction: wireGuardTransaction}
	}

	return &egressTransaction{
		module:                 m,
		wireGuardTransaction:   wireGuardTransaction,
		runtimeProfiles:        runtimeProfiles,
		profiles:               profiles,
		resolver:               NewResolver(profiles),
		overlayRuntime:         overlayRuntime,
		previousProfiles:       previousProfiles,
		previousResolver:       previousResolver,
		previousOverlayRuntime: previousOverlayRuntime,
		previousRollback:       previousRollback,
	}, nil
}

type egressTransaction struct {
	module                 *Module
	wireGuardTransaction   *modulewireguard.Transaction
	runtimeProfiles        []model.EgressProfile
	profiles               []model.EgressProfile
	resolver               Resolver
	overlayRuntime         module.OverlayRuntime
	previousProfiles       []model.EgressProfile
	previousResolver       Resolver
	previousOverlayRuntime module.OverlayRuntime
	previousRollback       *modulewireguard.Transaction
	committed              bool
}

func (t *egressTransaction) RegisterProviders(reg module.ProviderRegistry) error {
	if t == nil {
		return nil
	}
	if err := reg.Provide(module.ProviderFinalHopDialer, preparedFinalHopDialer{
		dialer:         Dialer{Resolver: t.resolver, OverlayRuntime: t.overlayRuntime},
		rollbackDialer: Dialer{Resolver: t.previousResolver, OverlayRuntime: t.previousOverlayRuntime},
	}); err != nil {
		return err
	}
	if err := reg.Provide(module.ProviderEgressResolver, t.resolver); err != nil {
		return err
	}
	return reg.Provide(module.ProviderEgressOverlayRuntime, preparedEgressOverlayProvider{transaction: t})
}

func (t *egressTransaction) Commit() error {
	if t == nil || t.module == nil {
		return nil
	}
	if t.wireGuardTransaction != nil && t.module.wireGuardRuntime != nil {
		t.module.wireGuardRuntime.Commit(t.wireGuardTransaction, t.runtimeProfiles)
		t.overlayRuntime = t.module.wireGuardRuntime.Provider()
	}
	t.module.mu.Lock()
	t.module.profiles = t.profiles
	t.module.resolver = t.resolver
	t.module.overlayRuntime = t.overlayRuntime
	t.module.rollback = t.wireGuardTransaction
	t.committed = true
	t.module.mu.Unlock()
	return nil
}

func (t *egressTransaction) Rollback() error {
	if t == nil {
		return nil
	}
	if t.wireGuardTransaction != nil {
		t.wireGuardTransaction.Rollback()
	}
	if t.module != nil {
		t.module.mu.Lock()
		if t.committed {
			t.module.profiles = CloneProfiles(t.previousProfiles)
			t.module.resolver = t.previousResolver
			t.module.overlayRuntime = t.previousOverlayRuntime
			t.module.rollback = t.previousRollback
		} else if t.module.rollback == t.wireGuardTransaction {
			t.module.rollback = nil
		}
		t.module.mu.Unlock()
	}
	return nil
}

func (m *Module) Stop(context.Context) error {
	if m == nil || m.wireGuardRuntime == nil {
		return nil
	}
	return m.wireGuardRuntime.Close()
}

func (m *Module) WireGuardRuntime() *WireGuardRuntime {
	if m == nil {
		return nil
	}
	return m.wireGuardRuntime
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

type preparedEgressOverlayProvider struct {
	transaction *egressTransaction
}

func (p preparedEgressOverlayProvider) RestorePreviousRuntimeForRollback(ctx context.Context) error {
	if p.transaction == nil || p.transaction.module == nil {
		return nil
	}
	return egressOverlayProvider{module: p.transaction.module}.RestorePreviousRuntimeForRollback(ctx)
}

func (p preparedEgressOverlayProvider) DialContext(ctx context.Context, agentID string, profileID int, network string, address string) (net.Conn, error) {
	overlay := p.overlayRuntime()
	if overlay == nil {
		return nil, fmt.Errorf("wireguard runtime provider is required for egress profile %d", profileID)
	}
	return overlay.DialContext(ctx, agentID, profileID, network, address)
}

func (p preparedEgressOverlayProvider) ListenTCP(ctx context.Context, agentID string, profileID int, address string) (net.Listener, error) {
	overlay := p.overlayRuntime()
	if overlay == nil {
		return nil, fmt.Errorf("wireguard runtime provider is required for egress profile %d", profileID)
	}
	return overlay.ListenTCP(ctx, agentID, profileID, address)
}

func (p preparedEgressOverlayProvider) ListenUDP(ctx context.Context, agentID string, profileID int, address string) (net.PacketConn, error) {
	overlay := p.overlayRuntime()
	if overlay == nil {
		return nil, fmt.Errorf("wireguard runtime provider is required for egress profile %d", profileID)
	}
	return overlay.ListenUDP(ctx, agentID, profileID, address)
}

func (p preparedEgressOverlayProvider) overlayRuntime() module.OverlayRuntime {
	if p.transaction == nil {
		return nil
	}
	if p.transaction.committed && p.transaction.module != nil {
		return p.transaction.module.EgressOverlayRuntime()
	}
	return p.transaction.overlayRuntime
}

func (m *Module) currentDialer() Dialer {
	if m == nil {
		return Dialer{Resolver: NewResolver(nil)}
	}
	m.mu.RLock()
	dialer := Dialer{Resolver: m.resolver, OverlayRuntime: m.overlayRuntime}
	m.mu.RUnlock()
	return dialer
}

func (m *Module) EgressOverlayRuntime() module.OverlayRuntime {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	overlay := m.overlayRuntime
	m.mu.RUnlock()
	return overlay
}

type egressOverlayProvider struct {
	module *Module
}

func (p egressOverlayProvider) RestorePreviousRuntimeForRollback(ctx context.Context) error {
	if p.module == nil || p.module.wireGuardRuntime == nil {
		return nil
	}
	p.module.mu.RLock()
	rollback := p.module.rollback
	p.module.mu.RUnlock()
	if rollback == nil {
		return nil
	}
	return rollback.RestorePrevious(ctx)
}

func (p egressOverlayProvider) DialContext(ctx context.Context, agentID string, profileID int, network string, address string) (net.Conn, error) {
	overlay := p.module.EgressOverlayRuntime()
	if overlay == nil {
		return nil, fmt.Errorf("wireguard runtime provider is required for egress profile %d", profileID)
	}
	return overlay.DialContext(ctx, agentID, profileID, network, address)
}

func (p egressOverlayProvider) ListenTCP(ctx context.Context, agentID string, profileID int, address string) (net.Listener, error) {
	overlay := p.module.EgressOverlayRuntime()
	if overlay == nil {
		return nil, fmt.Errorf("wireguard runtime provider is required for egress profile %d", profileID)
	}
	return overlay.ListenTCP(ctx, agentID, profileID, address)
}

func (p egressOverlayProvider) ListenUDP(ctx context.Context, agentID string, profileID int, address string) (net.PacketConn, error) {
	overlay := p.module.EgressOverlayRuntime()
	if overlay == nil {
		return nil, fmt.Errorf("wireguard runtime provider is required for egress profile %d", profileID)
	}
	return overlay.ListenUDP(ctx, agentID, profileID, address)
}

func (m *Module) FinalHopDialer(profiles []model.EgressProfile, overlayRuntime module.OverlayRuntime) relay.FinalHopDialer {
	return NewFinalHopDialer(profiles, overlayRuntime)
}

func NewFinalHopDialer(profiles []model.EgressProfile, overlayRuntime module.OverlayRuntime) relay.FinalHopDialer {
	return finalHopDialer{
		dialer: Dialer{
			Resolver:       NewResolver(profiles),
			OverlayRuntime: overlayRuntime,
		},
	}
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

func referencedEgressProfiles(snapshot model.Snapshot) []model.EgressProfile {
	references := referencedEgressProfileIDs(snapshot)
	if len(references) == 0 {
		return nil
	}
	out := make([]model.EgressProfile, 0, len(references))
	for _, profile := range snapshot.EgressProfiles {
		if _, ok := references[profile.ID]; ok {
			out = append(out, profile)
		}
	}
	return out
}

func referencedEgressProfileIDs(snapshot model.Snapshot) map[int]struct{} {
	references := make(map[int]struct{})
	add := func(id *int) {
		if id == nil || *id <= 0 {
			return
		}
		references[*id] = struct{}{}
	}
	for _, rule := range snapshot.Rules {
		add(rule.EgressProfileID)
	}
	for _, rule := range snapshot.L4Rules {
		add(rule.EgressProfileID)
	}
	return references
}

type WireGuardRuntime struct {
	runtime *modulewireguard.Runtime
}

func NewWireGuardRuntime(factory modulewireguard.Factory) *WireGuardRuntime {
	return &WireGuardRuntime{runtime: modulewireguard.NewRuntime(factory)}
}

func NewWireGuardRuntimeFromShared(runtime *modulewireguard.Runtime) *WireGuardRuntime {
	return &WireGuardRuntime{runtime: runtime}
}

func (r *WireGuardRuntime) Apply(ctx context.Context, profiles []model.EgressProfile) error {
	if r == nil || r.runtime == nil {
		return nil
	}
	return r.runtime.Apply(ctx, WireGuardProfiles(profiles))
}

func (r *WireGuardRuntime) Prepare(ctx context.Context, profiles []model.EgressProfile) (*modulewireguard.Transaction, module.OverlayRuntime, error) {
	if r == nil || r.runtime == nil {
		return nil, nil, nil
	}
	wireGuardProfiles := WireGuardProfiles(profiles)
	transaction, err := r.runtime.Prepare(ctx, wireGuardProfiles)
	if err != nil {
		return nil, nil, err
	}
	if transaction == nil {
		return nil, egressOverlayRuntime{runtime: r.runtime}, nil
	}
	return transaction, egressOverlayRuntime{transaction: transaction}, nil
}

func (r *WireGuardRuntime) Commit(transaction *modulewireguard.Transaction, profiles []model.EgressProfile) {
	if r == nil || r.runtime == nil || transaction == nil {
		return
	}
	r.runtime.Commit(transaction, WireGuardProfiles(profiles))
}

func (r *WireGuardRuntime) Close() error {
	if r == nil || r.runtime == nil {
		return nil
	}
	return r.runtime.Close()
}

func (r *WireGuardRuntime) Provider() module.OverlayRuntime {
	if r == nil || r.runtime == nil {
		return nil
	}
	return egressOverlayRuntime{runtime: r.runtime}
}

type egressOverlayRuntime struct {
	runtime     *modulewireguard.Runtime
	transaction *modulewireguard.Transaction
}

func (p egressOverlayRuntime) DialContext(ctx context.Context, agentID string, profileID int, network string, address string) (net.Conn, error) {
	runtime, err := p.runtimeForAgent(agentID, profileID)
	if err != nil {
		return nil, err
	}
	return runtime.DialContext(ctx, network, address)
}

func (p egressOverlayRuntime) ListenTCP(ctx context.Context, agentID string, profileID int, address string) (net.Listener, error) {
	runtime, err := p.runtimeForAgent(agentID, profileID)
	if err != nil {
		return nil, err
	}
	return runtime.ListenTCP(ctx, address)
}

func (p egressOverlayRuntime) ListenUDP(ctx context.Context, agentID string, profileID int, address string) (net.PacketConn, error) {
	runtime, err := p.runtimeForAgent(agentID, profileID)
	if err != nil {
		return nil, err
	}
	return runtime.ListenUDP(ctx, address)
}

func (p egressOverlayRuntime) runtimeForAgent(agentID string, profileID int) (modulewireguard.RuntimeHandle, error) {
	if p.transaction != nil {
		if runtime, ok := p.transaction.RuntimeForAgent(agentID, profileID); ok && runtime != nil {
			return runtime, nil
		}
		return nil, fmt.Errorf("wireguard egress profile %d runtime not found", profileID)
	}
	if p.runtime != nil {
		if runtime, ok := p.runtime.RuntimeForAgent(agentID, profileID); ok && runtime != nil {
			return runtime, nil
		}
		return nil, fmt.Errorf("wireguard egress profile %d runtime not found", profileID)
	}
	return nil, fmt.Errorf("wireguard runtime provider is required for egress profile %d", profileID)
}

func WireGuardProfiles(profiles []model.EgressProfile) []model.WireGuardProfile {
	out := make([]model.WireGuardProfile, 0, len(profiles))
	for _, profile := range profiles {
		if !profile.Enabled || !strings.EqualFold(strings.TrimSpace(profile.Type), "wireguard") {
			continue
		}
		out = append(out, WireGuardProfile(profile))
	}
	return out
}

func WireGuardProfile(profile model.EgressProfile) model.WireGuardProfile {
	cfg := profile.WireGuardConfig
	if cfg == nil {
		return model.WireGuardProfile{
			ID:       profile.ID,
			Name:     profile.Name,
			Mode:     modulewireguard.ModeGenericWireGuard,
			Enabled:  profile.Enabled,
			Revision: profile.Revision,
		}
	}
	return model.WireGuardProfile{
		ID:         profile.ID,
		Name:       profile.Name,
		Mode:       modulewireguard.ModeGenericWireGuard,
		PrivateKey: cfg.PrivateKey,
		Addresses:  append([]string(nil), cfg.Addresses...),
		Peers:      cloneWireGuardPeers(cfg.Peers),
		DNS:        append([]string(nil), cfg.DNS...),
		MTU:        cfg.MTU,
		Enabled:    profile.Enabled,
		Revision:   profile.Revision,
	}
}

func CloneProfiles(profiles []model.EgressProfile) []model.EgressProfile {
	if profiles == nil {
		return nil
	}
	cloned := make([]model.EgressProfile, len(profiles))
	for i, profile := range profiles {
		cloned[i] = profile
		if profile.WireGuardConfig != nil {
			cfg := *profile.WireGuardConfig
			cfg.Addresses = append([]string(nil), profile.WireGuardConfig.Addresses...)
			cfg.Peers = cloneWireGuardPeers(profile.WireGuardConfig.Peers)
			cfg.DNS = append([]string(nil), profile.WireGuardConfig.DNS...)
			cloned[i].WireGuardConfig = &cfg
		}
	}
	return cloned
}

func cloneWireGuardPeers(peers []model.WireGuardPeer) []model.WireGuardPeer {
	if peers == nil {
		return nil
	}
	cloned := make([]model.WireGuardPeer, len(peers))
	for i, peer := range peers {
		cloned[i] = peer
		cloned[i].AllowedIPs = append([]string(nil), peer.AllowedIPs...)
		cloned[i].Reserved = append([]byte(nil), peer.Reserved...)
	}
	return cloned
}
