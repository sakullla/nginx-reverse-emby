//go:build !integration

package embedded

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"strings"
	"testing"

	modulepki "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/pki"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay"
)

func TestIntegrationRelayCredentialCompositeExposesTunnelSecurity(t *testing.T) {
	store, err := modulepki.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	provider := WithRelayTunnelCredentials(embeddedRelayNoopProvider{}, &CredentialStore{delegate: store})
	if _, ok := provider.(relay.TunnelCredentialProvider); !ok {
		t.Fatal("credential composite does not expose TunnelCredentialProvider")
	}

	_, err = DialRelay(t.Context(), "tcp", "127.0.0.1:65530", []RelayHop{{
		Address: "127.0.0.1:65531",
		Listener: RelayListener{
			ID: 1, AgentID: "local", Name: "relay-a", ListenHost: "127.0.0.1",
			BindHosts: []string{"127.0.0.1"}, ListenPort: 65531,
			PublicHost: "127.0.0.1", PublicPort: 65531, Enabled: true,
			TLSMode: "pki_mtls", PKIIdentityID: "listener-identity", PKIIdentityState: "active",
		},
	}}, provider)
	if err == nil {
		t.Fatal("DialRelay() expected missing active tunnel security")
	}
	if strings.Contains(err.Error(), "tunnel credential provider is required") || !strings.Contains(err.Error(), modulepki.ErrSecurityInvalid.Error()) {
		t.Fatalf("DialRelay() did not use the embedded credential composite: %v", err)
	}
}

type embeddedRelayNoopProvider struct{}

func (embeddedRelayNoopProvider) ServerCertificate(context.Context, int) (*tls.Certificate, error) {
	return nil, nil
}

func (embeddedRelayNoopProvider) TrustedCAPool(context.Context, []int) (*x509.CertPool, error) {
	return nil, nil
}
