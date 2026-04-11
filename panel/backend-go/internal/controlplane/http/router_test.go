package http

import (
	"bytes"
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
	agents         []service.AgentSummary
	agentsByID     map[string]service.AgentSummary
	heartbeatReply service.HeartbeatReply
	heartbeatErr   error
	updateAgent    service.AgentSummary
	deleteAgent    service.AgentSummary
	applyResult    service.ApplyAgentResult
	statsByID      map[string]service.AgentStats
	getErr         error
	updateErr      error
	deleteErr      error
	statsErr       error
	applyErr       error
	state          *fakeAgentServiceState
}

type fakeAgentServiceState struct {
	updateAgentID  string
	updateInput    service.UpdateAgentRequest
	deleteAgentID  string
	statsAgentID   string
	applyAgentID   string
	heartbeat      service.HeartbeatRequest
	heartbeatToken string
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

func (f fakeAgentService) Heartbeat(_ context.Context, req service.HeartbeatRequest, token string) (service.HeartbeatReply, error) {
	if f.state != nil {
		f.state.heartbeat = req
		f.state.heartbeatToken = token
	}
	if f.heartbeatErr != nil {
		return service.HeartbeatReply{}, f.heartbeatErr
	}
	return f.heartbeatReply, nil
}

func (f fakeAgentService) Get(_ context.Context, agentID string) (service.AgentSummary, error) {
	if f.getErr != nil {
		return service.AgentSummary{}, f.getErr
	}
	if agent, ok := f.agentsByID[agentID]; ok {
		return agent, nil
	}
	return service.AgentSummary{}, service.ErrAgentNotFound
}

func (f fakeAgentService) Update(_ context.Context, agentID string, input service.UpdateAgentRequest) (service.AgentSummary, error) {
	if f.state != nil {
		f.state.updateAgentID = agentID
		f.state.updateInput = input
	}
	if f.updateErr != nil {
		return service.AgentSummary{}, f.updateErr
	}
	return f.updateAgent, nil
}

func (f fakeAgentService) Delete(_ context.Context, agentID string) (service.AgentSummary, error) {
	if f.state != nil {
		f.state.deleteAgentID = agentID
	}
	if f.deleteErr != nil {
		return service.AgentSummary{}, f.deleteErr
	}
	return f.deleteAgent, nil
}

func (f fakeAgentService) Stats(_ context.Context, agentID string) (service.AgentStats, error) {
	if f.state != nil {
		f.state.statsAgentID = agentID
	}
	if f.statsErr != nil {
		return service.AgentStats{}, f.statsErr
	}
	if stats, ok := f.statsByID[agentID]; ok {
		return stats, nil
	}
	return service.AgentStats{}, service.ErrAgentNotFound
}

func (f fakeAgentService) Apply(_ context.Context, agentID string) (service.ApplyAgentResult, error) {
	if f.state != nil {
		f.state.applyAgentID = agentID
	}
	if f.applyErr != nil {
		return service.ApplyAgentResult{}, f.applyErr
	}
	return f.applyResult, nil
}

type fakeL4RuleService struct {
	rules       map[string][]service.L4Rule
	createdRule service.L4Rule
	updatedRule service.L4Rule
	deletedRule service.L4Rule
}

func (f fakeL4RuleService) List(_ context.Context, agentID string) ([]service.L4Rule, error) {
	rules, ok := f.rules[agentID]
	if !ok {
		return nil, service.ErrAgentNotFound
	}
	return rules, nil
}

func (f fakeL4RuleService) Create(context.Context, string, service.L4RuleInput) (service.L4Rule, error) {
	return f.createdRule, nil
}

func (f fakeL4RuleService) Update(context.Context, string, int, service.L4RuleInput) (service.L4Rule, error) {
	return f.updatedRule, nil
}

func (f fakeL4RuleService) Delete(context.Context, string, int) (service.L4Rule, error) {
	return f.deletedRule, nil
}

type fakeRuleService struct {
	rules       map[string][]service.HTTPRule
	createdRule service.HTTPRule
	updatedRule service.HTTPRule
	deletedRule service.HTTPRule
	state       *fakeRuleServiceState
}

type fakeRuleServiceState struct {
	listAgentIDs   []string
	createAgentIDs []string
	updateAgentIDs []string
	updateIDs      []int
	deleteAgentIDs []string
	deleteIDs      []int
}

func (f fakeRuleService) List(_ context.Context, agentID string) ([]service.HTTPRule, error) {
	if f.state != nil {
		f.state.listAgentIDs = append(f.state.listAgentIDs, agentID)
	}
	rules, ok := f.rules[agentID]
	if !ok {
		return nil, service.ErrAgentNotFound
	}
	return rules, nil
}

func (f fakeRuleService) Create(_ context.Context, agentID string, _ service.HTTPRuleInput) (service.HTTPRule, error) {
	if f.state != nil {
		f.state.createAgentIDs = append(f.state.createAgentIDs, agentID)
	}
	return f.createdRule, nil
}

func (f fakeRuleService) Update(_ context.Context, agentID string, id int, _ service.HTTPRuleInput) (service.HTTPRule, error) {
	if f.state != nil {
		f.state.updateAgentIDs = append(f.state.updateAgentIDs, agentID)
		f.state.updateIDs = append(f.state.updateIDs, id)
	}
	return f.updatedRule, nil
}

func (f fakeRuleService) Delete(_ context.Context, agentID string, id int) (service.HTTPRule, error) {
	if f.state != nil {
		f.state.deleteAgentIDs = append(f.state.deleteAgentIDs, agentID)
		f.state.deleteIDs = append(f.state.deleteIDs, id)
	}
	return f.deletedRule, nil
}

type fakeVersionPolicyService struct {
	policies      []service.VersionPolicy
	createdPolicy service.VersionPolicy
	updatedPolicy service.VersionPolicy
	deletedPolicy service.VersionPolicy
}

func (f fakeVersionPolicyService) List(context.Context) ([]service.VersionPolicy, error) {
	return f.policies, nil
}

func (f fakeVersionPolicyService) Create(context.Context, service.VersionPolicyInput) (service.VersionPolicy, error) {
	return f.createdPolicy, nil
}

func (f fakeVersionPolicyService) Update(context.Context, string, service.VersionPolicyInput) (service.VersionPolicy, error) {
	return f.updatedPolicy, nil
}

func (f fakeVersionPolicyService) Delete(context.Context, string) (service.VersionPolicy, error) {
	return f.deletedPolicy, nil
}

type fakeRelayListenerService struct {
	listeners       map[string][]service.RelayListener
	createdListener service.RelayListener
	updatedListener service.RelayListener
	deletedListener service.RelayListener
	state           *fakeRelayListenerServiceState
}

type fakeRelayListenerServiceState struct {
	createdInputs []service.RelayListenerInput
	updatedInputs []service.RelayListenerInput
}

func (f fakeRelayListenerService) List(_ context.Context, agentID string) ([]service.RelayListener, error) {
	listeners, ok := f.listeners[agentID]
	if !ok {
		return nil, service.ErrAgentNotFound
	}
	return listeners, nil
}

func (f fakeRelayListenerService) Create(_ context.Context, _ string, input service.RelayListenerInput) (service.RelayListener, error) {
	if f.state != nil {
		f.state.createdInputs = append(f.state.createdInputs, input)
	}
	return f.createdListener, nil
}

func (f fakeRelayListenerService) Update(_ context.Context, _ string, _ int, input service.RelayListenerInput) (service.RelayListener, error) {
	if f.state != nil {
		f.state.updatedInputs = append(f.state.updatedInputs, input)
	}
	return f.updatedListener, nil
}

func (f fakeRelayListenerService) Delete(context.Context, string, int) (service.RelayListener, error) {
	return f.deletedListener, nil
}

type fakeCertificateService struct {
	certificates       map[string][]service.ManagedCertificate
	createdCertificate service.ManagedCertificate
	updatedCertificate service.ManagedCertificate
	deletedCertificate service.ManagedCertificate
	issuedCertificate  service.ManagedCertificate
	state              *fakeCertificateServiceState
}

type fakeCertificateServiceState struct {
	createInputs   []service.ManagedCertificateInput
	createAgentIDs []string
	updateInputs   []service.ManagedCertificateInput
	updateAgentIDs []string
	updateIDs      []int
	deleteAgentIDs []string
	deleteIDs      []int
	listAgentIDs   []string
	issueAgentIDs  []string
	issueIDs       []int
}

func (f fakeCertificateService) List(_ context.Context, agentID string) ([]service.ManagedCertificate, error) {
	if f.state != nil {
		f.state.listAgentIDs = append(f.state.listAgentIDs, agentID)
	}
	certs, ok := f.certificates[agentID]
	if !ok {
		return nil, service.ErrAgentNotFound
	}
	return certs, nil
}

func (f fakeCertificateService) Create(_ context.Context, agentID string, input service.ManagedCertificateInput) (service.ManagedCertificate, error) {
	if f.state != nil {
		f.state.createAgentIDs = append(f.state.createAgentIDs, agentID)
		f.state.createInputs = append(f.state.createInputs, input)
	}
	return f.createdCertificate, nil
}

func (f fakeCertificateService) Update(_ context.Context, agentID string, id int, input service.ManagedCertificateInput) (service.ManagedCertificate, error) {
	if f.state != nil {
		f.state.updateAgentIDs = append(f.state.updateAgentIDs, agentID)
		f.state.updateIDs = append(f.state.updateIDs, id)
		f.state.updateInputs = append(f.state.updateInputs, input)
	}
	return f.updatedCertificate, nil
}

func (f fakeCertificateService) Delete(_ context.Context, agentID string, id int) (service.ManagedCertificate, error) {
	if f.state != nil {
		f.state.deleteAgentIDs = append(f.state.deleteAgentIDs, agentID)
		f.state.deleteIDs = append(f.state.deleteIDs, id)
	}
	return f.deletedCertificate, nil
}

func (f fakeCertificateService) Issue(_ context.Context, agentID string, id int) (service.ManagedCertificate, error) {
	if f.state != nil {
		f.state.issueAgentIDs = append(f.state.issueAgentIDs, agentID)
		f.state.issueIDs = append(f.state.issueIDs, id)
	}
	return f.issuedCertificate, nil
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
	if payload["default_agent_id"] != "local" {
		t.Fatalf("default_agent_id = %v", payload["default_agent_id"])
	}
	localAgentEnabled, ok := payload["local_agent_enabled"].(bool)
	if !ok || !localAgentEnabled {
		t.Fatalf("local_agent_enabled = %v", payload["local_agent_enabled"])
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
				Mode:           "local",
				Status:         "online",
				IsLocal:        true,
				HTTPRulesCount: 1,
			}},
		},
		RuleService: fakeRuleService{
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
		L4RuleService:        fakeL4RuleService{},
		VersionPolicyService: fakeVersionPolicyService{},
		RelayListenerService: fakeRelayListenerService{},
		CertificateService:   fakeCertificateService{},
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
	var agentsPayload map[string]any
	if err := json.Unmarshal(agentsResp.Body.Bytes(), &agentsPayload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	agentsValue, ok := agentsPayload["agents"].([]any)
	if !ok || len(agentsValue) != 1 {
		t.Fatalf("unexpected agents payload: %+v", agentsPayload)
	}
	agentValue, ok := agentsValue[0].(map[string]any)
	if !ok {
		t.Fatalf("agents[0] type = %T", agentsValue[0])
	}
	isLocal, ok := agentValue["is_local"].(bool)
	if !ok || !isLocal {
		t.Fatalf("agents[0].is_local = %v", agentValue["is_local"])
	}
	if agentValue["mode"] != "local" {
		t.Fatalf("agents[0].mode = %v", agentValue["mode"])
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

func TestRouterServesAgentControlEndpoints(t *testing.T) {
	state := &fakeAgentServiceState{}
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
			agentsByID: map[string]service.AgentSummary{
				"edge-1": {
					ID:       "edge-1",
					Name:     "Edge 1",
					Mode:     "pull",
					Status:   "online",
					IsLocal:  false,
					AgentURL: "",
				},
			},
			updateAgent: service.AgentSummary{
				ID:       "edge-1",
				Name:     "Edge Renamed",
				Mode:     "master",
				Status:   "online",
				IsLocal:  false,
				AgentURL: "https://edge.example.com",
			},
			deleteAgent: service.AgentSummary{
				ID:      "edge-1",
				Name:    "Edge Renamed",
				Status:  "online",
				IsLocal: false,
			},
			statsByID: map[string]service.AgentStats{
				"edge-1": {
					"totalRequests": "42",
					"status":        "运行中",
				},
			},
			applyResult: service.ApplyAgentResult{
				Message: "waiting for agent heartbeat to apply",
			},
			state: state,
		},
		RuleService:          fakeRuleService{},
		L4RuleService:        fakeL4RuleService{},
		VersionPolicyService: fakeVersionPolicyService{},
		RelayListenerService: fakeRelayListenerService{},
		CertificateService:   fakeCertificateService{},
	})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/panel-api/agents/edge-1", nil)
	getReq.Header.Set("X-Panel-Token", "secret")
	getResp := httptest.NewRecorder()
	router.ServeHTTP(getResp, getReq)
	if getResp.Code != http.StatusOK {
		t.Fatalf("GET /panel-api/agents/edge-1 = %d", getResp.Code)
	}

	patchReq := httptest.NewRequest(http.MethodPatch, "/panel-api/agents/edge-1", bytes.NewBufferString(`{"name":"Edge Renamed"}`))
	patchReq.Header.Set("X-Panel-Token", "secret")
	patchResp := httptest.NewRecorder()
	router.ServeHTTP(patchResp, patchReq)
	if patchResp.Code != http.StatusOK {
		t.Fatalf("PATCH /panel-api/agents/edge-1 = %d", patchResp.Code)
	}
	if state.updateAgentID != "edge-1" || state.updateInput.Name == nil || *state.updateInput.Name != "Edge Renamed" {
		t.Fatalf("PATCH update state = %+v", state)
	}

	putReq := httptest.NewRequest(http.MethodPut, "/panel-api/agents/edge-1", bytes.NewBufferString(`{"name":"Edge Renamed","agent_url":"https://edge.example.com","agent_token":"token-edge-1","version":"1.2.3","tags":["edge"],"capabilities":["http_rules","l4"]}`))
	putReq.Header.Set("X-Panel-Token", "secret")
	putResp := httptest.NewRecorder()
	router.ServeHTTP(putResp, putReq)
	if putResp.Code != http.StatusOK {
		t.Fatalf("PUT /panel-api/agents/edge-1 = %d", putResp.Code)
	}
	if state.updateInput.AgentURL == nil || *state.updateInput.AgentURL != "https://edge.example.com" {
		t.Fatalf("PUT update input = %+v", state.updateInput)
	}

	statsReq := httptest.NewRequest(http.MethodGet, "/api/agents/edge-1/stats", nil)
	statsReq.Header.Set("X-Panel-Token", "secret")
	statsResp := httptest.NewRecorder()
	router.ServeHTTP(statsResp, statsReq)
	if statsResp.Code != http.StatusOK {
		t.Fatalf("GET /api/agents/edge-1/stats = %d", statsResp.Code)
	}
	var statsPayload map[string]any
	if err := json.Unmarshal(statsResp.Body.Bytes(), &statsPayload); err != nil {
		t.Fatalf("json.Unmarshal(stats) error = %v", err)
	}
	statsValue, ok := statsPayload["stats"].(map[string]any)
	if !ok || statsValue["totalRequests"] != "42" || state.statsAgentID != "edge-1" {
		t.Fatalf("stats payload/state = %+v / %+v", statsPayload, state)
	}

	applyReq := httptest.NewRequest(http.MethodPost, "/panel-api/agents/edge-1/apply", bytes.NewBufferString(`{}`))
	applyReq.Header.Set("X-Panel-Token", "secret")
	applyResp := httptest.NewRecorder()
	router.ServeHTTP(applyResp, applyReq)
	if applyResp.Code != http.StatusOK {
		t.Fatalf("POST /panel-api/agents/edge-1/apply = %d", applyResp.Code)
	}
	if state.applyAgentID != "edge-1" {
		t.Fatalf("apply state = %+v", state)
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/panel-api/agents/edge-1", nil)
	deleteReq.Header.Set("X-Panel-Token", "secret")
	deleteResp := httptest.NewRecorder()
	router.ServeHTTP(deleteResp, deleteReq)
	if deleteResp.Code != http.StatusOK {
		t.Fatalf("DELETE /panel-api/agents/edge-1 = %d", deleteResp.Code)
	}
	if state.deleteAgentID != "edge-1" {
		t.Fatalf("delete state = %+v", state)
	}
}

func TestRouterServesL4AndVersionPolicyEndpoints(t *testing.T) {
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
		AgentService: fakeAgentService{},
		RuleService:  fakeRuleService{},
		L4RuleService: fakeL4RuleService{
			rules: map[string][]service.L4Rule{
				"local": {{
					ID:           1,
					AgentID:      "local",
					Name:         "TCP 8443",
					Protocol:     "tcp",
					ListenHost:   "0.0.0.0",
					ListenPort:   8443,
					UpstreamHost: "emby",
					UpstreamPort: 8096,
					Backends:     []service.L4Backend{{Host: "emby", Port: 8096}},
					LoadBalancing: service.L4LoadBalancing{
						Strategy: "round_robin",
					},
					Tuning: service.L4Tuning{
						ProxyProtocol: service.L4ProxyProtocolTuning{},
					},
					RelayChain: []int{},
					Enabled:    true,
					Tags:       []string{},
					Revision:   4,
				}},
			},
			createdRule: service.L4Rule{ID: 2, AgentID: "local", Name: "TCP 9443", Protocol: "tcp", ListenHost: "0.0.0.0", ListenPort: 9443, UpstreamHost: "emby", UpstreamPort: 8096, Backends: []service.L4Backend{{Host: "emby", Port: 8096}}, LoadBalancing: service.L4LoadBalancing{Strategy: "round_robin"}, Tuning: service.L4Tuning{ProxyProtocol: service.L4ProxyProtocolTuning{}}, Enabled: true, Tags: []string{}, Revision: 5},
			updatedRule: service.L4Rule{ID: 2, AgentID: "local", Name: "TCP 9443", Protocol: "tcp", ListenHost: "127.0.0.1", ListenPort: 9443, UpstreamHost: "emby", UpstreamPort: 8096, Backends: []service.L4Backend{{Host: "emby", Port: 8096}}, LoadBalancing: service.L4LoadBalancing{Strategy: "round_robin"}, Tuning: service.L4Tuning{ProxyProtocol: service.L4ProxyProtocolTuning{}}, Enabled: true, Tags: []string{"edge"}, Revision: 6},
			deletedRule: service.L4Rule{ID: 2, AgentID: "local", Name: "TCP 9443", Protocol: "tcp", ListenHost: "127.0.0.1", ListenPort: 9443, UpstreamHost: "emby", UpstreamPort: 8096, Backends: []service.L4Backend{{Host: "emby", Port: 8096}}, LoadBalancing: service.L4LoadBalancing{Strategy: "round_robin"}, Tuning: service.L4Tuning{ProxyProtocol: service.L4ProxyProtocolTuning{}}, Enabled: true, Tags: []string{"edge"}, Revision: 6},
		},
		VersionPolicyService: fakeVersionPolicyService{
			policies: []service.VersionPolicy{{
				ID:             "stable",
				Channel:        "stable",
				DesiredVersion: "1.2.3",
				Packages: []service.VersionPackage{{
					Platform: "linux-amd64",
					URL:      "https://example.com/nre-agent",
					SHA256:   "abc123",
				}},
				Tags: []string{"default"},
			}},
			createdPolicy: service.VersionPolicy{ID: "beta", Channel: "beta", DesiredVersion: "1.3.0", Packages: []service.VersionPackage{{Platform: "linux-amd64", URL: "https://example.com/nre-agent-beta", SHA256: "def456"}}, Tags: []string{"canary"}},
			updatedPolicy: service.VersionPolicy{ID: "beta", Channel: "beta", DesiredVersion: "1.3.1", Packages: []service.VersionPackage{{Platform: "linux-amd64", URL: "https://example.com/nre-agent-beta-2", SHA256: "ghi789"}}, Tags: []string{"canary"}},
			deletedPolicy: service.VersionPolicy{ID: "beta", Channel: "beta", DesiredVersion: "1.3.1"},
		},
		RelayListenerService: fakeRelayListenerService{},
		CertificateService:   fakeCertificateService{},
	})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}

	getL4Req := httptest.NewRequest(http.MethodGet, "/panel-api/agents/local/l4-rules", nil)
	getL4Req.Header.Set("X-Panel-Token", "secret")
	getL4Resp := httptest.NewRecorder()
	router.ServeHTTP(getL4Resp, getL4Req)
	if getL4Resp.Code != http.StatusOK {
		t.Fatalf("GET /panel-api/agents/local/l4-rules = %d", getL4Resp.Code)
	}

	createL4Req := httptest.NewRequest(http.MethodPost, "/panel-api/agents/local/l4-rules", bytes.NewBufferString(`{"listen_port":9443,"upstream_host":"emby","upstream_port":8096}`))
	createL4Req.Header.Set("X-Panel-Token", "secret")
	createL4Req.Header.Set("Content-Type", "application/json")
	createL4Resp := httptest.NewRecorder()
	router.ServeHTTP(createL4Resp, createL4Req)
	if createL4Resp.Code != http.StatusCreated {
		t.Fatalf("POST /panel-api/agents/local/l4-rules = %d", createL4Resp.Code)
	}

	updateL4Req := httptest.NewRequest(http.MethodPut, "/panel-api/agents/local/l4-rules/2", bytes.NewBufferString(`{"listen_host":"127.0.0.1","tags":["edge"]}`))
	updateL4Req.Header.Set("X-Panel-Token", "secret")
	updateL4Req.Header.Set("Content-Type", "application/json")
	updateL4Resp := httptest.NewRecorder()
	router.ServeHTTP(updateL4Resp, updateL4Req)
	if updateL4Resp.Code != http.StatusOK {
		t.Fatalf("PUT /panel-api/agents/local/l4-rules/2 = %d", updateL4Resp.Code)
	}

	deleteL4Req := httptest.NewRequest(http.MethodDelete, "/panel-api/agents/local/l4-rules/2", nil)
	deleteL4Req.Header.Set("X-Panel-Token", "secret")
	deleteL4Resp := httptest.NewRecorder()
	router.ServeHTTP(deleteL4Resp, deleteL4Req)
	if deleteL4Resp.Code != http.StatusOK {
		t.Fatalf("DELETE /panel-api/agents/local/l4-rules/2 = %d", deleteL4Resp.Code)
	}

	getPoliciesReq := httptest.NewRequest(http.MethodGet, "/panel-api/version-policies", nil)
	getPoliciesReq.Header.Set("X-Panel-Token", "secret")
	getPoliciesResp := httptest.NewRecorder()
	router.ServeHTTP(getPoliciesResp, getPoliciesReq)
	if getPoliciesResp.Code != http.StatusOK {
		t.Fatalf("GET /panel-api/version-policies = %d", getPoliciesResp.Code)
	}

	createPolicyReq := httptest.NewRequest(http.MethodPost, "/panel-api/version-policies", bytes.NewBufferString(`{"id":"beta","channel":"beta","desired_version":"1.3.0","packages":[{"platform":"linux-amd64","url":"https://example.com/nre-agent-beta","sha256":"def456"}],"tags":["canary"]}`))
	createPolicyReq.Header.Set("X-Panel-Token", "secret")
	createPolicyReq.Header.Set("Content-Type", "application/json")
	createPolicyResp := httptest.NewRecorder()
	router.ServeHTTP(createPolicyResp, createPolicyReq)
	if createPolicyResp.Code != http.StatusCreated {
		t.Fatalf("POST /panel-api/version-policies = %d", createPolicyResp.Code)
	}

	updatePolicyReq := httptest.NewRequest(http.MethodPut, "/panel-api/version-policies/beta", bytes.NewBufferString(`{"desired_version":"1.3.1","packages":[{"platform":"linux-amd64","url":"https://example.com/nre-agent-beta-2","sha256":"ghi789"}],"tags":["canary"]}`))
	updatePolicyReq.Header.Set("X-Panel-Token", "secret")
	updatePolicyReq.Header.Set("Content-Type", "application/json")
	updatePolicyResp := httptest.NewRecorder()
	router.ServeHTTP(updatePolicyResp, updatePolicyReq)
	if updatePolicyResp.Code != http.StatusOK {
		t.Fatalf("PUT /panel-api/version-policies/beta = %d", updatePolicyResp.Code)
	}

	deletePolicyReq := httptest.NewRequest(http.MethodDelete, "/panel-api/version-policies/beta", nil)
	deletePolicyReq.Header.Set("X-Panel-Token", "secret")
	deletePolicyResp := httptest.NewRecorder()
	router.ServeHTTP(deletePolicyResp, deletePolicyReq)
	if deletePolicyResp.Code != http.StatusOK {
		t.Fatalf("DELETE /panel-api/version-policies/beta = %d", deletePolicyResp.Code)
	}
}

