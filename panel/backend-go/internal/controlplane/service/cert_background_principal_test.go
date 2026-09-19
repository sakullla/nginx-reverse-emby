//go:build exhaustive && !integration

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type failingBackgroundCertificateIssuer struct {
	called bool
}

func (i *failingBackgroundCertificateIssuer) Issue(context.Context, ManagedCertificate) (managedCertificateRenewalResult, error) {
	i.called = true
	return managedCertificateRenewalResult{}, errors.New("expected issuer failure")
}

func (i *failingBackgroundCertificateIssuer) Renew(context.Context, ManagedCertificate) (managedCertificateRenewalResult, error) {
	return managedCertificateRenewalResult{}, errors.New("unexpected renewal")
}

func TestManagedCertificateBackgroundSignerSuppliesSystemMutationPrincipal(t *testing.T) {
	t.Parallel()
	store := newServiceOwnerStore(t)
	cert := ManagedCertificate{ID: 8, Domain: "example.com", Enabled: true, Scope: "domain", IssuerMode: "master_cf_dns", CertificateType: "acme", Status: "issuing", Revision: 1}
	if err := store.SaveManagedCertificates(t.Context(), []storage.ManagedCertificateRow{managedCertificateToRow(cert)}); err != nil {
		t.Fatal(err)
	}
	issuer := &failingBackgroundCertificateIssuer{}
	sign := managedCertificateBackgroundSignerWithIssuer(config.Config{}, func() (storage.Store, error) {
		return store, nil
	}, issuer, nil)
	err := sign(context.Background(), cert.ID)
	if errors.Is(err, ErrMutationPrincipalRequired) {
		t.Fatalf("background signer missing system principal: %v", err)
	}
	if !issuer.called {
		t.Fatalf("issuer was not reached: %v", err)
	}
}
