package relay

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
)

type Listener = model.RelayListener

type Hop struct {
	Address    string   `json:"address"`
	ServerName string   `json:"server_name,omitempty"`
	Listener   Listener `json:"listener"`
}

type TLSMaterialProvider interface {
	ServerCertificate(ctx context.Context, certificateID int) (*tls.Certificate, error)
	TrustedCAPool(ctx context.Context, certificateIDs []int) (*x509.CertPool, error)
}

type WireGuardRuntime interface {
	DialContext(ctx context.Context, network string, address string) (net.Conn, error)
	ListenTCP(ctx context.Context, address string) (net.Listener, error)
	ListenTransparentTCP(ctx context.Context) (net.Listener, error)
	ListenUDP(ctx context.Context, address string) (net.PacketConn, error)
	ListenTransparentUDP(ctx context.Context, address string) (module.TransparentUDPConn, error)
}

type OverlayRuntimeProvider interface {
	OverlayRuntime(profileID int) (WireGuardRuntime, bool)
}

type AgentOverlayRuntimeProvider interface {
	OverlayRuntimeProvider
	OverlayRuntimeForAgent(agentID string, profileID int) (WireGuardRuntime, bool)
}

type HopOverlayRuntimeProvider interface {
	OverlayRuntimeForHop(hop Hop) (WireGuardRuntime, bool)
}

func ResolveOverlayRuntime(provider OverlayRuntimeProvider, agentID string, profileID int) (WireGuardRuntime, bool) {
	if provider == nil {
		return nil, false
	}
	if agentProvider, ok := provider.(AgentOverlayRuntimeProvider); ok && agentID != "" {
		return agentProvider.OverlayRuntimeForAgent(agentID, profileID)
	}
	return provider.OverlayRuntime(profileID)
}

func ResolveOverlayRuntimeForHop(provider OverlayRuntimeProvider, hop Hop) (WireGuardRuntime, bool) {
	if provider == nil || hop.Listener.WireGuardProfileID == nil || *hop.Listener.WireGuardProfileID <= 0 {
		return nil, false
	}
	runtime, ok := ResolveOverlayRuntime(provider, hop.Listener.AgentID, *hop.Listener.WireGuardProfileID)
	if ok {
		return runtime, true
	}
	if hopProvider, ok := provider.(HopOverlayRuntimeProvider); ok {
		return hopProvider.OverlayRuntimeForHop(hop)
	}
	return nil, false
}

func (o *DialOptions) applyOverlayRuntimeProvider() {
	if o == nil || o.OverlayProvider != nil || o.OverlayRuntime == nil {
		return
	}
	o.OverlayProvider = overlayRuntimeProvider{
		overlay:     o.OverlayRuntime,
		transparent: o.TransparentListener,
		agentID:     strings.TrimSpace(o.OverlayAgentID),
	}
}

type overlayRuntimeProvider struct {
	overlay     module.OverlayRuntime
	transparent module.TransparentListener
	agentID     string
}

func (p overlayRuntimeProvider) OverlayRuntime(profileID int) (WireGuardRuntime, bool) {
	return p.OverlayRuntimeForAgent(p.agentID, profileID)
}

func (p overlayRuntimeProvider) OverlayRuntimeForAgent(agentID string, profileID int) (WireGuardRuntime, bool) {
	if p.overlay == nil || profileID <= 0 {
		return nil, false
	}
	return overlayRuntime{
		overlay:     p.overlay,
		transparent: p.transparent,
		agentID:     strings.TrimSpace(agentID),
		profileID:   profileID,
	}, true
}

func (p overlayRuntimeProvider) OverlayRuntimeForHop(hop Hop) (WireGuardRuntime, bool) {
	if hop.Listener.WireGuardProfileID == nil || *hop.Listener.WireGuardProfileID <= 0 {
		return nil, false
	}
	return p.OverlayRuntimeForAgent(hop.Listener.AgentID, *hop.Listener.WireGuardProfileID)
}

type overlayRuntime struct {
	overlay     module.OverlayRuntime
	transparent module.TransparentListener
	agentID     string
	profileID   int
}

func (r overlayRuntime) DialContext(ctx context.Context, network string, address string) (net.Conn, error) {
	return r.overlay.DialContext(ctx, r.agentID, r.profileID, network, address)
}

func (r overlayRuntime) ListenTCP(ctx context.Context, address string) (net.Listener, error) {
	return r.overlay.ListenTCP(ctx, r.agentID, r.profileID, address)
}

func (r overlayRuntime) ListenTransparentTCP(ctx context.Context) (net.Listener, error) {
	if r.transparent == nil {
		return nil, fmt.Errorf("transparent listener provider is required")
	}
	return r.transparent.ListenTransparentTCP(ctx, r.agentID, r.profileID)
}

func (r overlayRuntime) ListenUDP(ctx context.Context, address string) (net.PacketConn, error) {
	return r.overlay.ListenUDP(ctx, r.agentID, r.profileID, address)
}

func (r overlayRuntime) ListenTransparentUDP(ctx context.Context, address string) (module.TransparentUDPConn, error) {
	if r.transparent == nil {
		return nil, fmt.Errorf("transparent listener provider is required")
	}
	return r.transparent.ListenTransparentUDP(ctx, r.agentID, r.profileID, address)
}