func TestRouterServesRelayListenerAndCertificateEndpoints(t *testing.T) {
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
		AgentService:         fakeAgentService{},
		RuleService:          fakeRuleService{},
		L4RuleService:        fakeL4RuleService{},
		VersionPolicyService: fakeVersionPolicyService{},
		RelayListenerService: fakeRelayListenerService{
			listeners: map[string][]service.RelayListener{
				"local": {{
					ID:                      1,
					AgentID:                 "local",
					Name:                    "relay-a",
					BindHosts:               []string{"0.0.0.0"},
					ListenHost:              "0.0.0.0",
					ListenPort:              7443,
					PublicHost:              "relay-a.example.com",
					PublicPort:              7443,
					Enabled:                 true,
					CertificateID:           intPtr(11),
					TLSMode:                 "pin_or_ca",
					PinSet:                  []service.RelayPin{{Type: "spki_sha256", Value: "abc"}},
					TrustedCACertificateIDs: []int{10},
					AllowSelfSigned:         true,
					Tags:                    []string{"relay"},
					Revision:                3,
				}},
			},
			createdListener: service.RelayListener{ID: 2, AgentID: "local", Name: "relay-b", BindHosts: []string{"0.0.0.0"}, ListenHost: "0.0.0.0", ListenPort: 8443, PublicHost: "relay-b.example.com", PublicPort: 8443, Enabled: true, CertificateID: intPtr(12), TLSMode: "pin_only", PinSet: []service.RelayPin{{Type: "spki_sha256", Value: "def"}}, TrustedCACertificateIDs: []int{}, AllowSelfSigned: false, Tags: []string{"edge"}, Revision: 4},
			updatedListener: service.RelayListener{ID: 2, AgentID: "local", Name: "relay-b", BindHosts: []string{"127.0.0.1"}, ListenHost: "127.0.0.1", ListenPort: 8443, PublicHost: "relay-b.example.com", PublicPort: 8443, Enabled: true, CertificateID: intPtr(12), TLSMode: "ca_only", PinSet: []service.RelayPin{}, TrustedCACertificateIDs: []int{10}, AllowSelfSigned: true, Tags: []string{"edge"}, Revision: 5},
			deletedListener: service.RelayListener{ID: 2, AgentID: "local", Name: "relay-b"},
		},
		CertificateService: fakeCertificateService{
			certificates: map[string][]service.ManagedCertificate{
				"local": {
					{
						ID:              11,
						Domain:          "relay-a.example.com",
						Enabled:         true,
						Scope:           "domain",
						IssuerMode:      "local_http01",
						TargetAgentIDs:  []string{"local"},
						Status:          "active",
						LastIssueAt:     "2026-04-10T00:00:00Z",
						LastError:       "",
						MaterialHash:    "hash1",
						AgentReports:    map[string]service.ManagedCertificateAgentReport{},
						ACMEInfo:        service.ManagedCertificateACMEInfo{},
						Tags:            []string{"relay"},
						Usage:           "relay_tunnel",
						CertificateType: "uploaded",
						SelfSigned:      false,
						Revision:        6,
					},
					{
						ID:              12,
						Domain:          "relay-b.example.com",
						Enabled:         true,
						Scope:           "domain",
						IssuerMode:      "local_http01",
						TargetAgentIDs:  []string{"local"},
						Status:          "pending",
						AgentReports:    map[string]service.ManagedCertificateAgentReport{},
						ACMEInfo:        service.ManagedCertificateACMEInfo{},
						Tags:            []string{"edge"},
						Usage:           "https",
						CertificateType: "acme",
						SelfSigned:      false,
						Revision:        7,
					},
				},
			},
			createdCertificate: service.ManagedCertificate{ID: 12, Domain: "relay-b.example.com", Enabled: true, Scope: "domain", IssuerMode: "local_http01", TargetAgentIDs: []string{"local"}, Status: "pending", Tags: []string{"edge"}, Usage: "https", CertificateType: "acme", Revision: 7},
			updatedCertificate: service.ManagedCertificate{ID: 12, Domain: "relay-b.example.com", Enabled: true, Scope: "domain", IssuerMode: "local_http01", TargetAgentIDs: []string{"local"}, Status: "active", Tags: []string{"edge"}, Usage: "https", CertificateType: "uploaded", Revision: 8},
			deletedCertificate: service.ManagedCertificate{ID: 12, Domain: "relay-b.example.com"},
			issuedCertificate:  service.ManagedCertificate{ID: 12, Domain: "relay-b.example.com", Status: "active", LastIssueAt: "2026-04-10T01:00:00Z"},
		},
	})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}

	getListenersReq := httptest.NewRequest(http.MethodGet, "/panel-api/agents/local/relay-listeners", nil)
	getListenersReq.Header.Set("X-Panel-Token", "secret")
	getListenersResp := httptest.NewRecorder()
	router.ServeHTTP(getListenersResp, getListenersReq)
	if getListenersResp.Code != http.StatusOK {
		t.Fatalf("GET /panel-api/agents/local/relay-listeners = %d", getListenersResp.Code)
	}

	createListenerReq := httptest.NewRequest(http.MethodPost, "/panel-api/agents/local/relay-listeners", bytes.NewBufferString(`{"name":"relay-b","listen_port":8443,"certificate_id":12,"pin_set":[{"type":"spki_sha256","value":"def"}]}`))
	createListenerReq.Header.Set("X-Panel-Token", "secret")
	createListenerReq.Header.Set("Content-Type", "application/json")
	createListenerResp := httptest.NewRecorder()
	router.ServeHTTP(createListenerResp, createListenerReq)
	if createListenerResp.Code != http.StatusCreated {
		t.Fatalf("POST /panel-api/agents/local/relay-listeners = %d", createListenerResp.Code)
	}

	updateListenerReq := httptest.NewRequest(http.MethodPut, "/panel-api/agents/local/relay-listeners/2", bytes.NewBufferString(`{"bind_hosts":["127.0.0.1"],"tls_mode":"ca_only","trusted_ca_certificate_ids":[10],"allow_self_signed":true}`))
	updateListenerReq.Header.Set("X-Panel-Token", "secret")
	updateListenerReq.Header.Set("Content-Type", "application/json")
	updateListenerResp := httptest.NewRecorder()
	router.ServeHTTP(updateListenerResp, updateListenerReq)
	if updateListenerResp.Code != http.StatusOK {
		t.Fatalf("PUT /panel-api/agents/local/relay-listeners/2 = %d", updateListenerResp.Code)
	}

	deleteListenerReq := httptest.NewRequest(http.MethodDelete, "/panel-api/agents/local/relay-listeners/2", nil)
	deleteListenerReq.Header.Set("X-Panel-Token", "secret")
	deleteListenerResp := httptest.NewRecorder()
	router.ServeHTTP(deleteListenerResp, deleteListenerReq)
	if deleteListenerResp.Code != http.StatusOK {
		t.Fatalf("DELETE /panel-api/agents/local/relay-listeners/2 = %d", deleteListenerResp.Code)
	}

	getCertificatesReq := httptest.NewRequest(http.MethodGet, "/panel-api/agents/local/certificates", nil)
	getCertificatesReq.Header.Set("X-Panel-Token", "secret")
	getCertificatesResp := httptest.NewRecorder()
	router.ServeHTTP(getCertificatesResp, getCertificatesReq)
	if getCertificatesResp.Code != http.StatusOK {
		t.Fatalf("GET /panel-api/agents/local/certificates = %d", getCertificatesResp.Code)
	}

	createCertificateReq := httptest.NewRequest(http.MethodPost, "/panel-api/agents/local/certificates", bytes.NewBufferString(`{"domain":"relay-b.example.com","scope":"domain","issuer_mode":"local_http01","certificate_type":"acme","target_agent_ids":["local"]}`))
	createCertificateReq.Header.Set("X-Panel-Token", "secret")
	createCertificateReq.Header.Set("Content-Type", "application/json")
	createCertificateResp := httptest.NewRecorder()
	router.ServeHTTP(createCertificateResp, createCertificateReq)
	if createCertificateResp.Code != http.StatusCreated {
		t.Fatalf("POST /panel-api/agents/local/certificates = %d", createCertificateResp.Code)
	}

	updateCertificateReq := httptest.NewRequest(http.MethodPut, "/panel-api/agents/local/certificates/12", bytes.NewBufferString(`{"certificate_type":"uploaded","status":"active"}`))
	updateCertificateReq.Header.Set("X-Panel-Token", "secret")
	updateCertificateReq.Header.Set("Content-Type", "application/json")
	updateCertificateResp := httptest.NewRecorder()
	router.ServeHTTP(updateCertificateResp, updateCertificateReq)
	if updateCertificateResp.Code != http.StatusOK {
		t.Fatalf("PUT /panel-api/agents/local/certificates/12 = %d", updateCertificateResp.Code)
	}

	issueCertificateReq := httptest.NewRequest(http.MethodPost, "/panel-api/agents/local/certificates/12/issue", bytes.NewBuffer(nil))
	issueCertificateReq.Header.Set("X-Panel-Token", "secret")
	issueCertificateResp := httptest.NewRecorder()
	router.ServeHTTP(issueCertificateResp, issueCertificateReq)
	if issueCertificateResp.Code != http.StatusOK {
		t.Fatalf("POST /panel-api/agents/local/certificates/12/issue = %d", issueCertificateResp.Code)
	}

	deleteCertificateReq := httptest.NewRequest(http.MethodDelete, "/panel-api/agents/local/certificates/12", nil)
	deleteCertificateReq.Header.Set("X-Panel-Token", "secret")
	deleteCertificateResp := httptest.NewRecorder()
	router.ServeHTTP(deleteCertificateResp, deleteCertificateReq)
	if deleteCertificateResp.Code != http.StatusOK {
		t.Fatalf("DELETE /panel-api/agents/local/certificates/12 = %d", deleteCertificateResp.Code)
	}
}

