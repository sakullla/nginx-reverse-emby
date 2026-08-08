package storage

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/marketplace"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/pkg/pluginsdk/compatfixture"
	"gorm.io/gorm"
)

func writeMigrationVerifiedPackage(t *testing.T, root, pluginID string, signingKey ed25519.PrivateKey) string {
	t.Helper()
	artifact := compatfixture.PolicyV1GuestWASM()
	artifactDigest := sha256.Sum256(artifact)
	manifest := fmt.Sprintf(`schema_version: 1
id: %s
version: 1.0.0
name: Migration Fixture
compatibility: {host: "*", agent: "*"}
runtime: {kind: wasm-policy, abi: "nre:policy/v1", host_scope: agent, entry: artifacts/policy.wasm}
artifacts:
  - {path: artifacts/policy.wasm, sha256: %x, size: %d, mode: wasm}
extension_points: [http.request]
permissions: [http.inspect]
config_schema: config.schema.json
resource_budget: {timeout_ms: 10, memory_bytes: 1048576, concurrency: 1, input_bytes: 65536, output_bytes: 65536}
failure_policy: {on_error: fail-open, on_budget: fail-open, restart: never, core_fallback: preserve}
signature: {algorithm: ed25519, key_id: community-release, file: package.sig}
cleanup: {instances: retain, config: retain, owned_data: retain, grants: retain, shared_refs: retain, audit_events: retain}
`, pluginID, artifactDigest, len(artifact))
	files := map[string][]byte{
		plugins.PackageManifestFile: []byte(manifest),
		plugins.ConfigSchemaFile:    []byte(`{"type":"object"}`),
		"artifacts/policy.wasm":     artifact,
	}
	for name, value := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, value, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	digest, err := plugins.ComputePackageDigest(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, plugins.PackageDigestFile), []byte(digest+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, plugins.PackageSignatureFile), []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(signingKey, []byte(digest)))), 0o600); err != nil {
		t.Fatal(err)
	}
	return digest
}

func TestPluginMigrationRejectsEveryDanglingPackageReferenceBeforeTargetWrites(t *testing.T) {
	digest := strings.Repeat("d", 64)
	tests := []struct {
		name   string
		insert func(*testing.T, *GormStore)
	}{
		{name: "active", insert: func(t *testing.T, source *GormStore) {
			row := danglingMigrationInstalledPlugin("dangling-active", digest)
			row.ActivePackageDigest = digest
			row.ActiveSourceID = "missing-source"
			if err := source.db.Create(&row).Error; err != nil {
				t.Fatal(err)
			}
		}},
		{name: "staged", insert: func(t *testing.T, source *GormStore) {
			row := danglingMigrationInstalledPlugin("dangling-staged", digest)
			row.StagedPackageDigest = digest
			row.StagedSourceID = "missing-source"
			if err := source.db.Create(&row).Error; err != nil {
				t.Fatal(err)
			}
		}},
		{name: "rollback", insert: func(t *testing.T, source *GormStore) {
			row := danglingMigrationInstalledPlugin("dangling-rollback", digest)
			row.RollbackPackageDigest = digest
			row.RollbackSourceID = "missing-source"
			if err := source.db.Create(&row).Error; err != nil {
				t.Fatal(err)
			}
		}},
		{name: "pending", insert: func(t *testing.T, source *GormStore) {
			row := danglingMigrationInstalledPlugin("dangling-pending", digest)
			row.PendingTargetDigest = digest
			row.ActiveSourceID = "missing-source"
			if err := source.db.Create(&row).Error; err != nil {
				t.Fatal(err)
			}
		}},
		{name: "grant", insert: func(t *testing.T, source *GormStore) {
			if err := source.db.Create(&PluginGrantRow{ID: "dangling-grant", PluginID: "dangling-grant", PackageDigest: digest, Permission: "http.inspect", GrantedBy: "admin", GrantedAt: time.Now().UTC()}).Error; err != nil {
				t.Fatal(err)
			}
		}},
		{name: "operation", insert: func(t *testing.T, source *GormStore) {
			if err := source.db.Create(&PluginOperationRow{ID: "dangling-operation", PluginID: "dangling-operation", Kind: "install", Status: "failed", TargetPackageDigest: digest, SourceID: "missing-source", AgentResultsJSON: `{}`, CreatedAt: time.Now().UTC()}).Error; err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source, err := NewSQLiteStore(t.TempDir(), "local")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = source.Close() })
			target, err := NewSQLiteStore(t.TempDir(), "local")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = target.Close() })
			test.insert(t, source)

			var gcIntents int64
			if err := source.db.Model(&PluginCacheGCIntentRow{}).Count(&gcIntents).Error; err != nil || gcIntents != 0 {
				t.Fatalf("source GC intents = %d, %v", gcIntents, err)
			}
			if err := CopyDefaultMigrationRows(t.Context(), source, target); err == nil || !strings.Contains(err.Error(), "no package candidate") {
				t.Fatalf("dangling %s migration error = %v", test.name, err)
			}
			for _, model := range []any{&PluginPackageRow{}, &PluginArtifactRow{}, &InstalledPluginRow{}, &PluginGrantRow{}, &PluginOperationRow{}} {
				var count int64
				if err := target.db.Model(model).Count(&count).Error; err != nil || count != 0 {
					t.Fatalf("target %T count after failed migration = %d, %v", model, count, err)
				}
			}
		})
	}
}

func danglingMigrationInstalledPlugin(pluginID, digest string) InstalledPluginRow {
	now := time.Now().UTC()
	return InstalledPluginRow{PluginID: pluginID, DesiredLifecycle: "disabled", CurrentLifecycle: "disabled", CleanupPolicyJSON: `{}`, LastOperationID: "dangling", StateVersion: 1, InstalledAt: now, UpdatedAt: now}
}

type migrationRollbackPackage struct {
	row   PluginPackageRow
	trust marketplace.SignatureTrust
}

func addMigrationRollbackPackage(t *testing.T, source *GormStore, pluginID string, signingKey ed25519.PrivateKey) migrationRollbackPackage {
	t.Helper()
	staging := t.TempDir()
	digest := writeMigrationVerifiedPackage(t, staging, pluginID, signingKey)
	publicKey := base64.StdEncoding.EncodeToString(signingKey.Public().(ed25519.PublicKey))
	fingerprint, err := marketplace.SourceSignerFingerprint(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	trust := marketplace.SignatureTrust{SourceID: "migration-rollback-source", SourceKind: marketplace.SourceKindCustom, KeyID: "community-release", PublicKey: publicKey, Fingerprint: fingerprint}
	cachePath, err := marketplace.SignerCachePath(filepath.Join(source.dataRoot, "plugins", "packages"), digest, fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(staging, cachePath); err != nil {
		t.Fatal(err)
	}
	row := PluginPackageRow{Identity: PluginPackageIdentity(digest, trust.SourceID, trust.Fingerprint), Digest: digest, PluginID: pluginID, Version: "1.0.0", SourceID: trust.SourceID, SourceKind: trust.SourceKind, SourceRiskLabel: marketplace.UntrustedRiskLabel, SignatureKeyID: trust.KeyID, SignaturePublicKey: trust.PublicKey, SignatureFingerprint: trust.Fingerprint, CachePath: cachePath, ManifestJSON: `{}`, ConfigSchemaJSON: `{}`, VerifiedAt: time.Now().UTC()}
	if err := source.db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	return migrationRollbackPackage{row: row, trust: trust}
}

func TestPluginVariantLifecycleReferencesControlGCAndMigration(t *testing.T) {
	tests := []struct {
		name           string
		addReference   func(*testing.T, *GormStore, PluginPackageRow)
		wantGCClaim    bool
		verifyMigrated func(*testing.T, *GormStore, PluginPackageRow)
	}{
		{
			name: "pending target remains live",
			addReference: func(t *testing.T, source *GormStore, row PluginPackageRow) {
				now := time.Now().UTC()
				installed := InstalledPluginRow{
					PluginID: row.PluginID, ActiveSourceID: row.SourceID,
					PendingOperationID: "pending-upgrade", PendingKind: "upgrade",
					PendingTargetDigest: row.Digest, PendingTargetIdentity: row.Identity,
					DesiredLifecycle: "disabled", CurrentLifecycle: "upgrading", CleanupPolicyJSON: `{}`,
					LastOperationID: "pending-upgrade", StateVersion: 1, InstalledAt: now, UpdatedAt: now,
				}
				if err := source.db.Create(&installed).Error; err != nil {
					t.Fatal(err)
				}
			},
			verifyMigrated: func(t *testing.T, target *GormStore, row PluginPackageRow) {
				installed, ok, err := target.GetInstalledPlugin(t.Context(), row.PluginID)
				if err != nil || !ok || installed.PendingTargetIdentity != row.Identity {
					t.Fatalf("migrated pending target = %+v, %v, %v", installed, ok, err)
				}
			},
		},
		{
			name: "retained grant remains live authorization",
			addReference: func(t *testing.T, source *GormStore, row PluginPackageRow) {
				grant := PluginGrantRow{ID: "retained-grant", GrantKey: "retained-grant-key", PluginID: row.PluginID, PackageDigest: row.Digest, PackageIdentity: row.Identity, Permission: "http.inspect", GrantedBy: "admin", GrantedAt: time.Now().UTC()}
				if err := source.db.Create(&grant).Error; err != nil {
					t.Fatal(err)
				}
			},
			verifyMigrated: func(t *testing.T, target *GormStore, row PluginPackageRow) {
				var grant PluginGrantRow
				if err := target.db.Where("id = ?", "retained-grant").First(&grant).Error; err != nil || grant.PackageIdentity != row.Identity {
					t.Fatalf("migrated retained grant = %+v, %v", grant, err)
				}
			},
		},
		{
			name:        "completed operation detaches as immutable history",
			wantGCClaim: true,
			addReference: func(t *testing.T, source *GormStore, row PluginPackageRow) {
				now := time.Now().UTC()
				operation := PluginOperationRow{
					ID: "completed-install", PluginID: row.PluginID, Kind: "install", Status: "succeeded",
					TargetPackageDigest: row.Digest, TargetPackageIdentity: row.Identity,
					AgentResultsJSON: `{}`, ActorID: "admin", SourceID: row.SourceID, SourceKind: row.SourceKind,
					SourceRiskLabel: row.SourceRiskLabel, CreatedAt: now, CompletedAt: &now,
				}
				if err := source.db.Create(&operation).Error; err != nil {
					t.Fatal(err)
				}
			},
			verifyMigrated: func(t *testing.T, target *GormStore, row PluginPackageRow) {
				if _, ok, err := target.GetPluginPackageByIdentity(t.Context(), row.Identity); err != nil || ok {
					t.Fatalf("detached completed operation pinned package = %v, %v", ok, err)
				}
				var operation PluginOperationRow
				if err := target.db.Where("id = ?", "completed-install").First(&operation).Error; err != nil || operation.TargetPackageIdentity != row.Identity || operation.SourceID != row.SourceID {
					t.Fatalf("migrated completed operation provenance = %+v, %v", operation, err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sourceRoot := t.TempDir()
			source, err := NewSQLiteStore(sourceRoot, "local")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = source.Close() })
			target, err := NewSQLiteStore(t.TempDir(), "local")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = target.Close() })
			t.Cleanup(func() {
				_ = marketplace.DiscardVerifiedCacheRoot(filepath.Join(target.dataRoot, "plugins", "packages"))
			})

			seed := sha256.Sum256([]byte("variant-reference-" + test.name))
			item := addMigrationRollbackPackage(t, source, "reference."+strings.ReplaceAll(test.name, " ", "."), ed25519.NewKeyFromSeed(seed[:]))
			test.addReference(t, source, item.row)
			now := time.Now().UTC()
			if err := source.writeTransaction(t.Context(), func(tx *gorm.DB) error {
				return schedulePackageGCTx(tx, item.row.SourceID, item.row.Digest, item.row.SignatureFingerprint, now)
			}); err != nil {
				t.Fatal(err)
			}
			claim, claimed, err := source.ClaimPackageGC(t.Context(), item.row.SourceID, item.row.Digest, item.row.SignatureFingerprint)
			if err != nil || claimed != test.wantGCClaim {
				t.Fatalf("GC claim = %+v, %v, %v; want claimed %v", claim, claimed, err, test.wantGCClaim)
			}
			if claimed {
				if err := source.CompletePackageGC(t.Context(), claim, ""); err != nil {
					t.Fatal(err)
				}
				if err := source.Close(); err != nil {
					t.Fatal(err)
				}
				source, err = NewSQLiteStore(sourceRoot, "local")
				if err != nil {
					t.Fatalf("reopen with detached completed operation: %v", err)
				}
			} else {
				var intent PluginCacheGCIntentRow
				if err := source.db.Where("source_id = ? AND digest = ? AND signer_fingerprint = ?", item.row.SourceID, item.row.Digest, item.row.SignatureFingerprint).First(&intent).Error; err != nil || !intent.Deferred {
					t.Fatalf("live reference GC intent = %+v, %v", intent, err)
				}
			}
			if err := CopyDefaultMigrationRows(t.Context(), source, target); err != nil {
				t.Fatal(err)
			}
			if !claimed {
				migrated, ok, err := target.GetPluginPackageByIdentity(t.Context(), item.row.Identity)
				if err != nil || !ok || migrated.SourceID != item.row.SourceID || migrated.SignatureFingerprint != item.row.SignatureFingerprint {
					t.Fatalf("migrated live package = %+v, %v, %v", migrated, ok, err)
				}
			}
			test.verifyMigrated(t, target, item.row)
		})
	}
}

func TestPluginMigrationRejectsCrossSourceIdentityDigestFastPathWithoutTargetWrites(t *testing.T) {
	source, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = source.Close() })
	target, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = target.Close() })

	digest := pluginTestDigest("cross-source-same-digest")
	fingerprintA := pluginTestDigest("cross-source-signer-a")
	fingerprintB := pluginTestDigest("cross-source-signer-b")
	identityA := PluginPackageIdentity(digest, "source-a", fingerprintA)
	identityB := PluginPackageIdentity(digest, "source-b", fingerprintB)
	now := time.Now().UTC()
	for _, row := range []PluginPackageRow{
		{Identity: identityA, Digest: digest, PluginID: "cross.source", Version: "1.0.0", SourceID: "source-a", SourceKind: marketplace.SourceKindCustom, SourceRiskLabel: marketplace.UntrustedRiskLabel, SignatureFingerprint: fingerprintA, CachePath: "packages/a", ManifestJSON: `{}`, ConfigSchemaJSON: `{}`, VerifiedAt: now},
		{Identity: identityB, Digest: digest, PluginID: "cross.source", Version: "1.0.0", SourceID: "source-b", SourceKind: marketplace.SourceKindCustom, SourceRiskLabel: marketplace.UntrustedRiskLabel, SignatureFingerprint: fingerprintB, CachePath: "packages/b", ManifestJSON: `{}`, ConfigSchemaJSON: `{}`, VerifiedAt: now},
	} {
		if err := source.db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	completed := now.Add(time.Second)
	operation := PluginOperationRow{
		ID: "cross-source-operation", PluginID: "cross.source", Kind: "install", Status: "succeeded",
		TargetPackageDigest: digest, TargetPackageIdentity: identityA, AgentResultsJSON: `{}`, ActorID: "admin",
		SourceID: "source-b", SourceKind: marketplace.SourceKindCustom, SourceRiskLabel: marketplace.UntrustedRiskLabel,
		CreatedAt: now, CompletedAt: &completed,
	}
	if err := source.db.Create(&operation).Error; err != nil {
		t.Fatal(err)
	}
	sentinel := AuditEventRow{ID: "target-sentinel", Action: "migration.sentinel", TargetKind: "test", TargetID: "sentinel", Result: "success", MetadataJSON: `{}`, CreatedAt: now}
	if err := target.db.Create(&sentinel).Error; err != nil {
		t.Fatal(err)
	}

	err = CopyDefaultMigrationRows(t.Context(), source, target)
	if err == nil || !strings.Contains(err.Error(), "belongs to source") {
		t.Fatalf("cross-source migration error = %v", err)
	}
	for _, model := range []any{&PluginPackageRow{}, &PluginOperationRow{}} {
		var count int64
		if err := target.db.Model(model).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("target %T count after rejected source mismatch = %d, %v", model, count, err)
		}
	}
	var sentinelCount int64
	if err := target.db.Model(&AuditEventRow{}).Where("id = ?", sentinel.ID).Count(&sentinelCount).Error; err != nil || sentinelCount != 1 {
		t.Fatalf("target sentinel after rejected migration = %d, %v", sentinelCount, err)
	}
}

func registerMigrationCreateFailure(t *testing.T, target *GormStore, table string) {
	t.Helper()
	if err := target.ensureSQLiteWriteDB(); err != nil {
		t.Fatal(err)
	}
	callbackName := "test:migration-failure:" + strings.ReplaceAll(table, "_", "-")
	if err := target.writeDB.Callback().Create().Before("gorm:create").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement != nil && tx.Statement.Table == table {
			tx.AddError(errors.New("injected migration create failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = target.writeDB.Callback().Create().Remove(callbackName) })
}

func TestPluginMigrationRollsBackNewCacheAfterLaterDatabaseFailure(t *testing.T) {
	source, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = source.Close() })
	target, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = target.Close() })
	targetRoot := filepath.Join(target.dataRoot, "plugins", "packages")
	t.Cleanup(func() { _ = marketplace.DiscardVerifiedCacheRoot(targetRoot) })

	signingKey := ed25519.NewKeyFromSeed([]byte("storage-migration-signing-seed!!"))
	item := addMigrationRollbackPackage(t, source, "rollback.database.failure", signingKey)
	installed := danglingMigrationInstalledPlugin(item.row.PluginID, item.row.Digest)
	installed.ActivePackageDigest = item.row.Digest
	installed.ActivePackageIdentity = item.row.Identity
	installed.ActiveSourceID = item.row.SourceID
	if err := source.db.Create(&installed).Error; err != nil {
		t.Fatal(err)
	}
	registerMigrationCreateFailure(t, target, (InstalledPluginRow{}).TableName())

	migrationErr := CopyDefaultMigrationRows(t.Context(), source, target)
	if migrationErr == nil || !strings.Contains(migrationErr.Error(), "injected migration create failure") {
		t.Fatalf("migration DB failure = %v", migrationErr)
	}
	targetPath, err := marketplace.SignerCachePath(targetRoot, item.row.Digest, item.row.SignatureFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(targetPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("new cache survived failed migration: %v (migration error: %v)", err, migrationErr)
	}
	for _, model := range []any{&PluginPackageRow{}, &PluginArtifactRow{}, &InstalledPluginRow{}} {
		var count int64
		if err := target.db.Model(model).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("target %T count after DB rollback = %d, %v", model, count, err)
		}
	}
}

