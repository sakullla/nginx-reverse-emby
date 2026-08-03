package app

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/core"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	modulepki "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/pki"
	modulerelay "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay"
)

type relayTunnelCredentialStore interface {
	InstallTLSCertificate(string, *tls.Config) (modulepki.CredentialMetadata, error)
	LoadActiveCredential(string) (modulepki.CredentialMetadata, error)
	LoadSecuritySnapshot() (modulepki.SecurityState, error)
}

type appRelayTunnelCredentialProvider struct {
	store relayTunnelCredentialStore
}

func (p appRelayTunnelCredentialProvider) InstallTunnelCertificate(ctx context.Context, storageIdentity string, config *tls.Config) (modulerelay.TunnelCredentialMetadata, error) {
	if err := contextErr(ctx); err != nil {
		return modulerelay.TunnelCredentialMetadata{}, err
	}
	if p.store == nil {
		return modulerelay.TunnelCredentialMetadata{}, errors.New("tunnel credential store is unavailable")
	}
	metadata, err := p.store.InstallTLSCertificate(storageIdentity, config)
	if err != nil {
		return modulerelay.TunnelCredentialMetadata{}, err
	}
	return relayTunnelCredentialMetadata(metadata)
}

func (p appRelayTunnelCredentialProvider) LoadTunnelCredential(ctx context.Context, storageIdentity string) (modulerelay.TunnelCredentialMetadata, error) {
	if err := contextErr(ctx); err != nil {
		return modulerelay.TunnelCredentialMetadata{}, err
	}
	if p.store == nil {
		return modulerelay.TunnelCredentialMetadata{}, errors.New("tunnel credential store is unavailable")
	}
	metadata, err := p.store.LoadActiveCredential(storageIdentity)
	if err != nil {
		return modulerelay.TunnelCredentialMetadata{}, err
	}
	return relayTunnelCredentialMetadata(metadata)
}

func (p appRelayTunnelCredentialProvider) LoadTunnelSecurity(ctx context.Context) (modulerelay.TunnelSecurityState, error) {
	if err := contextErr(ctx); err != nil {
		return modulerelay.TunnelSecurityState{}, err
	}
	if p.store == nil {
		return modulerelay.TunnelSecurityState{}, errors.New("tunnel credential store is unavailable")
	}
	security, err := p.store.LoadSecuritySnapshot()
	if err != nil {
		return modulerelay.TunnelSecurityState{}, err
	}
	return modulerelay.TunnelSecurityState{Hash: security.Hash, Snapshot: security.Snapshot}, nil
}

func relayTunnelCredentialMetadata(metadata modulepki.CredentialMetadata) (modulerelay.TunnelCredentialMetadata, error) {
	manifest := metadata.Manifest
	credential := manifest.Credential
	fingerprint, err := relayTunnelCertificateFingerprint(credential.CertificatePEM)
	if err != nil {
		return modulerelay.TunnelCredentialMetadata{}, err
	}
	return modulerelay.TunnelCredentialMetadata{
		Generation:                  manifest.Generation,
		CredentialFingerprintSHA256: fingerprint,
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

func relayTunnelCertificateFingerprint(encoded string) (string, error) {
	block, _ := pem.Decode([]byte(strings.TrimSpace(encoded)))
	if block == nil || block.Type != "CERTIFICATE" {
		return "", errors.New("active tunnel certificate PEM is invalid")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("parse active tunnel certificate: %w", err)
	}
	digest := sha256.Sum256(certificate.Raw)
	return hex.EncodeToString(digest[:]), nil
}

func contextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

type relaySecuritySyncClient struct {
	delegate SyncClient
	runtime  *core.Runtime
	module   *modulerelay.Module
}

func (c *relaySecuritySyncClient) Sync(ctx context.Context, request SyncRequest) (Snapshot, error) {
	snapshot, err := c.delegate.Sync(ctx, request)
	if err != nil {
		return Snapshot{}, err
	}
	c.reconcile(ctx, snapshot)
	return snapshot, nil
}

func (c *relaySecuritySyncClient) reconcile(ctx context.Context, heartbeat Snapshot) {
	if c == nil || c.module == nil {
		return
	}
	if err := c.module.ReconcileTunnelSecurity(ctx); err != nil {
		// Tunnel PKI is intentionally fail-closed without converting an otherwise
		// authenticated X-Agent-Token heartbeat into a control transport failure.
		log.Printf("[agent] relay tunnel security fenced: %v", err)
	}
	if heartbeat.RelayListeners == nil || c.runtime == nil {
		return
	}
	missing := missingActiveTunnelListenerIDs(c.runtime.ActiveSnapshot().RelayListeners, heartbeat.RelayListeners)
	if len(missing) == 0 {
		return
	}
	if err := c.module.FenceTunnelListeners(ctx, missing, "heartbeat omitted active pki_mtls listeners"); err != nil {
		log.Printf("[agent] close omitted pki_mtls relay listeners: %v", err)
	}
}

func missingActiveTunnelListenerIDs(active, heartbeat []model.RelayListener) []int {
	present := make(map[int]struct{}, len(heartbeat))
	for _, listener := range heartbeat {
		if listener.Enabled && strings.EqualFold(strings.TrimSpace(listener.TLSMode), modulerelay.TLSModePKIMTLS) {
			present[listener.ID] = struct{}{}
		}
	}
	missing := make([]int, 0)
	for _, listener := range active {
		if !listener.Enabled || !strings.EqualFold(strings.TrimSpace(listener.TLSMode), modulerelay.TLSModePKIMTLS) {
			continue
		}
		if _, ok := present[listener.ID]; !ok {
			missing = append(missing, listener.ID)
		}
	}
	return missing
}

type relaySecurityRevisionSyncClient struct {
	*relaySecuritySyncClient
	revision core.RevisionSyncClient
}

func (c *relaySecurityRevisionSyncClient) PullRevision(ctx context.Context) (model.RevisionPull, error) {
	return c.revision.PullRevision(ctx)
}

func (c *relaySecurityRevisionSyncClient) StartRevision(ctx context.Context, input model.RevisionStart) error {
	return c.revision.StartRevision(ctx, input)
}

func (c *relaySecurityRevisionSyncClient) ReportRevision(ctx context.Context, input model.RevisionReport) error {
	return c.revision.ReportRevision(ctx, input)
}

func (a *App) relayMTLSSyncClient() SyncClient {
	if a == nil {
		return nil
	}
	if a.syncClient == nil || a.moduleRegistry == nil {
		return a.syncClient
	}
	var relayModule *modulerelay.Module
	for _, configured := range a.moduleRegistry.Modules() {
		if candidate, ok := configured.(*modulerelay.Module); ok {
			relayModule = candidate
			break
		}
	}
	if relayModule == nil {
		return a.syncClient
	}
	if a.pkiStore != nil {
		provider := appRelayTunnelCredentialProvider{store: a.pkiStore}
		relayModule.SetTunnelCredentialProvider(provider)
		modulerelay.SetProcessTunnelCredentialProvider(provider)
	}
	wrapped := &relaySecuritySyncClient{delegate: a.syncClient, runtime: a.runtime, module: relayModule}
	if revision, ok := a.syncClient.(core.RevisionSyncClient); ok {
		return &relaySecurityRevisionSyncClient{relaySecuritySyncClient: wrapped, revision: revision}
	}
	return wrapped
}
