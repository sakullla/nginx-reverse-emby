//go:build integration

package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
)

func TestIntegrationUpdateManagedCertificatesSerializesReportMergesAndPreservesPointers(t *testing.T) {
	t.Parallel()
	store := newManagedCertificateGenerationTestStore(t)
	ctx := t.Context()
	const domain = "heartbeat-merge.example.com"
	if err := store.SaveManagedCertificates(ctx, []ManagedCertificateRow{{
		ID: 91, Domain: domain, Enabled: true, Scope: "domain", IssuerMode: "master_cf_dns",
		CertificateType: "acme", AgentReports: `{}`,
	}}); err != nil {
		t.Fatalf("SaveManagedCertificates(seed) error = %v", err)
	}
	pending := stageManagedCertificateGenerationForTest(t, store, domain, "pending-cert", "pending-key")

	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstResult := make(chan error, 1)
	go func() {
		firstResult <- store.UpdateManagedCertificates(ctx, func(rows []ManagedCertificateRow) ([]ManagedCertificateRow, bool, error) {
			close(firstEntered)
			<-releaseFirst
			rows[0].AgentReports = `{"edge-a":{}}`
			return rows, true, nil
		})
	}()
	<-firstEntered

	secondStarted := make(chan struct{})
	secondResult := make(chan error, 1)
	go func() {
		close(secondStarted)
		secondResult <- store.UpdateManagedCertificates(ctx, func(rows []ManagedCertificateRow) ([]ManagedCertificateRow, bool, error) {
			if !strings.Contains(rows[0].AgentReports, "edge-a") {
				return nil, false, errors.New("second update did not observe the first report")
			}
			rows[0].AgentReports = `{"edge-a":{},"edge-b":{}}`
			return rows, true, nil
		})
	}()
	<-secondStarted
	close(releaseFirst)
	if err := <-firstResult; err != nil {
		t.Fatalf("first UpdateManagedCertificates() error = %v", err)
	}
	if err := <-secondResult; err != nil {
		t.Fatalf("second UpdateManagedCertificates() error = %v", err)
	}

	rows, err := store.ListManagedCertificates(ctx)
	if err != nil || len(rows) != 1 {
		t.Fatalf("ListManagedCertificates() = (%+v, %v)", rows, err)
	}
	if !strings.Contains(rows[0].AgentReports, "edge-a") || !strings.Contains(rows[0].AgentReports, "edge-b") {
		t.Fatalf("merged agent reports = %s", rows[0].AgentReports)
	}
	if rows[0].PendingGenerationID != pending.ID {
		t.Fatalf("pending generation pointer = %q, want %q", rows[0].PendingGenerationID, pending.ID)
	}
}

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

func TestIntegrationSaveManagedCertificatesRetiresPendingGenerationWhenOwnershipChanges(t *testing.T) {
	t.Parallel()
	store := newManagedCertificateGenerationTestStore(t)
	ctx := t.Context()
	const domain = "ownership-transition.example.com"
	master := ManagedCertificateRow{
		ID: 1, Domain: domain, Enabled: true, Scope: "domain",
		IssuerMode: "master_cf_dns", CertificateType: "acme",
	}
	if err := store.SaveManagedCertificates(ctx, []ManagedCertificateRow{master}); err != nil {
		t.Fatalf("SaveManagedCertificates(master) error = %v", err)
	}
	active := stageManagedCertificateGenerationForTest(t, store, domain, "active-cert", "active-key")
	promoteManagedCertificateGenerationForTest(t, store, domain, active)
	obsolete := stageManagedCertificateGenerationForTest(t, store, domain, "obsolete-cert", "obsolete-key")

	local := master
	local.IssuerMode = "local_http01"
	local.CertificateType = "uploaded"
	local.Revision = 2
	if err := store.SaveManagedCertificates(ctx, []ManagedCertificateRow{local}); err != nil {
		t.Fatalf("SaveManagedCertificates(local transition) error = %v", err)
	}
	rows, err := store.ListManagedCertificates(ctx)
	if err != nil || len(rows) != 1 {
		t.Fatalf("ListManagedCertificates() = %#v, %v", rows, err)
	}
	if rows[0].ActiveGenerationID != active.ID || rows[0].PendingGenerationID != "" {
		t.Fatalf("generation pointers after ownership transition = active %q pending %q", rows[0].ActiveGenerationID, rows[0].PendingGenerationID)
	}
	var obsoleteRow ManagedCertificateGenerationRow
	err = store.db.WithContext(ctx).Where("id = ?", obsolete.ID).First(&obsoleteRow).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("load obsolete generation row: %v", err)
	}
	if err == nil && obsoleteRow.State == ManagedCertificateGenerationStatePending {
		t.Fatalf("obsolete generation remained pending: %#v", obsoleteRow)
	}

	replacement := ManagedCertificateBundle{Domain: domain, CertPEM: "replacement-cert", KeyPEM: "replacement-key"}
	if err := store.SaveManagedCertificateMaterial(ctx, domain, replacement); err != nil {
		t.Fatalf("SaveManagedCertificateMaterial(replacement) error = %v", err)
	}
	loaded, ok, err := store.LoadActiveManagedCertificateGeneration(ctx, domain)
	if err != nil || !ok || loaded.Material != replacement || loaded.ID == active.ID {
		t.Fatalf("active replacement = (%#v, %v, %v)", loaded, ok, err)
	}
}

func TestIntegrationSaveManagedCertificateMaterialCollectsSupersededGenerations(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name          string
		transactional bool
	}{
		{name: "direct"},
		{name: "revision transaction", transactional: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			store := newManagedCertificateGenerationTestStore(t)
			ctx := t.Context()
			const domain = "direct-retention.example.com"
			seedManagedCertificateGenerationRow(t, store, domain)

			generations := make([]ManagedCertificateGeneration, 0, 4)
			for index := 1; index <= 4; index++ {
				bundle := ManagedCertificateBundle{
					Domain:  domain,
					CertPEM: fmt.Sprintf("certificate-%d", index),
					KeyPEM:  fmt.Sprintf("private-key-%d", index),
				}
				var err error
				if testCase.transactional {
					err = store.WithRevisionMutation(ctx, func(tx *GormStore) (RevisionMutationDecision, error) {
						return RevisionMutationDecision{}, tx.SaveManagedCertificateMaterial(ctx, domain, bundle)
					})
				} else {
					err = store.SaveManagedCertificateMaterial(ctx, domain, bundle)
				}
				if err != nil {
					t.Fatalf("SaveManagedCertificateMaterial(%d) error = %v", index, err)
				}
				active, ok, err := store.LoadActiveManagedCertificateGeneration(ctx, domain)
				if err != nil || !ok || active.Material != bundle {
					t.Fatalf("active generation after save %d = (%#v, %v, %v)", index, active, ok, err)
				}
				generations = append(generations, active)
			}

			var rows []ManagedCertificateGenerationRow
			if err := store.db.WithContext(ctx).Where("domain = ?", domain).Find(&rows).Error; err != nil {
				t.Fatalf("list retained generations: %v", err)
			}
			if len(rows) != 2 {
				t.Fatalf("retained generation rows = %d, want active plus one rollback", len(rows))
			}
			retained := make(map[string]string, len(rows))
			for _, row := range rows {
				retained[row.ID] = row.State
			}
			if got := retained[generations[2].ID]; got != ManagedCertificateGenerationStateSuperseded {
				t.Fatalf("rollback generation state = %q, want %q", got, ManagedCertificateGenerationStateSuperseded)
			}
			if got := retained[generations[3].ID]; got != ManagedCertificateGenerationStateActive {
				t.Fatalf("active generation state = %q, want %q", got, ManagedCertificateGenerationStateActive)
			}
			for index, generation := range generations {
				_, err := os.Stat(store.managedCertificateGenerationDirectory(domain, generation.ID))
				if index < 2 && !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("collected generation %s directory error = %v, want not found", generation.ID, err)
				}
				if index >= 2 && err != nil {
					t.Fatalf("retained generation %s directory error = %v", generation.ID, err)
				}
			}
		})
	}
}

