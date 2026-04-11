package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func (f fakeAgentService) Heartbeat(context.Context, service.HeartbeatRequest, string) (service.HeartbeatReply, error) {
	return f.heartbeatReply, nil
}

func TestRouterServesJoinScriptAndHeartbeat(t *testing.T) {
	distDir := filepath.Join(t.TempDir(), "dist")
	assetDir := filepath.Join(t.TempDir(), "assets")
	if err := os.MkdirAll(filepath.Join(distDir, "assets"), 0o755); err != nil {
		t.Fatalf("MkdirAll(dist) error = %v", err)
	}
	if err := os.MkdirAll(assetDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(assetDir) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(distDir, "index.html"), []byte("<html><body>control-plane</body></html>"), 0o644); err != nil {
		t.Fatalf("WriteFile(index.html) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(distDir, "assets", "app.js"), []byte("console.log('panel');"), 0o644); err != nil {
		t.Fatalf("WriteFile(app.js) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(assetDir, "nre-agent-linux-amd64"), []byte{0x7f, 0x45, 0x4c, 0x46}, 0o755); err != nil {
		t.Fatalf("WriteFile(agent asset) error = %v", err)
	}

	router, err := NewRouter(Dependencies{
		Config: config.Config{
			PanelToken:           "secret",
			FrontendDistDir:      distDir,
			PublicAgentAssetsDir: assetDir,
		},
		SystemService: fakeSystemService{
			info: service.SystemInfo{
				Role:              "master",
				LocalApplyRuntime: "go-agent",
				DefaultAgentID:    "local",
				LocalAgentEnabled: true,
			},
		},
		AgentService:         fakeAgentService{heartbeatReply: service.HeartbeatReply{DesiredRevision: 12, Rules: []storage.HTTPRule{}}},
		RuleService:          fakeRuleService{},
		L4RuleService:        fakeL4RuleService{},
		VersionPolicyService: fakeVersionPolicyService{},
		RelayListenerService: fakeRelayListenerService{},
		CertificateService:   fakeCertificateService{},
	})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}

	server := httptest.NewServer(router)
	defer server.Close()

	scriptResp, err := http.Get(server.URL + "/panel-api/public/join-agent.sh")
	if err != nil {
		t.Fatalf("GET join-agent.sh error = %v", err)
	}
	defer scriptResp.Body.Close()
	if scriptResp.StatusCode != http.StatusOK {
		t.Fatalf("GET join-agent.sh = %d", scriptResp.StatusCode)
	}
	scriptBytes := new(bytes.Buffer)
	if _, err := scriptBytes.ReadFrom(scriptResp.Body); err != nil {
		t.Fatalf("ReadFrom(join-agent.sh) error = %v", err)
	}
	script := scriptBytes.String()
	if !strings.Contains(script, `DEFAULT_ASSET_BASE_URL="`+server.URL+`/panel-api/public/agent-assets"`) {
		t.Fatalf("join-agent.sh missing asset base url: %s", script)
	}
	if !strings.Contains(script, `ASSET_NAME="nre-agent-$PLATFORM-$ARCH"`) {
		t.Fatalf("join-agent.sh missing asset name: %s", script)
	}
	if strings.Contains(script, "light-agent.js") {
		t.Fatalf("join-agent.sh unexpectedly references light-agent.js")
	}

	heartbeatBody := bytes.NewBufferString(`{"current_revision":1}`)
	heartbeatReq, err := http.NewRequest(http.MethodPost, server.URL+"/panel-api/agents/heartbeat", heartbeatBody)
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	heartbeatReq.Header.Set("Content-Type", "application/json")
	heartbeatReq.Header.Set("X-Agent-Token", "agent-token")
	heartbeatResp, err := http.DefaultClient.Do(heartbeatReq)
	if err != nil {
		t.Fatalf("POST heartbeat error = %v", err)
	}
	defer heartbeatResp.Body.Close()
	if heartbeatResp.StatusCode != http.StatusOK {
		t.Fatalf("POST heartbeat = %d", heartbeatResp.StatusCode)
	}
	var heartbeatPayload struct {
		Sync service.HeartbeatReply `json:"sync"`
	}
	if err := json.NewDecoder(heartbeatResp.Body).Decode(&heartbeatPayload); err != nil {
		t.Fatalf("Decode heartbeat response error = %v", err)
	}
	if heartbeatPayload.Sync.DesiredRevision != 12 {
		t.Fatalf("heartbeat desired revision = %d", heartbeatPayload.Sync.DesiredRevision)
	}

	assetResp, err := http.Get(server.URL + "/assets/app.js")
	if err != nil {
		t.Fatalf("GET /assets/app.js error = %v", err)
	}
	defer assetResp.Body.Close()
	if assetResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /assets/app.js = %d", assetResp.StatusCode)
	}

	spaResp, err := http.Get(server.URL + "/agents/remote-1")
	if err != nil {
		t.Fatalf("GET /agents/remote-1 error = %v", err)
	}
	defer spaResp.Body.Close()
	if spaResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /agents/remote-1 = %d", spaResp.StatusCode)
	}

	binaryResp, err := http.Get(server.URL + "/panel-api/public/agent-assets/nre-agent-linux-amd64")
	if err != nil {
		t.Fatalf("GET public agent asset error = %v", err)
	}
	defer binaryResp.Body.Close()
	if binaryResp.StatusCode != http.StatusOK {
		t.Fatalf("GET public agent asset = %d", binaryResp.StatusCode)
	}
}

