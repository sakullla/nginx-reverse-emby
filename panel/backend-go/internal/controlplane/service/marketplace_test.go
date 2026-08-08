package service

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/marketplace"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

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
	if _, err := newPluginTestService(store).Install(ctx, PluginInstallRequest{Package: resolved, ActorID: "admin", ConfirmedPermissions: []string{"http.inspect"}, RiskAccepted: true}); err == nil {
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
	install := storage.PluginInstallTransaction{Package: storage.PluginPackageRow{Identity: identity, Digest: protectedDigest, PluginID: "protected", Version: "1.0.0", SourceID: source.ID, SourceKind: source.Kind, SignatureKeyID: trust.KeyID, SignaturePublicKey: trust.PublicKey, SignatureFingerprint: trust.Fingerprint, CachePath: protectedPath, ManifestJSON: "{}", ConfigSchemaJSON: "{}", VerifiedAt: now}, Installed: storage.InstalledPluginRow{PluginID: "protected", ActivePackageDigest: protectedDigest, ActivePackageIdentity: identity, ActiveSourceID: source.ID, ActiveSourceKind: source.Kind, DesiredLifecycle: "disabled", CurrentLifecycle: "disabled", CleanupPolicyJSON: "{}", LastOperationID: "install", InstalledAt: now, UpdatedAt: now}, Operation: storage.PluginOperationRow{ID: "install", PluginID: "protected", Kind: "install", Status: "succeeded", TargetPackageDigest: protectedDigest, TargetPackageIdentity: identity, AgentResultsJSON: "{}", ActorID: "admin", SourceID: source.ID, SourceKind: source.Kind, CreatedAt: now}, Audit: storage.AuditEventRow{ID: "audit-install", Action: "plugin.install", TargetKind: "plugin", TargetID: "protected", Result: "success", MetadataJSON: "{}", CreatedAt: now}}
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
	source, _ := marketplace.NewCustomSource("failed", "Failed", "https://example.com/failed.git", "main", "", 0)
	if err := store.SaveMarketplaceSource(ctx, source); err != nil {
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
	cachePath := filepath.Join(cacheRoot, digest)
	if err := os.MkdirAll(cachePath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := store.CompletePackageAcquisitions(ctx, source.ID, "refresh-failed", false); err != nil {
		t.Fatal(err)
	}
	svc := NewMarketplaceService(store, nil, plugins.NewValidator(plugins.ValidatorOptions{}), cacheRoot)
	svc.packageGC = func(string, string, string) error { return errors.New("injected cache removal failure") }
	if err := svc.RunPendingGC(ctx); err == nil {
		t.Fatal("injected cache cleanup failure was hidden")
	}
	if intents, _ := store.ListPackageGCIntents(ctx); len(intents) != 1 {
		t.Fatalf("failed cache GC intent was lost: %+v", intents)
	}
	svc.packageGC = marketplace.QuarantineAndDeleteVerifiedPackage
	if err := svc.RunPendingGC(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cachePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("orphan cache remains after retry: %v", err)
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

func (s *invalidGCIntentStore) RecordPackageGCFailure(_ context.Context, _, _, failure string) error {
	s.failure = failure
	return nil
}

func pluginTestOtherDigest() string {
	return "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
}