func TestIntegrationRevisionResourceRollbackDoesNotCollectManagedCertificateGenerations(t *testing.T) {
	t.Parallel()
	store := newManagedCertificateGenerationTestStore(t)
	ctx := t.Context()
	const domain = "rolled-back-retention.example.com"
	seedManagedCertificateGenerationRow(t, store, domain)

	oldest := stageManagedCertificateGenerationForTest(t, store, domain, "certificate-oldest", "key-oldest")
	promoteManagedCertificateGenerationForTest(t, store, domain, oldest)
	rollback := stageManagedCertificateGenerationForTest(t, store, domain, "certificate-rollback", "key-rollback")
	promoteManagedCertificateGenerationForTest(t, store, domain, rollback)
	active := stageManagedCertificateGenerationForTest(t, store, domain, "certificate-active", "key-active")
	promoteManagedCertificateGenerationForTest(t, store, domain, active)

	err := store.WithRevisionMutation(ctx, func(tx *GormStore) (RevisionMutationDecision, error) {
		if err := tx.SaveManagedCertificateMaterial(ctx, domain, ManagedCertificateBundle{
			Domain: domain, CertPEM: "rolled-back-certificate", KeyPEM: "rolled-back-key",
		}); err != nil {
			return RevisionMutationDecision{}, err
		}
		return RevisionMutationDecision{RollbackResources: true}, nil
	})
	if err != nil {
		t.Fatalf("WithRevisionMutation() error = %v", err)
	}

	loaded, ok, err := store.LoadActiveManagedCertificateGeneration(ctx, domain)
	if err != nil || !ok || loaded.ID != active.ID {
		t.Fatalf("active generation after resource rollback = (%#v, %v, %v)", loaded, ok, err)
	}
	var count int64
	if err := store.db.WithContext(ctx).Model(&ManagedCertificateGenerationRow{}).Where("domain = ?", domain).Count(&count).Error; err != nil {
		t.Fatalf("count generations after resource rollback: %v", err)
	}
	if count != 3 {
		t.Fatalf("generation rows after resource rollback = %d, want 3", count)
	}
	for _, generation := range []ManagedCertificateGeneration{oldest, rollback, active} {
		if _, err := os.Stat(store.managedCertificateGenerationDirectory(domain, generation.ID)); err != nil {
			t.Fatalf("generation %s was collected after resource rollback: %v", generation.ID, err)
		}
	}
}

