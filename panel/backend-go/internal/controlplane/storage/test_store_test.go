//go:build !fast

package storage

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

type storageSQLiteTemplateKey struct {
	trafficStatsEnabled bool
}

type storageSQLiteTemplate struct {
	once sync.Once
	data []byte
	err  error
}

var storageSQLiteTemplates sync.Map

func storageSQLiteTemplateData(key storageSQLiteTemplateKey) ([]byte, error) {
	value, _ := storageSQLiteTemplates.LoadOrStore(key, &storageSQLiteTemplate{})
	template := value.(*storageSQLiteTemplate)
	template.once.Do(func() {
		root, err := os.MkdirTemp("", "nre-storage-sqlite-template-")
		if err != nil {
			template.err = err
			return
		}
		defer os.RemoveAll(root)

		dsn := filepath.Join(root, "panel.db") +
			"?_pragma=journal_mode(MEMORY)&_pragma=synchronous(OFF)&_pragma=busy_timeout(5000)&_pragma=cache_size(-65536)&_pragma=temp_store(MEMORY)"
		store, err := NewStore(StoreConfig{
			Driver:              "sqlite",
			DSN:                 dsn,
			DataRoot:            root,
			LocalAgentID:        "local",
			TrafficStatsEnabled: key.trafficStatsEnabled,
		})
		if err != nil {
			template.err = err
			return
		}
		if err := store.Close(); err != nil {
			template.err = err
			return
		}
		template.data, template.err = os.ReadFile(filepath.Join(root, "panel.db"))
	})
	return template.data, template.err
}

func newStorageTestSQLiteStore(t *testing.T, dataRoot, localAgentID string, trafficStatsEnabled bool) (*SQLiteStore, error) {
	t.Helper()
	if testing.Short() {
		t.Skip("SQLite-backed storage scenarios run in the full test tier")
	}
	return newStorageTestSQLiteStoreForAllTiers(t, dataRoot, localAgentID, trafficStatsEnabled)
}

func newStorageTestSQLiteStoreForAllTiers(t *testing.T, dataRoot, localAgentID string, trafficStatsEnabled bool) (*SQLiteStore, error) {
	t.Helper()
	template, err := storageSQLiteTemplateData(storageSQLiteTemplateKey{
		trafficStatsEnabled: trafficStatsEnabled,
	})
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dataRoot, 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dataRoot, "panel.db"), template, 0o600); err != nil {
		return nil, err
	}
	return openExistingStorageTestSQLiteStore(dataRoot, localAgentID, trafficStatsEnabled)
}

func openExistingStorageTestSQLiteStore(dataRoot, localAgentID string, trafficStatsEnabled bool) (*SQLiteStore, error) {
	dsn := filepath.Join(dataRoot, "panel.db") +
		"?_pragma=journal_mode(MEMORY)&_pragma=synchronous(OFF)&_pragma=busy_timeout(5000)&_pragma=cache_size(-65536)&_pragma=temp_store(MEMORY)"
	return NewStore(StoreConfig{
		Driver:              "sqlite",
		DSN:                 dsn,
		DataRoot:            dataRoot,
		LocalAgentID:        localAgentID,
		SkipBootstrapSchema: true,
		TrafficStatsEnabled: trafficStatsEnabled,
	})
}

func newStorageMigrationTestStore(t *testing.T, localAgentID string) *SQLiteStore {
	t.Helper()
	store, err := newStorageTestSQLiteStore(t, t.TempDir(), localAgentID, true)
	if err != nil {
		t.Fatalf("open cloned SQLite migration fixture: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close cloned SQLite migration fixture: %v", err)
		}
	})
	return store
}

func newTrafficTestStore(t *testing.T, trafficStatsEnabled bool) *GormStore {
	t.Helper()
	store, err := newStorageTestSQLiteStore(t, t.TempDir(), "local", trafficStatsEnabled)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("store.Close() error = %v", err)
		}
	})
	return store
}
