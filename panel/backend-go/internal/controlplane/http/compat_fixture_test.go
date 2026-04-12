package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestRouterCompatibilityFixtureServesKeySQLiteBackedPanelEndpoints(t *testing.T) {
	dataRoot := t.TempDir()
	store, err := storage.NewSQLiteStore(dataRoot, "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	if err := store.SaveAgent(t.Context(), storage.AgentRow{
		ID:               "edge-1",
		Name:             "Edge 1",
		AgentToken:       "token-edge-1",
		Mode:             "pull",
		DesiredRevision:  4,
		CurrentRevision:  2,
		LastApplyStatus:  "success",
		CapabilitiesJSON: `["http_rules","cert_install","local_acme"]`,
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	if err := store.SaveHTTPRules(t.Context(), "local", []storage.HTTPRuleRow{{
		ID:                1,
		AgentID:           "local",
		FrontendURL:       "https://fixture.example.com",
		BackendURL:        "http://127.0.0.1:8096",
		BackendsJSON:      `[{"url":"http://127.0.0.1:8096"}]`,
		LoadBalancingJSON: `{"strategy":"round_robin"}`,
		Enabled:           true,
		TagsJSON:          `["media"]`,
		ProxyRedirect:     true,
		RelayChainJSON:    `[]`,
		PassProxyHeaders:  true,
		UserAgent:         "",
		CustomHeadersJSON: `[]`,
		Revision:          3,
	}}); err != nil {
		t.Fatalf("SaveHTTPRules() error = %v", err)
	}
	if err := store.SaveManagedCertificates(t.Context(), []storage.ManagedCertificateRow{{
		ID:              21,
		Domain:          "fixture.example.com",
		Enabled:         true,
		Scope:           "domain",
		IssuerMode:      "local_http01",
		TargetAgentIDs:  `["local"]`,
		Status:          "pending",
		LastIssueAt:     "",
		LastError:       "",
		MaterialHash:    "",
		AgentReports:    `{}`,
		ACMEInfo:        `{"Main_Domain":"fixture.example.com"}`,
		Usage:           "https",
		CertificateType: "acme",
		Revision:        3,
	}}); err != nil {
		t.Fatalf("SaveManagedCertificates() error = %v", err)
	}
	if err := store.SaveVersionPolicies(t.Context(), []storage.VersionPolicyRow{{
		ID:             "stable",
		Channel:        "stable",
		DesiredVersion: "1.2.3",
		PackagesJSON:   `[{"platform":"linux-amd64","url":"https://example.com/nre-agent-linux-amd64","sha256":"abc123","filename":"nre-agent-linux-amd64"}]`,
		TagsJSON:       `["default"]`,
	}}); err != nil {
		t.Fatalf("SaveVersionPolicies() error = %v", err)
	}

	router, err := NewRouter(Dependencies{
		Config: config.Config{
			DataDir:          dataRoot,
			PanelToken:       "secret",
			RegisterToken:    "register-secret",
			EnableLocalAgent: true,
			LocalAgentID:     "local",
			LocalAgentName:   "Local Agent",
		},
		SystemService:        service.NewSystemService(config.Config{EnableLocalAgent: true, LocalAgentID: "local", LocalAgentName: "Local Agent"}),
		AgentService:         service.NewAgentService(config.Config{EnableLocalAgent: true, LocalAgentID: "local", LocalAgentName: "Local Agent"}, store),
		RuleService:          service.NewRuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local", LocalAgentName: "Local Agent"}, store),
		L4RuleService:        service.NewL4RuleService(config.Config{EnableLocalAgent: true, LocalAgentID: "local", LocalAgentName: "Local Agent"}, store),
		VersionPolicyService: service.NewVersionPolicyService(store),
		RelayListenerService: service.NewRelayListenerService(config.Config{EnableLocalAgent: true, LocalAgentID: "local", LocalAgentName: "Local Agent"}, store),
		CertificateService:   service.NewCertificateService(config.Config{EnableLocalAgent: true, LocalAgentID: "local", LocalAgentName: "Local Agent"}, store),
	})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}

	t.Run("info", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/panel-api/info", nil)
		req.Header.Set("X-Panel-Token", "secret")
		resp := httptest.NewRecorder()
		router.ServeHTTP(resp, req)
		if resp.Code != http.StatusOK {
			t.Fatalf("GET /panel-api/info = %d", resp.Code)
		}

		var payload map[string]any
		if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}
		if payload["default_agent_id"] != "local" || payload["local_apply_runtime"] != "go-agent" {
			t.Fatalf("payload = %+v", payload)
		}
		if payload["master_register_token"] != "register-secret" {
			t.Fatalf("master_register_token = %v", payload["master_register_token"])
		}
	})

	t.Run("agents", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/panel-api/agents", nil)
		req.Header.Set("X-Panel-Token", "secret")
		resp := httptest.NewRecorder()
		router.ServeHTTP(resp, req)
		if resp.Code != http.StatusOK {
			t.Fatalf("GET /panel-api/agents = %d", resp.Code)
		}

		var payload struct {
			Agents []map[string]any `json:"agents"`
		}
		if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}
		if len(payload.Agents) != 2 {
			t.Fatalf("agents = %+v", payload.Agents)
		}
		if payload.Agents[0]["id"] != "edge-1" && payload.Agents[1]["id"] != "edge-1" {
			t.Fatalf("remote agent missing from payload: %+v", payload.Agents)
		}
	})

	t.Run("localRulesAlias", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/rules", nil)
		req.Header.Set("X-Panel-Token", "secret")
		resp := httptest.NewRecorder()
		router.ServeHTTP(resp, req)
		if resp.Code != http.StatusOK {
			t.Fatalf("GET /api/rules = %d", resp.Code)
		}

		var payload struct {
			Rules []map[string]any `json:"rules"`
		}
		if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}
		if len(payload.Rules) != 1 {
			t.Fatalf("rules = %+v", payload.Rules)
		}
		if payload.Rules[0]["frontend_url"] != "https://fixture.example.com" {
			t.Fatalf("rule = %+v", payload.Rules[0])
		}
		backends, ok := payload.Rules[0]["backends"].([]any)
		if !ok || len(backends) != 1 {
			t.Fatalf("backends = %#v", payload.Rules[0]["backends"])
		}
	})

	t.Run("globalCertificates", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/panel-api/certificates", nil)
		req.Header.Set("X-Panel-Token", "secret")
		resp := httptest.NewRecorder()
		router.ServeHTTP(resp, req)
		if resp.Code != http.StatusOK {
			t.Fatalf("GET /panel-api/certificates = %d", resp.Code)
		}

		var payload struct {
			Certificates []map[string]any `json:"certificates"`
		}
		if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}
		if len(payload.Certificates) != 1 {
			t.Fatalf("certificates = %+v", payload.Certificates)
		}
		if payload.Certificates[0]["domain"] != "fixture.example.com" || payload.Certificates[0]["issuer_mode"] != "local_http01" {
			t.Fatalf("certificate = %+v", payload.Certificates[0])
		}
		targets, ok := payload.Certificates[0]["target_agent_ids"].([]any)
		if !ok || len(targets) != 1 || targets[0] != "local" {
			t.Fatalf("target_agent_ids = %#v", payload.Certificates[0]["target_agent_ids"])
		}
	})

	t.Run("versionPolicies", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/panel-api/version-policies", nil)
		req.Header.Set("X-Panel-Token", "secret")
		resp := httptest.NewRecorder()
		router.ServeHTTP(resp, req)
		if resp.Code != http.StatusOK {
			t.Fatalf("GET /panel-api/version-policies = %d", resp.Code)
		}

		var payload struct {
			Policies []map[string]any `json:"policies"`
		}
		if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}
		if len(payload.Policies) != 1 {
			t.Fatalf("policies = %+v", payload.Policies)
		}
		if payload.Policies[0]["desired_version"] != "1.2.3" {
			t.Fatalf("version policy = %+v", payload.Policies[0])
		}
	})
}
