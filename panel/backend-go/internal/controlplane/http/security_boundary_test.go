package http

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/authz"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestNestedRuleMutationUsesParentAgentAuthorization(t *testing.T) {
	manager, token := newScopedAccessSession(t)
	state := &fakeRuleServiceState{}
	deps := trafficTestDependencies(fakeTrafficService{})
	deps.AccessManager = manager
	deps.RuleService = fakeRuleService{state: state}
	router, err := NewRouter(deps)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPut, "/panel-api/agents/edge-b/rules/7", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("hidden child mutation = %d body=%s, want 403", resp.Code, resp.Body.String())
	}
	if len(state.updateIDs) != 0 {
		t.Fatalf("rule update calls = %v, want none", state.updateIDs)
	}
}

func TestMonitorStreamFiltersSnapshotAndUpdatesByAgentGroup(t *testing.T) {
	manager, token := newScopedAccessSession(t)
	updates := make(chan service.AgentMonitorUpdate, 2)
	deps := trafficTestDependencies(fakeTrafficService{})
	deps.AccessManager = manager
	deps.AgentService = fakeAgentService{
		monitorSnapshot: service.AgentMonitorSnapshot{GeneratedAt: "2026-08-07T00:00:00Z", Agents: []service.AgentMonitorAgent{{ID: "edge-a"}, {ID: "edge-b"}}},
		monitorUpdates:  updates,
	}
	deps.MonitorStreamRefreshInterval = time.Hour
	deps.MonitorStreamMaxAge = time.Second
	router, err := NewRouter(deps)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/panel-api/agents/monitor-stream", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	flushed := make(chan struct{}, 3)
	resp := &fullDuplexRecorder{ResponseRecorder: httptest.NewRecorder(), flushed: flushed}
	done := make(chan struct{})
	go func() {
		router.ServeHTTP(resp, req)
		close(done)
	}()
	select {
	case <-flushed:
	case <-time.After(time.Second):
		t.Fatal("snapshot not flushed")
	}
	updates <- service.AgentMonitorUpdate{Agent: service.AgentMonitorAgent{ID: "edge-b"}}
	select {
	case <-flushed:
		t.Fatal("hidden update was flushed")
	case <-time.After(30 * time.Millisecond):
	}
	updates <- service.AgentMonitorUpdate{Agent: service.AgentMonitorAgent{ID: "edge-a"}}
	select {
	case <-flushed:
	case <-time.After(time.Second):
		t.Fatal("visible update not flushed")
	}
	cancel()
	<-done
	body := resp.Body.String()
	if strings.Contains(body, "edge-b") || !strings.Contains(body, "edge-a") {
		t.Fatalf("filtered monitor body = %q", body)
	}
}

func TestVerifyMapsBootstrapAuditFailureToServiceUnavailable(t *testing.T) {
	store, err := storage.NewStore(storage.StoreConfig{Driver: "sqlite", DataRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	manager := authz.NewManager(&failingAuditStore{GormStore: store}, authz.Options{})
	if err := manager.EnsureDefaults(t.Context()); err != nil {
		t.Fatal(err)
	}
	deps := trafficTestDependencies(fakeTrafficService{})
	deps.AccessManager = manager
	router, err := NewRouter(deps)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/panel-api/auth/verify", nil)
	req.Header.Set("X-Panel-Token", deps.Config.PanelToken)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	if resp.Code != http.StatusServiceUnavailable || !strings.Contains(resp.Body.String(), "audit_unavailable") {
		t.Fatalf("verify audit failure = %d body=%s", resp.Code, resp.Body.String())
	}
}

type failingAuditStore struct {
	*storage.GormStore
}

func (*failingAuditStore) AppendAuditEvent(context.Context, storage.AuditEventRow) error {
	return errors.New("audit write failed")
}

func TestConfiguredInvalidVaultKeyFailsRouterStartup(t *testing.T) {
	t.Setenv("PANEL_VAULT_MASTER_KEY", "invalid-key")
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.LocalAgentID = "local"
	if _, err := NewRouter(Dependencies{Config: cfg}); err == nil || !strings.Contains(err.Error(), "load secret vault key") {
		t.Fatalf("NewRouter() error = %v, want invalid configured vault key failure", err)
	}
}
