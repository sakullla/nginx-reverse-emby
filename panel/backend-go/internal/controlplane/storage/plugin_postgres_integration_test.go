//go:build integration

package storage

import (
	"context"
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

func TestPostgresConcurrentPluginMutationsShareAgentCatalogFence(t *testing.T) {
	dsn := postgresIntegrationSchemaDSN(t)
	dataRoot := t.TempDir()
	store, err := NewStore(StoreConfig{Driver: "postgres", DSN: dsn, DataRoot: dataRoot, LocalAgentID: "local"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close()
		_ = marketplace.DiscardVerifiedCacheRoot(filepath.Join(dataRoot, "plugins", "packages"))
	})
	if err := store.SaveAgent(t.Context(), AgentRow{ID: "edge-a", AgentToken: "edge-a-token", CapabilitiesJSON: `[]`}); err != nil {
		t.Fatal(err)
	}
	signingKey := ed25519.NewKeyFromSeed([]byte("storage-migration-signing-seed!!"))
	first := installActivePolicyFixture(t, store, signingKey, "policy.concurrent.first", "ip", "concurrent-ip", `["edge-a"]`, `["concurrent-first"]`)
	second := installActivePolicyFixture(t, store, signingKey, "policy.concurrent.second", "rate", "concurrent-rate", `["edge-a"]`, `["concurrent-second"]`)

	mutations := make([]PluginMutation, 0, 2)
	upgradeDigest := ""
	for index, instance := range []PluginInstanceRow{first, second} {
		installed, found, err := store.GetInstalledPlugin(t.Context(), instance.PluginID)
		if err != nil || !found {
			t.Fatalf("GetInstalledPlugin(%s) = %v, %v", instance.PluginID, found, err)
		}
		instance.ConfigJSON = fmt.Sprintf(`{"generation":%d}`, index+2)
		instance.ConfigVersion++
		now := time.Now().UTC()
		operationKind := "configure"
		operationID := fmt.Sprintf("postgres-concurrent-%s-%d", operationKind, index)
		mutation := PluginMutation{
			PluginID: instance.PluginID, ExpectedActive: installed.ActivePackageDigest, ExpectedStateVersion: installed.StateVersion,
			Installed: &installed, ReplaceInstance: &instance,
			Operation: PluginOperationRow{ID: operationID, PluginID: instance.PluginID, Kind: operationKind, Status: "succeeded", AgentResultsJSON: `{}`, CreatedAt: now},
			Audit:     AuditEventRow{ID: operationID + "-audit", Action: "plugin." + operationKind, TargetKind: "plugin", TargetID: instance.PluginID, Result: "success", MetadataJSON: `{}`, CreatedAt: now},
		}
		if index == 1 {
			candidate, artifacts := preparePolicyPackageFixture(t, store, signingKey, instance.PluginID, "rate", 32768)
			upgradeDigest = candidate.Digest
			mutation.Operation.Kind = "upgrade"
			mutation.Audit.Action = "plugin.upgrade"
			mutation.Package, mutation.Artifacts = &candidate, artifacts
			installed.ActivePackageDigest, installed.ActivePackageIdentity = candidate.Digest, candidate.Identity
			installed.RuntimeKind, installed.RuntimeABI, installed.HostScope = candidate.RuntimeKind, candidate.RuntimeABI, candidate.HostScope
			installed.ActiveSourceID, installed.ActiveSourceKind, installed.ActiveSourceRiskLabel = candidate.SourceID, candidate.SourceKind, candidate.SourceRiskLabel
			installed.ActiveSignatureKeyID, installed.ActiveSignaturePublicKey, installed.ActiveSignatureFingerprint = candidate.SignatureKeyID, candidate.SignaturePublicKey, candidate.SignatureFingerprint
		}
		mutations = append(mutations, mutation)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	start := make(chan struct{})
	errorsByMutation := make(chan error, len(mutations))
	for index := range mutations {
		mutation := mutations[index]
		go func() {
			<-start
			errorsByMutation <- store.ApplyPluginMutation(ctx, mutation)
		}()
	}
	close(start)
	for range mutations {
		select {
		case err := <-errorsByMutation:
			if err != nil {
				t.Fatalf("concurrent plugin mutation failed: %v", err)
			}
		case <-ctx.Done():
			t.Fatalf("concurrent plugin mutations did not complete: %v", ctx.Err())
		}
	}
	for index, expected := range []PluginInstanceRow{first, second} {
		persisted, found, err := store.GetPluginInstance(t.Context(), expected.ID)
		wantConfig := fmt.Sprintf(`{"generation":%d}`, index+2)
		if err != nil || !found || persisted.ConfigJSON != wantConfig {
			t.Fatalf("persisted plugin instance %s = %+v, %v, %v", expected.ID, persisted, found, err)
		}
	}
	upgraded, found, err := store.GetInstalledPlugin(t.Context(), second.PluginID)
	if err != nil || !found || upgraded.ActivePackageDigest != upgradeDigest {
		t.Fatalf("upgraded plugin = %+v, %v, %v", upgraded, found, err)
	}
}

func TestPostgresStandalonePluginPolicyCatalogReadUsesOneSnapshot(t *testing.T) {
	dsn := postgresIntegrationSchemaDSN(t)
	dataRoot := t.TempDir()
	reader, err := NewStore(StoreConfig{Driver: "postgres", DSN: dsn, DataRoot: dataRoot, LocalAgentID: "local"})
	if err != nil {
		t.Fatal(err)
	}
	writer, err := NewStore(StoreConfig{Driver: "postgres", DSN: dsn, DataRoot: dataRoot, LocalAgentID: "local", SkipBootstrapSchema: true})
	if err != nil {
		_ = reader.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = writer.Close()
		_ = reader.Close()
		_ = marketplace.DiscardVerifiedCacheRoot(filepath.Join(dataRoot, "plugins", "packages"))
	})
	testStandalonePluginPolicyCatalogReadUsesOneSnapshot(t, reader, writer, "postgres", func(ctx context.Context, store *GormStore, agentID string) ([]PluginPolicy, error) {
		return store.LoadAgentPluginPolicies(ctx, agentID)
	})
}

func TestPostgresCompleteAgentSnapshotUsesOneSnapshot(t *testing.T) {
	for _, test := range []struct {
		name              string
		transactionScoped bool
	}{
		{name: "standalone", transactionScoped: false},
		{name: "revision mutation", transactionScoped: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			dsn := postgresIntegrationSchemaDSN(t)
			dataRoot := t.TempDir()
			reader, err := NewStore(StoreConfig{Driver: "postgres", DSN: dsn, DataRoot: dataRoot, LocalAgentID: "local"})
			if err != nil {
				t.Fatal(err)
			}
			writer, err := NewStore(StoreConfig{Driver: "postgres", DSN: dsn, DataRoot: dataRoot, LocalAgentID: "local", SkipBootstrapSchema: true})
			if err != nil {
				_ = reader.Close()
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_ = writer.Close()
				_ = reader.Close()
				_ = marketplace.DiscardVerifiedCacheRoot(filepath.Join(dataRoot, "plugins", "packages"))
			})
			testCompleteAgentSnapshotUsesOneSnapshot(t, reader, writer, "postgres-"+strings.ReplaceAll(test.name, " ", "-"), test.transactionScoped)
		})
	}
}

