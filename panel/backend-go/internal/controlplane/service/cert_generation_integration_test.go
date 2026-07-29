//go:build integration

package service

import (
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestIntegrationManagedCertificateGenerationIntegrationAckPromotionAfterRestart(t *testing.T) {
	if testing.Short() {
		t.Skip("SQLite restart and acknowledgement promotion run in the full integration tier")
	}
	t.Parallel()

	dataRoot := t.TempDir()
	var store *storage.SQLiteStore
	initialized := false
	t.Cleanup(func() {
		if store != nil {
			_ = store.Close()
		}
	})
	open := func() {
		t.Helper()
		var err error
		if !initialized {
			store, err = newServiceTestSQLiteStore(t, dataRoot, "local")
			initialized = err == nil
		} else {
			store, err = openExistingServiceTestSQLiteStore(dataRoot, "local")
		}
		if err != nil {
			t.Fatalf("storage.NewSQLiteStore() error = %v", err)
		}
	}
	closeStore := func() {
		t.Helper()
		if err := store.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
		store = nil
	}

	const (
		certificateID = 801
		domain        = "ack-restart.example.test"
	)
	open()
	for _, agentID := range []string{"edge-a", "edge-b"} {
		if err := store.SaveAgent(t.Context(), storage.AgentRow{
			ID: agentID, Name: agentID, AgentToken: "integration-token-" + agentID,
			Platform: "linux-amd64", CapabilitiesJSON: `["cert_install","managed_certificate_reports_v1"]`,
			DesiredRevision: 1, CurrentRevision: 1, LastApplyRevision: 1, LastApplyStatus: "success",
		}); err != nil {
			t.Fatalf("SaveAgent(%s) error = %v", agentID, err)
		}
	}

	oldMaterial := mustCreateLeafSignedByCA(t, domain, mustCreateSelfSignedCA(t, "old-integration-ca.example.test"))
	newMaterial := mustCreateLeafSignedByCA(t, domain, mustCreateSelfSignedCA(t, "new-integration-ca.example.test"))
	oldBundle := storage.ManagedCertificateBundle{
		ID: certificateID, Domain: domain, Revision: 4,
		CertPEM: oldMaterial.CertPEM, KeyPEM: oldMaterial.KeyPEM,
	}
	newBundle := storage.ManagedCertificateBundle{
		ID: certificateID, Domain: domain, Revision: 5,
		CertPEM: newMaterial.CertPEM, KeyPEM: newMaterial.KeyPEM,
	}
	oldHash := hashManagedCertificateMaterial(oldBundle.CertPEM, oldBundle.KeyPEM)
	row := storage.ManagedCertificateRow{
		ID: certificateID, Domain: domain, Enabled: true, Scope: "domain", IssuerMode: "master_cf_dns",
		TargetAgentIDs: `["edge-a","edge-b"]`, Status: "active", MaterialHash: oldHash,
		CertificateType: "acme", Usage: "https", Revision: 4,
		ACMEInfo: `{"Main_Domain":"ack-restart.example.test","Profile":"shortlived","CA":"old-integration-ca.example.test"}`,
	}
	if err := store.SaveManagedCertificates(t.Context(), []storage.ManagedCertificateRow{row}); err != nil {
		t.Fatalf("SaveManagedCertificates(seed) error = %v", err)
	}
	active, err := store.StageManagedCertificateGeneration(t.Context(), domain, oldBundle)
	if err != nil {
		t.Fatalf("StageManagedCertificateGeneration(active) error = %v", err)
	}
	if err := store.PromoteManagedCertificateGeneration(t.Context(), domain, active.ID, active.MaterialHash); err != nil {
		t.Fatalf("PromoteManagedCertificateGeneration(active) error = %v", err)
	}
	pending, err := store.StageManagedCertificateGeneration(t.Context(), domain, newBundle)
	if err != nil {
		t.Fatalf("StageManagedCertificateGeneration(pending) error = %v", err)
	}
	createdAt, err := time.Parse(time.RFC3339Nano, pending.CreatedAt)
	if err != nil {
		t.Fatalf("parse pending CreatedAt: %v", err)
	}
	closeStore()

	open()
	assertManagedCertificateGenerationIntegrationPending(t, store, domain, active.ID, oldHash, pending.ID, pending.MaterialHash)
	desired, err := overlayPendingManagedCertificateGenerations(t.Context(), store, "edge-a", storage.Snapshot{})
	if err != nil {
		t.Fatalf("overlayPendingManagedCertificateGenerations() error = %v", err)
	}
	if len(desired.Certificates) != 1 || desired.Certificates[0].CertPEM != newBundle.CertPEM || desired.Certificates[0].KeyPEM != newBundle.KeyPEM {
		t.Fatalf("desired pending certificate snapshot = %+v", desired.Certificates)
	}
	activeProjection, found, err := store.LoadManagedCertificateMaterial(t.Context(), domain)
	if err != nil || !found || activeProjection.CertPEM != oldBundle.CertPEM || activeProjection.KeyPEM != oldBundle.KeyPEM {
		t.Fatalf("active projection while pending = (%+v, %v, %v)", activeProjection, found, err)
	}

	svc := NewCertificateService(config.Config{}, store)
	fresh := createdAt.Add(time.Second).Format(time.RFC3339Nano)
	stale := createdAt.Add(-time.Second).Format(time.RFC3339Nano)
	testCases := []struct {
		name    string
		reports map[string]ManagedCertificateAgentReport
	}{
		{
			name: "stale acknowledgement",
			reports: map[string]ManagedCertificateAgentReport{
				"edge-a": {Status: "active", MaterialHash: pending.MaterialHash, UpdatedAt: stale},
				"edge-b": {Status: "active", MaterialHash: pending.MaterialHash, UpdatedAt: fresh},
			},
		},
		{
			name: "partial acknowledgement",
			reports: map[string]ManagedCertificateAgentReport{
				"edge-a": {Status: "active", MaterialHash: pending.MaterialHash, UpdatedAt: fresh},
			},
		},
		{
			name: "apply error acknowledgement",
			reports: map[string]ManagedCertificateAgentReport{
				"edge-a": {Status: "active", MaterialHash: pending.MaterialHash, UpdatedAt: fresh},
				"edge-b": {Status: "error", MaterialHash: pending.MaterialHash, UpdatedAt: fresh},
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			saveManagedCertificateGenerationIntegrationReports(t, store, certificateID, testCase.reports)
			promoted, err := svc.reconcileManagedCertificateGenerationPromotions(t.Context())
			if err != nil {
				t.Fatalf("reconcileManagedCertificateGenerationPromotions() error = %v", err)
			}
			if promoted != 0 {
				t.Fatalf("promoted generations = %d, want 0", promoted)
			}
			assertManagedCertificateGenerationIntegrationPending(t, store, domain, active.ID, oldHash, pending.ID, pending.MaterialHash)
		})
	}

	saveManagedCertificateGenerationIntegrationReports(t, store, certificateID, map[string]ManagedCertificateAgentReport{
		"edge-a": {Status: "active", MaterialHash: pending.MaterialHash, UpdatedAt: fresh},
		"edge-b": {Status: "active", MaterialHash: pending.MaterialHash, UpdatedAt: fresh},
	})
	promoted, err := svc.reconcileManagedCertificateGenerationPromotions(t.Context())
	if err != nil || promoted != 1 {
		t.Fatalf("matching acknowledgement promoted=%d error=%v", promoted, err)
	}
	activeAfter, found, err := store.LoadActiveManagedCertificateGeneration(t.Context(), domain)
	if err != nil || !found || activeAfter.ID != pending.ID || activeAfter.MaterialHash != pending.MaterialHash {
		t.Fatalf("active after matching acknowledgement = (%+v, %v, %v)", activeAfter, found, err)
	}
	if _, found, err := store.LoadPendingManagedCertificateGeneration(t.Context(), domain); err != nil || found {
		t.Fatalf("pending after matching acknowledgement found=%v error=%v", found, err)
	}
	promoted, err = svc.reconcileManagedCertificateGenerationPromotions(t.Context())
	if err != nil || promoted != 0 {
		t.Fatalf("repeated matching acknowledgement promoted=%d error=%v", promoted, err)
	}
	closeStore()

	open()
	activeAfterRestart, found, err := store.LoadActiveManagedCertificateGeneration(t.Context(), domain)
	if err != nil || !found || activeAfterRestart.ID != pending.ID || activeAfterRestart.MaterialHash != pending.MaterialHash {
		t.Fatalf("promoted active after restart = (%+v, %v, %v)", activeAfterRestart, found, err)
	}
	rows, err := store.ListManagedCertificates(t.Context())
	if err != nil || len(rows) != 1 {
		t.Fatalf("ListManagedCertificates() = (%+v, %v)", rows, err)
	}
	if rows[0].Status != "active" || rows[0].MaterialHash != pending.MaterialHash {
		t.Fatalf("public certificate row after restart = %+v", rows[0])
	}
}

func saveManagedCertificateGenerationIntegrationReports(t *testing.T, store *storage.SQLiteStore, certificateID int, reports map[string]ManagedCertificateAgentReport) {
	t.Helper()
	rows, err := store.ListManagedCertificates(t.Context())
	if err != nil {
		t.Fatalf("ListManagedCertificates() error = %v", err)
	}
	cert, index, found := findManagedCertificateByID(rows, certificateID)
	if !found {
		t.Fatalf("managed certificate %d was not found", certificateID)
	}
	cert.AgentReports = reports
	rows[index] = managedCertificateToRow(cert)
	if err := store.SaveManagedCertificates(t.Context(), rows); err != nil {
		t.Fatalf("SaveManagedCertificates(reports) error = %v", err)
	}
}

func assertManagedCertificateGenerationIntegrationPending(
	t *testing.T,
	store *storage.SQLiteStore,
	domain, activeID, activeHash, pendingID, pendingHash string,
) {
	t.Helper()
	active, found, err := store.LoadActiveManagedCertificateGeneration(t.Context(), domain)
	if err != nil || !found || active.ID != activeID || active.MaterialHash != activeHash {
		t.Fatalf("active generation = (%+v, %v, %v)", active, found, err)
	}
	pending, found, err := store.LoadPendingManagedCertificateGeneration(t.Context(), domain)
	if err != nil || !found || pending.ID != pendingID || pending.MaterialHash != pendingHash {
		t.Fatalf("pending generation = (%+v, %v, %v)", pending, found, err)
	}
	rows, err := store.ListManagedCertificates(t.Context())
	if err != nil || len(rows) != 1 {
		t.Fatalf("ListManagedCertificates() = (%+v, %v)", rows, err)
	}
	if rows[0].Status != "active" || rows[0].MaterialHash != activeHash {
		t.Fatalf("public certificate row promoted early: %+v", rows[0])
	}
}
