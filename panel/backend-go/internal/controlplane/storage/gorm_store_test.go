package storage

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
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

func TestNewStoreAllowsSQLiteDSNWithoutDataRoot(t *testing.T) {
	t.Parallel()
	dbPath := t.TempDir() + "/panel.db"
	store, err := NewStore(StoreConfig{
		Driver:              "sqlite",
		DSN:                 dbPath,
		LocalAgentID:        "local",
		SkipBootstrapSchema: true,
	})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
}

func TestNewStoreRequiresDataRootForDefaultSQLiteDSN(t *testing.T) {
	t.Parallel()
	_, err := NewStore(StoreConfig{
		Driver:              "sqlite",
		LocalAgentID:        "local",
		SkipBootstrapSchema: true,
	})
	if err == nil || !strings.Contains(err.Error(), "data root is required") {
		t.Fatalf("NewStore() error = %v, want data root is required", err)
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

func TestWithSQLiteLockPragmasSkipsWALForReadOnlyURI(t *testing.T) {
	t.Parallel()
	got := withSQLiteLockPragmas("file:/tmp/panel.db?mode=ro")

	if strings.Contains(strings.ToLower(got), "journal_mode") {
		t.Fatalf("DSN = %q, want no journal_mode pragma", got)
	}
	if strings.Contains(strings.ToLower(got), "synchronous") {
		t.Fatalf("DSN = %q, want no synchronous pragma", got)
	}
	if strings.Contains(strings.ToLower(got), "cache_size") {
		t.Fatalf("DSN = %q, want no cache_size pragma", got)
	}
	if strings.Contains(strings.ToLower(got), "temp_store") {
		t.Fatalf("DSN = %q, want no temp_store pragma", got)
	}
	if !strings.Contains(got, "_pragma=busy_timeout(5000)") {
		t.Fatalf("DSN = %q, want busy_timeout pragma", got)
	}
}

func TestSchemaOptionsForDriverGatesSQLiteLegacyMigrations(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		driver string
		want   bool
	}{
		{driver: "", want: true},
		{driver: "sqlite", want: true},
		{driver: " SQLite ", want: true},
		{driver: "postgres", want: false},
		{driver: "mysql", want: false},
	}

	for _, tc := range testCases {
		t.Run(tc.driver, func(t *testing.T) {
			options := SchemaOptionsForDriver(tc.driver, true)
			if options.Driver != strings.ToLower(strings.TrimSpace(tc.driver)) {
				t.Fatalf("Driver = %q", options.Driver)
			}
			if options.SQLiteLegacyMigrations != tc.want {
				t.Fatalf("SQLiteLegacyMigrations = %v, want %v", options.SQLiteLegacyMigrations, tc.want)
			}
			if !options.TrafficStatsEnabled {
				t.Fatal("TrafficStatsEnabled = false, want true")
			}
		})
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

func TestSQLiteLegacyGCIntentBackfillChoosesStablePackageVariant(t *testing.T) {
	t.Parallel()
	store, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	digest := strings.Repeat("d", 64)
	firstFingerprint := strings.Repeat("1", 64)
	secondFingerprint := strings.Repeat("2", 64)
	now := time.Now().UTC()
	for _, row := range []PluginPackageRow{
		{Identity: strings.Repeat("a", 64), Digest: digest, PluginID: "legacy.gc", Version: "1.0.0", SourceID: "legacy-source", SignatureFingerprint: firstFingerprint, CachePath: "first", ManifestJSON: `{}`, ConfigSchemaJSON: `{}`, VerifiedAt: now},
		{Identity: strings.Repeat("b", 64), Digest: digest, PluginID: "legacy.gc", Version: "1.0.0", SourceID: "legacy-source", SignatureFingerprint: secondFingerprint, CachePath: "second", ManifestJSON: `{}`, ConfigSchemaJSON: `{}`, VerifiedAt: now},
	} {
		if err := store.db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := store.db.Exec("DROP TABLE plugin_cache_gc_intents").Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Exec(`CREATE TABLE plugin_cache_gc_intents (
source_id text NOT NULL, digest text NOT NULL, status text NOT NULL, deferred numeric NOT NULL DEFAULT false,
claim_token text NOT NULL DEFAULT '', claim_expires_at datetime, quarantine_path text NOT NULL DEFAULT '',
last_error text NOT NULL, updated_at datetime NOT NULL, PRIMARY KEY (source_id,digest))`).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Exec(`INSERT INTO plugin_cache_gc_intents (source_id,digest,status,last_error,updated_at) VALUES (?,?,?,?,?)`, "legacy-source", digest, "pending", "", now).Error; err != nil {
		t.Fatal(err)
	}
	if err := migrateSQLitePluginGCVariantIdentity(t.Context(), store.db); err != nil {
		t.Fatal(err)
	}
	var intent PluginCacheGCIntentRow
	if err := store.db.First(&intent).Error; err != nil || intent.SignerFingerprint != firstFingerprint {
		t.Fatalf("deterministic legacy intent backfill = %+v, %v", intent, err)
	}
}

func TestStoreConfigFromConfigPassesDatabaseSettings(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.DatabaseDriver = "postgres"
	cfg.DatabaseDSN = "postgres://nre:nre@postgres:5432/nre?sslmode=disable"
	cfg.DataDir = "/tmp/nre-data"
	cfg.LocalAgentID = "edge-1"
	cfg.TrafficStatsEnabled = false

	storeCfg := StoreConfigFromConfig(cfg)

	if storeCfg.Driver != "postgres" {
		t.Fatalf("Driver = %q", storeCfg.Driver)
	}
	if storeCfg.DSN != "postgres://nre:nre@postgres:5432/nre?sslmode=disable" {
		t.Fatalf("DSN = %q", storeCfg.DSN)
	}
	if storeCfg.DataRoot != "/tmp/nre-data" {
		t.Fatalf("DataRoot = %q", storeCfg.DataRoot)
	}
	if storeCfg.LocalAgentID != "edge-1" {
		t.Fatalf("LocalAgentID = %q", storeCfg.LocalAgentID)
	}
	if storeCfg.TrafficStatsEnabled {
		t.Fatal("TrafficStatsEnabled = true, want false")
	}
}
