package http

import (
	"context"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestDependenciesWithDefaultsUsesConfiguredStore(t *testing.T) {
	cfg := config.Default()
	cfg.DatabaseDriver = "postgres"
	cfg.DatabaseDSN = "postgres://nre:nre@postgres:5432/nre?sslmode=disable"
	cfg.DataDir = "/tmp/nre-data"
	cfg.LocalAgentID = "edge-1"
	cfg.TrafficStatsEnabled = false

	previousOpenConfiguredStore := openConfiguredStore
	t.Cleanup(func() {
		openConfiguredStore = previousOpenConfiguredStore
	})

	var gotStoreCfg storage.StoreConfig
	openConfiguredStore = func(gotCfg config.Config) (*storage.GormStore, error) {
		gotStoreCfg = storage.StoreConfigFromConfig(gotCfg)
		return &storage.GormStore{}, nil
	}

	if _, err := (Dependencies{Config: cfg}).withDefaults(); err != nil {
		t.Fatalf("withDefaults() error = %v", err)
	}
	if gotStoreCfg.Driver != "postgres" {
		t.Fatalf("Driver = %q", gotStoreCfg.Driver)
	}
	if gotStoreCfg.DSN != "postgres://nre:nre@postgres:5432/nre?sslmode=disable" {
		t.Fatalf("DSN = %q", gotStoreCfg.DSN)
	}
	if gotStoreCfg.DataRoot != "/tmp/nre-data" {
		t.Fatalf("DataRoot = %q", gotStoreCfg.DataRoot)
	}
	if gotStoreCfg.LocalAgentID != "edge-1" {
		t.Fatalf("LocalAgentID = %q", gotStoreCfg.LocalAgentID)
	}
	if gotStoreCfg.TrafficStatsEnabled {
		t.Fatal("TrafficStatsEnabled = true, want false")
	}
}

func TestNewRouterReturnsCloseableHandlerForOwnedConfiguredStore(t *testing.T) {
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.LocalAgentID = "local"

	previousOpenConfiguredStore := openConfiguredStore
	t.Cleanup(func() {
		openConfiguredStore = previousOpenConfiguredStore
	})

	store, err := storage.NewSQLiteStore(cfg.DataDir, cfg.LocalAgentID)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	openConfiguredStore = func(config.Config) (*storage.GormStore, error) {
		return store, nil
	}

	handler, err := NewRouter(Dependencies{Config: cfg})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}
	closeable, ok := handler.(interface{ Close() error })
	if !ok {
		t.Fatalf("handler type = %T, want Close method", handler)
	}
	if err := closeable.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	_, err = store.ListAgents(context.Background())
	if err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("ListAgents() error = %v, want closed database error", err)
	}
}
