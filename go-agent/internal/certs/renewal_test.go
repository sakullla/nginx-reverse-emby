package certs

import (
	"context"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func TestRenewalLoopRenewsExpiredLocalHTTP01Certificate(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC)
	first := mustCreateTLSMaterial(t, certificateSpec{
		commonName: "renew-loop.example.com",
		notBefore:  now.Add(-24 * time.Hour),
		notAfter:   now.Add(2 * time.Hour),
	})
	second := mustCreateTLSMaterial(t, certificateSpec{
		commonName: "renew-loop.example.com",
		notBefore:  now.Add(-time.Hour),
		notAfter:   now.Add(90 * 24 * time.Hour),
	})
	fake := &fakeACMEIssuer{
		results: []acmeIssueResult{
			{CertPEM: first.CertPEM, KeyPEM: first.KeyPEM},
			{CertPEM: second.CertPEM, KeyPEM: second.KeyPEM},
		},
	}

	manager := mustNewManager(
		t,
		t.TempDir(),
		withNow(func() time.Time { return now }),
		withRenewBefore(24*time.Hour),
		withACMEIssuerFactory(func(request acmeIssueRequest) (acmeIssuer, error) {
			return fake, nil
		}),
	)
	policy := model.ManagedCertificatePolicy{
		ID:              9201,
		Domain:          "renew-loop.example.com",
		Enabled:         true,
		Scope:           "domain",
		IssuerMode:      "local_http01",
		CertificateType: "acme",
		Usage:           "https",
	}

	if err := manager.Apply(context.Background(), nil, []model.ManagedCertificatePolicy{policy}); err != nil {
		t.Fatalf("initial apply failed: %v", err)
	}
	if len(fake.requests) != 1 {
		t.Fatalf("expected one initial acme request, got %d", len(fake.requests))
	}

	if err := manager.runRenewalLoopIteration(context.Background()); err != nil {
		t.Fatalf("renewal iteration failed: %v", err)
	}

	if len(fake.requests) != 2 {
		t.Fatalf("expected renewal loop to issue a second certificate, got %d requests", len(fake.requests))
	}
	info, err := manager.CertificateInfo(9201)
	if err != nil {
		t.Fatalf("certificate info failed: %v", err)
	}
	if info.Fingerprint != second.Fingerprint {
		t.Fatalf("expected renewed fingerprint, got %q want %q", info.Fingerprint, second.Fingerprint)
	}
}
