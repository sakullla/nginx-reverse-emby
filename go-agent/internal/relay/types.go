package relay

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
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
	ListenUDP(ctx context.Context, address string) (net.PacketConn, error)
}

type WireGuardRuntimeProvider interface {
	WireGuardRuntime(profileID int) (WireGuardRuntime, bool)
}
