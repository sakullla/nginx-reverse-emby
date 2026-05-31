package module

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
)

const (
	ProviderTLSMaterial            ProviderRef = "tls.material"
	ProviderOverlayRuntime         ProviderRef = "overlay.runtime"
	ProviderTransparentListener    ProviderRef = "transparent.listener"
	ProviderFinalHopDialer         ProviderRef = "finalhop.dialer"
	ProviderEgressResolver         ProviderRef = "egress.resolver"
	ProviderTrafficSink            ProviderRef = "traffic.sink"
	ProviderDiagnosticsHTTPSource  ProviderRef = "diagnostics.http.source"
	ProviderDiagnosticsL4Source    ProviderRef = "diagnostics.l4.source"
	ProviderDiagnosticsRelaySource ProviderRef = "diagnostics.relay.source"
)

type TLSMaterial interface {
	ServerCertificate(ctx context.Context, certificateID int) (*tls.Certificate, error)
	TrustedCAPool(ctx context.Context, certificateIDs []int) (*x509.CertPool, error)
}

type HostTLSMaterial interface {
	ServerCertificateForHost(ctx context.Context, host string) (*tls.Certificate, error)
}

type OverlayRuntime interface {
	DialContext(ctx context.Context, agentID string, profileID int, network string, address string) (net.Conn, error)
	ListenTCP(ctx context.Context, agentID string, profileID int, address string) (net.Listener, error)
	ListenTransparentTCP(ctx context.Context, agentID string, profileID int) (net.Listener, error)
	ListenUDP(ctx context.Context, agentID string, profileID int, address string) (net.PacketConn, error)
}

type FinalHopDialer interface {
	DialTCP(ctx context.Context, target string, profileID *int) (net.Conn, error)
	OpenUDP(ctx context.Context, target string, profileID *int) (any, error)
}
