package http

import (
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
)

func TestAttentionCertificateExpiryUsesShortLivedIPRenewalWindow(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 21, 14, 0, 0, 0, time.UTC)
	cert := service.ManagedCertificate{
		Scope:           "ip",
		CertificateType: "acme",
		IssuerMode:      "local_http01",
		LastIssueAt:     now.Add(-24 * time.Hour).Format(time.RFC3339Nano),
		NotAfter:        now.Add(6 * 24 * time.Hour).Format(time.RFC3339),
	}
	if attentionCertificateExpiresSoon(cert, now, now.Add(attentionCertExpiryWindow)) {
		t.Fatal("fresh short-lived IP certificate was reported as expiring")
	}
	cert.NotAfter = now.Add(2 * 24 * time.Hour).Format(time.RFC3339)
	cert.LastIssueAt = now.Add(-5 * 24 * time.Hour).Format(time.RFC3339Nano)
	if !attentionCertificateExpiresSoon(cert, now, now.Add(attentionCertExpiryWindow)) {
		t.Fatal("short-lived IP certificate inside its one-third renewal window was not reported")
	}
}

func TestAttentionCertificateExpiryKeepsDefaultWindowForOtherCertificates(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 21, 14, 0, 0, 0, time.UTC)
	for name, cert := range map[string]service.ManagedCertificate{
		"domain acme": {
			Scope: "domain", CertificateType: "acme", IssuerMode: "master_cf_dns",
			LastIssueAt: now.Add(-70 * 24 * time.Hour).Format(time.RFC3339Nano),
			NotAfter:    now.Add(20 * 24 * time.Hour).Format(time.RFC3339),
		},
		"uploaded ip": {
			Scope: "ip", CertificateType: "uploaded", IssuerMode: "local_http01",
			LastIssueAt: now.Add(-70 * 24 * time.Hour).Format(time.RFC3339Nano),
			NotAfter:    now.Add(20 * 24 * time.Hour).Format(time.RFC3339),
		},
		"ip with unknown issue time": {
			Scope: "ip", CertificateType: "acme", IssuerMode: "local_http01",
			NotAfter: now.Add(20 * 24 * time.Hour).Format(time.RFC3339),
		},
	} {
		t.Run(name, func(t *testing.T) {
			if !attentionCertificateExpiresSoon(cert, now, now.Add(attentionCertExpiryWindow)) {
				t.Fatal("certificate inside the default 30-day window was not reported")
			}
		})
	}
}
