package l4

import (
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	moduleegress "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/egress"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/policy"
)

type Providers struct {
	Relay           RelayMaterialProvider
	FinalHopDialer  relay.FinalHopDialer
	EgressResolver  module.EgressResolver
	EgressProfiles  []model.EgressProfile
	PolicyEvaluator policy.Evaluator
}

func (m *Module) runtimeProviders(resolver module.ProviderResolver, egressProfiles []model.EgressProfile) Providers {
	providers := Providers{EgressProfiles: cloneEgressProfiles(egressProfiles)}
	if resolver == nil {
		providers.FinalHopDialer = moduleegress.NewFinalHopDialer(providers.EgressProfiles)
		return providers
	}
	if tlsMaterial, _ := resolver.Resolve(module.ProviderTLSMaterial); tlsMaterial != nil {
		if relayTLS, ok := tlsMaterial.(RelayMaterialProvider); ok {
			providers.Relay = relayTLS
		}
	}
	if egressResolver, _ := resolver.Resolve(module.ProviderEgressResolver); egressResolver != nil {
		if profileResolver, ok := egressResolver.(module.EgressResolver); ok {
			providers.EgressResolver = profileResolver
		}
	}
	if evaluator, _ := resolver.Resolve(module.ProviderPolicyEvaluator); evaluator != nil {
		if policyEvaluator, ok := evaluator.(policy.Evaluator); ok {
			providers.PolicyEvaluator = policyEvaluator
		}
	}
	if finalHop, _ := resolver.Resolve(module.ProviderFinalHopDialer); finalHop != nil {
		if dialer := relay.FinalHopDialerFromProvider(finalHop); dialer != nil {
			providers.FinalHopDialer = dialer
		}
	}
	if providers.FinalHopDialer == nil {
		providers.FinalHopDialer = moduleegress.NewFinalHopDialer(providers.EgressProfiles)
	}
	return providers
}

func (p Providers) egressResolver() moduleegress.ProfileResolver {
	if p.EgressResolver != nil {
		return p.EgressResolver
	}
	return moduleegress.NewResolver(p.EgressProfiles)
}
