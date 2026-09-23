//go:build exhaustive && !integration

package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type renewalTimeoutTestIssuer struct {
	calls []int
}

func (i *renewalTimeoutTestIssuer) Issue(context.Context, ManagedCertificate) (managedCertificateRenewalResult, error) {
	return managedCertificateRenewalResult{}, nil
}

func (i *renewalTimeoutTestIssuer) Renew(ctx context.Context, cert ManagedCertificate) (managedCertificateRenewalResult, error) {
	i.calls = append(i.calls, cert.ID)
	if cert.ID == 1 {
		<-ctx.Done()
		return managedCertificateRenewalResult{}, ctx.Err()
	}
	return managedCertificateRenewalResult{}, nil
}

func TestRunRenewalPassContinuesAfterACMETimeoutAndSchedulesRetry(t *testing.T) {
	store := newServiceOwnerStore(t)
	now := time.Date(2026, time.September, 23, 14, 0, 0, 0, time.UTC)
	rows := []storage.ManagedCertificateRow{
		managedCertificateToRow(ManagedCertificate{
			ID: 1, Domain: "timeout.example.com", Enabled: true, Scope: "domain", IssuerMode: "master_cf_dns",
			CertificateType: "acme", TargetAgentIDs: []string{"local"}, Status: "active", Revision: 1,
			ACMEInfo: ManagedCertificateACMEInfo{Renew: now.Add(-time.Hour).Format(time.RFC3339)},
		}),
		managedCertificateToRow(ManagedCertificate{
			ID: 2, Domain: "healthy.example.com", Enabled: true, Scope: "domain", IssuerMode: "master_cf_dns",
			CertificateType: "acme", TargetAgentIDs: []string{"local"}, Status: "active", Revision: 2,
			ACMEInfo: ManagedCertificateACMEInfo{Renew: now.Add(-time.Hour).Format(time.RFC3339)},
		}),
	}
	if err := store.SaveManagedCertificates(t.Context(), rows); err != nil {
		t.Fatalf("seed managed certificates: %v", err)
	}

	issuer := &renewalTimeoutTestIssuer{}
	service := newCertificateServiceWithRenewal(config.Config{
		LocalAgentID:                  "local",
		ManagedCertificateACMETimeout: 20 * time.Millisecond,
	}, store, issuer)
	service.now = func() time.Time { return now }
	service.revisionMutation = true

	err := service.RunRenewalPass(context.Background())
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("renewal pass error = %v, want timeout", err)
	}
	if len(issuer.calls) != 2 || issuer.calls[0] != 1 || issuer.calls[1] != 2 {
		t.Fatalf("renewal issuer calls = %v, want [1 2]", issuer.calls)
	}

	persisted, err := store.ListManagedCertificates(t.Context())
	if err != nil {
		t.Fatalf("reload managed certificates: %v", err)
	}
	failed := managedCertificateFromRow(persisted[0])
	if failed.Status != "error" || failed.NextRetryAtUnix <= now.Unix() || failed.LastError == "" {
		t.Fatalf("timed out certificate state = %+v", failed)
	}
	healthy := managedCertificateFromRow(persisted[1])
	if healthy.Status != "active" || healthy.NextRetryAtUnix != 0 {
		t.Fatalf("healthy certificate state = %+v", healthy)
	}
	next, err := service.NextManagedCertificateRenewalRetryAt(t.Context(), now)
	if err != nil {
		t.Fatalf("find next renewal retry: %v", err)
	}
	if !next.After(now) || next.Unix() != failed.NextRetryAtUnix {
		t.Fatalf("next renewal retry = %s, failed retry = %d", next, failed.NextRetryAtUnix)
	}
}
