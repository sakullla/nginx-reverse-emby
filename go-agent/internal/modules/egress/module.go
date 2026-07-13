package egress

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
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
	factory          modulewireguard.Factory
	wireGuardRuntime *WireGuardRuntime
	profiles         []model.EgressProfile
	resolver         Resolver
	overlayRuntime   module.OverlayRuntime
	retiredRuntimes  []*WireGuardRuntime
}

func NewModule(factory ...modulewireguard.Factory) *Module {
	var create modulewireguard.Factory
	if len(factory) > 0 {
		create = factory[0]
	}
	return &Module{factory: create, wireGuardRuntime: NewWireGuardRuntime(create)}
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
	if err := transaction.Commit(); err != nil {
		return err
	}
	if finalizer, ok := transaction.(interface{ FinalizeCommitSuccess() }); ok {
		finalizer.FinalizeCommitSuccess()
	}
	return nil
}

func (m *Module) Prepare(ctx context.Context, req module.ApplyRequest) (module.ModuleTransaction, error) {
	if m == nil {
		return nil, nil
	}
	m.mu.RLock()
	previousWireGuardRuntime := m.wireGuardRuntime
	previousProfiles := CloneProfiles(m.profiles)
	previousResolver := m.resolver
	previousOverlayRuntime := m.overlayRuntime
	m.mu.RUnlock()

	profiles := CloneProfiles(req.Next.EgressProfiles)
	runtimeProfiles := referencedEgressProfiles(req.Next)
	candidateWireGuardRuntime := NewWireGuardRuntime(m.factory)
	if err := candidateWireGuardRuntime.Apply(ctx, runtimeProfiles); err != nil {
		_ = candidateWireGuardRuntime.Close()
		return nil, err
	}
	overlayRuntime := candidateWireGuardRuntime.Provider()

	return &egressTransaction{
		module:                   m,
		wireGuardRuntime:         candidateWireGuardRuntime,
		profiles:                 profiles,
		resolver:                 NewResolver(profiles),
		overlayRuntime:           overlayRuntime,
		previousWireGuardRuntime: previousWireGuardRuntime,
		previousProfiles:         previousProfiles,
		previousResolver:         previousResolver,
		previousOverlayRuntime:   previousOverlayRuntime,
	}, nil
}

type egressTransaction struct {
	module                   *Module
	wireGuardRuntime         *WireGuardRuntime
	profiles                 []model.EgressProfile
	resolver                 Resolver
	overlayRuntime           module.OverlayRuntime
	previousWireGuardRuntime *WireGuardRuntime
	previousProfiles         []model.EgressProfile
	previousResolver         Resolver
	previousOverlayRuntime   module.OverlayRuntime
	committed                bool
	destroyed                bool
	finalized                bool
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

func (*egressTransaction) Ready(context.Context) error { return nil }

func (t *egressTransaction) Publish() {
	if t == nil || t.module == nil {
		return
	}
	if t.committed {
		return
	}
	t.module.mu.Lock()
	t.module.wireGuardRuntime = t.wireGuardRuntime
	t.module.profiles = t.profiles
	t.module.resolver = t.resolver
	t.module.overlayRuntime = t.overlayRuntime
	t.committed = true
	t.module.mu.Unlock()
	return
}

func (t *egressTransaction) Destroy(context.Context) error {
	if t == nil || t.destroyed {
		return nil
	}
	t.destroyed = true
	if t.wireGuardRuntime == nil {
		return nil
	}
	return t.wireGuardRuntime.Close()
}

func (t *egressTransaction) Commit() error {
	if err := t.Ready(context.Background()); err != nil {
		return err
	}
	t.Publish()
	return nil
}

func (t *egressTransaction) FinalizeCommitSuccess() {
	if t == nil || !t.committed || t.finalized {
		return
	}
	t.finalized = true
	previous := t.previousWireGuardRuntime
	t.previousWireGuardRuntime = nil
	if t.module != nil {
		t.module.releaseLegacyWireGuardRuntime(previous)
	}
}

func (t *egressTransaction) Rollback() error {
	if t == nil || t.destroyed {
		return nil
	}
	if t.module != nil {
		t.module.mu.Lock()
		if t.committed {
			t.module.wireGuardRuntime = t.previousWireGuardRuntime
			t.module.profiles = CloneProfiles(t.previousProfiles)
			t.module.resolver = t.previousResolver
			t.module.overlayRuntime = t.previousOverlayRuntime
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
	runtimes := append([]*WireGuardRuntime{m.wireGuardRuntime}, m.retiredRuntimes...)
	m.wireGuardRuntime = nil
	m.retiredRuntimes = nil
	m.mu.Unlock()
	var closeErr error
	for _, runtime := range runtimes {
		if runtime != nil {
			closeErr = errors.Join(closeErr, runtime.Close())
		}
	}
	return closeErr
}

func (m *Module) releaseLegacyWireGuardRuntime(runtime *WireGuardRuntime) {
	if m == nil || runtime == nil {
		return
	}
	if err := runtime.Close(); err != nil {
		m.mu.Lock()
		m.retiredRuntimes = append(m.retiredRuntimes, runtime)
		m.mu.Unlock()
	}
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
	_ = ctx
	return nil
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
	_ = ctx
	return nil
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
		Addresses:  slices.Clone(cfg.Addresses),
		Peers:      cloneWireGuardPeers(cfg.Peers),
		DNS:        slices.Clone(cfg.DNS),
		MTU:        cfg.MTU,
		Enabled:    profile.Enabled,
		Revision:   profile.Revision,
	}
}

func CloneProfiles(profiles []model.EgressProfile) []model.EgressProfile {
	if profiles == nil {
		return nil
	}
	cloned := slices.Clone(profiles)
	for i, profile := range profiles {
		cloned[i].WireGuardConfig = cloneEgressWireGuardConfig(profile.WireGuardConfig)
	}
	return cloned
}

func cloneEgressWireGuardConfig(config *model.EgressWireGuardConfig) *model.EgressWireGuardConfig {
	if config == nil {
		return nil
	}
	cloned := *config
	cloned.Addresses = slices.Clone(config.Addresses)
	cloned.Peers = cloneWireGuardPeers(config.Peers)
	cloned.DNS = slices.Clone(config.DNS)
	return &cloned
}

func cloneWireGuardPeers(peers []model.WireGuardPeer) []model.WireGuardPeer {
	if peers == nil {
		return nil
	}
	cloned := slices.Clone(peers)
	for i, peer := range peers {
		cloned[i].AllowedIPs = slices.Clone(peer.AllowedIPs)
		cloned[i].Reserved = slices.Clone(peer.Reserved)
	}
	return cloned
}

var _ module.GenerationTransaction = (*egressTransaction)(nil)
