package service

import (
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestOverlayManagedCertificateForAgentPreservesMasterIssuanceBackoff(t *testing.T) {
	t.Parallel()
	cert := ManagedCertificate{
		IssuerMode:      "master_cf_dns",
		NextRetryAtUnix: 1786254268,
		RetryCount:      2,
		BackoffClass:    managedCertificateBackoffClassPersistent,
		AgentReports: map[string]ManagedCertificateAgentReport{
			"edge-1": {Status: "active", MaterialHash: "installed-hash"},
		},
	}

	overlaid := overlayManagedCertificateForAgent(cert, "edge-1")
	if overlaid.NextRetryAtUnix != cert.NextRetryAtUnix || overlaid.RetryCount != cert.RetryCount || overlaid.BackoffClass != cert.BackoffClass {
		t.Fatalf("master issuance backoff was overwritten by install report: %+v", overlaid)
	}
}

func TestApplyManagedCertificateHeartbeatReportsKeepsMultiAgentBackoffPerAgent(t *testing.T) {
	t.Parallel()
	rows, _, changed := applyManagedCertificateHeartbeatReports([]storage.ManagedCertificateRow{{
		ID:              31,
		Domain:          "shared.example.com",
		Enabled:         true,
		Scope:           "domain",
		IssuerMode:      "local_http01",
		TargetAgentIDs:  `["edge-a","edge-b"]`,
		Status:          "pending",
		AgentReports:    `{}`,
		CertificateType: "acme",
		Usage:           "https",
	}}, "edge-a", []ManagedCertificateHeartbeatReport{{
		ID:              31,
		Status:          "error",
		NextRetryAtUnix: 1786254268,
		RetryCount:      2,
		BackoffClass:    managedCertificateBackoffClassPersistent,
	}}, time.Date(2026, time.August, 9, 3, 30, 0, 0, time.UTC))
	if !changed || len(rows) != 1 {
		t.Fatalf("apply reports changed=%v rows=%+v", changed, rows)
	}
	cert := managedCertificateFromRow(rows[0])
	if cert.NextRetryAtUnix != 0 || cert.RetryCount != 0 || cert.BackoffClass != "" {
		t.Fatalf("multi-agent top-level backoff was overwritten: %+v", cert)
	}
	report := cert.AgentReports["edge-a"]
	if report.NextRetryAtUnix != 1786254268 || report.RetryCount != 2 || report.BackoffClass != managedCertificateBackoffClassPersistent {
		t.Fatalf("per-agent backoff was not retained: %+v", report)
	}
}
