package l4

import (
	"context"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	moduleegress "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/egress"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/moduleutil"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay"
)

type Providers struct {
	Relay               RelayMaterialProvider
	Overlay             module.OverlayRuntime
	TransparentListener module.TransparentListener
	FinalHopDialer      relay.FinalHopDialer
	EgressResolver      module.EgressResolver
	EgressOverlay       module.OverlayRuntime
	EgressProfiles      []model.EgressProfile
}

func (m *Module) runtimeProviders(resolver module.ProviderResolver, egressProfiles []model.EgressProfile) Providers {
	providers := Providers{EgressProfiles: cloneEgressProfiles(egressProfiles)}
	if resolver == nil {
		providers.FinalHopDialer = moduleegress.NewFinalHopDialer(providers.EgressProfiles, nil)
		return providers
	}
	if tlsMaterial, _ := resolver.Resolve(module.ProviderTLSMaterial); tlsMaterial != nil {
		if relayTLS, ok := tlsMaterial.(RelayMaterialProvider); ok {
			providers.Relay = relayTLS
		}
	}
	if overlay, _ := resolver.Resolve(module.ProviderOverlayRuntime); overlay != nil {
		if runtime, ok := overlay.(module.OverlayRuntime); ok {
			providers.Overlay = runtime
		}
	}
	if transparent, _ := resolver.Resolve(module.ProviderTransparentListener); transparent != nil {
		if listener, ok := transparent.(module.TransparentListener); ok {
			providers.TransparentListener = listener
		}
	}
	if overlay, _ := resolver.Resolve(module.ProviderEgressOverlayRuntime); overlay != nil {
		if runtime, ok := overlay.(module.OverlayRuntime); ok {
			providers.EgressOverlay = runtime
		}
	} else {
		providers.EgressOverlay = providers.Overlay
	}
	if egressResolver, _ := resolver.Resolve(module.ProviderEgressResolver); egressResolver != nil {
		if profileResolver, ok := egressResolver.(module.EgressResolver); ok {
			providers.EgressResolver = profileResolver
		}
	}
	if finalHop, _ := resolver.Resolve(module.ProviderFinalHopDialer); finalHop != nil {
		if dialer := relay.FinalHopDialerFromProvider(finalHop); dialer != nil {
			providers.FinalHopDialer = dialer
		}
	}
	if providers.FinalHopDialer == nil {
		providers.FinalHopDialer = moduleegress.NewFinalHopDialer(providers.EgressProfiles, providers.EgressOverlay)
	}
	return providers
}

func (p Providers) egressResolver() moduleegress.ProfileResolver {
	if p.EgressResolver != nil {
		return p.EgressResolver
	}
	return moduleegress.NewResolver(p.EgressProfiles)
}

func restoreOverlayProvidersForRollback(ctx context.Context, rules []model.L4Rule, providers Providers) error {
	if !hasOverlayListenRule(rules) {
		return nil
	}
	if err := moduleutil.RestoreProviderForRollback(ctx, providers.Overlay); err != nil {
		return err
	}
	if moduleutil.SameProvider(providers.Overlay, providers.TransparentListener) {
		return nil
	}
	return moduleutil.RestoreProviderForRollback(ctx, providers.TransparentListener)
}

func hasOverlayListenRule(rules []model.L4Rule) bool {
	for _, rule := range rules {
		if rule.Enabled && l4RuleUsesOverlay(rule) {
			return true
		}
	}
	return false
}

func restoreEgressOverlayForRollback(ctx context.Context, rules []model.L4Rule, overlay any) error {
	if !hasEgressProfileRule(rules) {
		return nil
	}
	return moduleutil.RestoreProviderForRollback(ctx, overlay)
}

func hasEgressProfileRule(rules []model.L4Rule) bool {
	for _, rule := range rules {
		if rule.Enabled && rule.EgressProfileID != nil && *rule.EgressProfileID > 0 {
			return true
		}
	}
	return false
}