func TestRouterRelayListenerWriteOnlyControlFieldsReachServiceButNotResponse(t *testing.T) {
	state := &fakeRelayListenerServiceState{}
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
		AgentService:         fakeAgentService{},
		RuleService:          fakeRuleService{},
		L4RuleService:        fakeL4RuleService{},
		VersionPolicyService: fakeVersionPolicyService{},
		RelayListenerService: fakeRelayListenerService{
			state:           state,
			createdListener: service.RelayListener{ID: 2, AgentID: "local", Name: "relay-b", BindHosts: []string{"0.0.0.0"}, ListenHost: "0.0.0.0", ListenPort: 8443, PublicHost: "relay-b.example.com", PublicPort: 8443, Enabled: true, CertificateID: intPtr(12), TLSMode: "pin_only", PinSet: []service.RelayPin{{Type: "spki_sha256", Value: "def"}}, TrustedCACertificateIDs: []int{}, AllowSelfSigned: false, Tags: []string{"edge"}, Revision: 4},
			updatedListener: service.RelayListener{ID: 2, AgentID: "local", Name: "relay-b", BindHosts: []string{"127.0.0.1"}, ListenHost: "127.0.0.1", ListenPort: 8443, PublicHost: "relay-b.example.com", PublicPort: 8443, Enabled: true, CertificateID: intPtr(12), TLSMode: "pin_only", PinSet: []service.RelayPin{{Type: "spki_sha256", Value: "def"}}, TrustedCACertificateIDs: []int{}, AllowSelfSigned: false, Tags: []string{"edge"}, Revision: 5},
		},
		CertificateService: fakeCertificateService{},
	})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}

	createReq := httptest.NewRequest(http.MethodPost, "/panel-api/agents/local/relay-listeners", bytes.NewBufferString(`{"name":"relay-b","listen_port":8443,"certificate_source":"auto_relay_ca","trust_mode_source":"auto"}`))
	createReq.Header.Set("X-Panel-Token", "secret")
	createReq.Header.Set("Content-Type", "application/json")
	createResp := httptest.NewRecorder()
	router.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("POST /panel-api/agents/local/relay-listeners = %d", createResp.Code)
	}
	if len(state.createdInputs) != 1 {
		t.Fatalf("len(state.createdInputs) = %d", len(state.createdInputs))
	}
	if state.createdInputs[0].CertificateSource == nil || *state.createdInputs[0].CertificateSource != "auto_relay_ca" {
		t.Fatalf("created CertificateSource = %v", state.createdInputs[0].CertificateSource)
	}
	if state.createdInputs[0].TrustModeSource == nil || *state.createdInputs[0].TrustModeSource != "auto" {
		t.Fatalf("created TrustModeSource = %v", state.createdInputs[0].TrustModeSource)
	}
	if bytes.Contains(createResp.Body.Bytes(), []byte("certificate_source")) || bytes.Contains(createResp.Body.Bytes(), []byte("trust_mode_source")) {
		t.Fatalf("write-only fields leaked in create response: %s", createResp.Body.String())
	}

	updateReq := httptest.NewRequest(http.MethodPut, "/panel-api/agents/local/relay-listeners/2", bytes.NewBufferString(`{"certificate_source":"existing_certificate","certificate_id":12,"trust_mode_source":"custom","tls_mode":"pin_only","pin_set":[{"type":"spki_sha256","value":"def"}]}`))
	updateReq.Header.Set("X-Panel-Token", "secret")
	updateReq.Header.Set("Content-Type", "application/json")
	updateResp := httptest.NewRecorder()
	router.ServeHTTP(updateResp, updateReq)
	if updateResp.Code != http.StatusOK {
		t.Fatalf("PUT /panel-api/agents/local/relay-listeners/2 = %d", updateResp.Code)
	}
	if len(state.updatedInputs) != 1 {
		t.Fatalf("len(state.updatedInputs) = %d", len(state.updatedInputs))
	}
	if state.updatedInputs[0].CertificateSource == nil || *state.updatedInputs[0].CertificateSource != "existing_certificate" {
		t.Fatalf("updated CertificateSource = %v", state.updatedInputs[0].CertificateSource)
	}
	if state.updatedInputs[0].TrustModeSource == nil || *state.updatedInputs[0].TrustModeSource != "custom" {
		t.Fatalf("updated TrustModeSource = %v", state.updatedInputs[0].TrustModeSource)
	}
	if bytes.Contains(updateResp.Body.Bytes(), []byte("certificate_source")) || bytes.Contains(updateResp.Body.Bytes(), []byte("trust_mode_source")) {
		t.Fatalf("write-only fields leaked in update response: %s", updateResp.Body.String())
	}
}

