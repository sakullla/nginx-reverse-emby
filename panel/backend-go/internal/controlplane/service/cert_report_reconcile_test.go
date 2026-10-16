package service

import (
	"reflect"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestCertificateReportCapableAgentDoesNotInferIssuanceFromApply(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	rules := []storage.HTTPRuleRow{{ID: 2, Enabled: true, FrontendURL: "https://media.example.com"}}
	for _, status := range []string{"pending", "error", "active"} {
		t.Run(status, func(t *testing.T) {
			cert := ManagedCertificate{
				ID: 1, Domain: "media.example.com", Enabled: true, Scope: "domain",
				IssuerMode: "local_http01", CertificateType: "acme", TargetAgentIDs: []string{"local"},
				Revision: 1, Status: status, LastError: "authorization failed",
				BackoffClass: "persistent", RetryCount: 1, NextRetryAtUnix: now.Add(time.Hour).Unix(),
			}
			rows := []storage.ManagedCertificateRow{managedCertificateToRow(cert)}
			// A previously applied revision can succeed without this certificate.
			// Its heartbeat must not claim that the current HTTPS rule was issued.
			got, changed := reconcileLocalHTTP01CertificatesForAgent(rows, "local", defaultLocalCapabilities,
				rules, 5, "success", "", nil, now)
			if changed || !reflect.DeepEqual(got, rows) {
				t.Fatalf("missing certificate report fabricated issuance: changed=%v certificate=%+v", changed, got[0])
			}
		})
	}
}

func TestCertificateReconcilePreservesExplicitReportAndLegacyFallback(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	cert := ManagedCertificate{
		ID: 1, Domain: "media.example.com", Enabled: true, Scope: "domain",
		IssuerMode: "local_http01", CertificateType: "acme", TargetAgentIDs: []string{"local"},
		Revision: 1, Status: "pending",
	}
	rows := []storage.ManagedCertificateRow{managedCertificateToRow(cert)}
	rules := []storage.HTTPRuleRow{{ID: 2, Enabled: true, FrontendURL: "https://media.example.com"}}
	legacy, changed := reconcileLocalHTTP01CertificatesForAgent(rows, "local", []string{"cert_install", "local_acme"},
		rules, 5, "success", "", nil, now)
	if !changed || legacy[0].Status != "active" {
		t.Fatal("legacy agent without certificate reports lost compatibility")
	}
	report := ManagedCertificateHeartbeatReport{
		ID: 1, Status: "active", MaterialHash: "issued-material", LastIssueAt: now.Format(time.RFC3339),
		NotAfter: now.Add(90 * 24 * time.Hour).Format(time.RFC3339),
	}
	reported, ids, changed := applyManagedCertificateHeartbeatReports(rows, "local", []ManagedCertificateHeartbeatReport{report}, now)
	if !changed || reported[0].Status != "active" || reported[0].MaterialHash != report.MaterialHash {
		t.Fatal("explicit issuance report was not applied")
	}
	got, changed := reconcileLocalHTTP01CertificatesForAgent(reported, "local", defaultLocalCapabilities,
		rules, 5, "success", "", ids, now.Add(time.Minute))
	if changed || !reflect.DeepEqual(got, reported) {
		t.Fatal("configuration heartbeat rewrote certificate issuance metadata")
	}
}
