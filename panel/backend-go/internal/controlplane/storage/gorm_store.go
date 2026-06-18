package storage

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/glebarez/sqlite"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type GormStore struct {
	db           *gorm.DB
	writeDB      *gorm.DB
	writeDSN     string
	dataRoot     string
	localAgentID string
	driver       string
	wireGuard    bool
	sqliteWrite  sync.Mutex
}

type StoreConfig struct {
	Driver              string
	DSN                 string
	DataRoot            string
	LocalAgentID        string
	SkipBootstrapSchema bool
	TrafficStatsEnabled bool
	WireGuardEnabled    bool
	WireGuardExplicit   bool
}

func StoreConfigFromConfig(cfg config.Config) StoreConfig {
	return StoreConfig{
		Driver:              cfg.DatabaseDriver,
		DSN:                 cfg.DatabaseDSN,
		DataRoot:            cfg.DataDir,
		LocalAgentID:        cfg.LocalAgentID,
		TrafficStatsEnabled: cfg.TrafficStatsEnabled,
		WireGuardEnabled:    cfg.WireGuardModuleEnabled(),
		WireGuardExplicit:   true,
	}
}

func NewConfiguredStore(cfg config.Config) (*GormStore, error) {
	return NewStore(StoreConfigFromConfig(cfg))
}

func (s *GormStore) writeTransaction(ctx context.Context, fn func(*gorm.DB) error) error {
	db := s.db
	if s.driver == "sqlite" {
		s.sqliteWrite.Lock()
		defer s.sqliteWrite.Unlock()
		if err := s.ensureSQLiteWriteDB(); err != nil {
			return err
		}
		if s.writeDB != nil {
			db = s.writeDB
		}
	}
	return db.WithContext(ctx).Transaction(fn)
}

func (s *GormStore) ensureSQLiteWriteDB() error {
	if s.writeDB != nil || strings.TrimSpace(s.writeDSN) == "" {
		return nil
	}
	writeDB, err := gorm.Open(sqlite.Open(s.writeDSN), &gorm.Config{})
	if err != nil {
		return err
	}
	if sqlDB, err := writeDB.DB(); err == nil {
		sqlDB.SetMaxOpenConns(1)
		sqlDB.SetMaxIdleConns(1)
	}
	s.writeDB = writeDB
	return nil
}

func NewStore(cfg StoreConfig) (*GormStore, error) {
	driver := strings.ToLower(strings.TrimSpace(cfg.Driver))
	if driver == "" {
		driver = "sqlite"
	}
	if driver == "sqlite" && strings.TrimSpace(cfg.DSN) == "" {
		if strings.TrimSpace(cfg.DataRoot) == "" {
			return nil, fmt.Errorf("data root is required for default sqlite DSN")
		}
		if err := os.MkdirAll(cfg.DataRoot, 0o755); err != nil {
			return nil, err
		}
	}

	dialector, err := resolveDialector(driver, cfg)
	if err != nil {
		return nil, err
	}
	db, err := gorm.Open(dialector, &gorm.Config{})
	if err != nil {
		return nil, err
	}
	var writeDSN string
	if driver == "sqlite" {
		sqliteDSN, err := resolveSQLiteDSN(cfg)
		if err != nil {
			sqlDB, dbErr := db.DB()
			if dbErr == nil {
				_ = sqlDB.Close()
			}
			return nil, err
		}
		sqliteDSN = withSQLiteLockPragmas(sqliteDSN)
		writeDSN = withSQLiteWriterOptions(sqliteDSN)
	}
	store := &GormStore{
		db:           db,
		writeDSN:     writeDSN,
		dataRoot:     cfg.DataRoot,
		localAgentID: cfg.LocalAgentID,
		driver:       driver,
		wireGuard:    storeWireGuardEnabled(cfg),
	}
	if !cfg.SkipBootstrapSchema {
		if err := BootstrapSchema(context.Background(), db, SchemaOptionsForDriver(driver, cfg.TrafficStatsEnabled, store.wireGuard)); err != nil {
			_ = store.Close()
			return nil, err
		}
	}
	return store, nil
}

func storeWireGuardEnabled(cfg StoreConfig) bool {
	return cfg.WireGuardEnabled || !cfg.WireGuardExplicit
}

func resolveDialector(driver string, cfg StoreConfig) (gorm.Dialector, error) {
	switch driver {
	case "postgres":
		return postgres.Open(cfg.DSN), nil
	case "mysql":
		return mysql.Open(cfg.DSN), nil
	case "sqlite":
		dsn, err := resolveSQLiteDSN(cfg)
		if err != nil {
			return nil, err
		}
		dsn = withSQLiteLockPragmas(dsn)
		return sqlite.Open(dsn), nil
	default:
		return nil, fmt.Errorf("unsupported database driver %q", driver)
	}
}

func resolveSQLiteDSN(cfg StoreConfig) (string, error) {
	dsn := strings.TrimSpace(cfg.DSN)
	if dsn != "" {
		return dsn, nil
	}
	if strings.TrimSpace(cfg.DataRoot) == "" {
		return "", fmt.Errorf("data root is required for default sqlite DSN")
	}
	return filepath.Join(cfg.DataRoot, "panel.db"), nil
}

