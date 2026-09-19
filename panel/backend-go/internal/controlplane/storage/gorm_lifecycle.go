package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"

	"gorm.io/gorm"
)

const gormLifecycleReadLockKey = "nre:database-lifecycle-read-lock"

// databaseLifecycle keeps the public *gorm.DB stable while a protected restore
// replaces its underlying SQLite pool. Query callbacks and whole transactions
// hold read leases; restore takes the exclusive lease and therefore waits for
// every admitted reader before closing any database handle.
type databaseLifecycle struct {
	group  *databaseLifecycleGroup
	pool   gorm.ConnPool
	closed bool
}

// databaseLifecycleGroup is shared by every GormStore opened on the same
// file-backed SQLite database. A protected restore therefore quiesces and
// reopens all in-process pools, including the service and embedded-agent
// stores, instead of only the store that initiated activation.
type databaseLifecycleGroup struct {
	mu           sync.RWMutex
	write        sync.Mutex
	members      map[*GormStore]struct{}
	databasePath string
	processLock  *pkiRestoreProcessLock
}

var databaseLifecycleGroups sync.Map

func newDatabaseLifecycleGroup(databasePath string) *databaseLifecycleGroup {
	return &databaseLifecycleGroup{
		members:      make(map[*GormStore]struct{}),
		databasePath: databasePath,
	}
}

func sharedDatabaseLifecycleGroup(databasePath string) *databaseLifecycleGroup {
	key := databasePath
	if runtime.GOOS == "windows" {
		key = strings.ToLower(key)
	}
	if existing, ok := databaseLifecycleGroups.Load(key); ok {
		return existing.(*databaseLifecycleGroup)
	}
	created := newDatabaseLifecycleGroup(databasePath)
	actual, _ := databaseLifecycleGroups.LoadOrStore(key, created)
	return actual.(*databaseLifecycleGroup)
}

type lifecycleConnPool struct {
	state *databaseLifecycle
}

type lifecycleTransaction struct {
	pool    gorm.ConnPool
	release func()
	once    sync.Once
}

func installDatabaseLifecycle(db *gorm.DB, group *databaseLifecycleGroup) (*databaseLifecycle, error) {
	if db == nil || db.ConnPool == nil {
		return nil, fmt.Errorf("database lifecycle requires a connection pool")
	}
	if group == nil {
		group = newDatabaseLifecycleGroup("")
	}
	state := &databaseLifecycle{group: group, pool: db.ConnPool}
	wrapper := &lifecycleConnPool{state: state}
	db.Config.ConnPool = wrapper
	db.Statement.ConnPool = wrapper

	before := func(tx *gorm.DB) {
		if tx == nil || tx.Statement == nil {
			return
		}
		if _, inTransaction := tx.Statement.ConnPool.(*lifecycleTransaction); inTransaction {
			return
		}
		state.group.mu.RLock()
		tx.Statement.Settings.Store(gormLifecycleReadLockKey, state)
	}
	after := func(tx *gorm.DB) {
		if tx == nil || tx.Statement == nil {
			return
		}
		if value, locked := tx.Statement.Settings.LoadAndDelete(gormLifecycleReadLockKey); locked {
			value.(*databaseLifecycle).group.mu.RUnlock()
		}
	}
	return state, errors.Join(
		db.Callback().Query().Before("gorm:query").Register("nre:lifecycle_before_query", before),
		db.Callback().Query().After("gorm:after_query").Register("nre:lifecycle_after_query", after),
		db.Callback().Raw().Before("gorm:raw").Register("nre:lifecycle_before_raw", before),
		db.Callback().Raw().After("gorm:raw").Register("nre:lifecycle_after_raw", after),
	)
}

func (p *lifecycleConnPool) current() (gorm.ConnPool, error) {
	if p == nil || p.state == nil || p.state.pool == nil {
		return nil, gorm.ErrInvalidDB
	}
	return p.state.pool, nil
}

func (p *lifecycleConnPool) PrepareContext(ctx context.Context, query string) (*sql.Stmt, error) {
	pool, err := p.current()
	if err != nil {
		return nil, err
	}
	return pool.PrepareContext(ctx, query)
}

func (p *lifecycleConnPool) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	pool, err := p.current()
	if err != nil {
		return nil, err
	}
	return pool.ExecContext(ctx, query, args...)
}

func (p *lifecycleConnPool) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	pool, err := p.current()
	if err != nil {
		return nil, err
	}
	return pool.QueryContext(ctx, query, args...)
}

func (p *lifecycleConnPool) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	pool, _ := p.current()
	return pool.QueryRowContext(ctx, query, args...)
}

func (p *lifecycleConnPool) BeginTx(ctx context.Context, opts *sql.TxOptions) (gorm.ConnPool, error) {
	if p == nil || p.state == nil {
		return nil, gorm.ErrInvalidDB
	}
	p.state.group.mu.RLock()
	pool, err := p.current()
	if err != nil {
		p.state.group.mu.RUnlock()
		return nil, err
	}
	var transaction gorm.ConnPool
	switch beginner := pool.(type) {
	case gorm.ConnPoolBeginner:
		transaction, err = beginner.BeginTx(ctx, opts)
	case gorm.TxBeginner:
		transaction, err = beginner.BeginTx(ctx, opts)
	default:
		err = gorm.ErrInvalidTransaction
	}
	if err != nil {
		p.state.group.mu.RUnlock()
		return nil, err
	}
	return &lifecycleTransaction{pool: transaction, release: p.state.group.mu.RUnlock}, nil
}

func (p *lifecycleConnPool) GetDBConn() (*sql.DB, error) {
	pool, err := p.current()
	if err != nil {
		return nil, err
	}
	if connector, ok := pool.(gorm.GetDBConnector); ok {
		return connector.GetDBConn()
	}
	if sqlDB, ok := pool.(*sql.DB); ok {
		return sqlDB, nil
	}
	return nil, gorm.ErrInvalidDB
}

func (tx *lifecycleTransaction) PrepareContext(ctx context.Context, query string) (*sql.Stmt, error) {
	return tx.pool.PrepareContext(ctx, query)
}

func (tx *lifecycleTransaction) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return tx.pool.ExecContext(ctx, query, args...)
}

func (tx *lifecycleTransaction) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return tx.pool.QueryContext(ctx, query, args...)
}

func (tx *lifecycleTransaction) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return tx.pool.QueryRowContext(ctx, query, args...)
}

func (tx *lifecycleTransaction) Commit() error {
	committer, ok := tx.pool.(gorm.TxCommitter)
	if !ok {
		tx.finish()
		return gorm.ErrInvalidTransaction
	}
	err := committer.Commit()
	tx.finish()
	return err
}

func (tx *lifecycleTransaction) Rollback() error {
	committer, ok := tx.pool.(gorm.TxCommitter)
	if !ok {
		tx.finish()
		return gorm.ErrInvalidTransaction
	}
	err := committer.Rollback()
	tx.finish()
	return err
}

func (tx *lifecycleTransaction) finish() {
	tx.once.Do(tx.release)
}

func closeGormConnPool(pool gorm.ConnPool) error {
	if connector, ok := pool.(gorm.GetDBConnector); ok {
		db, err := connector.GetDBConn()
		if err != nil {
			return err
		}
		return db.Close()
	}
	if db, ok := pool.(*sql.DB); ok {
		return db.Close()
	}
	return gorm.ErrInvalidDB
}
