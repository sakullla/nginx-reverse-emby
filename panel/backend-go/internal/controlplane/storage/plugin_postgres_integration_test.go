//go:build integration

package storage

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/marketplace"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestPostgresPluginVariantMigrationFromDigestIdentity(t *testing.T) {
	dsn := postgresIntegrationSchemaDSN(t)
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	legacyDDL := []string{
		`CREATE TABLE plugin_packages (digest varchar(64) PRIMARY KEY, plugin_id text NOT NULL, version text NOT NULL, cache_path text NOT NULL, manifest_json text NOT NULL, config_schema_json text NOT NULL, verified_at timestamp NOT NULL)`,
		`CREATE TABLE plugin_package_acquisitions (source_id varchar(64) NOT NULL, digest varchar(64) NOT NULL, snapshot_id varchar(64) NOT NULL DEFAULT '', status varchar(32) NOT NULL, updated_at timestamp NOT NULL, PRIMARY KEY (source_id,digest))`,
		`CREATE TABLE plugin_package_staging (source_id varchar(64) NOT NULL, operation_id varchar(64) NOT NULL, digest varchar(64) NOT NULL, updated_at timestamp NOT NULL, PRIMARY KEY (source_id,operation_id,digest))`,
		`CREATE TABLE marketplace_sources (id varchar(64) PRIMARY KEY)`,
		`CREATE TABLE plugin_cache_gc_intents (source_id varchar(64) NOT NULL, digest varchar(64) NOT NULL, status varchar(32) NOT NULL, deferred boolean NOT NULL DEFAULT false, claim_token varchar(64) NOT NULL DEFAULT '', claim_expires_at timestamp, quarantine_path text NOT NULL DEFAULT '', last_error text NOT NULL, updated_at timestamp NOT NULL, PRIMARY KEY (source_id,digest))`,
	}
	for _, statement := range legacyDDL {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("legacy schema setup failed: %v", err)
		}
	}
	digest := strings.Repeat("a", 64)
	now := time.Now().UTC()
	if err := db.Exec(`INSERT INTO plugin_packages (digest,plugin_id,version,cache_path,manifest_json,config_schema_json,verified_at) VALUES (?,?,?,?,?,?,?)`, digest, "legacy.plugin", "1.0.0", "legacy/package", `{}`, `{}`, now).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO plugin_cache_gc_intents (source_id,digest,status,last_error,updated_at) VALUES (?,?,?,?,?)`, "legacy-source", digest, "pending", "", now).Error; err != nil {
		t.Fatal(err)
	}

	if err := migratePostgresPluginVariantIdentity(t.Context(), db); err != nil {
		t.Fatalf("migratePostgresPluginVariantIdentity() error = %v", err)
	}

	secondIdentity := strings.Repeat("b", 64)
	if err := db.Exec(`INSERT INTO plugin_packages (identity,digest,plugin_id,version,cache_path,manifest_json,config_schema_json,verified_at) VALUES (?,?,?,?,?,?,?,?)`, secondIdentity, digest, "legacy.plugin", "1.0.0", "legacy/package-2", `{}`, `{}`, now).Error; err != nil {
		t.Fatalf("same digest package variant insert failed after migration: %v", err)
	}
	for _, fingerprint := range []string{strings.Repeat("c", 64), strings.Repeat("d", 64)} {
		if err := db.Exec(`INSERT INTO plugin_cache_gc_intents (source_id,digest,signer_fingerprint,status,last_error,updated_at) VALUES (?,?,?,?,?,?)`, "legacy-source", digest, fingerprint, "pending", "", now).Error; err != nil {
			t.Fatalf("same digest signer variant insert failed after migration: %v", err)
		}
	}
}

func TestPostgresPluginVariantReferenceTransactions(t *testing.T) {
	dsn := postgresIntegrationSchemaDSN(t)
	store, err := NewStore(StoreConfig{Driver: "postgres", DSN: dsn, DataRoot: t.TempDir(), LocalAgentID: "local"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	tests := []struct {
		name        string
		add         func(*testing.T, PluginPackageRow)
		wantClaimed bool
	}{
		{
			name: "pending target",
			add: func(t *testing.T, row PluginPackageRow) {
				now := time.Now().UTC()
				installed := InstalledPluginRow{PluginID: row.PluginID, ActiveSourceID: row.SourceID, PendingOperationID: "pending", PendingKind: "upgrade", PendingTargetDigest: row.Digest, PendingTargetIdentity: row.Identity, DesiredLifecycle: "disabled", CurrentLifecycle: "upgrading", CleanupPolicyJSON: `{}`, LastOperationID: "pending", StateVersion: 1, InstalledAt: now, UpdatedAt: now}
				if err := store.db.Create(&installed).Error; err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "retained grant",
			add: func(t *testing.T, row PluginPackageRow) {
				grant := PluginGrantRow{ID: "postgres-grant", GrantKey: "postgres-grant-key", PluginID: row.PluginID, PackageDigest: row.Digest, PackageIdentity: row.Identity, Permission: "http.inspect", GrantedBy: "admin", GrantedAt: time.Now().UTC()}
				if err := store.db.Create(&grant).Error; err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:        "completed operation",
			wantClaimed: true,
			add: func(t *testing.T, row PluginPackageRow) {
				now := time.Now().UTC()
				operation := PluginOperationRow{ID: "postgres-completed", PluginID: row.PluginID, Kind: "install", Status: "succeeded", AgentResultsJSON: `{}`, ActorID: "admin", CreatedAt: now, CompletedAt: &now}
				if err := BindPluginOperationPackage(&operation, row); err != nil {
					t.Fatal(err)
				}
				if err := store.db.Create(&operation).Error; err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			digest := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("postgres-reference-%d", index))))
			seed := sha256.Sum256([]byte(fmt.Sprintf("postgres-signer-%d", index)))
			publicKey := base64.StdEncoding.EncodeToString(ed25519.NewKeyFromSeed(seed[:]).Public().(ed25519.PublicKey))
			fingerprint, err := marketplace.SourceSignerFingerprint(publicKey)
			if err != nil {
				t.Fatal(err)
			}
			sourceID := fmt.Sprintf("postgres-source-%d", index)
			identity := PluginPackageIdentity(digest, sourceID, fingerprint)
			now := time.Now().UTC()
			row := PluginPackageRow{Identity: identity, Digest: digest, PluginID: fmt.Sprintf("postgres.reference.%d", index), Version: "1.0.0", SourceID: sourceID, SourceKind: marketplace.SourceKindCustom, SourceRiskLabel: marketplace.UntrustedRiskLabel, SignatureKeyID: "community-release", SignaturePublicKey: publicKey, SignatureFingerprint: fingerprint, CachePath: fmt.Sprintf("packages/%d", index), ManifestJSON: `{}`, ConfigSchemaJSON: `{}`, VerifiedAt: now}
			if err := store.db.Create(&row).Error; err != nil {
				t.Fatal(err)
			}
			test.add(t, row)
			intent := PluginCacheGCIntentRow{SourceID: sourceID, Digest: digest, SignerFingerprint: fingerprint, Status: "pending", CacheObjectsJSON: `[]`, UpdatedAt: now}
			if err := store.db.Create(&intent).Error; err != nil {
				t.Fatal(err)
			}
			claim, claimed, err := store.ClaimPackageGC(t.Context(), sourceID, digest, fingerprint)
			if err != nil || claimed != test.wantClaimed {
				t.Fatalf("PostgreSQL GC claim = %+v, %v, %v; want %v", claim, claimed, err, test.wantClaimed)
			}
			if claimed {
				if err := store.CompletePackageGC(t.Context(), claim, ""); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestPostgresPluginAcquisitionRebuildPreservesTrust(t *testing.T) {
	dsn := postgresIntegrationSchemaDSN(t)
	store, err := NewStore(StoreConfig{Driver: "postgres", DSN: dsn, DataRoot: t.TempDir(), LocalAgentID: "local"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	seed := sha256.Sum256([]byte("postgres-acquisition-rebuild-signer"))
	key := ed25519.NewKeyFromSeed(seed[:])
	source, err := marketplace.NewSignedCustomSource("postgres-acquisition", "Postgres Acquisition", "https://example.com/postgres-acquisition.git", "main", "", 0, marketplace.SourceSigner{KeyID: "community-release", SecretRef: "vault-postgres-acquisition", PublicKey: base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey))})
	if err != nil {
		t.Fatal(err)
	}
	trust, err := source.SignatureTrust()
	if err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte("postgres-acquisition-package")))
	snapshot := marketplace.Snapshot{ID: "postgres-acquisition-snapshot", SourceID: source.ID, Commit: "postgres-acquisition-commit", Path: "snapshot", ValidatedAt: time.Now().UTC(), Entries: []plugins.MarketEntry{{ID: "postgres.acquisition", Version: "1.0.0", PackageSHA256: digest, SignatureKeyID: trust.KeyID}}}
	if err := store.PromoteSnapshot(t.Context(), source, snapshot); err != nil {
		t.Fatal(err)
	}
	if err := backfillPluginOwnershipAndAcquisitions(t.Context(), store.db, store.LocalAgentID()); err != nil {
		t.Fatal(err)
	}
	acquisition, ok, err := store.CurrentPackageAcquisition(t.Context(), source.ID, digest)
	if err != nil || !ok || acquisition.SnapshotID != snapshot.ID || acquisition.Trust != trust {
		t.Fatalf("PostgreSQL rebuilt acquisition = %+v, %v, %v", acquisition, ok, err)
	}
	corruptFingerprint := fmt.Sprintf("%x", sha256.Sum256([]byte("postgres-corrupt-acquisition")))
	if err := store.db.Model(&PluginPackageAcquisitionRow{}).Where("source_id = ? AND digest = ?", source.ID, digest).Update("signature_fingerprint", corruptFingerprint).Error; err != nil {
		t.Fatal(err)
	}
	if err := backfillPluginOwnershipAndAcquisitions(t.Context(), store.db, store.LocalAgentID()); err == nil {
		t.Fatal("PostgreSQL acquisition rebuild accepted a complete mismatched signer tuple")
	}
	var retained PluginPackageAcquisitionRow
	if err := store.db.Where("source_id = ? AND digest = ?", source.ID, digest).First(&retained).Error; err != nil || retained.SignatureFingerprint != corruptFingerprint {
		t.Fatalf("PostgreSQL failed rebuild changed mismatched acquisition = %+v, %v", retained, err)
	}
}

func TestPostgresMarketplacePromotionRejectsActorSubstitution(t *testing.T) {
	dsn := postgresIntegrationSchemaDSN(t)
	store, err := NewStore(StoreConfig{Driver: "postgres", DSN: dsn, DataRoot: t.TempDir(), LocalAgentID: "local"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	seed := sha256.Sum256([]byte("postgres-promotion-lock-signer"))
	key := ed25519.NewKeyFromSeed(seed[:])
	source, err := marketplace.NewSignedCustomSource("postgres-promotion", "Postgres Promotion", "https://example.com/postgres-promotion.git", "main", "", 0, marketplace.SourceSigner{KeyID: "community-release", SecretRef: "vault-postgres-promotion", PublicKey: base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey))})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveMarketplaceSource(t.Context(), source); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	operation := marketplace.RefreshOperation{ID: "postgres-promotion-refresh", SourceID: source.ID, Commit: "postgres-promotion-commit", Status: "running", StartedAt: now, Actor: marketplace.OperationActor{ActorID: "trusted-actor", SessionID: "trusted-session", CorrelationID: "trusted-correlation"}, LeaseToken: "postgres-promotion-lease", LeaseExpiresAt: now.Add(time.Minute)}
	if err := store.AcquireRefreshLease(t.Context(), operation); err != nil {
		t.Fatal(err)
	}
	snapshot := marketplace.Snapshot{ID: "postgres-promotion-snapshot", SourceID: source.ID, Commit: operation.Commit, Path: "postgres-promotion-snapshot", ValidatedAt: now.Add(time.Second)}
	reservationPath := filepath.Join(store.dataRoot, "marketplace", "snapshots", snapshot.Path)
	if err := store.RegisterMarketplaceDirectoryCleanup(t.Context(), source.ID, operation.ID, []string{reservationPath}); err != nil {
		t.Fatal(err)
	}
	finished := now.Add(2 * time.Second)
	operation.Status, operation.FinishedAt = "succeeded", &finished
	operation.Actor = marketplace.OperationActor{ActorID: "attacker", SessionID: "attacker-session", CorrelationID: "attacker-correlation"}
	if err := store.PromoteSnapshotAndCompleteRefresh(t.Context(), source, snapshot, operation); err == nil {
		t.Fatal("PostgreSQL promotion accepted actor substitution")
	}
	var persisted MarketplaceRefreshOperationRow
	if err := store.db.Where("id = ?", operation.ID).First(&persisted).Error; err != nil || persisted.Status != "running" || persisted.ActorID != "trusted-actor" {
		t.Fatalf("PostgreSQL actor substitution changed operation = %+v, %v", persisted, err)
	}
	var snapshotCount, auditCount int64
	_ = store.db.Model(&MarketSnapshotRow{}).Where("id = ?", snapshot.ID).Count(&snapshotCount).Error
	_ = store.db.Model(&AuditEventRow{}).Where("action = ? AND target_id = ?", "marketplace.source.refresh", source.ID).Count(&auditCount).Error
	if snapshotCount != 0 || auditCount != 0 {
		t.Fatalf("PostgreSQL actor substitution left snapshot/audit state: %d/%d", snapshotCount, auditCount)
	}
}

func postgresIntegrationSchemaDSN(t *testing.T) string {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("NRE_TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("NRE_TEST_POSTGRES_DSN is not configured")
	}
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := admin.DB()
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("nre_storage_%d", time.Now().UnixNano())
	if err := admin.Exec(`CREATE SCHEMA "` + schema + `"`).Error; err != nil {
		_ = sqlDB.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = admin.Exec(`DROP SCHEMA IF EXISTS "` + schema + `" CASCADE`).Error
		_ = sqlDB.Close()
	})
	parsed, parseErr := url.Parse(dsn)
	if parseErr == nil && (parsed.Scheme == "postgres" || parsed.Scheme == "postgresql") {
		query := parsed.Query()
		query.Set("search_path", schema)
		parsed.RawQuery = query.Encode()
		return parsed.String()
	}
	return dsn + " search_path=" + schema
}
