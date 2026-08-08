package storage

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
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

func TestGetAgentReportedRevisionSkipsLocalAgentTable(t *testing.T) {
	t.Parallel()
	store := &GormStore{localAgentID: "local"}

	revision, found, err := store.GetAgentReportedRevision(t.Context(), "local")
	if err != nil {
		t.Fatalf("GetAgentReportedRevision() error = %v", err)
	}
	if found || revision != 0 {
		t.Fatalf("GetAgentReportedRevision() = (%d, %v), want (0, false)", revision, found)
	}
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

func TestPostgresTrafficBlockedNormalizationUsesBooleanValue(t *testing.T) {
	t.Parallel()
	db, err := gorm.Open(postgres.New(postgres.Config{
		DSN:                  "postgres://nre:nre@localhost:5432/nre?sslmode=disable",
		Conn:                 dryRunConnPool{},
		PreferSimpleProtocol: true,
		WithoutReturning:     true,
	}), &gorm.Config{
		DryRun: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	stmt := db.Model(&AgentRow{}).Where("traffic_blocked IS NULL").Update("traffic_blocked", false).Statement
	sql := stmt.SQL.String()
	if strings.Contains(sql, "traffic_blocked = 0") || strings.Contains(sql, `"traffic_blocked"=0`) {
		t.Fatalf("postgres traffic_blocked normalization SQL = %q, want boolean parameter", sql)
	}
	if len(stmt.Vars) == 0 || stmt.Vars[0] != false {
		t.Fatalf("postgres traffic_blocked normalization vars = %#v, want first var false", stmt.Vars)
	}
}

func TestManagedCertificatePointerSnapshotUsesServerRowLock(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		dialector gorm.Dialector
	}{
		{
			name: "postgres",
			dialector: postgres.New(postgres.Config{
				DSN:                  "postgres://nre:nre@localhost:5432/nre?sslmode=disable",
				Conn:                 dryRunConnPool{},
				PreferSimpleProtocol: true,
				WithoutReturning:     true,
			}),
		},
		{
			name: "mysql",
			dialector: mysql.New(mysql.Config{
				DSN:                       "nre:nre@tcp(localhost:3306)/nre",
				Conn:                      dryRunConnPool{},
				SkipInitializeWithVersion: true,
			}),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db, err := gorm.Open(test.dialector, &gorm.Config{DryRun: true})
			if err != nil {
				t.Fatalf("gorm.Open() error = %v", err)
			}
			var rows []ManagedCertificateRow
			statement := managedCertificatePointerSnapshotQuery(db).Find(&rows).Statement
			if sql := strings.ToUpper(statement.SQL.String()); !strings.Contains(sql, "FOR UPDATE") {
				t.Fatalf("pointer snapshot SQL = %q, want FOR UPDATE", statement.SQL.String())
			}
		})
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

func TestResolveDialectorAllowsSQLiteDSNWithoutDataRoot(t *testing.T) {
	t.Parallel()
	dbPath := t.TempDir() + "/panel.db"
	dialector, err := resolveDialector("sqlite", StoreConfig{DSN: dbPath})
	if err != nil {
		t.Fatalf("resolveDialector() error = %v", err)
	}
	if _, ok := dialector.(*sqlite.Dialector); !ok {
		t.Fatalf("dialector type = %T, want sqlite.Dialector", dialector)
	}
	db, err := gorm.Open(dialector, &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open() error = %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB() error = %v", err)
	}
	_ = sqlDB.Close()
}

func TestNewStoreAppliesSQLiteLockPragmasToExplicitFileDSN(t *testing.T) {
	t.Parallel()
	store, err := NewStore(StoreConfig{
		Driver:              "sqlite",
		DSN:                 t.TempDir() + "/panel.db",
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

	var busyTimeout int
	if err := store.db.Raw("PRAGMA busy_timeout").Scan(&busyTimeout).Error; err != nil {
		t.Fatalf("PRAGMA busy_timeout error = %v", err)
	}
	if busyTimeout < 5000 {
		t.Fatalf("busy_timeout = %d, want at least 5000", busyTimeout)
	}
	if store.writeDB != nil {
		t.Fatal("writeDB is open before first write, want lazy writer connection")
	}
	if err := store.writeTransaction(t.Context(), func(tx *gorm.DB) error {
		return nil
	}); err != nil {
		t.Fatalf("writeTransaction() error = %v", err)
	}
	if store.writeDB == nil {
		t.Fatal("writeDB is nil, want dedicated sqlite writer connection")
	}
	sqlDB, err := store.writeDB.DB()
	if err != nil {
		t.Fatalf("writeDB.DB() error = %v", err)
	}
	stats := sqlDB.Stats()
	if stats.MaxOpenConnections != 1 {
		t.Fatalf("writer MaxOpenConnections = %d, want 1", stats.MaxOpenConnections)
	}
}

func TestWithSQLiteLockPragmasPreservesExistingQuery(t *testing.T) {
	t.Parallel()
	got := withSQLiteLockPragmas("/tmp/panel.db?cache=shared")

	if !strings.Contains(got, "/tmp/panel.db?cache=shared") {
		t.Fatalf("DSN = %q, want original query preserved", got)
	}
	if !strings.Contains(got, "_pragma=journal_mode(WAL)") {
		t.Fatalf("DSN = %q, want WAL pragma", got)
	}
	if !strings.Contains(got, "_pragma=busy_timeout(5000)") {
		t.Fatalf("DSN = %q, want busy_timeout pragma", got)
	}
	if !strings.Contains(got, "_pragma=synchronous(NORMAL)") {
		t.Fatalf("DSN = %q, want synchronous pragma", got)
	}
	if !strings.Contains(got, "_pragma=cache_size(-65536)") {
		t.Fatalf("DSN = %q, want cache_size pragma", got)
	}
	if !strings.Contains(got, "_pragma=temp_store(MEMORY)") {
		t.Fatalf("DSN = %q, want temp_store pragma", got)
	}
}

func TestWithSQLiteLockPragmasIgnoresNonPragmaMatches(t *testing.T) {
	t.Parallel()
	got := withSQLiteLockPragmas("/tmp/journal_mode/panel.db?label=busy_timeout")

	if !strings.Contains(got, "_pragma=journal_mode(WAL)") {
		t.Fatalf("DSN = %q, want WAL pragma", got)
	}
	if !strings.Contains(got, "_pragma=busy_timeout(5000)") {
		t.Fatalf("DSN = %q, want busy_timeout pragma", got)
	}
	if !strings.Contains(got, "_pragma=synchronous(NORMAL)") {
		t.Fatalf("DSN = %q, want synchronous pragma", got)
	}
	if !strings.Contains(got, "_pragma=cache_size(-65536)") {
		t.Fatalf("DSN = %q, want cache_size pragma", got)
	}
	if !strings.Contains(got, "_pragma=temp_store(MEMORY)") {
		t.Fatalf("DSN = %q, want temp_store pragma", got)
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

func TestWithSQLiteLockPragmasPreservesExplicitLockPragmas(t *testing.T) {
	t.Parallel()
	for _, dsn := range []string{
		"/tmp/panel.db?_pragma=journal_mode(TRUNCATE)&_pragma=busy_timeout(10000)&_pragma=synchronous(FULL)&_pragma=cache_size(-1024)&_pragma=temp_store(FILE)",
		"/tmp/panel.db?_pragma=journal_mode=DELETE&_pragma=busy_timeout=10000&_pragma=synchronous=FULL&_pragma=cache_size=-1024&_pragma=temp_store=FILE",
	} {
		t.Run(dsn, func(t *testing.T) {
			if got := withSQLiteLockPragmas(dsn); got != dsn {
				t.Fatalf("DSN = %q, want %q", got, dsn)
			}
		})
	}
}

func TestWithSQLiteLockPragmasSkipsInMemoryDSN(t *testing.T) {
	t.Parallel()
	for _, dsn := range []string{":memory:", "file::memory:?cache=shared", "file:memdb1?mode=memory&cache=shared"} {
		t.Run(dsn, func(t *testing.T) {
			if got := withSQLiteLockPragmas(dsn); got != dsn {
				t.Fatalf("DSN = %q, want %q", got, dsn)
			}
		})
	}
}

func TestWithSQLiteWriterOptionsAddsImmediateTxLock(t *testing.T) {
	t.Parallel()
	got := withSQLiteWriterOptions("/tmp/panel.db?_pragma=journal_mode(WAL)")

	if !strings.Contains(got, "/tmp/panel.db?_pragma=journal_mode(WAL)") {
		t.Fatalf("DSN = %q, want original query preserved", got)
	}
	if !strings.Contains(got, "_txlock=immediate") {
		t.Fatalf("DSN = %q, want immediate txlock", got)
	}
}

func TestWithSQLiteWriterOptionsPreservesExplicitTxLock(t *testing.T) {
	t.Parallel()
	dsn := "/tmp/panel.db?_txlock=deferred"

	if got := withSQLiteWriterOptions(dsn); got != dsn {
		t.Fatalf("DSN = %q, want %q", got, dsn)
	}
}

func TestWithSQLiteWriterOptionsSkipsReadOnlyAndInMemoryDSN(t *testing.T) {
	t.Parallel()
	for _, dsn := range []string{":memory:", "file::memory:?cache=shared", "file:/tmp/panel.db?mode=ro"} {
		t.Run(dsn, func(t *testing.T) {
			if got := withSQLiteWriterOptions(dsn); got != "" {
				t.Fatalf("DSN = %q, want empty writer DSN", got)
			}
		})
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

func TestPostgresPluginVariantMigrationDDLReplacesDigestUniqueness(t *testing.T) {
	t.Parallel()
	joined := strings.ToLower(strings.Join(postgresPluginVariantMigrationStatements(), "\n"))
	for _, required := range []string{
		"add column if not exists identity",
		"update plugin_packages set identity = digest",
		"contype='u'",
		"i.indisunique and not i.indisprimary",
		"add primary key (identity)",
		"idx_plugin_packages_digest",
		"add column if not exists signer_fingerprint",
		"add primary key (source_id,digest,signer_fingerprint)",
	} {
		if !strings.Contains(joined, required) {
			t.Fatalf("PostgreSQL plugin variant migration is missing %q", required)
		}
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
