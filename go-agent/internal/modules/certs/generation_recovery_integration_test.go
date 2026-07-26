//go:build integration

package certs

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/pkg/acmeflow"
)

func TestIntegrationACMEGenerationRecoveryIntegrationCrashAndPublishFailure(t *testing.T) {
	t.Parallel()
	fixture := requireACMEIntegrationFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	initialNow := time.Now().UTC().Truncate(time.Second)
	clock := newACMEIntegrationClock(initialNow)
	dataDir := t.TempDir()
	manager := newACMEIntegrationManager(t, dataDir, fixture, clock, nil)
	policy := localHTTP01Policy(7220, fixture.validationIP)
	policy.Scope = "ip"
	if err := manager.Apply(ctx, nil, []model.ManagedCertificatePolicy{policy}); err != nil {
		t.Fatalf("issue active recovery certificate: %v", err)
	}
	activeGeneration := loadCurrentGeneration(t, manager, policy.ID, clock.Now())
	activeCertificate := requireServerCertificate(t, manager, policy)
	activeMaterialHash := hashManagedCertificateMaterial(activeGeneration.Material.CertificatePEM, activeGeneration.Material.PrivateKeyPEM)
	activeLegacyCertificate, err := os.ReadFile(filepath.Join(manager.materialDir(policy.ID), "cert.pem"))
	if err != nil {
		t.Fatalf("read active legacy certificate: %v", err)
	}

	renewAt := activeCertificate.Leaf.NotAfter.Add(-manager.renewBeforeForScope(activeCertificate.Leaf, policy.Scope)).Add(time.Second)
	clock.Set(renewAt)
	crashState, err := manager.prepareActiveState(ctx, nil, []model.ManagedCertificatePolicy{policy})
	if err != nil {
		t.Fatalf("stage crash candidate: %v", err)
	}
	crashPending := crashState.byID[policy.ID].pending
	if crashPending == nil || crashPending.generationID == activeGeneration.Manifest.ID {
		t.Fatalf("crash candidate = %#v, want a new staged generation", crashPending)
	}

	store, err := acmeflow.OpenStateStore(manager.acmeStateRoot(policy.ID), acmeflow.WithStateClock(clock.Now))
	if err != nil {
		t.Fatalf("open state store for crash injection: %v", err)
	}
	if err := store.PromoteGeneration(ctx, crashPending.generationID, nil); err != nil {
		_ = store.Close()
		t.Fatalf("write crash candidate current reference: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close state store before crash injection: %v", err)
	}
	crashCertificatePath := filepath.Join(manager.acmeStateRoot(policy.ID), "generations", crashPending.generationID, "certificate.pem")
	if err := os.WriteFile(crashCertificatePath, []byte("partial"), 0o600); err != nil {
		t.Fatalf("truncate crash candidate: %v", err)
	}
	_ = manager.Close()

	restartClock := newACMEIntegrationClock(initialNow)
	restarted := newACMEIntegrationManager(t, dataDir, fixture, restartClock, nil)
	if err := restarted.Apply(ctx, nil, []model.ManagedCertificatePolicy{policy}); err != nil {
		t.Fatalf("restart after partial generation: %v", err)
	}
	currentAfterCrash := loadCurrentGeneration(t, restarted, policy.ID, restartClock.Now())
	if currentAfterCrash.Manifest.ID != activeGeneration.Manifest.ID {
		t.Fatalf("restart loaded partial generation %q instead of active %q", currentAfterCrash.Manifest.ID, activeGeneration.Manifest.ID)
	}
	certificateAfterCrash := requireServerCertificate(t, restarted, policy)
	if !bytes.Equal(certificateAfterCrash.Leaf.Raw, activeCertificate.Leaf.Raw) {
		t.Fatal("restart after partial generation did not retain the active certificate")
	}
	if report := requireManagedCertificateReport(t, restarted, policy.ID); report.MaterialHash != activeMaterialHash {
		t.Fatalf("report after partial generation = %q, want active hash %q", report.MaterialHash, activeMaterialHash)
	}
	assertACMEIntegrationGenerationUnreadable(t, restarted, policy.ID, crashPending.generationID, restartClock.Now())

	restartClock.Set(renewAt)
	failureState, err := restarted.prepareActiveState(ctx, nil, []model.ManagedCertificatePolicy{policy})
	if err != nil {
		t.Fatalf("stage publish-failure candidate: %v", err)
	}
	failurePending := failureState.byID[policy.ID].pending
	if failurePending == nil || failurePending.generationID == activeGeneration.Manifest.ID || failurePending.generationID == crashPending.generationID {
		t.Fatalf("publish-failure candidate = %#v, want a distinct generation", failurePending)
	}
	metadataPath := filepath.Join(restarted.materialDir(policy.ID), "local_metadata.json")
	if err := os.Remove(metadataPath); err != nil {
		t.Fatalf("remove projection target: %v", err)
	}
	if err := os.Mkdir(metadataPath, 0o700); err != nil {
		t.Fatalf("block projection target: %v", err)
	}
	transaction := &certificateTransaction{manager: restarted, previous: restarted.activeState(), next: failureState}
	if err := transaction.Commit(); err == nil {
		t.Fatal("publish succeeded despite a non-regular projection target")
	}

	currentAfterFailure := loadCurrentGeneration(t, restarted, policy.ID, restartClock.Now())
	if currentAfterFailure.Manifest.ID != activeGeneration.Manifest.ID {
		t.Fatalf("publish failure changed current generation: %q -> %q", activeGeneration.Manifest.ID, currentAfterFailure.Manifest.ID)
	}
	legacyAfterFailure, err := os.ReadFile(filepath.Join(restarted.materialDir(policy.ID), "cert.pem"))
	if err != nil || !bytes.Equal(legacyAfterFailure, activeLegacyCertificate) {
		t.Fatalf("publish failure changed legacy certificate: %v", err)
	}
	if report := requireManagedCertificateReport(t, restarted, policy.ID); report.MaterialHash != activeMaterialHash {
		t.Fatalf("report after publish failure = %q, want active hash %q", report.MaterialHash, activeMaterialHash)
	}
	_ = restarted.Close()
	if err := os.Remove(metadataPath); err != nil {
		t.Fatalf("remove projection obstruction: %v", err)
	}

	finalClock := newACMEIntegrationClock(initialNow)
	finalManager := newOfflineACMEIntegrationManager(t, dataDir, fixture, finalClock)
	if err := finalManager.Apply(ctx, nil, []model.ManagedCertificatePolicy{policy}); err != nil {
		t.Fatalf("restart after publish failure: %v", err)
	}
	finalCurrent := loadCurrentGeneration(t, finalManager, policy.ID, finalClock.Now())
	if finalCurrent.Manifest.ID != activeGeneration.Manifest.ID {
		t.Fatalf("final restart current generation = %q, want %q", finalCurrent.Manifest.ID, activeGeneration.Manifest.ID)
	}
	finalCertificate := requireServerCertificate(t, finalManager, policy)
	if !bytes.Equal(finalCertificate.Leaf.Raw, activeCertificate.Leaf.Raw) {
		t.Fatal("final restart did not retain the last active certificate")
	}
	if bytes.Equal(finalCertificate.Leaf.Raw, failureState.byID[policy.ID].certificate.Leaf.Raw) {
		t.Fatal("final restart loaded the unpublished generation")
	}
	if report := requireManagedCertificateReport(t, finalManager, policy.ID); report.MaterialHash != activeMaterialHash {
		t.Fatalf("final report material hash = %q, want %q", report.MaterialHash, activeMaterialHash)
	}
	assertACMEIntegrationSensitiveModes(t, dataDir)
}

func assertACMEIntegrationGenerationUnreadable(t *testing.T, manager *Manager, certificateID int, generationID string, now time.Time) {
	t.Helper()
	store, err := acmeflow.OpenStateStore(manager.acmeStateRoot(certificateID), acmeflow.WithStateClock(func() time.Time { return now }))
	if err != nil {
		t.Fatalf("open state store: %v", err)
	}
	defer store.Close()
	if _, err := store.LoadGeneration(context.Background(), generationID); err == nil {
		t.Fatalf("partial generation %q remained loadable", generationID)
	}
}
