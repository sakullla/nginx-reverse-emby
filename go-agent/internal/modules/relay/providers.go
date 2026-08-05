package relay

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
)

type moduleTLSMaterialProvider struct {
	legacy TLSMaterialProvider
	module *Module
}

func (p moduleTLSMaterialProvider) ServerCertificate(ctx context.Context, certificateID int) (*tls.Certificate, error) {
	if p.legacy == nil {
		return nil, errors.New("managed TLS material provider is unavailable")
	}
	return p.legacy.ServerCertificate(ctx, certificateID)
}

func (p moduleTLSMaterialProvider) TrustedCAPool(ctx context.Context, certificateIDs []int) (*x509.CertPool, error) {
	if p.legacy == nil {
		return nil, errors.New("managed TLS material provider is unavailable")
	}
	return p.legacy.TrustedCAPool(ctx, certificateIDs)
}

func (p moduleTLSMaterialProvider) InstallTunnelCertificate(ctx context.Context, storageIdentity string, config *tls.Config) (TunnelCredentialMetadata, error) {
	provider := p.module.tunnelCredentialProvider()
	if provider == nil {
		return TunnelCredentialMetadata{}, errors.New("tunnel credential provider is unavailable")
	}
	return provider.InstallTunnelCertificate(ctx, storageIdentity, config)
}

func (p moduleTLSMaterialProvider) LoadTunnelCredential(ctx context.Context, storageIdentity string) (TunnelCredentialMetadata, error) {
	provider := p.module.tunnelCredentialProvider()
	if provider == nil {
		return TunnelCredentialMetadata{}, errors.New("tunnel credential provider is unavailable")
	}
	return provider.LoadTunnelCredential(ctx, storageIdentity)
}

func (p moduleTLSMaterialProvider) LoadTunnelSecurity(ctx context.Context) (TunnelSecurityState, error) {
	provider := p.module.tunnelCredentialProvider()
	if provider == nil {
		return TunnelSecurityState{}, errors.New("tunnel credential provider is unavailable")
	}
	return provider.LoadTunnelSecurity(ctx)
}

func FinalHopDialerFromProvider(provider any) FinalHopDialer {
	if dialer, ok := provider.(FinalHopDialer); ok {
		return dialer
	}
	if dialer, ok := provider.(module.FinalHopDialer); ok {
		return moduleFinalHopDialer{dialer: dialer}
	}
	return nil
}

func finalHopDialerFromProvider(provider any) FinalHopDialer {
	return FinalHopDialerFromProvider(provider)
}

type rollbackFinalHopProvider interface {
	PreviousFinalHopDialerForRollback() any
}

func finalHopProviderForRollback(provider any) any {
	rollbackProvider, ok := provider.(rollbackFinalHopProvider)
	if !ok || rollbackProvider == nil {
		return provider
	}
	previous := rollbackProvider.PreviousFinalHopDialerForRollback()
	if previous == nil {
		return provider
	}
	return previous
}

type moduleFinalHopDialer struct {
	dialer module.FinalHopDialer
}

func (d moduleFinalHopDialer) DialTCP(ctx context.Context, target string, profileID *int) (net.Conn, error) {
	return d.dialer.DialTCP(ctx, target, profileID)
}

func (d moduleFinalHopDialer) OpenUDP(ctx context.Context, target string, profileID *int) (UDPPacketPeer, error) {
	return d.dialer.OpenUDP(ctx, target, profileID)
}
