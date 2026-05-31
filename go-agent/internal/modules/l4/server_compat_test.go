package l4

import (
	"context"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/backends"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay"
)

func NewServerWithWireGuardProvider(
	ctx context.Context,
	rules []model.L4Rule,
	relayListeners []model.RelayListener,
	relayProvider RelayMaterialProvider,
	wireGuardProvider relay.WireGuardRuntimeProvider,
) (*Server, error) {
	return newServerWithOptions(ctx, rules, relayListeners, relayProvider, serverOptions{overlayProvider: wireGuardProvider})
}

func NewServerWithResourcesAndWireGuardProvider(
	ctx context.Context,
	rules []model.L4Rule,
	relayListeners []model.RelayListener,
	relayProvider RelayMaterialProvider,
	cache *backends.Cache,
	wireGuardProvider relay.WireGuardRuntimeProvider,
) (*Server, error) {
	return newServerWithOptions(ctx, rules, relayListeners, relayProvider, serverOptions{cache: cache, overlayProvider: wireGuardProvider})
}

func NewServerWithResourcesWireGuardAndEgressProfiles(
	ctx context.Context,
	rules []model.L4Rule,
	relayListeners []model.RelayListener,
	relayProvider RelayMaterialProvider,
	cache *backends.Cache,
	wireGuardProvider relay.WireGuardRuntimeProvider,
	egressProfiles []model.EgressProfile,
) (*Server, error) {
	return NewServerWithResourcesWireGuardAndEgressRuntime(ctx, rules, relayListeners, relayProvider, cache, wireGuardProvider, nil, egressProfiles)
}

func NewServerWithResourcesWireGuardAndEgressRuntime(
	ctx context.Context,
	rules []model.L4Rule,
	relayListeners []model.RelayListener,
	relayProvider RelayMaterialProvider,
	cache *backends.Cache,
	wireGuardProvider relay.WireGuardRuntimeProvider,
	egressOverlayRuntime module.OverlayRuntime,
	egressProfiles []model.EgressProfile,
) (*Server, error) {
	return newServerWithOptions(ctx, rules, relayListeners, relayProvider, serverOptions{
		cache:                cache,
		overlayProvider:      wireGuardProvider,
		egressOverlayRuntime: egressOverlayRuntime,
		egressProfiles:       egressProfiles,
	})
}
