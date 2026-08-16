//go:build !integration

package embedded

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
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

func TestRelayCredentialCompositePreservesActiveMetadata(t *testing.T) {
	metadata := embeddedRelayCredentialMetadata(t)
	security := modulepki.SecurityState{
		Hash: "security-hash",
		Snapshot: model.PKISecuritySnapshot{
			PKIDomainID: "domain-1", PKIEpoch: 3, SecurityRevision: 8, Full: true,
		},
	}
	store := &embeddedRelayCredentialStore{metadata: metadata, security: security}
	provider := relayCompositeTLSMaterialProvider{
		legacy: embeddedRelayNoopProvider{},
		tunnel: embeddedRelayTunnelCredentialProvider{store: store},
	}
	tunnel := relay.TunnelCredentialProvider(provider)

	loadedSecurity, err := tunnel.LoadTunnelSecurity(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if loadedSecurity.Hash != security.Hash || !reflect.DeepEqual(loadedSecurity.Snapshot, security.Snapshot) {
		t.Fatalf("loaded security = %+v, want %+v", loadedSecurity, security)
	}

	loaded, err := tunnel.LoadTunnelCredential(t.Context(), "listener-71")
	if err != nil {
		t.Fatal(err)
	}
	assertEmbeddedRelayMetadata(t, loaded)

	tlsConfig := &tls.Config{}
	installed, err := tunnel.InstallTunnelCertificate(t.Context(), "listener-71", tlsConfig)
	if err != nil {
		t.Fatal(err)
	}
	assertEmbeddedRelayMetadata(t, installed)
	if store.installIdentity != "listener-71" || store.installConfig != tlsConfig {
		t.Fatalf("install binding = %q/%p, want listener-71/%p", store.installIdentity, store.installConfig, tlsConfig)
	}
}

type embeddedRelayNoopProvider struct{}

func (embeddedRelayNoopProvider) ServerCertificate(context.Context, int) (*tls.Certificate, error) {
	return nil, nil
}

func (embeddedRelayNoopProvider) TrustedCAPool(context.Context, []int) (*x509.CertPool, error) {
	return nil, nil
}

type embeddedRelayCredentialStore struct {
	metadata        modulepki.CredentialMetadata
	security        modulepki.SecurityState
	installIdentity string
	installConfig   *tls.Config
}

func (store *embeddedRelayCredentialStore) InstallTLSCertificate(identity string, config *tls.Config) (modulepki.CredentialMetadata, error) {
	store.installIdentity = identity
	store.installConfig = config
	return store.metadata, nil
}

func (store *embeddedRelayCredentialStore) LoadActiveCredential(identity string) (modulepki.CredentialMetadata, error) {
	if identity != "listener-71" {
		return modulepki.CredentialMetadata{}, errors.New("credential not found")
	}
	return store.metadata, nil
}

func (store *embeddedRelayCredentialStore) LoadSecuritySnapshot() (modulepki.SecurityState, error) {
	return store.security, nil
}

func embeddedRelayCredentialMetadata(t *testing.T) modulepki.CredentialMetadata {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(71),
		Subject:      pkix.Name{CommonName: "listener-71"},
		DNSNames:     []string{"relay.example.com"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, privateKey.Public(), privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return modulepki.CredentialMetadata{Manifest: modulepki.CredentialManifest{
		Generation: "generation-7", PKIDomainID: "domain-1", PKIEpoch: 3, SecurityRevision: 8,
		Credential: model.PKITunnelCredential{
			IdentityID: "identity-71", CertificateID: "certificate-71", Purpose: model.PKICertificatePurposeServer,
			CertificatePEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
			AuthorityID:    "authority-1", CAGeneration: 4,
		},
		Expectation: modulepki.CredentialExpectation{
			DomainID: "domain-1", AgentID: "agent-1", Kind: model.PKIIdentityKindListener,
			ListenerID: "71", Purpose: model.PKICertificatePurposeServer,
			DNSNames: []string{"relay.example.com"}, IPAddresses: []string{"192.0.2.71"},
		},
	}}
}

func assertEmbeddedRelayMetadata(t *testing.T, metadata relay.TunnelCredentialMetadata) {
	t.Helper()
	if metadata.Generation != "generation-7" || metadata.IdentityID != "identity-71" ||
		metadata.CertificateID != "certificate-71" || metadata.CredentialFingerprintSHA256 == "" ||
		metadata.Purpose != model.PKICertificatePurposeServer || metadata.AuthorityID != "authority-1" || metadata.CAGeneration != 4 ||
		metadata.PKIDomainID != "domain-1" || metadata.PKIEpoch != 3 || metadata.SecurityRevision != 8 ||
		metadata.AgentID != "agent-1" || metadata.ListenerID != "71" ||
		!reflect.DeepEqual(metadata.DNSNames, []string{"relay.example.com"}) ||
		!reflect.DeepEqual(metadata.IPAddresses, []string{"192.0.2.71"}) {
		t.Fatalf("relay credential metadata = %+v", metadata)
	}
}
