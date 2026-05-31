package egress

import (
	"context"
	"net"
	"strings"
	"time"

	baseegress "github.com/sakullla/nginx-reverse-emby/go-agent/internal/egress"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	basewireguard "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/wireguard"
	modulewireguard "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/wireguard"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/proxyproto"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
)

type Module struct {
	wireGuardRuntime *WireGuardRuntime
}

func NewModule(factory basewireguard.Factory) *Module {
	return &Module{wireGuardRuntime: NewWireGuardRuntime(factory)}
}

func (m *Module) Name() string {
	return "egress"
}

func (m *Module) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{Name: m.Name()}
}

func (m *Module) RegisterProviders(module.ProviderRegistry) error {
	return nil
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

func (m *Module) Apply(context.Context, module.ApplyRequest) error {
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

func (m *Module) FinalHopDialer(profiles []model.EgressProfile, wireGuardProvider relay.WireGuardRuntimeProvider) relay.FinalHopDialer {
	return NewFinalHopDialer(profiles, wireGuardProvider)
}

func NewFinalHopDialer(profiles []model.EgressProfile, wireGuardProvider relay.WireGuardRuntimeProvider) relay.FinalHopDialer {
	return finalHopDialer{
		dialer: baseegress.Dialer{
			Resolver:          baseegress.NewResolver(profiles),
			WireGuardProvider: wireGuardProvider,
		},
	}
}

type finalHopDialer struct {
	dialer baseegress.Dialer
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

func NewWireGuardRuntime(factory basewireguard.Factory) *WireGuardRuntime {
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

func (r *WireGuardRuntime) Prepare(ctx context.Context, profiles []model.EgressProfile) (*basewireguard.Transaction, relay.WireGuardRuntimeProvider, error) {
	if r == nil || r.runtime == nil {
		return nil, nil, nil
	}
	wireGuardProfiles := WireGuardProfiles(profiles)
	transaction, err := r.runtime.Prepare(ctx, wireGuardProfiles)
	if err != nil {
		return nil, nil, err
	}
	if transaction == nil {
		return nil, egressWireGuardRuntimeProvider{runtime: r.runtime}, nil
	}
	return transaction, egressWireGuardRuntimeProvider{transaction: transaction}, nil
}

func (r *WireGuardRuntime) Commit(transaction *basewireguard.Transaction, profiles []model.EgressProfile) {
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

func (r *WireGuardRuntime) Provider() relay.WireGuardRuntimeProvider {
	if r == nil || r.runtime == nil {
		return nil
	}
	return egressWireGuardRuntimeProvider{runtime: r.runtime}
}

type egressWireGuardRuntimeProvider struct {
	runtime     *modulewireguard.Runtime
	transaction *basewireguard.Transaction
}

func (p egressWireGuardRuntimeProvider) WireGuardRuntime(profileID int) (relay.WireGuardRuntime, bool) {
	if p.transaction != nil {
		return p.transaction.Runtime(profileID)
	}
	if p.runtime != nil {
		return p.runtime.Runtime(profileID)
	}
	return nil, false
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
			Mode:     basewireguard.ModeGenericWireGuard,
			Enabled:  profile.Enabled,
			Revision: profile.Revision,
		}
	}
	return model.WireGuardProfile{
		ID:         profile.ID,
		Name:       profile.Name,
		Mode:       basewireguard.ModeGenericWireGuard,
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