func TestIntegrationManagedCertificateGenerationLoadRejectsStateDivergenceAndFallsBack(t *testing.T) {
	t.Parallel()
	store := newManagedCertificateGenerationTestStore(t)
	ctx := t.Context()
	const domain = "state-divergence.example.com"
	seedManagedCertificateGenerationRow(t, store, domain)
	rollback := stageManagedCertificateGenerationForTest(t, store, domain, "cert-rollback", "key-rollback")
	promoteManagedCertificateGenerationForTest(t, store, domain, rollback)
	current := stageManagedCertificateGenerationForTest(t, store, domain, "cert-current", "key-current")
	promoteManagedCertificateGenerationForTest(t, store, domain, current)
	if err := store.db.WithContext(ctx).Model(&ManagedCertificateGenerationRow{}).
		Where("id = ?", current.ID).Update("state", ManagedCertificateGenerationStatePending).Error; err != nil {
		t.Fatalf("corrupt current row state: %v", err)
	}

	active, ok, err := store.LoadActiveManagedCertificateGeneration(ctx, domain)
	if err != nil || !ok || active.ID != rollback.ID || active.State != ManagedCertificateGenerationStateActive {
		t.Fatalf("LoadActiveManagedCertificateGeneration() fallback = (%#v, %v, %v)", active, ok, err)
	}
	pending := stageManagedCertificateGenerationForTest(t, store, domain, "cert-pending", "key-pending")
	if err := store.db.WithContext(ctx).Model(&ManagedCertificateGenerationRow{}).
		Where("id = ?", pending.ID).Update("state", "unknown").Error; err != nil {
		t.Fatalf("corrupt pending row state: %v", err)
	}
	if _, ok, err := store.LoadPendingManagedCertificateGeneration(ctx, domain); err == nil || ok {
		t.Fatalf("LoadPendingManagedCertificateGeneration() divergence = (ok=%v, err=%v), want rejected", ok, err)
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

func TestIntegrationManagedCertificateGenerationCompatibilityProjectionFailurePreservesActive(t *testing.T) {
	t.Parallel()
	store := newManagedCertificateGenerationTestStore(t)
	ctx := t.Context()
	const domain = "compatibility-projection-failure.example.com"
	seedManagedCertificateGenerationRow(t, store, domain)
	previous := stageManagedCertificateGenerationForTest(t, store, domain, "previous-cert", "previous-key")
	promoteManagedCertificateGenerationForTest(t, store, domain, previous)
	pending := stageManagedCertificateGenerationForTest(t, store, domain, "pending-cert", "pending-key")

	legacyKeyPath := filepath.Join(store.legacyManagedCertificateDirectory(domain), "key")
	if err := os.Remove(legacyKeyPath); err != nil {
		t.Fatalf("remove compatibility key: %v", err)
	}
	if err := os.Mkdir(legacyKeyPath, 0o700); err != nil {
		t.Fatalf("create compatibility key obstruction: %v", err)
	}
	if err := store.PromoteManagedCertificateGeneration(ctx, domain, pending.ID, pending.MaterialHash); err == nil {
		t.Fatal("PromoteManagedCertificateGeneration() compatibility projection error = nil")
	}
	active, ok, err := store.LoadActiveManagedCertificateGeneration(ctx, domain)
	if err != nil || !ok || active.ID != previous.ID || active.Material != previous.Material {
		t.Fatalf("active after compatibility projection failure = (%#v, %v, %v)", active, ok, err)
	}
	canonicalCert, err := readManagedCertificateRegularFile(filepath.Join(store.managedCertificateDirectory(domain), "cert"))
	if err != nil || string(canonicalCert) != previous.Material.CertPEM {
		t.Fatalf("canonical projection after compatibility failure = %q, %v", canonicalCert, err)
	}
}

func TestIntegrationManagedCertificateSnapshotRepairsSplitPromotion(t *testing.T) {
	t.Parallel()
	store := newManagedCertificateGenerationTestStore(t)
	ctx := t.Context()
	const domain = "snapshot-split-promotion.example.com"
	row := ManagedCertificateRow{ID: 1, Domain: domain, Enabled: true}
	if err := store.SaveManagedCertificates(ctx, []ManagedCertificateRow{row}); err != nil {
		t.Fatalf("SaveManagedCertificates() error = %v", err)
	}
	previous := stageManagedCertificateGenerationForTest(t, store, domain, "previous-cert", "previous-key")
	promoteManagedCertificateGenerationForTest(t, store, domain, previous)
	active := stageManagedCertificateGenerationForTest(t, store, domain, "active-cert", "active-key")
	promoteManagedCertificateGenerationForTest(t, store, domain, active)
	if err := store.writeManagedCertificateLegacyProjection(domain, previous.Material); err != nil {
		t.Fatalf("write stale projection fixture: %v", err)
	}

	bundles, err := store.snapshotCertificateBundles(ctx, []ManagedCertificateRow{row})
	if err != nil {
		t.Fatalf("snapshotCertificateBundles() error = %v", err)
	}
	if len(bundles) != 1 || bundles[0].CertPEM != active.Material.CertPEM || bundles[0].KeyPEM != active.Material.KeyPEM {
		t.Fatalf("snapshot bundles = %#v, want active generation material", bundles)
	}
	projected, ok, err := store.readManagedCertificateMaterialSecure(domain)
	if err != nil || !ok || projected.CertPEM != active.Material.CertPEM || projected.KeyPEM != active.Material.KeyPEM {
		t.Fatalf("projection after snapshot reconciliation = (%#v, %v, %v)", projected, ok, err)
	}
}

func TestIntegrationManagedCertificateSnapshotRepairsProjectionAheadOfActivePointer(t *testing.T) {
	t.Parallel()
	store := newManagedCertificateGenerationTestStore(t)
	ctx := t.Context()
	const domain = "snapshot-projection-ahead.example.com"
	row := ManagedCertificateRow{ID: 1, Domain: domain, Enabled: true}
	if err := store.SaveManagedCertificates(ctx, []ManagedCertificateRow{row}); err != nil {
		t.Fatalf("SaveManagedCertificates() error = %v", err)
	}
	active := stageManagedCertificateGenerationForTest(t, store, domain, "active-cert", "active-key")
	promoteManagedCertificateGenerationForTest(t, store, domain, active)
	pending := stageManagedCertificateGenerationForTest(t, store, domain, "pending-cert", "pending-key")
	if err := store.writeManagedCertificateLegacyProjection(domain, pending.Material); err != nil {
		t.Fatalf("write projection-ahead fixture: %v", err)
	}

	bundles, err := store.snapshotCertificateBundles(ctx, []ManagedCertificateRow{row})
	if err != nil {
		t.Fatalf("snapshotCertificateBundles() error = %v", err)
	}
	if len(bundles) != 1 || bundles[0].CertPEM != active.Material.CertPEM || bundles[0].KeyPEM != active.Material.KeyPEM {
		t.Fatalf("snapshot bundles = %#v, want active generation material", bundles)
	}
	projected, ok, err := store.readManagedCertificateMaterialSecure(domain)
	if err != nil || !ok || projected.CertPEM != active.Material.CertPEM || projected.KeyPEM != active.Material.KeyPEM {
		t.Fatalf("projection after snapshot reconciliation = (%#v, %v, %v)", projected, ok, err)
	}
}

func TestIntegrationRevisionSnapshotDoesNotAcquireManagedCertificateDomainLock(t *testing.T) {
	t.Parallel()
	store := newManagedCertificateGenerationTestStore(t)
	ctx := t.Context()
	const domain = "revision-snapshot-lock-order.example.com"
	row := ManagedCertificateRow{ID: 1, Domain: domain, Enabled: true}
	if err := store.SaveManagedCertificates(ctx, []ManagedCertificateRow{row}); err != nil {
		t.Fatalf("SaveManagedCertificates() error = %v", err)
	}
	active := stageManagedCertificateGenerationForTest(t, store, domain, "active-cert", "active-key")
	promoteManagedCertificateGenerationForTest(t, store, domain, active)
	pending := stageManagedCertificateGenerationForTest(t, store, domain, "pending-cert", "pending-key")
	if err := store.writeManagedCertificateLegacyProjection(domain, pending.Material); err != nil {
		t.Fatalf("write projection-ahead fixture: %v", err)
	}

	releaseDomain := store.lockManagedCertificateDomain(domain)
	released := false
	defer func() {
		if !released {
			releaseDomain()
		}
	}()
	done := make(chan error, 1)
	go func() {
		done <- store.WithRevisionMutation(ctx, func(tx *GormStore) (RevisionMutationDecision, error) {
			bundles, err := tx.snapshotCertificateBundles(ctx, []ManagedCertificateRow{row})
			if err == nil && (len(bundles) != 1 || bundles[0].CertPEM != active.Material.CertPEM || bundles[0].KeyPEM != active.Material.KeyPEM) {
				err = errors.New("revision snapshot did not use active generation material")
			}
			return RevisionMutationDecision{}, err
		})
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("WithRevisionMutation(snapshot) error = %v", err)
		}
	case <-time.After(2 * time.Second):
		releaseDomain()
		released = true
		<-done
		t.Fatal("revision snapshot blocked on the managed certificate domain lock while holding sqliteWrite")
	}
	releaseDomain()
	released = true
}

func TestIntegrationRevisionGenerationAcquiresManagedCertificateDomainBeforeSQLite(t *testing.T) {
	t.Parallel()
	store := newManagedCertificateGenerationTestStore(t)
	ctx := t.Context()
	const domain = "revision-generation-lock-order.example.com"
	row := ManagedCertificateRow{ID: 1, Domain: domain, Enabled: true}
	if err := store.SaveManagedCertificates(ctx, []ManagedCertificateRow{row}); err != nil {
		t.Fatalf("SaveManagedCertificates() error = %v", err)
	}
	unsafeErr := store.WithRevisionMutation(ctx, func(tx *GormStore) (RevisionMutationDecision, error) {
		_, err := tx.StageManagedCertificateGeneration(ctx, domain, ManagedCertificateBundle{
			Domain: domain, CertPEM: "unsafe-cert", KeyPEM: "unsafe-key",
		})
		return RevisionMutationDecision{}, err
	})
	if !errors.Is(unsafeErr, ErrManagedCertificateDomainLockRequired) {
		t.Fatalf("unlocked revision Stage error = %v, want %v", unsafeErr, ErrManagedCertificateDomainLockRequired)
	}

	releaseDomain := store.lockManagedCertificateDomain(domain)
	released := false
	defer func() {
		if !released {
			releaseDomain()
		}
	}()
	var pending ManagedCertificateGeneration
	done := make(chan error, 1)
	go func() {
		done <- store.WithManagedCertificateDomainLock(ctx, domain, func(lockedCtx context.Context) error {
			return store.WithRevisionMutation(lockedCtx, func(tx *GormStore) (RevisionMutationDecision, error) {
				var err error
				pending, err = tx.StageManagedCertificateGeneration(lockedCtx, domain, ManagedCertificateBundle{
					Domain: domain, CertPEM: "pending-cert", KeyPEM: "pending-key",
				})
				if err != nil {
					return RevisionMutationDecision{}, err
				}
				loaded, found, err := tx.LoadPendingManagedCertificateGeneration(lockedCtx, domain)
				if err != nil {
					return RevisionMutationDecision{}, err
				}
				if !found || loaded.ID != pending.ID || loaded.MaterialHash != pending.MaterialHash {
					return RevisionMutationDecision{}, errors.New("revision mutation did not load the pending generation")
				}
				if _, found, err := tx.LoadActiveManagedCertificateGeneration(lockedCtx, domain); err != nil || found {
					return RevisionMutationDecision{}, errors.New("revision mutation saw an active generation before promotion")
				}
				if err := tx.PromoteManagedCertificateGeneration(lockedCtx, domain, pending.ID, pending.MaterialHash); err != nil {
					return RevisionMutationDecision{}, err
				}
				active, found, err := tx.LoadActiveManagedCertificateGeneration(lockedCtx, domain)
				if err != nil || !found || active.ID != pending.ID {
					return RevisionMutationDecision{}, errors.New("revision mutation did not load the promoted active generation")
				}
				material, found, err := tx.LoadManagedCertificateMaterial(lockedCtx, domain)
				if err != nil || !found || material.CertPEM != pending.Material.CertPEM || material.KeyPEM != pending.Material.KeyPEM {
					return RevisionMutationDecision{}, errors.New("revision mutation did not load the promoted material")
				}
				return RevisionMutationDecision{}, nil
			})
		})
	}()
	waitForManagedCertificateDomainLockRefs(t, store, domain, 2)
	writeDone := make(chan error, 1)
	go func() {
		writeDone <- store.writeTransaction(ctx, func(*gorm.DB) error { return nil })
	}()
	select {
	case err := <-writeDone:
		if err != nil {
			t.Fatalf("independent SQLite write while domain waiter blocked error = %v", err)
		}
	case <-time.After(2 * time.Second):
		releaseDomain()
		released = true
		<-writeDone
		t.Fatal("revision generation acquired SQLite before the managed certificate domain lock")
	}
	releaseDomain()
	released = true
	if err := <-done; err != nil {
		t.Fatalf("domain-scoped revision generation error = %v", err)
	}

	active, found, err := store.LoadActiveManagedCertificateGeneration(ctx, domain)
	if err != nil || !found || active.ID != pending.ID || active.MaterialHash != pending.MaterialHash {
		t.Fatalf("active generation after revision promotion = (%#v, %v, %v)", active, found, err)
	}
}

func TestIntegrationManagedCertificateGenerationCleanupRemovesDeletedDomainOnly(t *testing.T) {
	t.Parallel()
	store := newManagedCertificateGenerationTestStore(t)
	ctx := t.Context()
	const deletedDomain = "deleted-generation.example.com"
	const retainedDomain = "retained-generation.example.com"
	if err := store.SaveManagedCertificates(ctx, []ManagedCertificateRow{
		{ID: 1, Domain: deletedDomain, Enabled: true},
		{ID: 2, Domain: retainedDomain, Enabled: true},
	}); err != nil {
		t.Fatalf("SaveManagedCertificates() error = %v", err)
	}
	deleted := stageManagedCertificateGenerationForTest(t, store, deletedDomain, "deleted-cert", "deleted-key")
	promoteManagedCertificateGenerationForTest(t, store, deletedDomain, deleted)
	retained := stageManagedCertificateGenerationForTest(t, store, retainedDomain, "retained-cert", "retained-key")
	promoteManagedCertificateGenerationForTest(t, store, retainedDomain, retained)
	previous, err := store.ListManagedCertificates(ctx)
	if err != nil {
		t.Fatalf("ListManagedCertificates() error = %v", err)
	}
	if err := store.CleanupManagedCertificateMaterial(ctx, []ManagedCertificateRow{{ID: 1, Domain: deletedDomain}}, nil); err != nil {
		t.Fatalf("CleanupManagedCertificateMaterial(anchored) error = %v", err)
	}
	if active, ok, err := store.LoadActiveManagedCertificateGeneration(ctx, deletedDomain); err != nil || !ok || active.ID != deleted.ID {
		t.Fatalf("anchored active after conservative cleanup = (%#v, %v, %v)", active, ok, err)
	}
	var next []ManagedCertificateRow
	for _, row := range previous {
		if row.Domain == retainedDomain {
			next = append(next, row)
		}
	}
	if err := store.SaveManagedCertificates(ctx, next); err != nil {
		t.Fatalf("SaveManagedCertificates(retained) error = %v", err)
	}
	if err := store.CleanupManagedCertificateMaterial(ctx, previous, next); err != nil {
		t.Fatalf("CleanupManagedCertificateMaterial() error = %v", err)
	}
	var deletedRows int64
	if err := store.db.WithContext(ctx).Model(&ManagedCertificateGenerationRow{}).Where("domain = ?", deletedDomain).Count(&deletedRows).Error; err != nil {
		t.Fatalf("count deleted-domain generations: %v", err)
	}
	if deletedRows != 0 {
		t.Fatalf("deleted-domain generation rows = %d, want 0", deletedRows)
	}
	if _, err := os.Stat(store.managedCertificateDirectory(deletedDomain)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deleted-domain material directory still exists: %v", err)
	}
	active, ok, err := store.LoadActiveManagedCertificateGeneration(ctx, retainedDomain)
	if err != nil || !ok || active.ID != retained.ID {
		t.Fatalf("retained-domain active = (%#v, %v, %v)", active, ok, err)
	}
}

func TestIntegrationManagedCertificateGenerationAbortAndGCRetainRecoveryAnchors(t *testing.T) {
	t.Parallel()
	store := newManagedCertificateGenerationTestStore(t)
	ctx := t.Context()
	const domain = "retention.example.com"
	seedManagedCertificateGenerationRow(t, store, domain)
	oldest := stageManagedCertificateGenerationForTest(t, store, domain, "cert-oldest", "key-oldest")
	promoteManagedCertificateGenerationForTest(t, store, domain, oldest)
	rollback := stageManagedCertificateGenerationForTest(t, store, domain, "cert-rollback", "key-rollback")
	promoteManagedCertificateGenerationForTest(t, store, domain, rollback)
	active := stageManagedCertificateGenerationForTest(t, store, domain, "cert-active", "key-active")
	promoteManagedCertificateGenerationForTest(t, store, domain, active)
	pending := stageManagedCertificateGenerationForTest(t, store, domain, "cert-pending", "key-pending")

	if err := store.AbortManagedCertificateGeneration(ctx, domain, "gen-not-the-pending-id"); !errors.Is(err, ErrManagedCertificateGenerationNotFound) {
		t.Fatalf("AbortManagedCertificateGeneration(unrelated) error = %v", err)
	}
	if err := store.GarbageCollectManagedCertificateGenerations(ctx, domain); err != nil {
		t.Fatalf("GarbageCollectManagedCertificateGenerations(with pending) error = %v", err)
	}
	if loaded, ok, err := store.LoadPendingManagedCertificateGeneration(ctx, domain); err != nil || !ok || loaded.ID != pending.ID {
		t.Fatalf("pending after GC = (%#v, %v, %v)", loaded, ok, err)
	}
	if _, err := os.Stat(store.managedCertificateGenerationDirectory(domain, oldest.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("oldest superseded generation was not collected with pending retained: %v", err)
	}

	if err := store.AbortManagedCertificateGeneration(ctx, domain, pending.ID); err != nil {
		t.Fatalf("AbortManagedCertificateGeneration(pending) error = %v", err)
	}
	if _, ok, err := store.LoadPendingManagedCertificateGeneration(ctx, domain); err != nil || ok {
		t.Fatalf("pending after abort = (ok=%v, err=%v)", ok, err)
	}
	if err := store.AbortManagedCertificateGeneration(ctx, domain, active.ID); !errors.Is(err, ErrManagedCertificateGenerationActive) {
		t.Fatalf("AbortManagedCertificateGeneration(active) error = %v", err)
	}
	if err := store.GarbageCollectManagedCertificateGenerations(ctx, domain); err != nil {
		t.Fatalf("GarbageCollectManagedCertificateGenerations() error = %v", err)
	}
	for _, generation := range []ManagedCertificateGeneration{rollback, active} {
		if _, err := os.Stat(store.managedCertificateGenerationDirectory(domain, generation.ID)); err != nil {
			t.Fatalf("retained generation %s missing: %v", generation.ID, err)
		}
	}
}

func TestIntegrationManagedCertificateGenerationMigratesLegacyGenerationDirectory(t *testing.T) {
	t.Parallel()
	store := newManagedCertificateGenerationTestStore(t)
	ctx := t.Context()
	const domain = "legacy-generation-directory.example.com"
	seedManagedCertificateGenerationRow(t, store, domain)
	active := stageManagedCertificateGenerationForTest(t, store, domain, "legacy-tree-cert", "legacy-tree-key")
	promoteManagedCertificateGenerationForTest(t, store, domain, active)

	canonicalDirectory := store.managedCertificateDirectory(domain)
	legacyDirectory := store.legacyManagedCertificateDirectory(domain)
	if err := os.RemoveAll(legacyDirectory); err != nil {
		t.Fatalf("remove compatibility projection before legacy fixture move: %v", err)
	}
	if err := os.Remove(filepath.Join(canonicalDirectory, managedCertificateDomainMarkerName)); err != nil {
		t.Fatalf("remove domain marker from legacy fixture: %v", err)
	}
	if err := os.Rename(canonicalDirectory, legacyDirectory); err != nil {
		t.Fatalf("move generation tree into legacy directory: %v", err)
	}

	loaded, ok, err := store.LoadActiveManagedCertificateGeneration(ctx, domain)
	if err != nil || !ok || loaded.ID != active.ID || loaded.Material != active.Material {
		t.Fatalf("LoadActiveManagedCertificateGeneration() from legacy tree = (%#v, %v, %v)", loaded, ok, err)
	}
	if _, err := os.Stat(store.managedCertificateGenerationDirectory(domain, active.ID)); err != nil {
		t.Fatalf("legacy generation was not migrated into collision-free storage: %v", err)
	}
	legacyCert, certErr := readManagedCertificateRegularFile(filepath.Join(legacyDirectory, "cert"))
	legacyKey, keyErr := readManagedCertificateRegularFile(filepath.Join(legacyDirectory, "key"))
	if certErr != nil || keyErr != nil || string(legacyCert) != active.Material.CertPEM || string(legacyKey) != active.Material.KeyPEM {
		t.Fatalf("compatibility projection after legacy tree migration = (cert=%q, key=%q, certErr=%v, keyErr=%v)", legacyCert, legacyKey, certErr, keyErr)
	}
}

func TestIntegrationManagedCertificateGenerationRebuildsMissingCompatibilityProjection(t *testing.T) {
	t.Parallel()
	store := newManagedCertificateGenerationTestStore(t)
	ctx := t.Context()
	const domain = "interrupted-legacy-migration.example.com"
	seedManagedCertificateGenerationRow(t, store, domain)
	active := stageManagedCertificateGenerationForTest(t, store, domain, "canonical-cert", "canonical-key")
	promoteManagedCertificateGenerationForTest(t, store, domain, active)
	legacyDirectory := store.legacyManagedCertificateDirectory(domain)
	if err := os.RemoveAll(legacyDirectory); err != nil {
		t.Fatalf("remove compatibility projection crash fixture: %v", err)
	}

	loaded, ok, err := store.LoadActiveManagedCertificateGeneration(ctx, domain)
	if err != nil || !ok || loaded.ID != active.ID {
		t.Fatalf("LoadActiveManagedCertificateGeneration() = (%#v, %v, %v)", loaded, ok, err)
	}
	legacyCert, certErr := readManagedCertificateRegularFile(filepath.Join(legacyDirectory, "cert"))
	legacyKey, keyErr := readManagedCertificateRegularFile(filepath.Join(legacyDirectory, "key"))
	if certErr != nil || keyErr != nil || string(legacyCert) != active.Material.CertPEM || string(legacyKey) != active.Material.KeyPEM {
		t.Fatalf("rebuilt compatibility projection = (cert=%q, key=%q, certErr=%v, keyErr=%v)", legacyCert, legacyKey, certErr, keyErr)
	}
}

func TestIntegrationManagedCertificateGenerationLegacyProjectionTracksDynamicCollisionOwner(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name  string
		owner string
		alias string
	}{
		{name: "reserved owner first", owner: "edge:a.example.com", alias: "edge_a.example.com"},
		{name: "sanitized owner first", owner: "edge_a.example.com", alias: "edge:a.example.com"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			store := newManagedCertificateGenerationTestStore(t)
			ctx := t.Context()
			ownerRows := []ManagedCertificateRow{{ID: 1, Domain: testCase.owner, Enabled: true}}
			if err := store.SaveManagedCertificates(ctx, ownerRows); err != nil {
				t.Fatalf("SaveManagedCertificates(owner) error = %v", err)
			}
			ownerGeneration := stageManagedCertificateGenerationForTest(t, store, testCase.owner, "owner-cert", "owner-key")
			promoteManagedCertificateGenerationForTest(t, store, testCase.owner, ownerGeneration)

			collisionRows := []ManagedCertificateRow{
				{ID: 1, Domain: testCase.owner, Enabled: true},
				{ID: 2, Domain: testCase.alias, Enabled: true},
			}
			if err := store.SaveManagedCertificates(ctx, collisionRows); err != nil {
				t.Fatalf("SaveManagedCertificates(collision) error = %v", err)
			}
			aliasGeneration := stageManagedCertificateGenerationForTest(t, store, testCase.alias, "alias-cert", "alias-key")
			promoteManagedCertificateGenerationForTest(t, store, testCase.alias, aliasGeneration)
			legacyDirectory := store.legacyManagedCertificateDirectory(testCase.owner)
			if _, err := os.Stat(legacyDirectory); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("ambiguous legacy projection was not retired: %v", err)
			}

			aliasRows := []ManagedCertificateRow{{ID: 2, Domain: testCase.alias, Enabled: true}}
			if err := store.SaveManagedCertificates(ctx, aliasRows); err != nil {
				t.Fatalf("SaveManagedCertificates(alias only) error = %v", err)
			}
			if err := store.CleanupManagedCertificateMaterial(ctx, collisionRows, aliasRows); err != nil {
				t.Fatalf("CleanupManagedCertificateMaterial(owner removal) error = %v", err)
			}
			marker, markerErr := readManagedCertificateRegularFile(filepath.Join(legacyDirectory, managedCertificateDomainMarkerName))
			certPEM, certErr := readManagedCertificateRegularFile(filepath.Join(legacyDirectory, "cert"))
			keyPEM, keyErr := readManagedCertificateRegularFile(filepath.Join(legacyDirectory, "key"))
			if markerErr != nil || certErr != nil || keyErr != nil ||
				string(marker) != testCase.alias || string(certPEM) != aliasGeneration.Material.CertPEM || string(keyPEM) != aliasGeneration.Material.KeyPEM {
				t.Fatalf("legacy projection after owner transfer = marker %q cert %q key %q errors=(%v,%v,%v)", marker, certPEM, keyPEM, markerErr, certErr, keyErr)
			}
		})
	}
}

