package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
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
	source := marketplace.OfficialSource()
	entry := plugins.MarketEntry{ID: candidate.Package.Manifest.ID, Version: candidate.Package.Manifest.Version, Compatibility: candidate.Package.Manifest.Compatibility, PackagePath: "plugins/official.resolve/1.0.0", PackageSHA256: candidate.Package.Digest, Official: true}
	if err := store.PromoteSnapshot(ctx, source, marketplace.Snapshot{ID: "snapshot-current", SourceID: source.ID, Commit: "commit", Path: "snapshot", ValidatedAt: time.Now().UTC(), Entries: []plugins.MarketEntry{entry}}); err != nil {
		t.Fatal(err)
	}
	validator := plugins.NewValidator(plugins.ValidatorOptions{})
	catalog := NewMarketplaceService(store, nil, validator, filepath.Dir(candidate.CachePath))
	current, err := catalog.CurrentCatalog(ctx, source.ID)
	if err != nil || current.Source.Kind != marketplace.SourceKindOfficial || current.Snapshot.Commit != "commit" || len(current.Snapshot.Entries) != 1 {
		t.Fatalf("current market catalog = %+v, %v", current, err)
	}
	resolved, err := catalog.ResolvePackage(ctx, source.ID, entry.ID, entry.Version, entry.PackageSHA256)
	if err != nil || resolved.CachePath != candidate.CachePath || resolved.Package.Digest != candidate.Package.Digest || resolved.sourceID != marketplace.OfficialSourceID || resolved.sourceKind != marketplace.SourceKindOfficial {
		t.Fatalf("resolved package = %+v, %v", resolved, err)
	}
	if _, err := catalog.ResolvePackage(ctx, source.ID, entry.ID, entry.Version, pluginTestOtherDigest()); !errors.Is(err, ErrMarketplaceEntryNotFound) {
		t.Fatalf("non-current digest resolution error = %v", err)
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
	if _, err := svc.AddCustomSource(ctx, "bad", "Bad", "https://example.com/plugins.git?token=plaintext", "main", "", 0); err == nil {
		t.Fatal("unsafe source URL was accepted")
	}
	if _, err := svc.AddCustomSource(ctx, "private", "Private", "https://example.com/plugins.git", "main", "secret-ref", 0); err == nil {
		t.Fatal("credential source without trusted authorization was accepted")
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

func TestDeleteMarketplaceSourceCleansSnapshotsAndOnlyUnreferencedCache(t *testing.T) {
	ctx := context.Background()
	dataRoot := t.TempDir()
	store, err := storage.NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	cacheRoot := filepath.Join(dataRoot, "plugins", "packages")
	snapshotPath := filepath.Join(dataRoot, "marketplace", "snapshots", "community", "snapshot")
	protectedDigest, garbageDigest, sharedDigest := strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64)
	for _, path := range []string{filepath.Join(cacheRoot, protectedDigest), filepath.Join(cacheRoot, garbageDigest), filepath.Join(cacheRoot, sharedDigest), snapshotPath} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "fixture"), []byte("data"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	source, _ := marketplace.NewCustomSource("community", "Community", "https://example.com/plugins.git", "main", "", 0)
	snapshot := marketplace.Snapshot{ID: "snapshot", SourceID: source.ID, Commit: "commit", Path: snapshotPath, ValidatedAt: time.Now().UTC(), Entries: []plugins.MarketEntry{{ID: "protected", Version: "1.0.0", PackageSHA256: protectedDigest}, {ID: "garbage", Version: "1.0.0", PackageSHA256: garbageDigest}, {ID: "shared", Version: "1.0.0", PackageSHA256: sharedDigest}}}
	if err := store.PromoteSnapshot(ctx, source, snapshot); err != nil {
		t.Fatal(err)
	}
	other, _ := marketplace.NewCustomSource("other", "Other", "https://example.com/other.git", "main", "", 0)
	otherSnapshot := marketplace.Snapshot{ID: "other-snapshot", SourceID: other.ID, Commit: "other-commit", Path: filepath.Join(dataRoot, "marketplace", "snapshots", "other", "snapshot"), ValidatedAt: time.Now().UTC(), Entries: []plugins.MarketEntry{{ID: "shared", Version: "1.0.0", PackageSHA256: sharedDigest}}}
	if err := store.PromoteSnapshot(ctx, other, otherSnapshot); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	install := storage.PluginInstallTransaction{Package: storage.PluginPackageRow{Digest: protectedDigest, PluginID: "protected", Version: "1.0.0", CachePath: filepath.Join(cacheRoot, protectedDigest), ManifestJSON: "{}", ConfigSchemaJSON: "{}", VerifiedAt: now}, Installed: storage.InstalledPluginRow{PluginID: "protected", ActivePackageDigest: protectedDigest, DesiredLifecycle: "disabled", CurrentLifecycle: "disabled", CleanupPolicyJSON: "{}", LastOperationID: "install", InstalledAt: now, UpdatedAt: now}, Operation: storage.PluginOperationRow{ID: "install", PluginID: "protected", Kind: "install", Status: "succeeded", AgentResultsJSON: "{}", ActorID: "admin", CreatedAt: now}, Audit: storage.AuditEventRow{ID: "audit-install", Action: "plugin.install", TargetKind: "plugin", TargetID: "protected", Result: "success", MetadataJSON: "{}", CreatedAt: now}}
	if err := store.InstallPlugin(ctx, install); err != nil {
		t.Fatal(err)
	}
	svc := NewMarketplaceService(store, nil, plugins.NewValidator(plugins.ValidatorOptions{}), cacheRoot)
	if err := svc.DeleteSource(ctx, source.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.GetMarketplaceSource(ctx, source.ID); err != nil || ok {
		t.Fatalf("source remains: %v, %v", ok, err)
	}
	if _, err := os.Stat(snapshotPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("snapshot directory remains: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cacheRoot, garbageDigest)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unreferenced cache remains: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cacheRoot, protectedDigest)); err != nil {
		t.Fatalf("installed cache was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cacheRoot, sharedDigest)); err != nil {
		t.Fatalf("cache referenced by another catalog was removed: %v", err)
	}
}

func pluginTestOtherDigest() string {
	return "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
}
