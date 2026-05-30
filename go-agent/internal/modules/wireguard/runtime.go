package wireguard

import (
	"context"
	"net"
	"net/netip"
	"strings"
	"sync"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
	basewireguard "github.com/sakullla/nginx-reverse-emby/go-agent/internal/wireguard"
)

type Runtime struct {
	mu      sync.RWMutex
	manager *basewireguard.Manager
	profiles []model.WireGuardProfile
}

func NewRuntime(factory basewireguard.Factory) *Runtime {
	return &Runtime{
		manager: basewireguard.NewManager(basewireguard.ManagerOptions{Factory: factory}),
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

func (r *Runtime) Prepare(ctx context.Context, profiles []model.WireGuardProfile) (*basewireguard.Transaction, error) {
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

func (r *Runtime) Runtime(profileID int) (basewireguard.Runtime, bool) {
	if r == nil || r.manager == nil {
		return nil, false
	}
	return r.manager.Runtime(profileID)
}

func (r *Runtime) RuntimeForAgent(agentID string, profileID int) (basewireguard.Runtime, bool) {
	if r == nil || r.manager == nil {
		return nil, false
	}
	return r.manager.RuntimeForAgent(agentID, profileID)
}

func (r *Runtime) Commit(transaction *basewireguard.Transaction, profiles []model.WireGuardProfile) {
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

func (r *Runtime) Provider() relay.WireGuardRuntimeProvider {
	return NewRuntimeProvider(r, "")
}

func (r *Runtime) ProviderForAgent(agentID string) relay.WireGuardRuntimeProvider {
	return NewRuntimeProvider(r, agentID)
}

func (r *Runtime) TransactionProvider(transaction *basewireguard.Transaction, profiles []model.WireGuardProfile) relay.WireGuardRuntimeProvider {
	return NewTransactionProvider(transaction, "", profiles)
}

func (r *Runtime) TransactionProviderForAgent(transaction *basewireguard.Transaction, agentID string, profiles []model.WireGuardProfile) relay.WireGuardRuntimeProvider {
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

type wireGuardRuntimeProvider struct {
	runtime *Runtime
	agentID string
}

func NewRuntimeProvider(runtime *Runtime, agentID string) relay.WireGuardRuntimeProvider {
	return wireGuardRuntimeProvider{
		runtime: runtime,
		agentID: strings.TrimSpace(agentID),
	}
}

func (p wireGuardRuntimeProvider) WireGuardRuntime(profileID int) (relay.WireGuardRuntime, bool) {
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

func (p wireGuardRuntimeProvider) WireGuardRuntimeForAgent(agentID string, profileID int) (relay.WireGuardRuntime, bool) {
	if p.runtime == nil {
		return nil, false
	}
	runtime, ok := p.runtime.RuntimeForAgent(agentID, profileID)
	if !ok {
		return nil, false
	}
	return runtime, true
}

func (p wireGuardRuntimeProvider) WireGuardRuntimeForHop(hop relay.Hop) (relay.WireGuardRuntime, bool) {
	if p.runtime == nil {
		return nil, false
	}
	if hop.Listener.WireGuardProfileID != nil && *hop.Listener.WireGuardProfileID > 0 {
		if runtime, ok := p.WireGuardRuntimeForAgent(hop.Listener.AgentID, *hop.Listener.WireGuardProfileID); ok {
			return runtime, true
		}
	}
	profile, ok := wireGuardProfileForRelayHop(p.runtime.profileSnapshot(), p.agentID, hop)
	if !ok {
		return nil, false
	}
	runtime, ok := p.runtime.RuntimeForAgent(profile.AgentID, profile.ID)
	if !ok {
		return nil, false
	}
	return runtime, true
}

type wireGuardTransactionProvider struct {
	transaction *basewireguard.Transaction
	agentID     string
	profiles    []model.WireGuardProfile
}

func NewTransactionProvider(transaction *basewireguard.Transaction, agentID string, profiles []model.WireGuardProfile) relay.WireGuardRuntimeProvider {
	return wireGuardTransactionProvider{
		transaction: transaction,
		agentID:     strings.TrimSpace(agentID),
		profiles:    CloneWireGuardProfiles(profiles),
	}
}

func (p wireGuardTransactionProvider) WireGuardRuntime(profileID int) (relay.WireGuardRuntime, bool) {
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

func (p wireGuardTransactionProvider) WireGuardRuntimeForAgent(agentID string, profileID int) (relay.WireGuardRuntime, bool) {
	if p.transaction == nil {
		return nil, false
	}
	runtime, ok := p.transaction.RuntimeForAgent(agentID, profileID)
	if !ok {
		return nil, false
	}
	return runtime, true
}

func (p wireGuardTransactionProvider) WireGuardRuntimeForHop(hop relay.Hop) (relay.WireGuardRuntime, bool) {
	if p.transaction == nil {
		return nil, false
	}
	if hop.Listener.WireGuardProfileID != nil && *hop.Listener.WireGuardProfileID > 0 {
		if runtime, ok := p.WireGuardRuntimeForAgent(hop.Listener.AgentID, *hop.Listener.WireGuardProfileID); ok {
			return runtime, true
		}
	}
	profile, ok := wireGuardProfileForRelayHop(p.profiles, p.agentID, hop)
	if !ok {
		return nil, false
	}
	runtime, ok := p.transaction.RuntimeForAgent(profile.AgentID, profile.ID)
	if !ok {
		return nil, false
	}
	return runtime, true
}

func wireGuardProfileForRelayHop(profiles []model.WireGuardProfile, localAgentID string, hop relay.Hop) (model.WireGuardProfile, bool) {
	host, _, err := net.SplitHostPort(strings.TrimSpace(hop.Address))
	if err != nil {
		return model.WireGuardProfile{}, false
	}
	addr, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil {
		return model.WireGuardProfile{}, false
	}
	localAgentID = strings.TrimSpace(localAgentID)

	var found model.WireGuardProfile
	for _, profile := range profiles {
		if !profile.Enabled {
			continue
		}
		if localAgentID != "" && strings.TrimSpace(profile.AgentID) != localAgentID {
			continue
		}
		if !wireGuardProfileRoutesRelayHop(profile, addr) {
			continue
		}
		if found.ID != 0 {
			return model.WireGuardProfile{}, false
		}
		found = profile
	}
	return found, found.ID != 0
}

func wireGuardProfileRoutesRelayHop(profile model.WireGuardProfile, addr netip.Addr) bool {
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