func TestIntegrationManagedCertificateMigrationCopiesGenerationsAndFallsBackLegacy(t *testing.T) {
	t.Parallel()
	t.Run("generation graph", func(t *testing.T) {
		source := newManagedCertificateGenerationTestStore(t)
		target := newManagedCertificateGenerationTestStore(t)
		ctx := t.Context()
		const domain = "generation-migration.example.com"
		seedManagedCertificateGenerationRow(t, source, domain)
		rollback := stageManagedCertificateGenerationForTest(t, source, domain, "cert-rollback", "key-rollback")
		promoteManagedCertificateGenerationForTest(t, source, domain, rollback)
		active := stageManagedCertificateGenerationForTest(t, source, domain, "cert-active", "key-active")
		promoteManagedCertificateGenerationForTest(t, source, domain, active)
		pending := stageManagedCertificateGenerationForTest(t, source, domain, "cert-pending", "key-pending")

		for attempt := 0; attempt < 2; attempt++ {
			if err := CopyDefaultMigrationRows(ctx, source, target); err != nil {
				t.Fatalf("CopyDefaultMigrationRows(attempt %d) error = %v", attempt+1, err)
			}
		}
		targetActive, ok, err := target.LoadActiveManagedCertificateGeneration(ctx, domain)
		if err != nil || !ok || targetActive.ID != active.ID || targetActive.Material != active.Material {
			t.Fatalf("target active generation = (%#v, %v, %v)", targetActive, ok, err)
		}
		targetPending, ok, err := target.LoadPendingManagedCertificateGeneration(ctx, domain)
		if err != nil || !ok || targetPending.ID != pending.ID || targetPending.Material != pending.Material {
			t.Fatalf("target pending generation = (%#v, %v, %v)", targetPending, ok, err)
		}
		var count int64
		if err := target.db.WithContext(ctx).Model(&ManagedCertificateGenerationRow{}).Where("domain = ?", domain).Count(&count).Error; err != nil {
			t.Fatalf("count target generations: %v", err)
		}
		if count != 3 {
			t.Fatalf("target generation count = %d, want 3", count)
		}
		if _, err := os.Stat(target.managedCertificateGenerationDirectory(domain, rollback.ID)); err != nil {
			t.Fatalf("target rollback generation missing: %v", err)
		}
	})

	t.Run("legacy material", func(t *testing.T) {
		source := newManagedCertificateGenerationTestStore(t)
		target := newManagedCertificateGenerationTestStore(t)
		ctx := t.Context()
		const domain = "legacy-migration.example.com"
		seedManagedCertificateGenerationRow(t, source, domain)
		legacy := ManagedCertificateBundle{Domain: domain, CertPEM: "legacy-migration-cert", KeyPEM: "legacy-migration-key"}
		writeLegacyManagedCertificateGenerationMaterial(t, source.dataRoot, domain, legacy.CertPEM, legacy.KeyPEM)

		if err := CopyDefaultMigrationRows(ctx, source, target); err != nil {
			t.Fatalf("CopyDefaultMigrationRows() error = %v", err)
		}
		active, ok, err := target.LoadActiveManagedCertificateGeneration(ctx, domain)
		if err != nil || !ok || active.Material != legacy {
			t.Fatalf("target legacy fallback generation = (%#v, %v, %v)", active, ok, err)
		}
		var sourceCount int64
		if err := source.db.WithContext(ctx).Model(&ManagedCertificateGenerationRow{}).Where("domain = ?", domain).Count(&sourceCount).Error; err != nil {
			t.Fatalf("count source generations: %v", err)
		}
		if sourceCount != 0 {
			t.Fatalf("migration mutated legacy source generation count = %d", sourceCount)
		}
	})
}

