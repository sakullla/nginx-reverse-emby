package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/glebarez/sqlite"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type GormStore struct {
	db                   *gorm.DB
	writeDB              *gorm.DB
	writeDSN             string
	dataRoot             string
	localAgentID         string
	localAgentPresent    atomic.Bool
	driver               string
	transactionScoped    bool
	certificateGCDomains map[string]struct{}
	sqliteWrite          sync.Mutex
	databaseLifecycle    *databaseLifecycle
	storeConfig          StoreConfig
}

type StoreConfig struct {
	Driver              string
	DSN                 string
	DataRoot            string
	LocalAgentID        string
	LocalAgentPresent   *bool
	SkipBootstrapSchema bool
	TrafficStatsEnabled bool
}

func StoreConfigFromConfig(cfg config.Config) StoreConfig {
	localAgentPresent := cfg.EnableLocalAgent
	return StoreConfig{
		Driver:              cfg.DatabaseDriver,
		DSN:                 cfg.DatabaseDSN,
		DataRoot:            cfg.DataDir,
		LocalAgentID:        cfg.LocalAgentID,
		LocalAgentPresent:   &localAgentPresent,
		TrafficStatsEnabled: cfg.TrafficStatsEnabled,
	}
}

func NewConfiguredStore(cfg config.Config) (*GormStore, error) {
	return NewStore(StoreConfigFromConfig(cfg))
}

func (s *GormStore) writeTransaction(ctx context.Context, fn func(*gorm.DB) error) error {
	return s.writeTransactionWithOptions(ctx, nil, fn)
}

func (s *GormStore) writeTransactionWithOptions(ctx context.Context, options *sql.TxOptions, fn func(*gorm.DB) error) error {
	if s != nil && s.transactionScoped {
		return fn(s.db.WithContext(ctx))
	}
	db := s.db
	if s.driver == "sqlite" {
		if s.databaseLifecycle == nil || s.databaseLifecycle.group == nil {
			return gorm.ErrInvalidDB
		}
		s.databaseLifecycle.group.write.Lock()
		defer s.databaseLifecycle.group.write.Unlock()
		s.sqliteWrite.Lock()
		defer s.sqliteWrite.Unlock()
		if err := s.ensureSQLiteWriteDB(); err != nil {
			return err
		}
		if s.writeDB != nil {
			db = s.writeDB
		}
	}
	return db.WithContext(ctx).Transaction(fn, options)
}

// transactionView creates a transaction-scoped store without copying mutexes
// or atomic values from the owning store. Configuration used by storage
// helpers remains available, while every database operation is bound to tx.
func (s *GormStore) transactionView(tx *gorm.DB) *GormStore {
	view := &GormStore{
		db: tx, writeDB: tx, writeDSN: s.writeDSN,
		dataRoot: s.dataRoot, localAgentID: s.localAgentID, driver: s.driver,
		transactionScoped: true, certificateGCDomains: s.certificateGCDomains,
		databaseLifecycle: s.databaseLifecycle, storeConfig: s.storeConfig,
	}
	view.localAgentPresent.Store(s.localAgentPresent.Load())
	return view
}