func TestRouterCertificatePEMFieldsReachServiceOnCreateAndUpdate(t *testing.T) {
	state := &fakeCertificateServiceState{}
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
		AgentService:         fakeAgentService{},
		RuleService:          fakeRuleService{},
		L4RuleService:        fakeL4RuleService{},
		VersionPolicyService: fakeVersionPolicyService{},
		RelayListenerService: fakeRelayListenerService{},
		CertificateService: fakeCertificateService{
			state:              state,
			createdCertificate: service.ManagedCertificate{ID: 21, Domain: "uploaded.example.com"},
			updatedCertificate: service.ManagedCertificate{ID: 21, Domain: "uploaded.example.com"},
		},
	})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}

	createReq := httptest.NewRequest(http.MethodPost, "/panel-api/agents/local/certificates", bytes.NewBufferString(`{
		"domain":"uploaded.example.com",
		"issuer_mode":"local_http01",
		"certificate_type":"uploaded",
		"certificate_pem":"-----BEGIN CERTIFICATE-----\nCERT\n-----END CERTIFICATE-----",
		"private_key_pem":"-----BEGIN PRIVATE KEY-----\nKEY\n-----END PRIVATE KEY-----",
		"ca_pem":"-----BEGIN CERTIFICATE-----\nCA\n-----END CERTIFICATE-----"
	}`))
	createReq.Header.Set("X-Panel-Token", "secret")
	createReq.Header.Set("Content-Type", "application/json")
	createResp := httptest.NewRecorder()
	router.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("POST /panel-api/agents/local/certificates = %d", createResp.Code)
	}
	if len(state.createInputs) != 1 {
		t.Fatalf("len(state.createInputs) = %d", len(state.createInputs))
	}
	if state.createInputs[0].CertificatePEM == nil || *state.createInputs[0].CertificatePEM == "" {
		t.Fatalf("create certificate_pem missing: %+v", state.createInputs[0])
	}
	if state.createInputs[0].PrivateKeyPEM == nil || *state.createInputs[0].PrivateKeyPEM == "" {
		t.Fatalf("create private_key_pem missing: %+v", state.createInputs[0])
	}
	if state.createInputs[0].CAPEM == nil || *state.createInputs[0].CAPEM == "" {
		t.Fatalf("create ca_pem missing: %+v", state.createInputs[0])
	}

	updateReq := httptest.NewRequest(http.MethodPut, "/panel-api/agents/local/certificates/21", bytes.NewBufferString(`{
		"certificate_pem":"-----BEGIN CERTIFICATE-----\nCERT2\n-----END CERTIFICATE-----",
		"private_key_pem":"-----BEGIN PRIVATE KEY-----\nKEY2\n-----END PRIVATE KEY-----",
		"ca_pem":"-----BEGIN CERTIFICATE-----\nCA2\n-----END CERTIFICATE-----"
	}`))
	updateReq.Header.Set("X-Panel-Token", "secret")
	updateReq.Header.Set("Content-Type", "application/json")
	updateResp := httptest.NewRecorder()
	router.ServeHTTP(updateResp, updateReq)
	if updateResp.Code != http.StatusOK {
		t.Fatalf("PUT /panel-api/agents/local/certificates/21 = %d", updateResp.Code)
	}
	if len(state.updateInputs) != 1 {
		t.Fatalf("len(state.updateInputs) = %d", len(state.updateInputs))
	}
	if state.updateInputs[0].CertificatePEM == nil || *state.updateInputs[0].CertificatePEM == "" {
		t.Fatalf("update certificate_pem missing: %+v", state.updateInputs[0])
	}
	if state.updateInputs[0].PrivateKeyPEM == nil || *state.updateInputs[0].PrivateKeyPEM == "" {
		t.Fatalf("update private_key_pem missing: %+v", state.updateInputs[0])
	}
	if state.updateInputs[0].CAPEM == nil || *state.updateInputs[0].CAPEM == "" {
		t.Fatalf("update ca_pem missing: %+v", state.updateInputs[0])
	}
}