func TestIntegrationManagedCertificateMigrationInstallFailurePreservesTargetActive(t *testing.T) {
	t.Parallel()
	source := newManagedCertificateGenerationTestStore(t)
	target := newManagedCertificateGenerationTestStore(t)
	ctx := t.Context()
	const domain = "migration-install-failure.example.com"
	seedManagedCertificateGenerationRow(t, source, domain)
	sourceActive := stageManagedCertificateGenerationForTest(t, source, domain, "source-cert", "source-key")
	promoteManagedCertificateGenerationForTest(t, source, domain, sourceActive)
	seedManagedCertificateGenerationRow(t, target, domain)
	targetActive := stageManagedCertificateGenerationForTest(t, target, domain, "target-cert", "target-key")
	promoteManagedCertificateGenerationForTest(t, target, domain, targetActive)
	if sourceActive.ID == targetActive.ID {
		t.Fatal("unexpected generation ID collision in fixture")
	}
	obstruction := target.managedCertificateGenerationDirectory(domain, sourceActive.ID)
	if err := os.Mkdir(obstruction, 0o700); err != nil {
		t.Fatalf("create target generation obstruction: %v", err)
	}

	if err := CopyDefaultMigrationRows(ctx, source, target); err == nil {
		t.Fatal("CopyDefaultMigrationRows() install error = nil")
	}
	active, ok, err := target.LoadActiveManagedCertificateGeneration(ctx, domain)
	if err != nil || !ok || active.ID != targetActive.ID || active.Material != targetActive.Material {
		t.Fatalf("target active after failed migration = (%#v, %v, %v)", active, ok, err)
	}
}

