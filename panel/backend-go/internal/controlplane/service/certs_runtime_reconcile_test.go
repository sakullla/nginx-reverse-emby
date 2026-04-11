package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type localRuntimeManagedCertificateStoreStub struct {
	managedCerts []storage.ManagedCertificateRow
	rulesByAgent map[string][]storage.HTTPRuleRow
	saveCalled   bool
}

func (s *localRuntimeManagedCertificateStoreStub) ListManagedCertificates(context.Context) ([]storage.ManagedCertificateRow, error) {
	return append([]storage.ManagedCertificateRow(nil), s.managedCerts...), nil
}

func (s *localRuntimeManagedCertificateStoreStub) ListHTTPRules(_ context.Context, agentID string) ([]storage.HTTPRuleRow, error) {
	return append([]storage.HTTPRuleRow(nil), s.rulesByAgent[agentID]...), nil
}

func (s *localRuntimeManagedCertificateStoreStub) SaveManagedCertificates(_ context.Context, rows []storage.ManagedCertificateRow) error {
	s.managedCerts = append([]storage.ManagedCertificateRow(nil), rows...)
	s.saveCalled = true
	return nil
}

func TestReconcileManagedCertificatesFromLocalRuntimeStateUsesMetadataDrivenErrorOutcome(t *testing.T) {
	store := &localRuntimeManagedCertificateStoreStub{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              21,
			Domain:          "sync.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["local"]`,
			Status:          "pending",
			MaterialHash:    "hash-21",
			AgentReports:    `{}`,
			ACMEInfo:        `{"Main_Domain":"sync.example.com"}`,
			Usage:           "https",
			CertificateType: "acme",
			Revision:        4,
		}},
		rulesByAgent: map[string][]storage.HTTPRuleRow{
			"local": {{
				ID:          9,
				AgentID:     "local",
				FrontendURL: "https://sync.example.com",
				Enabled:     true,
				Revision:    4,
			}},
		},
	}

	err := ReconcileManagedCertificatesFromLocalRuntimeState(context.Background(), store, "local", storage.RuntimeState{
		CurrentRevision:   4,
		LastApplyRevision: 2,
		LastApplyStatus:   "success",
		Status:            "active",
		Metadata: map[string]string{
			"last_sync_error":     "apply failed",
			"last_apply_revision": "4",
			"last_apply_status":   "error",
			"last_apply_message":  "apply failed",
		},
	}, time.Date(2026, time.April, 11, 14, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("ReconcileManagedCertificatesFromLocalRuntimeState() error = %v", err)
	}
	if !store.saveCalled {
		t.Fatal("SaveManagedCertificates() was not called")
	}

	cert := managedCertificateFromRow(store.managedCerts[0])
	if cert.Status != "error" || cert.LastError != "apply failed" {
		t.Fatalf("unexpected reconciled cert = %+v", cert)
	}
	report := cert.AgentReports["local"]
	if report.Status != "error" || report.LastError != "apply failed" {
		t.Fatalf("unexpected reconciled report = %+v", report)
	}
}

func TestReconcileManagedCertificatesFromLocalRuntimeStateKeepsExplicitReportsAuthoritativeOnError(t *testing.T) {
	store := &localRuntimeManagedCertificateStoreStub{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              22,
			Domain:          "sync.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "local_http01",
			TargetAgentIDs:  `["local"]`,
			Status:          "pending",
			MaterialHash:    "hash-22",
			AgentReports:    `{}`,
			ACMEInfo:        `{"Main_Domain":"sync.example.com"}`,
			Usage:           "https",
			CertificateType: "acme",
			Revision:        4,
		}},
		rulesByAgent: map[string][]storage.HTTPRuleRow{
			"local": {{
				ID:          9,
				AgentID:     "local",
				FrontendURL: "https://sync.example.com",
				Enabled:     true,
				Revision:    4,
			}},
		},
	}

	err := ReconcileManagedCertificatesFromLocalRuntimeState(context.Background(), store, "local", storage.RuntimeState{
		CurrentRevision: 4,
		Status:          "active",
		Metadata: map[string]string{
			"last_sync_error":   "apply failed",
			"last_apply_status": "error",
		},
		ManagedCertificateReports: []storage.ManagedCertificateReport{{
			ID:           22,
			Domain:       "SYNC.EXAMPLE.COM",
			Status:       "active",
			LastIssueAt:  "2026-04-11T13:00:00Z",
			MaterialHash: "hash-22-new",
			ACMEInfo: storage.ManagedCertificateACMEInfo{
				MainDomain: "sync.example.com",
			},
		}},
	}, time.Date(2026, time.April, 11, 14, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("ReconcileManagedCertificatesFromLocalRuntimeState() error = %v", err)
	}
	if !store.saveCalled {
		t.Fatal("SaveManagedCertificates() was not called")
	}

	cert := managedCertificateFromRow(store.managedCerts[0])
	if cert.Status != "active" || cert.MaterialHash != "hash-22-new" {
		t.Fatalf("explicit report should remain authoritative even when apply status is error: %+v", cert)
	}
	report := cert.AgentReports["local"]
	if report.Status != "active" || report.MaterialHash != "hash-22-new" {
		t.Fatalf("unexpected explicit report overlay = %+v", report)
	}
}

func TestManagedCertificateHeartbeatReportsFromRuntimeState(t *testing.T) {
	converted := managedCertificateHeartbeatReportsFromRuntimeState([]storage.ManagedCertificateReport{{
		ID:           99,
		Domain:       "a.example.com",
		Status:       "active",
		LastIssueAt:  "2026-04-11T13:00:00Z",
		LastError:    "",
		MaterialHash: "hash-99",
		ACMEInfo: storage.ManagedCertificateACMEInfo{
			MainDomain: "a.example.com",
			KeyLength:  "ec256",
		},
		UpdatedAt: "2026-04-11T13:30:00Z",
	}})
	if len(converted) != 1 {
		t.Fatalf("converted reports = %+v", converted)
	}
	raw, err := json.Marshal(converted[0].ACMEInfo)
	if err != nil {
		t.Fatalf("json.Marshal(ACMEInfo) error = %v", err)
	}
	if string(raw) == "{}" {
		t.Fatalf("ACMEInfo unexpectedly empty after conversion: %s", raw)
	}
}
