package http

import (
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay"
)

func (m *Module) runtimeProviders(resolver module.ProviderResolver, egressProfiles []model.EgressProfile) (Providers, error) {
	provider := Providers{EgressProfiles: egressProfiles}
	if resolver == nil {
		return provider, nil
	}
	tlsMaterial, _ := resolver.Resolve(module.ProviderTLSMaterial)
	if hostTLS, ok := tlsMaterial.(TLSMaterialProvider); ok {
		provider.TLS = hostTLS
	}
	if relayTLS, ok := tlsMaterial.(RelayMaterialProvider); ok {
		provider.Relay = relayTLS
	}
	if egressResolver, _ := resolver.Resolve(module.ProviderEgressResolver); egressResolver != nil {
		if profileResolver, ok := egressResolver.(module.EgressResolver); ok {
			provider.EgressResolver = profileResolver
		}
	}
	finalHopProvider, _ := resolver.Resolve(module.ProviderFinalHopDialer)
	if dialer := relay.FinalHopDialerFromProvider(finalHopProvider); dialer != nil {
		provider.FinalHopDialer = dialer
	}
	return provider, nil
}
