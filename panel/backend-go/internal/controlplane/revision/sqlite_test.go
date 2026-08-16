//go:build !integration

package revision

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

var revisionSQLiteTemplateOnce sync.Once
var revisionSQLiteTemplate []byte
var revisionSQLiteTemplateErr error

func revisionSQLiteTemplateData() ([]byte, error) {
	revisionSQLiteTemplateOnce.Do(func() {
		root, err := os.MkdirTemp("", "nre-revision-sqlite-template-")
		if err != nil {
			revisionSQLiteTemplateErr = err
			return
		}
		defer os.RemoveAll(root)

		dsn := revisionSQLiteDSN(filepath.Join(root, "panel.db"))
		store, err := storage.NewStore(storage.StoreConfig{
			Driver:              "sqlite",
			DSN:                 dsn,
			DataRoot:            root,
			LocalAgentID:        "local",
			TrafficStatsEnabled: true,
		})
		if err != nil {
			revisionSQLiteTemplateErr = err
			return
		}
		if err := store.Close(); err != nil {
			revisionSQLiteTemplateErr = err
			return
		}
		revisionSQLiteTemplate, revisionSQLiteTemplateErr = os.ReadFile(filepath.Join(root, "panel.db"))
	})
	return revisionSQLiteTemplate, revisionSQLiteTemplateErr
}

func newRevisionSQLiteStore(t *testing.T, dataRoot string) (*storage.GormStore, error) {
	t.Helper()
	template, err := revisionSQLiteTemplateData()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dataRoot, 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dataRoot, "panel.db"), template, 0o600); err != nil {
		return nil, err
	}
	return storage.NewStore(storage.StoreConfig{
		Driver:              "sqlite",
		DSN:                 revisionSQLiteDSN(filepath.Join(dataRoot, "panel.db")),
		DataRoot:            dataRoot,
		LocalAgentID:        "local",
		SkipBootstrapSchema: true,
		TrafficStatsEnabled: true,
	})
}

func revisionSQLiteDSN(dbPath string) string {
	return dbPath +
		"?_pragma=journal_mode(MEMORY)&_pragma=synchronous(OFF)&_pragma=busy_timeout(5000)&_pragma=cache_size(-65536)&_pragma=temp_store(MEMORY)"
}
