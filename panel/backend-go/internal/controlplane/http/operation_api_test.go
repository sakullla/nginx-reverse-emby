package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestMutationEndpointsReturnAcceptedEnvelopeAndReplayOriginalResource(t *testing.T) {
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.PanelToken = "panel-secret"
	cfg.EnableLocalAgent = true
	cfg.LocalAgentID = "local"
	cfg.LocalAgentName = "Local"

	store, err := storage.NewSQLiteStore(cfg.DataDir, cfg.LocalAgentID)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	previousOpenConfiguredStore := openConfiguredStore
	openConfiguredStore = func(config.Config) (*storage.GormStore, error) { return store, nil }
	t.Cleanup(func() { openConfiguredStore = previousOpenConfiguredStore })

	router, err := NewRouter(Dependencies{Config: cfg})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}
	t.Cleanup(func() {
		if closer, ok := router.(interface{ Close() error }); ok {
			_ = closer.Close()
		}
	})

	const createBody = `{"frontend_url":"https://accepted.example.com","backends":[{"url":"http://127.0.0.1:8081"}],"enabled":true}`
	first := performPanelMutation(t, router, http.MethodPost, "/panel-api/agents/local/rules", createBody, "create-rule-1")
	if first.Code != http.StatusAccepted {
		t.Fatalf("first create status = %d body=%s, want 202", first.Code, first.Body.String())
	}
	firstPayload := decodeAcceptedMutation(t, first)
	firstRule := mutationResource(t, firstPayload, "rule")
	if firstPayload.OperationID == "" || firstPayload.StatusURL == "" || firstPayload.AgentID != "local" || firstPayload.DesiredRevision <= 0 {
		t.Fatalf("first accepted envelope = %+v", firstPayload)
	}
	statusReq := httptest.NewRequest(http.MethodGet, firstPayload.StatusURL, nil)
	statusReq.Header.Set("X-Panel-Token", "panel-secret")
	statusResp := httptest.NewRecorder()
	router.ServeHTTP(statusResp, statusReq)
	if statusResp.Code != http.StatusOK || !strings.Contains(statusResp.Body.String(), firstPayload.OperationID) {
		t.Fatalf("operation status = %d body=%s", statusResp.Code, statusResp.Body.String())
	}
	eventsReq := httptest.NewRequest(http.MethodGet, "/panel-api/revision-events?operation_id="+firstPayload.OperationID+"&limit=1", nil)
	eventsReq.Header.Set("X-Panel-Token", "panel-secret")
	eventsResp := httptest.NewRecorder()
	router.ServeHTTP(eventsResp, eventsReq)
	if eventsResp.Code != http.StatusOK || !strings.Contains(eventsResp.Body.String(), `"next_cursor":`) {
		t.Fatalf("revision events = %d body=%s", eventsResp.Code, eventsResp.Body.String())
	}

	replayed := performPanelMutation(t, router, http.MethodPost, "/panel-api/agents/local/rules", createBody, "create-rule-1")
	if replayed.Code != http.StatusAccepted {
		t.Fatalf("replayed create status = %d body=%s, want 202", replayed.Code, replayed.Body.String())
	}
	replayedPayload := decodeAcceptedMutation(t, replayed)
	if replayedPayload.OperationID != firstPayload.OperationID || replayedPayload.DesiredRevision != firstPayload.DesiredRevision {
		t.Fatalf("replayed envelope = %+v, first = %+v", replayedPayload, firstPayload)
	}
	replayedRule := mutationResource(t, replayedPayload, "rule")
	if replayedRule["id"] != firstRule["id"] || replayedRule["frontend_url"] != firstRule["frontend_url"] {
		t.Fatalf("replayed rule = %+v, first = %+v", replayedRule, firstRule)
	}
	conflictingReplay := performPanelMutation(t, router, http.MethodPost, "/panel-api/agents/local/rules",
		`{"frontend_url":"https://different.example.com","backends":[{"url":"http://127.0.0.1:8081"}]}`, "create-rule-1")
	if conflictingReplay.Code != http.StatusConflict {
		t.Fatalf("conflicting replay status = %d body=%s, want 409", conflictingReplay.Code, conflictingReplay.Body.String())
	}
	staticConflict := performPanelMutation(t, router, http.MethodPost, "/panel-api/agents/local/rules", createBody, "create-rule-2")
	if staticConflict.Code != http.StatusConflict {
		t.Fatalf("static conflict status = %d body=%s, want 409", staticConflict.Code, staticConflict.Body.String())
	}
	revisions, err := store.ListAgentRevisions(t.Context(), "local")
	committedMutations := 0
	for _, row := range revisions {
		if !row.LegacyBaseline {
			committedMutations++
		}
	}
	if err != nil || committedMutations != 1 {
		t.Fatalf("revisions after rejected writes = %+v, error=%v; want one mutation revision", revisions, err)
	}

	const concurrentBody = `{"frontend_url":"https://concurrent.example.com","backends":[{"url":"http://127.0.0.1:8082"}],"enabled":true}`
	concurrentResponses := make(chan *httptest.ResponseRecorder, 2)
	for range 2 {
		go func() {
			concurrentResponses <- performPanelMutation(
				t, router, http.MethodPost, "/panel-api/agents/local/rules", concurrentBody, "create-rule-concurrent",
			)
		}()
	}
	concurrentFirst := <-concurrentResponses
	concurrentSecond := <-concurrentResponses
	if concurrentFirst.Code != http.StatusAccepted || concurrentSecond.Code != http.StatusAccepted {
		t.Fatalf("concurrent statuses = %d/%d bodies=%s / %s", concurrentFirst.Code, concurrentSecond.Code, concurrentFirst.Body.String(), concurrentSecond.Body.String())
	}
	concurrentFirstPayload := decodeAcceptedMutation(t, concurrentFirst)
	concurrentSecondPayload := decodeAcceptedMutation(t, concurrentSecond)
	concurrentFirstRule := mutationResource(t, concurrentFirstPayload, "rule")
	concurrentSecondRule := mutationResource(t, concurrentSecondPayload, "rule")
	if concurrentFirstPayload.OperationID != concurrentSecondPayload.OperationID ||
		concurrentFirstRule["id"] != concurrentSecondRule["id"] || concurrentFirstRule["id"] == float64(0) {
		t.Fatalf("concurrent envelopes = %+v / %+v, rules=%+v / %+v", concurrentFirstPayload, concurrentSecondPayload, concurrentFirstRule, concurrentSecondRule)
	}

	ruleID := int(firstRule["id"].(float64))
	deletePath := "/panel-api/agents/local/rules/" + strconv.Itoa(ruleID)
	deleted := performPanelMutation(t, router, http.MethodDelete, deletePath, "", "delete-rule-1")
	if deleted.Code != http.StatusAccepted {
		t.Fatalf("first delete status = %d body=%s, want 202", deleted.Code, deleted.Body.String())
	}
	deletedPayload := decodeAcceptedMutation(t, deleted)
	deletedRule := mutationResource(t, deletedPayload, "rule")

	replayedDelete := performPanelMutation(t, router, http.MethodDelete, deletePath, "", "delete-rule-1")
	if replayedDelete.Code != http.StatusAccepted {
		t.Fatalf("replayed delete status = %d body=%s, want 202", replayedDelete.Code, replayedDelete.Body.String())
	}
	replayedDeletePayload := decodeAcceptedMutation(t, replayedDelete)
	if replayedDeletePayload.OperationID != deletedPayload.OperationID {
		t.Fatalf("replayed delete operation = %q, want %q", replayedDeletePayload.OperationID, deletedPayload.OperationID)
	}
	if got := mutationResource(t, replayedDeletePayload, "rule"); got["id"] != deletedRule["id"] {
		t.Fatalf("replayed deleted rule = %+v, first = %+v", got, deletedRule)
	}
}

