package relay

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

type Listener = model.RelayListener

type Hop struct {
	Address    string   `json:"address"`
	ServerName string   `json:"server_name,omitempty"`
	Listener   Listener `json:"listener"`

	// securityBinding is populated immediately before a tunnel dial. It keeps
	// transport selection, fallback state, and connection pools bound to the
	// same credential and signed PKI security generation without putting local
	// credential state on the control-plane wire.
	securityBinding string
}

type TLSMaterialProvider interface {
	ServerCertificate(ctx context.Context, certificateID int) (*tls.Certificate, error)
	TrustedCAPool(ctx context.Context, certificateIDs []int) (*x509.CertPool, error)
}

const (
	TLSModePKIMTLS                  = "pki_mtls"
	AgentTunnelCredentialIdentity   = "agent"
	PKIIdentityStateActive          = "active"
	PKIIdentityStateEnrollmentReady = "enrollment_required"
	PKIIdentityStateRevoked         = "revoked"
)

// TunnelCredentialMetadata is the public, serialization-safe binding for one
// locally active tunnel key. Private key material remains owned by the PKI
// store and can only be installed into a caller-owned tls.Config.
type TunnelCredentialMetadata struct {
	Generation                  string
	CredentialFingerprintSHA256 string
	IdentityID                  string
	CertificateID               string
	Purpose                     string
	AuthorityID                 string
	CAGeneration                int64
	PKIDomainID                 string
	PKIEpoch                    int64
	SecurityRevision            int64
	AgentID                     string
	ListenerID                  string
	DNSNames                    []string
	IPAddresses                 []string
}

type TunnelSecurityState struct {
	Hash     string
	Snapshot model.PKISecuritySnapshot
}

// TunnelCredentialProvider is deliberately separate from TLSMaterialProvider:
// public managed certificates remain the owner for non-tunnel TLS, while the
// tunnel PKI store owns key callbacks and signed security state for pki_mtls.
type TunnelCredentialProvider interface {
	InstallTunnelCertificate(context.Context, string, *tls.Config) (TunnelCredentialMetadata, error)
	LoadTunnelCredential(context.Context, string) (TunnelCredentialMetadata, error)
	LoadTunnelSecurity(context.Context) (TunnelSecurityState, error)
}

func relayListenerStorageIdentity(listenerID int) string {
	return fmt.Sprintf("listener-%d", listenerID)
}

func tunnelCredentialFingerprint(certificatePEM string) (string, error) {
	certificate, err := parseFirstCertificatePEM(certificatePEM)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(certificate.Raw)
	return hex.EncodeToString(digest[:]), nil
}

func normalizeTunnelFingerprint(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