func withSQLiteLockPragmas(dsn string) string {
	if isSQLiteInMemoryDSN(dsn) {
		return dsn
	}
	hasJournalMode, hasBusyTimeout, hasSynchronous, hasCacheSize, hasTempStore := sqliteLockPragmasConfigured(dsn)
	pragmas := []string{}
	if !hasJournalMode && !isSQLiteReadOnlyDSN(dsn) {
		pragmas = append(pragmas, "_pragma=journal_mode(WAL)")
	}
	if !hasBusyTimeout {
		pragmas = append(pragmas, "_pragma=busy_timeout(5000)")
	}
	if !hasSynchronous && !isSQLiteReadOnlyDSN(dsn) {
		pragmas = append(pragmas, "_pragma=synchronous(NORMAL)")
	}
	if !hasCacheSize && !isSQLiteReadOnlyDSN(dsn) {
		pragmas = append(pragmas, "_pragma=cache_size(-65536)")
	}
	if !hasTempStore && !isSQLiteReadOnlyDSN(dsn) {
		pragmas = append(pragmas, "_pragma=temp_store(MEMORY)")
	}
	if len(pragmas) == 0 {
		return dsn
	}
	separator := "?"
	if strings.Contains(dsn, "?") {
		separator = "&"
	}
	if strings.HasSuffix(dsn, "?") || strings.HasSuffix(dsn, "&") {
		separator = ""
	}
	return dsn + separator + strings.Join(pragmas, "&")
}

func withSQLiteWriterOptions(dsn string) string {
	if isSQLiteInMemoryDSN(dsn) || isSQLiteReadOnlyDSN(dsn) {
		return ""
	}
	queryStart := strings.Index(dsn, "?")
	if queryStart >= 0 && queryStart < len(dsn)-1 {
		values, err := url.ParseQuery(dsn[queryStart+1:])
		if err == nil && strings.TrimSpace(values.Get("_txlock")) != "" {
			return dsn
		}
	}
	separator := "?"
	if strings.Contains(dsn, "?") {
		separator = "&"
	}
	if strings.HasSuffix(dsn, "?") || strings.HasSuffix(dsn, "&") {
		separator = ""
	}
	return dsn + separator + "_txlock=immediate"
}

func sqliteLockPragmasConfigured(dsn string) (hasJournalMode bool, hasBusyTimeout bool, hasSynchronous bool, hasCacheSize bool, hasTempStore bool) {
	queryStart := strings.Index(dsn, "?")
	if queryStart < 0 || queryStart == len(dsn)-1 {
		return false, false, false, false, false
	}
	values, err := url.ParseQuery(dsn[queryStart+1:])
	if err != nil {
		return false, false, false, false, false
	}
	for _, pragma := range values["_pragma"] {
		name := strings.ToLower(strings.TrimSpace(pragma))
		if separator := strings.IndexAny(name, "(="); separator >= 0 {
			name = strings.TrimSpace(name[:separator])
		}
		switch name {
		case "journal_mode":
			hasJournalMode = true
		case "busy_timeout":
			hasBusyTimeout = true
		case "synchronous":
			hasSynchronous = true
		case "cache_size":
			hasCacheSize = true
		case "temp_store":
			hasTempStore = true
		}
	}
	return hasJournalMode, hasBusyTimeout, hasSynchronous, hasCacheSize, hasTempStore
}

func isSQLiteInMemoryDSN(dsn string) bool {
	trimmed := strings.TrimSpace(dsn)
	lower := strings.ToLower(trimmed)
	if lower == ":memory:" || strings.HasPrefix(lower, "file::memory:") {
		return true
	}
	queryStart := strings.Index(trimmed, "?")
	if queryStart < 0 || queryStart == len(trimmed)-1 {
		return false
	}
	values, err := url.ParseQuery(trimmed[queryStart+1:])
	if err != nil {
		return false
	}
	mode := strings.ToLower(strings.TrimSpace(values.Get("mode")))
	return mode == "memory"
}

func isSQLiteReadOnlyDSN(dsn string) bool {
	queryStart := strings.Index(dsn, "?")
	if queryStart < 0 || queryStart == len(dsn)-1 {
		return false
	}
	values, err := url.ParseQuery(dsn[queryStart+1:])
	if err != nil {
		return false
	}
	mode := strings.ToLower(strings.TrimSpace(values.Get("mode")))
	if mode == "ro" {
		return true
	}
	immutable := strings.ToLower(strings.TrimSpace(values.Get("immutable")))
	return immutable == "1" || immutable == "true"
}

func (s *GormStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}
	if err := sqlDB.Close(); err != nil {
		return err
	}
	if s.writeDB == nil {
		return nil
	}
	writeSQLDB, err := s.writeDB.DB()
	if err != nil {
		return err
	}
	return writeSQLDB.Close()
}
