//go:build integration

package main

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

var mainTestSQLiteTemplateOnce sync.Once
var mainTestSQLiteTemplate []byte
var mainTestSQLiteTemplateErr error

func mainTestSQLiteTemplateData() ([]byte, error) {
	mainTestSQLiteTemplateOnce.Do(func() {
		root, err := os.MkdirTemp("", "nre-main-sqlite-template-")
		if err != nil {
			mainTestSQLiteTemplateErr = err
			return
		}
		defer os.RemoveAll(root)

		dsn := filepath.Join(root, "panel.db") +
			"?_pragma=journal_mode(MEMORY)&_pragma=synchronous(OFF)&_pragma=busy_timeout(5000)&_pragma=cache_size(-65536)&_pragma=temp_store(MEMORY)"
		store, err := storage.NewStore(storage.StoreConfig{
			Driver:              "sqlite",
			DSN:                 dsn,
			DataRoot:            root,
			LocalAgentID:        "local",
			TrafficStatsEnabled: true,
		})
		if err != nil {
			mainTestSQLiteTemplateErr = err
			return
		}
		if err := store.Close(); err != nil {
			mainTestSQLiteTemplateErr = err
			return
		}
		mainTestSQLiteTemplate, mainTestSQLiteTemplateErr = os.ReadFile(filepath.Join(root, "panel.db"))
	})
	return mainTestSQLiteTemplate, mainTestSQLiteTemplateErr
}

func newMainTestSQLiteStore(t *testing.T, dataRoot, localAgentID string) (*storage.GormStore, error) {
	t.Helper()
	template, err := mainTestSQLiteTemplateData()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dataRoot, 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dataRoot, "panel.db"), template, 0o600); err != nil {
		return nil, err
	}
	return openExistingMainTestSQLiteStore(dataRoot, localAgentID)
}

func openExistingMainTestSQLiteStore(dataRoot, localAgentID string) (*storage.GormStore, error) {
	dsn := filepath.Join(dataRoot, "panel.db") +
		"?_pragma=journal_mode(MEMORY)&_pragma=synchronous(OFF)&_pragma=busy_timeout(5000)&_pragma=cache_size(-65536)&_pragma=temp_store(MEMORY)"
	return storage.NewStore(storage.StoreConfig{
		Driver:              "sqlite",
		DSN:                 dsn,
		DataRoot:            dataRoot,
		LocalAgentID:        localAgentID,
		SkipBootstrapSchema: true,
		TrafficStatsEnabled: true,
	})
}
