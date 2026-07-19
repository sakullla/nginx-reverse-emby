package service

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type serviceSQLiteTemplate struct {
	once sync.Once
	data []byte
	err  error
}

var serviceSQLiteTemplates sync.Map

func serviceSQLiteTemplateData(localAgentID string) ([]byte, error) {
	value, _ := serviceSQLiteTemplates.LoadOrStore(localAgentID, &serviceSQLiteTemplate{})
	template := value.(*serviceSQLiteTemplate)
	template.once.Do(func() {
		root, err := os.MkdirTemp("", "nre-service-sqlite-template-")
		if err != nil {
			template.err = err
			return
		}
		defer os.RemoveAll(root)

		store, err := storage.NewSQLiteStore(root, localAgentID)
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

func newServiceTestSQLiteStore(t *testing.T, dataRoot, localAgentID string) (*storage.SQLiteStore, error) {
	t.Helper()
	if testing.Short() {
		t.Skip("SQLite-backed service scenarios run in the full test tier")
	}

	template, err := serviceSQLiteTemplateData(localAgentID)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dataRoot, 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dataRoot, "panel.db"), template, 0o600); err != nil {
		return nil, err
	}
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
