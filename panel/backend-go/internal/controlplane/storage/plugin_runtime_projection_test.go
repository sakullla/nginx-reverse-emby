package storage

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/marketplace"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
	"gorm.io/gorm"
)

func TestPluginRuntimeProjectionPersistsArtifactsAndRejectsDigestMetadataConflict(t *testing.T) {
	ctx := context.Background()
	store, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	now := time.Now().UTC()
	digest := strings.Repeat("a", 64)
	manifest := runtimeProjectionManifest("runtime.plugin", strings.Repeat("b", 64))
	manifestJSON, _ := json.Marshal(manifest)
	row, artifacts, err := ProjectPluginPackage(PluginPackageRow{Digest: digest, PluginID: manifest.ID, Version: manifest.Version, CachePath: "packages/runtime", ManifestJSON: string(manifestJSON), ConfigSchemaJSON: `{}`, VerifiedAt: now}, manifest)
	if err != nil {
		t.Fatal(err)
	}
	installed := InstalledPluginRow{PluginID: manifest.ID, ActivePackageDigest: digest, RuntimeKind: manifest.Runtime.Kind, RuntimeABI: manifest.Runtime.ABI, HostScope: manifest.Runtime.HostScope, DesiredLifecycle: "disabled", CurrentLifecycle: "disabled", CleanupPolicyJSON: `{}`, LastOperationID: "install-runtime", StateVersion: 1, InstalledAt: now, UpdatedAt: now}
	transaction := PluginInstallTransaction{Package: row, Artifacts: artifacts, Installed: installed, Operation: PluginOperationRow{ID: "install-runtime", PluginID: manifest.ID, Kind: "install", Status: "succeeded", AgentResultsJSON: `{}`, ActorID: "admin", CreatedAt: now}, Audit: AuditEventRow{ID: "audit-runtime", Action: "plugin.install", TargetKind: "plugin", TargetID: manifest.ID, Result: "success", MetadataJSON: `{}`, CreatedAt: now}}
	if err := store.InstallPlugin(ctx, transaction); err != nil {
		t.Fatal(err)
	}

	stored, found, err := store.GetInstalledPlugin(ctx, manifest.ID)
	if err != nil || !found || stored.RuntimeKind != "wasm-policy" || stored.RuntimeABI != "nre:policy/v1" || stored.HostScope != "agent" {
		t.Fatalf("installed runtime projection = %+v, %v, %v", stored, found, err)
	}
	storedArtifacts, err := store.ListPluginArtifacts(ctx, digest)
	if err != nil || len(storedArtifacts) != 1 || storedArtifacts[0].Path != "artifacts/policy.wasm" || storedArtifacts[0].SHA256 != strings.Repeat("b", 64) {
		t.Fatalf("stored artifacts = %+v, %v", storedArtifacts, err)
	}

	conflictingManifest := manifest
	conflictingManifest.Artifacts[0].SHA256 = strings.Repeat("c", 64)
	conflictingRow, conflictingArtifacts, err := ProjectPluginPackage(row, conflictingManifest)
	if err != nil {
		t.Fatal(err)
	}
	err = store.writeTransaction(ctx, func(tx *gorm.DB) error { return ensurePluginPackageTx(tx, conflictingRow, conflictingArtifacts) })
	if !errors.Is(err, ErrPluginConflict) {
		t.Fatalf("conflicting artifact projection error = %v", err)
	}
	storedArtifacts, err = store.ListPluginArtifacts(ctx, digest)
	if err != nil || len(storedArtifacts) != 1 || storedArtifacts[0].SHA256 != strings.Repeat("b", 64) {
		t.Fatalf("conflicting artifact projection changed durable rows: %+v, %v", storedArtifacts, err)
	}
}

func TestPluginArtifactInstallFailureRollsBackPackageAndRuntimeProjection(t *testing.T) {
	ctx := context.Background()
	store, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	digest := strings.Repeat("d", 64)
	manifest := runtimeProjectionManifest("atomic.runtime", strings.Repeat("e", 64))
	manifestJSON, _ := json.Marshal(manifest)
	row, artifacts, err := ProjectPluginPackage(PluginPackageRow{Digest: digest, PluginID: manifest.ID, Version: manifest.Version, CachePath: "packages/atomic", ManifestJSON: string(manifestJSON), ConfigSchemaJSON: `{}`, VerifiedAt: now}, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.db.Exec(`CREATE TRIGGER fail_runtime_install BEFORE INSERT ON installed_plugins WHEN NEW.plugin_id = 'atomic.runtime' BEGIN SELECT RAISE(ABORT, 'injected runtime install failure'); END`).Error; err != nil {
		t.Fatal(err)
	}
	transaction := PluginInstallTransaction{Package: row, Artifacts: artifacts, Installed: InstalledPluginRow{PluginID: manifest.ID, ActivePackageDigest: digest, RuntimeKind: manifest.Runtime.Kind, RuntimeABI: manifest.Runtime.ABI, HostScope: manifest.Runtime.HostScope, DesiredLifecycle: "disabled", CurrentLifecycle: "disabled", CleanupPolicyJSON: `{}`, LastOperationID: "atomic-install", StateVersion: 1, InstalledAt: now, UpdatedAt: now}, Operation: PluginOperationRow{ID: "atomic-install", PluginID: manifest.ID, Kind: "install", Status: "succeeded", AgentResultsJSON: `{}`, ActorID: "admin", CreatedAt: now}, Audit: AuditEventRow{ID: "atomic-audit", Action: "plugin.install", TargetKind: "plugin", TargetID: manifest.ID, Result: "success", MetadataJSON: `{}`, CreatedAt: now}}
	if err := store.InstallPlugin(ctx, transaction); err == nil {
		t.Fatal("injected runtime install failure was ignored")
	}
	for model, name := range map[any]string{&PluginPackageRow{}: "package", &PluginArtifactRow{}: "artifact", &InstalledPluginRow{}: "installed", &PluginOperationRow{}: "operation"} {
		var count int64
		if err := store.db.Model(model).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("%s rows after rollback = %d, %v", name, count, err)
		}
	}
}