func TestPostgresAgentHeartbeatSnapshotUsesOneSnapshot(t *testing.T) {
	dsn := postgresIntegrationSchemaDSN(t)
	dataRoot := t.TempDir()
	reader, err := NewStore(StoreConfig{Driver: "postgres", DSN: dsn, DataRoot: dataRoot, LocalAgentID: "local"})
	if err != nil {
		t.Fatal(err)
	}
	writer, err := NewStore(StoreConfig{Driver: "postgres", DSN: dsn, DataRoot: dataRoot, LocalAgentID: "local", SkipBootstrapSchema: true})
	if err != nil {
		_ = reader.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = writer.Close()
		_ = reader.Close()
		_ = marketplace.DiscardVerifiedCacheRoot(filepath.Join(dataRoot, "plugins", "packages"))
	})
	testAgentHeartbeatSnapshotUsesOneSnapshot(t, reader, writer, "postgres")
}

func TestPostgresAgentHeartbeatPendingCertificateOverlayUsesOneSnapshot(t *testing.T) {
	dsn := postgresIntegrationSchemaDSN(t)
	dataRoot := t.TempDir()
	reader, err := NewStore(StoreConfig{Driver: "postgres", DSN: dsn, DataRoot: dataRoot, LocalAgentID: "local"})
	if err != nil {
		t.Fatal(err)
	}
	writer, err := NewStore(StoreConfig{Driver: "postgres", DSN: dsn, DataRoot: dataRoot, LocalAgentID: "local", SkipBootstrapSchema: true})
	if err != nil {
		_ = reader.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = writer.Close()
		_ = reader.Close()
		_ = marketplace.DiscardVerifiedCacheRoot(filepath.Join(dataRoot, "plugins", "packages"))
	})
	testAgentHeartbeatPendingCertificateOverlayUsesOneSnapshot(t, reader, writer, "postgres")
}