func TestRouterCertificateIssueRoutesPassRequestedAgentContext(t *testing.T) {
	state := &fakeCertificateServiceState{}
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
		AgentService:         fakeAgentService{},
		RuleService:          fakeRuleService{},
		L4RuleService:        fakeL4RuleService{},
		VersionPolicyService: fakeVersionPolicyService{},
		RelayListenerService: fakeRelayListenerService{},
		CertificateService: fakeCertificateService{
			certificates: map[string][]service.ManagedCertificate{
				"local": {{ID: 21, Domain: "media.example.com", Status: "pending"}},
			},
			state:             state,
			issuedCertificate: service.ManagedCertificate{ID: 21, Domain: "media.example.com", Status: "pending"},
		},
	})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}

	globalReq := httptest.NewRequest(http.MethodPost, "/panel-api/certificates/21/issue", bytes.NewBuffer(nil))
	globalReq.Header.Set("X-Panel-Token", "secret")
	globalResp := httptest.NewRecorder()
	router.ServeHTTP(globalResp, globalReq)
	if globalResp.Code != http.StatusOK {
		t.Fatalf("POST /panel-api/certificates/21/issue = %d", globalResp.Code)
	}

	agentReq := httptest.NewRequest(http.MethodPost, "/panel-api/agents/local/certificates/21/issue", bytes.NewBuffer(nil))
	agentReq.Header.Set("X-Panel-Token", "secret")
	agentResp := httptest.NewRecorder()
	router.ServeHTTP(agentResp, agentReq)
	if agentResp.Code != http.StatusOK {
		t.Fatalf("POST /panel-api/agents/local/certificates/21/issue = %d", agentResp.Code)
	}

	if len(state.issueAgentIDs) != 2 {
		t.Fatalf("len(state.issueAgentIDs) = %d", len(state.issueAgentIDs))
	}
	if state.issueAgentIDs[0] != "" || state.issueIDs[0] != 21 {
		t.Fatalf("global issue call = (%q, %d)", state.issueAgentIDs[0], state.issueIDs[0])
	}
	if state.issueAgentIDs[1] != "local" || state.issueIDs[1] != 21 {
		t.Fatalf("agent issue call = (%q, %d)", state.issueAgentIDs[1], state.issueIDs[1])
	}
}

