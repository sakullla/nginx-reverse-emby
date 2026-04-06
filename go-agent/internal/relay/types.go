package relay

import (
	"context"
	"crypto/tls"
	"crypto/x509"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

type Listener = model.RelayListener

type Hop struct {
	Address  string   `json:"address"`
	Listener Listener `json:"listener"`
}

type TLSMaterialProvider interface {
	ServerCertificate(ctx context.Context, certificateID int) (*tls.Certificate, error)
	TrustedCAPool(ctx context.Context, certificateIDs []int) (*x509.CertPool, error)
}
