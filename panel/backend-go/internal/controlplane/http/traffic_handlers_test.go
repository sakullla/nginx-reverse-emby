package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
)

type fakeTrafficService struct {
	policy     service.TrafficPolicy
	summary    service.TrafficSummary
	trend      []service.TrafficTrendPoint
	calibrated service.TrafficSummary
	cleanup    service.TrafficCleanupResult
	err        error
	state      *fakeTrafficServiceState
}

type fakeTrafficServiceState struct {
	policyAgentID    string
	updateAgentID    string
	updateInput      service.TrafficPolicy
	summaryAgentID   string
	trendQuery       service.TrafficTrendQuery
	calibrateAgentID string
	calibrationInput service.TrafficCalibrationRequest
	cleanupAgentID   string
}

func (f fakeTrafficService) GetPolicy(_ context.Context, agentID string) (service.TrafficPolicy, error) {
	if f.state != nil {
		f.state.policyAgentID = agentID
	}
	if f.err != nil {
		return service.TrafficPolicy{}, f.err
	}
	return f.policy, nil
}

func (f fakeTrafficService) UpdatePolicy(_ context.Context, agentID string, input service.TrafficPolicy) (service.TrafficPolicy, error) {
	if f.state != nil {
		f.state.updateAgentID = agentID
		f.state.updateInput = input
	}
	if f.err != nil {
		return service.TrafficPolicy{}, f.err
	}
	return input, nil
}

func (f fakeTrafficService) Summary(_ context.Context, agentID string) (service.TrafficSummary, error) {
	if f.state != nil {
		f.state.summaryAgentID = agentID
	}
	if f.err != nil {
		return service.TrafficSummary{}, f.err
	}
	return f.summary, nil
}

func (f fakeTrafficService) Trend(_ context.Context, query service.TrafficTrendQuery) ([]service.TrafficTrendPoint, error) {
	if f.state != nil {
		f.state.trendQuery = query
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.trend, nil
}

func (f fakeTrafficService) Calibrate(_ context.Context, agentID string, request service.TrafficCalibrationRequest) (service.TrafficSummary, error) {
	if f.state != nil {
		f.state.calibrateAgentID = agentID
		f.state.calibrationInput = request
	}
	if f.err != nil {
		return service.TrafficSummary{}, f.err
	}
	return f.calibrated, nil
}

func (f fakeTrafficService) Cleanup(_ context.Context, agentID string) (service.TrafficCleanupResult, error) {
	if f.state != nil {
		f.state.cleanupAgentID = agentID
	}
	if f.err != nil {
		return service.TrafficCleanupResult{}, f.err
	}
	return f.cleanup, nil
}

func TestTrafficPolicyRoutesRequirePanelToken(t *testing.T) {
	router, err := NewRouter(trafficTestDependencies(fakeTrafficService{}))
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/panel-api/agents/edge-1/traffic-policy", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("GET traffic-policy without token = %d", resp.Code)
	}
}

func TestTrafficPolicyDisabledReturnsStable404(t *testing.T) {
	trafficErr := service.TrafficServiceError{
		Code: service.ErrCodeTrafficStatsDisabled,
		Err:  service.ErrTrafficStatsDisabled,
	}
	router, err := NewRouter(trafficTestDependencies(fakeTrafficService{err: trafficErr}))
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/panel-api/agents/edge-1/traffic-policy", nil)
	req.Header.Set("X-Panel-Token", "secret")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusNotFound {
		t.Fatalf("GET traffic-policy disabled = %d", resp.Code)
	}
	var payload map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if payload["error"] != "traffic stats disabled" || payload["code"] != service.ErrCodeTrafficStatsDisabled {
		t.Fatalf("disabled payload = %+v", payload)
	}
}

