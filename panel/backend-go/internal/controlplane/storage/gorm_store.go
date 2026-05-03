package storage

import (
	"context"
	"fmt"
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
}

type StoreConfig struct {
	Driver              string
	DSN                 string
	DataRoot            string
	LocalAgentID        string
	TrafficStatsEnabled bool
}

func StoreConfigFromConfig(cfg config.Config) StoreConfig {
	return StoreConfig{
		Driver:              cfg.DatabaseDriver,
		DSN:                 cfg.DatabaseDSN,
		DataRoot:            cfg.DataDir,
		LocalAgentID:        cfg.LocalAgentID,
		TrafficStatsEnabled: cfg.TrafficStatsEnabled,
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
	if driver == "sqlite" {
		if err := os.MkdirAll(cfg.DataRoot, 0o755); err != nil {
			return nil, err
		}
	}

	db, err := gorm.Open(resolveDialector(driver, cfg), &gorm.Config{})
	if err != nil {
		return nil, err
	}
	store := &GormStore{
		db:           db,
		dataRoot:     cfg.DataRoot,
		localAgentID: cfg.LocalAgentID,
		driver:       driver,
	}
	if err := BootstrapSchema(context.Background(), db, SchemaOptionsForDriver(driver, cfg.TrafficStatsEnabled)); err != nil {
		_ = store.Close()
		return nil, err
	}
	return store, nil
}

func resolveDialector(driver string, cfg StoreConfig) gorm.Dialector {
	switch driver {
	case "postgres":
		return postgres.Open(cfg.DSN)
	case "mysql":
		return mysql.Open(cfg.DSN)
	case "sqlite":
		dsn := strings.TrimSpace(cfg.DSN)
		if dsn == "" {
			dsn = filepath.Join(cfg.DataRoot, "panel.db") + "?_journal_mode=WAL&_busy_timeout=5000"
		}
		return sqlite.Open(dsn)
	default:
		panic(fmt.Sprintf("unsupported database driver %q", driver))
	}
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
