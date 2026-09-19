//go:build integration

package storage

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestIntegrationManagedCertificateGenerationStageIsInvisibleUntilHashGatedPromote(t *testing.T) {
	t.Parallel()
	dataRoot := t.TempDir()
	store, err := newStorageTestSQLiteStore(t, dataRoot, "local", true)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ctx := t.Context()
	const domain = "generation.example.com"
	if err := store.SaveManagedCertificates(ctx, []ManagedCertificateRow{{ID: 1, Domain: domain, Enabled: true}}); err != nil {
		t.Fatalf("SaveManagedCertificates() error = %v", err)
	}
	bundle := ManagedCertificateBundle{Domain: domain, CertPEM: "certificate-v1", KeyPEM: "private-key-v1"}
	pending, err := store.StageManagedCertificateGeneration(ctx, domain, bundle)
	if err != nil {
		t.Fatalf("StageManagedCertificateGeneration() error = %v", err)
	}
	if pending.ID == "" || pending.MaterialHash == "" || pending.State != ManagedCertificateGenerationStatePending {
		t.Fatalf("pending generation = %#v", pending)
	}
	if _, ok, err := store.LoadActiveManagedCertificateGeneration(ctx, domain); err != nil || ok {
		t.Fatalf("LoadActiveManagedCertificateGeneration() before promote = (ok=%v, err=%v), want invisible", ok, err)
	}
	loadedPending, ok, err := store.LoadPendingManagedCertificateGeneration(ctx, domain)
	if err != nil || !ok || loadedPending.ID != pending.ID {
		t.Fatalf("LoadPendingManagedCertificateGeneration() = (%#v, %v, %v)", loadedPending, ok, err)
	}

	err = store.PromoteManagedCertificateGeneration(ctx, domain, pending.ID, "wrong-material-hash")
	if !errors.Is(err, ErrManagedCertificateGenerationHashMismatch) {
		t.Fatalf("PromoteManagedCertificateGeneration(wrong hash) error = %v, want hash mismatch", err)
	}
	if _, ok, err := store.LoadActiveManagedCertificateGeneration(ctx, domain); err != nil || ok {
		t.Fatalf("LoadActiveManagedCertificateGeneration() after rejected promote = (ok=%v, err=%v)", ok, err)
	}

	if err := store.PromoteManagedCertificateGeneration(ctx, domain, pending.ID, pending.MaterialHash); err != nil {
		t.Fatalf("PromoteManagedCertificateGeneration() error = %v", err)
	}
	active, ok, err := store.LoadActiveManagedCertificateGeneration(ctx, domain)
	if err != nil || !ok {
		t.Fatalf("LoadActiveManagedCertificateGeneration() = (%#v, %v, %v)", active, ok, err)
	}
	if active.ID != pending.ID || active.State != ManagedCertificateGenerationStateActive || active.Material != bundle {
		t.Fatalf("active generation = %#v, want promoted pending generation", active)
	}
	if _, ok, err := store.LoadPendingManagedCertificateGeneration(ctx, domain); err != nil || ok {
		t.Fatalf("LoadPendingManagedCertificateGeneration() after promote = (ok=%v, err=%v)", ok, err)
	}
	projected, ok, err := store.LoadManagedCertificateMaterial(ctx, domain)
	if err != nil || !ok || projected != bundle {
		t.Fatalf("LoadManagedCertificateMaterial() projection = (%#v, %v, %v)", projected, ok, err)
	}
}

func TestIntegrationManagedCertificateGenerationProjectionFailureCompensatesAndReconciles(t *testing.T) {
	t.Parallel()
	store := newManagedCertificateGenerationTestStore(t)
	ctx := t.Context()
	const domain = "projection-failure.example.com"
	seedManagedCertificateGenerationRow(t, store, domain)
	previous := stageManagedCertificateGenerationForTest(t, store, domain, "cert-previous", "key-previous")
	promoteManagedCertificateGenerationForTest(t, store, domain, previous)
	pending := stageManagedCertificateGenerationForTest(t, store, domain, "cert-next", "key-next")

	keyPath := filepath.Join(store.managedCertificateDirectory(domain), "key")
	if err := os.Remove(keyPath); err != nil {
		t.Fatalf("remove projection key fixture: %v", err)
	}
	if err := os.Mkdir(keyPath, 0o700); err != nil {
		t.Fatalf("create projection obstruction: %v", err)
	}
	if err := store.PromoteManagedCertificateGeneration(ctx, domain, pending.ID, pending.MaterialHash); err == nil {
		t.Fatal("PromoteManagedCertificateGeneration() projection error = nil")
	}
	active, ok, err := store.LoadActiveManagedCertificateGeneration(ctx, domain)
	if err != nil || !ok || active.ID != previous.ID {
		t.Fatalf("active after failed projection = (%#v, %v, %v)", active, ok, err)
	}
	if err := os.RemoveAll(keyPath); err != nil {
		t.Fatalf("remove projection obstruction: %v", err)
	}
	if err := store.ReconcileManagedCertificateGenerations(ctx, domain); err != nil {
		t.Fatalf("ReconcileManagedCertificateGenerations() error = %v", err)
	}
	legacy, ok, err := store.readManagedCertificateMaterialSecure(domain)
	if err != nil || !ok || legacy.CertPEM != previous.Material.CertPEM || legacy.KeyPEM != previous.Material.KeyPEM {
		t.Fatalf("legacy projection after reconcile = (%#v, %v, %v)", legacy, ok, err)
	}
}

func newManagedCertificateGenerationTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	store, err := newStorageTestSQLiteStore(t, t.TempDir(), "local", true)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func seedManagedCertificateGenerationRow(t *testing.T, store *GormStore, domain string) {
	t.Helper()
	seedManagedCertificateGenerationRowWithID(t, store, 1, domain)
}

func seedManagedCertificateGenerationRowWithID(t *testing.T, store *GormStore, id int, domain string) {
	t.Helper()
	if err := store.SaveManagedCertificates(t.Context(), []ManagedCertificateRow{{ID: id, Domain: domain, Enabled: true}}); err != nil {
		t.Fatalf("SaveManagedCertificates() error = %v", err)
	}
}

func stageManagedCertificateGenerationForTest(t *testing.T, store *GormStore, domain, certPEM, keyPEM string) ManagedCertificateGeneration {
	t.Helper()
	generation, err := store.StageManagedCertificateGeneration(t.Context(), domain, ManagedCertificateBundle{Domain: domain, CertPEM: certPEM, KeyPEM: keyPEM})
	if err != nil {
		t.Fatalf("StageManagedCertificateGeneration() error = %v", err)
	}
	return generation
}

func promoteManagedCertificateGenerationForTest(t *testing.T, store *GormStore, domain string, generation ManagedCertificateGeneration) {
	t.Helper()
	if err := store.PromoteManagedCertificateGeneration(t.Context(), domain, generation.ID, generation.MaterialHash); err != nil {
		t.Fatalf("PromoteManagedCertificateGeneration() error = %v", err)
	}
}
