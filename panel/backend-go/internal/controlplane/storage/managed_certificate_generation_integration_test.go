//go:build integration

package storage

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIntegrationManagedCertificateGenerationIntegrationLegacyPEMImportSurvivesRestart(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("SQLite restart and filesystem migration run in the full integration tier")
	}

	const domain = "legacy-restart.example.test"
	dataRoot := t.TempDir()
	legacyStore, err := NewStore(StoreConfig{
		Driver:              "sqlite",
		DataRoot:            dataRoot,
		LocalAgentID:        "local",
		SkipBootstrapSchema: true,
		TrafficStatsEnabled: true,
	})
	if err != nil {
		t.Fatalf("open legacy SQLite fixture: %v", err)
	}
	schema, err := os.ReadFile(filepath.Join("testdata", "legacy-managed-certificates-v1.sql"))
	if err != nil {
		t.Fatalf("read legacy SQLite schema fixture: %v", err)
	}
	if err := legacyStore.db.Exec(string(schema)).Error; err != nil {
		t.Fatalf("apply legacy SQLite schema fixture: %v", err)
	}
	if err := legacyStore.db.Exec(`INSERT INTO managed_certificates (id, domain) VALUES (?, ?)`, 7, domain).Error; err != nil {
		t.Fatalf("seed legacy SQLite row: %v", err)
	}
	var legacyColumns []struct {
		Name string `gorm:"column:name"`
	}
	if err := legacyStore.db.Raw(`PRAGMA table_info(managed_certificates)`).Scan(&legacyColumns).Error; err != nil {
		t.Fatalf("inspect legacy SQLite schema: %v", err)
	}
	if len(legacyColumns) != 2 || legacyColumns[0].Name != "id" || legacyColumns[1].Name != "domain" {
		t.Fatalf("legacy SQLite fixture columns = %#v, want fixed v1 id/domain schema", legacyColumns)
	}
	if err := legacyStore.Close(); err != nil {
		t.Fatalf("close legacy SQLite fixture: %v", err)
	}

	legacy := generateManagedCertificateLegacyIntegrationBundle(t, domain)
	if _, err := tls.X509KeyPair([]byte(legacy.CertPEM), []byte(legacy.KeyPEM)); err != nil {
		t.Fatalf("legacy PEM fixture is not a valid certificate/private-key pair: %v", err)
	}
	writeLegacyManagedCertificateGenerationMaterial(t, dataRoot, domain, legacy.CertPEM, legacy.KeyPEM)

	var source *SQLiteStore
	openSource := func() {
		t.Helper()
		var openErr error
		source, openErr = NewSQLiteStore(dataRoot, "local")
		if openErr != nil {
			t.Fatalf("bootstrap legacy SQLite fixture: %v", openErr)
		}
	}
	closeSource := func() {
		t.Helper()
		if source == nil {
			return
		}
		if closeErr := source.Close(); closeErr != nil {
			t.Fatalf("close migrated SQLite store: %v", closeErr)
		}
		source = nil
	}
	t.Cleanup(closeSource)

	openSource()
	for _, column := range []string{"ActiveGenerationID", "PendingGenerationID"} {
		if !source.db.Migrator().HasColumn(&ManagedCertificateRow{}, column) {
			t.Fatalf("migrated SQLite schema is missing %s", column)
		}
	}
	loaded, found, err := source.LoadManagedCertificateMaterial(t.Context(), domain)
	if err != nil || !found || loaded != legacy {
		t.Fatalf("LoadManagedCertificateMaterial() did not import the pinned legacy PEM fixture")
	}
	activeBefore, found, err := source.LoadActiveManagedCertificateGeneration(t.Context(), domain)
	if err != nil || !found || activeBefore.Material != legacy {
		t.Fatalf("LoadActiveManagedCertificateGeneration() did not expose the imported legacy material")
	}
	closeSource()

	openSource()
	activeAfter, found, err := source.LoadActiveManagedCertificateGeneration(t.Context(), domain)
	if err != nil || !found || activeAfter.ID != activeBefore.ID || activeAfter.Material != legacy {
		t.Fatalf("legacy generation changed after repeated bootstrap and restart")
	}
	assertManagedCertificateGenerationIntegrationCount(t, source, domain, 1)

	targetRoot := t.TempDir()
	target, err := NewSQLiteStore(targetRoot, "local")
	if err != nil {
		t.Fatalf("open cross-store migration target: %v", err)
	}
	for attempt := 1; attempt <= 2; attempt++ {
		if err := CopyDefaultMigrationRows(t.Context(), source, target); err != nil {
			t.Fatalf("CopyDefaultMigrationRows(attempt %d) error = %v", attempt, err)
		}
	}
	if err := target.Close(); err != nil {
		t.Fatalf("close cross-store migration target: %v", err)
	}
	target, err = NewSQLiteStore(targetRoot, "local")
	if err != nil {
		t.Fatalf("restart cross-store migration target: %v", err)
	}
	t.Cleanup(func() { _ = target.Close() })
	targetActive, found, err := target.LoadActiveManagedCertificateGeneration(t.Context(), domain)
	if err != nil || !found || targetActive.Material != legacy {
		t.Fatalf("cross-store migration did not preserve the pinned legacy material after restart")
	}
	assertManagedCertificateGenerationIntegrationCount(t, target, domain, 1)
}

