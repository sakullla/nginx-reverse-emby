package wireguard

import (
	"context"
	"net"
	"net/netip"
	"slices"
	"strings"
	"sync"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
)

type Runtime struct {
	mu       sync.RWMutex
	manager  *Manager
	profiles []model.WireGuardProfile
}

func NewRuntime(factory Factory) *Runtime {
	return &Runtime{
		manager: NewManager(ManagerOptions{Factory: factory}),
	}
}

func (r *Runtime) Apply(ctx context.Context, profiles []model.WireGuardProfile) error {
	if r == nil || r.manager == nil {
		return nil
	}
	if err := r.manager.Apply(ctx, profiles); err != nil {
		return err
	}
	r.storeProfiles(profiles)
	return nil
}

func (r *Runtime) Prepare(ctx context.Context, profiles []model.WireGuardProfile) (*Transaction, error) {
	if r == nil || r.manager == nil {
		return nil, nil
	}
	return r.manager.Prepare(ctx, profiles)
}

func (r *Runtime) Recreate(ctx context.Context, profiles []model.WireGuardProfile) error {
	if r == nil || r.manager == nil {
		return nil
	}
	if err := r.manager.Recreate(ctx, profiles); err != nil {
		return err
	}
	r.storeProfiles(profiles)
	return nil
}

func (r *Runtime) Runtime(profileID int) (RuntimeHandle, bool) {
	if r == nil || r.manager == nil {
		return nil, false
	}
	return r.manager.Runtime(profileID)
}

func (r *Runtime) RuntimeForAgent(agentID string, profileID int) (RuntimeHandle, bool) {
	if r == nil || r.manager == nil {
		return nil, false
	}
	return r.manager.RuntimeForAgent(agentID, profileID)
}

func (r *Runtime) Commit(transaction *Transaction, profiles []model.WireGuardProfile) {
	if transaction == nil {
		return
	}
	transaction.Commit()
	r.storeProfiles(profiles)
}

func (r *Runtime) Close() error {
	if r == nil || r.manager == nil {
		return nil
	}
	return r.manager.Close()
}

func (r *Runtime) OverlayProvider() any {
	return overlayRuntimeProvider{runtime: r}
}

func (r *Runtime) TransparentListenerProvider() any {
	return transparentListenerProvider{runtime: r}
}

func (r *Runtime) storeProfiles(profiles []model.WireGuardProfile) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.profiles = CloneWireGuardProfiles(profiles)
}

func (r *Runtime) profileSnapshot() []model.WireGuardProfile {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return CloneWireGuardProfiles(r.profiles)
}

func (r *Runtime) Profiles() []model.WireGuardProfile {
	return r.profileSnapshot()
}

func WireGuardProfileRoutesRelayHop(profile model.WireGuardProfile, addr netip.Addr) bool {
	for _, peer := range profile.Peers {
		for _, allowed := range peer.AllowedIPs {
			prefix, err := netip.ParsePrefix(strings.TrimSpace(allowed))
			if err != nil {
				continue
			}
			if prefix.Addr().BitLen() != addr.BitLen() {
				continue
			}
			if prefix.Contains(addr) {
				return true
			}
		}
	}
	return false
}

func CloneWireGuardProfiles(profiles []model.WireGuardProfile) []model.WireGuardProfile {
	if profiles == nil {
		return nil
	}
	cloned := slices.Clone(profiles)
	for i, profile := range profiles {
		cloned[i].BindAddresses = slices.Clone(profile.BindAddresses)
		cloned[i].Addresses = slices.Clone(profile.Addresses)
		cloned[i].DNS = slices.Clone(profile.DNS)
		cloned[i].Tags = slices.Clone(profile.Tags)
		cloned[i].Peers = slices.Clone(profile.Peers)
		for j := range cloned[i].Peers {
			cloned[i].Peers[j].AllowedIPs = slices.Clone(profile.Peers[j].AllowedIPs)
			cloned[i].Peers[j].Reserved = slices.Clone(profile.Peers[j].Reserved)
		}
	}
	return cloned
}

type overlayRuntimeProvider struct {
	runtime *Runtime
}

func (p overlayRuntimeProvider) DialContext(ctx context.Context, agentID string, profileID int, network string, address string) (net.Conn, error) {
	runtime, err := p.runtimeForAgent(agentID, profileID)
	if err != nil {
		return nil, err
	}
	return runtime.DialContext(ctx, network, address)
}

func (p overlayRuntimeProvider) ListenTCP(ctx context.Context, agentID string, profileID int, address string) (net.Listener, error) {
	runtime, err := p.runtimeForAgent(agentID, profileID)
	if err != nil {
		return nil, err
	}
	return runtime.ListenTCP(ctx, address)
}

func (p overlayRuntimeProvider) ListenUDP(ctx context.Context, agentID string, profileID int, address string) (net.PacketConn, error) {
	runtime, err := p.runtimeForAgent(agentID, profileID)
	if err != nil {
		return nil, err
	}
	return runtime.ListenUDP(ctx, address)
}

func (p overlayRuntimeProvider) runtimeForAgent(agentID string, profileID int) (RuntimeHandle, error) {
	if p.runtime == nil {
		return nil, net.ErrClosed
	}
	runtime, ok := p.runtime.RuntimeForAgent(agentID, profileID)
	if !ok {
		return nil, net.ErrClosed
	}
	return runtime, nil
}

type transparentListenerProvider struct {
	runtime *Runtime
}

func (p transparentListenerProvider) ListenTransparentTCP(ctx context.Context, agentID string, profileID int) (net.Listener, error) {
	runtime, err := p.runtimeForAgent(agentID, profileID)
	if err != nil {
		return nil, err
	}
	return runtime.ListenTransparentTCP(ctx)
}

func (p transparentListenerProvider) ListenTransparentUDP(ctx context.Context, agentID string, profileID int, address string) (module.TransparentUDPConn, error) {
	runtime, err := p.runtimeForAgent(agentID, profileID)
	if err != nil {
		return nil, err
	}
	conn, err := runtime.ListenTransparentUDP(ctx, address)
	if err != nil {
		return nil, err
	}
	return transparentUDPConnAdapter{conn: conn}, nil
}

func (p transparentListenerProvider) runtimeForAgent(agentID string, profileID int) (RuntimeHandle, error) {
	return overlayRuntimeProvider{runtime: p.runtime}.runtimeForAgent(agentID, profileID)
}

type transparentUDPConnAdapter struct {
	conn TransparentUDPConn
}

func (c transparentUDPConnAdapter) Close() error {
	return c.conn.Close()
}

func (c transparentUDPConnAdapter) LocalAddr() net.Addr {
	return c.conn.LocalAddr()
}

func (c transparentUDPConnAdapter) ReadPacket() (module.TransparentUDPPacket, error) {
	packet, err := c.conn.ReadPacket()
	if err != nil {
		return module.TransparentUDPPacket{}, err
	}
	return module.TransparentUDPPacket{
		Peer:        packet.Peer,
		OriginalDst: packet.OriginalDst,
		Payload:     append([]byte(nil), packet.Payload...),
	}, nil
}

func (c transparentUDPConnAdapter) WritePacket(payload []byte, peer *net.UDPAddr, source string) error {
	return c.conn.WritePacket(payload, peer, source)
}