func TestRemoteRevisionRoutesEnforceAgentTokenAndLeaseFencing(t *testing.T) {
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.PanelToken = "panel-secret"
	cfg.EnableLocalAgent = false
	cfg.LocalAgentID = "local"

	store, err := storage.NewSQLiteStore(cfg.DataDir, cfg.LocalAgentID)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	if err := store.SaveAgent(t.Context(), storage.AgentRow{
		ID: "edge-1", Name: "Edge 1", AgentToken: "edge-secret", Mode: "pull",
		CapabilitiesJSON: `["http_rules","cert_install"]`, LastApplyStatus: "success",
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	previousOpenConfiguredStore := openConfiguredStore
	openConfiguredStore = func(config.Config) (*storage.GormStore, error) { return store, nil }
	t.Cleanup(func() { openConfiguredStore = previousOpenConfiguredStore })
	router, err := NewRouter(Dependencies{Config: cfg})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}
	t.Cleanup(func() {
		if closer, ok := router.(interface{ Close() error }); ok {
			_ = closer.Close()
		}
	})

	created := performPanelMutation(t, router, http.MethodPost, "/panel-api/agents/edge-1/rules",
		`{"frontend_url":"http://remote.example.com","backends":[{"url":"http://127.0.0.1:8081"}],"enabled":true}`, "remote-create-1")
	if created.Code != http.StatusAccepted {
		t.Fatalf("remote rule create = %d body=%s", created.Code, created.Body.String())
	}

	unauthorized := httptest.NewRequest(http.MethodPost, "/panel-api/agent-revisions/pull", nil)
	unauthorizedResp := httptest.NewRecorder()
	router.ServeHTTP(unauthorizedResp, unauthorized)
	if unauthorizedResp.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized pull = %d body=%s", unauthorizedResp.Code, unauthorizedResp.Body.String())
	}

	pullReq := httptest.NewRequest(http.MethodPost, "/panel-api/agent-revisions/pull", nil)
	pullReq.Header.Set("X-Agent-Token", "edge-secret")
	pullResp := httptest.NewRecorder()
	router.ServeHTTP(pullResp, pullReq)
	if pullResp.Code != http.StatusOK {
		t.Fatalf("remote pull = %d body=%s", pullResp.Code, pullResp.Body.String())
	}
	var pullPayload struct {
		Revision service.RemoteRevisionPull `json:"revision"`
	}
	if err := json.Unmarshal(pullResp.Body.Bytes(), &pullPayload); err != nil {
		t.Fatalf("decode pull: %v", err)
	}
	lease := pullPayload.Revision.Lease
	if !pullPayload.Revision.HasUpdate || lease == nil || pullPayload.Revision.Snapshot == nil {
		t.Fatalf("remote pull payload = %+v", pullPayload.Revision)
	}

	start := service.RemoteRevisionStart{
		AgentID: "edge-1", Revision: lease.Revision, RetryCycle: lease.RetryCycle,
		Attempt: lease.Attempt, LeaseID: lease.LeaseID, GenerationID: "generation-1",
	}
	wrongAgent := start
	wrongAgent.AgentID = "edge-2"
	wrongAgentResp := performAgentJSON(t, router, "/panel-api/agent-revisions/"+strconv.FormatInt(lease.Revision, 10)+"/start", wrongAgent)
	if wrongAgentResp.Code != http.StatusForbidden {
		t.Fatalf("cross-agent start = %d body=%s, want 403", wrongAgentResp.Code, wrongAgentResp.Body.String())
	}
	missingGeneration := start
	missingGeneration.GenerationID = ""
	missingGenerationResp := performAgentJSON(t, router, "/panel-api/agent-revisions/"+strconv.FormatInt(lease.Revision, 10)+"/start", missingGeneration)
	if missingGenerationResp.Code != http.StatusBadRequest {
		t.Fatalf("missing-generation start = %d body=%s, want 400", missingGenerationResp.Code, missingGenerationResp.Body.String())
	}
	startResp := performAgentJSON(t, router, "/panel-api/agent-revisions/"+strconv.FormatInt(lease.Revision, 10)+"/start", start)
	if startResp.Code != http.StatusOK {
		t.Fatalf("remote start = %d body=%s", startResp.Code, startResp.Body.String())
	}
	report := service.RemoteRevisionReport{
		AgentID: "edge-1", Revision: lease.Revision, RetryCycle: lease.RetryCycle,
		Attempt: lease.Attempt, LeaseID: lease.LeaseID, GenerationID: "generation-1",
		Status: storage.AgentRevisionDrainStateDraining,
	}
	reportResp := performAgentJSON(t, router, "/panel-api/agent-revisions/"+strconv.FormatInt(lease.Revision, 10)+"/report", report)
	if reportResp.Code != http.StatusOK {
		t.Fatalf("remote applied report = %d body=%s", reportResp.Code, reportResp.Body.String())
	}

	report.Status = storage.AgentRevisionStateFailed
	report.ErrorCode = "stale"
	staleResp := performAgentJSON(t, router, "/panel-api/agent-revisions/"+strconv.FormatInt(lease.Revision, 10)+"/report", report)
	if staleResp.Code != http.StatusConflict {
		t.Fatalf("stale report = %d body=%s, want 409", staleResp.Code, staleResp.Body.String())
	}
	row, found, err := store.GetCoordinatorRevision(t.Context(), "edge-1", lease.Revision)
	if err != nil || !found || row.State != storage.AgentRevisionStateApplied {
		t.Fatalf("revision after stale report = %+v found=%v error=%v", row, found, err)
	}
	retryPath := "/panel-api/agents/edge-1/revisions/" + strconv.FormatInt(lease.Revision, 10) + "/retry"
	unauthorizedRetry := httptest.NewRequest(http.MethodPost, retryPath, nil)
	unauthorizedRetryResp := httptest.NewRecorder()
	router.ServeHTTP(unauthorizedRetryResp, unauthorizedRetry)
	if unauthorizedRetryResp.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized retry = %d body=%s", unauthorizedRetryResp.Code, unauthorizedRetryResp.Body.String())
	}
	retryReq := httptest.NewRequest(http.MethodPost, retryPath, nil)
	retryReq.Header.Set("X-Panel-Token", "panel-secret")
	retryResp := httptest.NewRecorder()
	router.ServeHTTP(retryResp, retryReq)
	if retryResp.Code != http.StatusConflict {
		t.Fatalf("retry applied revision = %d body=%s, want 409", retryResp.Code, retryResp.Body.String())
	}
	rollbackReq := httptest.NewRequest(http.MethodPost, "/panel-api/agents/edge-1/revisions/rollback", nil)
	rollbackReq.Header.Set("X-Panel-Token", "panel-secret")
	rollbackResp := httptest.NewRecorder()
	router.ServeHTTP(rollbackResp, rollbackReq)
	if rollbackResp.Code != http.StatusAccepted {
		t.Fatalf("rollback = %d body=%s, want 202", rollbackResp.Code, rollbackResp.Body.String())
	}
	rollbackPayload := decodeAcceptedMutation(t, rollbackResp)
	if rollbackPayload.OperationID == "" || rollbackPayload.DesiredRevision <= lease.Revision {
		t.Fatalf("rollback envelope = %+v", rollbackPayload)
	}
}

