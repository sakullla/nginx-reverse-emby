package http

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

const (
	httpTestWireGuardPrivateKey   = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	httpTestWireGuardPublicKey    = "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB="
	httpTestWireGuardPresharedKey = "CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC="
	httpTestWireGuardRedacted     = "xxxxx"
)

func TestRouterWireGuardProfilesCreateAndListRedactsSecrets(t *testing.T) {
	router, cleanup := newWireGuardHTTPTestRouter(t)
	defer cleanup()

	for _, prefix := range []string{"/api", "/panel-api"} {
		createReq := httptest.NewRequest(http.MethodPost, prefix+"/agents/local/wireguard-profiles", bytes.NewBufferString(validWireGuardHTTPPayload()))
		createReq.Header.Set("X-Panel-Token", "secret")
		createReq.Header.Set("Content-Type", "application/json")
		createResp := httptest.NewRecorder()
		router.ServeHTTP(createResp, createReq)
		if createResp.Code != http.StatusCreated {
			t.Fatalf("POST %s/agents/local/wireguard-profiles = %d, body=%s", prefix, createResp.Code, createResp.Body.String())
		}
		created := decodeWireGuardHTTPProfileResponse(t, createResp.Body.Bytes(), "profile")
		assertWireGuardHTTPSecretsRedacted(t, created)

		getReq := httptest.NewRequest(http.MethodGet, prefix+"/agents/local/wireguard-profiles", nil)
		getReq.Header.Set("X-Panel-Token", "secret")
		getResp := httptest.NewRecorder()
		router.ServeHTTP(getResp, getReq)
		if getResp.Code != http.StatusOK {
			t.Fatalf("GET %s/agents/local/wireguard-profiles = %d, body=%s", prefix, getResp.Code, getResp.Body.String())
		}
		profiles := decodeWireGuardHTTPProfilesResponse(t, getResp.Body.Bytes())
		if len(profiles) == 0 {
			t.Fatalf("GET %s returned no profiles", prefix)
		}
		assertWireGuardHTTPSecretsRedacted(t, profiles[len(profiles)-1])
	}
}

func TestRouterWireGuardProfilesRejectsInvalidCIDR(t *testing.T) {
	router, cleanup := newWireGuardHTTPTestRouter(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/panel-api/agents/local/wireguard-profiles", bytes.NewBufferString(`{
		"name":"bad cidr",
		"mode":"generic_wireguard",
		"private_key":"`+httpTestWireGuardPrivateKey+`",
		"listen_port":51820,
		"addresses":["10.20.0.1"],
		"peers":[{"name":"peer-a","public_key":"`+httpTestWireGuardPublicKey+`","preshared_key":"`+httpTestWireGuardPresharedKey+`","allowed_ips":["10.20.0.2/32"]}]
	}`))
	req.Header.Set("X-Panel-Token", "secret")
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("POST invalid CIDR = %d, body=%s", resp.Code, resp.Body.String())
	}
}

func TestRouterWireGuardProfilesDeleteRouteWorks(t *testing.T) {
	router, cleanup := newWireGuardHTTPTestRouter(t)
	defer cleanup()

	createReq := httptest.NewRequest(http.MethodPost, "/panel-api/agents/local/wireguard-profiles", bytes.NewBufferString(validWireGuardHTTPPayload()))
	createReq.Header.Set("X-Panel-Token", "secret")
	createReq.Header.Set("Content-Type", "application/json")
	createResp := httptest.NewRecorder()
	router.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("POST /panel-api/agents/local/wireguard-profiles = %d, body=%s", createResp.Code, createResp.Body.String())
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/panel-api/agents/local/wireguard-profiles/1", nil)
	deleteReq.Header.Set("X-Panel-Token", "secret")
	deleteResp := httptest.NewRecorder()
	router.ServeHTTP(deleteResp, deleteReq)
	if deleteResp.Code != http.StatusOK {
		t.Fatalf("DELETE /panel-api/agents/local/wireguard-profiles/1 = %d, body=%s", deleteResp.Code, deleteResp.Body.String())
	}
	deleted := decodeWireGuardHTTPProfileResponse(t, deleteResp.Body.Bytes(), "profile")
	assertWireGuardHTTPSecretsRedacted(t, deleted)
}

func newWireGuardHTTPTestRouter(t *testing.T) (http.Handler, func()) {
	t.Helper()

	store, err := storage.NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	cfg := config.Config{PanelToken: "secret", LocalAgentID: "local", EnableLocalAgent: true}
	router, err := NewRouter(Dependencies{
		Config:                  cfg,
		SystemService:           fakeSystemService{},
		AgentService:            fakeAgentService{},
		RuleService:             fakeRuleService{},
		L4RuleService:           fakeL4RuleService{},
		VersionPolicyService:    fakeVersionPolicyService{},
		RelayListenerService:    fakeRelayListenerService{},
		CertificateService:      fakeCertificateService{},
		TaskService:             fakeTaskService{},
		BackupService:           fakeBackupService{},
		TrafficService:          unavailableTrafficService{},
		WireGuardProfileService: service.NewWireGuardProfileService(cfg, store),
	})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}
	return router, func() { _ = store.Close() }
}

func validWireGuardHTTPPayload() string {
	return `{
		"name":"wg-a",
		"mode":"generic_wireguard",
		"private_key":"` + httpTestWireGuardPrivateKey + `",
		"listen_port":51820,
		"addresses":["10.20.0.1/24"],
		"peers":[{"name":"peer-a","public_key":"` + httpTestWireGuardPublicKey + `","preshared_key":"` + httpTestWireGuardPresharedKey + `","endpoint":"peer.example.com:51820","allowed_ips":["10.20.0.2/32"],"persistent_keepalive_seconds":25}],
		"dns":["1.1.1.1"],
		"mtu":1420,
		"enabled":true,
		"tags":["edge"]
	}`
}

func decodeWireGuardHTTPProfileResponse(t *testing.T, body []byte, field string) service.WireGuardProfile {
	t.Helper()
	var payload struct {
		OK      bool                     `json:"ok"`
		Profile service.WireGuardProfile `json:"profile"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !payload.OK {
		t.Fatalf("ok = false in %s", string(body))
	}
	if field != "profile" {
		t.Fatalf("unsupported field %q", field)
	}
	return payload.Profile
}

func decodeWireGuardHTTPProfilesResponse(t *testing.T, body []byte) []service.WireGuardProfile {
	t.Helper()
	var payload struct {
		OK       bool                       `json:"ok"`
		Profiles []service.WireGuardProfile `json:"profiles"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !payload.OK {
		t.Fatalf("ok = false in %s", string(body))
	}
	return payload.Profiles
}

func assertWireGuardHTTPSecretsRedacted(t *testing.T, profile service.WireGuardProfile) {
	t.Helper()
	if profile.PrivateKey != httpTestWireGuardRedacted {
		t.Fatalf("private_key = %q, want redacted", profile.PrivateKey)
	}
	if len(profile.Peers) != 1 {
		t.Fatalf("peers length = %d, want 1", len(profile.Peers))
	}
	if profile.Peers[0].PresharedKey != httpTestWireGuardRedacted {
		t.Fatalf("peer preshared_key = %q, want redacted", profile.Peers[0].PresharedKey)
	}
}
