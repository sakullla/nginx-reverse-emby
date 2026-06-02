package l4

import (
	"slices"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	moduleegress "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/egress"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/moduleutil"
)

func cloneL4Rules(rules []model.L4Rule) []model.L4Rule {
	if rules == nil {
		return nil
	}
	cloned := slices.Clone(rules)
	for i, rule := range rules {
		cloned[i].Backends = slices.Clone(rule.Backends)
		cloned[i].RelayChain = slices.Clone(rule.RelayChain)
		cloned[i].RelayLayers = moduleutil.CloneIntLayers(rule.RelayLayers)
		cloned[i].Tags = slices.Clone(rule.Tags)
		cloned[i].WireGuardProfileID = moduleutil.ClonePtr(rule.WireGuardProfileID)
		cloned[i].EgressProfileID = moduleutil.ClonePtr(rule.EgressProfileID)
	}
	return cloned
}

func cloneRelayListeners(listeners []model.RelayListener) []model.RelayListener {
	return moduleutil.CloneRelayListeners(listeners)
}

func cloneEgressProfiles(profiles []model.EgressProfile) []model.EgressProfile {
	return moduleegress.CloneProfiles(profiles)
}

func cloneProviders(providers Providers) Providers {
	providers.EgressProfiles = cloneEgressProfiles(providers.EgressProfiles)
	return providers
}

func snapshotProviders(providers Providers, egressProfiles []model.EgressProfile) Providers {
	providers = cloneProviders(providers)
	profiles := cloneEgressProfiles(egressProfiles)
	providers.EgressProfiles = profiles
	providers.EgressResolver = nil
	if providers.FinalHopDialer == nil {
		providers.FinalHopDialer = moduleegress.NewFinalHopDialer(profiles, providers.EgressOverlay)
	}
	return providers
}