type acceptedMutationPayload struct {
	OperationID     string         `json:"operation_id"`
	AgentID         string         `json:"agent_id"`
	DesiredRevision int64          `json:"desired_revision"`
	ApplyStatus     string         `json:"apply_status"`
	StatusURL       string         `json:"status_url"`
	Replayed        bool           `json:"replayed"`
	Raw             map[string]any `json:"-"`
}

func performPanelMutation(t *testing.T, handler http.Handler, method, path, body, idempotencyKey string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("X-Panel-Token", "panel-secret")
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	return resp
}

func decodeAcceptedMutation(t *testing.T, resp *httptest.ResponseRecorder) acceptedMutationPayload {
	t.Helper()
	var raw map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode mutation response: %v body=%s", err, resp.Body.String())
	}
	payload := acceptedMutationPayload{Raw: raw}
	encoded, _ := json.Marshal(raw)
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatalf("decode accepted envelope: %v", err)
	}
	return payload
}

func mutationResource(t *testing.T, payload acceptedMutationPayload, field string) map[string]any {
	t.Helper()
	resource, ok := payload.Raw[field].(map[string]any)
	if !ok {
		t.Fatalf("%s resource = %#v", field, payload.Raw[field])
	}
	return resource
}

func performAgentJSON(t *testing.T, handler http.Handler, path string, payload any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(body)))
	req.Header.Set("X-Agent-Token", "edge-secret")
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	return resp
}
