package service

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestCertificateServiceRunRenewalPassSingleFlightsPerCertificateID(t *testing.T) {
	const certID = 60
	now := time.Date(2026, 4, 11, 1, 2, 3, 0, time.UTC)
	renewedMaterial := mustCreateSelfSignedCA(t, "SingleFlight Material")

	blocked := make(chan struct{})
	proceed := make(chan struct{})
	issuer := &blockingManagedCertificateRenewalIssuer{
		blocked: blocked,
		proceed: proceed,
		result: managedCertificateRenewalResult{
			Changed:      true,
			LastIssueAt:  "2026-04-11T01:02:03Z",
			MaterialHash: "single-flight-hash",
			ACMEInfo: ManagedCertificateACMEInfo{
				MainDomain: "single.example.com",
				CA:         "LetsEncrypt",
				Renew:      "2026-07-10T00:00:00Z",
			},
			Material: storage.ManagedCertificateBundle{
				CertPEM: strings.TrimSpace(renewedMaterial.CertPEM),
				KeyPEM:  strings.TrimSpace(renewedMaterial.KeyPEM),
			},
		},
	}

	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              certID,
			Domain:          "single.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "master_cf_dns",
			TargetAgentIDs:  `["local"]`,
			Status:          "active",
			ACMEInfo:        `{"Main_Domain":"single.example.com","Renew":"2026-04-10T00:00:00Z"}`,
			CertificateType: "acme",
			Usage:           "https",
			Revision:        1,
		}},
	}

	svc1 := newCertificateServiceWithRenewal(config.Config{LocalAgentID: "local"}, store, issuer)
	svc1.now = func() time.Time { return now }
	svc2 := newCertificateServiceWithRenewal(config.Config{LocalAgentID: "local"}, store, issuer)
	svc2.now = func() time.Time { return now }

	var err1, err2 error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		err1 = svc1.RunRenewalPass(context.Background())
	}()
	go func() {
		defer wg.Done()
		err2 = svc2.RunRenewalPass(context.Background())
	}()

	// Wait until the first issuer call is blocked.
	<-blocked

	// Let the first call proceed.
	close(proceed)

	wg.Wait()

	// At least one must succeed.
	if err1 != nil && err2 != nil {
		t.Fatalf("both calls failed: err1=%v, err2=%v", err1, err2)
	}

	// Exactly one issuance should have occurred.
	if issuer.callCount() != 1 {
		t.Fatalf("issuer callCount = %d, want 1", issuer.callCount())
	}
}

type blockingManagedCertificateRenewalIssuer struct {
	blocked chan struct{}
	proceed chan struct{}
	mu      sync.Mutex
	calls   []int
	result  managedCertificateRenewalResult
}

func (b *blockingManagedCertificateRenewalIssuer) Issue(_ context.Context, cert ManagedCertificate) (managedCertificateRenewalResult, error) {
	return b.issueOrRenew(cert)
}

func (b *blockingManagedCertificateRenewalIssuer) Renew(_ context.Context, cert ManagedCertificate) (managedCertificateRenewalResult, error) {
	return b.issueOrRenew(cert)
}

func (b *blockingManagedCertificateRenewalIssuer) issueOrRenew(cert ManagedCertificate) (managedCertificateRenewalResult, error) {
	b.mu.Lock()
	first := len(b.calls) == 0
	b.calls = append(b.calls, cert.ID)
	b.mu.Unlock()

	if first {
		// Signal that we are blocked and wait to proceed.
		b.blocked <- struct{}{}
		<-b.proceed
	}
	return b.result, nil
}

func (b *blockingManagedCertificateRenewalIssuer) callCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.calls)
}
