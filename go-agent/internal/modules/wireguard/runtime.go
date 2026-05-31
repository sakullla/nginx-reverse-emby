package wireguard

import (
	"context"
	"net"
	"net/netip"
	"strings"
	"sync"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
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

func (r *Runtime) Provider() wireGuardRuntimeProvider {
	return NewRuntimeProvider(r, "")
}

func (r *Runtime) OverlayProvider() any {
	return overlayRuntimeProvider{runtime: r}
}

func (r *Runtime) ProviderForAgent(agentID string) wireGuardRuntimeProvider {
	return NewRuntimeProvider(r, agentID)
}

func (r *Runtime) TransactionProvider(transaction *Transaction, profiles []model.WireGuardProfile) wireGuardTransactionProvider {
	return NewTransactionProvider(transaction, "", profiles)
}

func (r *Runtime) TransactionProviderForAgent(transaction *Transaction, agentID string, profiles []model.WireGuardProfile) wireGuardTransactionProvider {
	return NewTransactionProvider(transaction, agentID, profiles)
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

type wireGuardRuntimeProvider struct {
	runtime *Runtime
	agentID string
}

func NewRuntimeProvider(runtime *Runtime, agentID string) wireGuardRuntimeProvider {
	return wireGuardRuntimeProvider{
		runtime: runtime,
		agentID: strings.TrimSpace(agentID),
	}
}

func (p wireGuardRuntimeProvider) WireGuardRuntime(profileID int) (RuntimeHandle, bool) {
	if p.runtime == nil {
		return nil, false
	}
	if p.agentID != "" {
		runtime, ok := p.runtime.RuntimeForAgent(p.agentID, profileID)
		if ok {
			return runtime, true
		}
		return nil, false
	}
	runtime, ok := p.runtime.Runtime(profileID)
	if !ok {
		return nil, false
	}
	return runtime, true
}

func (p wireGuardRuntimeProvider) WireGuardRuntimeForAgent(agentID string, profileID int) (RuntimeHandle, bool) {
	if p.runtime == nil {
		return nil, false
	}
	runtime, ok := p.runtime.RuntimeForAgent(agentID, profileID)
	if !ok {
		return nil, false
	}
	return runtime, true
}

type wireGuardTransactionProvider struct {
	transaction *Transaction
	agentID     string
	profiles    []model.WireGuardProfile
}

func NewTransactionProvider(transaction *Transaction, agentID string, profiles []model.WireGuardProfile) wireGuardTransactionProvider {
	return wireGuardTransactionProvider{
		transaction: transaction,
		agentID:     strings.TrimSpace(agentID),
		profiles:    CloneWireGuardProfiles(profiles),
	}
}

func (p wireGuardTransactionProvider) WireGuardRuntime(profileID int) (RuntimeHandle, bool) {
	if p.transaction == nil {
		return nil, false
	}
	if p.agentID != "" {
		runtime, ok := p.transaction.RuntimeForAgent(p.agentID, profileID)
		if ok {
			return runtime, true
		}
		return nil, false
	}
	runtime, ok := p.transaction.Runtime(profileID)
	if !ok {
		return nil, false
	}
	return runtime, true
}

func (p wireGuardTransactionProvider) WireGuardRuntimeForAgent(agentID string, profileID int) (RuntimeHandle, bool) {
	if p.transaction == nil {
		return nil, false
	}
	runtime, ok := p.transaction.RuntimeForAgent(agentID, profileID)
	if !ok {
		return nil, false
	}
	return runtime, true
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
	cloned := make([]model.WireGuardProfile, len(profiles))
	for i, profile := range profiles {
		cloned[i] = profile
		cloned[i].BindAddresses = append([]string(nil), profile.BindAddresses...)
		cloned[i].Addresses = append([]string(nil), profile.Addresses...)
		cloned[i].DNS = append([]string(nil), profile.DNS...)
		cloned[i].Tags = append([]string(nil), profile.Tags...)
		cloned[i].Peers = append([]model.WireGuardPeer(nil), profile.Peers...)
		for j := range cloned[i].Peers {
			cloned[i].Peers[j].AllowedIPs = append([]string(nil), profile.Peers[j].AllowedIPs...)
			cloned[i].Peers[j].Reserved = append([]byte(nil), profile.Peers[j].Reserved...)
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

func (p overlayRuntimeProvider) ListenTransparentTCP(ctx context.Context, agentID string, profileID int) (net.Listener, error) {
	runtime, err := p.runtimeForAgent(agentID, profileID)
	if err != nil {
		return nil, err
	}
	return runtime.ListenTransparentTCP(ctx)
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
