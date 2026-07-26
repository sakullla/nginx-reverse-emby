package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
)

type attentionTestPayload struct {
	OK      bool `json:"ok"`
	Offline struct {
		Count    int      `json:"count"`
		AgentIDs []string `json:"agent_ids"`
	} `json:"offline"`
	Blocked struct {
		Count    int      `json:"count"`
		AgentIDs []string `json:"agent_ids"`
	} `json:"blocked"`
	ExpiringCerts struct {
		Count int `json:"count"`
		Items []struct {
			ID       int    `json:"id"`
			Domain   string `json:"domain"`
			NotAfter string `json:"not_after"`
		} `json:"items"`
	} `json:"expiring_certs"`
	SyncFailed struct {
		Count    int      `json:"count"`
		AgentIDs []string `json:"agent_ids"`
	} `json:"sync_failed"`
}

func attentionTestDependencies(trafficSvc fakeTrafficService, agents []service.AgentSummary, certs []service.ManagedCertificate, trafficEnabled bool) Dependencies {
	return Dependencies{
		Config: config.Config{PanelToken: "secret", TrafficStatsEnabled: trafficEnabled},
		SystemService: fakeSystemService{
			info: service.SystemInfo{
				Role:                "master",
				LocalApplyRuntime:   "go-agent",
				TrafficStatsEnabled: trafficEnabled,
			},
		},
		AgentService:         fakeAgentService{agents: agents},
		RuleService:          fakeRuleService{},
		L4RuleService:        fakeL4RuleService{},
		VersionPolicyService: fakeVersionPolicyService{},
		RelayListenerService: fakeRelayListenerService{},
		CertificateService:   fakeCertificateService{certificates: map[string][]service.ManagedCertificate{"": certs}},
		TrafficService:       trafficSvc,
	}
}

func getAttention(t *testing.T, router http.Handler) attentionTestPayload {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/panel-api/dashboard/attention", nil)
	req.Header.Set("X-Panel-Token", "secret")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("GET dashboard/attention = %d body=%s", resp.Code, resp.Body.String())
	}
	var payload attentionTestPayload
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	return payload
}

func TestDashboardAttentionAggregatesSignals(t *testing.T) {
	now := time.Now()
	agents := []service.AgentSummary{
		{ID: "edge-ok", Status: "online", LastApplyStatus: "success"},
		{ID: "edge-off", Status: "offline", LastApplyStatus: "error"},
		{ID: "edge-fail", Status: "online", LastApplyStatus: "error", DesiredRevision: 3, CurrentRevision: 3, LastApplyRevision: 3},
		{ID: "edge-pending", Status: "online", LastApplyStatus: "error", DesiredRevision: 5, CurrentRevision: 3, LastApplyRevision: 3},
	}
	certs := []service.ManagedCertificate{
		{ID: 1, Domain: "soon.example.com", NotAfter: now.Add(10 * 24 * time.Hour).Format(time.RFC3339)},
		{ID: 2, Domain: "expired.example.com", NotAfter: now.Add(-24 * time.Hour).Format(time.RFC3339)},
		{ID: 3, Domain: "far.example.com", NotAfter: now.Add(90 * 24 * time.Hour).Format(time.RFC3339)},
		{ID: 4, Domain: "no-date.example.com"},
	}
	trafficSvc := fakeTrafficService{
		overview: service.TrafficOverviewResult{
			Agents: []service.TrafficOverviewAgent{
				{AgentID: "edge-ok", Blocked: true},
				{AgentID: "edge-fail", Blocked: false},
			},
		},
	}
	router, err := NewRouter(attentionTestDependencies(trafficSvc, agents, certs, true))
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}

	payload := getAttention(t, router)
	if !payload.OK {
		t.Fatalf("payload not ok: %+v", payload)
	}
	if payload.Offline.Count != 1 || len(payload.Offline.AgentIDs) != 1 || payload.Offline.AgentIDs[0] != "edge-off" {
		t.Fatalf("offline = %+v", payload.Offline)
	}
	if payload.Blocked.Count != 1 || len(payload.Blocked.AgentIDs) != 1 || payload.Blocked.AgentIDs[0] != "edge-ok" {
		t.Fatalf("blocked = %+v", payload.Blocked)
	}
	// edge-off 离线优先,不重复计入同步失败;edge-pending 尚未应用到目标版本,算 pending 不算失败
	if payload.SyncFailed.Count != 1 || len(payload.SyncFailed.AgentIDs) != 1 || payload.SyncFailed.AgentIDs[0] != "edge-fail" {
		t.Fatalf("sync_failed = %+v", payload.SyncFailed)
	}
	if payload.ExpiringCerts.Count != 1 || len(payload.ExpiringCerts.Items) != 1 {
		t.Fatalf("expiring_certs = %+v", payload.ExpiringCerts)
	}
	if payload.ExpiringCerts.Items[0].ID != 1 || payload.ExpiringCerts.Items[0].Domain != "soon.example.com" {
		t.Fatalf("expiring cert item = %+v", payload.ExpiringCerts.Items[0])
	}
}

func TestDashboardAttentionSkipsBlockedWhenTrafficDisabled(t *testing.T) {
	state := &fakeTrafficServiceState{}
	agents := []service.AgentSummary{
		{ID: "edge-1", Status: "online", LastApplyStatus: "success"},
	}
	router, err := NewRouter(attentionTestDependencies(fakeTrafficService{state: state}, agents, nil, false))
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}

	payload := getAttention(t, router)
	if !payload.OK {
		t.Fatalf("payload not ok: %+v", payload)
	}
	if payload.Blocked.Count != 0 || len(payload.Blocked.AgentIDs) != 0 {
		t.Fatalf("blocked = %+v", payload.Blocked)
	}
	if state.overviewCalled {
		t.Fatal("Overview() should not be called when traffic stats disabled")
	}
}
