//go:build !integration

package http

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/authz"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestTokenMatchesRequiresExactSecret(t *testing.T) {
	t.Parallel()
	if tokenMatches("secret", "secret-extra") || !tokenMatches("secret", "secret") {
		t.Fatal("token matching is not exact")
	}
}

func TestPanelAuthInfoRulesAndMonitorUseExactResourceScope(t *testing.T) {
	t.Parallel()
	access := newScopedAccessManager(t)
	created := &fakeOwnerRuleService{rules: []service.HTTPRule{
		{ID: 1, AgentID: "visible-edge", FrontendURL: "https://visible.example.com"},
	}}
	deps := Dependencies{
		Config: config.Config{
			PanelToken:    "secret",
			RegisterToken: "register-secret",
			LocalAgentID:  "visible-edge",
		},
		SystemService: fakeSystemService{info: service.SystemInfo{
			Role: "master", DataDir: "C:/srv/nre/data", DefaultAgentID: "visible-edge",
		}},
		AgentService: fakeOwnerAgentService{
			agents: []service.AgentSummary{
				{ID: "visible-edge", Name: "visible", Status: "online"},
				{ID: "hidden-edge", Name: "hidden", Status: "online"},
			},
			snapshot: service.AgentMonitorSnapshot{Agents: []service.AgentMonitorAgent{
				{ID: "visible-edge", Name: "visible"},
				{ID: "hidden-edge", Name: "hidden"},
			}},
			updates: make(chan service.AgentMonitorUpdate),
		},
		RuleService:   created,
		AccessManager: access.Manager,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/panel-api/health", deps.handleHealth)
	mux.Handle("/panel-api/info", deps.requirePanelToken(http.HandlerFunc(deps.handleInfo)))
	mux.Handle("/panel-api/agents/{agentID}/rules", deps.requirePanelToken(http.HandlerFunc(deps.handleAgentRules)))
	mux.Handle("/panel-api/agents/{agentID}/rules/{id}", deps.requirePanelToken(http.HandlerFunc(deps.handleAgentRule)))
	mux.Handle("/panel-api/agents/monitor-stream", deps.requirePanelToken(http.HandlerFunc(deps.handleAgentMonitorStream)))

	health := httptest.NewRecorder()
	mux.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/panel-api/health", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("health status = %d", health.Code)
	}

	unauth := httptest.NewRecorder()
	mux.ServeHTTP(unauth, httptest.NewRequest(http.MethodGet, "/panel-api/info", nil))
	if unauth.Code != http.StatusUnauthorized {
		t.Fatalf("info without token status = %d", unauth.Code)
	}

	infoReq := httptest.NewRequest(http.MethodGet, "/panel-api/info", nil)
	infoReq.Header.Set("X-Panel-Session", access.session)
	info := httptest.NewRecorder()
	mux.ServeHTTP(info, infoReq)
	if info.Code != http.StatusOK {
		t.Fatalf("info status = %d body=%s", info.Code, info.Body.String())
	}
	var infoBody map[string]any
	if err := json.Unmarshal(info.Body.Bytes(), &infoBody); err != nil {
		t.Fatal(err)
	}
	if _, hasDir := infoBody["data_dir"]; hasDir {
		t.Fatalf("scoped actor received data_dir: %v", infoBody)
	}
	if _, hasToken := infoBody["master_register_token"]; hasToken {
		t.Fatalf("scoped actor received register token: %v", infoBody)
	}

	kind, id, ok := deps.requestResource(http.MethodPut, "/panel-api/agents/visible-edge/rules/1")
	if !ok || kind != "http_rule" || id != "visible-edge:1" {
		t.Fatalf("nested rule resource = %s %s ok=%v", kind, id, ok)
	}

	allowBody := `{"frontend_url":"https://visible.example.com"}`
	allowReq := httptest.NewRequest(http.MethodPut, "/panel-api/agents/visible-edge/rules/1", strings.NewReader(allowBody))
	allowReq.Header.Set("X-Panel-Session", access.session)
	allowReq.Header.Set("Content-Type", "application/json")
	allow := httptest.NewRecorder()
	mux.ServeHTTP(allow, allowReq)
	if allow.Code != http.StatusOK {
		t.Fatalf("visible nested mutation status=%d body=%s", allow.Code, allow.Body.String())
	}

	denyReq := httptest.NewRequest(http.MethodPut, "/panel-api/agents/hidden-edge/rules/2", bytes.NewBufferString(allowBody))
	denyReq.Header.Set("X-Panel-Session", access.session)
	denyReq.Header.Set("Content-Type", "application/json")
	deny := httptest.NewRecorder()
	mux.ServeHTTP(deny, denyReq)
	if deny.Code != http.StatusForbidden {
		t.Fatalf("hidden nested mutation status=%d body=%s", deny.Code, deny.Body.String())
	}

	monitorReq := httptest.NewRequest(http.MethodGet, "/panel-api/agents/monitor-stream", nil)
	monitorReq.Header.Set("X-Panel-Session", access.session)
	ctx, cancel := context.WithCancel(monitorReq.Context())
	defer cancel()
	monitorReq = monitorReq.WithContext(ctx)
	monitor := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	done := make(chan struct{})
	go func() {
		defer close(done)
		mux.ServeHTTP(monitor, monitorReq)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for monitor.Body.Len() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	body := monitor.Body.String()
	if !strings.Contains(body, `"id":"visible-edge"`) || strings.Contains(body, `"id":"hidden-edge"`) {
		t.Fatalf("monitor snapshot leaked unauthorized agents: %s", body)
	}
}

type flushRecorder struct{ *httptest.ResponseRecorder }

func (flushRecorder) Flush() {}

type scopedAccess struct {
	*authz.Manager
	session string
}

func newScopedAccessManager(t *testing.T) scopedAccess {
	t.Helper()
	store := newHTTPAuthzStore(t)
	manager := authz.NewManager(store, authz.Options{})
	if err := manager.EnsureDefaults(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAgent(t.Context(), storage.AgentRow{ID: "visible-edge"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAgent(t.Context(), storage.AgentRow{ID: "hidden-edge"}); err != nil {
		t.Fatal(err)
	}
	visible, err := manager.CreateResourceGroup(t.Context(), "visible-group", "")
	if err != nil {
		t.Fatal(err)
	}
	hidden, err := manager.CreateResourceGroup(t.Context(), "hidden-group", "")
	if err != nil {
		t.Fatal(err)
	}
	role, err := manager.CreateRole(t.Context(), "http-writer", "", []string{authz.PermissionResourceRead, authz.PermissionResourceWrite})
	if err != nil {
		t.Fatal(err)
	}
	user, err := manager.CreateUser(t.Context(), "scoped-http", "", "correct-horse-battery", []string{role.ID})
	if err != nil {
		t.Fatal(err)
	}
	admin := authz.BootstrapActor()
	if err := manager.GrantResourceGroup(t.Context(), admin, "user", user.ID, visible.ID); err != nil {
		t.Fatal(err)
	}
	if err := manager.BindResource(t.Context(), admin, "agent", "visible-edge", visible.ID); err != nil {
		t.Fatal(err)
	}
	if err := manager.BindResource(t.Context(), admin, "agent", "hidden-edge", hidden.ID); err != nil {
		t.Fatal(err)
	}
	login, err := manager.Login(t.Context(), user.Username, "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	return scopedAccess{Manager: manager, session: login.Token}
}

func newHTTPAuthzStore(t *testing.T) *storage.GormStore {
	t.Helper()
	root := t.TempDir()
	store, err := storage.NewStore(storage.StoreConfig{Driver: "sqlite", DataRoot: root, LocalAgentID: "local"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	clone := t.TempDir()
	if err := os.WriteFile(filepath.Join(clone, "panel.db"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	dsn := filepath.Join(clone, "panel.db") + "?_pragma=journal_mode(MEMORY)&_pragma=synchronous(OFF)&_pragma=busy_timeout(5000)&_pragma=temp_store(MEMORY)"
	opened, err := storage.NewStore(storage.StoreConfig{
		Driver: "sqlite", DSN: dsn, DataRoot: clone, LocalAgentID: "local", SkipBootstrapSchema: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = opened.Close() })
	return opened
}

type fakeSystemService struct {
	info service.SystemInfo
}

func (f fakeSystemService) Info(context.Context) service.SystemInfo { return f.info }

type fakeOwnerAgentService struct {
	agents   []service.AgentSummary
	snapshot service.AgentMonitorSnapshot
	updates  chan service.AgentMonitorUpdate
}

func (f fakeOwnerAgentService) List(context.Context) ([]service.AgentSummary, error) {
	return f.agents, nil
}
func (f fakeOwnerAgentService) Get(context.Context, string) (service.AgentSummary, error) {
	return service.AgentSummary{}, service.ErrAgentNotFound
}
func (f fakeOwnerAgentService) GetByToken(context.Context, string) (service.AgentSummary, error) {
	return service.AgentSummary{}, service.ErrAgentUnauthorized
}
func (f fakeOwnerAgentService) Register(context.Context, service.RegisterRequest, string) (service.AgentSummary, error) {
	return service.AgentSummary{}, service.ErrAgentNotFound
}
func (f fakeOwnerAgentService) Update(context.Context, string, service.UpdateAgentRequest) (service.AgentSummary, error) {
	return service.AgentSummary{}, service.ErrAgentNotFound
}
func (f fakeOwnerAgentService) Delete(context.Context, string) (service.AgentSummary, error) {
	return service.AgentSummary{}, service.ErrAgentNotFound
}
func (f fakeOwnerAgentService) Stats(context.Context, string) (service.AgentStats, error) {
	return service.AgentStats{}, service.ErrAgentNotFound
}
func (f fakeOwnerAgentService) Apply(context.Context, string) (service.ApplyAgentResult, error) {
	return service.ApplyAgentResult{}, service.ErrAgentNotFound
}
func (f fakeOwnerAgentService) Heartbeat(context.Context, service.HeartbeatRequest, string) (service.HeartbeatReply, error) {
	return service.HeartbeatReply{}, service.ErrAgentUnauthorized
}
func (f fakeOwnerAgentService) MonitorSnapshot(context.Context) (service.AgentMonitorSnapshot, error) {
	return f.snapshot, nil
}
func (f fakeOwnerAgentService) SubscribeMonitorUpdates(context.Context) (<-chan service.AgentMonitorUpdate, func()) {
	ch := f.updates
	if ch == nil {
		ch = make(chan service.AgentMonitorUpdate)
	}
	return ch, func() {}
}

type fakeOwnerRuleService struct {
	rules []service.HTTPRule
}

func (f *fakeOwnerRuleService) List(_ context.Context, agentID string) ([]service.HTTPRule, error) {
	out := make([]service.HTTPRule, 0, len(f.rules))
	for _, rule := range f.rules {
		if rule.AgentID == agentID {
			out = append(out, rule)
		}
	}
	return out, nil
}
func (f *fakeOwnerRuleService) ListPage(context.Context, service.ListQuery) ([]service.HTTPRule, service.PageMeta, error) {
	return nil, service.PageMeta{}, nil
}
func (f *fakeOwnerRuleService) Get(context.Context, string, int) (service.HTTPRule, error) {
	return service.HTTPRule{}, io.EOF
}
func (f *fakeOwnerRuleService) Create(context.Context, string, service.HTTPRuleInput) (service.HTTPRule, error) {
	return service.HTTPRule{}, io.EOF
}
func (f *fakeOwnerRuleService) Update(_ context.Context, agentID string, id int, _ service.HTTPRuleInput) (service.HTTPRule, error) {
	return service.HTTPRule{ID: id, AgentID: agentID, FrontendURL: "https://visible.example.com"}, nil
}
func (f *fakeOwnerRuleService) Delete(context.Context, string, int) (service.HTTPRule, error) {
	return service.HTTPRule{}, io.EOF
}