func TestPostgresRevisionMutationUsesRepeatableRead(t *testing.T) {
	dsn := postgresIntegrationSchemaDSN(t)
	dataRoot := t.TempDir()
	store, err := NewStore(StoreConfig{Driver: "postgres", DSN: dsn, DataRoot: dataRoot, LocalAgentID: "local"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close()
		_ = marketplace.DiscardVerifiedCacheRoot(filepath.Join(dataRoot, "plugins", "packages"))
	})

	var isolation string
	err = store.WithRevisionMutation(t.Context(), func(tx *GormStore) (RevisionMutationDecision, error) {
		if err := tx.db.Raw("SHOW transaction_isolation").Scan(&isolation).Error; err != nil {
			return RevisionMutationDecision{}, err
		}
		return RevisionMutationDecision{}, nil
	})
	if err != nil {
		t.Fatalf("WithRevisionMutation() error = %v", err)
	}
	if isolation != "repeatable read" {
		t.Fatalf("revision mutation isolation = %q, want repeatable read", isolation)
	}
}

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
	operation := refreshOperationForSource(source, marketplace.RefreshOperation{ID: "postgres-promotion-refresh", SourceID: source.ID, Commit: "postgres-promotion-commit", Status: "running", StartedAt: now, Actor: marketplace.OperationActor{ActorID: "trusted-actor", SessionID: "trusted-session", CorrelationID: "trusted-correlation"}, LeaseToken: "postgres-promotion-lease", LeaseExpiresAt: now.Add(time.Minute)})
	if err := store.AcquireRefreshLease(t.Context(), operation); err != nil {
		t.Fatal(err)
	}
	snapshot := marketplace.Snapshot{ID: "postgres-promotion-snapshot", SourceID: source.ID, Commit: operation.Commit, Path: "postgres-promotion-snapshot", ValidatedAt: now.Add(time.Second)}
	reservationPath := filepath.Join(store.dataRoot, "marketplace", "snapshots", snapshot.Path)
	if err := store.RegisterMarketplaceDirectoryCleanup(t.Context(), source.ID, operation.ID, []string{reservationPath}); err != nil {
		t.Fatal(err)
	}
	finished := time.Now().UTC()
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

