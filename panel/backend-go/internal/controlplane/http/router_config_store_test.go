package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

func TestNewRouterDefaultTrafficServiceUsesConfiguredTimezone(t *testing.T) {
	cfg := config.Default()
	cfg.PanelToken = "secret"
	cfg.DataDir = t.TempDir()
	cfg.LocalAgentID = "local"
	cfg.Timezone = "Asia/Shanghai"
	cfg.TrafficStatsEnabled = true

	previousOpenConfiguredStore := openConfiguredStore
	t.Cleanup(func() {
		openConfiguredStore = previousOpenConfiguredStore
	})

	store, err := storage.NewStore(storage.StoreConfig{
		Driver:              "sqlite",
		DataRoot:            cfg.DataDir,
		LocalAgentID:        cfg.LocalAgentID,
		TrafficStatsEnabled: true,
	})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	openConfiguredStore = func(config.Config) (*storage.GormStore, error) {
		return store, nil
	}
	ctx := context.Background()
	if err := store.SaveTrafficPolicy(ctx, storage.AgentTrafficPolicyRow{
		AgentID:              "edge-1",
		Direction:            "both",
		CycleStartDay:        5,
		HourlyRetentionDays:  30,
		DailyRetentionMonths: 3,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.IncrementTrafficBuckets(ctx, storage.TrafficDelta{
		AgentID:     "edge-1",
		ScopeType:   "agent_total",
		BucketStart: time.Date(2026, 5, 4, 17, 30, 0, 0, time.UTC),
		RXBytes:     100,
		TXBytes:     50,
	}); err != nil {
		t.Fatal(err)
	}

	handler, err := NewRouter(Dependencies{Config: cfg})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}
	t.Cleanup(func() {
		if closeable, ok := handler.(interface{ Close() error }); ok {
			_ = closeable.Close()
		}
	})

	req := httptest.NewRequest(http.MethodGet, "/panel-api/traffic-overview", nil)
	req.Header.Set("X-Panel-Token", "secret")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("GET traffic-overview = %d body=%s", resp.Code, resp.Body.String())
	}
	var payload struct {
		Agents []struct {
			CycleStart string `json:"cycle_start"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if len(payload.Agents) != 1 || payload.Agents[0].CycleStart != "2026-05-04T16:00:00Z" {
		t.Fatalf("agents = %+v, want Asia/Shanghai cycle start", payload.Agents)
	}
}
