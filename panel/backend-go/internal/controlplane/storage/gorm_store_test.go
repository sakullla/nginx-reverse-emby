//go:build !integration

package storage

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"
)

type dryRunConnPool struct{}

func (dryRunConnPool) PrepareContext(context.Context, string) (*sql.Stmt, error) {
	return nil, nil
}

func (dryRunConnPool) ExecContext(context.Context, string, ...interface{}) (sql.Result, error) {
	return nil, nil
}

func (dryRunConnPool) QueryContext(context.Context, string, ...interface{}) (*sql.Rows, error) {
	return nil, nil
}

func (dryRunConnPool) QueryRowContext(context.Context, string, ...interface{}) *sql.Row {
	return nil
}

func TestNewStoreRejectsUnsupportedDriver(t *testing.T) {
	t.Parallel()
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("NewStore panicked: %v", recovered)
		}
	}()

	_, err := NewStore(StoreConfig{
		Driver:       "oracle",
		DataRoot:     t.TempDir(),
		LocalAgentID: "local",
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported database driver") {
		t.Fatalf("NewStore() error = %v, want unsupported database driver error", err)
	}
}

func TestNewStoreEnablesSQLiteWALForDefaultDSN(t *testing.T) {
	t.Parallel()
	store, err := NewStore(StoreConfig{
		Driver:              "sqlite",
		DataRoot:            t.TempDir(),
		LocalAgentID:        "local",
		SkipBootstrapSchema: true,
	})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	var journalMode string
	if err := store.db.Raw("PRAGMA journal_mode").Scan(&journalMode).Error; err != nil {
		t.Fatalf("PRAGMA journal_mode error = %v", err)
	}
	if journalMode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", journalMode)
	}
	var synchronous int
	if err := store.db.Raw("PRAGMA synchronous").Scan(&synchronous).Error; err != nil {
		t.Fatalf("PRAGMA synchronous error = %v", err)
	}
	if synchronous != 1 {
		t.Fatalf("synchronous = %d, want 1 (NORMAL)", synchronous)
	}
	var cacheSize int
	if err := store.db.Raw("PRAGMA cache_size").Scan(&cacheSize).Error; err != nil {
		t.Fatalf("PRAGMA cache_size error = %v", err)
	}
	if cacheSize != -65536 {
		t.Fatalf("cache_size = %d, want -65536", cacheSize)
	}
	var tempStore int
	if err := store.db.Raw("PRAGMA temp_store").Scan(&tempStore).Error; err != nil {
		t.Fatalf("PRAGMA temp_store error = %v", err)
	}
	if tempStore != 2 {
		t.Fatalf("temp_store = %d, want 2 (MEMORY)", tempStore)
	}
}

func TestBootstrapSchemaUpgradesOriginalPluginDigestPrimaryKeys(t *testing.T) {
	t.Parallel()
	store, err := NewStore(StoreConfig{
		Driver: "sqlite", DSN: t.TempDir() + "/legacy-plugin.db", DataRoot: t.TempDir(), LocalAgentID: "local", SkipBootstrapSchema: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	digest := strings.Repeat("a", 64)
	legacyDDL := []string{
		`CREATE TABLE plugin_packages (digest text PRIMARY KEY, plugin_id text NOT NULL, version text NOT NULL, cache_path text NOT NULL, manifest_json text NOT NULL, config_schema_json text NOT NULL, verified_at datetime NOT NULL)`,
		`CREATE TABLE plugin_package_acquisitions (source_id text NOT NULL, digest text NOT NULL, snapshot_id text NOT NULL DEFAULT '', status text NOT NULL, updated_at datetime NOT NULL, PRIMARY KEY (source_id,digest))`,
		`CREATE TABLE plugin_package_staging (source_id text NOT NULL, operation_id text NOT NULL, digest text NOT NULL, updated_at datetime NOT NULL, PRIMARY KEY (source_id,operation_id,digest))`,
		`CREATE TABLE plugin_cache_gc_intents (source_id text NOT NULL, digest text NOT NULL, status text NOT NULL, deferred numeric NOT NULL DEFAULT false, claim_token text NOT NULL DEFAULT '', claim_expires_at datetime, quarantine_path text NOT NULL DEFAULT '', last_error text NOT NULL, updated_at datetime NOT NULL, PRIMARY KEY (source_id,digest))`,
		`CREATE TABLE marketplace_sources (id text PRIMARY KEY)`,
		`INSERT INTO plugin_packages (digest,plugin_id,version,cache_path,manifest_json,config_schema_json,verified_at) VALUES ('` + digest + `','legacy.plugin','1.0.0','legacy/package','{}','{}',CURRENT_TIMESTAMP)`,
		`INSERT INTO plugin_cache_gc_intents (source_id,digest,status,last_error,updated_at) VALUES ('legacy-source','` + digest + `','pending','',CURRENT_TIMESTAMP)`,
	}
	for _, statement := range legacyDDL {
		if err := store.db.Exec(statement).Error; err != nil {
			t.Fatalf("legacy schema setup failed: %v", err)
		}
	}
	if err := BootstrapSchema(t.Context(), store.db, SchemaOptionsForDriver("sqlite", false)); err != nil {
		t.Fatalf("BootstrapSchema() from original plugin tables error = %v", err)
	}
	type tableColumn struct {
		Name string `gorm:"column:name"`
		PK   int    `gorm:"column:pk"`
	}
	var packageColumns []tableColumn
	if err := store.db.Raw("PRAGMA table_info('plugin_packages')").Scan(&packageColumns).Error; err != nil {
		t.Fatal(err)
	}
	packagePK := map[string]int{}
	for _, column := range packageColumns {
		packagePK[column.Name] = column.PK
	}
	if packagePK["identity"] != 1 || packagePK["digest"] != 0 {
		t.Fatalf("plugin_packages primary key = %#v, want identity only", packagePK)
	}
	var gcColumns []tableColumn
	if err := store.db.Raw("PRAGMA table_info('plugin_cache_gc_intents')").Scan(&gcColumns).Error; err != nil {
		t.Fatal(err)
	}
	gcPK := map[string]int{}
	for _, column := range gcColumns {
		gcPK[column.Name] = column.PK
	}
	if gcPK["source_id"] != 1 || gcPK["digest"] != 2 || gcPK["signer_fingerprint"] != 3 {
		t.Fatalf("plugin_cache_gc_intents primary key = %#v, want source/digest/fingerprint", gcPK)
	}
	firstFingerprint := strings.Repeat("b", 64)
	secondFingerprint := strings.Repeat("c", 64)
	for _, fingerprint := range []string{firstFingerprint, secondFingerprint} {
		if err := store.db.Create(&PluginCacheGCIntentRow{SourceID: "legacy-source", Digest: digest, SignerFingerprint: fingerprint, Status: "pending", UpdatedAt: time.Now().UTC()}).Error; err != nil {
			t.Fatalf("same digest signer variant insert failed after upgrade: %v", err)
		}
	}
	var variantCount int64
	if err := store.db.Model(&PluginCacheGCIntentRow{}).Where("source_id = ? AND digest = ?", "legacy-source", digest).Count(&variantCount).Error; err != nil || variantCount != 2 {
		t.Fatalf("same digest signer variant count = %d, %v", variantCount, err)
	}
}