func TestIntegrationManagedCertificateGenerationIntegrationCrashMatrixKeepsSafeActive(t *testing.T) {
	if testing.Short() {
		t.Skip("SQLite crash phase recovery runs in the full integration tier")
	}
	t.Parallel()

	testCases := []struct {
		name               string
		phase              string
		expectPromoted     bool
		expectPending      bool
		expectArtifactGone bool
	}{
		{name: "temporary filesystem stage", phase: "temporary-stage", expectPending: true, expectArtifactGone: true},
		{name: "finalized filesystem stage before database pointer", phase: "finalized-before-db", expectArtifactGone: true},
		{name: "pending database pointer", phase: "pending-pointer", expectPending: true},
		{name: "lost active database pointer", phase: "lost-active-pointer", expectPending: true},
		{name: "corrupt pending generation after database pointer", phase: "corrupt-pending"},
		{name: "canonical projection swap failure", phase: "canonical-projection", expectPending: true},
		{name: "compatibility projection swap failure", phase: "compatibility-projection", expectPending: true},
		{name: "committed active pointer before projection repair", phase: "committed-pointer", expectPromoted: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			const domain = "legacy-restart.example.test"
			dataRoot := t.TempDir()
			store, err := newStorageTestSQLiteStore(t, dataRoot, "local", true)
			if err != nil {
				t.Fatalf("NewSQLiteStore() error = %v", err)
			}
			closed := false
			t.Cleanup(func() {
				if !closed {
					_ = store.Close()
				}
			})

			seedManagedCertificateGenerationRow(t, store, domain)
			previousMaterial := generateManagedCertificateLegacyIntegrationBundle(t, domain)
			previous := stageManagedCertificateGenerationForTest(t, store, domain, previousMaterial.CertPEM, previousMaterial.KeyPEM)
			promoteManagedCertificateGenerationForTest(t, store, domain, previous)
			pending := stageManagedCertificateGenerationForTest(t, store, domain, previousMaterial.CertPEM+"\n", previousMaterial.KeyPEM)
			artifactPath := ""

			switch testCase.phase {
			case "temporary-stage":
				artifactPath = filepath.Join(store.managedCertificateGenerationsDirectory(domain), ".stage-interrupted")
				if err := os.MkdirAll(artifactPath, 0o700); err != nil {
					t.Fatalf("create interrupted stage directory: %v", err)
				}
				if err := os.WriteFile(filepath.Join(artifactPath, "cert"), []byte("partial"), 0o600); err != nil {
					t.Fatalf("write interrupted stage fixture: %v", err)
				}
			case "finalized-before-db":
				artifactPath = store.managedCertificateGenerationDirectory(domain, pending.ID)
				if err := store.db.WithContext(t.Context()).Model(&ManagedCertificateRow{}).
					Where("domain = ?", domain).Update("pending_generation_id", "").Error; err != nil {
					t.Fatalf("clear pending pointer crash fixture: %v", err)
				}
				if err := store.db.WithContext(t.Context()).Delete(&ManagedCertificateGenerationRow{}, "id = ?", pending.ID).Error; err != nil {
					t.Fatalf("remove pending row crash fixture: %v", err)
				}
			case "pending-pointer":
			case "lost-active-pointer":
				if err := store.db.WithContext(t.Context()).Model(&ManagedCertificateRow{}).
					Where("domain = ?", domain).Update("active_generation_id", "").Error; err != nil {
					t.Fatalf("clear active pointer crash fixture: %v", err)
				}
			case "corrupt-pending":
				if err := os.Remove(filepath.Join(store.managedCertificateGenerationDirectory(domain, pending.ID), "key")); err != nil {
					t.Fatalf("remove pending key crash fixture: %v", err)
				}
			case "canonical-projection", "compatibility-projection":
				projectionRoot := store.managedCertificateDirectory(domain)
				if testCase.phase == "compatibility-projection" {
					projectionRoot = store.legacyManagedCertificateDirectory(domain)
				}
				obstruction := filepath.Join(projectionRoot, "key")
				if err := os.Remove(obstruction); err != nil {
					t.Fatalf("remove projection key fixture: %v", err)
				}
				if err := os.Mkdir(obstruction, 0o700); err != nil {
					t.Fatalf("create projection obstruction: %v", err)
				}
				if err := store.PromoteManagedCertificateGeneration(t.Context(), domain, pending.ID, pending.MaterialHash); err == nil {
					t.Fatal("projection crash fixture unexpectedly promoted")
				}
				if err := os.RemoveAll(obstruction); err != nil {
					t.Fatalf("remove projection obstruction: %v", err)
				}
			case "committed-pointer":
				if err := store.PromoteManagedCertificateGeneration(t.Context(), domain, pending.ID, pending.MaterialHash); err != nil {
					t.Fatalf("commit active pointer crash fixture: %v", err)
				}
				if err := store.writeManagedCertificateLegacyProjection(domain, previous.Material); err != nil {
					t.Fatalf("restore stale projection crash fixture: %v", err)
				}
			default:
				t.Fatalf("unknown crash phase %q", testCase.phase)
			}

			if err := store.Close(); err != nil {
				t.Fatalf("close crash fixture store: %v", err)
			}
			closed = true
			store, err = openExistingStorageTestSQLiteStore(dataRoot, "local", true)
			if err != nil {
				t.Fatalf("restart crash fixture store: %v", err)
			}
			closed = false
			if err := store.ReconcileManagedCertificateGenerations(t.Context(), domain); err != nil {
				t.Fatalf("ReconcileManagedCertificateGenerations() error = %v", err)
			}

			expectedActive := previous
			if testCase.expectPromoted {
				expectedActive = pending
			}
			active, found, err := store.LoadActiveManagedCertificateGeneration(t.Context(), domain)
			if err != nil || !found || active.ID != expectedActive.ID || active.Material != expectedActive.Material {
				t.Fatalf("active generation after %s restart is not the last committed material", testCase.phase)
			}
			projected, found, err := store.LoadManagedCertificateMaterial(t.Context(), domain)
			if err != nil || !found || projected != expectedActive.Material {
				t.Fatalf("legacy projection after %s restart does not match active", testCase.phase)
			}
			gotPending, found, err := store.LoadPendingManagedCertificateGeneration(t.Context(), domain)
			if err != nil {
				t.Fatalf("LoadPendingManagedCertificateGeneration(%s) error = %v", testCase.phase, err)
			}
			if testCase.expectPending && (!found || gotPending.ID != pending.ID || gotPending.Material != pending.Material) {
				t.Fatalf("pending generation after %s restart was not preserved", testCase.phase)
			}
			if !testCase.expectPending && found {
				t.Fatalf("pending generation after %s restart became visible unexpectedly", testCase.phase)
			}
			if testCase.expectArtifactGone {
				if _, err := os.Stat(artifactPath); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("unreferenced stage artifact survived %s restart: %v", testCase.phase, err)
				}
			}
			if testCase.expectPromoted {
				rollback, err := store.loadManagedCertificateGeneration(t.Context(), domain, previous.ID)
				if err != nil || rollback.State != ManagedCertificateGenerationStateSuperseded || rollback.Material != previous.Material {
					t.Fatalf("rollback generation was not retained after committed pointer recovery")
				}
			}
		})
	}
}

func generateManagedCertificateLegacyIntegrationBundle(t *testing.T, domain string) ManagedCertificateBundle {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate legacy fixture private key: %v", err)
	}
	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: domain},
		DNSNames:              []string{domain},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatalf("generate legacy fixture certificate: %v", err)
	}
	privateKeyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatalf("marshal legacy fixture private key: %v", err)
	}
	return ManagedCertificateBundle{
		Domain:  domain,
		CertPEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER})),
		KeyPEM:  string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKeyDER})),
	}
}

func assertManagedCertificateGenerationIntegrationCount(t *testing.T, store *SQLiteStore, domain string, want int64) {
	t.Helper()
	var generationCount int64
	if err := store.db.WithContext(t.Context()).Model(&ManagedCertificateGenerationRow{}).
		Where("domain = ?", domain).Count(&generationCount).Error; err != nil {
		t.Fatalf("count migrated generations: %v", err)
	}
	if generationCount != want {
		t.Fatalf("migrated generation count = %d, want %d", generationCount, want)
	}
}
