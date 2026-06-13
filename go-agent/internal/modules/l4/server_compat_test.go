package l4

import (
	"context"
	"fmt"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay"
	"net"
)

func NewServerWithOverlayProvider(
	ctx context.Context,
	rules []model.L4Rule,
	relayListeners []model.RelayListener,
	relayProvider RelayMaterialProvider,
	wireGuardProvider relay.OverlayRuntimeProvider,
) (*Server, error) {
	overlay, transparent := testWireGuardOverlayProviders(wireGuardProvider)
	return newServerWithOptions(ctx, rules, relayListeners, relayProvider, serverOptions{
		overlayRuntime:      overlay,
		transparentListener: transparent,
	})
}

func NewServerWithResourcesAndOverlayProvider(
	ctx context.Context,
	rules []model.L4Rule,
	relayListeners []model.RelayListener,
	relayProvider RelayMaterialProvider,
	cache *model.Cache,
	wireGuardProvider relay.OverlayRuntimeProvider,
) (*Server, error) {
	overlay, transparent := testWireGuardOverlayProviders(wireGuardProvider)
	return newServerWithOptions(ctx, rules, relayListeners, relayProvider, serverOptions{
		cache:               cache,
		overlayRuntime:      overlay,
		transparentListener: transparent,
	})
}

func NewServerWithResourcesWireGuardAndEgressProfiles(
	ctx context.Context,
	rules []model.L4Rule,
	relayListeners []model.RelayListener,
	relayProvider RelayMaterialProvider,
	cache *model.Cache,
	wireGuardProvider relay.OverlayRuntimeProvider,
	egressProfiles []model.EgressProfile,
) (*Server, error) {
	return NewServerWithResourcesWireGuardAndEgressRuntime(ctx, rules, relayListeners, relayProvider, cache, wireGuardProvider, nil, egressProfiles)
}

func NewServerWithResourcesWireGuardAndEgressRuntime(
	ctx context.Context,
	rules []model.L4Rule,
	relayListeners []model.RelayListener,
	relayProvider RelayMaterialProvider,
	cache *model.Cache,
	wireGuardProvider relay.OverlayRuntimeProvider,
	egressOverlayRuntime module.OverlayRuntime,
	egressProfiles []model.EgressProfile,
) (*Server, error) {
	overlay, transparent := testWireGuardOverlayProviders(wireGuardProvider)
	return newServerWithOptions(ctx, rules, relayListeners, relayProvider, serverOptions{
		cache:                cache,
		overlayRuntime:       overlay,
		transparentListener:  transparent,
		egressOverlayRuntime: egressOverlayRuntime,
		egressProfiles:       egressProfiles,
	})
}

type testWireGuardOverlayProvider struct {
	provider relay.OverlayRuntimeProvider
}

func testWireGuardOverlayProviders(provider relay.OverlayRuntimeProvider) (module.OverlayRuntime, module.TransparentListener) {
	if provider == nil {
		return nil, nil
	}
	overlay := testWireGuardOverlayProvider{provider: provider}
	return overlay, overlay
}

func (p testWireGuardOverlayProvider) runtime(agentID string, profileID int) (relay.WireGuardRuntime, error) {
	runtime, ok := relay.ResolveOverlayRuntime(p.provider, agentID, profileID)
	if !ok || runtime == nil {
		return nil, fmt.Errorf("wireguard profile %d runtime not found", profileID)
	}
	return runtime, nil
}

func (p testWireGuardOverlayProvider) DialContext(ctx context.Context, agentID string, profileID int, network string, address string) (net.Conn, error) {
	runtime, err := p.runtime(agentID, profileID)
	if err != nil {
		return nil, err
	}
	return runtime.DialContext(ctx, network, address)
}

func (p testWireGuardOverlayProvider) ListenTCP(ctx context.Context, agentID string, profileID int, address string) (net.Listener, error) {
	runtime, err := p.runtime(agentID, profileID)
	if err != nil {
		return nil, err
	}
	return runtime.ListenTCP(ctx, address)
}

func (p testWireGuardOverlayProvider) ListenUDP(ctx context.Context, agentID string, profileID int, address string) (net.PacketConn, error) {
	runtime, err := p.runtime(agentID, profileID)
	if err != nil {
		return nil, err
	}
	return runtime.ListenUDP(ctx, address)
}

func (p testWireGuardOverlayProvider) ListenTransparentTCP(ctx context.Context, agentID string, profileID int) (net.Listener, error) {
	runtime, err := p.runtime(agentID, profileID)
	if err != nil {
		return nil, err
	}
	return runtime.ListenTransparentTCP(ctx)
}

func (p testWireGuardOverlayProvider) ListenTransparentUDP(ctx context.Context, agentID string, profileID int, address string) (module.TransparentUDPConn, error) {
	runtime, err := p.runtime(agentID, profileID)
	if err != nil {
		return nil, err
	}
	return runtime.ListenTransparentUDP(ctx, address)
}