func (s *GormStore) readSnapshotTransaction(ctx context.Context, read func(*GormStore) error) error {
	if s == nil || s.db == nil || read == nil {
		return gorm.ErrInvalidDB
	}
	if s.transactionScoped {
		return read(s)
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return read(s.transactionView(tx))
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
}

// PluginReadTransaction exposes a stable read-only snapshot to the plugin
// admin service without leaking the underlying gorm transaction.
func (s *GormStore) PluginReadTransaction(ctx context.Context, read func(*GormStore) error) error {
	return s.readSnapshotTransaction(ctx, read)
}

func (s *GormStore) ensureSQLiteWriteDB() error {
	if s.writeDB != nil || strings.TrimSpace(s.writeDSN) == "" {
		return nil
	}
	writeDB, err := gorm.Open(sqlite.Open(s.writeDSN), &gorm.Config{TranslateError: true})
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
	var lifecycleGroup *databaseLifecycleGroup
	var lifecycleGroupLocked bool
	var lifecycleGroupWriteLocked bool
	var storeRegistered bool
	if driver == "sqlite" {
		sqliteDSN, err := resolveSQLiteDSN(cfg)
		if err != nil {
			return nil, err
		}
		if !isSQLiteInMemoryDSN(sqliteDSN) {
			activeDatabasePath, err := sqliteDatabasePathFromDSN(sqliteDSN)
			if err != nil {
				return nil, err
			}
			lifecycleGroup = sharedDatabaseLifecycleGroup(activeDatabasePath)
		} else {
			lifecycleGroup = newDatabaseLifecycleGroup("")
		}
		lifecycleGroup.write.Lock()
		lifecycleGroupWriteLocked = true
		lifecycleGroup.mu.Lock()
		lifecycleGroupLocked = true
		defer func() {
			if lifecycleGroupLocked {
				if !storeRegistered && len(lifecycleGroup.members) == 0 && lifecycleGroup.processLock != nil {
					_ = lifecycleGroup.processLock.Close()
					lifecycleGroup.processLock = nil
				}
				lifecycleGroup.mu.Unlock()
				lifecycleGroupLocked = false
			}
			if lifecycleGroupWriteLocked {
				lifecycleGroup.write.Unlock()
				lifecycleGroupWriteLocked = false
			}
		}()
		if lifecycleGroup.databasePath != "" && len(lifecycleGroup.members) == 0 {
			if err := preparePKIRestoreLifecycleGroup(context.Background(), lifecycleGroup); err != nil {
				return nil, fmt.Errorf("recover protected SQLite restore: %w", err)
			}
		}
	} else {
		lifecycleGroup = newDatabaseLifecycleGroup("")
	}

	dialector, err := resolveDialector(driver, cfg)
	if err != nil {
		return nil, err
	}
	db, err := gorm.Open(dialector, &gorm.Config{TranslateError: true})
	if err != nil {
		return nil, err
	}
	lifecycle, err := installDatabaseLifecycle(db, lifecycleGroup)
	if err != nil {
		if sqlDB, dbErr := db.DB(); dbErr == nil {
			_ = sqlDB.Close()
		}
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
		db:                db,
		writeDSN:          writeDSN,
		dataRoot:          cfg.DataRoot,
		localAgentID:      cfg.LocalAgentID,
		driver:            driver,
		databaseLifecycle: lifecycle,
		storeConfig:       cfg,
	}
	localAgentPresent := strings.TrimSpace(cfg.LocalAgentID) != ""
	if cfg.LocalAgentPresent != nil {
		localAgentPresent = *cfg.LocalAgentPresent
	}
	store.localAgentPresent.Store(localAgentPresent)
	store.storeConfig.Driver = driver
	if lifecycleGroupLocked {
		lifecycleGroup.members[store] = struct{}{}
		storeRegistered = true
		lifecycleGroup.mu.Unlock()
		lifecycleGroupLocked = false
	} else {
		lifecycleGroup.mu.Lock()
		lifecycleGroup.members[store] = struct{}{}
		lifecycleGroup.mu.Unlock()
		storeRegistered = true
	}
	if !cfg.SkipBootstrapSchema {
		schemaOptions := SchemaOptionsForDriver(driver, cfg.TrafficStatsEnabled)
		schemaOptions.LocalAgentID = store.LocalAgentID()
		bootstrapErr := BootstrapSchema(context.Background(), db, schemaOptions)
		if lifecycleGroupWriteLocked {
			lifecycleGroup.write.Unlock()
			lifecycleGroupWriteLocked = false
		}
		if bootstrapErr != nil {
			_ = store.Close()
			return nil, bootstrapErr
		}
		if err := store.BootstrapRevisionLedger(context.Background()); err != nil {
			_ = store.Close()
			return nil, err
		}
		if _, err := store.ExternalizeRuntimeArtifacts(context.Background()); err != nil {
			_ = store.Close()
			return nil, err
		}
		compact, err := store.runtimeArtifactCompactionPending(context.Background())
		if err != nil {
			_ = store.Close()
			return nil, err
		}
		if compact {
			if err := store.compactExternalizedSQLite(context.Background()); err != nil {
				_ = store.Close()
				return nil, err
			}
			if err := store.markRuntimeArtifactCompactionComplete(context.Background()); err != nil {
				_ = store.Close()
				return nil, err
			}
		}
	}
	if lifecycleGroupWriteLocked {
		lifecycleGroup.write.Unlock()
		lifecycleGroupWriteLocked = false
	}
	return store, nil
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
	if s == nil || s.db == nil || s.transactionScoped {
		return nil
	}
	if s.databaseLifecycle == nil || s.databaseLifecycle.group == nil {
		return gorm.ErrInvalidDB
	}
	group := s.databaseLifecycle.group
	group.write.Lock()
	defer group.write.Unlock()
	s.sqliteWrite.Lock()
	defer s.sqliteWrite.Unlock()
	group.mu.Lock()
	defer group.mu.Unlock()
	result := s.closeDatabaseHandlesLocked()
	delete(group.members, s)
	if len(group.members) == 0 && group.processLock != nil {
		result = errors.Join(result, group.processLock.Close())
		group.processLock = nil
	}
	return result
}