func TestTrafficTrendReturnsPoints(t *testing.T) {
	state := &fakeTrafficServiceState{}
	router, err := NewRouter(trafficTestDependencies(fakeTrafficService{
		state: state,
		trend: []service.TrafficTrendPoint{
			{
				AgentID:        "edge-1",
				ScopeType:      "http_rule",
				ScopeID:        "7",
				BucketStart:    "2026-05-03T10:00:00Z",
				RXBytes:        10,
				TXBytes:        20,
				AccountedBytes: 30,
			},
		},
	}))
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/panel-api/agents/edge-1/traffic-trend?granularity=day&from=2026-05-01T00:00:00Z&to=2026-05-03T00:00:00Z&scope_type=http_rule&scope_id=7", nil)
	req.Header.Set("X-Panel-Token", "secret")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("GET traffic-trend = %d body=%s", resp.Code, resp.Body.String())
	}
	if state.trendQuery.AgentID != "edge-1" ||
		state.trendQuery.Granularity != "day" ||
		state.trendQuery.From != "2026-05-01T00:00:00Z" ||
		state.trendQuery.To != "2026-05-03T00:00:00Z" ||
		state.trendQuery.ScopeType != "http_rule" ||
		state.trendQuery.ScopeID != "7" {
		t.Fatalf("trend query = %+v", state.trendQuery)
	}
	var payload struct {
		OK     bool                        `json:"ok"`
		Points []service.TrafficTrendPoint `json:"points"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if !payload.OK || len(payload.Points) != 1 || payload.Points[0].AccountedBytes != 30 {
		t.Fatalf("trend payload = %+v", payload)
	}
}

func TestTrafficHandlersForwardPolicySummaryCalibrationAndCleanup(t *testing.T) {
	state := &fakeTrafficServiceState{}
	quota := int64(1024)
	router, err := NewRouter(trafficTestDependencies(fakeTrafficService{
		state: state,
		summary: service.TrafficSummary{
			AgentID:        "edge-1",
			UsedBytes:      256,
			AccountedBytes: 512,
		},
		calibrated: service.TrafficSummary{
			AgentID:   "edge-1",
			UsedBytes: 123,
		},
		cleanup: service.TrafficCleanupResult{
			AgentID:     "edge-1",
			DeletedRows: 4,
		},
	}))
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}

	patchBody := []byte(`{"direction":"tx","cycle_start_day":3,"monthly_quota_bytes":1024,"block_when_exceeded":true}`)
	patchReq := httptest.NewRequest(http.MethodPatch, "/panel-api/agents/edge-1/traffic-policy", bytes.NewReader(patchBody))
	patchReq.Header.Set("X-Panel-Token", "secret")
	patchResp := httptest.NewRecorder()
	router.ServeHTTP(patchResp, patchReq)
	if patchResp.Code != http.StatusOK {
		t.Fatalf("PATCH traffic-policy = %d body=%s", patchResp.Code, patchResp.Body.String())
	}
	if state.updateAgentID != "edge-1" ||
		state.updateInput.Direction != "tx" ||
		state.updateInput.CycleStartDay != 3 ||
		state.updateInput.MonthlyQuotaBytes == nil ||
		*state.updateInput.MonthlyQuotaBytes != quota ||
		!state.updateInput.BlockWhenExceeded {
		t.Fatalf("policy update state = %+v", state)
	}

	summaryReq := httptest.NewRequest(http.MethodGet, "/panel-api/agents/edge-1/traffic-summary", nil)
	summaryReq.Header.Set("X-Panel-Token", "secret")
	summaryResp := httptest.NewRecorder()
	router.ServeHTTP(summaryResp, summaryReq)
	if summaryResp.Code != http.StatusOK {
		t.Fatalf("GET traffic-summary = %d body=%s", summaryResp.Code, summaryResp.Body.String())
	}
	var summaryPayload struct {
		OK      bool                   `json:"ok"`
		Summary service.TrafficSummary `json:"summary"`
	}
	if err := json.Unmarshal(summaryResp.Body.Bytes(), &summaryPayload); err != nil {
		t.Fatalf("json.Unmarshal(summary) error = %v", err)
	}
	if state.summaryAgentID != "edge-1" || !summaryPayload.OK || summaryPayload.Summary.UsedBytes != 256 {
		t.Fatalf("summary state=%+v payload=%+v", state, summaryPayload)
	}

	calibrateReq := httptest.NewRequest(http.MethodPost, "/panel-api/agents/edge-1/traffic-calibration", bytes.NewReader([]byte(`{"used_bytes":123}`)))
	calibrateReq.Header.Set("X-Panel-Token", "secret")
	calibrateResp := httptest.NewRecorder()
	router.ServeHTTP(calibrateResp, calibrateReq)
	if calibrateResp.Code != http.StatusOK {
		t.Fatalf("POST traffic-calibration = %d body=%s", calibrateResp.Code, calibrateResp.Body.String())
	}
	if state.calibrateAgentID != "edge-1" || state.calibrationInput.UsedBytes != 123 {
		t.Fatalf("calibration state = %+v", state)
	}

	cleanupReq := httptest.NewRequest(http.MethodPost, "/panel-api/agents/edge-1/traffic-cleanup", nil)
	cleanupReq.Header.Set("X-Panel-Token", "secret")
	cleanupResp := httptest.NewRecorder()
	router.ServeHTTP(cleanupResp, cleanupReq)
	if cleanupResp.Code != http.StatusOK {
		t.Fatalf("POST traffic-cleanup = %d body=%s", cleanupResp.Code, cleanupResp.Body.String())
	}
	var cleanupPayload struct {
		OK     bool                         `json:"ok"`
		Result service.TrafficCleanupResult `json:"result"`
	}
	if err := json.Unmarshal(cleanupResp.Body.Bytes(), &cleanupPayload); err != nil {
		t.Fatalf("json.Unmarshal(cleanup) error = %v", err)
	}
	if state.cleanupAgentID != "edge-1" || !cleanupPayload.OK || cleanupPayload.Result.DeletedRows != 4 {
		t.Fatalf("cleanup state=%+v payload=%+v", state, cleanupPayload)
	}
}

func TestSystemInfoExposesTrafficStatsEnabled(t *testing.T) {
	router, err := NewRouter(trafficTestDependencies(fakeTrafficService{}))
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/panel-api/info", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("GET /panel-api/info = %d", resp.Code)
	}
	var payload map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if payload["traffic_stats_enabled"] != false {
		t.Fatalf("traffic_stats_enabled = %v", payload["traffic_stats_enabled"])
	}
}

func trafficTestDependencies(trafficSvc fakeTrafficService) Dependencies {
	return Dependencies{
		Config: config.Config{PanelToken: "secret"},
		SystemService: fakeSystemService{
			info: service.SystemInfo{
				Role:                "master",
				LocalApplyRuntime:   "go-agent",
				TrafficStatsEnabled: false,
			},
		},
		AgentService:         fakeAgentService{},
		RuleService:          fakeRuleService{},
		L4RuleService:        fakeL4RuleService{},
		VersionPolicyService: fakeVersionPolicyService{},
		RelayListenerService: fakeRelayListenerService{},
		CertificateService:   fakeCertificateService{},
		TrafficService:       trafficSvc,
	}
}
