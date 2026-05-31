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
	modulewireguard "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/wireguard"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/proxyproto"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
)

type Module struct {
	mu               sync.RWMutex
	wireGuardRuntime *WireGuardRuntime
	profiles         []model.EgressProfile
	resolver         Resolver
	overlayRuntime   module.OverlayRuntime
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
		Provides: []module.ProviderRef{module.ProviderFinalHopDialer, module.ProviderEgressResolver},
		Optional: []module.ProviderRef{module.ProviderOverlayRuntime},
	}
}

func (m *Module) RegisterProviders(reg module.ProviderRegistry) error {
	if err := reg.Provide(module.ProviderFinalHopDialer, m); err != nil {
		return err
	}
	return reg.Provide(module.ProviderEgressResolver, m)
}

func (m *Module) Capabilities(module.SnapshotView) []module.Capability {
	return []module.Capability{{Name: "egress_profiles", Enabled: true}}
}

func (m *Module) Health(context.Context) module.Health {
	return module.Health{Status: "healthy"}
}

func (m *Module) Start(context.Context, model.Snapshot) error {
	return nil
}

func (m *Module) Apply(ctx context.Context, req module.ApplyRequest) error {
	profiles := CloneProfiles(req.Next.EgressProfiles)
	if m != nil && m.wireGuardRuntime != nil {
		if err := m.wireGuardRuntime.Apply(ctx, profiles); err != nil {
			return err
		}
	}

	var overlayRuntime module.OverlayRuntime
	if req.Providers != nil {
		if provider, ok := req.Providers.Resolve(module.ProviderOverlayRuntime); ok {
			runtime, ok := provider.(module.OverlayRuntime)
			if !ok {
				return fmt.Errorf("provider %s has type %T, want module.OverlayRuntime", module.ProviderOverlayRuntime, provider)
			}
			overlayRuntime = runtime
		}
	}
	if overlayRuntime == nil && m != nil && m.wireGuardRuntime != nil {
		overlayRuntime = m.wireGuardRuntime.Provider()
	}
	if m == nil {
		return nil
	}
	m.mu.Lock()
	m.profiles = profiles
	m.resolver = NewResolver(profiles)
	m.overlayRuntime = overlayRuntime
	m.mu.Unlock()
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

func (m *Module) currentDialer() Dialer {
	if m == nil {
		return Dialer{Resolver: NewResolver(nil)}
	}
	m.mu.RLock()
	dialer := Dialer{Resolver: m.resolver, OverlayRuntime: m.overlayRuntime}
	m.mu.RUnlock()
	return dialer
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
	conn   proxyproto.UDPPacketConn
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