func TestPluginMigrationRollsBackPreparedObjectsOnIntentDatabaseFailure(t *testing.T) {
	fixture := newPreparedGCMigrationFixture(t)
	preexisting := fixture.objects[0]
	preexistingSource := filepath.Join(fixture.sourceRoot, filepath.FromSlash(preexisting.Path))
	preexistingTarget := filepath.Join(fixture.targetRoot, filepath.FromSlash(preexisting.Path))
	created, err := copyGCQuarantineDirectory(preexistingSource, preexistingTarget, fixture.claim.Digest)
	if err != nil || !created {
		t.Fatalf("prepare preexisting target object = %v, %v", created, err)
	}
	registerMigrationCreateFailure(t, fixture.target, (PluginCacheGCIntentRow{}).TableName())

	if err := CopyDefaultMigrationRows(t.Context(), fixture.source, fixture.target); err == nil || !strings.Contains(err.Error(), "injected migration create failure") {
		t.Fatalf("prepared-object DB failure = %v", err)
	}
	if computed, err := plugins.ComputePackageDigest(preexistingTarget); err != nil || computed != fixture.claim.Digest {
		t.Fatalf("preexisting prepared object after rollback = %q, %v", computed, err)
	}
	for _, object := range fixture.objects[1:] {
		for _, relative := range []string{object.Path, object.QuarantinePath} {
			if _, err := os.Lstat(filepath.Join(fixture.targetRoot, filepath.FromSlash(relative))); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("new prepared object %q survived rollback: %v", relative, err)
			}
		}
	}
	var intents int64
	if err := fixture.target.db.Model(&PluginCacheGCIntentRow{}).Count(&intents).Error; err != nil || intents != 0 {
		t.Fatalf("target GC intents after rollback = %d, %v", intents, err)
	}
}

func TestPluginMigrationBatchFailureRollsBackOnlyNewCacheObjects(t *testing.T) {
	source, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = source.Close() })
	target, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = target.Close() })
	targetRoot := filepath.Join(target.dataRoot, "plugins", "packages")
	t.Cleanup(func() { _ = marketplace.DiscardVerifiedCacheRoot(targetRoot) })

	signingKey := ed25519.NewKeyFromSeed([]byte("storage-migration-signing-seed!!"))
	items := []migrationRollbackPackage{
		addMigrationRollbackPackage(t, source, "rollback.batch.one", signingKey),
		addMigrationRollbackPackage(t, source, "rollback.batch.two", signingKey),
		addMigrationRollbackPackage(t, source, "rollback.batch.three", signingKey),
	}
	sort.Slice(items, func(i, j int) bool { return items[i].row.Digest < items[j].row.Digest })
	if _, _, err := importVerifiedPackageDirectory(items[0].row.CachePath, targetRoot, items[0].row.Digest, items[0].trust, nil); err != nil {
		t.Fatalf("prepopulate sealed target cache: %v", err)
	}
	if err := os.Chmod(items[2].row.CachePath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(items[2].row.CachePath, plugins.PackageSignatureFile), []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}

	migrationErr := CopyDefaultMigrationRows(t.Context(), source, target)
	if migrationErr == nil {
		t.Fatal("mid-batch package validation failure = nil")
	}
	for index, item := range items {
		path, err := marketplace.SignerCachePath(targetRoot, item.row.Digest, item.row.SignatureFingerprint)
		if err != nil {
			t.Fatal(err)
		}
		_, statErr := os.Lstat(path)
		if index == 0 {
			if statErr != nil {
				t.Fatalf("preexisting sealed cache was removed: %v", statErr)
			}
			continue
		}
		if !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("batch cache %d survived failed migration: %v (migration error: %v)", index, statErr, migrationErr)
		}
	}
	var packages int64
	if err := target.db.Model(&PluginPackageRow{}).Count(&packages).Error; err != nil || packages != 0 {
		t.Fatalf("target package rows after batch rollback = %d, %v", packages, err)
	}
}

