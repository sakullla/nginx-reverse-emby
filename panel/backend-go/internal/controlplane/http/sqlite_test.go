package http

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

var httpSQLiteTemplateOnce sync.Once
var httpSQLiteTemplate []byte
var httpSQLiteTemplateErr error

func httpSQLiteTemplateData() ([]byte, error) {
	httpSQLiteTemplateOnce.Do(func() {
		root, err := os.MkdirTemp("", "nre-http-sqlite-template-")
		if err != nil {
			httpSQLiteTemplateErr = err
			return
		}
		defer os.RemoveAll(root)

		store, err := storage.NewStore(storage.StoreConfig{
			Driver:              "sqlite",
			DSN:                 httpSQLiteDSN(filepath.Join(root, "panel.db")),
			DataRoot:            root,
			LocalAgentID:        "local",
			TrafficStatsEnabled: true,
		})
		if err != nil {
			httpSQLiteTemplateErr = err
			return
		}
		if err := store.Close(); err != nil {
			httpSQLiteTemplateErr = err
			return
		}
		httpSQLiteTemplate, httpSQLiteTemplateErr = os.ReadFile(filepath.Join(root, "panel.db"))
	})
	return httpSQLiteTemplate, httpSQLiteTemplateErr
}

func newHTTPTestSQLiteStore(t *testing.T, dataRoot, localAgentID string) (*storage.GormStore, error) {
	t.Helper()
	template, err := httpSQLiteTemplateData()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dataRoot, 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dataRoot, "panel.db"), template, 0o600); err != nil {
		return nil, err
	}
	return openExistingHTTPTestSQLiteStore(dataRoot, localAgentID)
}

func openExistingHTTPTestSQLiteStore(dataRoot, localAgentID string) (*storage.GormStore, error) {
	return storage.NewStore(storage.StoreConfig{
		Driver:              "sqlite",
		DSN:                 httpSQLiteDSN(filepath.Join(dataRoot, "panel.db")),
		DataRoot:            dataRoot,
		LocalAgentID:        localAgentID,
		SkipBootstrapSchema: true,
		TrafficStatsEnabled: true,
	})
}

func httpSQLiteDSN(dbPath string) string {
	return dbPath +
		"?_pragma=journal_mode(MEMORY)&_pragma=synchronous(OFF)&_pragma=busy_timeout(5000)&_pragma=cache_size(-65536)&_pragma=temp_store(MEMORY)"
}