func TestMarketplaceRuntimeProjectionPersistsCurrentIndexFields(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	source := marketplace.OfficialSource()
	entry := plugins.MarketEntry{
		ID: "market.runtime", Version: "1.0.0", Compatibility: plugins.Compatibility{Host: "*", Agent: "*"},
		Runtime:     plugins.RuntimeIndex{Kind: "rpc-service", ABI: "nre:rpc/v1", HostScope: "control-plane"},
		Artifacts:   []plugins.ArtifactIndex{{SHA256: strings.Repeat("3", 64), Size: 128, GOOS: "linux", GOARCH: "amd64"}},
		PackagePath: "plugins/market.runtime/1.0.0", PackageSHA256: strings.Repeat("4", 64), SignatureKeyID: plugins.OfficialSignatureKeyID, Provenance: "sakullla-plugins", Official: true,
	}
	snapshot := marketplace.Snapshot{ID: "runtime-snapshot", SourceID: source.ID, Commit: strings.Repeat("6", 40), Path: "runtime-snapshot", ValidatedAt: time.Now().UTC(), Entries: []plugins.MarketEntry{entry}}
	if err := store.PromoteSnapshot(t.Context(), source, snapshot); err != nil {
		t.Fatal(err)
	}
	var row MarketEntryRow
	if err := store.db.Where("snapshot_id = ? AND plugin_id = ?", snapshot.ID, entry.ID).First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.RuntimeKind != entry.Runtime.Kind || row.RuntimeABI != entry.Runtime.ABI || row.HostScope != entry.Runtime.HostScope || row.SignatureKeyID != entry.SignatureKeyID || row.Provenance != entry.Provenance || !strings.Contains(row.ArtifactsJSON, entry.Artifacts[0].SHA256) {
		t.Fatalf("market runtime projection = %+v", row)
	}
	current, found, err := store.CurrentSnapshot(t.Context(), source.ID)
	if err != nil || !found || len(current.Entries) != 1 || current.Entries[0].Runtime != entry.Runtime {
		t.Fatalf("current runtime catalog = %+v, %v, %v", current, found, err)
	}
}

