package storage

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestManagedCertificateGenerationStageIsInvisibleUntilHashGatedPromote(t *testing.T) {
	dataRoot := t.TempDir()
	store, err := NewSQLiteStore(dataRoot, "local")
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

func TestManagedCertificateGenerationSaveRowsPreservesInternalPointers(t *testing.T) {
	store := newManagedCertificateGenerationTestStore(t)
	ctx := t.Context()
	const domain = "preserve-pointers.example.com"
	seedManagedCertificateGenerationRow(t, store, domain)
	active := stageManagedCertificateGenerationForTest(t, store, domain, "cert-active", "key-active")
	promoteManagedCertificateGenerationForTest(t, store, domain, active)
	pending := stageManagedCertificateGenerationForTest(t, store, domain, "cert-pending", "key-pending")

	if err := store.SaveManagedCertificates(ctx, []ManagedCertificateRow{{ID: 1, Domain: domain, Enabled: true, Status: "active"}}); err != nil {
		t.Fatalf("SaveManagedCertificates() error = %v", err)
	}
	loadedActive, ok, err := store.LoadActiveManagedCertificateGeneration(ctx, domain)
	if err != nil || !ok || loadedActive.ID != active.ID {
		t.Fatalf("active after legacy row save = (%#v, %v, %v)", loadedActive, ok, err)
	}
	loadedPending, ok, err := store.LoadPendingManagedCertificateGeneration(ctx, domain)
	if err != nil || !ok || loadedPending.ID != pending.ID {
		t.Fatalf("pending after legacy row save = (%#v, %v, %v)", loadedPending, ok, err)
	}
}

func TestManagedCertificateGenerationLoadRejectsStateDivergenceAndFallsBack(t *testing.T) {
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

func TestManagedCertificateGenerationProjectionFailureCompensatesAndReconciles(t *testing.T) {
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

func TestManagedCertificateGenerationProjectionPreflightPreservesLegacyMaterial(t *testing.T) {
	store := newManagedCertificateGenerationTestStore(t)
	ctx := t.Context()
	const domain = "projection-preflight.example.com"
	seedManagedCertificateGenerationRow(t, store, domain)
	writeLegacyManagedCertificateGenerationMaterial(t, store.dataRoot, domain, "cert-legacy", "key-legacy")
	pending := stageManagedCertificateGenerationForTest(t, store, domain, "cert-next", "key-next")

	keyPath := filepath.Join(store.managedCertificateDirectory(domain), "key")
	if err := os.Remove(keyPath); err != nil {
		t.Fatalf("remove legacy projection key: %v", err)
	}
	if err := os.Mkdir(keyPath, 0o700); err != nil {
		t.Fatalf("create projection obstruction: %v", err)
	}
	if err := store.PromoteManagedCertificateGeneration(ctx, domain, pending.ID, pending.MaterialHash); err == nil {
		t.Fatal("PromoteManagedCertificateGeneration() projection error = nil")
	}
	certPEM, err := readManagedCertificateRegularFile(filepath.Join(store.managedCertificateDirectory(domain), "cert"))
	if err != nil {
		t.Fatalf("read legacy projection certificate: %v", err)
	}
	if got := string(certPEM); got != "cert-legacy" {
		t.Fatalf("legacy projection certificate = %q, want %q", got, "cert-legacy")
	}
	projectionTemps, err := filepath.Glob(filepath.Join(store.managedCertificateDirectory(domain), ".projection-*"))
	if err != nil {
		t.Fatalf("glob projection temporary files: %v", err)
	}
	if len(projectionTemps) != 0 {
		t.Fatalf("projection temporary files after preflight failure = %v", projectionTemps)
	}
	if _, ok, err := store.LoadActiveManagedCertificateGeneration(ctx, domain); err != nil || ok {
		t.Fatalf("active after failed first projection = (ok=%v, err=%v), want no active", ok, err)
	}
	gotPending, ok, err := store.LoadPendingManagedCertificateGeneration(ctx, domain)
	if err != nil || !ok || gotPending.ID != pending.ID {
		t.Fatalf("pending after failed first projection = (%#v, %v, %v)", gotPending, ok, err)
	}
}

func TestManagedCertificateGenerationGCSkipsCorruptNewestRollback(t *testing.T) {
	store := newManagedCertificateGenerationTestStore(t)
	ctx := t.Context()
	const domain = "gc-corrupt-rollback.example.com"
	seedManagedCertificateGenerationRow(t, store, domain)
	validRollback := stageManagedCertificateGenerationForTest(t, store, domain, "cert-valid-rollback", "key-valid-rollback")
	promoteManagedCertificateGenerationForTest(t, store, domain, validRollback)
	corruptNewest := stageManagedCertificateGenerationForTest(t, store, domain, "cert-corrupt-rollback", "key-corrupt-rollback")
	promoteManagedCertificateGenerationForTest(t, store, domain, corruptNewest)
	active := stageManagedCertificateGenerationForTest(t, store, domain, "cert-active", "key-active")
	promoteManagedCertificateGenerationForTest(t, store, domain, active)
	if err := os.WriteFile(filepath.Join(store.managedCertificateGenerationDirectory(domain, corruptNewest.ID), "cert"), []byte("corrupt"), 0o600); err != nil {
		t.Fatalf("corrupt newest rollback: %v", err)
	}

	if err := store.GarbageCollectManagedCertificateGenerations(ctx, domain); err != nil {
		t.Fatalf("GarbageCollectManagedCertificateGenerations() error = %v", err)
	}
	for _, generation := range []ManagedCertificateGeneration{validRollback, active} {
		if _, err := os.Stat(store.managedCertificateGenerationDirectory(domain, generation.ID)); err != nil {
			t.Fatalf("retained generation %s missing: %v", generation.ID, err)
		}
	}
	if _, err := os.Stat(store.managedCertificateGenerationDirectory(domain, corruptNewest.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("corrupt newest rollback was not collected: %v", err)
	}
}

func TestManagedCertificateGenerationCleanupRemovesDeletedDomainOnly(t *testing.T) {
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

func TestManagedCertificateGenerationReconcileRemovesFinalizedOrphansOnly(t *testing.T) {
	store := newManagedCertificateGenerationTestStore(t)
	ctx := t.Context()
	const domain = "orphan-generation.example.com"
	seedManagedCertificateGenerationRow(t, store, domain)
	rollback := stageManagedCertificateGenerationForTest(t, store, domain, "rollback-cert", "rollback-key")
	promoteManagedCertificateGenerationForTest(t, store, domain, rollback)
	active := stageManagedCertificateGenerationForTest(t, store, domain, "active-cert", "active-key")
	promoteManagedCertificateGenerationForTest(t, store, domain, active)
	orphan := store.managedCertificateGenerationDirectory(domain, "gen-orphan-finalized")
	if err := os.Mkdir(orphan, 0o700); err != nil {
		t.Fatalf("create finalized orphan: %v", err)
	}
	if err := os.WriteFile(filepath.Join(orphan, "manifest.json"), []byte(`{"orphan":true}`), 0o600); err != nil {
		t.Fatalf("write finalized orphan fixture: %v", err)
	}

	if err := store.ReconcileManagedCertificateGenerations(ctx, domain); err != nil {
		t.Fatalf("ReconcileManagedCertificateGenerations() error = %v", err)
	}
	if _, err := os.Stat(orphan); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("finalized orphan still exists: %v", err)
	}
	for _, generation := range []ManagedCertificateGeneration{rollback, active} {
		if _, err := os.Stat(store.managedCertificateGenerationDirectory(domain, generation.ID)); err != nil {
			t.Fatalf("recovery anchor %s was removed: %v", generation.ID, err)
		}
	}
}

func TestManagedCertificateGenerationReconcileFallsBackAndCleansOrphanStage(t *testing.T) {
	store := newManagedCertificateGenerationTestStore(t)
	ctx := t.Context()
	const domain = "reconcile.example.com"
	seedManagedCertificateGenerationRow(t, store, domain)
	rollback := stageManagedCertificateGenerationForTest(t, store, domain, "cert-rollback", "key-rollback")
	promoteManagedCertificateGenerationForTest(t, store, domain, rollback)
	current := stageManagedCertificateGenerationForTest(t, store, domain, "cert-current", "key-current")
	promoteManagedCertificateGenerationForTest(t, store, domain, current)
	if err := os.WriteFile(filepath.Join(store.managedCertificateGenerationDirectory(domain, current.ID), "cert"), []byte("corrupt"), 0o600); err != nil {
		t.Fatalf("corrupt active generation: %v", err)
	}
	orphan := filepath.Join(store.managedCertificateGenerationsDirectory(domain), ".stage-orphan")
	if err := os.Mkdir(orphan, 0o700); err != nil {
		t.Fatalf("create orphan stage: %v", err)
	}
	if err := store.ReconcileManagedCertificateGenerations(ctx, domain); err != nil {
		t.Fatalf("ReconcileManagedCertificateGenerations() error = %v", err)
	}
	active, ok, err := store.LoadActiveManagedCertificateGeneration(ctx, domain)
	if err != nil || !ok || active.ID != rollback.ID {
		t.Fatalf("active after corrupt-current reconcile = (%#v, %v, %v)", active, ok, err)
	}
	if _, err := os.Stat(orphan); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("orphan stage still exists: %v", err)
	}
}

func TestManagedCertificateGenerationAbortAndGCRetainRecoveryAnchors(t *testing.T) {
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

func TestManagedCertificateGenerationLegacyLoadImportsIdempotentActive(t *testing.T) {
	store := newManagedCertificateGenerationTestStore(t)
	ctx := t.Context()
	const domain = "legacy-import.example.com"
	seedManagedCertificateGenerationRow(t, store, domain)
	legacy := ManagedCertificateBundle{Domain: domain, CertPEM: "legacy-cert", KeyPEM: "legacy-key"}
	writeLegacyManagedCertificateGenerationMaterial(t, store.dataRoot, domain, legacy.CertPEM, legacy.KeyPEM)

	for attempt := 0; attempt < 2; attempt++ {
		loaded, ok, err := store.LoadManagedCertificateMaterial(ctx, domain)
		if err != nil || !ok || loaded != legacy {
			t.Fatalf("LoadManagedCertificateMaterial(attempt %d) = (%#v, %v, %v)", attempt+1, loaded, ok, err)
		}
	}
	active, ok, err := store.LoadActiveManagedCertificateGeneration(ctx, domain)
	if err != nil || !ok || active.Material != legacy {
		t.Fatalf("legacy active generation = (%#v, %v, %v)", active, ok, err)
	}
	var count int64
	if err := store.db.WithContext(ctx).Model(&ManagedCertificateGenerationRow{}).Where("domain = ?", domain).Count(&count).Error; err != nil {
		t.Fatalf("count legacy generations: %v", err)
	}
	if count != 1 {
		t.Fatalf("legacy generation count = %d, want 1", count)
	}
}

func TestManagedCertificateGenerationLegacyIDsAreDomainScoped(t *testing.T) {
	store := newManagedCertificateGenerationTestStore(t)
	ctx := t.Context()
	domains := []string{"same-material-a.example.com", "same-material-b.example.com"}
	if err := store.SaveManagedCertificates(ctx, []ManagedCertificateRow{
		{ID: 1, Domain: domains[0], Enabled: true},
		{ID: 2, Domain: domains[1], Enabled: true},
	}); err != nil {
		t.Fatalf("SaveManagedCertificates() error = %v", err)
	}
	for _, domain := range domains {
		writeLegacyManagedCertificateGenerationMaterial(t, store.dataRoot, domain, "same-cert", "same-key")
	}
	ids := make(map[string]struct{}, len(domains))
	for _, domain := range domains {
		loaded, ok, err := store.LoadManagedCertificateMaterial(ctx, domain)
		if err != nil || !ok || loaded.CertPEM != "same-cert" || loaded.KeyPEM != "same-key" {
			t.Fatalf("LoadManagedCertificateMaterial(%s) = (%#v, %v, %v)", domain, loaded, ok, err)
		}
		active, ok, err := store.LoadActiveManagedCertificateGeneration(ctx, domain)
		if err != nil || !ok {
			t.Fatalf("LoadActiveManagedCertificateGeneration(%s) = (%#v, %v, %v)", domain, active, ok, err)
		}
		ids[active.ID] = struct{}{}
	}
	if len(ids) != len(domains) {
		t.Fatalf("domain-scoped legacy generation IDs = %#v", ids)
	}
}

func TestManagedCertificateGenerationLegacySaveCreatesActive(t *testing.T) {
	store := newManagedCertificateGenerationTestStore(t)
	ctx := t.Context()
	const domain = "legacy-save.example.com"
	seedManagedCertificateGenerationRow(t, store, domain)
	bundle := ManagedCertificateBundle{Domain: domain, CertPEM: "saved-cert", KeyPEM: "saved-key"}
	if err := store.SaveManagedCertificateMaterial(ctx, domain, bundle); err != nil {
		t.Fatalf("SaveManagedCertificateMaterial() error = %v", err)
	}
	active, ok, err := store.LoadActiveManagedCertificateGeneration(ctx, domain)
	if err != nil || !ok || active.Material != bundle {
		t.Fatalf("active generation after legacy save = (%#v, %v, %v)", active, ok, err)
	}
}

func TestManagedCertificateMigrationCopiesGenerationsAndFallsBackLegacy(t *testing.T) {
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

func TestManagedCertificateMigrationFallsBackLegacyForInvalidGenerationGraph(t *testing.T) {
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
		{
			name: "missing pending",
			mutate: func(t *testing.T, store *GormStore, domain string, pending ManagedCertificateGeneration) {
				t.Helper()
				if err := os.RemoveAll(store.managedCertificateGenerationDirectory(domain, pending.ID)); err != nil {
					t.Fatalf("remove source pending generation: %v", err)
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

func TestManagedCertificateMigrationInstallFailurePreservesTargetActive(t *testing.T) {
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

func TestManagedCertificateMigrationIncrementalInstallFailurePreservesTargetActive(t *testing.T) {
	source := newManagedCertificateGenerationTestStore(t)
	target := newManagedCertificateGenerationTestStore(t)
	ctx := t.Context()
	const domain = "incremental-migration-failure.example.com"
	seedManagedCertificateGenerationRow(t, source, domain)
	previous := stageManagedCertificateGenerationForTest(t, source, domain, "previous-cert", "previous-key")
	promoteManagedCertificateGenerationForTest(t, source, domain, previous)
	if err := CopyDefaultMigrationRows(ctx, source, target); err != nil {
		t.Fatalf("initial CopyDefaultMigrationRows() error = %v", err)
	}
	next := stageManagedCertificateGenerationForTest(t, source, domain, "next-cert", "next-key")
	promoteManagedCertificateGenerationForTest(t, source, domain, next)
	obstruction := target.managedCertificateGenerationDirectory(domain, next.ID)
	if err := os.Mkdir(obstruction, 0o700); err != nil {
		t.Fatalf("create incremental target obstruction: %v", err)
	}

	if err := CopyDefaultMigrationRows(ctx, source, target); err == nil {
		t.Fatal("incremental CopyDefaultMigrationRows() install error = nil")
	}
	active, ok, err := target.LoadActiveManagedCertificateGeneration(ctx, domain)
	if err != nil || !ok || active.ID != previous.ID || active.Material != previous.Material {
		t.Fatalf("target active after failed incremental migration = (%#v, %v, %v)", active, ok, err)
	}
}

func TestManagedCertificateMigrationSchemaIsAdditiveAndIdempotent(t *testing.T) {
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

func TestManagedCertificateGenerationRejectsTraversalSymlinkAndPreservesPermissions(t *testing.T) {
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

func writeLegacyManagedCertificateGenerationMaterial(t *testing.T, dataRoot, domain, certPEM, keyPEM string) {
	t.Helper()
	directory := managedCertificateDirectory(filepath.Join(dataRoot, "managed_certificates"), domain)
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
	store, err := NewSQLiteStore(t.TempDir(), "local")
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