func TestMarketplaceSourcePersistsBoundSignerIdentity(t *testing.T) {
	ctx := context.Background()
	dataRoot := t.TempDir()
	store, err := NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	source, err := marketplace.NewSignedCustomSource("signed", "Signed", "https://example.com/signed.git", "main", "", 0, marketplace.SourceSigner{
		KeyID: "community-release", SecretRef: "vault-signer-ref", PublicKey: base64.StdEncoding.EncodeToString(make([]byte, 32)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveMarketplaceSource(ctx, source); err != nil {
		t.Fatal(err)
	}
	loaded, ok, err := store.GetMarketplaceSource(ctx, source.ID)
	if err != nil || !ok {
		t.Fatalf("load signed source = (%+v, %v, %v)", loaded, ok, err)
	}
	if loaded.SignerKeyID != source.SignerKeyID || loaded.SignerSecretRef != source.SignerSecretRef || loaded.SignerPublicKey != source.SignerPublicKey || loaded.SignerFingerprint != source.SignerFingerprint {
		t.Fatalf("persisted signer binding changed: got %+v want %+v", loaded, source)
	}
	if err := store.db.Model(&MarketplaceSourceRow{}).Where("id = ?", source.ID).Update("signer_fingerprint", "").Error; err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatal(err)
	}
	loaded, ok, err = store.GetMarketplaceSource(ctx, source.ID)
	if err != nil || !ok || loaded.SignerFingerprint != source.SignerFingerprint {
		t.Fatalf("restart signer fingerprint backfill = (%+v, %v, %v)", loaded, ok, err)
	}
}

func TestStagePackageAcquisitionPersistsDerivedSourceTrustFingerprint(t *testing.T) {
	custom, err := marketplace.NewSignedCustomSource("staging-custom", "Staging Custom", "https://example.com/staging.git", "main", "", 0, marketplace.SourceSigner{
		KeyID: "staging-release", SecretRef: "vault-staging-release", PublicKey: base64.StdEncoding.EncodeToString(make([]byte, ed25519.PublicKeySize)),
	})
	if err != nil {
		t.Fatal(err)
	}
	for index, source := range []marketplace.Source{marketplace.OfficialSource(), custom} {
		source := source
		t.Run(source.Kind, func(t *testing.T) {
			ctx := context.Background()
			store, err := NewSQLiteStore(t.TempDir(), "local")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			if err := store.SaveMarketplaceSource(ctx, source); err != nil {
				t.Fatal(err)
			}
			trust, err := source.SignatureTrust()
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			operationID := fmt.Sprintf("staging-trust-%d", index)
			operation := marketplace.RefreshOperation{ID: operationID, SourceID: source.ID, Status: "running", StartedAt: now, LeaseToken: operationID + "-lease", LeaseExpiresAt: now.Add(time.Minute)}
			if err := store.AcquireRefreshLease(ctx, operation); err != nil {
				t.Fatal(err)
			}
			digest := pluginTestDigest(fmt.Sprintf("%x", index+10))
			if err := store.StagePackageAcquisition(ctx, source.ID, digest, operation.ID); err != nil {
				t.Fatal(err)
			}
			var staged PluginPackageStagingRow
			if err := store.db.Where("source_id = ? AND digest = ? AND operation_id = ?", source.ID, digest, operation.ID).First(&staged).Error; err != nil {
				t.Fatal(err)
			}
			if staged.SignerFingerprint != trust.Fingerprint {
				t.Fatalf("staged signer fingerprint = %q, want derived trust %q", staged.SignerFingerprint, trust.Fingerprint)
			}
			if err := store.CompletePackageAcquisitions(ctx, source.ID, operation.ID, false); err != nil {
				t.Fatal(err)
			}
			intents, err := store.ListPackageGCIntents(ctx)
			if err != nil || len(intents) != 1 || intents[0].SignerFingerprint != trust.Fingerprint {
				t.Fatalf("failed acquisition GC intent = %+v, %v", intents, err)
			}
		})
	}
}

func TestPluginDurableRowsSurviveDefaultMigration(t *testing.T) {
	ctx := context.Background()
	source, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = source.Close() })
	targetDataRoot := t.TempDir()
	target, err := NewSQLiteStore(targetDataRoot, "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = target.Close() })
	t.Cleanup(func() {
		_ = marketplace.DiscardVerifiedCacheRoot(filepath.Join(targetDataRoot, "plugins", "packages"))
	})
	now := time.Now().UTC().Truncate(time.Millisecond)
	if err := source.CreateResourceGroup(ctx, ResourceGroupRow{ID: "default", Name: "Default", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := source.SaveAgent(ctx, AgentRow{ID: "local", Version: "1.0.0", CapabilitiesJSON: `[]`}); err != nil {
		t.Fatal(err)
	}
	if err := source.BindResource(ctx, ResourceBindingRow{ID: "local-binding", ResourceKind: "agent", ResourceID: "local", ResourceGroupID: "default", UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	packageStaging := filepath.Join(source.dataRoot, "plugin-fixture")
	signingKey := ed25519.NewKeyFromSeed([]byte("storage-migration-signing-seed!!"))
	packageDigest := writeMigrationVerifiedPackage(t, packageStaging, "example.plugin", signingKey)
	packagePath := filepath.Join(source.dataRoot, "plugins", "packages", packageDigest)
	if err := os.MkdirAll(filepath.Dir(packagePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(packageStaging, packagePath); err != nil {
		t.Fatal(err)
	}
	custom, err := marketplace.NewSignedCustomSource("community", "Community", "https://example.com/plugins.git", "main", "", 0, marketplace.SourceSigner{KeyID: "community-release", SecretRef: "vault-community-release", PublicKey: base64.StdEncoding.EncodeToString(signingKey.Public().(ed25519.PublicKey))})
	if err != nil {
		t.Fatal(err)
	}
	snapshotPath := filepath.Join(source.dataRoot, "marketplace", "snapshots", custom.ID, "snapshot-1")
	if err := os.MkdirAll(snapshotPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(snapshotPath, "market.yaml"), []byte("schema_version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	trust, err := custom.SignatureTrust()
	if err != nil {
		t.Fatal(err)
	}
	snapshot := marketplace.Snapshot{ID: "snapshot-1", SourceID: custom.ID, Commit: "commit-1", Path: snapshotPath, ValidatedAt: now, Entries: []plugins.MarketEntry{{ID: "example.plugin", Version: "1.0.0", PackagePath: "plugins/example.plugin/1.0.0", PackageSHA256: packageDigest, SignatureKeyID: trust.KeyID}}}
	if err := source.PromoteSnapshot(ctx, custom, snapshot); err != nil {
		t.Fatal(err)
	}
	finished := now.Add(time.Second)
	if err := source.SaveRefreshOperation(ctx, marketplace.RefreshOperation{ID: "refresh-1", SourceID: custom.ID, Commit: snapshot.Commit, Status: "succeeded", StartedAt: now, FinishedAt: &finished}); err != nil {
		t.Fatal(err)
	}
	install := PluginInstallTransaction{
		Package:   PluginPackageRow{Digest: packageDigest, PluginID: "example.plugin", Version: "1.0.0", SourceID: custom.ID, SourceKind: custom.Kind, SourceRiskLabel: custom.RiskLabel, SignatureKeyID: trust.KeyID, SignaturePublicKey: trust.PublicKey, SignatureFingerprint: trust.Fingerprint, CachePath: packagePath, ManifestJSON: "{}", ConfigSchemaJSON: `{"type":"object"}`, VerifiedAt: now},
		Installed: InstalledPluginRow{PluginID: "example.plugin", ActivePackageDigest: packageDigest, DesiredLifecycle: "disabled", CurrentLifecycle: "disabled", CleanupPolicyJSON: "{}", LastOperationID: "op-install", InstalledAt: now, UpdatedAt: now},
		Grants:    []PluginGrantRow{{ID: "grant-1", PluginID: "example.plugin", PackageDigest: packageDigest, Permission: "http.inspect", GrantedBy: "admin", GrantedAt: now}},
		Operation: PluginOperationRow{ID: "op-install", PluginID: "example.plugin", Kind: "install", Status: "succeeded", TargetPackageDigest: packageDigest, AgentResultsJSON: "{}", ActorID: "admin", CreatedAt: now, CompletedAt: &now},
		Audit:     AuditEventRow{ID: "audit-install", ActorID: "admin", Action: "plugin.install", TargetKind: "plugin", TargetID: "example.plugin", Result: "success", MetadataJSON: "{}", CreatedAt: now},
	}
	if err := source.InstallPlugin(ctx, install); err != nil {
		t.Fatal(err)
	}
	instance := PluginInstanceRow{ID: "instance-1", PluginID: "example.plugin", ResourceGroupID: "default", TargetJSON: "[]", ConfigJSON: "{}", PendingConfigJSON: "", StatusSummaryJSON: "{}", CurrentState: "disabled", UpdatedAt: now}
	installed := install.Installed
	installed.LastOperationID = "op-configure"
	if err := source.ApplyPluginMutation(ctx, PluginMutation{PluginID: installed.PluginID, ExpectedActive: installed.ActivePackageDigest, Installed: &installed, ReplaceInstance: &instance, Operation: PluginOperationRow{ID: "op-configure", PluginID: installed.PluginID, Kind: "configure", Status: "succeeded", AgentResultsJSON: "{}", ActorID: "admin", CreatedAt: now}, Audit: AuditEventRow{ID: "audit-configure", ActorID: "admin", Action: "plugin.configure", TargetKind: "plugin", TargetID: installed.PluginID, Result: "success", MetadataJSON: "{}", CreatedAt: now}}); err != nil {
		t.Fatal(err)
	}

	if err := CopyDefaultMigrationRows(ctx, source, target); err != nil {
		t.Fatal(err)
	}
	if migrated, ok, err := target.GetInstalledPlugin(ctx, installed.PluginID); err != nil || !ok || migrated.ActivePackageDigest != installed.ActivePackageDigest {
		t.Fatalf("migrated installed plugin = %+v, %v, %v", migrated, ok, err)
	}
	migratedPackage, ok, err := target.GetPluginPackage(ctx, packageDigest)
	wantMigratedPath, pathErr := marketplace.SignerCachePath(filepath.Join(target.dataRoot, "plugins", "packages"), packageDigest, trust.Fingerprint)
	if err != nil || pathErr != nil || !ok || migratedPackage.CachePath != wantMigratedPath {
		t.Fatalf("migrated package = %+v, %v, %v", migratedPackage, ok, err)
	}
	if computed, err := plugins.ComputePackageDigest(migratedPackage.CachePath); err != nil || computed != packageDigest {
		t.Fatalf("migrated package digest = %q, %v", computed, err)
	}
	if err := CopyDefaultMigrationRows(ctx, source, target); err != nil {
		t.Fatalf("idempotent migration over sealed target: %v", err)
	}
	if repeated, ok, err := target.GetPluginPackageByIdentity(ctx, migratedPackage.Identity); err != nil || !ok || repeated.CachePath != migratedPackage.CachePath {
		t.Fatalf("repeated migration replaced sealed package root: %+v, %v, %v", repeated, ok, err)
	}
	migratedArtifact := filepath.Join(migratedPackage.CachePath, "artifacts", "policy.wasm")
	if info, err := os.Stat(migratedArtifact); err != nil || info.Mode().Perm()&0o111 != 0 {
		t.Fatalf("migrated package artifact executable mode = %v, %v", info, err)
	}
	if writable, err := os.OpenFile(migratedArtifact, os.O_WRONLY, 0); err == nil {
		_ = writable.Close()
		t.Fatal("migrated verified cache remained writable")
	}
	if err := os.RemoveAll(packagePath); err != nil {
		t.Fatal(err)
	}
	if computed, err := plugins.ComputePackageDigest(migratedPackage.CachePath); err != nil || computed != packageDigest {
		t.Fatalf("target package depends on removed source root: %q, %v", computed, err)
	}
	if migrated, ok, err := target.GetPluginInstance(ctx, instance.ID); err != nil || !ok || migrated.PluginID != installed.PluginID {
		t.Fatalf("migrated plugin instance = %+v, %v, %v", migrated, ok, err)
	}
	if operations, err := target.ListPluginOperations(ctx, installed.PluginID); err != nil || len(operations) != 2 {
		t.Fatalf("migrated operations = %+v, %v", operations, err)
	}
	if current, ok, err := target.CurrentSnapshot(ctx, custom.ID); err != nil || !ok || current.ID != snapshot.ID {
		t.Fatalf("migrated market snapshot = %+v, %v, %v", current, ok, err)
	}
	if current, _, _ := target.CurrentSnapshot(ctx, custom.ID); current.Path != filepath.Join(target.dataRoot, "marketplace", "snapshots", custom.ID, "snapshot-1") {
		t.Fatalf("migrated snapshot path = %q", current.Path)
	}
	var acquisition PluginPackageAcquisitionRow
	if err := target.db.WithContext(ctx).Where("source_id = ? AND digest = ?", custom.ID, packageDigest).First(&acquisition).Error; err != nil || acquisition.Status != "catalog" {
		t.Fatalf("migrated package acquisition = %+v, %v", acquisition, err)
	}
	if binding, err := target.GetResourceBinding(ctx, "plugin_instance", instance.ID); err != nil || binding.ResourceGroupID != instance.ResourceGroupID {
		t.Fatalf("migrated plugin instance binding = %+v, %v", binding, err)
	}
	gcClaim := marketplace.PackageGCClaim{SourceID: custom.ID, Digest: packageDigest, SignerFingerprint: trust.Fingerprint, Token: "migration-test", QuarantineID: "gcq-migration-test"}
	if err := marketplace.QuarantineAndDeleteVerifiedPackageVariant(filepath.Join(target.dataRoot, "plugins", "packages"), gcClaim); err != nil {
		t.Fatalf("GC migrated sealed cache: %v", err)
	}
	if _, err := os.Stat(migratedPackage.CachePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("migrated sealed cache remains after GC: %v", err)
	}
}

func TestMigrationPreservesSameDigestSignerSourceVariants(t *testing.T) {
	ctx := context.Background()
	sourceRoot, targetRoot := t.TempDir(), t.TempDir()
	source, err := NewSQLiteStore(sourceRoot, "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = source.Close() })
	target, err := NewSQLiteStore(targetRoot, "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = target.Close() })
	t.Cleanup(func() { _ = marketplace.DiscardVerifiedCacheRoot(filepath.Join(targetRoot, "plugins", "packages")) })
	firstKey := ed25519.NewKeyFromSeed([]byte("storage-migration-signing-seed!!"))
	secondSeed := sha256.Sum256([]byte("storage-migration-second-signer"))
	secondKey := ed25519.NewKeyFromSeed(secondSeed[:])
	type variant struct {
		sourceID, fingerprint, publicKey, cachePath, identity string
	}
	variants := make([]variant, 0, 2)
	contentDigest := ""
	for index, signingKey := range []ed25519.PrivateKey{firstKey, secondKey} {
		staging := t.TempDir()
		digest := writeMigrationVerifiedPackage(t, staging, "variant.migration", signingKey)
		if contentDigest == "" {
			contentDigest = digest
		} else if digest != contentDigest {
			t.Fatalf("same-content signer fixtures have different digests: %s, %s", contentDigest, digest)
		}
		publicKey := base64.StdEncoding.EncodeToString(signingKey.Public().(ed25519.PublicKey))
		fingerprint, err := marketplace.SourceSignerFingerprint(publicKey)
		if err != nil {
			t.Fatal(err)
		}
		sourceID := fmt.Sprintf("variant-source-%d", index+1)
		cachePath, err := marketplace.SignerCachePath(filepath.Join(sourceRoot, "plugins", "packages"), digest, fingerprint)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(staging, cachePath); err != nil {
			t.Fatal(err)
		}
		variants = append(variants, variant{sourceID: sourceID, fingerprint: fingerprint, publicKey: publicKey, cachePath: cachePath, identity: PluginPackageIdentity(digest, sourceID, fingerprint)})
		if err := source.db.Create(&PluginPackageRow{Identity: variants[index].identity, Digest: digest, PluginID: "variant.migration", Version: "1.0.0", SourceID: sourceID, SourceKind: marketplace.SourceKindCustom, SourceRiskLabel: marketplace.UntrustedRiskLabel, SignatureKeyID: "community-release", SignaturePublicKey: publicKey, SignatureFingerprint: fingerprint, CachePath: cachePath, ManifestJSON: `{}`, ConfigSchemaJSON: `{}`, VerifiedAt: time.Now().UTC()}).Error; err != nil {
			t.Fatal(err)
		}
	}
	var packages []PluginPackageRow
	if err := source.db.Order("identity").Find(&packages).Error; err != nil || len(packages) != 2 || packages[0].Digest != packages[1].Digest {
		t.Fatalf("source signer variants = %+v, %v", packages, err)
	}
	digest := packages[0].Digest
	now := time.Now().UTC()
	if err := source.db.Create(&InstalledPluginRow{PluginID: "variant.migration", ActivePackageDigest: digest, ActivePackageIdentity: variants[1].identity, ActiveSourceID: variants[1].sourceID, ActiveSourceKind: marketplace.SourceKindCustom, ActiveSourceRiskLabel: marketplace.UntrustedRiskLabel, RollbackPackageDigest: digest, RollbackPackageIdentity: variants[0].identity, RollbackSourceID: variants[0].sourceID, RollbackSourceKind: marketplace.SourceKindCustom, RollbackSourceRiskLabel: marketplace.UntrustedRiskLabel, DesiredLifecycle: "disabled", CurrentLifecycle: "disabled", CleanupPolicyJSON: `{}`, LastOperationID: "variant-upgrade", StateVersion: 2, InstalledAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := CopyDefaultMigrationRows(ctx, source, target); err != nil {
		t.Fatal(err)
	}
	for _, item := range variants {
		row, ok, err := target.GetPluginPackageByIdentity(ctx, item.identity)
		if err != nil || !ok || row.SignatureFingerprint != item.fingerprint || row.SourceID != item.sourceID {
			t.Fatalf("migrated signer variant %s = %+v, %v, %v", item.identity, row, ok, err)
		}
		if computed, err := plugins.ComputePackageDigest(row.CachePath); err != nil || computed != digest {
			t.Fatalf("migrated signer variant digest = %q, %v", computed, err)
		}
	}
	migrated, ok, err := target.GetInstalledPlugin(ctx, "variant.migration")
	if err != nil || !ok || migrated.ActivePackageIdentity != variants[1].identity || migrated.RollbackPackageIdentity != variants[0].identity {
		t.Fatalf("migrated lifecycle signer references = %+v, %v, %v", migrated, ok, err)
	}
}

func TestMigrationSeparatesLiveAndQuarantinedSameDigestSignerVariants(t *testing.T) {
	ctx := context.Background()
	sourceRoot, targetRoot := t.TempDir(), t.TempDir()
	source, err := NewSQLiteStore(sourceRoot, "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = source.Close() })
	target, err := NewSQLiteStore(targetRoot, "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = target.Close() })
	t.Cleanup(func() { _ = marketplace.DiscardVerifiedCacheRoot(filepath.Join(targetRoot, "plugins", "packages")) })

	firstKey := ed25519.NewKeyFromSeed([]byte("storage-migration-signing-seed!!"))
	secondSeed := sha256.Sum256([]byte("storage-migration-quarantine-signer"))
	secondKey := ed25519.NewKeyFromSeed(secondSeed[:])
	type variant struct {
		sourceID, fingerprint, publicKey, cachePath, identity string
	}
	variants := make([]variant, 0, 2)
	digest := ""
	for index, signingKey := range []ed25519.PrivateKey{firstKey, secondKey} {
		staging := t.TempDir()
		candidateDigest := writeMigrationVerifiedPackage(t, staging, "variant.quarantine", signingKey)
		if digest == "" {
			digest = candidateDigest
		} else if digest != candidateDigest {
			t.Fatalf("same-content signer fixtures have different digests: %s, %s", digest, candidateDigest)
		}
		publicKey := base64.StdEncoding.EncodeToString(signingKey.Public().(ed25519.PublicKey))
		fingerprint, err := marketplace.SourceSignerFingerprint(publicKey)
		if err != nil {
			t.Fatal(err)
		}
		sourceID := fmt.Sprintf("quarantine-source-%d", index+1)
		cachePath, err := marketplace.SignerCachePath(filepath.Join(sourceRoot, "plugins", "packages"), digest, fingerprint)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(staging, cachePath); err != nil {
			t.Fatal(err)
		}
		item := variant{sourceID: sourceID, fingerprint: fingerprint, publicKey: publicKey, cachePath: cachePath, identity: PluginPackageIdentity(digest, sourceID, fingerprint)}
		variants = append(variants, item)
		if err := source.db.Create(&PluginPackageRow{Identity: item.identity, Digest: digest, PluginID: "variant.quarantine", Version: "1.0.0", SourceID: sourceID, SourceKind: marketplace.SourceKindCustom, SourceRiskLabel: marketplace.UntrustedRiskLabel, SignatureKeyID: "community-release", SignaturePublicKey: publicKey, SignatureFingerprint: fingerprint, CachePath: cachePath, ManifestJSON: `{}`, ConfigSchemaJSON: `{}`, VerifiedAt: time.Now().UTC()}).Error; err != nil {
			t.Fatal(err)
		}
	}

	now := time.Now().UTC()
	if err := source.db.Create(&InstalledPluginRow{PluginID: "variant.quarantine", ActivePackageDigest: digest, ActivePackageIdentity: variants[1].identity, ActiveSourceID: variants[1].sourceID, ActiveSourceKind: marketplace.SourceKindCustom, ActiveSourceRiskLabel: marketplace.UntrustedRiskLabel, DesiredLifecycle: "disabled", CurrentLifecycle: "disabled", CleanupPolicyJSON: `{}`, LastOperationID: "variant-install", StateVersion: 1, InstalledAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	claim := marketplace.PackageGCClaim{SourceID: variants[0].sourceID, Digest: digest, SignerFingerprint: variants[0].fingerprint, Token: "gc_migration_quarantine", QuarantineID: "gcq_migration_quarantine"}
	quarantineRelative, err := marketplace.PackageGCQuarantinePath(claim)
	if err != nil {
		t.Fatal(err)
	}
	quarantinePath := filepath.Join(sourceRoot, "plugins", "packages", filepath.FromSlash(quarantineRelative))
	if err := os.MkdirAll(filepath.Dir(quarantinePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(variants[0].cachePath, quarantinePath); err != nil {
		t.Fatal(err)
	}
	if err := source.db.Create(&PluginCacheGCIntentRow{SourceID: claim.SourceID, Digest: digest, SignerFingerprint: claim.SignerFingerprint, Status: "deleting", ClaimToken: claim.Token, ClaimExpiresAt: now.Add(time.Hour), QuarantinePath: quarantineRelative, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := source.db.Create(&PluginDigestFenceRow{Digest: digest, ClaimToken: claim.Token, ClaimExpiresAt: now.Add(time.Hour), UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}

	if err := CopyDefaultMigrationRows(ctx, source, target); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := target.GetPluginPackageByIdentity(ctx, variants[0].identity); err != nil || ok {
		t.Fatalf("quarantined signer variant migrated as live package = %v, %v", ok, err)
	}
	live, ok, err := target.GetPluginPackageByIdentity(ctx, variants[1].identity)
	if err != nil || !ok || live.SignatureFingerprint != variants[1].fingerprint {
		t.Fatalf("live signer variant migration = %+v, %v, %v", live, ok, err)
	}
	if computed, err := plugins.ComputePackageDigest(live.CachePath); err != nil || computed != digest {
		t.Fatalf("live signer variant digest = %q, %v", computed, err)
	}
	var intent PluginCacheGCIntentRow
	if err := target.db.Where("source_id = ? AND digest = ? AND signer_fingerprint = ?", claim.SourceID, digest, claim.SignerFingerprint).First(&intent).Error; err != nil {
		t.Fatal(err)
	}
	if intent.ClaimToken != "" || intent.QuarantinePath != quarantineRelative || intent.Status != "pending" {
		t.Fatalf("migrated quarantined signer intent = %+v", intent)
	}
	targetQuarantine := filepath.Join(targetRoot, "plugins", "packages", filepath.FromSlash(intent.QuarantinePath))
	if computed, err := plugins.ComputePackageDigest(targetQuarantine); err != nil || computed != digest {
		t.Fatalf("quarantined signer variant digest = %q, %v", computed, err)
	}
}

type preparedGCMigrationFixture struct {
	source, target         *GormStore
	sourceRoot, targetRoot string
	claim                  marketplace.PackageGCClaim
	objects                []marketplace.PackageGCObject
	identity               string
}

func newPreparedGCMigrationFixture(t *testing.T) preparedGCMigrationFixture {
	t.Helper()
	source, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = source.Close() })
	target, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = target.Close() })
	sourceRoot := filepath.Join(source.dataRoot, "plugins", "packages")
	targetRoot := filepath.Join(target.dataRoot, "plugins", "packages")
	t.Cleanup(func() { _ = marketplace.DiscardVerifiedCacheRoot(targetRoot) })
	packageRoot := t.TempDir()
	signingKey := ed25519.NewKeyFromSeed([]byte("storage-migration-signing-seed!!"))
	digest := writeMigrationVerifiedPackage(t, packageRoot, "prepared.gc.migration", signingKey)
	publicKey := base64.StdEncoding.EncodeToString(signingKey.Public().(ed25519.PublicKey))
	fingerprint, err := marketplace.SourceSignerFingerprint(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	trust := marketplace.SignatureTrust{SourceID: "prepared-gc-source", SourceKind: marketplace.SourceKindCustom, KeyID: "community-release", PublicKey: publicKey, Fingerprint: fingerprint}
	claim := marketplace.PackageGCClaim{SourceID: trust.SourceID, Digest: digest, SignerFingerprint: fingerprint, Token: "prepared-gc-worker", QuarantineID: "prepared-gc-object", Trust: trust}
	signerObject, err := marketplace.NewPackageGCObject(claim, marketplace.PackageGCLayoutSigner)
	if err != nil {
		t.Fatal(err)
	}
	legacyObject, err := marketplace.NewPackageGCObject(claim, marketplace.PackageGCLayoutLegacy)
	if err != nil {
		t.Fatal(err)
	}
	for _, object := range []marketplace.PackageGCObject{signerObject, legacyObject} {
		livePath := filepath.Join(sourceRoot, filepath.FromSlash(object.Path))
		if err := copyPluginPackageDirectory(packageRoot, livePath); err != nil {
			t.Fatal(err)
		}
	}
	identity := PluginPackageIdentity(digest, trust.SourceID, fingerprint)
	now := time.Now().UTC()
	if err := source.db.Create(&PluginPackageRow{Identity: identity, Digest: digest, PluginID: "prepared.gc.migration", Version: "1.0.0", SourceID: trust.SourceID, SourceKind: trust.SourceKind, SourceRiskLabel: marketplace.UntrustedRiskLabel, SignatureKeyID: trust.KeyID, SignaturePublicKey: trust.PublicKey, SignatureFingerprint: trust.Fingerprint, CachePath: filepath.Join(sourceRoot, filepath.FromSlash(signerObject.Path)), ManifestJSON: `{}`, ConfigSchemaJSON: `{}`, VerifiedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal([]marketplace.PackageGCObject{signerObject, legacyObject})
	if err != nil {
		t.Fatal(err)
	}
	if err := source.db.Create(&PluginCacheGCIntentRow{SourceID: claim.SourceID, Digest: digest, SignerFingerprint: fingerprint, SignerSourceKind: trust.SourceKind, SignerKeyID: trust.KeyID, SignerPublicKey: trust.PublicKey, Status: "deleting", ClaimToken: claim.Token, ClaimExpiresAt: now.Add(time.Hour), QuarantineID: claim.QuarantineID, ObjectsPrepared: true, CacheObjectsJSON: string(encoded), UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := source.db.Create(&PluginDigestFenceRow{Digest: digest, ClaimToken: claim.Token, ClaimExpiresAt: now.Add(time.Hour), UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	return preparedGCMigrationFixture{source: source, target: target, sourceRoot: sourceRoot, targetRoot: targetRoot, claim: claim, objects: []marketplace.PackageGCObject{signerObject, legacyObject}, identity: identity}
}

func (fixture preparedGCMigrationFixture) moveToQuarantine(t *testing.T, index int) {
	t.Helper()
	object := fixture.objects[index]
	livePath := filepath.Join(fixture.sourceRoot, filepath.FromSlash(object.Path))
	quarantinePath := filepath.Join(fixture.sourceRoot, filepath.FromSlash(object.QuarantinePath))
	if err := os.MkdirAll(filepath.Dir(quarantinePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(livePath, quarantinePath); err != nil {
		t.Fatal(err)
	}
}

func TestPreparedMultiObjectGCMigrationReconcilesCrashStates(t *testing.T) {
	states := []struct {
		name   string
		mutate func(*testing.T, preparedGCMigrationFixture)
	}{
		{name: "after prepare"},
		{name: "after signer rename", mutate: func(t *testing.T, fixture preparedGCMigrationFixture) { fixture.moveToQuarantine(t, 0) }},
		{name: "partial delete before DB completion", mutate: func(t *testing.T, fixture preparedGCMigrationFixture) {
			if err := os.RemoveAll(filepath.Join(fixture.sourceRoot, filepath.FromSlash(fixture.objects[0].Path))); err != nil {
				t.Fatal(err)
			}
			fixture.moveToQuarantine(t, 1)
		}},
		{name: "physical delete before DB completion", mutate: func(t *testing.T, fixture preparedGCMigrationFixture) {
			for _, object := range fixture.objects {
				if err := os.RemoveAll(filepath.Join(fixture.sourceRoot, filepath.FromSlash(object.Path))); err != nil {
					t.Fatal(err)
				}
			}
		}},
	}
	for _, state := range states {
		t.Run(state.name, func(t *testing.T) {
			fixture := newPreparedGCMigrationFixture(t)
			if state.mutate != nil {
				state.mutate(t, fixture)
			}
			if err := CopyDefaultMigrationRows(t.Context(), fixture.source, fixture.target); err != nil {
				t.Fatal(err)
			}
			var intent PluginCacheGCIntentRow
			if err := fixture.target.db.Where("source_id = ? AND digest = ? AND signer_fingerprint = ?", fixture.claim.SourceID, fixture.claim.Digest, fixture.claim.SignerFingerprint).First(&intent).Error; err != nil {
				t.Fatal(err)
			}
			if !intent.ObjectsPrepared || intent.CacheObjectsJSON == "" || intent.QuarantineID != fixture.claim.QuarantineID || intent.QuarantinePath != "" || intent.ClaimToken != "" || intent.Status != "pending" {
				t.Fatalf("migrated prepared intent = %+v", intent)
			}
			if _, ok, err := fixture.target.GetPluginPackageByIdentity(t.Context(), fixture.identity); err != nil || ok {
				t.Fatalf("unreferenced prepared package row survived migration: %v, %v", ok, err)
			}
			for _, object := range fixture.objects {
				for _, relative := range []string{object.Path, object.QuarantinePath} {
					sourcePath := filepath.Join(fixture.sourceRoot, filepath.FromSlash(relative))
					targetPath := filepath.Join(fixture.targetRoot, filepath.FromSlash(relative))
					sourceExists, sourceErr := migrationPackagePathExists(sourcePath)
					targetExists, targetErr := migrationPackagePathExists(targetPath)
					if sourceErr != nil || targetErr != nil || sourceExists != targetExists {
						t.Fatalf("migrated object state %q source=%v/%v target=%v/%v", relative, sourceExists, sourceErr, targetExists, targetErr)
					}
					if targetExists {
						if computed, err := plugins.ComputePackageDigest(targetPath); err != nil || computed != fixture.claim.Digest {
							t.Fatalf("migrated object digest %q = %q, %v", relative, computed, err)
						}
					}
				}
			}
		})
	}
}

func TestPreparedMultiObjectGCMigrationFailsClosedOnTamperedBinding(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, preparedGCMigrationFixture)
	}{
		{name: "missing object fields", mutate: func(t *testing.T, fixture preparedGCMigrationFixture) {
			if err := fixture.source.db.Model(&PluginCacheGCIntentRow{}).Where("source_id = ? AND digest = ?", fixture.claim.SourceID, fixture.claim.Digest).Update("cache_objects_json", `[{"layout":"signer"}]`).Error; err != nil {
				t.Fatal(err)
			}
		}},
		{name: "traversal path", mutate: func(t *testing.T, fixture preparedGCMigrationFixture) {
			objects := append([]marketplace.PackageGCObject(nil), fixture.objects...)
			objects[0].Path = "../outside"
			encoded, _ := json.Marshal(objects)
			if err := fixture.source.db.Model(&PluginCacheGCIntentRow{}).Where("source_id = ? AND digest = ?", fixture.claim.SourceID, fixture.claim.Digest).Update("cache_objects_json", string(encoded)).Error; err != nil {
				t.Fatal(err)
			}
		}},
		{name: "live and quarantine collision", mutate: func(t *testing.T, fixture preparedGCMigrationFixture) {
			object := fixture.objects[0]
			quarantinePath := filepath.Join(fixture.sourceRoot, filepath.FromSlash(object.QuarantinePath))
			if err := copyPluginPackageDirectory(filepath.Join(fixture.sourceRoot, filepath.FromSlash(object.Path)), quarantinePath); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "tampered legacy signer", mutate: func(t *testing.T, fixture preparedGCMigrationFixture) {
			legacyPath := filepath.Join(fixture.sourceRoot, filepath.FromSlash(fixture.objects[1].Path), plugins.PackageSignatureFile)
			if err := os.WriteFile(legacyPath, []byte("tampered"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPreparedGCMigrationFixture(t)
			test.mutate(t, fixture)
			if err := CopyDefaultMigrationRows(t.Context(), fixture.source, fixture.target); err == nil {
				t.Fatal("tampered prepared GC migration succeeded")
			}
		})
	}
}

func TestPreparedMultiObjectGCMigrationRestoresReferencedVariantCanonically(t *testing.T) {
	fixture := newPreparedGCMigrationFixture(t)
	fixture.moveToQuarantine(t, 0)
	now := time.Now().UTC()
	if err := fixture.source.db.Create(&InstalledPluginRow{PluginID: "prepared.gc.migration", ActivePackageDigest: fixture.claim.Digest, ActivePackageIdentity: fixture.identity, ActiveSourceID: fixture.claim.SourceID, ActiveSourceKind: marketplace.SourceKindCustom, DesiredLifecycle: "disabled", CurrentLifecycle: "disabled", CleanupPolicyJSON: `{}`, LastOperationID: "prepared-gc-install", StateVersion: 1, InstalledAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := CopyDefaultMigrationRows(t.Context(), fixture.source, fixture.target); err != nil {
		t.Fatal(err)
	}
	row, ok, err := fixture.target.GetPluginPackageByIdentity(t.Context(), fixture.identity)
	if err != nil || !ok {
		t.Fatalf("referenced prepared package row = %+v, %v, %v", row, ok, err)
	}
	canonical := filepath.Join(fixture.targetRoot, filepath.FromSlash(fixture.objects[0].Path))
	if row.CachePath != canonical {
		t.Fatalf("referenced prepared package cache path = %q, want %q", row.CachePath, canonical)
	}
	if computed, err := plugins.ComputePackageDigest(canonical); err != nil || computed != fixture.claim.Digest {
		t.Fatalf("referenced prepared canonical cache = %q, %v", computed, err)
	}
	for _, removed := range []string{fixture.objects[0].QuarantinePath, fixture.objects[1].Path, fixture.objects[1].QuarantinePath} {
		if _, err := os.Stat(filepath.Join(fixture.targetRoot, filepath.FromSlash(removed))); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("referenced migration retained duplicate object %q: %v", removed, err)
		}
	}
	var intent PluginCacheGCIntentRow
	if err := fixture.target.db.Where("source_id = ? AND digest = ?", fixture.claim.SourceID, fixture.claim.Digest).First(&intent).Error; err != nil || !intent.ObjectsPrepared || intent.ClaimToken != "" || intent.Status != "pending" {
		t.Fatalf("referenced prepared intent = %+v, %v", intent, err)
	}
}

func TestMigrationReconcilesUnpreparedIntentAfterSharedVariantDeletion(t *testing.T) {
	for _, referenced := range []bool{false, true} {
		name := map[bool]string{false: "unreferenced retires missing metadata", true: "referenced fails closed"}[referenced]
		t.Run(name, func(t *testing.T) {
			source, err := NewSQLiteStore(t.TempDir(), "local")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = source.Close() })
			target, err := NewSQLiteStore(t.TempDir(), "local")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = target.Close() })
			targetRoot := filepath.Join(target.dataRoot, "plugins", "packages")
			t.Cleanup(func() { _ = marketplace.DiscardVerifiedCacheRoot(targetRoot) })
			packageRoot := t.TempDir()
			signingKey := ed25519.NewKeyFromSeed([]byte("storage-migration-signing-seed!!"))
			digest := writeMigrationVerifiedPackage(t, packageRoot, "shared.unprepared.gc", signingKey)
			publicKey := base64.StdEncoding.EncodeToString(signingKey.Public().(ed25519.PublicKey))
			fingerprint, err := marketplace.SourceSignerFingerprint(publicKey)
			if err != nil {
				t.Fatal(err)
			}
			cachePath, err := marketplace.SignerCachePath(filepath.Join(source.dataRoot, "plugins", "packages"), digest, fingerprint)
			if err != nil {
				t.Fatal(err)
			}
			if err := copyPluginPackageDirectory(packageRoot, cachePath); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			identityA := PluginPackageIdentity(digest, "shared-source-a", fingerprint)
			identityB := PluginPackageIdentity(digest, "shared-source-b", fingerprint)
			for _, item := range []struct{ sourceID, identity string }{{"shared-source-a", identityA}, {"shared-source-b", identityB}} {
				if err := source.db.Create(&PluginPackageRow{Identity: item.identity, Digest: digest, PluginID: "shared.unprepared.gc", Version: "1.0.0", SourceID: item.sourceID, SourceKind: marketplace.SourceKindCustom, SourceRiskLabel: marketplace.UntrustedRiskLabel, SignatureKeyID: "community-release", SignaturePublicKey: publicKey, SignatureFingerprint: fingerprint, CachePath: cachePath, ManifestJSON: `{}`, ConfigSchemaJSON: `{}`, VerifiedAt: now}).Error; err != nil {
					t.Fatal(err)
				}
			}
			// Source A completed its GC: its metadata and the shared physical
			// signer object are gone. Source B still has an older unprepared intent.
			if err := source.db.Delete(&PluginPackageRow{}, "identity = ?", identityA).Error; err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(cachePath); err != nil {
				t.Fatal(err)
			}
			if err := source.db.Create(&PluginCacheGCIntentRow{SourceID: "shared-source-b", Digest: digest, SignerFingerprint: fingerprint, Status: "pending", QuarantineID: "shared-source-b-gcq", CacheObjectsJSON: `[]`, UpdatedAt: now}).Error; err != nil {
				t.Fatal(err)
			}
			if err := source.db.Create(&PluginDigestFenceRow{Digest: digest, UpdatedAt: now}).Error; err != nil {
				t.Fatal(err)
			}
			if referenced {
				if err := source.db.Create(&InstalledPluginRow{PluginID: "shared.unprepared.gc", ActivePackageDigest: digest, ActivePackageIdentity: identityB, ActiveSourceID: "shared-source-b", ActiveSourceKind: marketplace.SourceKindCustom, DesiredLifecycle: "disabled", CurrentLifecycle: "disabled", CleanupPolicyJSON: `{}`, LastOperationID: "shared-install", StateVersion: 1, InstalledAt: now, UpdatedAt: now}).Error; err != nil {
					t.Fatal(err)
				}
			} else if err := target.db.Create(&PluginArtifactRow{ID: "stale-shared-artifact", PackageIdentity: identityB, PackageDigest: digest, Path: "artifacts/stale.wasm", SHA256: pluginTestDigest("a"), SizeBytes: 1, Mode: "wasm"}).Error; err != nil {
				t.Fatal(err)
			}
			err = CopyDefaultMigrationRows(t.Context(), source, target)
			if referenced {
				if err == nil {
					t.Fatal("referenced missing shared cache migrated")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, ok, err := target.GetPluginPackageByIdentity(t.Context(), identityB); err != nil || ok {
				t.Fatalf("missing unreferenced shared package metadata = %v, %v", ok, err)
			}
			var artifacts int64
			if err := target.db.Model(&PluginArtifactRow{}).Where("package_identity = ?", identityB).Count(&artifacts).Error; err != nil || artifacts != 0 {
				t.Fatalf("missing unreferenced shared artifacts = %d, %v", artifacts, err)
			}
			var intent PluginCacheGCIntentRow
			if err := target.db.Where("source_id = ? AND digest = ?", "shared-source-b", digest).First(&intent).Error; err != nil || intent.Status != "pending" || intent.ClaimToken != "" || !intent.ClaimExpiresAt.IsZero() {
				t.Fatalf("migrated unprepared shared intent = %+v, %v", intent, err)
			}
			var fence PluginDigestFenceRow
			if err := target.db.Where("digest = ?", digest).First(&fence).Error; err != nil || fence.ClaimToken != "" || !fence.ClaimExpiresAt.IsZero() {
				t.Fatalf("migrated shared digest fence = %+v, %v", fence, err)
			}
		})
	}
}

func TestMarketplaceCleanupMigrationRewritesPathAndClearsClaim(t *testing.T) {
	ctx := context.Background()
	source, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = source.Close() })
	targetDataRoot := t.TempDir()
	target, err := NewSQLiteStore(targetDataRoot, "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = target.Close() })
	sourcePath := filepath.Join(source.dataRoot, "marketplace", "snapshots", "retired", "snapshot-a")
	if err := os.MkdirAll(sourcePath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourcePath, "market.yaml"), []byte("schema_version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	row := MarketplaceDirectoryCleanupRow{ID: "cleanup-migrate", SourceID: "retired", OperationID: "refresh-old", Path: sourcePath, PathDigest: pluginStorageDigest(sourcePath), State: "claimed", ClaimToken: "source-worker", ClaimExpiresAt: now.Add(time.Hour), UpdatedAt: now}
	if err := source.db.WithContext(ctx).Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	if err := copyMarketplaceDirectoryCleanupRows(ctx, source, target); err != nil {
		t.Fatal(err)
	}
	var migrated MarketplaceDirectoryCleanupRow
	if err := target.db.WithContext(ctx).First(&migrated, "id = ?", row.ID).Error; err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.ToSlash(filepath.Join("snapshots", "retired", "snapshot-a"))
	if migrated.Path != wantPath || migrated.PathDigest != pluginStorageDigest(wantPath) || migrated.ClaimToken != "" || !migrated.ClaimExpiresAt.IsZero() {
		t.Fatalf("migrated cleanup ownership = %+v", migrated)
	}
	if _, err := os.Stat(filepath.Join(target.dataRoot, "marketplace", filepath.FromSlash(wantPath), "market.yaml")); err != nil {
		t.Fatalf("migrated cleanup directory = %v", err)
	}
}

func TestBootstrapReplacesLegacyPluginIndexesAfterSafeIndexesExist(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.db.Exec("CREATE UNIQUE INDEX idx_marketplace_directory_cleanup_path ON marketplace_directory_cleanup(path)").Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Exec("CREATE UNIQUE INDEX idx_plugin_grant ON plugin_grants(plugin_id, package_digest, permission, resource_selector)").Error; err != nil {
		t.Fatal(err)
	}
	if err := BootstrapSchema(t.Context(), store.db, SchemaOptionsForDriver("sqlite", false)); err != nil {
		t.Fatal(err)
	}
	for _, legacy := range []struct {
		model any
		name  string
	}{{&MarketplaceDirectoryCleanupRow{}, "idx_marketplace_directory_cleanup_path"}, {&PluginGrantRow{}, "idx_plugin_grant"}} {
		if store.db.Migrator().HasIndex(legacy.model, legacy.name) {
			t.Fatalf("legacy index %s remains", legacy.name)
		}
	}
	if !store.db.Migrator().HasIndex(&MarketplaceDirectoryCleanupRow{}, "PathDigest") || !store.db.Migrator().HasIndex(&PluginGrantRow{}, "GrantKey") {
		t.Fatal("replacement plugin indexes are missing")
	}
}

func TestDuplicateKeyErrorsAreRecognizedAcrossSupportedDrivers(t *testing.T) {
	for name, err := range map[string]error{
		"gorm":       gorm.ErrDuplicatedKey,
		"sqlite":     errors.New("UNIQUE constraint failed: installed_plugins.plugin_id"),
		"mysql 1062": errors.New("Error 1062 (23000): Duplicate entry 'official.plugin' for key 'PRIMARY'"),
		"postgres":   errors.New(`ERROR: duplicate key value violates unique constraint "installed_plugins_pkey" (SQLSTATE 23505)`),
	} {
		t.Run(name, func(t *testing.T) {
			if !isDuplicateKeyError(err) {
				t.Fatalf("duplicate error was not recognized: %v", err)
			}
		})
	}
	if isDuplicateKeyError(errors.New("connection reset")) {
		t.Fatal("unrelated database error was classified as duplicate")
	}
}

func TestConcurrentPluginInstallReturnsStableAlreadyInstalledConflict(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	digest := pluginTestDigest("c")
	now := time.Now().UTC()
	input := func(suffix string) PluginInstallTransaction {
		return PluginInstallTransaction{
			Package:   PluginPackageRow{Digest: digest, PluginID: "concurrent.install", Version: "1.0.0", CachePath: "cache", ManifestJSON: `{}`, ConfigSchemaJSON: `{"type":"object"}`, VerifiedAt: now},
			Installed: InstalledPluginRow{PluginID: "concurrent.install", ActivePackageDigest: digest, DesiredLifecycle: "disabled", CurrentLifecycle: "disabled", CleanupPolicyJSON: `{}`, LastOperationID: "install-" + suffix, InstalledAt: now, UpdatedAt: now},
			Operation: PluginOperationRow{ID: "install-" + suffix, PluginID: "concurrent.install", Kind: "install", Status: "succeeded", TargetPackageDigest: digest, AgentResultsJSON: `{}`, ActorID: "admin", CreatedAt: now, CompletedAt: &now},
			Audit:     AuditEventRow{ID: "audit-install-" + suffix, ActorID: "admin", Action: "plugin.install", TargetKind: "plugin", TargetID: "concurrent.install", Result: "success", MetadataJSON: `{}`, CreatedAt: now},
		}
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for _, suffix := range []string{"a", "b"} {
		workers.Add(1)
		go func(suffix string) {
			defer workers.Done()
			<-start
			results <- store.InstallPlugin(t.Context(), input(suffix))
		}(suffix)
	}
	close(start)
	workers.Wait()
	close(results)
	succeeded, conflicted := 0, 0
	for err := range results {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrPluginAlreadyInstalled):
			conflicted++
		default:
			t.Fatalf("concurrent install returned unstable error: %v", err)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("concurrent install results succeeded=%d conflicted=%d", succeeded, conflicted)
	}
}

func TestMigrationCopiesCurrentCatalogCacheWithoutInstalledPackageRow(t *testing.T) {
	ctx := context.Background()
	source, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = source.Close() })
	targetDataRoot := t.TempDir()
	target, err := NewSQLiteStore(targetDataRoot, "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = target.Close() })
	t.Cleanup(func() {
		_ = marketplace.DiscardVerifiedCacheRoot(filepath.Join(targetDataRoot, "plugins", "packages"))
	})
	staging := t.TempDir()
	signingKey := ed25519.NewKeyFromSeed([]byte("storage-migration-signing-seed!!"))
	digest := writeMigrationVerifiedPackage(t, staging, "catalog.plugin", signingKey)
	sourceCache := filepath.Join(source.dataRoot, "plugins", "packages", digest)
	if err := os.MkdirAll(filepath.Dir(sourceCache), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(staging, sourceCache); err != nil {
		t.Fatal(err)
	}
	market, _ := marketplace.NewSignedCustomSource("catalog-only", "Catalog Only", "https://example.com/plugins.git", "main", "", 0, marketplace.SourceSigner{KeyID: "community-release", SecretRef: "vault-catalog", PublicKey: base64.StdEncoding.EncodeToString(signingKey.Public().(ed25519.PublicKey))})
	snapshotPath := filepath.Join(source.dataRoot, "marketplace", "snapshots", market.ID, "current")
	if err := os.MkdirAll(snapshotPath, 0o755); err != nil {
		t.Fatal(err)
	}
	snapshot := marketplace.Snapshot{ID: "catalog-current", SourceID: market.ID, Commit: "commit", Path: snapshotPath, ValidatedAt: time.Now().UTC(), Entries: []plugins.MarketEntry{{ID: "catalog.plugin", Version: "1.0.0", PackagePath: "plugins/catalog.plugin/1.0.0", PackageSHA256: digest, SignatureKeyID: market.SignerKeyID}}}
	if err := source.PromoteSnapshot(ctx, market, snapshot); err != nil {
		t.Fatal(err)
	}
	if err := CopyDefaultMigrationRows(ctx, source, target); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(sourceCache); err != nil {
		t.Fatal(err)
	}
	targetCache, err := marketplace.SignerCachePath(filepath.Join(target.dataRoot, "plugins", "packages"), digest, market.SignerFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if computed, err := plugins.ComputePackageDigest(targetCache); err != nil || computed != digest {
		t.Fatalf("catalog-only cache migration = %q, %v", computed, err)
	}
	if _, ok, err := target.CurrentMarketEntry(ctx, market.ID, "catalog.plugin", "1.0.0", digest); err != nil || !ok {
		t.Fatalf("migrated catalog entry unavailable: %v, %v", ok, err)
	}
}

func TestMigrationKeepsOneAuthoritativeQuarantineStateAndResetsFence(t *testing.T) {
	for _, referenced := range []bool{false, true} {
		t.Run(map[bool]string{false: "unreferenced", true: "referenced"}[referenced], func(t *testing.T) {
			ctx := context.Background()
			source, err := NewSQLiteStore(t.TempDir(), "local")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = source.Close() })
			targetDataRoot := t.TempDir()
			target, err := NewSQLiteStore(targetDataRoot, "local")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = target.Close() })
			t.Cleanup(func() {
				_ = marketplace.DiscardVerifiedCacheRoot(filepath.Join(targetDataRoot, "plugins", "packages"))
			})
			staging := t.TempDir()
			signingKey := ed25519.NewKeyFromSeed([]byte("storage-migration-signing-seed!!"))
			digest := writeMigrationVerifiedPackage(t, staging, "migration.plugin", signingKey)
			publicKey := base64.StdEncoding.EncodeToString(signingKey.Public().(ed25519.PublicKey))
			fingerprint, err := marketplace.SourceSignerFingerprint(publicKey)
			if err != nil {
				t.Fatal(err)
			}
			gcClaim := marketplace.PackageGCClaim{SourceID: "removed-source", Digest: digest, SignerFingerprint: fingerprint, Token: "old-claim", QuarantineID: "gcq-old-claim"}
			quarantineRelative, err := marketplace.PackageGCQuarantinePath(gcClaim)
			if err != nil {
				t.Fatal(err)
			}
			quarantinePath := filepath.Join(source.dataRoot, "plugins", "packages", filepath.FromSlash(quarantineRelative))
			if err := os.MkdirAll(filepath.Dir(quarantinePath), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(staging, quarantinePath); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			livePath := filepath.Join(source.dataRoot, "plugins", "packages", digest)
			if err := source.db.Create(&PluginPackageRow{Digest: digest, PluginID: "migration.plugin", Version: "1.0.0", SourceID: "removed-source", SourceKind: marketplace.SourceKindCustom, SignatureKeyID: "community-release", SignaturePublicKey: publicKey, SignatureFingerprint: fingerprint, CachePath: livePath, ManifestJSON: `{}`, ConfigSchemaJSON: `{}`, VerifiedAt: now}).Error; err != nil {
				t.Fatal(err)
			}
			if err := source.db.Create(&PluginCacheGCIntentRow{SourceID: "removed-source", Digest: digest, SignerFingerprint: fingerprint, Status: "claimed", ClaimToken: "old-claim", ClaimExpiresAt: now.Add(time.Hour), QuarantinePath: quarantineRelative, UpdatedAt: now}).Error; err != nil {
				t.Fatal(err)
			}
			if err := source.db.Create(&PluginDigestFenceRow{Digest: digest, ClaimToken: "old-claim", ClaimExpiresAt: now.Add(time.Hour), UpdatedAt: now}).Error; err != nil {
				t.Fatal(err)
			}
			if referenced {
				if err := source.db.Create(&InstalledPluginRow{PluginID: "migration.plugin", ActivePackageDigest: digest, DesiredLifecycle: "disabled", CurrentLifecycle: "disabled", CleanupPolicyJSON: `{}`, LastOperationID: "install", StateVersion: 1, InstalledAt: now, UpdatedAt: now}).Error; err != nil {
					t.Fatal(err)
				}
			}
			if err := CopyDefaultMigrationRows(ctx, source, target); err != nil {
				t.Fatal(err)
			}
			var intent PluginCacheGCIntentRow
			if err := target.db.Where("source_id = ? AND digest = ?", "removed-source", digest).First(&intent).Error; err != nil || intent.Status != "pending" {
				t.Fatalf("migrated GC intent = %+v, %v", intent, err)
			}
			var fence PluginDigestFenceRow
			if err := target.db.Where("digest = ?", digest).First(&fence).Error; err != nil || fence.ClaimToken != "" || !fence.ClaimExpiresAt.IsZero() {
				t.Fatalf("migrated digest fence = %+v, %v", fence, err)
			}
			targetLive, pathErr := marketplace.SignerCachePath(filepath.Join(target.dataRoot, "plugins", "packages"), digest, fingerprint)
			if pathErr != nil {
				t.Fatal(pathErr)
			}
			if referenced {
				if intent.QuarantinePath != "" || intent.ClaimToken != "" {
					t.Fatalf("referenced intent retained quarantine ownership %+v", intent)
				}
				if computed, err := plugins.ComputePackageDigest(targetLive); err != nil || computed != digest {
					t.Fatalf("referenced live cache = %q, %v", computed, err)
				}
			} else {
				if intent.QuarantinePath == "" || intent.ClaimToken != "" {
					t.Fatalf("unreferenced intent lost quarantine ownership %+v", intent)
				}
				if _, err := os.Stat(targetLive); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("unreferenced migration created live cache: %v", err)
				}
				targetQuarantine := filepath.Join(target.dataRoot, "plugins", "packages", filepath.FromSlash(intent.QuarantinePath))
				if computed, err := plugins.ComputePackageDigest(targetQuarantine); err != nil || computed != digest {
					t.Fatalf("unreferenced quarantine cache = %q, %v", computed, err)
				}
			}
		})
	}
}

func TestDeleteMarketplaceSourcePreservesRefreshHistoryAndInstalledPackage(t *testing.T) {
	ctx := context.Background()
	store, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	source, _ := marketplace.NewCustomSource("community", "Community", "https://example.com/plugins.git", "main", "", 0)
	if err := store.SaveMarketplaceSource(ctx, source); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	stable := marketplace.Snapshot{ID: "stable-snapshot", SourceID: source.ID, Commit: "stable-commit", Path: "stable/path", ValidatedAt: now}
	if err := store.PromoteSnapshot(ctx, source, stable); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRefreshOperation(ctx, marketplace.RefreshOperation{ID: "refresh-kept", SourceID: source.ID, Status: "failed", ErrorClass: "fetch", Error: "offline", StartedAt: now, FinishedAt: &now}); err != nil {
		t.Fatal(err)
	}
	if updated, ok, err := store.GetMarketplaceSource(ctx, source.ID); err != nil || !ok || updated.LastResult != "failed" || updated.LastError != "offline" || !updated.LastCompletedAt.Equal(now) {
		t.Fatalf("refresh failure not projected to source: %+v, %v, %v", updated, ok, err)
	}
	if current, ok, err := store.CurrentSnapshot(ctx, source.ID); err != nil || !ok || current.ID != stable.ID || current.Commit != stable.Commit {
		t.Fatalf("refresh failure replaced current snapshot: %+v, %v, %v", current, ok, err)
	}
	if _, err := store.DeleteMarketplaceSource(ctx, source.ID); err != nil {
		t.Fatal(err)
	}
	var operation MarketplaceRefreshOperationRow
	if err := store.db.WithContext(ctx).Where("id = ?", "refresh-kept").First(&operation).Error; err != nil {
		t.Fatalf("source deletion removed refresh history: %v", err)
	}
	if _, err := store.DeleteMarketplaceSource(ctx, marketplace.OfficialSourceID); err == nil {
		t.Fatal("official source deletion was accepted")
	}
}

func TestApplyPluginMutationRejectsMismatchedInstalledIdentity(t *testing.T) {
	ctx := context.Background()
	store, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	install := PluginInstallTransaction{Package: PluginPackageRow{Digest: pluginTestDigest("a"), PluginID: "one.plugin", Version: "1.0.0", CachePath: "packages/a", ManifestJSON: "{}", ConfigSchemaJSON: "{}", VerifiedAt: now}, Installed: InstalledPluginRow{PluginID: "one.plugin", ActivePackageDigest: pluginTestDigest("a"), DesiredLifecycle: "disabled", CurrentLifecycle: "disabled", CleanupPolicyJSON: "{}", LastOperationID: "install", InstalledAt: now, UpdatedAt: now}, Operation: PluginOperationRow{ID: "install", PluginID: "one.plugin", Kind: "install", Status: "succeeded", AgentResultsJSON: "{}", ActorID: "admin", CreatedAt: now}, Audit: AuditEventRow{ID: "audit", Action: "plugin.install", TargetKind: "plugin", TargetID: "one.plugin", Result: "success", MetadataJSON: "{}", CreatedAt: now}}
	if err := store.InstallPlugin(ctx, install); err != nil {
		t.Fatal(err)
	}
	wrong := install.Installed
	wrong.PluginID = "other.plugin"
	err = store.ApplyPluginMutation(ctx, PluginMutation{PluginID: "one.plugin", Installed: &wrong, Operation: PluginOperationRow{ID: "bad", PluginID: "one.plugin", Kind: "configure", Status: "failed", AgentResultsJSON: "{}", ActorID: "admin", CreatedAt: now}, Audit: AuditEventRow{ID: "bad-audit", Action: "plugin.configure", TargetKind: "plugin", TargetID: "one.plugin", Result: "failure", MetadataJSON: "{}", CreatedAt: now}})
	if err == nil {
		t.Fatal("mismatched installed plugin identity was accepted")
	}
}

func TestMarketplacePromotionAndRefreshCompletionAreAtomic(t *testing.T) {
	ctx := context.Background()
	store, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	source, _ := marketplace.NewCustomSource("atomic", "Atomic", "https://example.com/plugins.git", "main", "", 0)
	now := time.Now().UTC()
	stable := marketplace.Snapshot{ID: "stable", SourceID: source.ID, Commit: "stable-commit", Path: "stable", ValidatedAt: now}
	if err := store.PromoteSnapshot(ctx, source, stable); err != nil {
		t.Fatal(err)
	}
	op := marketplace.RefreshOperation{ID: "refresh-next", SourceID: source.ID, Commit: "next-commit", Status: "running", StartedAt: now, LeaseToken: "lease-next", LeaseExpiresAt: now.Add(2 * time.Minute)}
	if err := store.AcquireRefreshLease(ctx, op); err != nil {
		t.Fatal(err)
	}
	reserveMarketplaceSnapshotForTest(t, store, source.ID, op.ID, "next")
	if err := store.db.Exec(`CREATE TRIGGER fail_refresh_completion BEFORE UPDATE OF status ON marketplace_refresh_operations WHEN NEW.status = 'succeeded' BEGIN SELECT RAISE(ABORT, 'injected completion failure'); END`).Error; err != nil {
		t.Fatal(err)
	}
	finished := now.Add(time.Minute)
	op.Status, op.FinishedAt = "succeeded", &finished
	next := marketplace.Snapshot{ID: "next", SourceID: source.ID, Commit: op.Commit, Path: "next", ValidatedAt: finished}
	if err := store.PromoteSnapshotAndCompleteRefresh(ctx, source, next, op); err == nil {
		t.Fatal("injected refresh completion failure was ignored")
	}
	current, ok, err := store.CurrentSnapshot(ctx, source.ID)
	if err != nil || !ok || current.ID != stable.ID {
		t.Fatalf("failed atomic promotion changed current snapshot: %+v, %v, %v", current, ok, err)
	}
	var persisted MarketplaceRefreshOperationRow
	if err := store.db.Where("id = ?", op.ID).First(&persisted).Error; err != nil || persisted.Status != "running" {
		t.Fatalf("failed atomic promotion changed operation: %+v, %v", persisted, err)
	}
}

func TestMarketplaceRefreshLeaseRecoversExpiredAndDeleteCannotResurrectSource(t *testing.T) {
	ctx := context.Background()
	store, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	source, _ := marketplace.NewCustomSource("leased", "Leased", "https://example.com/plugins.git", "main", "", 0)
	if err := store.SaveMarketplaceSource(ctx, source); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	expired := marketplace.RefreshOperation{ID: "expired", SourceID: source.ID, Status: "running", StartedAt: now.Add(-2 * time.Hour), LeaseToken: "old-lease", LeaseExpiresAt: now.Add(-time.Hour)}
	if err := store.AcquireRefreshLease(ctx, expired); err != nil {
		t.Fatal(err)
	}
	abandonedDigest := pluginTestDigest("f")
	if err := store.db.Create(&PluginPackageStagingRow{SourceID: source.ID, OperationID: expired.ID, Digest: abandonedDigest, UpdatedAt: expired.StartedAt}).Error; err != nil {
		t.Fatal(err)
	}
	fresh := marketplace.RefreshOperation{ID: "fresh", SourceID: source.ID, Commit: "next", Status: "running", StartedAt: now, LeaseToken: "new-lease", LeaseExpiresAt: now.Add(time.Minute)}
	if err := store.AcquireRefreshLease(ctx, fresh); err != nil {
		t.Fatalf("expired lease was not recoverable: %v", err)
	}
	var abandonedCount, intentCount int64
	if err := store.db.Model(&PluginPackageStagingRow{}).Where("operation_id = ?", expired.ID).Count(&abandonedCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Model(&PluginCacheGCIntentRow{}).Where("source_id = ? AND digest = ?", source.ID, abandonedDigest).Count(&intentCount).Error; err != nil {
		t.Fatal(err)
	}
	if abandonedCount != 0 || intentCount != 1 {
		t.Fatalf("expired refresh reaper = staging %d, intents %d", abandonedCount, intentCount)
	}
	var interrupted MarketplaceRefreshOperationRow
	if err := store.db.Where("id = ?", expired.ID).First(&interrupted).Error; err != nil || interrupted.Status != "failed" || interrupted.ErrorClass != "interrupted" {
		t.Fatalf("expired operation was not terminally recovered: %+v, %v", interrupted, err)
	}
	expired.Status, expired.ErrorClass, expired.Error = "failed", "stale_worker", "late failure"
	lateFinished := now.Add(time.Second)
	expired.FinishedAt = &lateFinished
	if err := store.SaveRefreshOperation(ctx, expired); err == nil {
		t.Fatal("stale refresh worker rewrote its terminal operation")
	}
	leasedSource, ok, err := store.GetMarketplaceSource(ctx, source.ID)
	if err != nil || !ok || leasedSource.LastResult != "running" {
		t.Fatalf("stale refresh clobbered current lease source status: %+v, %v, %v", leasedSource, ok, err)
	}
	if _, err := store.DeleteMarketplaceSource(ctx, source.ID); err != nil {
		t.Fatal(err)
	}
	finished := now.Add(30 * time.Second)
	fresh.Status, fresh.FinishedAt = "succeeded", &finished
	snapshot := marketplace.Snapshot{ID: "new", SourceID: source.ID, Commit: fresh.Commit, Path: "new", ValidatedAt: finished}
	if err := store.PromoteSnapshotAndCompleteRefresh(ctx, source, snapshot, fresh); err == nil {
		t.Fatal("refresh promotion recreated a concurrently deleted source")
	}
	if _, ok, err := store.GetMarketplaceSource(ctx, source.ID); err != nil || ok {
		t.Fatalf("deleted source was resurrected: %v, %v", ok, err)
	}
	fresh.Status, fresh.ErrorClass, fresh.Error = "failed", "source_deleted", "marketplace source was deleted"
	if err := store.SaveRefreshOperation(ctx, fresh); err != nil {
		t.Fatalf("deleted-source refresh could not reach terminal failure: %v", err)
	}
	var terminal MarketplaceRefreshOperationRow
	if err := store.db.Where("id = ?", fresh.ID).First(&terminal).Error; err != nil || terminal.Status != "failed" {
		t.Fatalf("refresh terminal operation = %+v, %v", terminal, err)
	}
}

func TestCatalogAcquisitionSurvivesConcurrentFailedRefreshAndTracksCurrentSnapshot(t *testing.T) {
	ctx := context.Background()
	store, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	source, _ := marketplace.NewSignedCustomSource("membership", "Membership", "https://example.com/plugins.git", "main", "", 0, marketplace.SourceSigner{KeyID: "membership-release", SecretRef: "vault-membership", PublicKey: base64.StdEncoding.EncodeToString(make([]byte, 32))})
	trust, err := source.SignatureTrust()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	digest := pluginTestDigest("a")
	snapshot := marketplace.Snapshot{ID: "one", SourceID: source.ID, Commit: "one", Path: filepath.Join(store.dataRoot, "marketplace", "snapshots", source.ID, "one"), ValidatedAt: now, Entries: []plugins.MarketEntry{{ID: "one.plugin", Version: "1.0.0", PackageSHA256: digest, SignatureKeyID: trust.KeyID}}}
	if err := store.PromoteSnapshot(ctx, source, snapshot); err != nil {
		t.Fatal(err)
	}
	validate := func() error {
		candidate := PluginPackageRow{Digest: digest, SourceID: source.ID, SourceKind: source.Kind, SignatureKeyID: trust.KeyID, SignaturePublicKey: trust.PublicKey, SignatureFingerprint: trust.Fingerprint}
		return store.writeTransaction(ctx, func(tx *gorm.DB) error { return validatePackageAcquisitionTx(tx, source.ID, digest, candidate) })
	}
	if err := validate(); err != nil {
		t.Fatal(err)
	}
	refresh := marketplace.RefreshOperation{ID: "refresh-same", SourceID: source.ID, Status: "running", StartedAt: now.Add(time.Second), LeaseToken: "lease-same", LeaseExpiresAt: now.Add(time.Minute)}
	if err := store.AcquireRefreshLease(ctx, refresh); err != nil {
		t.Fatal(err)
	}
	if err := store.StagePackageAcquisition(ctx, source.ID, digest, refresh.ID); err != nil {
		t.Fatal(err)
	}
	if err := validate(); err != nil {
		t.Fatalf("staging refresh hid current catalog acquisition: %v", err)
	}
	if err := store.CompletePackageAcquisitions(ctx, source.ID, refresh.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := validate(); err != nil {
		t.Fatalf("failed refresh deleted current catalog acquisition: %v", err)
	}
	finished := now.Add(2 * time.Second)
	refresh.Status, refresh.ErrorClass, refresh.Error, refresh.FinishedAt = "failed", "validation", "fixture", &finished
	if err := store.SaveRefreshOperation(ctx, refresh); err != nil {
		t.Fatal(err)
	}
	completed, ok, err := store.GetMarketplaceSource(ctx, source.ID)
	if err != nil || !ok || !completed.LastCompletedAt.Equal(finished) {
		t.Fatalf("failed refresh completion baseline = %+v, %v, %v", completed, ok, err)
	}
	empty := marketplace.Snapshot{ID: "two", SourceID: source.ID, Commit: "two", Path: filepath.Join(store.dataRoot, "marketplace", "snapshots", source.ID, "two"), ValidatedAt: now.Add(3 * time.Second)}
	if err := store.PromoteSnapshot(ctx, source, empty); err != nil {
		t.Fatal(err)
	}
	if err := validate(); err == nil {
		t.Fatal("package removed from current snapshot retained install acquisition")
	}
	intents, err := store.ListPackageGCIntents(ctx)
	if err != nil || len(intents) != 1 || intents[0].Digest != digest || intents[0].SourceID != source.ID {
		t.Fatalf("retired catalog digest GC intents = %+v, %v", intents, err)
	}
}

func TestPackageGCClaimTokenRejectsConcurrentAndStaleCompletion(t *testing.T) {
	ctx := context.Background()
	store, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	source, _ := marketplace.NewSignedCustomSource("gc-token", "GC Token", "https://example.com/plugins.git", "main", "", 0, marketplace.SourceSigner{
		KeyID: "gc-token-release", SecretRef: "vault-gc-token", PublicKey: base64.StdEncoding.EncodeToString(make([]byte, ed25519.PublicKeySize)),
	})
	if err := store.SaveMarketplaceSource(ctx, source); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	refresh := marketplace.RefreshOperation{ID: "gc-refresh", SourceID: source.ID, Status: "running", StartedAt: now, LeaseToken: "gc-lease", LeaseExpiresAt: now.Add(time.Minute)}
	if err := store.AcquireRefreshLease(ctx, refresh); err != nil {
		t.Fatal(err)
	}
	digest := pluginTestDigest("d")
	if err := store.StagePackageAcquisition(ctx, source.ID, digest, refresh.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.CompletePackageAcquisitions(ctx, source.ID, refresh.ID, false); err != nil {
		t.Fatal(err)
	}
	first, ok, err := store.ClaimPackageGC(ctx, source.ID, digest, source.SignerFingerprint)
	if err != nil || !ok {
		t.Fatalf("first claim = %+v, %v, %v", first, ok, err)
	}
	if _, ok, err := store.ClaimPackageGC(ctx, source.ID, digest, source.SignerFingerprint); err != nil || ok {
		t.Fatalf("live second claim = %v, %v", ok, err)
	}
	stale := first
	stale.Token = "wrong-token"
	if err := store.CompletePackageGC(ctx, stale, ""); err == nil {
		t.Fatal("foreign claim completed package GC")
	}
	if err := store.CompletePackageGC(ctx, first, "injected failure"); err != nil {
		t.Fatal(err)
	}
	second, ok, err := store.ClaimPackageGC(ctx, source.ID, digest, source.SignerFingerprint)
	if err != nil || !ok {
		t.Fatalf("retry claim = %+v, %v, %v", second, ok, err)
	}
	if err := store.CompletePackageGC(ctx, first, ""); err == nil {
		t.Fatal("stale success completed a newer package GC claim")
	}
	if err := store.CompletePackageGC(ctx, second, ""); err != nil {
		t.Fatal(err)
	}
}

func TestPackageGCIsolatesSameDigestSourceSignerVariant(t *testing.T) {
	ctx := context.Background()
	store, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte("variant-gc")))
	now := time.Now().UTC()
	firstFingerprint := fmt.Sprintf("%x", sha256.Sum256([]byte("signer-a")))
	secondFingerprint := fmt.Sprintf("%x", sha256.Sum256([]byte("signer-b")))
	firstIdentity := PluginPackageIdentity(digest, "source-a", firstFingerprint)
	secondIdentity := PluginPackageIdentity(digest, "source-b", secondFingerprint)
	for _, row := range []PluginPackageRow{
		{Identity: firstIdentity, Digest: digest, PluginID: "variant.gc", Version: "1.0.0", SourceID: "source-a", SignatureFingerprint: firstFingerprint, CachePath: "packages/a", ManifestJSON: `{}`, ConfigSchemaJSON: `{}`, VerifiedAt: now},
		{Identity: secondIdentity, Digest: digest, PluginID: "variant.gc", Version: "1.0.0", SourceID: "source-b", SignatureFingerprint: secondFingerprint, CachePath: "packages/b", ManifestJSON: `{}`, ConfigSchemaJSON: `{}`, VerifiedAt: now},
	} {
		if err := store.db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
		if err := store.db.Create(&PluginArtifactRow{ID: pluginStorageDigest(row.Identity, "artifacts/policy.wasm"), PackageIdentity: row.Identity, PackageDigest: digest, Path: "artifacts/policy.wasm", SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(row.Identity))), SizeBytes: 1}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := store.db.Create(&PluginPackageAcquisitionRow{SourceID: "source-b", Digest: digest, Status: "catalog", UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&InstalledPluginRow{PluginID: "variant.gc", ActivePackageDigest: digest, ActivePackageIdentity: secondIdentity, ActiveSourceID: "source-b", DesiredLifecycle: "disabled", CurrentLifecycle: "disabled", CleanupPolicyJSON: `{}`, LastOperationID: "install", StateVersion: 1, InstalledAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.writeTransaction(ctx, func(tx *gorm.DB) error { return schedulePackageGCTx(tx, "source-a", digest, firstFingerprint, now) }); err != nil {
		t.Fatal(err)
	}
	claim, ok, err := store.ClaimPackageGC(ctx, "source-a", digest, firstFingerprint)
	if err != nil || !ok {
		t.Fatalf("claim isolated signer variant = %+v, %v, %v", claim, ok, err)
	}
	if err := store.CompletePackageGC(ctx, claim, ""); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.GetPluginPackageByIdentity(ctx, firstIdentity); err != nil || ok {
		t.Fatalf("collected signer variant remains = %v, %v", ok, err)
	}
	if _, ok, err := store.GetPluginPackageByIdentity(ctx, secondIdentity); err != nil || !ok {
		t.Fatalf("active signer variant was collected = %v, %v", ok, err)
	}
	if artifacts, err := store.ListPluginArtifactsByIdentity(ctx, firstIdentity); err != nil || len(artifacts) != 0 {
		t.Fatalf("collected signer artifacts remain = %+v, %v", artifacts, err)
	}
	if artifacts, err := store.ListPluginArtifactsByIdentity(ctx, secondIdentity); err != nil || len(artifacts) != 1 {
		t.Fatalf("active signer artifacts were collected = %+v, %v", artifacts, err)
	}
}

func TestPackageGCRestartDefersOnlyActiveRotatedSignerVariant(t *testing.T) {
	ctx := context.Background()
	dataRoot := t.TempDir()
	store, err := NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	firstKey := ed25519.NewKeyFromSeed([]byte("storage-migration-signing-seed!!"))
	secondSeed := sha256.Sum256([]byte("same-source-rotated-gc-signer"))
	secondKey := ed25519.NewKeyFromSeed(secondSeed[:])
	const sourceID = "rotated-source"
	type variant struct {
		fingerprint string
		identity    string
	}
	variants := make([]variant, 0, 2)
	digest := ""
	for index, signingKey := range []ed25519.PrivateKey{firstKey, secondKey} {
		staging := t.TempDir()
		candidateDigest := writeMigrationVerifiedPackage(t, staging, "rotated.gc", signingKey)
		if digest == "" {
			digest = candidateDigest
		} else if digest != candidateDigest {
			t.Fatalf("rotated signer changed canonical content digest: %s != %s", digest, candidateDigest)
		}
		publicKey := base64.StdEncoding.EncodeToString(signingKey.Public().(ed25519.PublicKey))
		fingerprint, err := marketplace.SourceSignerFingerprint(publicKey)
		if err != nil {
			t.Fatal(err)
		}
		cachePath, err := marketplace.SignerCachePath(filepath.Join(dataRoot, "plugins", "packages"), digest, fingerprint)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(staging, cachePath); err != nil {
			t.Fatal(err)
		}
		trust := marketplace.SignatureTrust{SourceID: sourceID, SourceKind: marketplace.SourceKindCustom, KeyID: "community-release", PublicKey: publicKey, Fingerprint: fingerprint}
		validator, err := marketplace.ValidatorForSignatureTrust(trust)
		if err != nil {
			t.Fatal(err)
		}
		validated, err := validator.ValidatePackage(cachePath, plugins.PackageExpectation{})
		if err != nil {
			t.Fatal(err)
		}
		manifestJSON, err := json.Marshal(validated.Manifest)
		if err != nil {
			t.Fatal(err)
		}
		row, artifacts, err := ProjectPluginPackage(PluginPackageRow{
			Digest: digest, PluginID: validated.Manifest.ID, Version: validated.Manifest.Version, SourceID: sourceID,
			SourceKind: marketplace.SourceKindCustom, SourceRiskLabel: marketplace.UntrustedRiskLabel,
			SignaturePublicKey: publicKey, SignatureFingerprint: fingerprint, CachePath: cachePath,
			ManifestJSON: string(manifestJSON), ConfigSchemaJSON: `{}`, VerifiedAt: time.Now().UTC(),
		}, validated.Manifest)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
		for artifactIndex := range artifacts {
			if err := store.db.Create(&artifacts[artifactIndex]).Error; err != nil {
				t.Fatal(err)
			}
		}
		variants = append(variants, variant{fingerprint: fingerprint, identity: row.Identity})
		if index > 0 && row.Identity == variants[0].identity {
			t.Fatal("rotated signer reused durable package identity")
		}
	}
	now := time.Now().UTC()
	if err := store.db.Create(&InstalledPluginRow{
		PluginID: "rotated.gc", ActivePackageDigest: digest, ActivePackageIdentity: variants[1].identity,
		ActiveSourceID: sourceID, ActiveSourceKind: marketplace.SourceKindCustom,
		RollbackPackageDigest: digest, RollbackPackageIdentity: variants[0].identity,
		RollbackSourceID: sourceID, RollbackSourceKind: marketplace.SourceKindCustom,
		RuntimeKind: "wasm-policy", RuntimeABI: "nre:policy/v1", HostScope: "agent",
		DesiredLifecycle: "disabled", CurrentLifecycle: "disabled", CleanupPolicyJSON: `{}`,
		LastOperationID: "install", StateVersion: 1, InstalledAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	for _, item := range variants {
		if err := store.writeTransaction(ctx, func(tx *gorm.DB) error {
			return schedulePackageGCTx(tx, sourceID, digest, item.fingerprint, now)
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatal(err)
	}
	intents, err := store.ListPackageGCIntents(ctx)
	if err != nil || len(intents) != 2 {
		t.Fatalf("rotated signer intents after restart = %+v, %v", intents, err)
	}
	if _, claimed, err := store.ClaimPackageGC(ctx, sourceID, digest, variants[0].fingerprint); err != nil || claimed {
		t.Fatalf("rollback signer claim = %v, %v, want deferred", claimed, err)
	}
	if _, claimed, err := store.ClaimPackageGC(ctx, sourceID, digest, variants[1].fingerprint); err != nil || claimed {
		t.Fatalf("active rotated signer claim = %v, %v, want deferred", claimed, err)
	}
	if err := store.db.Model(&InstalledPluginRow{}).Where("plugin_id = ?", "rotated.gc").Updates(map[string]any{
		"rollback_package_digest": "", "rollback_package_identity": "", "rollback_source_id": "", "rollback_source_kind": "",
	}).Error; err != nil {
		t.Fatal(err)
	}
	firstClaim, claimed, err := store.ClaimPackageGC(ctx, sourceID, digest, variants[0].fingerprint)
	if err != nil || !claimed {
		t.Fatalf("retired signer claim = %+v, %v, %v", firstClaim, claimed, err)
	}
	if err := store.CompletePackageGC(ctx, firstClaim, ""); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.GetPluginPackageByIdentity(ctx, variants[0].identity); err != nil || ok {
		t.Fatalf("retired signer variant after GC = %v, %v", ok, err)
	}
	if _, claimed, err = store.ClaimPackageGC(ctx, sourceID, digest, variants[1].fingerprint); err != nil || claimed {
		t.Fatalf("active rotated signer claim = %v, %v, want deferred", claimed, err)
	}
	if err := store.db.Where("plugin_id = ?", "rotated.gc").Delete(&InstalledPluginRow{}).Error; err != nil {
		t.Fatal(err)
	}
	secondClaim, claimed, err := store.ClaimPackageGC(ctx, sourceID, digest, variants[1].fingerprint)
	if err != nil || !claimed {
		t.Fatalf("unreferenced rotated signer claim = %+v, %v, %v", secondClaim, claimed, err)
	}
	if err := store.CompletePackageGC(ctx, secondClaim, ""); err != nil {
		t.Fatal(err)
	}
}

func TestExpiredGCClaimKeepsDigestFencedUntilQuarantineOwnerCompletes(t *testing.T) {
	ctx := context.Background()
	dataRoot := t.TempDir()
	store, err := NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	signingKey := ed25519.NewKeyFromSeed([]byte("storage-migration-signing-seed!!"))
	source, err := marketplace.NewSignedCustomSource("gc-expired", "GC Expired", "https://example.com/plugins.git", "main", "", 0, marketplace.SourceSigner{
		KeyID: "gc-expired-release", SecretRef: "vault-gc-expired", PublicKey: base64.StdEncoding.EncodeToString(signingKey.Public().(ed25519.PublicKey)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveMarketplaceSource(ctx, source); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	refresh := marketplace.RefreshOperation{ID: "gc-expired-refresh", SourceID: source.ID, Status: "running", StartedAt: now, LeaseToken: "gc-expired-lease", LeaseExpiresAt: now.Add(time.Minute)}
	if err := store.AcquireRefreshLease(ctx, refresh); err != nil {
		t.Fatal(err)
	}
	digest := pluginTestDigest("e")
	if err := store.StagePackageAcquisition(ctx, source.ID, digest, refresh.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.CompletePackageAcquisitions(ctx, source.ID, refresh.ID, false); err != nil {
		t.Fatal(err)
	}
	claim, ok, err := store.ClaimPackageGC(ctx, source.ID, digest, source.SignerFingerprint)
	if err != nil || !ok {
		t.Fatalf("claim = %+v, %v, %v", claim, ok, err)
	}
	cacheRoot := filepath.Join(dataRoot, "plugins", "packages")
	livePath, err := marketplace.SignerCachePath(cacheRoot, digest, source.SignerFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(livePath, 0o755); err != nil {
		t.Fatal(err)
	}
	object, err := marketplace.NewPackageGCObject(claim, marketplace.PackageGCLayoutSigner)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PreparePackageGCObjects(ctx, claim, []marketplace.PackageGCObject{object}); err != nil {
		t.Fatal(err)
	}
	if err := store.db.Model(&PluginDigestFenceRow{}).Where("digest = ?", digest).Update("claim_expires_at", now.Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Model(&PluginCacheGCIntentRow{}).Where("source_id = ? AND digest = ?", source.ID, digest).Update("claim_expires_at", now.Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	expiredMutationRan := false
	if err := store.WithPackageGCMutation(ctx, claim, func() error {
		expiredMutationRan = true
		return nil
	}); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("expired claim mutation renewal = %v, want stale", err)
	}
	if expiredMutationRan {
		t.Fatal("expired claim renewed itself inside the mutation critical section")
	}
	if err := store.CompletePackageGC(ctx, claim, "expired failure"); err == nil {
		t.Fatal("expired claim recorded failure before takeover")
	}
	if err := store.StagePackageAcquisition(ctx, source.ID, digest, refresh.ID); err == nil || !strings.Contains(err.Error(), "being deleted") {
		t.Fatalf("expired deleting digest was reacquired: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatal(err)
	}
	replacement, ok, err := store.ClaimPackageGC(ctx, source.ID, digest, source.SignerFingerprint)
	if err != nil || !ok || replacement.Token == claim.Token || replacement.QuarantineID != claim.QuarantineID || !replacement.ObjectsPrepared || len(replacement.Objects) != 1 || replacement.Objects[0] != object {
		t.Fatalf("replacement claim = %+v, %v, %v", replacement, ok, err)
	}
	if recoveredPath, err := marketplace.PackageGCQuarantinePath(replacement); err != nil || recoveredPath != object.QuarantinePath {
		t.Fatalf("recovered quarantine ownership = %q, %v", recoveredPath, err)
	}
	if err := store.CompletePackageGC(ctx, claim, ""); err == nil {
		t.Fatal("expired worker completed replacement GC lease")
	}
	if err := store.CompletePackageGC(ctx, claim, "late failure"); err == nil {
		t.Fatal("expired worker overwrote replacement GC lease with failure")
	}
	if err := store.WithPackageGCMutation(ctx, replacement, func() error { return os.RemoveAll(livePath) }); err != nil {
		t.Fatal(err)
	}
	if err := store.CompletePackageGC(ctx, replacement, ""); err != nil {
		t.Fatal(err)
	}
	if err := store.StagePackageAcquisition(ctx, source.ID, digest, refresh.ID); err != nil {
		t.Fatalf("reacquire replacement-completed variant: %v", err)
	}
	marker := filepath.Join(livePath, "republished")
	if err := os.MkdirAll(livePath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte("new generation"), 0o600); err != nil {
		t.Fatal(err)
	}
	staleMutationRan := false
	if err := store.WithPackageGCMutation(ctx, claim, func() error {
		staleMutationRan = true
		return os.RemoveAll(livePath)
	}); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("expired prepared worker mutation = %v, want stale", err)
	}
	if staleMutationRan {
		t.Fatal("expired prepared worker entered the cache mutation critical section")
	}
	if err := store.CompletePackageGC(ctx, claim, "late mutation failure"); err == nil {
		t.Fatal("expired prepared worker recorded failure after reacquisition")
	}
	if contents, err := os.ReadFile(marker); err != nil || string(contents) != "new generation" {
		t.Fatalf("expired prepared worker changed republished cache: %q, %v", contents, err)
	}
}

func TestSnapshotPromotionRetiresMetadataIntoDurableDirectoryWork(t *testing.T) {
	ctx := context.Background()
	store, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	source, _ := marketplace.NewCustomSource("retention", "Retention", "https://example.com/plugins.git", "main", "", 0)
	now := time.Now().UTC()
	firstPath := filepath.Join(store.dataRoot, "marketplace", "snapshots", source.ID, "first")
	secondPath := filepath.Join(store.dataRoot, "marketplace", "snapshots", source.ID, "second")
	for _, candidate := range []string{firstPath, secondPath} {
		if err := os.MkdirAll(candidate, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.PromoteSnapshot(ctx, source, marketplace.Snapshot{ID: "first", SourceID: source.ID, Commit: "one", Path: firstPath, ValidatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.PromoteSnapshot(ctx, source, marketplace.Snapshot{ID: "second", SourceID: source.ID, Commit: "two", Path: secondPath, ValidatedAt: now.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	var snapshots int64
	if err := store.db.Model(&MarketSnapshotRow{}).Where("source_id = ?", source.ID).Count(&snapshots).Error; err != nil || snapshots != 1 {
		t.Fatalf("retained snapshot rows = %d, %v", snapshots, err)
	}
	works, err := store.ListMarketplaceDirectoryCleanup(ctx)
	if err != nil || len(works) != 1 || !strings.HasSuffix(works[0].Path, "/first") {
		t.Fatalf("retired snapshot work = %+v, %v", works, err)
	}
}

func TestMarketplaceDirectoryCleanupClaimsOnlyAbandonedWork(t *testing.T) {
	ctx := context.Background()
	store, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	source, _ := marketplace.NewCustomSource("claims", "Claims", "https://example.com/plugins.git", "main", "", 0)
	if err := store.SaveMarketplaceSource(ctx, source); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	operation := marketplace.RefreshOperation{ID: "refresh-claim", SourceID: source.ID, Status: "running", StartedAt: now, LeaseToken: "lease-claim", LeaseExpiresAt: now.Add(time.Minute)}
	if err := store.AcquireRefreshLease(ctx, operation); err != nil {
		t.Fatal(err)
	}
	staging := filepath.Join(store.dataRoot, "marketplace", "staging", source.ID, operation.ID)
	snapshotPath := filepath.Join(store.dataRoot, "marketplace", "snapshots", source.ID, "candidate")
	if err := store.RegisterMarketplaceDirectoryCleanup(ctx, source.ID, operation.ID, []string{staging, snapshotPath}); err != nil {
		t.Fatal(err)
	}
	if work, ok, err := store.ClaimMarketplaceDirectoryCleanup(ctx, source.ID, time.Minute); err != nil || ok {
		t.Fatalf("running refresh cleanup claimed: %+v, %v, %v", work, ok, err)
	}
	if err := store.AbandonMarketplaceRefresh(ctx, source.ID, operation.ID, operation.LeaseToken, "timeout"); err != nil {
		t.Fatal(err)
	}
	completed, ok, err := store.GetMarketplaceSource(ctx, source.ID)
	if err != nil || !ok || completed.LastResult != "failed" || completed.LastCompletedAt.Before(now) {
		t.Fatalf("abandoned refresh completion baseline = %+v, %v, %v", completed, ok, err)
	}
	work, ok, err := store.ClaimMarketplaceDirectoryCleanup(ctx, source.ID, time.Minute)
	if err != nil || !ok || work.ClaimToken == "" {
		t.Fatalf("abandoned refresh cleanup claim = %+v, %v, %v", work, ok, err)
	}
	if err := store.CompleteMarketplaceDirectoryCleanup(ctx, work, "retry"); err != nil {
		t.Fatal(err)
	}
}

func TestRefreshLeaseCannotRenewAfterExpiryOrAbandonNewOwner(t *testing.T) {
	ctx := context.Background()
	store, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	source, _ := marketplace.NewCustomSource("lease-fence", "Lease Fence", "https://example.com/plugins.git", "main", "", 0)
	if err := store.SaveMarketplaceSource(ctx, source); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	old := marketplace.RefreshOperation{ID: "refresh-old", SourceID: source.ID, Status: "running", StartedAt: now, LeaseToken: "lease-old", LeaseExpiresAt: now.Add(time.Minute)}
	if err := store.AcquireRefreshLease(ctx, old); err != nil {
		t.Fatal(err)
	}
	past := now.Add(-time.Minute)
	if err := store.db.Model(&MarketplaceSourceRow{}).Where("id = ?", source.ID).Update("refresh_lease_expires_at", past).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Model(&MarketplaceRefreshOperationRow{}).Where("id = ?", old.ID).Update("lease_expires_at", past).Error; err != nil {
		t.Fatal(err)
	}
	old.LeaseExpiresAt = now.Add(2 * time.Minute)
	if err := store.RenewRefreshLease(ctx, old); err == nil {
		t.Fatal("expired refresh lease was revived")
	}
	var expired MarketplaceRefreshOperationRow
	if err := store.db.Where("id = ?", old.ID).First(&expired).Error; err != nil || expired.LeaseExpiresAt.After(now) {
		t.Fatalf("expired operation lease = %+v, %v", expired, err)
	}
	fresh := marketplace.RefreshOperation{ID: "refresh-fresh", SourceID: source.ID, Status: "running", StartedAt: now.Add(time.Second), LeaseToken: "lease-fresh", LeaseExpiresAt: now.Add(3 * time.Minute)}
	if err := store.AcquireRefreshLease(ctx, fresh); err != nil {
		t.Fatal(err)
	}
	if err := store.AbandonMarketplaceRefresh(ctx, source.ID, old.ID, old.LeaseToken, "timeout"); err != nil {
		t.Fatal(err)
	}
	current, ok, err := store.GetMarketplaceSource(ctx, source.ID)
	if err != nil || !ok || current.LastResult != "running" {
		t.Fatalf("stale abandon changed new lease source = %+v, %v, %v", current, ok, err)
	}
	var freshRow MarketplaceRefreshOperationRow
	if err := store.db.Where("id = ?", fresh.ID).First(&freshRow).Error; err != nil || freshRow.Status != "running" || freshRow.LeaseToken != fresh.LeaseToken {
		t.Fatalf("stale abandon changed new operation = %+v, %v", freshRow, err)
	}
}

func TestPromotionRequiresExactUnclaimedSnapshotReservation(t *testing.T) {
	ctx := context.Background()
	store, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	source, _ := marketplace.NewCustomSource("reservation", "Reservation", "https://example.com/plugins.git", "main", "", 0)
	if err := store.SaveMarketplaceSource(ctx, source); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	operation := marketplace.RefreshOperation{ID: "refresh-reservation", SourceID: source.ID, Commit: "commit", Status: "running", StartedAt: now, LeaseToken: "lease-reservation", LeaseExpiresAt: now.Add(time.Minute)}
	if err := store.AcquireRefreshLease(ctx, operation); err != nil {
		t.Fatal(err)
	}
	finished := now.Add(time.Second)
	operation.Status, operation.FinishedAt = "succeeded", &finished
	snapshot := marketplace.Snapshot{ID: "candidate", SourceID: source.ID, Commit: operation.Commit, Path: "reservation/candidate", ValidatedAt: finished}
	if err := store.PromoteSnapshotAndCompleteRefresh(ctx, source, snapshot, operation); !errors.Is(err, ErrPluginConflict) {
		t.Fatalf("missing reservation error = %v", err)
	}
	reserveMarketplaceSnapshotForTest(t, store, source.ID, operation.ID, snapshot.Path)
	if err := store.db.Model(&MarketplaceDirectoryCleanupRow{}).Where("operation_id = ?", operation.ID).Updates(map[string]any{"claim_token": "stale-cleaner", "claim_expires_at": now.Add(-time.Minute)}).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.PromoteSnapshotAndCompleteRefresh(ctx, source, snapshot, operation); !errors.Is(err, ErrPluginConflict) {
		t.Fatalf("claimed reservation error = %v", err)
	}
	if err := store.db.Model(&MarketplaceDirectoryCleanupRow{}).Where("operation_id = ?", operation.ID).Updates(map[string]any{"claim_token": "", "claim_expires_at": time.Time{}}).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.PromoteSnapshotAndCompleteRefresh(ctx, source, snapshot, operation); err != nil {
		t.Fatal(err)
	}
}

func TestPluginLifecycleRejectsConflictingStoredPackageMetadataBeforeOperation(t *testing.T) {
	ctx := context.Background()
	store, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	digest := pluginTestDigest("c")
	corrupt := PluginPackageRow{Digest: digest, PluginID: "attacker.plugin", Version: "9.9.9", CachePath: "wrong/cache", ManifestJSON: `{"id":"attacker.plugin"}`, ConfigSchemaJSON: `{}`, VerifiedAt: now}
	if err := store.db.Create(&corrupt).Error; err != nil {
		t.Fatal(err)
	}
	candidate := PluginPackageRow{Digest: digest, PluginID: "safe.plugin", Version: "1.0.0", CachePath: "verified/cache", ManifestJSON: `{"id":"safe.plugin","version":"1.0.0"}`, ConfigSchemaJSON: `{"type":"object"}`, VerifiedAt: now}
	install := PluginInstallTransaction{
		Package:   candidate,
		Installed: InstalledPluginRow{PluginID: candidate.PluginID, ActivePackageDigest: digest, DesiredLifecycle: "disabled", CurrentLifecycle: "disabled", CleanupPolicyJSON: `{}`, LastOperationID: "install-conflict", InstalledAt: now, UpdatedAt: now},
		Operation: PluginOperationRow{ID: "install-conflict", PluginID: candidate.PluginID, Kind: "install", Status: "succeeded", AgentResultsJSON: `{}`, ActorID: "admin", CreatedAt: now},
		Audit:     AuditEventRow{ID: "audit-install-conflict", Action: "plugin.install", TargetKind: "plugin", TargetID: candidate.PluginID, Result: "failure", MetadataJSON: `{}`, CreatedAt: now},
	}
	if err := store.InstallPlugin(ctx, install); !errors.Is(err, ErrPluginConflict) {
		t.Fatalf("conflicting install package error = %v", err)
	}
	var installedCount, operationCount int64
	_ = store.db.Model(&InstalledPluginRow{}).Where("plugin_id = ?", candidate.PluginID).Count(&installedCount).Error
	_ = store.db.Model(&PluginOperationRow{}).Where("id = ?", install.Operation.ID).Count(&operationCount).Error
	if installedCount != 0 || operationCount != 0 {
		t.Fatalf("conflicting install persisted lifecycle rows: installed=%d operations=%d", installedCount, operationCount)
	}
	activeDigest := pluginTestDigest("a")
	installed := InstalledPluginRow{PluginID: candidate.PluginID, ActivePackageDigest: activeDigest, DesiredLifecycle: "enabled", CurrentLifecycle: "enabled", CleanupPolicyJSON: `{}`, LastOperationID: "installed", StateVersion: 1, InstalledAt: now, UpdatedAt: now}
	if err := store.db.Create(&installed).Error; err != nil {
		t.Fatal(err)
	}
	next := installed
	next.StagedPackageDigest = digest
	next.PendingOperationID = "upgrade-conflict"
	next.PendingKind = "upgrade"
	mutation := PluginMutation{PluginID: installed.PluginID, ExpectedActive: activeDigest, ExpectedStateVersion: 1, Installed: &next, Package: &candidate, Operation: PluginOperationRow{ID: "upgrade-conflict", PluginID: installed.PluginID, Kind: "upgrade", Status: "applying", AgentResultsJSON: `{}`, ActorID: "admin", CreatedAt: now}, Audit: AuditEventRow{ID: "audit-upgrade-conflict", Action: "plugin.upgrade", TargetKind: "plugin", TargetID: installed.PluginID, Result: "failure", MetadataJSON: `{}`, CreatedAt: now}}
	if err := store.ApplyPluginMutation(ctx, mutation); !errors.Is(err, ErrPluginConflict) {
		t.Fatalf("conflicting upgrade package error = %v", err)
	}
	loaded, ok, err := store.GetInstalledPlugin(ctx, installed.PluginID)
	if err != nil || !ok || loaded.ActivePackageDigest != activeDigest || loaded.PendingOperationID != "" {
		t.Fatalf("conflicting upgrade changed active lifecycle = %+v, %v, %v", loaded, ok, err)
	}
}

func TestLegacyPackageProvenanceBackfillsLifecycleSlots(t *testing.T) {
	ctx := context.Background()
	store, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	digest := pluginTestDigest("e")
	if err := store.db.Exec(`INSERT INTO plugin_packages (digest,plugin_id,version,cache_path,manifest_json,config_schema_json,verified_at,source_id,source_kind,source_risk_label) VALUES (?,?,?,?,?,?,?,?,?,?)`, digest, "legacy.plugin", "1.0.0", "cache", "{}", "{}", now, "community", marketplace.SourceKindCustom, marketplace.UntrustedRiskLabel).Error; err != nil {
		t.Fatal(err)
	}
	installed := InstalledPluginRow{PluginID: "legacy.plugin", ActivePackageDigest: digest, RollbackPackageDigest: digest, DesiredLifecycle: "disabled", CurrentLifecycle: "disabled", CleanupPolicyJSON: "{}", LastOperationID: "legacy-install", StateVersion: 1, InstalledAt: now, UpdatedAt: now}
	if err := store.db.Create(&installed).Error; err != nil {
		t.Fatal(err)
	}
	if err := backfillPluginOwnershipAndAcquisitions(ctx, store.db, store.LocalAgentID()); err != nil {
		t.Fatal(err)
	}
	loaded, ok, err := store.GetInstalledPlugin(ctx, installed.PluginID)
	if err != nil || !ok || loaded.ActiveSourceID != "community" || loaded.ActiveSourceKind != marketplace.SourceKindCustom || loaded.RollbackSourceID != "community" {
		t.Fatalf("backfilled lifecycle provenance = %+v, %v, %v", loaded, ok, err)
	}
}

func TestMigrationRewritesPendingMarketplaceDeletionPathsAcrossDataRoots(t *testing.T) {
	ctx := context.Background()
	source, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = source.Close() })
	target, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = target.Close() })
	market, _ := marketplace.NewCustomSource("pending-delete", "Pending Delete", "https://example.com/delete.git", "main", "", 0)
	snapshotPath := filepath.Join(source.dataRoot, "marketplace", "snapshots", market.ID, "snapshot")
	if err := os.MkdirAll(snapshotPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(snapshotPath, "market.yaml"), []byte("schema_version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot := marketplace.Snapshot{ID: "pending-snapshot", SourceID: market.ID, Commit: "commit", Path: snapshotPath, ValidatedAt: time.Now().UTC()}
	if err := source.PromoteSnapshot(ctx, market, snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err := source.DeleteMarketplaceSource(ctx, market.ID); err != nil {
		t.Fatal(err)
	}
	absoluteJSON, _ := json.Marshal([]string{snapshotPath})
	if err := source.db.Model(&MarketplaceSourceDeletionRow{}).Where("source_id = ?", market.ID).Update("snapshot_paths_json", string(absoluteJSON)).Error; err != nil {
		t.Fatal(err)
	}
	if err := CopyDefaultMigrationRows(ctx, source, target); err != nil {
		t.Fatal(err)
	}
	var tombstone MarketplaceSourceDeletionRow
	if err := target.db.Where("source_id = ?", market.ID).First(&tombstone).Error; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(tombstone.SnapshotPathsJSON, source.dataRoot) || !strings.Contains(tombstone.SnapshotPathsJSON, "pending-delete/snapshot") {
		t.Fatalf("migrated deletion paths = %s", tombstone.SnapshotPathsJSON)
	}
	if _, err := os.Stat(filepath.Join(target.dataRoot, "marketplace", "snapshots", market.ID, "snapshot", "market.yaml")); err != nil {
		t.Fatalf("pending deletion directory was not migrated: %v", err)
	}
}

func TestAgentRebindPreservesPluginTargetGroupInvariant(t *testing.T) {
	ctx := context.Background()
	store, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	for _, groupID := range []string{"group-a", "group-b"} {
		if err := store.CreateResourceGroup(ctx, ResourceGroupRow{ID: groupID, Name: groupID, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	for _, agentID := range []string{"edge-a", "edge-b"} {
		if err := store.SaveAgent(ctx, AgentRow{ID: agentID, Version: "1.0.0", CapabilitiesJSON: `[]`}); err != nil {
			t.Fatal(err)
		}
		if err := store.BindResource(ctx, ResourceBindingRow{ID: agentID + "-binding", ResourceKind: "agent", ResourceID: agentID, ResourceGroupID: "group-a", UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	instance := PluginInstanceRow{ID: "multi-target", PluginID: "group.plugin", ResourceGroupID: "group-a", TargetJSON: `["edge-a","edge-b"]`, ConfigJSON: `{}`, StatusSummaryJSON: `{}`, CurrentState: "disabled", UpdatedAt: now}
	if err := store.db.Create(&instance).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&ResourceBindingRow{ID: "instance-binding", ResourceKind: "plugin_instance", ResourceID: instance.ID, ResourceGroupID: "group-a", UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.BindResource(ctx, ResourceBindingRow{ID: "edge-a-binding", ResourceKind: "agent", ResourceID: "edge-a", ResourceGroupID: "group-b", UpdatedAt: now.Add(time.Second)}); err == nil {
		t.Fatal("agent rebind created a cross-group plugin target")
	}
	binding, err := store.GetResourceBinding(ctx, "agent", "edge-a")
	if err != nil || binding.ResourceGroupID != "group-a" {
		t.Fatalf("failed rebind partially moved agent: %+v, %v", binding, err)
	}
	instance.TargetJSON = `["edge-a"]`
	if err := store.db.Model(&PluginInstanceRow{}).Where("id = ?", instance.ID).Update("target_json", instance.TargetJSON).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.BindResource(ctx, ResourceBindingRow{ID: "edge-a-binding", ResourceKind: "agent", ResourceID: "edge-a", ResourceGroupID: "group-b", UpdatedAt: now.Add(2 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	loaded, ok, err := store.GetPluginInstance(ctx, instance.ID)
	if err != nil || !ok || loaded.ResourceGroupID != "group-b" {
		t.Fatalf("single-target plugin did not follow agent ownership: %+v, %v, %v", loaded, ok, err)
	}
	pendingOnly := PluginInstanceRow{ID: "pending-only", PluginID: "group.plugin", ResourceGroupID: "group-b", TargetJSON: `["edge-a"]`, ConfigJSON: `{}`, PendingOperationID: "configure-pending", PendingResourceGroupID: "group-a", PendingTargetJSON: `["edge-b"]`, PendingConfigJSON: `{}`, StatusSummaryJSON: `{}`, CurrentState: "applying", UpdatedAt: now}
	if err := store.db.Create(&pendingOnly).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.BindResource(ctx, ResourceBindingRow{ID: "edge-b-binding", ResourceKind: "agent", ResourceID: "edge-b", ResourceGroupID: "group-b", UpdatedAt: now.Add(3 * time.Second)}); err == nil {
		t.Fatal("pending-only plugin target allowed a cross-group agent rebind")
	}
	edgeB, err := store.GetResourceBinding(ctx, "agent", "edge-b")
	if err != nil || edgeB.ResourceGroupID != "group-a" {
		t.Fatalf("failed pending-only rebind partially moved agent: %+v, %v", edgeB, err)
	}
	if err := store.db.Model(&PluginInstanceRow{}).Where("id = ?", pendingOnly.ID).Updates(map[string]any{"pending_operation_id": "", "pending_resource_group_id": "", "pending_target_json": ""}).Error; err != nil {
		t.Fatal(err)
	}
	upgradePending := PluginInstanceRow{ID: "upgrade-pending", PluginID: "group.plugin", ResourceGroupID: "group-a", TargetJSON: `["edge-b"]`, ConfigJSON: `{}`, PendingOperationID: "upgrade-operation", PendingConfigJSON: `{}`, StatusSummaryJSON: `{}`, CurrentState: "applying", UpdatedAt: now}
	if err := store.db.Create(&upgradePending).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.BindResource(ctx, ResourceBindingRow{ID: "edge-b-binding", ResourceKind: "agent", ResourceID: "edge-b", ResourceGroupID: "group-b", UpdatedAt: now.Add(4 * time.Second)}); err != nil {
		t.Fatalf("upgrade pending row with no pending scope blocked agent rebind: %v", err)
	}
	loadedUpgrade, ok, err := store.GetPluginInstance(ctx, upgradePending.ID)
	if err != nil || !ok || loadedUpgrade.ResourceGroupID != "group-b" {
		t.Fatalf("upgrade pending active target did not follow agent ownership: %+v, %v, %v", loadedUpgrade, ok, err)
	}
}

func TestCustomLocalAgentIDDrivesLegacyDefaultTargetRebind(t *testing.T) {
	ctx := context.Background()
	store, err := NewSQLiteStore(t.TempDir(), "embedded-custom")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	if err := store.CreateResourceGroup(ctx, ResourceGroupRow{ID: "group-b", Name: "group-b", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAgent(ctx, AgentRow{ID: "embedded-custom", Version: "1.0.0", CapabilitiesJSON: `[]`, IsLocal: true}); err != nil {
		t.Fatal(err)
	}
	if err := store.BindResource(ctx, ResourceBindingRow{ID: "custom-binding", ResourceKind: "agent", ResourceID: "embedded-custom", ResourceGroupID: "default", UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	instance := PluginInstanceRow{ID: "legacy-default-target", PluginID: "group.plugin", ResourceGroupID: "default", TargetJSON: `null`, ConfigJSON: `{}`, StatusSummaryJSON: `{}`, CurrentState: "disabled", UpdatedAt: now}
	if err := store.db.Create(&instance).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&ResourceBindingRow{ID: "instance-binding", ResourceKind: "plugin_instance", ResourceID: instance.ID, ResourceGroupID: "default", ParentResourceKind: "agent", ParentResourceID: "embedded-custom", UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.BindResource(ctx, ResourceBindingRow{ID: "custom-binding", ResourceKind: "agent", ResourceID: "embedded-custom", ResourceGroupID: "group-b", UpdatedAt: now.Add(time.Second)}); err != nil {
		t.Fatalf("custom local default target rebind failed: %v", err)
	}
	loaded, ok, err := store.GetPluginInstance(ctx, instance.ID)
	if err != nil || !ok || loaded.ResourceGroupID != "group-b" {
		t.Fatalf("legacy default-target instance did not follow custom local rebind: %+v, %v, %v", loaded, ok, err)
	}
	binding, err := store.GetResourceBinding(ctx, "plugin_instance", instance.ID)
	if err != nil || binding.ResourceGroupID != "group-b" || binding.ParentResourceID != "embedded-custom" {
		t.Fatalf("legacy default-target binding = %+v, %v", binding, err)
	}
}

func TestPluginUninstallQuotaRecomputeRollsBackWithLaterFailure(t *testing.T) {
	ctx := context.Background()
	store, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	pluginID, instanceID := "rollback.cleanup", "rollback-instance"
	installed := InstalledPluginRow{PluginID: pluginID, ActivePackageDigest: pluginTestDigest("cleanup"), DesiredLifecycle: "disabled", CurrentLifecycle: "disabled", CleanupPolicyJSON: `{}`, LastOperationID: "install", StateVersion: 1, InstalledAt: now, UpdatedAt: now}
	instance := PluginInstanceRow{ID: instanceID, PluginID: pluginID, ResourceGroupID: "default", TargetJSON: `["local"]`, ConfigJSON: `{}`, StatusSummaryJSON: `{}`, CurrentState: "disabled", StateVersion: 1, UpdatedAt: now}
	allocation := QuotaAllocationRow{ID: "rollback-allocation", ResourceKind: "plugin_instance", ResourceID: instanceID, Metric: "application_count", SubjectKind: "resource_group", SubjectID: "default", ResourceGroupID: "default", Amount: 1, CreatedAt: now}
	usage := QuotaUsageRow{ID: "rollback-usage", SubjectKind: "resource_group", SubjectID: "default", ResourceGroupID: "default", Metric: "application_count", Current: 1, UpdatedAt: now}
	for _, row := range []any{&installed, &instance, &allocation, &usage} {
		if err := store.db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := store.db.Exec(`CREATE TRIGGER fail_uninstall_operation BEFORE INSERT ON plugin_operations WHEN NEW.id = 'rollback-uninstall' BEGIN SELECT RAISE(ABORT, 'injected later uninstall failure'); END`).Error; err != nil {
		t.Fatal(err)
	}
	operation := PluginOperationRow{ID: "rollback-uninstall", PluginID: pluginID, Kind: "uninstall", Status: "succeeded", AgentResultsJSON: `{}`, ActorID: "admin", CreatedAt: now}
	err = store.ApplyPluginMutation(ctx, PluginMutation{PluginID: pluginID, ExpectedStateVersion: 1, DeletePlugin: true, DeleteInstances: true, Operation: operation, Audit: AuditEventRow{ID: "rollback-uninstall-audit", ActorID: "admin", Action: "plugin.uninstall", TargetKind: "plugin", TargetID: pluginID, Result: "success", MetadataJSON: `{}`, CreatedAt: now}})
	if err == nil {
		t.Fatal("injected later uninstall failure was ignored")
	}
	if _, ok, err := store.GetInstalledPlugin(ctx, pluginID); err != nil || !ok {
		t.Fatalf("failed uninstall removed plugin state: %v, %v", ok, err)
	}
	if _, ok, err := store.GetPluginInstance(ctx, instanceID); err != nil || !ok {
		t.Fatalf("failed uninstall removed plugin instance: %v, %v", ok, err)
	}
	var allocationCount int64
	if err := store.db.Model(&QuotaAllocationRow{}).Where("id = ?", allocation.ID).Count(&allocationCount).Error; err != nil || allocationCount != 1 {
		t.Fatalf("failed uninstall allocation count = %d, %v", allocationCount, err)
	}
	var persistedUsage QuotaUsageRow
	if err := store.db.Where("id = ?", usage.ID).First(&persistedUsage).Error; err != nil || persistedUsage.Current != 1 {
		t.Fatalf("failed uninstall quota usage = %+v, %v", persistedUsage, err)
	}
}

func TestPluginUninstallQuotaRecomputeClearsAllocationAndUsage(t *testing.T) {
	ctx := context.Background()
	store, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	pluginID, instanceID := "success.cleanup", "success-instance"
	installed := InstalledPluginRow{PluginID: pluginID, ActivePackageDigest: pluginTestDigest("success-cleanup"), DesiredLifecycle: "disabled", CurrentLifecycle: "disabled", CleanupPolicyJSON: `{}`, LastOperationID: "install", StateVersion: 1, InstalledAt: now, UpdatedAt: now}
	instance := PluginInstanceRow{ID: instanceID, PluginID: pluginID, ResourceGroupID: "default", TargetJSON: `["local"]`, ConfigJSON: `{}`, StatusSummaryJSON: `{}`, CurrentState: "disabled", StateVersion: 1, UpdatedAt: now}
	allocation := QuotaAllocationRow{ID: "success-allocation", ResourceKind: "plugin_instance", ResourceID: instanceID, Metric: "application_count", SubjectKind: "resource_group", SubjectID: "default", ResourceGroupID: "default", Amount: 1, CreatedAt: now}
	usage := QuotaUsageRow{ID: "success-usage", SubjectKind: "resource_group", SubjectID: "default", ResourceGroupID: "default", Metric: "application_count", Current: 1, UpdatedAt: now}
	for _, row := range []any{&installed, &instance, &allocation, &usage} {
		if err := store.db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	operation := PluginOperationRow{ID: "success-uninstall", PluginID: pluginID, Kind: "uninstall", Status: "succeeded", AgentResultsJSON: `{}`, ActorID: "admin", CreatedAt: now}
	if err := store.ApplyPluginMutation(ctx, PluginMutation{PluginID: pluginID, ExpectedStateVersion: 1, DeletePlugin: true, DeleteInstances: true, Operation: operation, Audit: AuditEventRow{ID: "success-uninstall-audit", ActorID: "admin", Action: "plugin.uninstall", TargetKind: "plugin", TargetID: pluginID, Result: "success", MetadataJSON: `{}`, CreatedAt: now}}); err != nil {
		t.Fatal(err)
	}
	var allocationCount int64
	if err := store.db.Model(&QuotaAllocationRow{}).Where("id = ?", allocation.ID).Count(&allocationCount).Error; err != nil || allocationCount != 0 {
		t.Fatalf("successful uninstall allocation count = %d, %v", allocationCount, err)
	}
	var persistedUsage QuotaUsageRow
	if err := store.db.Where("id = ?", usage.ID).First(&persistedUsage).Error; err != nil || persistedUsage.Current != 0 {
		t.Fatalf("successful uninstall quota usage = %+v, %v", persistedUsage, err)
	}
	if _, ok, err := store.GetInstalledPlugin(ctx, pluginID); err != nil || ok {
		t.Fatalf("successful uninstall retained plugin: %v, %v", ok, err)
	}
	if _, ok, err := store.GetPluginInstance(ctx, instanceID); err != nil || ok {
		t.Fatalf("successful uninstall retained instance: %v, %v", ok, err)
	}
}

func TestPluginInstanceRowVersionRejectsLifecycleOverwriteAfterRebind(t *testing.T) {
	ctx := context.Background()
	store, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	installed := InstalledPluginRow{PluginID: "cas.plugin", ActivePackageDigest: pluginTestDigest("a"), DesiredLifecycle: "disabled", CurrentLifecycle: "disabled", CleanupPolicyJSON: `{}`, LastOperationID: "install", StateVersion: 1, InstalledAt: now, UpdatedAt: now}
	if err := store.db.Create(&installed).Error; err != nil {
		t.Fatal(err)
	}
	instance := PluginInstanceRow{ID: "cas-instance", PluginID: installed.PluginID, ResourceGroupID: "group-a", TargetJSON: `["edge"]`, ConfigJSON: `{}`, StatusSummaryJSON: `{}`, CurrentState: "disabled", StateVersion: 1, UpdatedAt: now}
	if err := store.db.Create(&instance).Error; err != nil {
		t.Fatal(err)
	}
	stale := instance
	if err := store.db.Model(&PluginInstanceRow{}).Where("id = ?", instance.ID).Updates(map[string]any{"resource_group_id": "group-b", "state_version": gorm.Expr("state_version + 1")}).Error; err != nil {
		t.Fatal(err)
	}
	installed.LastOperationID = "upgrade"
	mutation := PluginMutation{PluginID: installed.PluginID, ExpectedActive: installed.ActivePackageDigest, ExpectedStateVersion: installed.StateVersion, Installed: &installed, ReplaceInstances: []PluginInstanceRow{stale}, Operation: PluginOperationRow{ID: "upgrade", PluginID: installed.PluginID, Kind: "upgrade", Status: "applying", AgentResultsJSON: `{}`, CreatedAt: now}, Audit: AuditEventRow{ID: "upgrade-audit", Action: "plugin.upgrade", TargetKind: "plugin", TargetID: installed.PluginID, Result: "accepted", MetadataJSON: `{}`, CreatedAt: now}}
	if err := store.ApplyPluginMutation(ctx, mutation); err == nil || !strings.Contains(err.Error(), "instance changed concurrently") {
		t.Fatalf("stale lifecycle instance overwrite error = %v", err)
	}
	loaded, ok, err := store.GetPluginInstance(ctx, instance.ID)
	if err != nil || !ok || loaded.ResourceGroupID != "group-b" || loaded.StateVersion != 2 {
		t.Fatalf("rebound instance was overwritten: %+v, %v, %v", loaded, ok, err)
	}
}

func TestDuplicateMarketplaceSourcePreservesRuntimeStateAndAuditsWithoutCredential(t *testing.T) {
	ctx := WithQuotaActor(context.Background(), QuotaActor{UserID: "admin", SessionID: "session", CorrelationID: "request"})
	store, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	source, _ := marketplace.NewCustomSource("duplicate", "Duplicate", "https://example.com/plugins.git", "main", "vault-secret-ref", 0)
	if err := store.SaveMarketplaceSource(ctx, source); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	snapshot := marketplace.Snapshot{ID: "current", SourceID: source.ID, Commit: "commit", Path: "current", ValidatedAt: now}
	refresh := marketplace.RefreshOperation{ID: "refresh-audited", SourceID: source.ID, Commit: snapshot.Commit, Status: "running", StartedAt: now, Actor: marketplace.OperationActor{ActorID: "admin", SessionID: "session", CorrelationID: "request"}, LeaseToken: "lease-audited", LeaseExpiresAt: now.Add(time.Minute)}
	if err := store.AcquireRefreshLease(ctx, refresh); err != nil {
		t.Fatal(err)
	}
	reserveMarketplaceSnapshotForTest(t, store, source.ID, refresh.ID, snapshot.Path)
	refresh.Status, refresh.FinishedAt = "succeeded", &now
	if err := store.PromoteSnapshotAndCompleteRefresh(ctx, source, snapshot, refresh); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveMarketplaceSource(ctx, source); !errors.Is(err, ErrMarketplaceSourceExists) {
		t.Fatalf("duplicate source error = %v", err)
	}
	current, ok, err := store.CurrentSnapshot(ctx, source.ID)
	if err != nil || !ok || current.ID != snapshot.ID {
		t.Fatalf("duplicate source cleared current snapshot: %+v, %v, %v", current, ok, err)
	}
	var audits []AuditEventRow
	if err := store.db.Where("target_kind = ? AND target_id = ? AND action = ?", "marketplace_source", source.ID, "marketplace.source.add").Find(&audits).Error; err != nil || len(audits) == 0 {
		t.Fatalf("source audit missing: %+v, %v", audits, err)
	}
	for _, audit := range audits {
		if strings.Contains(audit.MetadataJSON, source.CredentialRef) || audit.ActorID != "admin" || audit.SessionID != "session" || audit.CorrelationID != "request" {
			t.Fatalf("source audit leaked credential or lost provenance: %+v", audit)
		}
	}
	var refreshAudit AuditEventRow
	if err := store.db.Where("action = ? AND target_id = ?", "marketplace.source.refresh", source.ID).First(&refreshAudit).Error; err != nil || refreshAudit.ActorID != "admin" || refreshAudit.SessionID != "session" || refreshAudit.CorrelationID != "request" || strings.Contains(refreshAudit.MetadataJSON, source.CredentialRef) {
		t.Fatalf("refresh audit lost provenance or leaked credential: %+v, %v", refreshAudit, err)
	}
}

func reserveMarketplaceSnapshotForTest(t *testing.T, store *GormStore, sourceID, operationID, candidate string) {
	t.Helper()
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(store.dataRoot, "marketplace", "snapshots", filepath.FromSlash(candidate))
	}
	if err := store.RegisterMarketplaceDirectoryCleanup(context.Background(), sourceID, operationID, []string{candidate}); err != nil {
		t.Fatal(err)
	}
}

func TestApplyPluginMutationUsesRowVersionCASForSameDigest(t *testing.T) {
	ctx := context.Background()
	store, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	digest := pluginTestDigest("c")
	install := PluginInstallTransaction{Package: PluginPackageRow{Digest: digest, PluginID: "cas.plugin", Version: "1.0.0", CachePath: "packages/c", ManifestJSON: "{}", ConfigSchemaJSON: "{}", VerifiedAt: now}, Installed: InstalledPluginRow{PluginID: "cas.plugin", ActivePackageDigest: digest, DesiredLifecycle: "disabled", CurrentLifecycle: "disabled", CleanupPolicyJSON: "{}", LastOperationID: "install-cas", StateVersion: 1, InstalledAt: now, UpdatedAt: now}, Operation: PluginOperationRow{ID: "install-cas", PluginID: "cas.plugin", Kind: "install", Status: "succeeded", AgentResultsJSON: "{}", ActorID: "admin", CreatedAt: now}, Audit: AuditEventRow{ID: "audit-cas", Action: "plugin.install", TargetKind: "plugin", TargetID: "cas.plugin", Result: "success", MetadataJSON: "{}", CreatedAt: now}}
	if err := store.InstallPlugin(ctx, install); err != nil {
		t.Fatal(err)
	}
	first := install.Installed
	first.DesiredLifecycle, first.CurrentLifecycle, first.LastOperationID = "enabled", "applying", "enable-cas"
	second := install.Installed
	second.DesiredLifecycle, second.CurrentLifecycle, second.LastOperationID = "disabled", "applying", "disable-cas"
	mutation := func(row *InstalledPluginRow, id, kind string) PluginMutation {
		return PluginMutation{PluginID: row.PluginID, ExpectedActive: digest, ExpectedStateVersion: 1, Installed: row, Operation: PluginOperationRow{ID: id, PluginID: row.PluginID, Kind: kind, Status: "applying", AgentResultsJSON: "{}", ActorID: "admin", CreatedAt: now}, Audit: AuditEventRow{ID: "audit-" + id, Action: "plugin." + kind, TargetKind: "plugin", TargetID: row.PluginID, Result: "accepted", MetadataJSON: "{}", CreatedAt: now}}
	}
	if err := store.ApplyPluginMutation(ctx, mutation(&first, "enable-cas", "enable")); err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyPluginMutation(ctx, mutation(&second, "disable-cas", "disable")); err == nil {
		t.Fatal("stale same-digest mutation overwrote a concurrent state transition")
	}
	persisted, _, _ := store.GetInstalledPlugin(ctx, install.Installed.PluginID)
	if persisted.DesiredLifecycle != "enabled" || persisted.StateVersion != 2 {
		t.Fatalf("CAS result = %+v", persisted)
	}
}

func pluginTestDigest(value string) string { return strings.Repeat(value, 64) }
