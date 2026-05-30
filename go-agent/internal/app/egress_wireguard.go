package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/wireguard"
)

type egressWireGuardRuntime struct {
	runtime *sharedWireGuardRuntime
}

func newEgressWireGuardRuntime(factory wireguard.Factory) *egressWireGuardRuntime {
	return &egressWireGuardRuntime{runtime: newSharedWireGuardRuntimeWithFactory(factory)}
}

func (r *egressWireGuardRuntime) Apply(ctx context.Context, profiles []model.EgressProfile) error {
	if r == nil || r.runtime == nil {
		return nil
	}
	return r.runtime.Apply(ctx, egressWireGuardProfiles(profiles))
}

func (r *egressWireGuardRuntime) Prepare(ctx context.Context, profiles []model.EgressProfile) (*wireguard.Transaction, relay.WireGuardRuntimeProvider, error) {
	if r == nil || r.runtime == nil {
		return nil, nil, nil
	}
	wireGuardProfiles := egressWireGuardProfiles(profiles)
	transaction, err := r.runtime.Prepare(ctx, wireGuardProfiles)
	if err != nil {
		return nil, nil, err
	}
	if transaction == nil {
		return nil, egressWireGuardRuntimeProvider{provider: r.runtime.provider()}, nil
	}
	return transaction, egressWireGuardRuntimeProvider{
		provider: wireGuardTransactionProvider{transaction: transaction, profiles: cloneWireGuardProfiles(wireGuardProfiles)},
	}, nil
}

func (r *egressWireGuardRuntime) Commit(transaction *wireguard.Transaction, profiles []model.EgressProfile) {
	if r == nil || r.runtime == nil || transaction == nil {
		return
	}
	r.runtime.Commit(transaction, egressWireGuardProfiles(profiles))
}

func (r *egressWireGuardRuntime) Close() error {
	if r == nil || r.runtime == nil {
		return nil
	}
	return r.runtime.Close()
}

func (r *egressWireGuardRuntime) Provider() relay.WireGuardRuntimeProvider {
	if r == nil || r.runtime == nil {
		return nil
	}
	return egressWireGuardRuntimeProvider{provider: r.runtime.provider()}
}

type egressWireGuardRuntimeProvider struct {
	provider relay.WireGuardRuntimeProvider
}

func (p egressWireGuardRuntimeProvider) WireGuardRuntime(profileID int) (relay.WireGuardRuntime, bool) {
	if p.provider == nil {
		return nil, false
	}
	return p.provider.WireGuardRuntime(profileID)
}

func egressWireGuardProfiles(profiles []model.EgressProfile) []model.WireGuardProfile {
	out := make([]model.WireGuardProfile, 0, len(profiles))
	for _, profile := range profiles {
		if !profile.Enabled || !strings.EqualFold(strings.TrimSpace(profile.Type), "wireguard") {
			continue
		}
		out = append(out, egressWireGuardProfile(profile))
	}
	return out
}

func egressWireGuardProfile(profile model.EgressProfile) model.WireGuardProfile {
	cfg := profile.WireGuardConfig
	if cfg == nil {
		return model.WireGuardProfile{
			ID:       profile.ID,
			Name:     profile.Name,
			Mode:     wireguard.ModeGenericWireGuard,
			Enabled:  profile.Enabled,
			Revision: profile.Revision,
		}
	}
	return model.WireGuardProfile{
		ID:         profile.ID,
		Name:       profile.Name,
		Mode:       wireguard.ModeGenericWireGuard,
		PrivateKey: cfg.PrivateKey,
		Addresses:  append([]string(nil), cfg.Addresses...),
		Peers:      cloneWireGuardPeers(cfg.Peers),
		DNS:        append([]string(nil), cfg.DNS...),
		MTU:        cfg.MTU,
		Enabled:    profile.Enabled,
		Revision:   profile.Revision,
	}
}

func cloneEgressProfiles(profiles []model.EgressProfile) []model.EgressProfile {
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

func validateEgressWireGuardReferences(rules []model.L4Rule, egressProfiles []model.EgressProfile, provider relay.WireGuardRuntimeProvider) error {
	resolver := egressProfileByID(egressProfiles)
	for _, rule := range rules {
		if len(rule.RelayLayers) > 0 || rule.EgressProfileID == nil || *rule.EgressProfileID <= 0 {
			continue
		}
		profile, ok := resolver[*rule.EgressProfileID]
		if !ok || !profile.Enabled || !strings.EqualFold(strings.TrimSpace(profile.Type), "wireguard") {
			continue
		}
		if provider == nil {
			return fmt.Errorf("wireguard runtime provider is required for egress profile %d", profile.ID)
		}
		runtime, ok := provider.WireGuardRuntime(profile.ID)
		if !ok || runtime == nil {
			return fmt.Errorf("wireguard egress profile %d runtime not found", profile.ID)
		}
	}
	return nil
}

func egressProfileByID(profiles []model.EgressProfile) map[int]model.EgressProfile {
	byID := make(map[int]model.EgressProfile, len(profiles))
	for _, profile := range profiles {
		byID[profile.ID] = profile
	}
	return byID
}