func TestIntegrationManagedCertificateMigrationFallsBackLegacyForInvalidGenerationGraph(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name   string
		mutate func(*testing.T, *GormStore, string, ManagedCertificateGeneration)
	}{
		{
			name: "corrupt active",
			mutate: func(t *testing.T, store *GormStore, domain string, _ ManagedCertificateGeneration) {
				t.Helper()
				var certificate ManagedCertificateRow
				if err := store.db.Where("domain = ?", domain).First(&certificate).Error; err != nil {
					t.Fatalf("load source certificate: %v", err)
				}
				path := filepath.Join(store.managedCertificateGenerationDirectory(domain, certificate.ActiveGenerationID), "cert")
				if err := os.WriteFile(path, []byte("corrupt"), 0o600); err != nil {
					t.Fatalf("corrupt source active generation: %v", err)
				}
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			source := newManagedCertificateGenerationTestStore(t)
			target := newManagedCertificateGenerationTestStore(t)
			ctx := t.Context()
			const domain = "invalid-generation-migration.example.com"
			seedManagedCertificateGenerationRow(t, source, domain)
			active := stageManagedCertificateGenerationForTest(t, source, domain, "legacy-safe-cert", "legacy-safe-key")
			promoteManagedCertificateGenerationForTest(t, source, domain, active)
			pending := stageManagedCertificateGenerationForTest(t, source, domain, "pending-cert", "pending-key")
			testCase.mutate(t, source, domain, pending)

			var sourceBefore ManagedCertificateRow
			if err := source.db.WithContext(ctx).Where("domain = ?", domain).First(&sourceBefore).Error; err != nil {
				t.Fatalf("load source pointers before migration: %v", err)
			}
			var sourceCountBefore int64
			if err := source.db.WithContext(ctx).Model(&ManagedCertificateGenerationRow{}).Where("domain = ?", domain).Count(&sourceCountBefore).Error; err != nil {
				t.Fatalf("count source generations before migration: %v", err)
			}

			if err := CopyDefaultMigrationRows(ctx, source, target); err != nil {
				t.Fatalf("CopyDefaultMigrationRows() error = %v", err)
			}
			fallback, ok, err := target.LoadActiveManagedCertificateGeneration(ctx, domain)
			if err != nil || !ok || fallback.Material.CertPEM != active.Material.CertPEM || fallback.Material.KeyPEM != active.Material.KeyPEM {
				t.Fatalf("target legacy fallback = (%#v, %v, %v)", fallback, ok, err)
			}
			if fallback.ID == active.ID || fallback.ID == pending.ID {
				t.Fatalf("target fallback reused invalid source generation id %q", fallback.ID)
			}
			if _, ok, err := target.LoadPendingManagedCertificateGeneration(ctx, domain); err != nil || ok {
				t.Fatalf("target pending after fallback = (ok=%v, err=%v)", ok, err)
			}
			var copiedSourceRows int64
			if err := target.db.WithContext(ctx).Model(&ManagedCertificateGenerationRow{}).
				Where("id IN ?", []string{active.ID, pending.ID}).Count(&copiedSourceRows).Error; err != nil {
				t.Fatalf("count copied invalid source rows: %v", err)
			}
			if copiedSourceRows != 0 {
				t.Fatalf("copied invalid source generation rows = %d", copiedSourceRows)
			}
			var sourceAfter ManagedCertificateRow
			if err := source.db.WithContext(ctx).Where("domain = ?", domain).First(&sourceAfter).Error; err != nil {
				t.Fatalf("load source pointers after migration: %v", err)
			}
			var sourceCountAfter int64
			if err := source.db.WithContext(ctx).Model(&ManagedCertificateGenerationRow{}).Where("domain = ?", domain).Count(&sourceCountAfter).Error; err != nil {
				t.Fatalf("count source generations after migration: %v", err)
			}
			if sourceAfter.ActiveGenerationID != sourceBefore.ActiveGenerationID ||
				sourceAfter.PendingGenerationID != sourceBefore.PendingGenerationID || sourceCountAfter != sourceCountBefore {
				t.Fatalf("migration mutated source: before=%#v/%d after=%#v/%d", sourceBefore, sourceCountBefore, sourceAfter, sourceCountAfter)
			}
		})
	}
}