func TestPostgresRefreshFinalizationRequiresDurableLease(t *testing.T) {
	dsn := postgresIntegrationSchemaDSN(t)
	store, err := NewStore(StoreConfig{Driver: "postgres", DSN: dsn, DataRoot: t.TempDir(), LocalAgentID: "local"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	source, _ := marketplace.NewCustomSource("postgres-lease-required", "Postgres Lease Required", "https://example.com/postgres-lease-required.git", "main", "", 0)
	other, _ := marketplace.NewCustomSource("postgres-lease-other", "Postgres Lease Other", "https://example.com/postgres-lease-other.git", "main", "", 0)
	for _, candidate := range []marketplace.Source{source, other} {
		if err := store.SaveMarketplaceSource(t.Context(), candidate); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	operation := refreshOperationForSource(source, marketplace.RefreshOperation{ID: "postgres-lease-required-refresh", SourceID: source.ID, Commit: "durable-commit", Status: "running", StartedAt: now, Actor: marketplace.OperationActor{ActorID: "trusted-actor", SessionID: "trusted-session", CorrelationID: "trusted-correlation"}, LeaseToken: "durable-lease", LeaseExpiresAt: now.Add(time.Minute)})
	if err := store.AcquireRefreshLease(t.Context(), operation); err != nil {
		t.Fatal(err)
	}
	finished := time.Now().UTC()
	failure := operation
	failure.Status, failure.ErrorClass, failure.Error, failure.FinishedAt = "failed", "fetch", "offline", &finished

	missingToken := failure
	missingToken.LeaseToken = ""
	missingToken.SourceID = other.ID
	missingToken.Actor = marketplace.OperationActor{ActorID: "attacker", SessionID: "attacker-session", CorrelationID: "attacker-correlation"}
	if err := store.SaveRefreshOperation(t.Context(), missingToken); err == nil {
		t.Fatal("PostgreSQL missing-token actor/source substitution was accepted")
	}
	directSuccess := operation
	directSuccess.Status, directSuccess.FinishedAt = "succeeded", &finished
	if err := store.SaveRefreshOperation(t.Context(), directSuccess); err == nil {
		t.Fatal("PostgreSQL direct refresh success was accepted")
	}
	missingHistory := failure
	missingHistory.ID, missingHistory.LeaseToken = "postgres-missing-refresh", ""
	if err := store.SaveRefreshOperation(t.Context(), missingHistory); err == nil {
		t.Fatal("PostgreSQL missing-token caller created refresh history")
	}
	var persisted MarketplaceRefreshOperationRow
	if err := store.db.Where("id = ?", operation.ID).First(&persisted).Error; err != nil || persisted.Status != "running" || persisted.SourceID != source.ID || persisted.ActorID != operation.Actor.ActorID || persisted.LeaseToken != operation.LeaseToken {
		t.Fatalf("PostgreSQL rejected finalization changed durable operation: %+v, %v", persisted, err)
	}
	var durableSource, otherSource MarketplaceSourceRow
	if err := store.db.Where("id = ?", source.ID).First(&durableSource).Error; err != nil || durableSource.RefreshLeaseToken != operation.LeaseToken || durableSource.LastResult != "running" {
		t.Fatalf("PostgreSQL rejected finalization changed durable source: %+v, %v", durableSource, err)
	}
	if err := store.db.Where("id = ?", other.ID).First(&otherSource).Error; err != nil || otherSource.RefreshLeaseToken != "" || otherSource.LastResult != "" {
		t.Fatalf("PostgreSQL rejected finalization changed substituted source: %+v, %v", otherSource, err)
	}
	var auditCount, missingCount int64
	_ = store.db.Model(&AuditEventRow{}).Where("action = ?", "marketplace.source.refresh").Count(&auditCount).Error
	_ = store.db.Model(&MarketplaceRefreshOperationRow{}).Where("id = ?", missingHistory.ID).Count(&missingCount).Error
	if auditCount != 0 || missingCount != 0 {
		t.Fatalf("PostgreSQL rejected finalization left audit/history: %d/%d", auditCount, missingCount)
	}
	expiredAt := now.Add(-time.Minute)
	if err := store.db.Model(&MarketplaceRefreshOperationRow{}).Where("id = ?", operation.ID).Update("lease_expires_at", expiredAt).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRefreshOperation(t.Context(), failure); err == nil {
		t.Fatal("PostgreSQL expired durable lease finalized refresh failure")
	}
	if err := store.db.Model(&MarketplaceRefreshOperationRow{}).Where("id = ?", operation.ID).Update("lease_expires_at", operation.LeaseExpiresAt).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Where("id = ?", operation.ID).First(&persisted).Error; err != nil || persisted.Status != "running" {
		t.Fatalf("PostgreSQL expired lease finalization changed operation: %+v, %v", persisted, err)
	}

	if err := store.SaveRefreshOperation(t.Context(), failure); err != nil {
		t.Fatal(err)
	}
	completedAttack := failure
	completedAttack.LeaseToken = ""
	completedAttack.SourceID = other.ID
	completedAttack.Actor.ActorID = "attacker"
	completedAttack.Error = "rewritten"
	if err := store.SaveRefreshOperation(t.Context(), completedAttack); err == nil {
		t.Fatal("PostgreSQL missing-token caller rewrote completed refresh history")
	}
	if err := store.db.Where("id = ?", operation.ID).First(&persisted).Error; err != nil || persisted.Status != "failed" || persisted.SourceID != source.ID || persisted.ActorID != operation.Actor.ActorID || persisted.Error != failure.Error {
		t.Fatalf("PostgreSQL completed refresh changed after rejected rewrite: %+v, %v", persisted, err)
	}
	if err := store.db.Model(&AuditEventRow{}).Where("action = ?", "marketplace.source.refresh").Count(&auditCount).Error; err != nil || auditCount != 1 {
		t.Fatalf("PostgreSQL completed-row attack changed audit count: %d, %v", auditCount, err)
	}
}

func TestPostgresRefreshFinalizationRejectsInvalidTimestamps(t *testing.T) {
	dsn := postgresIntegrationSchemaDSN(t)
	store, err := NewStore(StoreConfig{Driver: "postgres", DSN: dsn, DataRoot: t.TempDir(), LocalAgentID: "local"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	tests := []struct {
		name      string
		promotion bool
		timing    string
	}{
		{name: "failure nil", timing: "nil"},
		{name: "failure backdated", timing: "backdated"},
		{name: "failure future", timing: "future"},
		{name: "failure after lease", timing: "after_lease"},
		{name: "promotion nil", promotion: true, timing: "nil"},
		{name: "promotion backdated", promotion: true, timing: "backdated"},
		{name: "promotion future", promotion: true, timing: "future"},
		{name: "promotion after lease", promotion: true, timing: "after_lease"},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			slug := fmt.Sprintf("postgres-timestamp-%d", index)
			source, err := marketplace.NewCustomSource(slug, slug, "https://example.com/"+slug+".git", "main", "", 0)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.SaveMarketplaceSource(t.Context(), source); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC().Truncate(time.Millisecond)
			operation := refreshOperationForSource(source, marketplace.RefreshOperation{ID: slug + "-refresh", SourceID: source.ID, Commit: slug + "-commit", Status: "running", StartedAt: now, Actor: marketplace.OperationActor{ActorID: "trusted-actor", SessionID: "trusted-session", CorrelationID: "trusted-correlation"}, LeaseToken: slug + "-lease", LeaseExpiresAt: now.Add(time.Minute)})
			if err := store.AcquireRefreshLease(t.Context(), operation); err != nil {
				t.Fatal(err)
			}
			switch test.timing {
			case "nil":
				operation.FinishedAt = nil
			case "backdated":
				backdated := now.Add(-time.Nanosecond)
				operation.FinishedAt = &backdated
			case "future":
				future := time.Now().UTC().Add(time.Hour)
				operation.FinishedAt = &future
			case "after_lease":
				afterLease := operation.LeaseExpiresAt.Add(time.Second)
				operation.FinishedAt = &afterLease
			}
			if test.promotion {
				operation.Status = "succeeded"
				snapshot := marketplace.Snapshot{ID: slug + "-snapshot", SourceID: source.ID, Commit: operation.Commit, Path: slug + "-snapshot", ValidatedAt: now.Add(time.Second)}
				reservationPath := filepath.Join(store.dataRoot, "marketplace", "snapshots", snapshot.Path)
				if err := store.RegisterMarketplaceDirectoryCleanup(t.Context(), source.ID, operation.ID, []string{reservationPath}); err != nil {
					t.Fatal(err)
				}
				if err := store.PromoteSnapshotAndCompleteRefresh(t.Context(), source, snapshot, operation); err == nil {
					t.Fatal("PostgreSQL promotion accepted invalid completion time")
				}
			} else {
				operation.Status, operation.ErrorClass, operation.Error = "failed", "fetch", "offline"
				if err := store.SaveRefreshOperation(t.Context(), operation); err == nil {
					t.Fatal("PostgreSQL failure accepted invalid completion time")
				}
			}
			var persisted MarketplaceRefreshOperationRow
			if err := store.db.Where("id = ?", operation.ID).First(&persisted).Error; err != nil || persisted.Status != "running" || persisted.FinishedAt != nil {
				t.Fatalf("PostgreSQL invalid completion changed operation: %+v, %v", persisted, err)
			}
			var persistedSource MarketplaceSourceRow
			if err := store.db.Where("id = ?", source.ID).First(&persistedSource).Error; err != nil || persistedSource.LastResult != "running" || persistedSource.RefreshLeaseToken != operation.LeaseToken || persistedSource.CurrentSnapshotID != "" {
				t.Fatalf("PostgreSQL invalid completion changed source: %+v, %v", persistedSource, err)
			}
			var snapshotCount, auditCount int64
			_ = store.db.Model(&MarketSnapshotRow{}).Where("source_id = ?", source.ID).Count(&snapshotCount).Error
			_ = store.db.Model(&AuditEventRow{}).Where("action = ? AND target_id = ?", "marketplace.source.refresh", source.ID).Count(&auditCount).Error
			if snapshotCount != 0 || auditCount != 0 {
				t.Fatalf("PostgreSQL invalid completion left snapshot/audit: %d/%d", snapshotCount, auditCount)
			}
		})
	}
	for index, promotion := range []bool{false, true} {
		name := "failure"
		if promotion {
			name = "promotion"
		}
		t.Run(name+" renewed durable lease", func(t *testing.T) {
			slug := fmt.Sprintf("postgres-renewed-%d", index)
			source, err := marketplace.NewCustomSource(slug, slug, "https://example.com/"+slug+".git", "main", "", 0)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.SaveMarketplaceSource(t.Context(), source); err != nil {
				t.Fatal(err)
			}
			startedAt := time.Now().UTC().Add(-time.Second)
			operation := refreshOperationForSource(source, marketplace.RefreshOperation{ID: slug + "-refresh", SourceID: source.ID, Commit: slug + "-commit", Status: "running", StartedAt: startedAt, LeaseToken: slug + "-lease", LeaseExpiresAt: time.Now().UTC().Add(time.Minute)})
			if err := store.AcquireRefreshLease(t.Context(), operation); err != nil {
				t.Fatal(err)
			}
			renewal := operation
			renewal.LeaseExpiresAt = time.Now().UTC().Add(2 * time.Minute)
			if err := store.RenewRefreshLease(t.Context(), renewal); err != nil {
				t.Fatal(err)
			}
			finished := time.Now().UTC()
			operation.LeaseExpiresAt = finished.Add(-time.Second)
			operation.FinishedAt = &finished
			if promotion {
				operation.Status = "succeeded"
				snapshot := marketplace.Snapshot{ID: slug + "-snapshot", SourceID: source.ID, Commit: operation.Commit, Path: slug + "-snapshot", ValidatedAt: finished}
				reservationPath := filepath.Join(store.dataRoot, "marketplace", "snapshots", snapshot.Path)
				if err := store.RegisterMarketplaceDirectoryCleanup(t.Context(), source.ID, operation.ID, []string{reservationPath}); err != nil {
					t.Fatal(err)
				}
				if err := store.PromoteSnapshotAndCompleteRefresh(t.Context(), source, snapshot, operation); err != nil {
					t.Fatalf("PostgreSQL renewed durable promotion rejected stale caller lease metadata: %v", err)
				}
			} else {
				operation.Status, operation.ErrorClass, operation.Error = "failed", "fetch", "offline"
				if err := store.SaveRefreshOperation(t.Context(), operation); err != nil {
					t.Fatalf("PostgreSQL renewed durable failure rejected stale caller lease metadata: %v", err)
				}
			}
			var persisted MarketplaceRefreshOperationRow
			if err := store.db.Where("id = ?", operation.ID).First(&persisted).Error; err != nil || persisted.Status != operation.Status || persisted.FinishedAt == nil {
				t.Fatalf("PostgreSQL renewed durable completion = %+v, %v", persisted, err)
			}
		})
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
