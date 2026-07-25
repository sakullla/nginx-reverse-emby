package localagent

import (
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestLocalSyncSourceOverlaysPendingManagedCertificateGeneration(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	const domain = "local-pending.example.test"
	if err := store.SaveManagedCertificates(t.Context(), []storage.ManagedCertificateRow{{
		ID: 301, Domain: domain, Enabled: true, Scope: "domain", IssuerMode: "master_cf_dns",
		TargetAgentIDs: `[]`, Status: "pending", CertificateType: "acme", Usage: "https", Revision: 3,
	}}); err != nil {
		t.Fatalf("SaveManagedCertificates() error = %v", err)
	}
	pending, err := store.StageManagedCertificateGeneration(t.Context(), domain, storage.ManagedCertificateBundle{
		ID: 301, Domain: domain, Revision: 3, CertPEM: "pending-cert", KeyPEM: "pending-key",
	})
	if err != nil {
		t.Fatalf("StageManagedCertificateGeneration() error = %v", err)
	}

	snapshot, err := NewSyncSource(store, "local").Sync(t.Context(), SyncRequest{})
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if len(snapshot.Certificates) != 1 || snapshot.Certificates[0].CertPEM != pending.Material.CertPEM || snapshot.Certificates[0].KeyPEM != pending.Material.KeyPEM {
		t.Fatalf("snapshot certificates = %+v", snapshot.Certificates)
	}
	if len(snapshot.CertificatePolicies) != 1 || snapshot.CertificatePolicies[0].ID != 301 || snapshot.CertificatePolicies[0].IssuerMode != "master_cf_dns" {
		t.Fatalf("snapshot certificate policies = %+v", snapshot.CertificatePolicies)
	}
	if _, found, err := store.LoadActiveManagedCertificateGeneration(t.Context(), domain); err != nil || found {
		t.Fatalf("active generation before local report found=%v error=%v", found, err)
	}
}
