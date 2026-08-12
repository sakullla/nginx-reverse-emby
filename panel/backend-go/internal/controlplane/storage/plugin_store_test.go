package storage

import (
	"bytes"
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
	"github.com/sakullla/nginx-reverse-emby/plugin-sdk/go/compatfixture"
	"gorm.io/gorm"
)

func writeMigrationVerifiedPackage(t *testing.T, root, pluginID string, signingKey ed25519.PrivateKey) string {
	return writeMigrationVerifiedPolicyPackage(t, root, pluginID, "waf", "http.request", signingKey)
}

func writeMigrationVerifiedPolicyPackage(t *testing.T, root, pluginID, policyKind, extensionPoint string, signingKey ed25519.PrivateKey) string {
	return writeMigrationVerifiedPolicyPackageWithBudget(t, root, pluginID, policyKind, extensionPoint, 65536, signingKey)
}

func writeMigrationVerifiedPolicyPackageWithBudget(t *testing.T, root, pluginID, policyKind, extensionPoint string, inputBytes int64, signingKey ed25519.PrivateKey) string {
	t.Helper()
	artifact := compatfixture.PolicyV1GuestWASM()
	artifactDigest := sha256.Sum256(artifact)
	manifest := fmt.Sprintf(`schema_version: 1
id: %s
version: 1.0.0
name: Migration Fixture
compatibility: {host: "*", agent: "*"}
runtime: {kind: wasm-policy, abi: "nre:policy/v1", host_scope: agent, entry: artifacts/policy.wasm, policy_kind: %s}
artifacts:
  - {path: artifacts/policy.wasm, sha256: %x, size: %d, mode: wasm}
extension_points: [%s]
permissions: [http.inspect]
config_schema: config.schema.json
resource_budget: {timeout_ms: 2, memory_bytes: 1048576, concurrency: 1, input_bytes: %d, output_bytes: 4096}
failure_policy: {on_error: fail-open, on_budget: fail-open, restart: never, core_fallback: preserve}
signature: {algorithm: ed25519, key_id: community-release, file: package.sig}
cleanup: {instances: retain, config: retain, owned_data: retain, grants: retain, shared_refs: retain, audit_events: retain}
`, pluginID, policyKind, artifactDigest, len(artifact), extensionPoint, inputBytes)
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
			source, err := newStorageTestSQLiteStore(t, t.TempDir(), "local", true)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = source.Close() })
			target, err := newStorageTestSQLiteStore(t, t.TempDir(), "local", true)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = target.Close() })
			test.insert(t, source)

			var gcIntents int64
			if err := source.db.Model(&PluginCacheGCIntentRow{}).Count(&gcIntents).Error; err != nil || gcIntents != 0 {
				t.Fatalf("source GC intents = %d, %v", gcIntents, err)
			}
			if err := CopyDefaultMigrationRows(t.Context(), source, target); err == nil || (!strings.Contains(err.Error(), "no package candidate") && !strings.Contains(err.Error(), "0 candidates")) {
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

func TestLegacyOperationMigrationRejectsAmbiguousSameDigestSignersAtomically(t *testing.T) {
	source, err := newStorageTestSQLiteStore(t, t.TempDir(), "local", true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = source.Close() })
	target, err := newStorageTestSQLiteStore(t, t.TempDir(), "local", true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = target.Close() })
	digest := pluginTestDigest("ambiguous-operation-digest")
	now := time.Now().UTC()
	for index, sourceID := range []string{"ambiguous-source-a", "ambiguous-source-b"} {
		seed := sha256.Sum256([]byte(sourceID))
		publicKey := base64.StdEncoding.EncodeToString(ed25519.NewKeyFromSeed(seed[:]).Public().(ed25519.PublicKey))
		fingerprint, err := marketplace.SourceSignerFingerprint(publicKey)
		if err != nil {
			t.Fatal(err)
		}
		row := PluginPackageRow{Identity: PluginPackageIdentity(digest, sourceID, fingerprint), Digest: digest, PluginID: "ambiguous.operation", Version: fmt.Sprintf("1.0.%d", index), SourceID: sourceID, SourceKind: marketplace.SourceKindCustom, SourceRiskLabel: marketplace.UntrustedRiskLabel, SignatureKeyID: "community-release", SignaturePublicKey: publicKey, SignatureFingerprint: fingerprint, CachePath: filepath.Join("packages", sourceID), ManifestJSON: `{}`, ConfigSchemaJSON: `{}`, VerifiedAt: now}
		if err := source.db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	completed := now.Add(time.Second)
	if err := source.db.Create(&PluginOperationRow{ID: "ambiguous-operation", PluginID: "ambiguous.operation", Kind: "enable", Status: "succeeded", TargetPackageDigest: digest, AgentResultsJSON: `{}`, CreatedAt: now, CompletedAt: &completed}).Error; err != nil {
		t.Fatal(err)
	}
	err = CopyDefaultMigrationRows(t.Context(), source, target)
	if err == nil || !strings.Contains(err.Error(), "resolves to 2 candidates") {
		t.Fatalf("ambiguous same-digest signer migration error = %v", err)
	}
	for _, model := range []any{&PluginPackageRow{}, &PluginOperationRow{}} {
		var count int64
		if err := target.db.Model(model).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("target %T count after ambiguous migration = %d, %v", model, count, err)
		}
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

func TestPluginMigrationBatchFailureRollsBackOnlyNewCacheObjects(t *testing.T) {
	source, err := newStorageTestSQLiteStore(t, t.TempDir(), "local", true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = source.Close() })
	target, err := newStorageTestSQLiteStore(t, t.TempDir(), "local", true)
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

type preparedGCMigrationFixture struct {
	source, target         *GormStore
	sourceRoot, targetRoot string
	claim                  marketplace.PackageGCClaim
	objects                []marketplace.PackageGCObject
	identity               string
}

func newPreparedGCMigrationFixture(t *testing.T) preparedGCMigrationFixture {
	t.Helper()
	source, err := newStorageTestSQLiteStore(t, t.TempDir(), "local", true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = source.Close() })
	target, err := newStorageTestSQLiteStore(t, t.TempDir(), "local", true)
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

func TestConcurrentPluginInstallReturnsStableAlreadyInstalledConflict(t *testing.T) {
	store, err := newStorageTestSQLiteStore(t, t.TempDir(), "local", true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	digest := pluginTestDigest("c")
	now := time.Now().UTC()
	seed := sha256.Sum256([]byte("concurrent-install-signer"))
	publicKey := base64.StdEncoding.EncodeToString(ed25519.NewKeyFromSeed(seed[:]).Public().(ed25519.PublicKey))
	fingerprint, err := marketplace.SourceSignerFingerprint(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	input := func(suffix string) PluginInstallTransaction {
		packageRow := PluginPackageRow{Digest: digest, PluginID: "concurrent.install", Version: "1.0.0", SourceID: "concurrent-source", SourceKind: marketplace.SourceKindCustom, SourceRiskLabel: marketplace.UntrustedRiskLabel, SignatureKeyID: "community-release", SignaturePublicKey: publicKey, SignatureFingerprint: fingerprint, CachePath: "cache", ManifestJSON: `{}`, ConfigSchemaJSON: `{"type":"object"}`, VerifiedAt: now}
		packageRow.Identity = PluginPackageIdentity(digest, packageRow.SourceID, fingerprint)
		operation := PluginOperationRow{ID: "install-" + suffix, PluginID: "concurrent.install", Kind: "install", Status: "succeeded", AgentResultsJSON: `{}`, ActorID: "admin", CreatedAt: now, CompletedAt: &now}
		if err := BindPluginOperationPackage(&operation, packageRow); err != nil {
			panic(err)
		}
		return PluginInstallTransaction{
			Package:   packageRow,
			Installed: InstalledPluginRow{PluginID: "concurrent.install", ActivePackageDigest: digest, ActivePackageIdentity: packageRow.Identity, ActiveSourceID: packageRow.SourceID, ActiveSourceKind: packageRow.SourceKind, ActiveSourceRiskLabel: packageRow.SourceRiskLabel, ActiveSignatureKeyID: packageRow.SignatureKeyID, ActiveSignaturePublicKey: packageRow.SignaturePublicKey, ActiveSignatureFingerprint: packageRow.SignatureFingerprint, DesiredLifecycle: "disabled", CurrentLifecycle: "disabled", CleanupPolicyJSON: `{}`, LastOperationID: "install-" + suffix, InstalledAt: now, UpdatedAt: now},
			Operation: operation,
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

func TestMarketplacePromotionAndRefreshCompletionAreAtomic(t *testing.T) {
	ctx := context.Background()
	store, err := newStorageTestSQLiteStore(t, t.TempDir(), "local", true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	source := newSignedStorageMarketplaceSource(t, "atomic", "Atomic", "https://example.com/plugins.git", "main", "")
	now := time.Now().UTC()
	stable := marketplace.Snapshot{ID: "stable", SourceID: source.ID, Commit: strings.Repeat("b", 40), Path: "stable", ValidatedAt: now}
	if err := store.PromoteSnapshot(ctx, source, stable); err != nil {
		t.Fatal(err)
	}
	op := refreshOperationForSource(source, marketplace.RefreshOperation{ID: "refresh-next", SourceID: source.ID, Commit: strings.Repeat("c", 40), Status: "running", StartedAt: now, LeaseToken: "lease-next", LeaseExpiresAt: now.Add(2 * time.Minute)})
	if err := store.AcquireRefreshLease(ctx, op); err != nil {
		t.Fatal(err)
	}
	reserveMarketplaceSnapshotForTest(t, store, source.ID, op.ID, "next")
	if err := store.db.Exec(`CREATE TRIGGER fail_refresh_completion BEFORE UPDATE OF status ON marketplace_refresh_operations WHEN NEW.status = 'succeeded' BEGIN SELECT RAISE(ABORT, 'injected completion failure'); END`).Error; err != nil {
		t.Fatal(err)
	}
	finished := time.Now().UTC()
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
	if err := store.db.Exec(`DROP TRIGGER fail_refresh_completion`).Error; err != nil {
		t.Fatal(err)
	}
	renewal := op
	renewal.LeaseExpiresAt = time.Now().UTC().Add(2 * time.Minute)
	if err := store.RenewRefreshLease(ctx, renewal); err != nil {
		t.Fatal(err)
	}
	finished = time.Now().UTC()
	op.LeaseExpiresAt = finished.Add(-time.Second)
	op.FinishedAt = &finished
	next.ValidatedAt = finished
	if err := store.PromoteSnapshotAndCompleteRefresh(ctx, source, next, op); err != nil {
		t.Fatalf("renewed durable lease rejected stale caller lease metadata: %v", err)
	}
}

func TestMarketplaceRefreshLeaseRecoversExpiredAndDeleteCannotResurrectSource(t *testing.T) {
	ctx := context.Background()
	store, err := newStorageTestSQLiteStore(t, t.TempDir(), "local", true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	source, _ := marketplace.NewSignedCustomSource("leased", "Leased", "https://example.com/plugins.git", "main", "", 0, marketplace.SourceSigner{KeyID: "leased-release", SecretRef: "vault-leased", PublicKey: base64.StdEncoding.EncodeToString(make([]byte, 32))})
	if err := store.SaveMarketplaceSource(ctx, source); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	expired := refreshOperationForSource(source, marketplace.RefreshOperation{ID: "expired", SourceID: source.ID, Status: "running", StartedAt: now.Add(-2 * time.Hour), LeaseToken: "old-lease", LeaseExpiresAt: now.Add(-time.Hour)})
	if err := store.AcquireRefreshLease(ctx, expired); err != nil {
		t.Fatal(err)
	}
	abandonedDigest := pluginTestDigest("f")
	if err := store.db.Create(&PluginPackageStagingRow{SourceID: source.ID, OperationID: expired.ID, Digest: abandonedDigest, UpdatedAt: expired.StartedAt}).Error; err != nil {
		t.Fatal(err)
	}
	fresh := refreshOperationForSource(source, marketplace.RefreshOperation{ID: "fresh", SourceID: source.ID, Commit: strings.Repeat("f", 40), Status: "running", StartedAt: now, LeaseToken: "new-lease", LeaseExpiresAt: now.Add(time.Minute)})
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
	finished := time.Now().UTC()
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

func TestMarketplaceSourceEditInvalidatesActiveRefreshAndCleansOperationSigner(t *testing.T) {
	ctx := context.Background()
	dataRoot := t.TempDir()
	store, err := NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	source := newSignedStorageMarketplaceSource(t, "edit-active-refresh", "Edit Active Refresh", "https://example.com/edit-active.git", "main", "")
	if err := store.SaveMarketplaceSource(ctx, source); err != nil {
		t.Fatal(err)
	}
	oldTrust := marketplaceTrustForTest(t, source)
	started := time.Now().UTC()
	old := refreshOperationForSource(source, marketplace.RefreshOperation{
		ID: "edit-active-old", SourceID: source.ID, Status: "running", StartedAt: started,
		LeaseToken: "edit-active-old-lease", LeaseExpiresAt: started.Add(5 * time.Minute),
	})
	if err := store.AcquireRefreshLease(ctx, old); err != nil {
		t.Fatal(err)
	}
	digest := pluginTestDigest("e")
	if err := store.StagePackageAcquisition(ctx, source.ID, digest, old.ID, oldTrust); err != nil {
		t.Fatal(err)
	}

	rotated, err := marketplace.NewSignedCustomSource(source.ID, source.Name, source.URL, "release", "", 0, marketplace.SourceSigner{
		KeyID: "edit-active-release-v2", SecretRef: "vault-edit-active-v2", PublicKey: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{2}, ed25519.PublicKeySize)),
	})
	if err != nil {
		t.Fatal(err)
	}
	rotated.ConfigRevision = source.ConfigRevision + 1
	updated, err := store.UpdateMarketplaceSource(ctx, rotated, source.ConfigRevision)
	if err != nil {
		t.Fatal(err)
	}
	if updated.RefName != "release" || updated.ConfigRevision != 2 || updated.SignerFingerprint == oldTrust.Fingerprint || updated.LastResult != "refresh_required" || !updated.LeaseExpiresAt.IsZero() {
		t.Fatalf("rotated source = %+v", updated)
	}
	var oldRow MarketplaceRefreshOperationRow
	if err := store.db.Where("id = ?", old.ID).First(&oldRow).Error; err != nil {
		t.Fatal(err)
	}
	if oldRow.Status != "failed" || oldRow.ErrorClass != "source_generation_changed" || oldRow.SignerKeyID != oldTrust.KeyID || oldRow.SignerFingerprint != oldTrust.Fingerprint || oldRow.SignerPublicKey != oldTrust.PublicKey {
		t.Fatalf("old refresh operation was not terminalized with S1 trust: %+v", oldRow)
	}

	renewal := old
	renewal.LeaseExpiresAt = time.Now().UTC().Add(10 * time.Minute)
	if err := store.RenewRefreshLease(ctx, renewal); err == nil {
		t.Fatal("old generation renewed after source edit")
	}
	if err := store.StagePackageAcquisition(ctx, source.ID, pluginTestDigest("f"), old.ID, oldTrust); err == nil {
		t.Fatal("old generation staged a package after source edit")
	}
	finished := time.Now().UTC()
	old.Status, old.ErrorClass, old.Error, old.FinishedAt = "failed", "fetch", "late failure", &finished
	if err := store.SaveRefreshOperation(ctx, old); err == nil {
		t.Fatal("old generation finalized after source edit")
	}
	staleSuccess := old
	staleSuccess.Status, staleSuccess.ErrorClass, staleSuccess.Error = "succeeded", "", ""
	staleSuccess.Commit = strings.Repeat("a", 40)
	staleSnapshot := marketplace.Snapshot{ID: "edit-active-stale", SourceID: source.ID, Commit: staleSuccess.Commit, Path: "edit-active-stale", SourceRevision: source.ConfigRevision, RefKind: source.RefKind, RefName: source.RefName, ValidatedAt: finished}
	if err := store.PromoteSnapshotAndCompleteRefresh(ctx, source, staleSnapshot, staleSuccess); err == nil {
		t.Fatal("old generation promoted after source edit")
	}
	afterStale, ok, err := store.GetMarketplaceSource(ctx, source.ID)
	if err != nil || !ok || afterStale.ConfigRevision != 2 || afterStale.RefName != "release" || afterStale.CurrentSnapshot != "" || afterStale.LastResult != "refresh_required" {
		t.Fatalf("stale worker changed S2 source: %+v ok=%v err=%v", afterStale, ok, err)
	}

	now := time.Now().UTC()
	fresh := refreshOperationForSource(afterStale, marketplace.RefreshOperation{
		ID: "edit-active-fresh", SourceID: source.ID, Status: "running", StartedAt: now,
		LeaseToken: "edit-active-fresh-lease", LeaseExpiresAt: now.Add(5 * time.Minute),
	})
	if err := store.AcquireRefreshLease(ctx, fresh); err != nil {
		t.Fatalf("fresh generation could not immediately acquire: %v", err)
	}
	if err := store.AbandonMarketplaceRefresh(ctx, source.ID, old.ID, old.LeaseToken, "late_cleanup"); err != nil {
		t.Fatal(err)
	}
	var stagedCount int64
	if err := store.db.Model(&PluginPackageStagingRow{}).Where("source_id = ? AND operation_id = ?", source.ID, old.ID).Count(&stagedCount).Error; err != nil || stagedCount != 0 {
		t.Fatalf("old acquisition staging count=%d err=%v", stagedCount, err)
	}
	var intent PluginCacheGCIntentRow
	if err := store.db.Where("source_id = ? AND digest = ? AND signer_fingerprint = ?", source.ID, digest, oldTrust.Fingerprint).First(&intent).Error; err != nil {
		t.Fatal(err)
	}
	if intent.SignerKeyID != oldTrust.KeyID || intent.SignerPublicKey != oldTrust.PublicKey || intent.SignerSourceKind != oldTrust.SourceKind {
		t.Fatalf("cache cleanup mixed S2 signer into S1 intent: %+v", intent)
	}
	var sourceRow MarketplaceSourceRow
	if err := store.db.Where("id = ?", source.ID).First(&sourceRow).Error; err != nil || sourceRow.RefreshLeaseToken != fresh.LeaseToken || sourceRow.LastResult != "running" {
		t.Fatalf("old cleanup changed fresh generation: %+v err=%v", sourceRow, err)
	}

	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatal(err)
	}
	claim, claimed, err := store.ClaimPackageGC(ctx, source.ID, digest, oldTrust.Fingerprint)
	if err != nil || !claimed {
		t.Fatalf("restart did not recover old signer cleanup: claimed=%v err=%v", claimed, err)
	}
	if claim.Trust.KeyID != oldTrust.KeyID || claim.Trust.PublicKey != oldTrust.PublicKey || claim.Trust.Fingerprint != oldTrust.Fingerprint {
		t.Fatalf("restart cleanup trust = %+v, want %+v", claim.Trust, oldTrust)
	}
}

func TestPackagePublicationFencesBootstrapAndSourceEditUntilCacheWriteCompletes(t *testing.T) {
	ctx := context.Background()
	dataRoot := t.TempDir()
	store, err := NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	source := newSignedStorageMarketplaceSource(t, "publish-edit", "Publish Edit", "https://example.com/publish-edit.git", "main", "")
	if err := store.SaveMarketplaceSource(ctx, source); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	operation := refreshOperationForSource(source, marketplace.RefreshOperation{
		ID: "publish-edit-op", SourceID: source.ID, Status: "running", StartedAt: now,
		LeaseToken: "publish-edit-lease", LeaseExpiresAt: now.Add(time.Minute),
	})
	if err := store.AcquireRefreshLease(ctx, operation); err != nil {
		t.Fatal(err)
	}
	digest := pluginTestDigest("a")
	trust := marketplaceTrustForTest(t, source)
	if err := store.StagePackageAcquisition(ctx, source.ID, digest, operation.ID, trust); err != nil {
		t.Fatal(err)
	}
	publishedPath := filepath.Join(dataRoot, "publish-edit-object")
	publishEntered := make(chan struct{})
	releasePublish := make(chan struct{})
	publishDone := make(chan error, 1)
	go func() {
		publishDone <- store.PublishPackageAcquisition(ctx, source.ID, digest, operation.ID, trust, func() error {
			if err := os.WriteFile(publishedPath, []byte("verified"), 0o600); err != nil {
				return err
			}
			close(publishEntered)
			<-releasePublish
			return nil
		})
	}()
	<-publishEntered

	reopenStarted := make(chan struct{})
	reopenDone := make(chan error, 1)
	go func() {
		close(reopenStarted)
		reopened, err := NewSQLiteStore(dataRoot, "local")
		if err == nil {
			err = reopened.Close()
		}
		reopenDone <- err
	}()
	<-reopenStarted
	select {
	case err := <-reopenDone:
		t.Fatalf("bootstrap crossed an active package publication: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(releasePublish)
	if err := <-publishDone; err != nil {
		t.Fatal(err)
	}
	if err := <-reopenDone; err != nil {
		t.Fatal(err)
	}

	rotated := source
	rotated.RefName, rotated.ConfigRevision = "release", source.ConfigRevision+1
	if _, err := store.UpdateMarketplaceSource(ctx, rotated, source.ConfigRevision); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatal(err)
	}
	var staged int64
	if err := store.db.Model(&PluginPackageStagingRow{}).Where("source_id = ? AND operation_id = ?", source.ID, operation.ID).Count(&staged).Error; err != nil || staged != 0 {
		t.Fatalf("restart did not reconcile terminal publication marker count=%d err=%v", staged, err)
	}
	claim, claimed, err := store.ClaimPackageGC(ctx, source.ID, digest, trust.Fingerprint)
	if err != nil || !claimed {
		t.Fatalf("reconciled package was not claimable: claimed=%v err=%v", claimed, err)
	}
	if err := store.WithPackageGCMutation(ctx, claim, func() error { return os.Remove(publishedPath) }); err != nil {
		t.Fatal(err)
	}
	if err := store.CompletePackageGC(ctx, claim, ""); err != nil {
		t.Fatal(err)
	}
}

func TestRepositoryCatalogMissingProvenanceFailsClosed(t *testing.T) {
	ctx := context.Background()
	store, err := newStorageTestSQLiteStore(t, t.TempDir(), "local", true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	source, _ := marketplace.NewSignedCustomSource("legacy-provenance", "Legacy Provenance", "https://example.com/plugins.git", "main", "", 0, marketplace.SourceSigner{KeyID: "legacy-release", SecretRef: "vault-legacy", PublicKey: base64.StdEncoding.EncodeToString(make([]byte, 32))})
	trust, _ := source.SignatureTrust()
	digest := pluginTestDigest("c")
	if err := store.PromoteSnapshot(ctx, source, marketplace.Snapshot{ID: "legacy-current", SourceID: source.ID, Commit: strings.Repeat("3", 40), Path: "legacy-current", ValidatedAt: time.Now().UTC(), Entries: []plugins.MarketEntry{{ID: "legacy.plugin", Version: "1.0.0", PackageSHA256: digest, SignatureKeyID: trust.KeyID}}}); err != nil {
		t.Fatal(err)
	}
	if err := store.db.Model(&MarketplaceSourceRow{}).Where("id = ?", source.ID).Update("current_resolved_oid", "").Error; err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.CurrentSnapshot(ctx, source.ID); !errors.Is(err, ErrMarketplaceCatalogStale) {
		t.Fatalf("legacy snapshot without provenance error = %v", err)
	}
	if _, _, err := store.CurrentPackageAcquisition(ctx, source.ID, digest); !errors.Is(err, ErrMarketplaceCatalogStale) {
		t.Fatalf("legacy acquisition without provenance error = %v", err)
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

func TestPluginUninstallQuotaRecomputeRollsBackWithLaterFailure(t *testing.T) {
	ctx := context.Background()
	store, err := newStorageTestSQLiteStore(t, t.TempDir(), "local", true)
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
	store, err := newStorageTestSQLiteStore(t, t.TempDir(), "local", true)
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

func newSignedStorageMarketplaceSource(t *testing.T, id, name, remoteURL, branch, credentialRef string) marketplace.Source {
	t.Helper()
	source, err := marketplace.NewSignedCustomSource(id, name, remoteURL, branch, credentialRef, 0, marketplace.SourceSigner{KeyID: id + "-release", SecretRef: "vault-" + id, PublicKey: base64.StdEncoding.EncodeToString(make([]byte, 32))})
	if err != nil {
		t.Fatal(err)
	}
	return source
}

func refreshOperationForSource(source marketplace.Source, operation marketplace.RefreshOperation) marketplace.RefreshOperation {
	operation.SourceRevision = source.ConfigRevision
	operation.RefKind = source.RefKind
	operation.RefName = source.RefName
	trust, err := source.SignatureTrust()
	if err != nil {
		panic(err)
	}
	operation.SignerSourceKind = trust.SourceKind
	operation.SignerKeyID = trust.KeyID
	operation.SignerPublicKey = trust.PublicKey
	operation.SignerFingerprint = trust.Fingerprint
	if operation.Actor.ActorID == "" {
		operation.Actor.ActorID = "test.marketplace"
	}
	if operation.Actor.CorrelationID == "" {
		operation.Actor.CorrelationID = operation.ID
	}
	return operation
}

func marketplaceTrustForTest(t *testing.T, source marketplace.Source) marketplace.SignatureTrust {
	t.Helper()
	trust, err := source.SignatureTrust()
	if err != nil {
		t.Fatal(err)
	}
	return trust
}

func pluginTestDigest(value string) string { return strings.Repeat(value, 64) }
