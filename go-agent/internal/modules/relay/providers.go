package relay

import (
	"context"
	"fmt"
	"net"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
)

func overlayRuntimeFromProvider(provider any) module.OverlayRuntime {
	if overlay, ok := provider.(module.OverlayRuntime); ok {
		return overlay
	}
	return nil
}

func FinalHopDialerFromProvider(provider any) FinalHopDialer {
	if dialer, ok := provider.(FinalHopDialer); ok {
		return dialer
	}
	if dialer, ok := provider.(module.FinalHopDialer); ok {
		return moduleFinalHopDialer{dialer: dialer}
	}
	return nil
}

func finalHopDialerFromProvider(provider any) FinalHopDialer {
	return FinalHopDialerFromProvider(provider)
}

type rollbackFinalHopProvider interface {
	PreviousFinalHopDialerForRollback() any
}

func finalHopProviderForRollback(provider any) any {
	rollbackProvider, ok := provider.(rollbackFinalHopProvider)
	if !ok || rollbackProvider == nil {
		return provider
	}
	previous := rollbackProvider.PreviousFinalHopDialerForRollback()
	if previous == nil {
		return provider
	}
	return previous
}

type moduleFinalHopDialer struct {
	dialer module.FinalHopDialer
}

func (d moduleFinalHopDialer) DialTCP(ctx context.Context, target string, profileID *int) (net.Conn, error) {
	return d.dialer.DialTCP(ctx, target, profileID)
}

func (d moduleFinalHopDialer) OpenUDP(ctx context.Context, target string, profileID *int) (UDPPacketPeer, error) {
	return d.dialer.OpenUDP(ctx, target, profileID)
}

type moduleOverlayRuntimeProvider struct {
	overlay module.OverlayRuntime
}

func (p moduleOverlayRuntimeProvider) OverlayRuntime(profileID int) (WireGuardRuntime, bool) {
	return p.OverlayRuntimeForAgent("", profileID)
}

func (p moduleOverlayRuntimeProvider) OverlayRuntimeForAgent(agentID string, profileID int) (WireGuardRuntime, bool) {
	if p.overlay == nil || profileID <= 0 {
		return nil, false
	}
	return moduleOverlayWireGuardRuntime{overlay: p.overlay, agentID: strings.TrimSpace(agentID), profileID: profileID}, true
}

func (p moduleOverlayRuntimeProvider) OverlayRuntimeForHop(hop Hop) (WireGuardRuntime, bool) {
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

func (r moduleOverlayWireGuardRuntime) ListenTransparentTCP(ctx context.Context) (net.Listener, error) {
	return nil, fmt.Errorf("transparent tcp listener is not provided by overlay.runtime for relay")
}

func (r moduleOverlayWireGuardRuntime) ListenUDP(ctx context.Context, address string) (net.PacketConn, error) {
	return r.overlay.ListenUDP(ctx, r.agentID, r.profileID, address)
}

func (r moduleOverlayWireGuardRuntime) ListenTransparentUDP(ctx context.Context, address string) (module.TransparentUDPConn, error) {
	return nil, fmt.Errorf("transparent udp listener is not provided by overlay.runtime for relay")
}
