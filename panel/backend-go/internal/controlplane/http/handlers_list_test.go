package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
)

func TestPaginatedResourceListEndpoints(t *testing.T) {
	t.Parallel()
	router, err := NewRouter(Dependencies{
		Config: config.Config{
			PanelToken:     "secret",
			LocalAgentID:   "local",
			LocalAgentName: "Local",
		},
		SystemService: fakeSystemService{
			info: service.SystemInfo{Role: "master", DefaultAgentID: "local", LocalAgentEnabled: true},
		},
		AgentService: fakeAgentService{},
		RuleService: fakeRuleService{
			rules: map[string][]service.HTTPRule{
				"local": {
					{ID: 1, AgentID: "local", AgentName: "Local", FrontendURL: "https://local.example.com"},
					{ID: 2, AgentID: "local", AgentName: "Local", FrontendURL: "https://local-2.example.com"},
				},
				"edge": {
					{ID: 3, AgentID: "edge", AgentName: "Edge", FrontendURL: "https://edge.example.com"},
				},
			},
		},
		L4RuleService: fakeL4RuleService{
			rules: map[string][]service.L4Rule{
				"local": {{ID: 11, AgentID: "local", AgentName: "Local", Name: "l4-local", ListenPort: 1001}},
				"edge":  {{ID: 12, AgentID: "edge", AgentName: "Edge", Name: "l4-edge", ListenPort: 2002}},
			},
		},
		RelayListenerService: fakeRelayListenerService{
			listeners: map[string][]service.RelayListener{
				"local": {{ID: 21, AgentID: "local", AgentName: "Local", Name: "relay-local"}},
				"edge":  {{ID: 22, AgentID: "edge", AgentName: "Edge", Name: "relay-edge"}},
			},
		},
		CertificateService: fakeCertificateService{
			certificates: map[string][]service.ManagedCertificate{
				"": {
					{ID: 31, Domain: "a.example.com", AgentID: "local", AgentName: "Local", TargetAgentIDs: []string{"local"}},
					{ID: 32, Domain: "b.example.com", AgentID: "edge", AgentName: "Edge", TargetAgentIDs: []string{"edge"}},
				},
				"edge": {
					{ID: 32, Domain: "b.example.com", AgentID: "edge", AgentName: "Edge", TargetAgentIDs: []string{"edge"}},
				},
			},
		},
		WireGuardProfileService: unavailableWireGuardProfileService{disabled: false},
		VersionPolicyService:    fakeVersionPolicyService{},
	})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}

	type listEnvelope struct {
		OK       bool               `json:"ok"`
		Rules    []service.HTTPRule `json:"rules"`
		Total    int                `json:"total"`
		Page     int                `json:"page"`
		PageSize int                `json:"page_size"`
	}

	req := httptest.NewRequest(http.MethodGet, "/panel-api/http-rules?page=1&page_size=2", nil)
	req.Header.Set("X-Panel-Token", "secret")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("GET /http-rules = %d body=%s", resp.Code, resp.Body.String())
	}
	var body listEnvelope
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if !body.OK || body.Total != 3 || body.Page != 1 || body.PageSize != 2 || len(body.Rules) != 2 {
		t.Fatalf("unexpected envelope: %+v rules=%v", body, body.Rules)
	}
	for _, rule := range body.Rules {
		if rule.AgentID == "" {
			t.Fatalf("rule missing agent_id: %+v", rule)
		}
	}

	req = httptest.NewRequest(http.MethodGet, "/panel-api/http-rules?agent_id=edge&page=1&page_size=20", nil)
	req.Header.Set("X-Panel-Token", "secret")
	resp = httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("GET /http-rules?agent_id=edge = %d", resp.Code)
	}
	body = listEnvelope{}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode edge body: %v", err)
	}
	if body.Total != 1 || len(body.Rules) != 1 || body.Rules[0].ID != 3 {
		t.Fatalf("edge envelope: %+v", body)
	}

	// page_size clamp
	req = httptest.NewRequest(http.MethodGet, "/panel-api/http-rules?page_size=500", nil)
	req.Header.Set("X-Panel-Token", "secret")
	resp = httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	body = listEnvelope{}
	_ = json.Unmarshal(resp.Body.Bytes(), &body)
	if body.PageSize != service.MaxListPageSize {
		t.Fatalf("page_size = %d, want %d", body.PageSize, service.MaxListPageSize)
	}

	// l4 list
	req = httptest.NewRequest(http.MethodGet, "/panel-api/l4-rules?page=1&page_size=10", nil)
	req.Header.Set("X-Panel-Token", "secret")
	resp = httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("GET /l4-rules = %d body=%s", resp.Code, resp.Body.String())
	}

	// relay list
	req = httptest.NewRequest(http.MethodGet, "/panel-api/relay-listeners?agent_id=edge", nil)
	req.Header.Set("X-Panel-Token", "secret")
	resp = httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("GET /relay-listeners = %d body=%s", resp.Code, resp.Body.String())
	}

	// certificates paginated path via query params on existing /certificates
	req = httptest.NewRequest(http.MethodGet, "/panel-api/certificates?page=1&page_size=10", nil)
	req.Header.Set("X-Panel-Token", "secret")
	resp = httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("GET /certificates?page=1 = %d body=%s", resp.Code, resp.Body.String())
	}
	var certBody struct {
		OK           bool                         `json:"ok"`
		Certificates []service.ManagedCertificate `json:"certificates"`
		Total        int                          `json:"total"`
		Page         int                          `json:"page"`
		PageSize     int                          `json:"page_size"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &certBody); err != nil {
		t.Fatalf("decode cert body: %v", err)
	}
	if !certBody.OK || certBody.Total != 2 || len(certBody.Certificates) != 2 {
		t.Fatalf("cert envelope: %+v", certBody)
	}

	// legacy certificates full list without pagination params still works
	req = httptest.NewRequest(http.MethodGet, "/panel-api/certificates", nil)
	req.Header.Set("X-Panel-Token", "secret")
	resp = httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("GET /certificates legacy = %d body=%s", resp.Code, resp.Body.String())
	}

	// dual-path: enabled/status alone still route to ListPage (paginated envelope)
	req = httptest.NewRequest(http.MethodGet, "/panel-api/certificates?enabled=true", nil)
	req.Header.Set("X-Panel-Token", "secret")
	resp = httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("GET /certificates?enabled=true = %d body=%s", resp.Code, resp.Body.String())
	}
	certBody = struct {
		OK           bool                         `json:"ok"`
		Certificates []service.ManagedCertificate `json:"certificates"`
		Total        int                          `json:"total"`
		Page         int                          `json:"page"`
		PageSize     int                          `json:"page_size"`
	}{}
	if err := json.Unmarshal(resp.Body.Bytes(), &certBody); err != nil {
		t.Fatalf("decode enabled cert body: %v", err)
	}
	if !certBody.OK || certBody.Page == 0 || certBody.PageSize == 0 {
		t.Fatalf("enabled dual-path should use ListPage envelope: %+v", certBody)
	}

	req = httptest.NewRequest(http.MethodGet, "/panel-api/certificates?status=active", nil)
	req.Header.Set("X-Panel-Token", "secret")
	resp = httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("GET /certificates?status=active = %d body=%s", resp.Code, resp.Body.String())
	}
	certBody = struct {
		OK           bool                         `json:"ok"`
		Certificates []service.ManagedCertificate `json:"certificates"`
		Total        int                          `json:"total"`
		Page         int                          `json:"page"`
		PageSize     int                          `json:"page_size"`
	}{}
	if err := json.Unmarshal(resp.Body.Bytes(), &certBody); err != nil {
		t.Fatalf("decode status cert body: %v", err)
	}
	if !certBody.OK || certBody.Page == 0 || certBody.PageSize == 0 {
		t.Fatalf("status dual-path should use ListPage envelope: %+v", certBody)
	}

	// local /rules alias remains non-paginated agent list
	req = httptest.NewRequest(http.MethodGet, "/panel-api/rules", nil)
	req.Header.Set("X-Panel-Token", "secret")
	resp = httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("GET /rules local alias = %d body=%s", resp.Code, resp.Body.String())
	}
}
