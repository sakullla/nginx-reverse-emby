package http

import (
	"context"
	"fmt"
	"net"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay"
)

func (m *Module) runtimeProviders(resolver module.ProviderResolver, egressProfiles []model.EgressProfile) (Providers, error) {
	tlsMaterial, _ := resolver.Resolve(module.ProviderTLSMaterial)
	provider := Providers{}
	if hostTLS, ok := tlsMaterial.(TLSMaterialProvider); ok {
		provider.TLS = hostTLS
	}
	if relayTLS, ok := tlsMaterial.(RelayMaterialProvider); ok {
		provider.Relay = relayTLS
	}
	overlayProvider, _ := resolver.Resolve(module.ProviderOverlayRuntime)
	if overlay := overlayRuntimeFromProvider(overlayProvider); overlay != nil {
		provider.OverlayProvider = moduleOverlayRuntimeProvider{overlay: overlay}
	}
	egressOverlayProvider, _ := resolver.Resolve(module.ProviderEgressOverlayRuntime)
	if overlay := overlayRuntimeFromProvider(egressOverlayProvider); overlay != nil {
		provider.EgressOverlay = overlay
	} else if overlay := overlayRuntimeFromProvider(overlayProvider); overlay != nil {
		provider.EgressOverlay = overlay
	}
	finalHopProvider, _ := resolver.Resolve(module.ProviderFinalHopDialer)
	if dialer := finalHopDialerFromProvider(finalHopProvider); dialer != nil {
		provider.FinalHopDialer = dialer
	}
	provider.EgressProfiles = egressProfiles
	return provider, nil
}

func overlayRuntimeFromProvider(provider any) module.OverlayRuntime {
	if overlay, ok := provider.(module.OverlayRuntime); ok {
		return overlay
	}
	return nil
}

func finalHopDialerFromProvider(provider any) relay.FinalHopDialer {
	if dialer, ok := provider.(relay.FinalHopDialer); ok {
		return dialer
	}
	if dialer, ok := provider.(module.FinalHopDialer); ok {
		return moduleFinalHopDialer{dialer: dialer}
	}
	return nil
}

type moduleFinalHopDialer struct {
	dialer module.FinalHopDialer
}

func (d moduleFinalHopDialer) DialTCP(ctx context.Context, target string, profileID *int) (net.Conn, error) {
	return d.dialer.DialTCP(ctx, target, profileID)
}

func (d moduleFinalHopDialer) OpenUDP(ctx context.Context, target string, profileID *int) (relay.UDPPacketPeer, error) {
	return d.dialer.OpenUDP(ctx, target, profileID)
}

type moduleOverlayRuntimeProvider struct {
	overlay module.OverlayRuntime
}

func (p moduleOverlayRuntimeProvider) OverlayRuntime(profileID int) (relay.WireGuardRuntime, bool) {
	return p.OverlayRuntimeForAgent("", profileID)
}

func (p moduleOverlayRuntimeProvider) OverlayRuntimeForAgent(agentID string, profileID int) (relay.WireGuardRuntime, bool) {
	if p.overlay == nil || profileID <= 0 {
		return nil, false
	}
	return moduleOverlayWireGuardRuntime{overlay: p.overlay, agentID: strings.TrimSpace(agentID), profileID: profileID}, true
}

func (p moduleOverlayRuntimeProvider) OverlayRuntimeForHop(hop relay.Hop) (relay.WireGuardRuntime, bool) {
	if hop.Listener.WireGuardProfileID == nil || *hop.Listener.WireGuardProfileID <= 0 {
		return nil, false
	}
	return p.OverlayRuntimeForAgent(hop.Listener.AgentID, *hop.Listener.WireGuardProfileID)
}

type moduleOverlayWireGuardRuntime struct {
	overlay   module.OverlayRuntime
	agentID   string
	profileID int
}

func (r moduleOverlayWireGuardRuntime) DialContext(ctx context.Context, network string, address string) (net.Conn, error) {
	return r.overlay.DialContext(ctx, r.agentID, r.profileID, network, address)
}

func (r moduleOverlayWireGuardRuntime) ListenTCP(ctx context.Context, address string) (net.Listener, error) {
	return r.overlay.ListenTCP(ctx, r.agentID, r.profileID, address)
}

func (r moduleOverlayWireGuardRuntime) ListenTransparentTCP(context.Context) (net.Listener, error) {
	return nil, fmt.Errorf("transparent tcp listener is not provided by overlay.runtime for http")
}

func (r moduleOverlayWireGuardRuntime) ListenUDP(ctx context.Context, address string) (net.PacketConn, error) {
	return r.overlay.ListenUDP(ctx, r.agentID, r.profileID, address)
}

func (r moduleOverlayWireGuardRuntime) ListenTransparentUDP(context.Context, string) (module.TransparentUDPConn, error) {
	return nil, fmt.Errorf("transparent udp listener is not provided by overlay.runtime for http")
}
