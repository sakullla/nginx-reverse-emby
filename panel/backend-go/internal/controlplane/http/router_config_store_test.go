package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
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
	requestStart := time.Now()
	handler.ServeHTTP(resp, req)
	requestEnd := time.Now()

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
	wantStart := expectedTrafficCycleStartForTest(t, requestStart, "Asia/Shanghai", 5)
	wantEnd := expectedTrafficCycleStartForTest(t, requestEnd, "Asia/Shanghai", 5)
	if len(payload.Agents) != 1 || (payload.Agents[0].CycleStart != wantStart && payload.Agents[0].CycleStart != wantEnd) {
		t.Fatalf("agents = %+v, want Asia/Shanghai cycle start %q", payload.Agents, wantStart)
	}
}

func expectedTrafficCycleStartForTest(t *testing.T, now time.Time, timezone string, cycleStartDay int) string {
	t.Helper()
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		t.Fatalf("LoadLocation(%q) error = %v", timezone, err)
	}
	local := now.In(loc)
	candidate := time.Date(local.Year(), local.Month(), cycleStartDay, 0, 0, 0, 0, loc)
	if local.Before(candidate) {
		candidate = candidate.AddDate(0, -1, 0)
	}
	return candidate.UTC().Format(time.RFC3339)
}

func TestNewRouterInjectedCoreServicesWithoutWireGuardDoesNotOpenConfiguredStore(t *testing.T) {
	previousOpenConfiguredStore := openConfiguredStore
	t.Cleanup(func() {
		openConfiguredStore = previousOpenConfiguredStore
	})

	var opened bool
	openConfiguredStore = func(config.Config) (*storage.GormStore, error) {
		opened = true
		return nil, errors.New("configured store should not be opened")
	}

	router, err := NewRouter(Dependencies{
		Config: config.Config{PanelToken: "secret", TrafficStatsEnabled: true},
		SystemService: fakeSystemService{
			info: service.SystemInfo{
				Role:              "master",
				LocalApplyRuntime: "go-agent",
				DefaultAgentID:    "local",
				LocalAgentEnabled: true,
			},
		},
		AgentService: fakeAgentService{
			agents: []service.AgentSummary{{
				ID:      "local",
				Name:    "Local Agent",
				Mode:    "local",
				Status:  "online",
				IsLocal: true,
			}},
		},
		RuleService: fakeRuleService{
			rules: map[string][]service.HTTPRule{
				"local": {{
					ID:          1,
					AgentID:     "local",
					FrontendURL: "https://media.example.com",
					Backends:    []service.HTTPRuleBackend{{URL: "http://emby:8096"}},
				}},
			},
		},
		L4RuleService:        fakeL4RuleService{},
		VersionPolicyService: fakeVersionPolicyService{},
		RelayListenerService: fakeRelayListenerService{},
		CertificateService:   fakeCertificateService{},
		TrafficService:       fakeTrafficService{},
	})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}
	if opened {
		t.Fatal("NewRouter opened configured store")
	}

	agentsReq := httptest.NewRequest(http.MethodGet, "/panel-api/agents", nil)
	agentsReq.Header.Set("X-Panel-Token", "secret")
	agentsResp := httptest.NewRecorder()
	router.ServeHTTP(agentsResp, agentsReq)
	if agentsResp.Code != http.StatusOK {
		t.Fatalf("GET /panel-api/agents = %d, body=%s", agentsResp.Code, agentsResp.Body.String())
	}

	rulesReq := httptest.NewRequest(http.MethodGet, "/panel-api/agents/local/rules", nil)
	rulesReq.Header.Set("X-Panel-Token", "secret")
	rulesResp := httptest.NewRecorder()
	router.ServeHTTP(rulesResp, rulesReq)
	if rulesResp.Code != http.StatusOK {
		t.Fatalf("GET /panel-api/agents/local/rules = %d, body=%s", rulesResp.Code, rulesResp.Body.String())
	}
}

func TestNewRouterInjectedCoreServicesBuildsWireGuardServicesFromOwnedStore(t *testing.T) {
	cfg := config.Default()
	cfg.PanelToken = "secret"
	cfg.DataDir = t.TempDir()
	cfg.LocalAgentID = "local"
	cfg.EnableLocalAgent = true

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

	router, err := NewRouter(Dependencies{
		Config:               cfg,
		SystemService:        fakeSystemService{},
		AgentService:         fakeAgentService{},
		RuleService:          fakeRuleService{},
		L4RuleService:        fakeL4RuleService{},
		VersionPolicyService: fakeVersionPolicyService{},
		RelayListenerService: fakeRelayListenerService{},
		CertificateService:   fakeCertificateService{},
	})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}
	t.Cleanup(func() {
		if closeable, ok := router.(interface{ Close() error }); ok {
			_ = closeable.Close()
		}
	})

	createReq := httptest.NewRequest(http.MethodPost, "/panel-api/agents/local/wireguard-profiles", strings.NewReader(validWireGuardHTTPClientProfilePayload(51820)))
	createReq.Header.Set("X-Panel-Token", "secret")
	createReq.Header.Set("Content-Type", "application/json")
	createResp := httptest.NewRecorder()
	router.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("POST /panel-api/agents/local/wireguard-profiles = %d, body=%s", createResp.Code, createResp.Body.String())
	}
	profile := decodeWireGuardHTTPProfileResponse(t, createResp.Body.Bytes(), "profile")

	listReq := httptest.NewRequest(http.MethodGet, "/panel-api/agents/local/wireguard-profiles", nil)
	listReq.Header.Set("X-Panel-Token", "secret")
	listResp := httptest.NewRecorder()
	router.ServeHTTP(listResp, listReq)
	if listResp.Code != http.StatusOK {
		t.Fatalf("GET /panel-api/agents/local/wireguard-profiles = %d, body=%s", listResp.Code, listResp.Body.String())
	}
	profiles := decodeWireGuardHTTPProfilesResponse(t, listResp.Body.Bytes())
	if len(profiles) != 1 || profiles[0].ID != profile.ID {
		t.Fatalf("profiles = %+v, want created profile id %d", profiles, profile.ID)
	}
}
