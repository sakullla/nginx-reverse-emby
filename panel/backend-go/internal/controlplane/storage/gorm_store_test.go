package storage

import (
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"gorm.io/gorm"
)

func TestStoreConfigFromConfigPassesDatabaseSettings(t *testing.T) {
	cfg := config.Default()
	cfg.DatabaseDriver = "postgres"
	cfg.DatabaseDSN = "postgres://nre:nre@postgres:5432/nre?sslmode=disable"
	cfg.DataDir = "/tmp/nre-data"
	cfg.LocalAgentID = "edge-1"
	cfg.TrafficStatsEnabled = false
	cfg.WireGuardEnabled = false
	cfg.WireGuardExplicit = true

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
	if storeCfg.WireGuardEnabled {
		t.Fatal("WireGuardEnabled = true, want false")
	}
}

func TestNewStoreRejectsUnsupportedDriver(t *testing.T) {
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
}

func TestResolveDialectorAllowsSQLiteDSNWithoutDataRoot(t *testing.T) {
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
}

func TestWithSQLiteLockPragmasPreservesExistingQuery(t *testing.T) {
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
}

func TestWithSQLiteLockPragmasIgnoresNonPragmaMatches(t *testing.T) {
	got := withSQLiteLockPragmas("/tmp/journal_mode/panel.db?label=busy_timeout")

	if !strings.Contains(got, "_pragma=journal_mode(WAL)") {
		t.Fatalf("DSN = %q, want WAL pragma", got)
	}
	if !strings.Contains(got, "_pragma=busy_timeout(5000)") {
		t.Fatalf("DSN = %q, want busy_timeout pragma", got)
	}
}

func TestWithSQLiteLockPragmasSkipsWALForReadOnlyURI(t *testing.T) {
	got := withSQLiteLockPragmas("file:/tmp/panel.db?mode=ro")

	if strings.Contains(strings.ToLower(got), "journal_mode") {
		t.Fatalf("DSN = %q, want no journal_mode pragma", got)
	}
	if !strings.Contains(got, "_pragma=busy_timeout(5000)") {
		t.Fatalf("DSN = %q, want busy_timeout pragma", got)
	}
}

func TestWithSQLiteLockPragmasPreservesExplicitLockPragmas(t *testing.T) {
	dsn := "/tmp/panel.db?_pragma=journal_mode(TRUNCATE)&_pragma=busy_timeout(10000)"

	if got := withSQLiteLockPragmas(dsn); got != dsn {
		t.Fatalf("DSN = %q, want %q", got, dsn)
	}
}

func TestWithSQLiteLockPragmasSkipsInMemoryDSN(t *testing.T) {
	for _, dsn := range []string{":memory:", "file::memory:?cache=shared"} {
		t.Run(dsn, func(t *testing.T) {
			if got := withSQLiteLockPragmas(dsn); got != dsn {
				t.Fatalf("DSN = %q, want %q", got, dsn)
			}
		})
	}
}

func TestSchemaOptionsForDriverGatesSQLiteLegacyMigrations(t *testing.T) {
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
			if options.SQLiteLegacyMigrations != tc.want {
				t.Fatalf("SQLiteLegacyMigrations = %v, want %v", options.SQLiteLegacyMigrations, tc.want)
			}
			if !options.TrafficStatsEnabled {
				t.Fatal("TrafficStatsEnabled = false, want true")
			}
		})
	}
}
