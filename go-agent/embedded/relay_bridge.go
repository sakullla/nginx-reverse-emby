package embedded

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
)

type RelayHop struct {
	Address    string
	ServerName string
	Listener   RelayListener
}

type RelayTLSMaterialProvider interface {
	ServerCertificate(ctx context.Context, certificateID int) (*tls.Certificate, error)
	TrustedCAPool(ctx context.Context, certificateIDs []int) (*x509.CertPool, error)
}

func DialRelay(
	ctx context.Context,
	network string,
	target string,
	chain []RelayHop,
	provider RelayTLSMaterialProvider,
) (net.Conn, error) {
	hops := make([]relay.Hop, 0, len(chain))
	for _, hop := range chain {
		hops = append(hops, relay.Hop{
			Address:    hop.Address,
			ServerName: hop.ServerName,
			Listener:   relay.Listener(hop.Listener),
		})
	}
	return relay.Dial(ctx, network, target, hops, relay.TLSMaterialProvider(provider))
}
