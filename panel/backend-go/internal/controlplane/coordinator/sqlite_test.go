//go:build !integration

package coordinator

import (
	"os"
	"path/filepath"
	"sync"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

var coordinatorSQLiteTemplateOnce sync.Once
var coordinatorSQLiteTemplate []byte
var coordinatorSQLiteTemplateErr error

func coordinatorSQLiteTemplateData() ([]byte, error) {
	coordinatorSQLiteTemplateOnce.Do(func() {
		root, err := os.MkdirTemp("", "nre-coordinator-sqlite-template-")
		if err != nil {
			coordinatorSQLiteTemplateErr = err
			return
		}
		defer os.RemoveAll(root)

		dsn := coordinatorSQLiteDSN(filepath.Join(root, "coordinator.db"))
		store, err := storage.NewStore(storage.StoreConfig{
			Driver:              "sqlite",
			DSN:                 dsn,
			DataRoot:            root,
			LocalAgentID:        "local",
			TrafficStatsEnabled: true,
		})
		if err != nil {
			coordinatorSQLiteTemplateErr = err
			return
		}
		if err := store.Close(); err != nil {
			coordinatorSQLiteTemplateErr = err
			return
		}
		coordinatorSQLiteTemplate, coordinatorSQLiteTemplateErr = os.ReadFile(filepath.Join(root, "coordinator.db"))
	})
	return coordinatorSQLiteTemplate, coordinatorSQLiteTemplateErr
}

func seedCoordinatorSQLiteFixture(dbPath string) error {
	template, err := coordinatorSQLiteTemplateData()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dbPath, template, 0o600)
}

func ensureCoordinatorSQLiteFixture(dbPath string) error {
	if _, err := os.Stat(dbPath); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return seedCoordinatorSQLiteFixture(dbPath)
}

func coordinatorSQLiteDSN(dbPath string) string {
	return dbPath +
		"?_pragma=journal_mode(MEMORY)&_pragma=synchronous(OFF)&_pragma=busy_timeout(5000)&_pragma=cache_size(-65536)&_pragma=temp_store(MEMORY)"
}
