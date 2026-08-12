package service

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/marketplace"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	"gorm.io/gorm"
)

func TestMigratedScalarQuarantineRunPendingGCCleansPhysicalObject(t *testing.T) {
	ctx := context.Background()
	candidate := pluginCandidateFixture(t, "migrated.scalar.gc", "1.0.0", nil, plugins.CleanupPolicy{Instances: "delete", Config: "delete", OwnedData: "delete", Grants: "delete", SharedRefs: "retain", AuditEvents: "retain"})
	sourceRoot, targetRoot := t.TempDir(), t.TempDir()
	sourceCacheRoot := filepath.Join(sourceRoot, "plugins", "packages")
	claim := marketplace.PackageGCClaim{SourceID: candidate.SignatureTrust.SourceID, Digest: candidate.Package.Digest, SignerFingerprint: candidate.SignatureTrust.Fingerprint, Token: "gc_scalar_migration", QuarantineID: "gcq_scalar_migration", Trust: candidate.SignatureTrust}
	scalarRelative, err := marketplace.PackageGCQuarantinePath(claim)
	if err != nil {
		t.Fatal(err)
	}
	sourceQuarantine := filepath.Join(sourceCacheRoot, filepath.FromSlash(scalarRelative))
	if err := copyMarketplaceTestDirectory(candidate.CachePath, sourceQuarantine); err != nil {
		t.Fatal(err)
	}
	source, err := newServiceTestSQLiteStoreForAllTiers(t, sourceRoot, "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = source.Close() })
	target, err := newServiceTestSQLiteStoreForAllTiers(t, targetRoot, "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = target.Close() })
	t.Cleanup(func() {
		_ = marketplace.DiscardVerifiedCacheRoot(sourceCacheRoot)
		_ = marketplace.DiscardVerifiedCacheRoot(filepath.Join(targetRoot, "plugins", "packages"))
	})
	db, err := gorm.Open(sqlite.Open(filepath.Join(sourceRoot, "panel.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	now := time.Now().UTC()
	livePath, err := marketplace.SignerCachePath(sourceCacheRoot, candidate.Package.Digest, candidate.SignatureTrust.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	packageRow := storage.PluginPackageRow{Identity: storage.PluginPackageIdentity(candidate.Package.Digest, candidate.SignatureTrust.SourceID, candidate.SignatureTrust.Fingerprint), Digest: candidate.Package.Digest, PluginID: candidate.Package.Manifest.ID, Version: candidate.Package.Manifest.Version, SourceID: candidate.SignatureTrust.SourceID, SourceKind: candidate.SignatureTrust.SourceKind, SourceRiskLabel: marketplace.UntrustedRiskLabel, SignatureKeyID: candidate.SignatureTrust.KeyID, SignaturePublicKey: candidate.SignatureTrust.PublicKey, SignatureFingerprint: candidate.SignatureTrust.Fingerprint, CachePath: livePath, ManifestJSON: `{}`, ConfigSchemaJSON: `{}`, VerifiedAt: now}
	if err := db.Create(&packageRow).Error; err != nil {
		t.Fatal(err)
	}
	legacyIntent := storage.PluginCacheGCIntentRow{SourceID: claim.SourceID, Digest: claim.Digest, SignerFingerprint: claim.SignerFingerprint, Status: "deleting", ClaimToken: claim.Token, ClaimExpiresAt: now.Add(time.Hour), QuarantinePath: scalarRelative, CacheObjectsJSON: `[]`, UpdatedAt: now}
	if err := db.Create(&legacyIntent).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&storage.PluginDigestFenceRow{Digest: claim.Digest, ClaimToken: claim.Token, ClaimExpiresAt: now.Add(time.Hour), UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := storage.CopyDefaultMigrationRows(ctx, source, target); err != nil {
		t.Fatal(err)
	}
	targetDB, err := gorm.Open(sqlite.Open(filepath.Join(targetRoot, "panel.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	targetSQLDB, err := targetDB.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = targetSQLDB.Close() })
	var migratedIntent storage.PluginCacheGCIntentRow
	if err := targetDB.Where("source_id = ? AND digest = ? AND signer_fingerprint = ?", claim.SourceID, claim.Digest, claim.SignerFingerprint).First(&migratedIntent).Error; err != nil || !migratedIntent.ObjectsPrepared || migratedIntent.QuarantinePath != "" {
		t.Fatalf("migrated scalar intent = %+v, %v", migratedIntent, err)
	}
	var objects []marketplace.PackageGCObject
	if err := json.Unmarshal([]byte(migratedIntent.CacheObjectsJSON), &objects); err != nil || len(objects) != 1 {
		t.Fatalf("migrated scalar objects = %+v, %v", objects, err)
	}
	targetQuarantine := filepath.Join(targetRoot, "plugins", "packages", filepath.FromSlash(objects[0].QuarantinePath))
	if _, err := os.Stat(targetQuarantine); err != nil {
		t.Fatalf("migrated canonical quarantine missing before GC: %v", err)
	}
	service := NewMarketplaceService(target, nil, pluginTestValidator(), filepath.Join(targetRoot, "plugins", "packages"))
	if err := service.RunPendingGC(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(targetQuarantine); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("migrated scalar quarantine leaked after GC: %v", err)
	}
	if intents, err := target.ListPackageGCIntents(ctx); err != nil || len(intents) != 0 {
		t.Fatalf("completed migrated scalar intents = %+v, %v", intents, err)
	}
}

func copyMarketplaceTestDirectory(source, target string) error {
	return filepath.Walk(source, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, relative)
		if info.IsDir() {
			return os.MkdirAll(destination, info.Mode().Perm())
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(destination, data, info.Mode().Perm())
	})
}

func TestMarketplaceResolvePackageUsesOnlyCurrentSnapshotAndDigestCache(t *testing.T) {
	ctx := context.Background()
	store, err := newServiceTestSQLiteStore(t, t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	cleanup := plugins.CleanupPolicy{Instances: "delete", Config: "delete", OwnedData: "delete", Grants: "delete", SharedRefs: "retain", AuditEvents: "retain"}
	candidate := pluginCandidateFixture(t, "official.resolve", "1.0.0", []string{"http.inspect"}, cleanup)
	source, err := marketplaceTestSource("test-fixture-source")
	if err != nil {
		t.Fatal(err)
	}
	manifest := candidate.Package.Manifest
	entry := plugins.MarketEntry{ID: manifest.ID, Version: manifest.Version, Compatibility: manifest.Compatibility, Runtime: plugins.RuntimeIndex{Kind: manifest.Runtime.Kind, ABI: manifest.Runtime.ABI, HostScope: manifest.Runtime.HostScope, PolicyKind: manifest.Runtime.PolicyKind}, Artifacts: []plugins.ArtifactIndex{{SHA256: manifest.Artifacts[0].SHA256, Size: manifest.Artifacts[0].Size}}, PackagePath: "plugins/official.resolve/1.0.0", PackageSHA256: candidate.Package.Digest, SignatureKeyID: manifest.Signature.KeyID, Provenance: "custom", Official: false}
	currentOID := strings.Repeat("1", 40)
	if err := store.PromoteSnapshot(ctx, source, marketplace.Snapshot{ID: "snapshot-current", SourceID: source.ID, Commit: currentOID, Path: "snapshot", ValidatedAt: time.Now().UTC(), Entries: []plugins.MarketEntry{entry}}); err != nil {
		t.Fatal(err)
	}
	validator := pluginTestValidator()
	catalog := NewMarketplaceService(store, nil, validator, filepath.Dir(candidate.CachePath))
	current, err := catalog.CurrentCatalog(ctx, source.ID)
	if err != nil || current.Source.Kind != marketplace.SourceKindCustom || current.Snapshot.Commit != currentOID || len(current.Snapshot.Entries) != 1 {
		t.Fatalf("current market catalog = %+v, %v", current, err)
	}
	resolved, err := catalog.ResolvePackage(ctx, source.ID, entry.ID, entry.Version, entry.PackageSHA256)
	if err != nil || resolved.CachePath != candidate.CachePath || resolved.Package.Digest != candidate.Package.Digest || resolved.sourceID != source.ID || resolved.sourceKind != marketplace.SourceKindCustom {
		t.Fatalf("resolved package = %+v, %v", resolved, err)
	}
	if _, err := catalog.ResolvePackage(ctx, source.ID, entry.ID, entry.Version, pluginTestOtherDigest()); !errors.Is(err, ErrMarketplaceEntryNotFound) {
		t.Fatalf("non-current digest resolution error = %v", err)
	}
	if err := store.PromoteSnapshot(ctx, source, marketplace.Snapshot{ID: "snapshot-next", SourceID: source.ID, Commit: strings.Repeat("2", 40), Path: "snapshot-next", ValidatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if _, err := newPluginTestServiceAtRoot(t, store, filepath.Dir(filepath.Dir(resolved.CachePath))).Install(ctx, PluginInstallRequest{Package: resolved, ActorID: "admin", ConfirmedPermissions: []string{"http.inspect"}, RiskAccepted: true}); err == nil {
		t.Fatal("candidate resolved from a removed snapshot remained installable")
	}
}

func TestOfficialSourceLazyInitializationIsIdempotentUnderConcurrency(t *testing.T) {
	store, err := newServiceTestSQLiteStore(t, t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	barrier := &officialSourceBarrierStore{marketplaceCatalogStore: store, release: make(chan struct{})}
	service := NewMarketplaceService(barrier, nil, plugins.NewValidator(plugins.ValidatorOptions{}), t.TempDir())
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			sources, err := service.ListSources(t.Context())
			if err == nil && (len(sources) != 1 || sources[0].ID != marketplace.OfficialSourceID || sources[0].RefreshInterval != marketplace.OfficialRefreshInterval) {
				err = errors.New("official source was not returned after concurrent initialization")
			}
			results <- err
		}()
	}
	workers.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
}

type officialSourceBarrierStore struct {
	marketplaceCatalogStore
	mu      sync.Mutex
	waiting int
	release chan struct{}
}

func (s *officialSourceBarrierStore) GetMarketplaceSource(ctx context.Context, sourceID string) (marketplace.Source, bool, error) {
	source, ok, err := s.marketplaceCatalogStore.GetMarketplaceSource(ctx, sourceID)
	if err != nil || ok || sourceID != marketplace.OfficialSourceID {
		return source, ok, err
	}
	s.mu.Lock()
	s.waiting++
	if s.waiting == 2 {
		close(s.release)
	}
	release := s.release
	s.mu.Unlock()
	<-release
	return source, ok, nil
}

func TestDeleteMarketplaceSourceCleansSnapshotsAndOnlyUnreferencedCache(t *testing.T) {
	ctx := context.Background()
	dataRoot := t.TempDir()
	store, err := newServiceTestSQLiteStoreForAllTiers(t, dataRoot, "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	cacheRoot := filepath.Join(dataRoot, "plugins", "packages")
	cleanupMarketplaceCache(t, cacheRoot)
	snapshotPath := filepath.Join(dataRoot, "marketplace", "snapshots", "community", "snapshot")
	protectedDigest, garbageDigest, sharedDigest := strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64)
	source, _ := marketplaceTestSource("community")
	trust, err := source.SignatureTrust()
	if err != nil {
		t.Fatal(err)
	}
	protectedPath, _ := marketplace.SignerCachePath(cacheRoot, protectedDigest, trust.Fingerprint)
	garbagePath, _ := marketplace.SignerCachePath(cacheRoot, garbageDigest, trust.Fingerprint)
	sharedPath, _ := marketplace.SignerCachePath(cacheRoot, sharedDigest, trust.Fingerprint)
	for _, path := range []string{protectedPath, garbagePath, sharedPath, snapshotPath} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "fixture"), []byte("data"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := marketplace.Snapshot{ID: "snapshot", SourceID: source.ID, Commit: strings.Repeat("1", 40), Path: snapshotPath, ValidatedAt: time.Now().UTC(), Entries: []plugins.MarketEntry{{ID: "protected", Version: "1.0.0", PackageSHA256: protectedDigest, SignatureKeyID: source.SignerKeyID}, {ID: "garbage", Version: "1.0.0", PackageSHA256: garbageDigest, SignatureKeyID: source.SignerKeyID}, {ID: "shared", Version: "1.0.0", PackageSHA256: sharedDigest, SignatureKeyID: source.SignerKeyID}}}
	if err := store.PromoteSnapshot(ctx, source, snapshot); err != nil {
		t.Fatal(err)
	}
	other, _ := marketplaceTestSource("other")
	otherSnapshot := marketplace.Snapshot{ID: "other-snapshot", SourceID: other.ID, Commit: strings.Repeat("2", 40), Path: filepath.Join(dataRoot, "marketplace", "snapshots", "other", "snapshot"), ValidatedAt: time.Now().UTC(), Entries: []plugins.MarketEntry{{ID: "shared", Version: "1.0.0", PackageSHA256: sharedDigest, SignatureKeyID: other.SignerKeyID}}}
	if err := store.PromoteSnapshot(ctx, other, otherSnapshot); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	identity := storage.PluginPackageIdentity(protectedDigest, source.ID, trust.Fingerprint)
	packageRow := storage.PluginPackageRow{Identity: identity, Digest: protectedDigest, PluginID: "protected", Version: "1.0.0", SourceID: source.ID, SourceKind: source.Kind, SourceRiskLabel: source.RiskLabel, SignatureKeyID: trust.KeyID, SignaturePublicKey: trust.PublicKey, SignatureFingerprint: trust.Fingerprint, CachePath: protectedPath, ManifestJSON: "{}", ConfigSchemaJSON: "{}", VerifiedAt: now}
	operation := storage.PluginOperationRow{ID: "install", PluginID: "protected", Kind: "install", Status: "succeeded", AgentResultsJSON: "{}", ActorID: "admin", CreatedAt: now, CompletedAt: &now}
	if err := storage.BindPluginOperationPackage(&operation, packageRow); err != nil {
		t.Fatal(err)
	}
	install := storage.PluginInstallTransaction{Package: packageRow, Installed: storage.InstalledPluginRow{PluginID: "protected", ActivePackageDigest: protectedDigest, ActivePackageIdentity: identity, ActiveSourceID: source.ID, ActiveSourceKind: source.Kind, ActiveSourceRiskLabel: source.RiskLabel, ActiveSignatureKeyID: trust.KeyID, ActiveSignaturePublicKey: trust.PublicKey, ActiveSignatureFingerprint: trust.Fingerprint, DesiredLifecycle: "disabled", CurrentLifecycle: "disabled", CleanupPolicyJSON: "{}", LastOperationID: "install", InstalledAt: now, UpdatedAt: now}, Operation: operation, Audit: storage.AuditEventRow{ID: "audit-install", Action: "plugin.install", TargetKind: "plugin", TargetID: "protected", Result: "success", MetadataJSON: "{}", CreatedAt: now}}
	if err := store.InstallPlugin(ctx, install); err != nil {
		t.Fatal(err)
	}
	svc := NewMarketplaceService(store, nil, plugins.NewValidator(plugins.ValidatorOptions{}), cacheRoot)
	svc.removeAll = func(path string) error {
		if path == snapshotPath {
			return errors.New("injected snapshot removal failure")
		}
		return os.RemoveAll(path)
	}
	if err := svc.DeleteSource(ctx, source.ID); err == nil {
		t.Fatal("source deletion did not persist a retry after filesystem failure")
	}
	if _, ok, err := store.GetMarketplaceSource(ctx, source.ID); err != nil || ok {
		t.Fatalf("deleting source remained visible: %v, %v", ok, err)
	}
	// A restarted service must discover durable snapshot and cache work without
	// relying on the original DeleteSource return value.
	svc = NewMarketplaceService(store, nil, plugins.NewValidator(plugins.ValidatorOptions{}), cacheRoot)
	if err := svc.RunPendingGC(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.GetMarketplaceSource(ctx, source.ID); err != nil || ok {
		t.Fatalf("source remains: %v, %v", ok, err)
	}
	if _, err := os.Stat(snapshotPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("snapshot directory remains: %v", err)
	}
	if _, err := os.Stat(garbagePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unreferenced cache remains: %v", err)
	}
	if _, err := os.Stat(protectedPath); err != nil {
		t.Fatalf("installed cache was removed: %v", err)
	}
	if _, err := os.Stat(sharedPath); err != nil {
		t.Fatalf("cache referenced by another catalog was removed: %v", err)
	}
	persisted, ok, err := store.GetInstalledPlugin(ctx, "protected")
	if err != nil || !ok {
		t.Fatalf("installed plugin = %+v, %v, %v", persisted, ok, err)
	}
	completed := time.Now().UTC()
	if err := store.ApplyPluginMutation(ctx, storage.PluginMutation{PluginID: persisted.PluginID, ExpectedActive: persisted.ActivePackageDigest, ExpectedStateVersion: persisted.StateVersion, DeletePlugin: true, DeleteInstances: true, DeleteGrants: true, Operation: storage.PluginOperationRow{ID: "uninstall-protected", PluginID: persisted.PluginID, Kind: "uninstall", Status: "succeeded", AgentResultsJSON: "{}", ActorID: "admin", CreatedAt: completed, CompletedAt: &completed}, Audit: storage.AuditEventRow{ID: "audit-uninstall-protected", Action: "plugin.uninstall", TargetKind: "plugin", TargetID: persisted.PluginID, Result: "success", MetadataJSON: "{}", CreatedAt: completed}}); err != nil {
		t.Fatal(err)
	}
	if err := svc.RunPendingGC(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(protectedPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deferred package cache was not reclaimed after uninstall: %v", err)
	}
}

func TestPackageGCReconcilesCoexistingLayoutsAcrossRetryDespiteUnrelatedSignerReference(t *testing.T) {
	ctx := context.Background()
	dataRoot := t.TempDir()
	store, err := newServiceTestSQLiteStoreForAllTiers(t, dataRoot, "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	validator := pluginTestValidator()
	cleanup := plugins.CleanupPolicy{Instances: "delete", Config: "delete", OwnedData: "delete", Grants: "delete", SharedRefs: "retain", AuditEvents: "retain"}
	first := pluginCandidateFixtureAtRoot(t, filepath.Join(t.TempDir(), "plugins", "packages"), "coexisting.gc", "1.0.0", nil, cleanup)
	legacy := pluginCandidateFixtureAtRoot(t, filepath.Join(t.TempDir(), "plugins", "packages"), "coexisting.gc", "1.0.0", nil, cleanup)
	if first.Package.Digest != legacy.Package.Digest {
		t.Fatalf("coexisting fixtures have different digests: %s, %s", first.Package.Digest, legacy.Package.Digest)
	}
	source, err := marketplaceTestSource("coexisting-gc")
	if err != nil {
		t.Fatal(err)
	}
	trust, err := source.SignatureTrust()
	if err != nil {
		t.Fatal(err)
	}
	cacheRoot := filepath.Join(dataRoot, "plugins", "packages")
	cleanupMarketplaceCache(t, cacheRoot)
	legacyPath, err := marketplace.CachePath(cacheRoot, first.Package.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(legacy.Package.Root, legacyPath); err != nil {
		t.Fatal(err)
	}
	cache, err := marketplace.NewVerifiedCache(cacheRoot, validator, store)
	if err != nil {
		t.Fatal(err)
	}
	signerPath, err := cache.StoreWithTrust(first.Package, validator, trust)
	if err != nil {
		t.Fatal(err)
	}

	snapshotPath := filepath.Join(dataRoot, "marketplace", "snapshots", source.ID, "current")
	if err := os.MkdirAll(snapshotPath, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := first.Package.Manifest
	entry := plugins.MarketEntry{ID: manifest.ID, Version: manifest.Version, Compatibility: manifest.Compatibility, Runtime: plugins.RuntimeIndex{Kind: manifest.Runtime.Kind, ABI: manifest.Runtime.ABI, HostScope: manifest.Runtime.HostScope, PolicyKind: manifest.Runtime.PolicyKind}, PackageSHA256: first.Package.Digest, SignatureKeyID: trust.KeyID, Provenance: "custom"}
	if err := store.PromoteSnapshot(ctx, source, marketplace.Snapshot{ID: "coexisting-current", SourceID: source.ID, Commit: strings.Repeat("4", 40), Path: snapshotPath, ValidatedAt: time.Now().UTC(), Entries: []plugins.MarketEntry{entry}}); err != nil {
		t.Fatal(err)
	}

	otherSeed := sha256.Sum256([]byte("unrelated same-digest signer"))
	otherKey := ed25519.NewKeyFromSeed(otherSeed[:])
	otherSource, err := marketplace.NewSignedCustomSource("unrelated-gc", "Unrelated GC", "https://example.com/unrelated-gc.git", "main", "", 0, marketplace.SourceSigner{KeyID: "unrelated-release", SecretRef: "vault-unrelated", PublicKey: base64.StdEncoding.EncodeToString(otherKey.Public().(ed25519.PublicKey))})
	if err != nil {
		t.Fatal(err)
	}
	otherTrust, err := otherSource.SignatureTrust()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	otherIdentity := storage.PluginPackageIdentity(first.Package.Digest, otherSource.ID, otherTrust.Fingerprint)
	otherPackage := storage.PluginPackageRow{Identity: otherIdentity, Digest: first.Package.Digest, PluginID: "unrelated.gc", Version: "1.0.0", SourceID: otherSource.ID, SourceKind: otherSource.Kind, SourceRiskLabel: otherSource.RiskLabel, SignatureKeyID: otherTrust.KeyID, SignaturePublicKey: otherTrust.PublicKey, SignatureFingerprint: otherTrust.Fingerprint, CachePath: filepath.Join(cacheRoot, "unrelated"), ManifestJSON: "{}", ConfigSchemaJSON: "{}", VerifiedAt: now}
	otherOperation := storage.PluginOperationRow{ID: "install-unrelated", PluginID: "unrelated.gc", Kind: "install", Status: "succeeded", AgentResultsJSON: "{}", ActorID: "admin", CreatedAt: now, CompletedAt: &now}
	if err := storage.BindPluginOperationPackage(&otherOperation, otherPackage); err != nil {
		t.Fatal(err)
	}
	if err := store.InstallPlugin(ctx, storage.PluginInstallTransaction{
		Package:   otherPackage,
		Installed: storage.InstalledPluginRow{PluginID: "unrelated.gc", ActivePackageDigest: first.Package.Digest, ActivePackageIdentity: otherIdentity, ActiveSourceID: otherSource.ID, ActiveSourceKind: otherSource.Kind, ActiveSourceRiskLabel: otherSource.RiskLabel, ActiveSignatureKeyID: otherTrust.KeyID, ActiveSignaturePublicKey: otherTrust.PublicKey, ActiveSignatureFingerprint: otherTrust.Fingerprint, DesiredLifecycle: "disabled", CurrentLifecycle: "disabled", CleanupPolicyJSON: "{}", LastOperationID: "install-unrelated", InstalledAt: now, UpdatedAt: now},
		Operation: otherOperation,
		Audit:     storage.AuditEventRow{ID: "audit-install-unrelated", Action: "plugin.install", TargetKind: "plugin", TargetID: "unrelated.gc", Result: "success", MetadataJSON: "{}", CreatedAt: now},
	}); err != nil {
		t.Fatal(err)
	}

	svc := NewMarketplaceService(store, nil, validator, cacheRoot)
	svc.legacyPackageGC = func(string, marketplace.PackageGCClaim, *plugins.Validator, plugins.PackageExpectation) error {
		return errors.New("injected coexistence restart")
	}
	if err := svc.DeleteSource(ctx, source.ID); err == nil {
		t.Fatal("coexisting layout interruption was reported as complete")
	}
	if _, err := os.Stat(signerPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("signer object was not reconciled before interruption: %v", err)
	}
	if _, err := os.Stat(legacyPath); err != nil {
		t.Fatalf("legacy object was lost before restart: %v", err)
	}
	persistedClaim, claimed, err := store.ClaimPackageGC(ctx, source.ID, first.Package.Digest, trust.Fingerprint)
	if err != nil || !claimed || !persistedClaim.ObjectsPrepared || len(persistedClaim.Objects) != 2 {
		t.Fatalf("persisted coexistence objects = %+v, %v, %v", persistedClaim, claimed, err)
	}
	legacyResumed := false
	svc.legacyPackageGC = func(root string, claim marketplace.PackageGCClaim, validator *plugins.Validator, expectation plugins.PackageExpectation) error {
		legacyResumed = true
		return marketplace.QuarantineAndDeleteLegacyVerifiedPackage(root, claim, validator, expectation)
	}
	if err := svc.executePackageGC(ctx, persistedClaim, ""); err != nil {
		t.Fatal(err)
	}
	if err := svc.RunPendingGC(ctx); err != nil {
		t.Fatal(err)
	}
	if !legacyResumed {
		t.Fatal("restart did not resume the persisted legacy cache object")
	}
	for _, removed := range []string{signerPath, legacyPath} {
		if _, err := os.Stat(removed); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("coexisting claimed cache object remains at %q: %v", removed, err)
		}
	}
	if _, ok, err := store.GetInstalledPlugin(ctx, "unrelated.gc"); err != nil || !ok {
		t.Fatalf("unrelated same-digest signer reference was changed: %v, %v", ok, err)
	}
}

func TestSourceEditDuringRefreshCleansOnlyOperationSignerCacheAfterRestart(t *testing.T) {
	ctx := context.Background()
	dataRoot := t.TempDir()
	store, err := newServiceTestSQLiteStoreForAllTiers(t, dataRoot, "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	source, err := marketplaceTestSource("rotated-cache")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveMarketplaceSource(ctx, source); err != nil {
		t.Fatal(err)
	}
	oldTrust := mustMarketplaceSignatureTrust(t, source)
	now := time.Now().UTC()
	old := marketplaceRefreshOperationForTest(t, source, marketplace.RefreshOperation{
		ID: "rotated-cache-old", SourceID: source.ID,
		Status: "running", StartedAt: now, LeaseToken: "rotated-cache-old-lease", LeaseExpiresAt: now.Add(time.Minute),
	})
	if err := store.AcquireRefreshLease(ctx, old); err != nil {
		t.Fatal(err)
	}
	digest := strings.Repeat("9", 64)
	if err := store.StagePackageAcquisition(ctx, source.ID, digest, old.ID, oldTrust); err != nil {
		t.Fatal(err)
	}
	rotatedSeed := sha256.Sum256([]byte("rotated-cache-v2"))
	rotatedKey := ed25519.NewKeyFromSeed(rotatedSeed[:])
	rotated, err := marketplace.NewSignedCustomSource(source.ID, source.Name, source.URL, "release", "", 0, marketplace.SourceSigner{
		KeyID: "rotated-cache-v2", SecretRef: "vault-rotated-cache-v2", PublicKey: base64.StdEncoding.EncodeToString(rotatedKey.Public().(ed25519.PublicKey)),
	})
	if err != nil {
		t.Fatal(err)
	}
	rotated.ConfigRevision = source.ConfigRevision + 1
	rotated, err = store.UpdateMarketplaceSource(ctx, rotated, source.ConfigRevision)
	if err != nil {
		t.Fatal(err)
	}
	newTrust := mustMarketplaceSignatureTrust(t, rotated)
	cacheRoot := filepath.Join(dataRoot, "plugins", "packages")
	cleanupMarketplaceCache(t, cacheRoot)
	oldPath, err := marketplace.SignerCachePath(cacheRoot, digest, oldTrust.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	newPath, err := marketplace.SignerCachePath(cacheRoot, digest, newTrust.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []string{oldPath, newPath} {
		if err := os.MkdirAll(candidate, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	fresh := marketplaceRefreshOperationForTest(t, rotated, marketplace.RefreshOperation{
		ID: "rotated-cache-fresh", SourceID: source.ID,
		Status: "running", StartedAt: time.Now().UTC(), LeaseToken: "rotated-cache-fresh-lease", LeaseExpiresAt: time.Now().UTC().Add(time.Minute),
	})
	if err := store.AcquireRefreshLease(ctx, fresh); err != nil {
		t.Fatalf("rotated source could not acquire after old generation cleanup: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = storage.NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatal(err)
	}
	svc := NewMarketplaceService(store, nil, plugins.NewValidator(plugins.ValidatorOptions{}), cacheRoot)
	if err := svc.RunPendingGC(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(oldPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("S1 signer cache remains after restart cleanup: %v", err)
	}
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("S2 signer cache was removed by S1 cleanup: %v", err)
	}
	if intents, err := store.ListPackageGCIntents(ctx); err != nil || len(intents) != 0 {
		t.Fatalf("S1 cleanup intent remained after restart: %+v err=%v", intents, err)
	}
}

func marketplaceTestSource(id string) (marketplace.Source, error) {
	return marketplace.NewSignedCustomSource(id, strings.ToUpper(id[:1])+id[1:], "https://example.com/"+id+".git", "main", "", 0, marketplace.SourceSigner{KeyID: "test-fixture", SecretRef: "vault-" + id, PublicKey: base64.StdEncoding.EncodeToString(pluginTestSigningKey().Public().(ed25519.PublicKey))})
}

func mustMarketplaceSignatureTrust(t *testing.T, source marketplace.Source) marketplace.SignatureTrust {
	t.Helper()
	trust, err := source.SignatureTrust()
	if err != nil {
		t.Fatal(err)
	}
	return trust
}

func marketplaceRefreshOperationForTest(t *testing.T, source marketplace.Source, operation marketplace.RefreshOperation) marketplace.RefreshOperation {
	t.Helper()
	trust := mustMarketplaceSignatureTrust(t, source)
	operation.SourceRevision, operation.RefKind, operation.RefName = source.ConfigRevision, source.RefKind, source.RefName
	operation.SignerSourceKind, operation.SignerKeyID = trust.SourceKind, trust.KeyID
	operation.SignerPublicKey, operation.SignerFingerprint = trust.PublicKey, trust.Fingerprint
	if operation.Actor.ActorID == "" {
		operation.Actor.ActorID = "test.marketplace"
	}
	if operation.Actor.CorrelationID == "" {
		operation.Actor.CorrelationID = operation.ID
	}
	return operation
}

func cleanupMarketplaceCache(t *testing.T, root string) {
	t.Helper()
	t.Cleanup(func() {
		if err := marketplace.DiscardVerifiedCacheRoot(root); err != nil {
			t.Errorf("discard verified cache root: %v", err)
		}
	})
}

func TestPendingGCRejectsTraversalDigestWithoutFilesystemAccess(t *testing.T) {
	ctx := context.Background()
	dataRoot := t.TempDir()
	store, err := newServiceTestSQLiteStoreForAllTiers(t, dataRoot, "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	fake := &invalidGCIntentStore{marketplaceCatalogStore: store, intent: marketplace.PackageGCIntent{SourceID: "corrupt", Digest: `..\..\outside`}}
	svc := NewMarketplaceService(fake, nil, plugins.NewValidator(plugins.ValidatorOptions{}), filepath.Join(dataRoot, "plugins", "packages"))
	removed := false
	svc.removeAll = func(string) error { removed = true; return nil }
	if err := svc.RunPendingGC(ctx); err == nil {
		t.Fatal("invalid durable GC digest was not rejected")
	}
	if removed {
		t.Fatal("invalid durable GC digest reached filesystem deletion")
	}
	if fake.failure != "invalid package digest" {
		t.Fatalf("invalid intent failure = %q", fake.failure)
	}
}

type invalidGCIntentStore struct {
	marketplaceCatalogStore
	intent  marketplace.PackageGCIntent
	failure string
}

func (s *invalidGCIntentStore) ListPackageGCIntents(context.Context) ([]marketplace.PackageGCIntent, error) {
	return []marketplace.PackageGCIntent{s.intent}, nil
}

func (s *invalidGCIntentStore) RecordPackageGCFailure(_ context.Context, _, _, _, failure string) error {
	s.failure = failure
	return nil
}

func pluginTestOtherDigest() string {
	return "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
}