func TestIntegrationManagedCertificateGenerationSerializesStageAgainstReconcile(t *testing.T) {
	t.Parallel()
	store := newManagedCertificateGenerationTestStore(t)
	ctx := t.Context()
	const domain = "stage-reconcile-barrier.example.com"
	seedManagedCertificateGenerationRow(t, store, domain)
	if err := store.writeTransaction(ctx, func(*gorm.DB) error { return nil }); err != nil {
		t.Fatalf("initialize write database: %v", err)
	}

	store.sqliteWrite.Lock()
	writeLocked := true
	defer func() {
		if writeLocked {
			store.sqliteWrite.Unlock()
		}
	}()
	reconcileDone := make(chan error, 1)
	go func() { reconcileDone <- store.ReconcileManagedCertificateGenerations(ctx, domain) }()
	waitForManagedCertificateDomainLockRefs(t, store, domain, 1)

	stageDone := make(chan struct {
		generation ManagedCertificateGeneration
		err        error
	}, 1)
	go func() {
		generation, err := store.StageManagedCertificateGeneration(ctx, domain, ManagedCertificateBundle{
			Domain: domain, CertPEM: "barrier-cert", KeyPEM: "barrier-key",
		})
		stageDone <- struct {
			generation ManagedCertificateGeneration
			err        error
		}{generation: generation, err: err}
	}()
	waitForManagedCertificateDomainLockRefs(t, store, domain, 2)
	if _, err := os.Stat(store.managedCertificateGenerationsDirectory(domain)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Stage published generation storage while Reconcile held the domain lock: %v", err)
	}

	store.sqliteWrite.Unlock()
	writeLocked = false
	if err := <-reconcileDone; err != nil {
		t.Fatalf("ReconcileManagedCertificateGenerations() error = %v", err)
	}
	staged := <-stageDone
	if staged.err != nil {
		t.Fatalf("StageManagedCertificateGeneration() error = %v", staged.err)
	}
	pending, ok, err := store.LoadPendingManagedCertificateGeneration(ctx, domain)
	if err != nil || !ok || pending.ID != staged.generation.ID {
		t.Fatalf("pending after serialized Stage/Reconcile = (%#v, %v, %v)", pending, ok, err)
	}
}

func TestIntegrationManagedCertificateGenerationSerializesPromotionProjectionAgainstReconcile(t *testing.T) {
	t.Parallel()
	store := newManagedCertificateGenerationTestStore(t)
	ctx := t.Context()
	const domain = "promote-reconcile-barrier.example.com"
	seedManagedCertificateGenerationRow(t, store, domain)
	previous := stageManagedCertificateGenerationForTest(t, store, domain, "previous-cert", "previous-key")
	promoteManagedCertificateGenerationForTest(t, store, domain, previous)
	next := stageManagedCertificateGenerationForTest(t, store, domain, "next-cert", "next-key")

	release := store.lockManagedCertificateDomain(domain)
	promoteDone := make(chan error, 1)
	go func() {
		promoteDone <- store.PromoteManagedCertificateGeneration(ctx, domain, next.ID, next.MaterialHash)
	}()
	waitForManagedCertificateDomainLockRefs(t, store, domain, 2)
	reconcileDone := make(chan error, 1)
	go func() { reconcileDone <- store.ReconcileManagedCertificateGenerations(ctx, domain) }()
	waitForManagedCertificateDomainLockRefs(t, store, domain, 3)

	var row ManagedCertificateRow
	if err := store.db.WithContext(ctx).Where("domain = ?", domain).First(&row).Error; err != nil {
		release()
		t.Fatalf("load certificate before releasing barrier: %v", err)
	}
	material, ok, err := store.readManagedCertificateMaterialSecure(domain)
	if err != nil || !ok || row.ActiveGenerationID != previous.ID || material.CertPEM != previous.Material.CertPEM {
		release()
		t.Fatalf("promotion became partially visible before domain lock release: row=%#v material=%#v ok=%v err=%v", row, material, ok, err)
	}
	release()
	if err := <-promoteDone; err != nil {
		t.Fatalf("PromoteManagedCertificateGeneration() error = %v", err)
	}
	if err := <-reconcileDone; err != nil {
		t.Fatalf("ReconcileManagedCertificateGenerations() error = %v", err)
	}
	active, ok, err := store.LoadActiveManagedCertificateGeneration(ctx, domain)
	if err != nil || !ok || active.ID != next.ID {
		t.Fatalf("active after serialized Promote/Reconcile = (%#v, %v, %v)", active, ok, err)
	}
	material, ok, err = store.readManagedCertificateMaterialSecure(domain)
	if err != nil || !ok || material.CertPEM != next.Material.CertPEM || material.KeyPEM != next.Material.KeyPEM {
		t.Fatalf("projection after serialized Promote/Reconcile = (%#v, %v, %v)", material, ok, err)
	}
}

func waitForManagedCertificateDomainLockRefs(t *testing.T, store *GormStore, domain string, want int) {
	t.Helper()
	key := managedCertificateDomainLockKey(store.dataRoot, domain)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		managedCertificateDomainLocks.Lock()
		entry := managedCertificateDomainLocks.entries[key]
		got := 0
		if entry != nil {
			got = entry.refs
		}
		managedCertificateDomainLocks.Unlock()
		if got == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("managed certificate domain lock refs did not reach %d", want)
}

func TestIntegrationManagedCertificateMigrationReconcileFailureRestoresEntireTargetGraph(t *testing.T) {
	t.Parallel()
	source := newManagedCertificateGenerationTestStore(t)
	target := newManagedCertificateGenerationTestStore(t)
	ctx := t.Context()
	const domain = "migration-reconcile-rollback.example.com"
	seedManagedCertificateGenerationRow(t, source, domain)
	sourceActive := stageManagedCertificateGenerationForTest(t, source, domain, "source-cert", "source-key")
	promoteManagedCertificateGenerationForTest(t, source, domain, sourceActive)
	seedManagedCertificateGenerationRow(t, target, domain)
	targetActive := stageManagedCertificateGenerationForTest(t, target, domain, "target-cert", "target-key")
	promoteManagedCertificateGenerationForTest(t, target, domain, targetActive)
	targetPending := stageManagedCertificateGenerationForTest(t, target, domain, "target-pending-cert", "target-pending-key")

	keyPath := filepath.Join(target.managedCertificateDirectory(domain), "key")
	if err := os.Remove(keyPath); err != nil {
		t.Fatalf("remove target projection key: %v", err)
	}
	if err := os.Mkdir(keyPath, 0o700); err != nil {
		t.Fatalf("create target projection obstruction: %v", err)
	}
	if err := CopyDefaultMigrationRows(ctx, source, target); err == nil {
		t.Fatal("CopyDefaultMigrationRows() reconcile error = nil")
	}

	var certificate ManagedCertificateRow
	if err := target.db.WithContext(ctx).Where("domain = ?", domain).First(&certificate).Error; err != nil {
		t.Fatalf("load restored target certificate: %v", err)
	}
	if certificate.ActiveGenerationID != targetActive.ID || certificate.PendingGenerationID != targetPending.ID {
		t.Fatalf("restored target pointers = active %q pending %q, want %q/%q", certificate.ActiveGenerationID, certificate.PendingGenerationID, targetActive.ID, targetPending.ID)
	}
	for generationID, wantState := range map[string]string{
		targetActive.ID:  ManagedCertificateGenerationStateActive,
		targetPending.ID: ManagedCertificateGenerationStatePending,
	} {
		var row ManagedCertificateGenerationRow
		if err := target.db.WithContext(ctx).Where("id = ?", generationID).First(&row).Error; err != nil {
			t.Fatalf("load restored target generation %s: %v", generationID, err)
		}
		if row.State != wantState {
			t.Fatalf("restored target generation %s state = %q, want %q", generationID, row.State, wantState)
		}
	}
	if _, err := os.Stat(target.managedCertificateGenerationDirectory(domain, sourceActive.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("new source generation remains after reconcile rollback: %v", err)
	}
	if err := os.RemoveAll(keyPath); err != nil {
		t.Fatalf("remove target projection obstruction: %v", err)
	}
	if err := target.ReconcileManagedCertificateGenerations(ctx, domain); err != nil {
		t.Fatalf("ReconcileManagedCertificateGenerations() after repair error = %v", err)
	}
	active, ok, err := target.LoadActiveManagedCertificateGeneration(ctx, domain)
	if err != nil || !ok || active.ID != targetActive.ID || active.Material != targetActive.Material {
		t.Fatalf("target active after migration rollback repair = (%#v, %v, %v)", active, ok, err)
	}
}

func assertNoManagedCertificateMigrationTemporaryDirectories(t *testing.T, store *GormStore, domain string) {
	t.Helper()
	entries, err := os.ReadDir(store.managedCertificateGenerationsDirectory(domain))
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		t.Fatalf("read managed certificate generations directory: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".stage-") {
			t.Fatalf("temporary generation directory remains: %s", entry.Name())
		}
	}
}

