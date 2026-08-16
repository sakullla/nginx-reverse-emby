//go:build !integration

package http

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/authz"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
)

func TestTokenMatchesRequiresExactSecret(t *testing.T) {
	t.Parallel()
	if tokenMatches("secret", "secret-extra") || !tokenMatches("secret", "secret") {
		t.Fatal("token matching is not exact")
	}
}

func TestPanelAuthInfoRulesAndMonitorUseExactResourceScope(t *testing.T) {
	t.Parallel()
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
		},
		RuleService: fakeOwnerRuleService{rules: []service.HTTPRule{
			{ID: 1, AgentID: "visible-edge", FrontendURL: "https://visible.example.com"},
			{ID: 2, AgentID: "hidden-edge", FrontendURL: "https://hidden.example.com"},
		}},
		AccessManager: nil,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/panel-api/health", deps.handleHealth)
	mux.Handle("/panel-api/info", deps.requirePanelToken(http.HandlerFunc(deps.handleInfo)))
	mux.Handle("/panel-api/agents/{agentID}/rules", deps.requirePanelToken(http.HandlerFunc(deps.handleAgentRules)))
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
	infoReq.Header.Set("X-Panel-Token", "secret")
	info := httptest.NewRecorder()
	mux.ServeHTTP(info, infoReq)
	if info.Code != http.StatusOK {
		t.Fatalf("info status = %d body=%s", info.Code, info.Body.String())
	}
	var infoBody map[string]any
	if err := json.Unmarshal(info.Body.Bytes(), &infoBody); err != nil {
		t.Fatal(err)
	}
	if infoBody["data_dir"] != "C:/srv/nre/data" || infoBody["master_register_token"] != "register-secret" {
		t.Fatalf("bootstrap info omitted privileged fields: %v", infoBody)
	}

	kind, id, ok := deps.requestResource(http.MethodGet, "/panel-api/agents/visible-edge/rules/1")
	if !ok || kind != "http_rule" || id != "visible-edge:1" {
		t.Fatalf("nested rule resource = %s %s ok=%v", kind, id, ok)
	}

	rulesReq := httptest.NewRequest(http.MethodGet, "/panel-api/agents/visible-edge/rules", nil)
	rulesReq.Header.Set("X-Panel-Token", "secret")
	rules := httptest.NewRecorder()
	mux.ServeHTTP(rules, rulesReq)
	if rules.Code != http.StatusOK || !strings.Contains(rules.Body.String(), "visible.example.com") {
		t.Fatalf("rules status=%d body=%s", rules.Code, rules.Body.String())
	}

	monitorReq := httptest.NewRequest(http.MethodHead, "/panel-api/agents/monitor-stream", nil)
	monitorReq.Header.Set("X-Panel-Token", "secret")
	monitor := httptest.NewRecorder()
	mux.ServeHTTP(monitor, monitorReq)
	if monitor.Code != http.StatusOK {
		t.Fatalf("monitor HEAD status = %d", monitor.Code)
	}

	snapshot := service.AgentMonitorSnapshot{Agents: []service.AgentMonitorAgent{
		{ID: "visible-edge"}, {ID: "hidden-edge"},
	}}
	filtered, err := deps.filterMonitorSnapshot(context.WithValue(t.Context(), actorContextKey{}, authz.BootstrapActor()), snapshot)
	if err != nil || len(filtered.Agents) != 2 {
		t.Fatalf("bootstrap monitor filter = %+v err=%v", filtered, err)
	}
}

type fakeSystemService struct {
	info service.SystemInfo
}

func (f fakeSystemService) Info(context.Context) service.SystemInfo { return f.info }

type fakeOwnerAgentService struct {
	agents   []service.AgentSummary
	snapshot service.AgentMonitorSnapshot
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
	ch := make(chan service.AgentMonitorUpdate)
	return ch, func() {}
}

type fakeOwnerRuleService struct {
	rules []service.HTTPRule
}

func (f fakeOwnerRuleService) List(context.Context, string) ([]service.HTTPRule, error) {
	return f.rules, nil
}
func (f fakeOwnerRuleService) ListPage(context.Context, service.ListQuery) ([]service.HTTPRule, service.PageMeta, error) {
	return nil, service.PageMeta{}, nil
}
func (f fakeOwnerRuleService) Get(context.Context, string, int) (service.HTTPRule, error) {
	return service.HTTPRule{}, io.EOF
}
func (f fakeOwnerRuleService) Create(context.Context, string, service.HTTPRuleInput) (service.HTTPRule, error) {
	return service.HTTPRule{}, io.EOF
}
func (f fakeOwnerRuleService) Update(context.Context, string, int, service.HTTPRuleInput) (service.HTTPRule, error) {
	return service.HTTPRule{}, io.EOF
}
func (f fakeOwnerRuleService) Delete(context.Context, string, int) (service.HTTPRule, error) {
	return service.HTTPRule{}, io.EOF
}
