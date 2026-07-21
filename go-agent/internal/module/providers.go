package module

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

const (
	ProviderTLSMaterial            ProviderRef = "tls.material"
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

type FinalHopDialer interface {
	DialTCP(ctx context.Context, target string, profileID *int) (net.Conn, error)
	OpenUDP(ctx context.Context, target string, profileID *int) (UDPPeer, error)
}

type EgressResolver interface {
	Resolve(id *int, network string) (model.EgressProfile, bool, error)
}

type UDPPeer interface {
	Close() error
	SetReadDeadline(time.Time) error
	SetWriteDeadline(time.Time) error
	ReadPacket() ([]byte, error)
	WritePacket([]byte) error
}
