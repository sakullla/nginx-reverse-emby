package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
)

type fakeSystemService struct {
	info service.SystemInfo
}

func (f fakeSystemService) Info(context.Context) service.SystemInfo {
	return f.info
}

type fakeAgentService struct {
	agents []service.AgentSummary
	rules  map[string][]service.HTTPRule
}

func (f fakeAgentService) List(context.Context) ([]service.AgentSummary, error) {
	return f.agents, nil
}

func (f fakeAgentService) Register(context.Context, service.RegisterRequest, string) (service.AgentSummary, error) {
	if len(f.agents) == 0 {
		return service.AgentSummary{}, service.ErrAgentNotFound
	}
	return f.agents[0], nil
}

func (f fakeAgentService) ListHTTPRules(_ context.Context, agentID string) ([]service.HTTPRule, error) {
	rules, ok := f.rules[agentID]
	if !ok {
		return nil, service.ErrAgentNotFound
	}
	return rules, nil
}

func TestRouterServesPanelAuthAndInfoEndpoints(t *testing.T) {
	router, err := NewRouter(Dependencies{
		Config: config.Config{
			PanelToken:    "secret",
			RegisterToken: "register-secret",
		},
		SystemService: fakeSystemService{
			info: service.SystemInfo{
				Role:              "master",
				LocalApplyRuntime: "go-agent",
				DefaultAgentID:    "local",
				LocalAgentEnabled: true,
			},
		},
		AgentService: fakeAgentService{},
	})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}

	healthReq := httptest.NewRequest(http.MethodHead, "/panel-api/health", nil)
	healthResp := httptest.NewRecorder()
	router.ServeHTTP(healthResp, healthReq)
	if healthResp.Code != http.StatusOK {
		t.Fatalf("HEAD /panel-api/health = %d", healthResp.Code)
	}

	verifyReq := httptest.NewRequest(http.MethodGet, "/panel-api/auth/verify", nil)
	verifyReq.Header.Set("X-Panel-Token", "secret")
	verifyResp := httptest.NewRecorder()
	router.ServeHTTP(verifyResp, verifyReq)
	if verifyResp.Code != http.StatusOK {
		t.Fatalf("GET /panel-api/auth/verify = %d", verifyResp.Code)
	}

	unauthorizedReq := httptest.NewRequest(http.MethodGet, "/panel-api/auth/verify", nil)
	unauthorizedResp := httptest.NewRecorder()
	router.ServeHTTP(unauthorizedResp, unauthorizedReq)
	if unauthorizedResp.Code != http.StatusUnauthorized {
		t.Fatalf("GET /panel-api/auth/verify without token = %d", unauthorizedResp.Code)
	}

	infoReq := httptest.NewRequest(http.MethodGet, "/panel-api/info", nil)
	infoReq.Header.Set("X-Panel-Token", "secret")
	infoResp := httptest.NewRecorder()
	router.ServeHTTP(infoResp, infoReq)
	if infoResp.Code != http.StatusOK {
		t.Fatalf("GET /panel-api/info = %d", infoResp.Code)
	}

	var payload map[string]any
	if err := json.Unmarshal(infoResp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if payload["role"] != "master" || payload["local_apply_runtime"] != "go-agent" {
		t.Fatalf("unexpected info payload: %+v", payload)
	}
	if payload["master_register_token"] != "register-secret" {
		t.Fatalf("master_register_token = %v", payload["master_register_token"])
	}
}

func TestRouterServesAgentsAndRulesEndpoints(t *testing.T) {
	router, err := NewRouter(Dependencies{
		Config: config.Config{PanelToken: "secret"},
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
				ID:             "local",
				Name:           "Local Agent",
				Status:         "online",
				IsLocal:        true,
				HTTPRulesCount: 1,
			}},
			rules: map[string][]service.HTTPRule{
				"local": {{
					ID:               1,
					AgentID:          "local",
					FrontendURL:      "https://emby.example.com",
					BackendURL:       "http://emby:8096",
					Backends:         []service.HTTPRuleBackend{{URL: "http://emby:8096"}},
					LoadBalancing:    service.HTTPLoadBalancing{Strategy: "round_robin"},
					Enabled:          true,
					Tags:             []string{},
					ProxyRedirect:    true,
					RelayChain:       []int{},
					PassProxyHeaders: true,
					UserAgent:        "",
					CustomHeaders:    []service.HTTPCustomHeader{},
					Revision:         3,
				}},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}

	agentsReq := httptest.NewRequest(http.MethodGet, "/panel-api/agents", nil)
	agentsReq.Header.Set("X-Panel-Token", "secret")
	agentsResp := httptest.NewRecorder()
	router.ServeHTTP(agentsResp, agentsReq)
	if agentsResp.Code != http.StatusOK {
		t.Fatalf("GET /panel-api/agents = %d", agentsResp.Code)
	}

	rulesReq := httptest.NewRequest(http.MethodGet, "/panel-api/agents/local/rules", nil)
	rulesReq.Header.Set("X-Panel-Token", "secret")
	rulesResp := httptest.NewRecorder()
	router.ServeHTTP(rulesResp, rulesReq)
	if rulesResp.Code != http.StatusOK {
		t.Fatalf("GET /panel-api/agents/local/rules = %d", rulesResp.Code)
	}

	missingReq := httptest.NewRequest(http.MethodGet, "/panel-api/agents/missing/rules", nil)
	missingReq.Header.Set("X-Panel-Token", "secret")
	missingResp := httptest.NewRecorder()
	router.ServeHTTP(missingResp, missingReq)
	if missingResp.Code != http.StatusNotFound {
		t.Fatalf("GET /panel-api/agents/missing/rules = %d", missingResp.Code)
	}
}

func TestMapServiceErrorMapsAgentNotFound(t *testing.T) {
	status, payload := mapServiceError(service.ErrAgentNotFound)
	if status != http.StatusNotFound {
		t.Fatalf("status = %d", status)
	}
	if payload["message"] != "agent not found" {
		t.Fatalf("payload = %+v", payload)
	}

	status, payload = mapServiceError(errors.New("boom"))
	if status != http.StatusInternalServerError {
		t.Fatalf("status = %d", status)
	}
	if payload["message"] != "internal server error" {
		t.Fatalf("payload = %+v", payload)
	}
}
