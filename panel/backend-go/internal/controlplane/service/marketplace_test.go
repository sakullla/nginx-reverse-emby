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
	source, err := storage.NewSQLiteStore(sourceRoot, "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = source.Close() })
	target, err := storage.NewSQLiteStore(targetRoot, "local")
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
	store, err := storage.NewSQLiteStore(t.TempDir(), "local")
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
	entry := plugins.MarketEntry{ID: manifest.ID, Version: manifest.Version, Compatibility: manifest.Compatibility, Runtime: plugins.RuntimeIndex{Kind: manifest.Runtime.Kind, ABI: manifest.Runtime.ABI, HostScope: manifest.Runtime.HostScope}, Artifacts: []plugins.ArtifactIndex{{SHA256: manifest.Artifacts[0].SHA256, Size: manifest.Artifacts[0].Size}}, PackagePath: "plugins/official.resolve/1.0.0", PackageSHA256: candidate.Package.Digest, SignatureKeyID: manifest.Signature.KeyID, Provenance: "custom", Official: false}
	if err := store.PromoteSnapshot(ctx, source, marketplace.Snapshot{ID: "snapshot-current", SourceID: source.ID, Commit: "commit", Path: "snapshot", ValidatedAt: time.Now().UTC(), Entries: []plugins.MarketEntry{entry}}); err != nil {
		t.Fatal(err)
	}
	validator := pluginTestValidator()
	catalog := NewMarketplaceService(store, nil, validator, filepath.Dir(candidate.CachePath))
	current, err := catalog.CurrentCatalog(ctx, source.ID)
	if err != nil || current.Source.Kind != marketplace.SourceKindCustom || current.Snapshot.Commit != "commit" || len(current.Snapshot.Entries) != 1 {
		t.Fatalf("current market catalog = %+v, %v", current, err)
	}
	resolved, err := catalog.ResolvePackage(ctx, source.ID, entry.ID, entry.Version, entry.PackageSHA256)
	if err != nil || resolved.CachePath != candidate.CachePath || resolved.Package.Digest != candidate.Package.Digest || resolved.sourceID != source.ID || resolved.sourceKind != marketplace.SourceKindCustom {
		t.Fatalf("resolved package = %+v, %v", resolved, err)
	}
	if _, err := catalog.ResolvePackage(ctx, source.ID, entry.ID, entry.Version, pluginTestOtherDigest()); !errors.Is(err, ErrMarketplaceEntryNotFound) {
		t.Fatalf("non-current digest resolution error = %v", err)
	}
	if err := store.PromoteSnapshot(ctx, source, marketplace.Snapshot{ID: "snapshot-next", SourceID: source.ID, Commit: "next", Path: "snapshot-next", ValidatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if _, err := newPluginTestServiceAtRoot(t, store, filepath.Dir(filepath.Dir(resolved.CachePath))).Install(ctx, PluginInstallRequest{Package: resolved, ActorID: "admin", ConfirmedPermissions: []string{"http.inspect"}, RiskAccepted: true}); err == nil {
		t.Fatal("candidate resolved from a removed snapshot remained installable")
	}
}

func TestMarketplacePrevalidationFailuresAreAuditedWithTrustedProvenance(t *testing.T) {
	ctx := storage.WithQuotaActor(context.Background(), storage.QuotaActor{UserID: "admin", SessionID: "session", CorrelationID: "request"})
	store, err := storage.NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	svc := NewMarketplaceService(store, nil, plugins.NewValidator(plugins.ValidatorOptions{}), t.TempDir())
	if _, err := svc.AddCustomSource(ctx, "bad", "Bad", "https://example.com/plugins.git?token=plaintext", "main", "", 0, marketplace.SourceSigner{}); err == nil {
		t.Fatal("unsafe source URL was accepted")
	}
	if _, err := svc.AddCustomSource(ctx, "private", "Private", "https://example.com/plugins.git", "main", "secret-ref", 0, marketplace.SourceSigner{}); err == nil {
		t.Fatal("credential source without trusted authorization was accepted")
	}
	if _, err := svc.AddCustomSource(ctx, "negative", "Negative", "https://example.com/plugins.git", "main", "", -time.Hour, marketplace.SourceSigner{}); err == nil {
		t.Fatal("negative refresh interval was accepted")
	}
	audits, err := store.ListAuditEvents(ctx, 20)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, audit := range audits {
		if audit.Action != "marketplace.source.add" {
			continue
		}
		if strings.Contains(audit.MetadataJSON, "plaintext") || strings.Contains(audit.MetadataJSON, "secret-ref") {
			t.Fatalf("marketplace failure audit leaked input: %+v", audit)
		}
		if audit.ActorID != "admin" || audit.SessionID != "session" || audit.CorrelationID != "request" {
			t.Fatalf("marketplace failure audit lost provenance: %+v", audit)
		}
		found[audit.ErrorClass] = true
	}
	if !found["validation"] || !found["credential_authorization"] {
		t.Fatalf("missing prevalidation audits: %+v", found)
	}
}

func TestOfficialSourceLazyInitializationIsIdempotentUnderConcurrency(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir(), "local")
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
	store, err := storage.NewSQLiteStore(dataRoot, "local")
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
	snapshot := marketplace.Snapshot{ID: "snapshot", SourceID: source.ID, Commit: "commit", Path: snapshotPath, ValidatedAt: time.Now().UTC(), Entries: []plugins.MarketEntry{{ID: "protected", Version: "1.0.0", PackageSHA256: protectedDigest, SignatureKeyID: source.SignerKeyID}, {ID: "garbage", Version: "1.0.0", PackageSHA256: garbageDigest, SignatureKeyID: source.SignerKeyID}, {ID: "shared", Version: "1.0.0", PackageSHA256: sharedDigest, SignatureKeyID: source.SignerKeyID}}}
	if err := store.PromoteSnapshot(ctx, source, snapshot); err != nil {
		t.Fatal(err)
	}
	other, _ := marketplaceTestSource("other")
	otherSnapshot := marketplace.Snapshot{ID: "other-snapshot", SourceID: other.ID, Commit: "other-commit", Path: filepath.Join(dataRoot, "marketplace", "snapshots", "other", "snapshot"), ValidatedAt: time.Now().UTC(), Entries: []plugins.MarketEntry{{ID: "shared", Version: "1.0.0", PackageSHA256: sharedDigest, SignatureKeyID: other.SignerKeyID}}}
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

func TestDeferredUninstallAfterSourceDeletionReclaimsExactTrustLegacyCache(t *testing.T) {
	ctx := context.Background()
	dataRoot := t.TempDir()
	store, err := storage.NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	validator := pluginTestValidator()
	candidate := pluginCandidateFixture(t, "legacy.deferred", "1.0.0", nil, plugins.CleanupPolicy{Instances: "delete", Config: "delete", OwnedData: "delete", Grants: "delete", SharedRefs: "retain", AuditEvents: "retain"})
	source, err := marketplaceTestSource("legacy-deferred")
	if err != nil {
		t.Fatal(err)
	}
	trust, err := source.SignatureTrust()
	if err != nil {
		t.Fatal(err)
	}
	cacheRoot := filepath.Join(dataRoot, "plugins", "packages")
	cleanupMarketplaceCache(t, cacheRoot)
	legacyPath, err := marketplace.CachePath(cacheRoot, candidate.Package.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(candidate.Package.Root, legacyPath); err != nil {
		t.Fatal(err)
	}
	if _, err := marketplace.NewVerifiedCache(cacheRoot, validator, store); err != nil {
		t.Fatal(err)
	}

	manifest := candidate.Package.Manifest
	snapshotPath := filepath.Join(dataRoot, "marketplace", "snapshots", source.ID, "current")
	if err := os.MkdirAll(snapshotPath, 0o755); err != nil {
		t.Fatal(err)
	}
	entry := plugins.MarketEntry{ID: manifest.ID, Version: manifest.Version, Compatibility: manifest.Compatibility, Runtime: plugins.RuntimeIndex{Kind: manifest.Runtime.Kind, ABI: manifest.Runtime.ABI, HostScope: manifest.Runtime.HostScope}, PackageSHA256: candidate.Package.Digest, SignatureKeyID: trust.KeyID, Provenance: "custom"}
	if err := store.PromoteSnapshot(ctx, source, marketplace.Snapshot{ID: "legacy-current", SourceID: source.ID, Commit: "legacy-commit", Path: snapshotPath, ValidatedAt: time.Now().UTC(), Entries: []plugins.MarketEntry{entry}}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	identity := storage.PluginPackageIdentity(candidate.Package.Digest, source.ID, trust.Fingerprint)
	packageRow := storage.PluginPackageRow{Identity: identity, Digest: candidate.Package.Digest, PluginID: manifest.ID, Version: manifest.Version, SourceID: source.ID, SourceKind: source.Kind, SourceRiskLabel: source.RiskLabel, SignatureKeyID: trust.KeyID, SignaturePublicKey: trust.PublicKey, SignatureFingerprint: trust.Fingerprint, CachePath: legacyPath, ManifestJSON: "{}", ConfigSchemaJSON: "{}", VerifiedAt: now}
	operation := storage.PluginOperationRow{ID: "install-legacy", PluginID: manifest.ID, Kind: "install", Status: "succeeded", AgentResultsJSON: "{}", ActorID: "admin", CreatedAt: now, CompletedAt: &now}
	if err := storage.BindPluginOperationPackage(&operation, packageRow); err != nil {
		t.Fatal(err)
	}
	install := storage.PluginInstallTransaction{
		Package:   packageRow,
		Installed: storage.InstalledPluginRow{PluginID: manifest.ID, ActivePackageDigest: candidate.Package.Digest, ActivePackageIdentity: identity, ActiveSourceID: source.ID, ActiveSourceKind: source.Kind, ActiveSourceRiskLabel: source.RiskLabel, ActiveSignatureKeyID: trust.KeyID, ActiveSignaturePublicKey: trust.PublicKey, ActiveSignatureFingerprint: trust.Fingerprint, DesiredLifecycle: "disabled", CurrentLifecycle: "disabled", CleanupPolicyJSON: "{}", LastOperationID: "install-legacy", InstalledAt: now, UpdatedAt: now},
		Operation: operation,
		Audit:     storage.AuditEventRow{ID: "audit-install-legacy", Action: "plugin.install", TargetKind: "plugin", TargetID: manifest.ID, Result: "success", MetadataJSON: "{}", CreatedAt: now},
	}
	if err := store.InstallPlugin(ctx, install); err != nil {
		t.Fatal(err)
	}

	svc := NewMarketplaceService(store, nil, validator, cacheRoot)
	if err := svc.DeleteSource(ctx, source.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.GetMarketplaceSource(ctx, source.ID); err != nil || ok {
		t.Fatalf("source deletion was not completed while package GC was deferred: %v, %v", ok, err)
	}
	if _, err := os.Stat(legacyPath); err != nil {
		t.Fatalf("referenced legacy cache was removed before uninstall: %v", err)
	}
	persisted, ok, err := store.GetInstalledPlugin(ctx, manifest.ID)
	if err != nil || !ok {
		t.Fatalf("installed legacy plugin = %+v, %v, %v", persisted, ok, err)
	}
	completed := time.Now().UTC()
	if err := store.ApplyPluginMutation(ctx, storage.PluginMutation{PluginID: persisted.PluginID, ExpectedActive: persisted.ActivePackageDigest, ExpectedStateVersion: persisted.StateVersion, DeletePlugin: true, DeleteInstances: true, DeleteGrants: true, Operation: storage.PluginOperationRow{ID: "uninstall-legacy", PluginID: persisted.PluginID, Kind: "uninstall", Status: "succeeded", AgentResultsJSON: "{}", ActorID: "admin", CreatedAt: completed, CompletedAt: &completed}, Audit: storage.AuditEventRow{ID: "audit-uninstall-legacy", Action: "plugin.uninstall", TargetKind: "plugin", TargetID: persisted.PluginID, Result: "success", MetadataJSON: "{}", CreatedAt: completed}}); err != nil {
		t.Fatal(err)
	}
	if err := svc.RunPendingGC(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(legacyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy cache remains after deferred uninstall GC: %v", err)
	}
	if _, ok, err := store.GetPluginPackageByIdentity(ctx, identity); err != nil || ok {
		t.Fatalf("legacy package metadata completed before physical deletion: %v, %v", ok, err)
	}
}

func TestPackageGCReconcilesCoexistingLayoutsAcrossRetryDespiteUnrelatedSignerReference(t *testing.T) {
	ctx := context.Background()
	dataRoot := t.TempDir()
	store, err := storage.NewSQLiteStore(dataRoot, "local")
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
	entry := plugins.MarketEntry{ID: manifest.ID, Version: manifest.Version, Compatibility: manifest.Compatibility, Runtime: plugins.RuntimeIndex{Kind: manifest.Runtime.Kind, ABI: manifest.Runtime.ABI, HostScope: manifest.Runtime.HostScope}, PackageSHA256: first.Package.Digest, SignatureKeyID: trust.KeyID, Provenance: "custom"}
	if err := store.PromoteSnapshot(ctx, source, marketplace.Snapshot{ID: "coexisting-current", SourceID: source.ID, Commit: "coexisting-commit", Path: snapshotPath, ValidatedAt: time.Now().UTC(), Entries: []plugins.MarketEntry{entry}}); err != nil {
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

func TestPendingGCRetriesRetiredSnapshotDirectoryWithoutTouchingCurrent(t *testing.T) {
	ctx := context.Background()
	dataRoot := t.TempDir()
	store, err := storage.NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	source, _ := marketplace.NewCustomSource("retired", "Retired", "https://example.com/plugins.git", "main", "", 0)
	oldPath := filepath.Join(dataRoot, "marketplace", "snapshots", source.ID, "old")
	currentPath := filepath.Join(dataRoot, "marketplace", "snapshots", source.ID, "current")
	for _, candidate := range []string{oldPath, currentPath} {
		if err := os.MkdirAll(candidate, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	if err := store.PromoteSnapshot(ctx, source, marketplace.Snapshot{ID: "old", SourceID: source.ID, Commit: "old", Path: oldPath, ValidatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.PromoteSnapshot(ctx, source, marketplace.Snapshot{ID: "current", SourceID: source.ID, Commit: "current", Path: currentPath, ValidatedAt: now.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	svc := NewMarketplaceService(store, nil, plugins.NewValidator(plugins.ValidatorOptions{}), filepath.Join(dataRoot, "plugins", "packages"))
	svc.removeAll = func(candidate string) error {
		if candidate == oldPath {
			return errors.New("injected retired snapshot failure")
		}
		return os.RemoveAll(candidate)
	}
	if err := svc.RunPendingGC(ctx); err == nil {
		t.Fatal("retired snapshot failure was hidden")
	}
	svc = NewMarketplaceService(store, nil, plugins.NewValidator(plugins.ValidatorOptions{}), filepath.Join(dataRoot, "plugins", "packages"))
	if err := svc.RunPendingGC(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(oldPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("retired snapshot remains: %v", err)
	}
	if _, err := os.Stat(currentPath); err != nil {
		t.Fatalf("current snapshot was removed: %v", err)
	}
}

func TestFailedRefreshAcquisitionCacheGCIsDurableAndRetryable(t *testing.T) {
	ctx := context.Background()
	dataRoot := t.TempDir()
	store, err := storage.NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	source, err := marketplaceTestSource("failed")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveMarketplaceSource(ctx, source); err != nil {
		t.Fatal(err)
	}
	trust, err := source.SignatureTrust()
	if err != nil {
		t.Fatal(err)
	}
	digest := strings.Repeat("d", 64)
	now := time.Now().UTC()
	refresh := marketplace.RefreshOperation{ID: "refresh-failed", SourceID: source.ID, Status: "running", StartedAt: now, LeaseToken: "lease-failed", LeaseExpiresAt: now.Add(time.Minute)}
	if err := store.AcquireRefreshLease(ctx, refresh); err != nil {
		t.Fatal(err)
	}
	if err := store.StagePackageAcquisition(ctx, source.ID, digest, "refresh-failed"); err != nil {
		t.Fatal(err)
	}
	cacheRoot := filepath.Join(dataRoot, "plugins", "packages")
	cleanupMarketplaceCache(t, cacheRoot)
	cachePath, err := marketplace.SignerCachePath(cacheRoot, digest, trust.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cachePath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := store.CompletePackageAcquisitions(ctx, source.ID, "refresh-failed", false); err != nil {
		t.Fatal(err)
	}
	svc := NewMarketplaceService(store, nil, plugins.NewValidator(plugins.ValidatorOptions{}), cacheRoot)
	svc.packageVariantGC = func(string, marketplace.PackageGCClaim) error { return errors.New("injected cache removal failure") }
	if err := svc.RunPendingGC(ctx); err == nil {
		t.Fatal("injected cache cleanup failure was hidden")
	}
	if intents, _ := store.ListPackageGCIntents(ctx); len(intents) != 1 {
		t.Fatalf("failed cache GC intent was lost: %+v", intents)
	}
	svc.packageVariantGC = marketplace.QuarantineAndDeleteVerifiedPackageVariant
	if err := svc.RunPendingGC(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cachePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("orphan cache remains after retry: %v", err)
	}
}

func TestOfficialPartialRefreshFailureCollectsSignerAwareCache(t *testing.T) {
	ctx := context.Background()
	dataRoot := t.TempDir()
	store, err := storage.NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	source := marketplace.OfficialSource()
	if err := store.SaveMarketplaceSource(ctx, source); err != nil {
		t.Fatal(err)
	}
	trust, err := source.SignatureTrust()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	refresh := marketplace.RefreshOperation{ID: "official-partial-refresh", SourceID: source.ID, Status: "running", StartedAt: now, LeaseToken: "official-partial-lease", LeaseExpiresAt: now.Add(time.Minute)}
	if err := store.AcquireRefreshLease(ctx, refresh); err != nil {
		t.Fatal(err)
	}
	digest := strings.Repeat("e", 64)
	if err := store.StagePackageAcquisition(ctx, source.ID, digest, refresh.ID); err != nil {
		t.Fatal(err)
	}
	cacheRoot := filepath.Join(dataRoot, "plugins", "packages")
	cleanupMarketplaceCache(t, cacheRoot)
	cachePath, err := marketplace.SignerCachePath(cacheRoot, digest, trust.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cachePath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cachePath, "first-package-cached"), []byte("partial refresh"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.CompletePackageAcquisitions(ctx, source.ID, refresh.ID, false); err != nil {
		t.Fatal(err)
	}
	intents, err := store.ListPackageGCIntents(ctx)
	if err != nil || len(intents) != 1 || intents[0].SignerFingerprint != trust.Fingerprint {
		t.Fatalf("official partial-refresh GC intent = %+v, %v", intents, err)
	}
	svc := NewMarketplaceService(store, nil, plugins.NewValidator(plugins.ValidatorOptions{}), cacheRoot)
	if err := svc.RunPendingGC(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cachePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("official signer-aware cache remains after failed refresh GC: %v", err)
	}
	if intents, err := store.ListPackageGCIntents(ctx); err != nil || len(intents) != 0 {
		t.Fatalf("official failed-refresh GC intent remained after deletion: %+v, %v", intents, err)
	}
}

func TestDeleteSourceUsesSealAwareFencedPackageGC(t *testing.T) {
	ctx := context.Background()
	dataRoot := t.TempDir()
	store, err := storage.NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	candidate := pluginCandidateFixture(t, "sealed.gc", "1.0.0", nil, plugins.CleanupPolicy{Instances: "delete", Config: "delete", OwnedData: "delete", Grants: "delete", SharedRefs: "retain", AuditEvents: "retain"})
	validator := pluginTestValidator()
	cacheRoot := filepath.Join(dataRoot, "plugins", "packages")
	cleanupMarketplaceCache(t, cacheRoot)
	cache, err := marketplace.NewVerifiedCache(cacheRoot, validator, store)
	if err != nil {
		t.Fatal(err)
	}
	source, err := marketplaceTestSource("sealed-gc")
	if err != nil {
		t.Fatal(err)
	}
	trust, err := source.SignatureTrust()
	if err != nil {
		t.Fatal(err)
	}
	sealedPath, err := cache.StoreWithTrust(candidate.Package, validator, trust)
	if err != nil {
		t.Fatal(err)
	}
	manifest := candidate.Package.Manifest
	entry := plugins.MarketEntry{ID: manifest.ID, Version: manifest.Version, Compatibility: manifest.Compatibility, Runtime: plugins.RuntimeIndex{Kind: manifest.Runtime.Kind, ABI: manifest.Runtime.ABI, HostScope: manifest.Runtime.HostScope}, Artifacts: []plugins.ArtifactIndex{{SHA256: manifest.Artifacts[0].SHA256, Size: manifest.Artifacts[0].Size}}, PackagePath: "plugins/sealed.gc/1.0.0", PackageSHA256: candidate.Package.Digest, SignatureKeyID: source.SignerKeyID, Provenance: "custom"}
	snapshotPath := filepath.Join(dataRoot, "marketplace", "snapshots", source.ID, "current")
	if err := os.MkdirAll(snapshotPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := store.PromoteSnapshot(ctx, source, marketplace.Snapshot{ID: "sealed-current", SourceID: source.ID, Commit: "sealed-commit", Path: snapshotPath, ValidatedAt: time.Now().UTC(), Entries: []plugins.MarketEntry{entry}}); err != nil {
		t.Fatal(err)
	}
	svc := NewMarketplaceService(store, nil, validator, cacheRoot)
	svc.packageVariantGC = func(string, marketplace.PackageGCClaim) error {
		return errors.New("injected signer variant GC interruption")
	}
	if err := svc.DeleteSource(ctx, source.ID); err == nil {
		t.Fatal("interrupted signer variant GC was reported as successful")
	}
	if intents, err := store.ListPackageGCIntents(ctx); err != nil || len(intents) != 1 || intents[0].SignerFingerprint != trust.Fingerprint {
		t.Fatalf("durable signer variant GC intent = %+v, %v", intents, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = storage.NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatal(err)
	}
	svc = NewMarketplaceService(store, nil, validator, cacheRoot)
	svc.packageVariantGC = func(string, marketplace.PackageGCClaim) error { return nil }
	if err := svc.RunPendingGC(ctx); err == nil {
		t.Fatal("signer variant GC completed durable metadata without physical deletion")
	}
	if intents, err := store.ListPackageGCIntents(ctx); err != nil || len(intents) != 1 {
		t.Fatalf("no-op signer variant GC lost durable intent: %+v, %v", intents, err)
	}
	if _, err := os.Stat(sealedPath); err != nil {
		t.Fatalf("no-op signer variant GC changed physical cache: %v", err)
	}
	svc = NewMarketplaceService(store, nil, validator, cacheRoot)
	if err := svc.RunPendingGC(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sealedPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("sealed package cache remains after source deletion: %v", err)
	}
}

func marketplaceTestSource(id string) (marketplace.Source, error) {
	return marketplace.NewSignedCustomSource(id, strings.ToUpper(id[:1])+id[1:], "https://example.com/"+id+".git", "main", "", 0, marketplace.SourceSigner{KeyID: "test-fixture", SecretRef: "vault-" + id, PublicKey: base64.StdEncoding.EncodeToString(pluginTestSigningKey().Public().(ed25519.PublicKey))})
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
	store, err := storage.NewSQLiteStore(dataRoot, "local")
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