func TestRouterCertificateIssuePerAgentMissingAgentReturnsNotFoundBeforeIssue(t *testing.T) {
	state := &fakeCertificateServiceState{}
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
		AgentService:         fakeAgentService{},
		RuleService:          fakeRuleService{},
		L4RuleService:        fakeL4RuleService{},
		VersionPolicyService: fakeVersionPolicyService{},
		RelayListenerService: fakeRelayListenerService{},
		CertificateService: fakeCertificateService{
			certificates: map[string][]service.ManagedCertificate{
				"local": {{ID: 21, Domain: "media.example.com", Status: "pending"}},
			},
			state:             state,
			issuedCertificate: service.ManagedCertificate{ID: 21, Domain: "media.example.com", Status: "pending"},
		},
	})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/panel-api/agents/missing/certificates/21/issue", bytes.NewBuffer(nil))
	req.Header.Set("X-Panel-Token", "secret")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("POST /panel-api/agents/missing/certificates/21/issue = %d", resp.Code)
	}

	var payload map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if payload["message"] != "agent not found" {
		t.Fatalf("payload = %+v", payload)
	}
	if len(state.issueAgentIDs) != 0 {
		t.Fatalf("issue should not be called, got %+v", state.issueAgentIDs)
	}
}

func TestRouterCertificateIssuePerAgentUnassignedCertificateReturnsNotFoundBeforeIssue(t *testing.T) {
	state := &fakeCertificateServiceState{}
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
		AgentService:         fakeAgentService{},
		RuleService:          fakeRuleService{},
		L4RuleService:        fakeL4RuleService{},
		VersionPolicyService: fakeVersionPolicyService{},
		RelayListenerService: fakeRelayListenerService{},
		CertificateService: fakeCertificateService{
			certificates: map[string][]service.ManagedCertificate{
				"local": {{ID: 22, Domain: "other.example.com", Status: "pending"}},
			},
			state:             state,
			issuedCertificate: service.ManagedCertificate{ID: 21, Domain: "media.example.com", Status: "pending"},
		},
	})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/panel-api/agents/local/certificates/21/issue", bytes.NewBuffer(nil))
	req.Header.Set("X-Panel-Token", "secret")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("POST /panel-api/agents/local/certificates/21/issue = %d", resp.Code)
	}

	var payload map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if payload["message"] != "certificate not found" {
		t.Fatalf("payload = %+v", payload)
	}
	if len(state.issueAgentIDs) != 0 {
		t.Fatalf("issue should not be called, got %+v", state.issueAgentIDs)
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

func TestRouterServesHTTPRuleCRUDAndValidation(t *testing.T) {
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
		AgentService: fakeAgentService{},
		RuleService: fakeRuleService{
			rules: map[string][]service.HTTPRule{
				"local": {{
					ID:               1,
					AgentID:          "local",
					FrontendURL:      "https://emby.example.com",
					BackendURL:       "http://emby:8096",
					Backends:         []service.HTTPRuleBackend{{URL: "http://emby:8096"}},
					LoadBalancing:    service.HTTPLoadBalancing{Strategy: "round_robin"},
					Enabled:          true,
					Tags:             []string{"media"},
					ProxyRedirect:    true,
					RelayChain:       []int{},
					PassProxyHeaders: true,
					UserAgent:        "",
					CustomHeaders:    []service.HTTPCustomHeader{},
					Revision:         3,
				}},
			},
			createdRule: service.HTTPRule{ID: 2, AgentID: "local", FrontendURL: "https://new.example.com", BackendURL: "http://emby:8096"},
			updatedRule: service.HTTPRule{ID: 2, AgentID: "local", FrontendURL: "https://updated.example.com", BackendURL: "http://emby:8096"},
			deletedRule: service.HTTPRule{ID: 2, AgentID: "local", FrontendURL: "https://updated.example.com", BackendURL: "http://emby:8096"},
		},
		L4RuleService:        fakeL4RuleService{},
		VersionPolicyService: fakeVersionPolicyService{},
		RelayListenerService: fakeRelayListenerService{},
		CertificateService:   fakeCertificateService{},
	})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/panel-api/agents/local/rules", nil)
	getReq.Header.Set("X-Panel-Token", "secret")
	getResp := httptest.NewRecorder()
	router.ServeHTTP(getResp, getReq)
	if getResp.Code != http.StatusOK {
		t.Fatalf("GET /panel-api/agents/local/rules = %d", getResp.Code)
	}
	var getPayload map[string]any
	if err := json.Unmarshal(getResp.Body.Bytes(), &getPayload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if ok, cast := getPayload["ok"].(bool); !cast || !ok {
		t.Fatalf("GET ok = %v", getPayload["ok"])
	}
	if _, found := getPayload["rules"]; !found {
		t.Fatalf("GET payload missing rules: %+v", getPayload)
	}

	getAliasReq := httptest.NewRequest(http.MethodGet, "/api/agents/local/rules", nil)
	getAliasReq.Header.Set("X-Panel-Token", "secret")
	getAliasResp := httptest.NewRecorder()
	router.ServeHTTP(getAliasResp, getAliasReq)
	if getAliasResp.Code != http.StatusOK {
		t.Fatalf("GET /api/agents/local/rules = %d", getAliasResp.Code)
	}

	createReq := httptest.NewRequest(http.MethodPost, "/panel-api/agents/local/rules", bytes.NewBufferString(`{"frontend_url":"https://new.example.com","backend_url":"http://emby:8096"}`))
	createReq.Header.Set("X-Panel-Token", "secret")
	createReq.Header.Set("Content-Type", "application/json")
	createResp := httptest.NewRecorder()
	router.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("POST /panel-api/agents/local/rules = %d", createResp.Code)
	}
	var createPayload map[string]any
	if err := json.Unmarshal(createResp.Body.Bytes(), &createPayload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if ok, cast := createPayload["ok"].(bool); !cast || !ok {
		t.Fatalf("POST ok = %v", createPayload["ok"])
	}
	if _, found := createPayload["rule"]; !found {
		t.Fatalf("POST payload missing rule: %+v", createPayload)
	}

	updateReq := httptest.NewRequest(http.MethodPut, "/panel-api/agents/local/rules/2", bytes.NewBufferString(`{"frontend_url":"https://updated.example.com"}`))
	updateReq.Header.Set("X-Panel-Token", "secret")
	updateReq.Header.Set("Content-Type", "application/json")
	updateResp := httptest.NewRecorder()
	router.ServeHTTP(updateResp, updateReq)
	if updateResp.Code != http.StatusOK {
		t.Fatalf("PUT /panel-api/agents/local/rules/2 = %d", updateResp.Code)
	}
	var updatePayload map[string]any
	if err := json.Unmarshal(updateResp.Body.Bytes(), &updatePayload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if ok, cast := updatePayload["ok"].(bool); !cast || !ok {
		t.Fatalf("PUT ok = %v", updatePayload["ok"])
	}
	if _, found := updatePayload["rule"]; !found {
		t.Fatalf("PUT payload missing rule: %+v", updatePayload)
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/panel-api/agents/local/rules/2", nil)
	deleteReq.Header.Set("X-Panel-Token", "secret")
	deleteResp := httptest.NewRecorder()
	router.ServeHTTP(deleteResp, deleteReq)
	if deleteResp.Code != http.StatusOK {
		t.Fatalf("DELETE /panel-api/agents/local/rules/2 = %d", deleteResp.Code)
	}
	var deletePayload map[string]any
	if err := json.Unmarshal(deleteResp.Body.Bytes(), &deletePayload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if ok, cast := deletePayload["ok"].(bool); !cast || !ok {
		t.Fatalf("DELETE ok = %v", deletePayload["ok"])
	}
	if _, found := deletePayload["rule"]; !found {
		t.Fatalf("DELETE payload missing rule: %+v", deletePayload)
	}

	invalidIDReq := httptest.NewRequest(http.MethodPut, "/panel-api/agents/local/rules/not-an-int", bytes.NewBufferString(`{}`))
	invalidIDReq.Header.Set("X-Panel-Token", "secret")
	invalidIDResp := httptest.NewRecorder()
	router.ServeHTTP(invalidIDResp, invalidIDReq)
	if invalidIDResp.Code != http.StatusBadRequest {
		t.Fatalf("PUT /panel-api/agents/local/rules/not-an-int = %d", invalidIDResp.Code)
	}
}

func TestRouterLegacyLocalAPIRoutesMapToLocalAgent(t *testing.T) {
	agentState := &fakeAgentServiceState{}
	ruleState := &fakeRuleServiceState{}
	router, err := NewRouter(Dependencies{
		Config: config.Config{
			PanelToken:   "secret",
			LocalAgentID: "local-node",
		},
		SystemService: fakeSystemService{
			info: service.SystemInfo{
				Role:              "master",
				LocalApplyRuntime: "go-agent",
				DefaultAgentID:    "local-node",
				LocalAgentEnabled: true,
			},
		},
		AgentService: fakeAgentService{
			applyResult: service.ApplyAgentResult{Message: "applied"},
			statsByID: map[string]service.AgentStats{
				"local-node": {"requests": "9"},
			},
			state: agentState,
		},
		RuleService: fakeRuleService{
			rules: map[string][]service.HTTPRule{
				"local-node": {{
					ID:          7,
					AgentID:     "local-node",
					FrontendURL: "https://media.example.com",
					BackendURL:  "http://emby:8096",
				}},
			},
			createdRule: service.HTTPRule{ID: 8, AgentID: "local-node", FrontendURL: "https://new.example.com", BackendURL: "http://emby:8096"},
			updatedRule: service.HTTPRule{ID: 8, AgentID: "local-node", FrontendURL: "https://updated.example.com", BackendURL: "http://emby:8096"},
			deletedRule: service.HTTPRule{ID: 8, AgentID: "local-node", FrontendURL: "https://updated.example.com", BackendURL: "http://emby:8096"},
			state:       ruleState,
		},
		L4RuleService:        fakeL4RuleService{},
		VersionPolicyService: fakeVersionPolicyService{},
		RelayListenerService: fakeRelayListenerService{},
		CertificateService:   fakeCertificateService{},
	})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}

	unauthorizedReq := httptest.NewRequest(http.MethodGet, "/api/rules", nil)
	unauthorizedResp := httptest.NewRecorder()
	router.ServeHTTP(unauthorizedResp, unauthorizedReq)
	if unauthorizedResp.Code != http.StatusUnauthorized {
		t.Fatalf("GET /api/rules without token = %d", unauthorizedResp.Code)
	}

	getRulesReq := httptest.NewRequest(http.MethodGet, "/api/rules", nil)
	getRulesReq.Header.Set("X-Panel-Token", "secret")
	getRulesResp := httptest.NewRecorder()
	router.ServeHTTP(getRulesResp, getRulesReq)
	if getRulesResp.Code != http.StatusOK {
		t.Fatalf("GET /api/rules = %d", getRulesResp.Code)
	}
	var getRulesPayload map[string]any
	if err := json.Unmarshal(getRulesResp.Body.Bytes(), &getRulesPayload); err != nil {
		t.Fatalf("json.Unmarshal(get rules) error = %v", err)
	}
	if _, ok := getRulesPayload["rules"]; !ok {
		t.Fatalf("GET /api/rules payload missing rules: %+v", getRulesPayload)
	}

	createRuleReq := httptest.NewRequest(http.MethodPost, "/api/rules", bytes.NewBufferString(`{"frontend_url":"https://new.example.com","backend_url":"http://emby:8096"}`))
	createRuleReq.Header.Set("X-Panel-Token", "secret")
	createRuleReq.Header.Set("Content-Type", "application/json")
	createRuleResp := httptest.NewRecorder()
	router.ServeHTTP(createRuleResp, createRuleReq)
	if createRuleResp.Code != http.StatusCreated {
		t.Fatalf("POST /api/rules = %d", createRuleResp.Code)
	}

	updateRuleReq := httptest.NewRequest(http.MethodPut, "/api/rules/8", bytes.NewBufferString(`{"frontend_url":"https://updated.example.com"}`))
	updateRuleReq.Header.Set("X-Panel-Token", "secret")
	updateRuleReq.Header.Set("Content-Type", "application/json")
	updateRuleResp := httptest.NewRecorder()
	router.ServeHTTP(updateRuleResp, updateRuleReq)
	if updateRuleResp.Code != http.StatusOK {
		t.Fatalf("PUT /api/rules/8 = %d", updateRuleResp.Code)
	}

	deleteRuleReq := httptest.NewRequest(http.MethodDelete, "/api/rules/8", nil)
	deleteRuleReq.Header.Set("X-Panel-Token", "secret")
	deleteRuleResp := httptest.NewRecorder()
	router.ServeHTTP(deleteRuleResp, deleteRuleReq)
	if deleteRuleResp.Code != http.StatusOK {
		t.Fatalf("DELETE /api/rules/8 = %d", deleteRuleResp.Code)
	}

	statsReq := httptest.NewRequest(http.MethodGet, "/api/stats", nil)
	statsReq.Header.Set("X-Panel-Token", "secret")
	statsResp := httptest.NewRecorder()
	router.ServeHTTP(statsResp, statsReq)
	if statsResp.Code != http.StatusOK {
		t.Fatalf("GET /api/stats = %d", statsResp.Code)
	}
	var statsPayload map[string]any
	if err := json.Unmarshal(statsResp.Body.Bytes(), &statsPayload); err != nil {
		t.Fatalf("json.Unmarshal(stats) error = %v", err)
	}
	if _, ok := statsPayload["stats"]; !ok {
		t.Fatalf("GET /api/stats payload missing stats: %+v", statsPayload)
	}

	applyReq := httptest.NewRequest(http.MethodPost, "/api/apply", bytes.NewBufferString(`{}`))
	applyReq.Header.Set("X-Panel-Token", "secret")
	applyResp := httptest.NewRecorder()
	router.ServeHTTP(applyResp, applyReq)
	if applyResp.Code != http.StatusOK {
		t.Fatalf("POST /api/apply = %d", applyResp.Code)
	}
	var applyPayload map[string]any
	if err := json.Unmarshal(applyResp.Body.Bytes(), &applyPayload); err != nil {
		t.Fatalf("json.Unmarshal(apply) error = %v", err)
	}
	if applyPayload["message"] != "applied" {
		t.Fatalf("POST /api/apply payload = %+v", applyPayload)
	}

	if len(ruleState.listAgentIDs) == 0 || ruleState.listAgentIDs[0] != "local-node" {
		t.Fatalf("rule list agent context = %+v", ruleState.listAgentIDs)
	}
	if len(ruleState.createAgentIDs) == 0 || ruleState.createAgentIDs[0] != "local-node" {
		t.Fatalf("rule create agent context = %+v", ruleState.createAgentIDs)
	}
	if len(ruleState.updateAgentIDs) == 0 || ruleState.updateAgentIDs[0] != "local-node" || len(ruleState.updateIDs) == 0 || ruleState.updateIDs[0] != 8 {
		t.Fatalf("rule update context = %+v %+v", ruleState.updateAgentIDs, ruleState.updateIDs)
	}
	if len(ruleState.deleteAgentIDs) == 0 || ruleState.deleteAgentIDs[0] != "local-node" || len(ruleState.deleteIDs) == 0 || ruleState.deleteIDs[0] != 8 {
		t.Fatalf("rule delete context = %+v %+v", ruleState.deleteAgentIDs, ruleState.deleteIDs)
	}
	if agentState.statsAgentID != "local-node" {
		t.Fatalf("stats agent context = %q", agentState.statsAgentID)
	}
	if agentState.applyAgentID != "local-node" {
		t.Fatalf("apply agent context = %q", agentState.applyAgentID)
	}
}

func TestRouterGlobalCertificateCRUDRoutesUseGlobalContext(t *testing.T) {
	state := &fakeCertificateServiceState{}
	for _, prefix := range []string{"/api", "/panel-api"} {
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
			AgentService:         fakeAgentService{},
			RuleService:          fakeRuleService{},
			L4RuleService:        fakeL4RuleService{},
			VersionPolicyService: fakeVersionPolicyService{},
			RelayListenerService: fakeRelayListenerService{},
			CertificateService: fakeCertificateService{
				certificates: map[string][]service.ManagedCertificate{
					"": {{ID: 31, Domain: "global.example.com", Status: "pending"}},
				},
				createdCertificate: service.ManagedCertificate{ID: 32, Domain: "created.example.com", Status: "pending"},
				updatedCertificate: service.ManagedCertificate{ID: 31, Domain: "updated.example.com", Status: "pending"},
				deletedCertificate: service.ManagedCertificate{ID: 31, Domain: "updated.example.com", Status: "pending"},
				state:              state,
			},
		})
		if err != nil {
			t.Fatalf("NewRouter() error = %v", err)
		}

		unauthorizedReq := httptest.NewRequest(http.MethodGet, prefix+"/certificates", nil)
		unauthorizedResp := httptest.NewRecorder()
		router.ServeHTTP(unauthorizedResp, unauthorizedReq)
		if unauthorizedResp.Code != http.StatusUnauthorized {
			t.Fatalf("GET %s/certificates without token = %d", prefix, unauthorizedResp.Code)
		}

		listReq := httptest.NewRequest(http.MethodGet, prefix+"/certificates", nil)
		listReq.Header.Set("X-Panel-Token", "secret")
		listResp := httptest.NewRecorder()
		router.ServeHTTP(listResp, listReq)
		if listResp.Code != http.StatusOK {
			t.Fatalf("GET %s/certificates = %d", prefix, listResp.Code)
		}
		var listPayload map[string]any
		if err := json.Unmarshal(listResp.Body.Bytes(), &listPayload); err != nil {
			t.Fatalf("json.Unmarshal(list) error = %v", err)
		}
		if _, ok := listPayload["certificates"]; !ok {
			t.Fatalf("GET %s/certificates payload missing certificates: %+v", prefix, listPayload)
		}

		createReq := httptest.NewRequest(http.MethodPost, prefix+"/certificates", bytes.NewBufferString(`{"domain":"created.example.com","issuer_mode":"local_http01"}`))
		createReq.Header.Set("X-Panel-Token", "secret")
		createReq.Header.Set("Content-Type", "application/json")
		createResp := httptest.NewRecorder()
		router.ServeHTTP(createResp, createReq)
		if createResp.Code != http.StatusCreated {
			t.Fatalf("POST %s/certificates = %d", prefix, createResp.Code)
		}

		updateReq := httptest.NewRequest(http.MethodPut, prefix+"/certificates/31", bytes.NewBufferString(`{"domain":"updated.example.com"}`))
		updateReq.Header.Set("X-Panel-Token", "secret")
		updateReq.Header.Set("Content-Type", "application/json")
		updateResp := httptest.NewRecorder()
		router.ServeHTTP(updateResp, updateReq)
		if updateResp.Code != http.StatusOK {
			t.Fatalf("PUT %s/certificates/31 = %d", prefix, updateResp.Code)
		}

		deleteReq := httptest.NewRequest(http.MethodDelete, prefix+"/certificates/31", nil)
		deleteReq.Header.Set("X-Panel-Token", "secret")
		deleteResp := httptest.NewRecorder()
		router.ServeHTTP(deleteResp, deleteReq)
		if deleteResp.Code != http.StatusOK {
			t.Fatalf("DELETE %s/certificates/31 = %d", prefix, deleteResp.Code)
		}
	}

	if len(state.listAgentIDs) != 2 || state.listAgentIDs[0] != "" || state.listAgentIDs[1] != "" {
		t.Fatalf("list agent contexts = %+v", state.listAgentIDs)
	}
	if len(state.createAgentIDs) != 2 || state.createAgentIDs[0] != "" || state.createAgentIDs[1] != "" {
		t.Fatalf("create agent contexts = %+v", state.createAgentIDs)
	}
	if len(state.updateAgentIDs) != 2 || state.updateAgentIDs[0] != "" || state.updateAgentIDs[1] != "" {
		t.Fatalf("update agent contexts = %+v", state.updateAgentIDs)
	}
	if len(state.deleteAgentIDs) != 2 || state.deleteAgentIDs[0] != "" || state.deleteAgentIDs[1] != "" {
		t.Fatalf("delete agent contexts = %+v", state.deleteAgentIDs)
	}
	if len(state.updateIDs) != 2 || state.updateIDs[0] != 31 || state.updateIDs[1] != 31 {
		t.Fatalf("update ids = %+v", state.updateIDs)
	}
	if len(state.deleteIDs) != 2 || state.deleteIDs[0] != 31 || state.deleteIDs[1] != 31 {
		t.Fatalf("delete ids = %+v", state.deleteIDs)
	}
}

func intPtr(value int) *int {
	return &value
}
