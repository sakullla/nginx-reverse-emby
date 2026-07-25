//go:build integration

package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestManagedCertificateGenerationIntegrationLegacyPEMImportSurvivesRestart(t *testing.T) {
	if testing.Short() {
		t.Skip("SQLite restart and filesystem migration run in the full integration tier")
	}

	dataRoot := t.TempDir()
	var store *SQLiteStore
	t.Cleanup(func() {
		if store != nil {
			_ = store.Close()
		}
	})
	open := func() {
		t.Helper()
		var err error
		store, err = NewSQLiteStore(dataRoot, "local")
		if err != nil {
			t.Fatalf("NewSQLiteStore() error = %v", err)
		}
	}
	closeStore := func() {
		t.Helper()
		if err := store.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
		store = nil
	}

	const domain = "legacy-restart.example.test"
	legacy := ManagedCertificateBundle{Domain: domain, CertPEM: "legacy-integration-cert", KeyPEM: "legacy-integration-key"}
	open()
	seedManagedCertificateGenerationRow(t, store, domain)
	closeStore()
	writeLegacyManagedCertificateGenerationMaterial(t, dataRoot, domain, legacy.CertPEM, legacy.KeyPEM)

	open()
	loaded, found, err := store.LoadManagedCertificateMaterial(t.Context(), domain)
	if err != nil || !found || loaded != legacy {
		t.Fatalf("LoadManagedCertificateMaterial() = (%#v, %v, %v)", loaded, found, err)
	}
	activeBefore, found, err := store.LoadActiveManagedCertificateGeneration(t.Context(), domain)
	if err != nil || !found || activeBefore.Material != legacy {
		t.Fatalf("LoadActiveManagedCertificateGeneration() = (%#v, %v, %v)", activeBefore, found, err)
	}
	closeStore()

	open()
	activeAfter, found, err := store.LoadActiveManagedCertificateGeneration(t.Context(), domain)
	if err != nil || !found || activeAfter.ID != activeBefore.ID || activeAfter.Material != legacy {
		t.Fatalf("active generation after restart = (%#v, %v, %v)", activeAfter, found, err)
	}
	var generationCount int64
	if err := store.db.WithContext(t.Context()).Model(&ManagedCertificateGenerationRow{}).
		Where("domain = ?", domain).Count(&generationCount).Error; err != nil {
		t.Fatalf("count imported generations: %v", err)
	}
	if generationCount != 1 {
		t.Fatalf("imported generation count = %d, want 1", generationCount)
	}
}

func TestManagedCertificateGenerationIntegrationProjectionFailureRestartKeepsOldActive(t *testing.T) {
	if testing.Short() {
		t.Skip("SQLite restart and projection failure run in the full integration tier")
	}

	dataRoot := t.TempDir()
	var store *SQLiteStore
	t.Cleanup(func() {
		if store != nil {
			_ = store.Close()
		}
	})
	open := func() {
		t.Helper()
		var err error
		store, err = NewSQLiteStore(dataRoot, "local")
		if err != nil {
			t.Fatalf("NewSQLiteStore() error = %v", err)
		}
	}
	closeStore := func() {
		t.Helper()
		if err := store.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
		store = nil
	}

	const domain = "projection-restart.example.test"
	open()
	seedManagedCertificateGenerationRow(t, store, domain)
	previous := stageManagedCertificateGenerationForTest(t, store, domain, "old-integration-cert", "old-integration-key")
	promoteManagedCertificateGenerationForTest(t, store, domain, previous)
	pending := stageManagedCertificateGenerationForTest(t, store, domain, "pending-integration-cert", "pending-integration-key")
	closeStore()

	open()
	activeAfterStage, found, err := store.LoadActiveManagedCertificateGeneration(t.Context(), domain)
	if err != nil || !found || activeAfterStage.ID != previous.ID || activeAfterStage.Material != previous.Material {
		t.Fatalf("active after staged restart = (%#v, %v, %v)", activeAfterStage, found, err)
	}
	pendingAfterStage, found, err := store.LoadPendingManagedCertificateGeneration(t.Context(), domain)
	if err != nil || !found || pendingAfterStage.ID != pending.ID || pendingAfterStage.Material != pending.Material {
		t.Fatalf("pending after staged restart = (%#v, %v, %v)", pendingAfterStage, found, err)
	}

	projectionKey := filepath.Join(store.managedCertificateDirectory(domain), "key")
	if err := os.Remove(projectionKey); err != nil {
		t.Fatalf("remove projection key fixture: %v", err)
	}
	if err := os.Mkdir(projectionKey, 0o700); err != nil {
		t.Fatalf("create projection obstruction: %v", err)
	}
	if err := store.PromoteManagedCertificateGeneration(t.Context(), domain, pending.ID, pending.MaterialHash); err == nil {
		t.Fatal("PromoteManagedCertificateGeneration() projection error = nil")
	}
	if err := os.RemoveAll(projectionKey); err != nil {
		t.Fatalf("remove projection obstruction: %v", err)
	}
	closeStore()

	open()
	if err := store.ReconcileManagedCertificateGenerations(t.Context(), domain); err != nil {
		t.Fatalf("ReconcileManagedCertificateGenerations() after restart error = %v", err)
	}
	activeAfterFailure, found, err := store.LoadActiveManagedCertificateGeneration(t.Context(), domain)
	if err != nil || !found || activeAfterFailure.ID != previous.ID || activeAfterFailure.Material != previous.Material {
		t.Fatalf("active after failed promotion restart = (%#v, %v, %v)", activeAfterFailure, found, err)
	}
	pendingAfterFailure, found, err := store.LoadPendingManagedCertificateGeneration(t.Context(), domain)
	if err != nil || !found || pendingAfterFailure.ID != pending.ID || pendingAfterFailure.Material != pending.Material {
		t.Fatalf("pending after failed promotion restart = (%#v, %v, %v)", pendingAfterFailure, found, err)
	}
	projected, found, err := store.LoadManagedCertificateMaterial(t.Context(), domain)
	if err != nil || !found || projected != previous.Material {
		t.Fatalf("legacy projection after restart = (%#v, %v, %v)", projected, found, err)
	}
}
