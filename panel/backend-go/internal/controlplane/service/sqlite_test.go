//go:build !integration

package service

import (
	"context"
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

var serviceSQLiteTemplateFixture serviceSQLiteTemplate

func authenticatedServiceMutationContext(t *testing.T) context.Context {
	t.Helper()
	ctx := storage.WithQuotaActor(t.Context(), storage.QuotaActor{UserID: "test-service-operator"})
	return WithResourceAuthorizer(ctx, func(context.Context, string, string) error { return nil })
}

func serviceSQLiteTemplateData() ([]byte, error) {
	serviceSQLiteTemplateFixture.once.Do(func() {
		root, err := os.MkdirTemp("", "nre-service-sqlite-template-")
		if err != nil {
			serviceSQLiteTemplateFixture.err = err
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
			serviceSQLiteTemplateFixture.err = err
			return
		}
		if err := store.Close(); err != nil {
			serviceSQLiteTemplateFixture.err = err
			return
		}
		serviceSQLiteTemplateFixture.data, serviceSQLiteTemplateFixture.err = os.ReadFile(filepath.Join(root, "panel.db"))
	})
	return serviceSQLiteTemplateFixture.data, serviceSQLiteTemplateFixture.err
}

func newServiceTestSQLiteStore(t *testing.T, dataRoot, localAgentID string) (*storage.SQLiteStore, error) {
	t.Helper()
	if testing.Short() {
		t.Skip("SQLite-backed service scenarios run in the full test tier")
	}
	return newServiceTestSQLiteStoreForAllTiers(t, dataRoot, localAgentID)
}

func newServiceTestSQLiteStoreForAllTiers(t *testing.T, dataRoot, localAgentID string) (*storage.SQLiteStore, error) {
	t.Helper()
	template, err := serviceSQLiteTemplateData()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dataRoot, 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dataRoot, "panel.db"), template, 0o600); err != nil {
		return nil, err
	}
	return openExistingServiceTestSQLiteStore(dataRoot, localAgentID)
}

func openExistingServiceTestSQLiteStore(dataRoot, localAgentID string) (*storage.SQLiteStore, error) {
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
