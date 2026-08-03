package embedded

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"strings"

	modulepki "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/pki"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay"
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

// WithRelayTunnelCredentials combines public managed-certificate material
// with the embedded runtime's private tunnel identity facade. The returned
// provider keeps legacy relay modes compatible while making pki_mtls usable
// without relying on process-global initialization order.
func WithRelayTunnelCredentials(provider RelayTLSMaterialProvider, credentials *CredentialStore) RelayTLSMaterialProvider {
	var tunnel relay.TunnelCredentialProvider
	if credentials != nil && credentials.delegate != nil {
		tunnel = embeddedRelayTunnelCredentialProvider{store: credentials.delegate}
	}
	return relayCompositeTLSMaterialProvider{legacy: provider, tunnel: tunnel}
}

type relayCompositeTLSMaterialProvider struct {
	legacy RelayTLSMaterialProvider
	tunnel relay.TunnelCredentialProvider
}

func (p relayCompositeTLSMaterialProvider) ServerCertificate(ctx context.Context, certificateID int) (*tls.Certificate, error) {
	if p.legacy == nil {
		return nil, errors.New("managed TLS material provider is unavailable")
	}
	return p.legacy.ServerCertificate(ctx, certificateID)
}

func (p relayCompositeTLSMaterialProvider) TrustedCAPool(ctx context.Context, certificateIDs []int) (*x509.CertPool, error) {
	if p.legacy == nil {
		return nil, errors.New("managed TLS material provider is unavailable")
	}
	return p.legacy.TrustedCAPool(ctx, certificateIDs)
}

func (p relayCompositeTLSMaterialProvider) InstallTunnelCertificate(ctx context.Context, storageIdentity string, config *tls.Config) (relay.TunnelCredentialMetadata, error) {
	if p.tunnel == nil {
		return relay.TunnelCredentialMetadata{}, errors.New("embedded tunnel credential store is unavailable")
	}
	return p.tunnel.InstallTunnelCertificate(ctx, storageIdentity, config)
}

func (p relayCompositeTLSMaterialProvider) LoadTunnelCredential(ctx context.Context, storageIdentity string) (relay.TunnelCredentialMetadata, error) {
	if p.tunnel == nil {
		return relay.TunnelCredentialMetadata{}, errors.New("embedded tunnel credential store is unavailable")
	}
	return p.tunnel.LoadTunnelCredential(ctx, storageIdentity)
}

func (p relayCompositeTLSMaterialProvider) LoadTunnelSecurity(ctx context.Context) (relay.TunnelSecurityState, error) {
	if p.tunnel == nil {
		return relay.TunnelSecurityState{}, errors.New("embedded tunnel credential store is unavailable")
	}
	return p.tunnel.LoadTunnelSecurity(ctx)
}

type relayTunnelCredentialStore interface {
	InstallTLSCertificate(string, *tls.Config) (modulepki.CredentialMetadata, error)
	LoadActiveCredential(string) (modulepki.CredentialMetadata, error)
	LoadSecuritySnapshot() (modulepki.SecurityState, error)
}

type embeddedRelayTunnelCredentialProvider struct {
	store relayTunnelCredentialStore
}

func (p embeddedRelayTunnelCredentialProvider) InstallTunnelCertificate(ctx context.Context, storageIdentity string, config *tls.Config) (relay.TunnelCredentialMetadata, error) {
	if err := relayBridgeContextError(ctx); err != nil {
		return relay.TunnelCredentialMetadata{}, err
	}
	if p.store == nil {
		return relay.TunnelCredentialMetadata{}, errors.New("embedded tunnel credential store is unavailable")
	}
	metadata, err := p.store.InstallTLSCertificate(storageIdentity, config)
	if err != nil {
		return relay.TunnelCredentialMetadata{}, err
	}
	return embeddedRelayTunnelCredentialMetadata(metadata)
}

func (p embeddedRelayTunnelCredentialProvider) LoadTunnelCredential(ctx context.Context, storageIdentity string) (relay.TunnelCredentialMetadata, error) {
	if err := relayBridgeContextError(ctx); err != nil {
		return relay.TunnelCredentialMetadata{}, err
	}
	if p.store == nil {
		return relay.TunnelCredentialMetadata{}, errors.New("embedded tunnel credential store is unavailable")
	}
	metadata, err := p.store.LoadActiveCredential(storageIdentity)
	if err != nil {
		return relay.TunnelCredentialMetadata{}, err
	}
	return embeddedRelayTunnelCredentialMetadata(metadata)
}

func (p embeddedRelayTunnelCredentialProvider) LoadTunnelSecurity(ctx context.Context) (relay.TunnelSecurityState, error) {
	if err := relayBridgeContextError(ctx); err != nil {
		return relay.TunnelSecurityState{}, err
	}
	if p.store == nil {
		return relay.TunnelSecurityState{}, errors.New("embedded tunnel credential store is unavailable")
	}
	security, err := p.store.LoadSecuritySnapshot()
	if err != nil {
		return relay.TunnelSecurityState{}, err
	}
	return relay.TunnelSecurityState{Hash: security.Hash, Snapshot: security.Snapshot}, nil
}

func embeddedRelayTunnelCredentialMetadata(metadata modulepki.CredentialMetadata) (relay.TunnelCredentialMetadata, error) {
	manifest := metadata.Manifest
	credential := manifest.Credential
	block, _ := pem.Decode([]byte(strings.TrimSpace(credential.CertificatePEM)))
	if block == nil || block.Type != "CERTIFICATE" {
		return relay.TunnelCredentialMetadata{}, errors.New("active tunnel certificate PEM is invalid")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return relay.TunnelCredentialMetadata{}, fmt.Errorf("parse active tunnel certificate: %w", err)
	}
	digest := sha256.Sum256(certificate.Raw)
	return relay.TunnelCredentialMetadata{
		Generation:                  manifest.Generation,
		CredentialFingerprintSHA256: hex.EncodeToString(digest[:]),
		IdentityID:                  credential.IdentityID,
		CertificateID:               credential.CertificateID,
		Purpose:                     credential.Purpose,
		AuthorityID:                 credential.AuthorityID,
		CAGeneration:                credential.CAGeneration,
		PKIDomainID:                 manifest.PKIDomainID,
		PKIEpoch:                    manifest.PKIEpoch,
		SecurityRevision:            manifest.SecurityRevision,
		AgentID:                     manifest.Expectation.AgentID,
		ListenerID:                  manifest.Expectation.ListenerID,
		DNSNames:                    append([]string(nil), manifest.Expectation.DNSNames...),
		IPAddresses:                 append([]string(nil), manifest.Expectation.IPAddresses...),
	}, nil
}

func relayBridgeContextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
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
