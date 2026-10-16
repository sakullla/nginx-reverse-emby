//go:build !integration

package service

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

var serviceSQLiteFixture struct {
	once sync.Once
	data []byte
	err  error
}

// Service scenarios need independent durable stores, but do not test schema
// migration. Bootstrap once, then copy the closed database for each fixture.
// Keep the normal SQLite settings so persistence and lifecycle assertions still
// exercise the production store. Migration coverage remains in storage tests.
func newServiceSQLiteStore(t *testing.T, root string) (*storage.GormStore, error) {
	t.Helper()
	serviceSQLiteFixture.once.Do(func() {
		templateRoot, err := os.MkdirTemp("", "nre-service-sqlite-template-")
		if err != nil {
			serviceSQLiteFixture.err = err
			return
		}
		defer os.RemoveAll(templateRoot)
		store, err := storage.NewSQLiteStore(templateRoot, "local")
		if err != nil {
			serviceSQLiteFixture.err = err
			return
		}
		if err := store.Close(); err != nil {
			serviceSQLiteFixture.err = err
			return
		}
		serviceSQLiteFixture.data, serviceSQLiteFixture.err = os.ReadFile(filepath.Join(templateRoot, "panel.db"))
	})
	if serviceSQLiteFixture.err != nil {
		return nil, serviceSQLiteFixture.err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(root, "panel.db"), serviceSQLiteFixture.data, 0o600); err != nil {
		return nil, err
	}
	return storage.NewStore(storage.StoreConfig{
		Driver: "sqlite", DataRoot: root, LocalAgentID: "local",
		TrafficStatsEnabled: true, SkipBootstrapSchema: true,
	})
}
