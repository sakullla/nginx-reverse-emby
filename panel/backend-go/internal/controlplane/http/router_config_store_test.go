package http

import (
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
