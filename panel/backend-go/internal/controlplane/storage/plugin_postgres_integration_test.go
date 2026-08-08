//go:build integration

package storage

import (
	"os"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestPostgresPluginVariantMigrationFromDigestIdentity(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("NRE_TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("NRE_TEST_POSTGRES_DSN is not configured")
	}
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
