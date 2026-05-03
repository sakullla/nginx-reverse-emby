package storage

import (
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
)

func TestStoreConfigFromConfigPassesDatabaseSettings(t *testing.T) {
	cfg := config.Default()
	cfg.DatabaseDriver = "postgres"
	cfg.DatabaseDSN = "postgres://nre:nre@postgres:5432/nre?sslmode=disable"
	cfg.DataDir = "/tmp/nre-data"
	cfg.LocalAgentID = "edge-1"
	cfg.TrafficStatsEnabled = false

	storeCfg := StoreConfigFromConfig(cfg)

	if storeCfg.Driver != "postgres" {
		t.Fatalf("Driver = %q", storeCfg.Driver)
	}
	if storeCfg.DSN != "postgres://nre:nre@postgres:5432/nre?sslmode=disable" {
		t.Fatalf("DSN = %q", storeCfg.DSN)
	}
	if storeCfg.DataRoot != "/tmp/nre-data" {
		t.Fatalf("DataRoot = %q", storeCfg.DataRoot)
	}
	if storeCfg.LocalAgentID != "edge-1" {
		t.Fatalf("LocalAgentID = %q", storeCfg.LocalAgentID)
	}
	if storeCfg.TrafficStatsEnabled {
		t.Fatal("TrafficStatsEnabled = true, want false")
	}
}

func TestNewStoreRejectsUnsupportedDriver(t *testing.T) {
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("NewStore panicked: %v", recovered)
		}
	}()

	_, err := NewStore(StoreConfig{
		Driver:       "oracle",
		DataRoot:     t.TempDir(),
		LocalAgentID: "local",
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported database driver") {
		t.Fatalf("NewStore() error = %v, want unsupported database driver error", err)
	}
}

func TestSchemaOptionsForDriverGatesSQLiteLegacyMigrations(t *testing.T) {
	testCases := []struct {
		driver string
		want   bool
	}{
		{driver: "", want: true},
		{driver: "sqlite", want: true},
		{driver: " SQLite ", want: true},
		{driver: "postgres", want: false},
		{driver: "mysql", want: false},
	}

	for _, tc := range testCases {
		t.Run(tc.driver, func(t *testing.T) {
			options := SchemaOptionsForDriver(tc.driver, true)
			if options.SQLiteLegacyMigrations != tc.want {
				t.Fatalf("SQLiteLegacyMigrations = %v, want %v", options.SQLiteLegacyMigrations, tc.want)
			}
			if !options.TrafficStatsEnabled {
				t.Fatal("TrafficStatsEnabled = false, want true")
			}
		})
	}
}
