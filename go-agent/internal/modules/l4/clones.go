package l4

import (
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	moduleegress "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/egress"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/moduleutil"
)

func cloneL4Rules(rules []model.L4Rule) []model.L4Rule {
	cloned := moduleutil.CloneL4Rules(rules)
	for i := range cloned {
		cloned[i].PolicyRef = clonePolicyRef(rules[i].PolicyRef)
	}
	return cloned
}

func clonePolicyRef(ref *model.PolicyRef) *model.PolicyRef {
	if ref == nil {
		return nil
	}
	cloned := *ref
	cloned.Overlay = append([]byte(nil), ref.Overlay...)
	return &cloned
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
		providers.FinalHopDialer = moduleegress.NewFinalHopDialer(profiles)
	}
	return providers
}