func TestPluginRuntimeMigrationBackfillsCurrentContractAndRebuildsLegacyDataOnlyState(t *testing.T) {
	t.Run("backfill installed identity from verified projection", func(t *testing.T) {
		store, err := NewSQLiteStore(t.TempDir(), "local")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.Close() })
		now := time.Now().UTC()
		digest := strings.Repeat("f", 64)
		manifest := runtimeProjectionManifest("migrated.runtime", strings.Repeat("1", 64))
		manifestJSON, _ := json.Marshal(manifest)
		seed := sha256.Sum256([]byte("runtime-projection-migration-signer"))
		publicKey := base64.StdEncoding.EncodeToString(ed25519.NewKeyFromSeed(seed[:]).Public().(ed25519.PublicKey))
		fingerprint, err := marketplace.SourceSignerFingerprint(publicKey)
		if err != nil {
			t.Fatal(err)
		}
		packageRow := PluginPackageRow{Digest: digest, PluginID: manifest.ID, Version: manifest.Version, SourceID: "runtime-projection-source", SourceKind: marketplace.SourceKindCustom, SourceRiskLabel: marketplace.UntrustedRiskLabel, SignatureKeyID: manifest.Signature.KeyID, SignaturePublicKey: publicKey, SignatureFingerprint: fingerprint, CachePath: "packages/migrated", ManifestJSON: string(manifestJSON), ConfigSchemaJSON: `{}`, VerifiedAt: now}
		packageRow.Identity = PluginPackageIdentity(digest, packageRow.SourceID, fingerprint)
		projected, artifacts, err := ProjectPluginPackage(packageRow, manifest)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.db.Create(&projected).Error; err != nil {
			t.Fatal(err)
		}
		if err := store.db.Create(&artifacts).Error; err != nil {
			t.Fatal(err)
		}
		if err := store.db.Create(&InstalledPluginRow{PluginID: manifest.ID, ActivePackageDigest: digest, DesiredLifecycle: "disabled", CurrentLifecycle: "disabled", CleanupPolicyJSON: `{}`, LastOperationID: "install", StateVersion: 1, InstalledAt: now, UpdatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
		if err := BootstrapSchema(t.Context(), store.db, SchemaOptionsForDriver("sqlite", true)); err != nil {
			t.Fatal(err)
		}
		installed, found, err := store.GetInstalledPlugin(t.Context(), manifest.ID)
		artifacts, artifactsErr := store.ListPluginArtifacts(t.Context(), digest)
		if err != nil || artifactsErr != nil || !found || installed.RuntimeABI != manifest.Runtime.ABI || len(artifacts) != 1 {
			t.Fatalf("migrated runtime = %+v artifacts=%+v found=%v errors=%v/%v", installed, artifacts, found, err, artifactsErr)
		}
	})

	t.Run("controlled rebuild legacy data-only records", func(t *testing.T) {
		store, err := NewSQLiteStore(t.TempDir(), "local")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.Close() })
		now := time.Now().UTC()
		digest := strings.Repeat("2", 64)
		rows := []any{
			&MarketplaceSourceRow{ID: "legacy", Kind: "custom", Purpose: "market", Name: "Legacy", URL: "https://example.com/legacy.git", RefKind: "branch", RefName: "main", ConfigRevision: 1, CurrentSnapshotID: "legacy-snapshot", LastError: "", UpdatedAt: now},
			&MarketSnapshotRow{ID: "legacy-snapshot", SourceID: "legacy", Commit: "legacy", Path: "legacy", EntriesJSON: `[{"id":"legacy.plugin","version":"1.0.0"}]`, ValidatedAt: now},
			&PluginPackageRow{Digest: digest, PluginID: "legacy.plugin", Version: "1.0.0", CachePath: "packages/legacy", ManifestJSON: `{"id":"legacy.plugin","version":"1.0.0"}`, ConfigSchemaJSON: `{}`, VerifiedAt: now},
			&InstalledPluginRow{PluginID: "legacy.plugin", ActivePackageDigest: digest, DesiredLifecycle: "disabled", CurrentLifecycle: "disabled", CleanupPolicyJSON: `{}`, LastOperationID: "legacy-install", StateVersion: 1, InstalledAt: now, UpdatedAt: now},
			&PluginOperationRow{ID: "legacy-install", PluginID: "legacy.plugin", Kind: "install", Status: "succeeded", AgentResultsJSON: `{}`, ActorID: "admin", CreatedAt: now},
			&AuditEventRow{ID: "legacy-audit", Action: "plugin.install", TargetKind: "plugin", TargetID: "legacy.plugin", Result: "success", MetadataJSON: `{}`, CreatedAt: now},
		}
		for _, row := range rows {
			if err := store.db.Create(row).Error; err != nil {
				t.Fatal(err)
			}
		}
		if err := BootstrapSchema(t.Context(), store.db, SchemaOptionsForDriver("sqlite", true)); err != nil {
			t.Fatal(err)
		}
		if _, found, err := store.GetInstalledPlugin(t.Context(), "legacy.plugin"); err != nil || found {
			t.Fatalf("legacy installed record survived controlled rebuild: %v, %v", found, err)
		}
		var source MarketplaceSourceRow
		if err := store.db.Where("id = ?", "legacy").First(&source).Error; err != nil || source.CurrentSnapshotID != "" || source.LastResult != "rebuild_required" {
			t.Fatalf("legacy source rebuild state = %+v, %v", source, err)
		}
		for model, name := range map[any]string{&PluginOperationRow{}: "operation", &AuditEventRow{}: "audit"} {
			var count int64
			if err := store.db.Model(model).Count(&count).Error; err != nil || count != 1 {
				t.Fatalf("historical %s rows after rebuild = %d, %v", name, count, err)
			}
		}
	})
}

func runtimeProjectionManifest(id, artifactDigest string) plugins.Manifest {
	return plugins.Manifest{
		SchemaVersion: 1, ID: id, Version: "1.0.0", Name: id,
		Runtime:        plugins.Runtime{Kind: "wasm-policy", ABI: "nre:policy/v1", HostScope: "agent", Entry: "artifacts/policy.wasm", PolicyKind: "waf"},
		Artifacts:      []plugins.Artifact{{Path: "artifacts/policy.wasm", SHA256: artifactDigest, Size: 12, Mode: "wasm"}},
		ResourceBudget: plugins.ResourceBudget{TimeoutMS: 10, MemoryBytes: 65536, Concurrency: 2, InputBytes: 1024, OutputBytes: 1024},
		FailurePolicy:  plugins.FailurePolicy{OnError: "fail-closed", OnBudget: "fail-closed", Restart: "never", CoreFallback: "preserve"},
		Signature:      plugins.Signature{Algorithm: "ed25519", KeyID: "test-key", File: "package.sig"},
	}
}