func TestIntegrationManagedCertificateMigrationSchemaIsAdditiveAndIdempotent(t *testing.T) {
	t.Parallel()
	assertGenerationSchema := func(t *testing.T, store *GormStore) {
		t.Helper()
		if !store.db.Migrator().HasTable(&ManagedCertificateGenerationRow{}) {
			t.Fatal("managed certificate generations table is missing")
		}
		for _, column := range []string{"ActiveGenerationID", "PendingGenerationID"} {
			if !store.db.Migrator().HasColumn(&ManagedCertificateRow{}, column) {
				t.Fatalf("managed certificates column %s is missing", column)
			}
		}
	}

	t.Run("fresh", func(t *testing.T) {
		store := newManagedCertificateGenerationTestStore(t)
		for attempt := 0; attempt < 2; attempt++ {
			if err := BootstrapSQLiteSchema(t.Context(), store.db); err != nil {
				t.Fatalf("BootstrapSQLiteSchema(attempt %d) error = %v", attempt+1, err)
			}
		}
		assertGenerationSchema(t, store)
	})

	t.Run("legacy", func(t *testing.T) {
		store, err := NewStore(StoreConfig{
			Driver:              "sqlite",
			DataRoot:            t.TempDir(),
			LocalAgentID:        "local",
			SkipBootstrapSchema: true,
			TrafficStatsEnabled: true,
		})
		if err != nil {
			t.Fatalf("NewStore() error = %v", err)
		}
		t.Cleanup(func() { _ = store.Close() })
		if err := store.db.Exec(`CREATE TABLE managed_certificates (id integer PRIMARY KEY, domain text)`).Error; err != nil {
			t.Fatalf("create legacy managed_certificates table: %v", err)
		}
		if err := store.db.Exec(`INSERT INTO managed_certificates (id, domain) VALUES (7, 'legacy-schema.example.com')`).Error; err != nil {
			t.Fatalf("seed legacy managed_certificates row: %v", err)
		}
		for attempt := 0; attempt < 2; attempt++ {
			if err := BootstrapSQLiteSchema(t.Context(), store.db); err != nil {
				t.Fatalf("BootstrapSQLiteSchema(attempt %d) error = %v", attempt+1, err)
			}
		}
		assertGenerationSchema(t, store)
		var row ManagedCertificateRow
		if err := store.db.Where("id = ?", 7).First(&row).Error; err != nil || row.Domain != "legacy-schema.example.com" {
			t.Fatalf("legacy row after schema bootstrap = (%#v, %v)", row, err)
		}
	})
}

func TestIntegrationManagedCertificateGenerationRejectsTraversalSymlinkAndPreservesPermissions(t *testing.T) {
	t.Parallel()
	store := newManagedCertificateGenerationTestStore(t)
	ctx := t.Context()
	if _, err := store.StageManagedCertificateGeneration(ctx, "../../escape", ManagedCertificateBundle{CertPEM: "cert", KeyPEM: "key"}); err == nil {
		t.Fatal("StageManagedCertificateGeneration(traversal) error = nil")
	}

	const domain = "safe.example.com"
	seedManagedCertificateGenerationRow(t, store, domain)
	pending := stageManagedCertificateGenerationForTest(t, store, domain, "cert", "key")
	if err := store.PromoteManagedCertificateGeneration(ctx, domain, "../"+pending.ID, pending.MaterialHash); err == nil {
		t.Fatal("PromoteManagedCertificateGeneration(injected id) error = nil")
	}

	if runtime.GOOS != "windows" {
		const symlinkDomain = "symlink.example.com"
		seedManagedCertificateGenerationRowWithID(t, store, 2, symlinkDomain)
		outside := t.TempDir()
		certificateDir := store.managedCertificateDirectory(symlinkDomain)
		if err := os.MkdirAll(filepath.Dir(certificateDir), 0o700); err != nil {
			t.Fatalf("create managed certificate root: %v", err)
		}
		if err := os.Symlink(outside, certificateDir); err != nil {
			t.Fatalf("create symlink fixture: %v", err)
		}
		if _, err := store.StageManagedCertificateGeneration(ctx, symlinkDomain, ManagedCertificateBundle{CertPEM: "secret-cert", KeyPEM: "secret-key"}); err == nil {
			t.Fatal("StageManagedCertificateGeneration(symlink escape) error = nil")
		}
		entries, err := os.ReadDir(outside)
		if err != nil || len(entries) != 0 {
			t.Fatalf("symlink target entries = %d, error = %v", len(entries), err)
		}
	}

	if runtime.GOOS != "windows" {
		for _, path := range []string{
			store.managedCertificateDirectory(domain),
			store.managedCertificateGenerationsDirectory(domain),
			store.managedCertificateGenerationDirectory(domain, pending.ID),
		} {
			info, err := os.Stat(path)
			if err != nil {
				t.Fatalf("stat directory %s: %v", path, err)
			}
			if info.Mode().Perm() != 0o700 {
				t.Fatalf("directory mode(%s) = %v, want 0700", path, info.Mode().Perm())
			}
		}
		for _, name := range []string{"manifest.json", "cert", "key"} {
			path := filepath.Join(store.managedCertificateGenerationDirectory(domain, pending.ID), name)
			info, err := os.Stat(path)
			if err != nil {
				t.Fatalf("stat file %s: %v", path, err)
			}
			if info.Mode().Perm() != 0o600 {
				t.Fatalf("file mode(%s) = %v, want 0600", path, info.Mode().Perm())
			}
		}
	}
}

func TestIntegrationManagedCertificateGenerationAmbiguousProjectionRejectsSymlinkRoot(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("symlink root safety requires Unix symlink semantics")
	}
	store := newManagedCertificateGenerationTestStore(t)
	const owner = "edge:a.example.com"
	const alias = "edge_a.example.com"
	root := filepath.Join(store.dataRoot, "managed_certificates")
	outside := t.TempDir()
	projection := legacyManagedCertificateDirectory(outside, owner)
	if err := os.MkdirAll(projection, 0o700); err != nil {
		t.Fatalf("create outside projection: %v", err)
	}
	for name, value := range map[string]string{
		managedCertificateDomainMarkerName: owner,
		"cert":                             "outside-cert",
		"key":                              "outside-key",
	} {
		if err := os.WriteFile(filepath.Join(projection, name), []byte(value), 0o600); err != nil {
			t.Fatalf("write outside projection %s: %v", name, err)
		}
	}
	if err := os.Symlink(outside, root); err != nil {
		t.Fatalf("replace managed certificate root with symlink: %v", err)
	}

	if err := store.retireManagedCertificateLegacyProjection(alias); err == nil {
		t.Fatal("retireManagedCertificateLegacyProjection(symlink root) error = nil")
	}
	for _, name := range []string{managedCertificateDomainMarkerName, "cert", "key"} {
		if _, err := os.Stat(filepath.Join(projection, name)); err != nil {
			t.Fatalf("outside projection %s was removed through symlink root: %v", name, err)
		}
	}
}

func writeLegacyManagedCertificateGenerationMaterial(t *testing.T, dataRoot, domain, certPEM, keyPEM string) {
	t.Helper()
	directory := legacyManagedCertificateDirectory(filepath.Join(dataRoot, "managed_certificates"), domain)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("create legacy material directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "cert"), []byte(certPEM), 0o600); err != nil {
		t.Fatalf("write legacy certificate: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "key"), []byte(keyPEM), 0o600); err != nil {
		t.Fatalf("write legacy key: %v", err)
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
