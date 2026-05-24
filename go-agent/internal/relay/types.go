package relay

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/wireguard"
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
	ListenTransparentUDP(ctx context.Context, address string) (wireguard.TransparentUDPConn, error)
}

type WireGuardRuntimeProvider interface {
	WireGuardRuntime(profileID int) (WireGuardRuntime, bool)
}

type AgentWireGuardRuntimeProvider interface {
	WireGuardRuntimeProvider
	WireGuardRuntimeForAgent(agentID string, profileID int) (WireGuardRuntime, bool)
}

type HopWireGuardRuntimeProvider interface {
	WireGuardRuntimeForHop(hop Hop) (WireGuardRuntime, bool)
}

func ResolveWireGuardRuntime(provider WireGuardRuntimeProvider, agentID string, profileID int) (WireGuardRuntime, bool) {
	if provider == nil {
		return nil, false
	}
	if agentProvider, ok := provider.(AgentWireGuardRuntimeProvider); ok && agentID != "" {
		return agentProvider.WireGuardRuntimeForAgent(agentID, profileID)
	}
	return provider.WireGuardRuntime(profileID)
}

func ResolveWireGuardRuntimeForHop(provider WireGuardRuntimeProvider, hop Hop) (WireGuardRuntime, bool) {
	if provider == nil || hop.Listener.WireGuardProfileID == nil || *hop.Listener.WireGuardProfileID <= 0 {
		return nil, false
	}
	runtime, ok := ResolveWireGuardRuntime(provider, hop.Listener.AgentID, *hop.Listener.WireGuardProfileID)
	if ok {
		return runtime, true
	}
	if hopProvider, ok := provider.(HopWireGuardRuntimeProvider); ok {
		return hopProvider.WireGuardRuntimeForHop(hop)
	}
	return nil, false
}
