package storage

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/glebarez/sqlite"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type GormStore struct {
	db           *gorm.DB
	dataRoot     string
	localAgentID string
	driver       string
	wireGuard    bool
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
	store := &GormStore{
		db:           db,
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
		dsn := strings.TrimSpace(cfg.DSN)
		if dsn == "" {
			if strings.TrimSpace(cfg.DataRoot) == "" {
				return nil, fmt.Errorf("data root is required for default sqlite DSN")
			}
			dsn = filepath.Join(cfg.DataRoot, "panel.db")
		}
		dsn = withSQLiteLockPragmas(dsn)
		return sqlite.Open(dsn), nil
	default:
		return nil, fmt.Errorf("unsupported database driver %q", driver)
	}
}

func withSQLiteLockPragmas(dsn string) string {
	if isSQLiteInMemoryDSN(dsn) {
		return dsn
	}
	hasJournalMode, hasBusyTimeout := sqliteLockPragmasConfigured(dsn)
	pragmas := []string{}
	if !hasJournalMode && !isSQLiteReadOnlyDSN(dsn) {
		pragmas = append(pragmas, "_pragma=journal_mode(WAL)")
	}
	if !hasBusyTimeout {
		pragmas = append(pragmas, "_pragma=busy_timeout(5000)")
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

func sqliteLockPragmasConfigured(dsn string) (hasJournalMode bool, hasBusyTimeout bool) {
	queryStart := strings.Index(dsn, "?")
	if queryStart < 0 || queryStart == len(dsn)-1 {
		return false, false
	}
	values, err := url.ParseQuery(dsn[queryStart+1:])
	if err != nil {
		return false, false
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
		}
	}
	return hasJournalMode, hasBusyTimeout
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
	return sqlDB.Close()
}