func TestHeartbeatResponseOmitsNoUpdateFieldsButKeepsRelayListeners(t *testing.T) {
	router, err := NewRouter(Dependencies{
		Config:       config.Config{PanelToken: "secret"},
		SystemService: fakeSystemService{},
		AgentService: fakeAgentService{heartbeatReply: service.HeartbeatReply{
			HasUpdate:       false,
			DesiredRevision: 7,
			RelayListeners:  []storage.RelayListener{{ID: 11, AgentID: "edge", Name: "relay-a"}},
		}},
		RuleService:          fakeRuleService{},
		L4RuleService:        fakeL4RuleService{},
		VersionPolicyService: fakeVersionPolicyService{},
		RelayListenerService: fakeRelayListenerService{},
		CertificateService:   fakeCertificateService{},
	})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/panel-api/agents/heartbeat", bytes.NewBufferString(`{"current_revision":7}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Token", "agent-token")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("POST heartbeat = %d", resp.Code)
	}

	var payload map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	syncPayload, ok := payload["sync"].(map[string]any)
	if !ok {
		t.Fatalf("sync payload = %#v", payload["sync"])
	}
	if _, found := syncPayload["rules"]; found {
		t.Fatalf("unexpected rules key in no-update payload: %+v", syncPayload)
	}
	if _, found := syncPayload["l4_rules"]; found {
		t.Fatalf("unexpected l4_rules key in no-update payload: %+v", syncPayload)
	}
	if _, found := syncPayload["certificates"]; found {
		t.Fatalf("unexpected certificates key in no-update payload: %+v", syncPayload)
	}
	if _, found := syncPayload["certificate_policies"]; found {
		t.Fatalf("unexpected certificate_policies key in no-update payload: %+v", syncPayload)
	}
	if _, found := syncPayload["relay_listeners"]; !found {
		t.Fatalf("expected relay_listeners key in no-update payload: %+v", syncPayload)
	}
}

func TestHeartbeatResponseIncludesEmptyArraysWhenUpdateClearsState(t *testing.T) {
	router, err := NewRouter(Dependencies{
		Config:       config.Config{PanelToken: "secret"},
		SystemService: fakeSystemService{},
		AgentService: fakeAgentService{heartbeatReply: service.HeartbeatReply{
			HasUpdate:           true,
			DesiredRevision:     9,
			Rules:               []storage.HTTPRule{},
			L4Rules:             []storage.L4Rule{},
			RelayListeners:      []storage.RelayListener{},
			Certificates:        []storage.ManagedCertificateBundle{},
			CertificatePolicies: []storage.ManagedCertificatePolicy{},
		}},
		RuleService:          fakeRuleService{},
		L4RuleService:        fakeL4RuleService{},
		VersionPolicyService: fakeVersionPolicyService{},
		RelayListenerService: fakeRelayListenerService{},
		CertificateService:   fakeCertificateService{},
	})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/panel-api/agents/heartbeat", bytes.NewBufferString(`{"current_revision":1}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Token", "agent-token")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("POST heartbeat = %d", resp.Code)
	}

	var payload map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	syncPayload, ok := payload["sync"].(map[string]any)
	if !ok {
		t.Fatalf("sync payload = %#v", payload["sync"])
	}
	for _, key := range []string{"rules", "l4_rules", "certificates", "certificate_policies"} {
		value, found := syncPayload[key]
		if !found {
			t.Fatalf("expected %s key in update payload: %+v", key, syncPayload)
		}
		arrayValue, ok := value.([]any)
		if !ok || len(arrayValue) != 0 {
			t.Fatalf("expected %s to be an empty array, got %#v", key, value)
		}
	}
}
