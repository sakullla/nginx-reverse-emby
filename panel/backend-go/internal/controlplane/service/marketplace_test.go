package service

import (
	"context"
	"errors"
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
	if err != nil || resolved.CachePath != candidate.CachePath || resolved.Package.Digest != candidate.Package.Digest {
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

func pluginTestOtherDigest() string {
	return "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
}
