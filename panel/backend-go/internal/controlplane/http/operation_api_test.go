package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/revision"
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

	profileCreated := performPanelMutation(t, router, http.MethodPost, "/panel-api/egress-profiles",
		`{"name":"accepted-direct","type":"direct","enabled":true}`, "create-egress-1")
	if profileCreated.Code != http.StatusAccepted {
		t.Fatalf("egress create status = %d body=%s, want 202", profileCreated.Code, profileCreated.Body.String())
	}
	profilePayload := decodeAcceptedMutation(t, profileCreated)
	profile := mutationResource(t, profilePayload, "profile")
	profileID := int(profile["id"].(float64))
	profileDeleted := performPanelMutation(t, router, http.MethodDelete,
		"/panel-api/egress-profiles/"+strconv.Itoa(profileID), "", "delete-egress-1")
	if profileDeleted.Code != http.StatusAccepted {
		t.Fatalf("egress delete status = %d body=%s, want 202", profileDeleted.Code, profileDeleted.Body.String())
	}
	if got := mutationResource(t, decodeAcceptedMutation(t, profileDeleted), "profile"); got["id"] != profile["id"] {
		t.Fatalf("deleted egress profile = %+v, created = %+v", got, profile)
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

func TestRevisionRoutesAndStatusURLsUseBothAPIPrefixes(t *testing.T) {
	for _, prefix := range []string{"/panel-api", "/api"} {
		t.Run(strings.TrimPrefix(prefix, "/"), func(t *testing.T) {
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

			body := `{"frontend_url":"https://` + strings.TrimPrefix(prefix, "/") + `.prefix.example.com","backends":[{"url":"http://127.0.0.1:8081"}],"enabled":true}`
			created := performPanelMutation(t, router, http.MethodPost, prefix+"/agents/local/rules", body, "prefix-create")
			if created.Code != http.StatusAccepted {
				t.Fatalf("create status = %d body=%s, want 202", created.Code, created.Body.String())
			}
			accepted := decodeAcceptedMutation(t, created)
			if !strings.HasPrefix(accepted.StatusURL, prefix+"/operations/") {
				t.Fatalf("status_url = %q, want prefix %q", accepted.StatusURL, prefix+"/operations/")
			}

			for name, path := range map[string]string{
				"operation": accepted.StatusURL,
				"revision":  prefix + "/agents/local/revisions/" + strconv.FormatInt(accepted.DesiredRevision, 10),
				"events":    prefix + "/revision-events?operation_id=" + accepted.OperationID,
			} {
				req := httptest.NewRequest(http.MethodGet, path, nil)
				req.Header.Set("X-Panel-Token", "panel-secret")
				resp := httptest.NewRecorder()
				router.ServeHTTP(resp, req)
				if resp.Code != http.StatusOK {
					t.Fatalf("%s route status = %d body=%s", name, resp.Code, resp.Body.String())
				}
			}

			for _, path := range []string{
				prefix + "/agent-revisions/pull",
				prefix + "/agent-revisions/1/start",
				prefix + "/agent-revisions/1/report",
			} {
				req := httptest.NewRequest(http.MethodPost, path, nil)
				resp := httptest.NewRecorder()
				router.ServeHTTP(resp, req)
				if resp.Code != http.StatusUnauthorized {
					t.Fatalf("unauthenticated remote route %q status = %d body=%s, want 401", path, resp.Code, resp.Body.String())
				}
			}
		})
	}
}

func TestPersistedMutationReplayExtraPreservesTopLevelResponseShape(t *testing.T) {
	result := revision.MutationResult{
		Operation: storage.OperationRow{ID: "operation-import", Status: storage.OperationStatusPending},
		Agents:    []revision.AgentMutationResult{{AgentID: "local", DesiredRevision: 7}},
		ReplayExtra: json.RawMessage(`{
			"manifest":{"package_version":1},
			"summary":{"imported":{"agents":1}},
			"report":{"imported":[{"kind":"agent","key":"edge-a"}]}
		}`),
		Replayed: true,
	}
	extra, found, err := decodeMutationReplayExtra(result)
	if err != nil || !found {
		t.Fatalf("decodeMutationReplayExtra() found=%v error=%v", found, err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/system/backup/import", nil)
	payload, statusURL := buildAcceptedMutationPayload(req, result, "", nil, extra)
	if statusURL != "/api/operations/operation-import" {
		t.Fatalf("status URL = %q", statusURL)
	}
	for _, field := range []string{"manifest", "summary", "report"} {
		if _, ok := payload[field]; !ok {
			t.Fatalf("replayed payload missing top-level %q: %+v", field, payload)
		}
	}
	if _, nested := payload["result"]; nested {
		t.Fatalf("replayed payload unexpectedly nested import result: %+v", payload)
	}
}

func TestMutationReplaySurvivesCommittedResponseEnvelopeGapAndRestart(t *testing.T) {
	cfg := config.Default()
	dataDir := t.TempDir()
	cfg.DataDir = dataDir
	cfg.PanelToken = "panel-secret"
	cfg.EnableLocalAgent = true
	cfg.LocalAgentID = "local"
	cfg.LocalAgentName = "Local"

	activeStore, err := storage.NewSQLiteStore(dataDir, cfg.LocalAgentID)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	previousOpenConfiguredStore := openConfiguredStore
	openConfiguredStore = func(config.Config) (*storage.GormStore, error) { return activeStore, nil }
	t.Cleanup(func() { openConfiguredStore = previousOpenConfiguredStore })

	router, err := NewRouter(Dependencies{Config: cfg})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}

	const createBody = `{"frontend_url":"https://restart-replay.example.com","backends":[{"url":"http://127.0.0.1:8081"}],"enabled":true}`
	createdResponse := performPanelMutation(t, router, http.MethodPost, "/panel-api/agents/local/rules", createBody, "restart-create")
	if createdResponse.Code != http.StatusAccepted {
		t.Fatalf("create status = %d body=%s", createdResponse.Code, createdResponse.Body.String())
	}
	created := decodeAcceptedMutation(t, createdResponse)
	createdRule := mutationResource(t, created, "rule")
	ruleID := int(createdRule["id"].(float64))
	rulePath := "/panel-api/agents/local/rules/" + strconv.Itoa(ruleID)

	const updateBody = `{"frontend_url":"https://restart-replay.example.com","backends":[{"url":"http://127.0.0.1:8082"}],"enabled":true}`
	updatedResponse := performPanelMutation(t, router, http.MethodPut, rulePath, updateBody, "restart-update")
	if updatedResponse.Code != http.StatusAccepted {
		t.Fatalf("update status = %d body=%s", updatedResponse.Code, updatedResponse.Body.String())
	}
	updated := decodeAcceptedMutation(t, updatedResponse)
	updatedRule := mutationResource(t, updated, "rule")

	deletedResponse := performPanelMutation(t, router, http.MethodDelete, rulePath, "", "restart-delete")
	if deletedResponse.Code != http.StatusAccepted {
		t.Fatalf("delete status = %d body=%s", deletedResponse.Code, deletedResponse.Body.String())
	}
	deleted := decodeAcceptedMutation(t, deletedResponse)
	deletedRule := mutationResource(t, deleted, "rule")

	for _, key := range []string{"restart-create", "restart-update", "restart-delete"} {
		simulateCommittedMutationBeforeEnvelopePersistence(t, activeStore, key)
	}
	if closer, ok := router.(interface{ Close() error }); ok {
		if err := closer.Close(); err != nil {
			t.Fatalf("close first router: %v", err)
		}
	}

	activeStore, err = storage.NewSQLiteStore(dataDir, cfg.LocalAgentID)
	if err != nil {
		t.Fatalf("reopen SQLite store: %v", err)
	}
	restarted, err := NewRouter(Dependencies{Config: cfg})
	if err != nil {
		t.Fatalf("NewRouter(restarted) error = %v", err)
	}
	t.Cleanup(func() {
		if closer, ok := restarted.(interface{ Close() error }); ok {
			_ = closer.Close()
		}
	})

	tests := []struct {
		name     string
		method   string
		path     string
		body     string
		key      string
		original acceptedMutationPayload
		resource map[string]any
	}{
		{name: "create", method: http.MethodPost, path: "/panel-api/agents/local/rules", body: createBody, key: "restart-create", original: created, resource: createdRule},
		{name: "update", method: http.MethodPut, path: rulePath, body: updateBody, key: "restart-update", original: updated, resource: updatedRule},
		{name: "delete", method: http.MethodDelete, path: rulePath, key: "restart-delete", original: deleted, resource: deletedRule},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			response := performPanelMutation(t, restarted, tc.method, tc.path, tc.body, tc.key)
			if response.Code != http.StatusAccepted {
				t.Fatalf("replay status = %d body=%s, want 202", response.Code, response.Body.String())
			}
			replayed := decodeAcceptedMutation(t, response)
			if replayed.OperationID != tc.original.OperationID || !replayed.Replayed {
				t.Fatalf("replayed envelope = %+v, original = %+v", replayed, tc.original)
			}
			if got := mutationResource(t, replayed, "rule"); !reflect.DeepEqual(got, tc.resource) {
				t.Fatalf("replayed resource = %#v, want %#v", got, tc.resource)
			}
		})
	}
}

func simulateCommittedMutationBeforeEnvelopePersistence(t *testing.T, store *storage.GormStore, key string) {
	t.Helper()
	record, found, err := store.GetIdempotencyRecord(t.Context(), service.PanelIdempotencyScope, key)
	if err != nil || !found {
		t.Fatalf("GetIdempotencyRecord(%q) found=%v error=%v", key, found, err)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(record.ResponseJSON), &envelope); err != nil {
		t.Fatalf("decode idempotency envelope %q: %v", key, err)
	}
	committed := make(map[string]json.RawMessage)
	for _, field := range []string{
		"operation", "agents", "no_op", "http_request_fingerprint",
		"replay_resource", "replay_resource_field", "replay_extra",
	} {
		if value, ok := envelope[field]; ok {
			committed[field] = value
		}
	}
	encoded, err := json.Marshal(committed)
	if err != nil {
		t.Fatalf("encode committed mutation response %q: %v", key, err)
	}
	var operation storage.OperationRow
	if err := json.Unmarshal(committed["operation"], &operation); err != nil || operation.ID == "" {
		t.Fatalf("decode committed operation %q: %+v error=%v", key, operation, err)
	}
	updated, err := store.UpdateIdempotencyResponseJSON(
		t.Context(), service.PanelIdempotencyScope, key, operation.ID, string(encoded),
	)
	if err != nil || !updated {
		t.Fatalf("UpdateIdempotencyResponseJSON(%q) updated=%v error=%v", key, updated, err)
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

	seedFailedRevisionForHTTP(t, store, "edge-failed", 1)
	rollbackPath := "/panel-api/agents/edge-1/revisions/rollback"
	rollbackResponses := make(chan *httptest.ResponseRecorder, 2)
	startRollback := make(chan struct{})
	for range 2 {
		go func() {
			<-startRollback
			rollbackResponses <- performPanelMutation(t, router, http.MethodPost, rollbackPath, "", "rollback-edge-1")
		}()
	}
	close(startRollback)
	rollbackFirst := <-rollbackResponses
	rollbackSecond := <-rollbackResponses
	if rollbackFirst.Code != http.StatusAccepted || rollbackSecond.Code != http.StatusAccepted {
		t.Fatalf("concurrent rollback statuses = %d/%d bodies=%s / %s, want 202/202",
			rollbackFirst.Code, rollbackSecond.Code, rollbackFirst.Body.String(), rollbackSecond.Body.String())
	}
	rollbackPayload := decodeAcceptedMutation(t, rollbackFirst)
	rollbackSecondPayload := decodeAcceptedMutation(t, rollbackSecond)
	if rollbackPayload.OperationID == "" || rollbackPayload.DesiredRevision <= lease.Revision {
		t.Fatalf("rollback envelope = %+v", rollbackPayload)
	}
	if rollbackSecondPayload.OperationID != rollbackPayload.OperationID ||
		rollbackSecondPayload.DesiredRevision != rollbackPayload.DesiredRevision {
		t.Fatalf("concurrent rollback envelopes = %+v / %+v", rollbackPayload, rollbackSecondPayload)
	}
	simulateCommittedActionBeforeEnvelopePersistence(t, store, "rollback-edge-1")

	retryPath = "/panel-api/agents/edge-failed/revisions/1/retry"
	differentAction := performPanelMutation(t, router, http.MethodPost, retryPath, "", "rollback-edge-1")
	if differentAction.Code != http.StatusConflict || !strings.Contains(differentAction.Body.String(), "different request") {
		t.Fatalf("same-key different-action status = %d body=%s, want idempotency 409", differentAction.Code, differentAction.Body.String())
	}
	failedBeforeRetry, found, err := store.GetCoordinatorRevision(t.Context(), "edge-failed", 1)
	if err != nil || !found || failedBeforeRetry.RetryCycle != 0 || failedBeforeRetry.State != storage.AgentRevisionStateFailed {
		t.Fatalf("failed revision after rejected different action = %+v found=%v error=%v", failedBeforeRetry, found, err)
	}

	replayedRollback := performPanelMutation(t, router, http.MethodPost, rollbackPath, "", "rollback-edge-1")
	if replayedRollback.Code != http.StatusAccepted {
		t.Fatalf("replayed rollback = %d body=%s, want 202", replayedRollback.Code, replayedRollback.Body.String())
	}
	replayedRollbackPayload := decodeAcceptedMutation(t, replayedRollback)
	if replayedRollbackPayload.OperationID != rollbackPayload.OperationID ||
		replayedRollbackPayload.DesiredRevision != rollbackPayload.DesiredRevision {
		t.Fatalf("replayed rollback = %+v, first = %+v", replayedRollbackPayload, rollbackPayload)
	}

	retryResponses := make(chan *httptest.ResponseRecorder, 2)
	startRetry := make(chan struct{})
	for range 2 {
		go func() {
			<-startRetry
			retryResponses <- performPanelMutation(t, router, http.MethodPost, retryPath, "", "retry-edge-failed")
		}()
	}
	close(startRetry)
	firstRetry := <-retryResponses
	secondRetry := <-retryResponses
	if firstRetry.Code != http.StatusAccepted || secondRetry.Code != http.StatusAccepted {
		t.Fatalf("concurrent retry statuses = %d/%d bodies=%s / %s, want 202/202", firstRetry.Code, secondRetry.Code, firstRetry.Body.String(), secondRetry.Body.String())
	}
	firstRetryPayload := decodeAcceptedMutation(t, firstRetry)
	secondRetryPayload := decodeAcceptedMutation(t, secondRetry)
	if firstRetryPayload.OperationID != secondRetryPayload.OperationID ||
		firstRetryPayload.DesiredRevision != secondRetryPayload.DesiredRevision {
		t.Fatalf("concurrent retry envelopes = %+v / %+v", firstRetryPayload, secondRetryPayload)
	}

	simulateCommittedActionBeforeEnvelopePersistence(t, store, "retry-edge-failed")
	replayedRetry := performPanelMutation(t, router, http.MethodPost, retryPath, "", "retry-edge-failed")
	if replayedRetry.Code != http.StatusAccepted {
		t.Fatalf("replayed retry status = %d body=%s, want 202", replayedRetry.Code, replayedRetry.Body.String())
	}
	replayedRetryPayload := decodeAcceptedMutation(t, replayedRetry)
	if firstRetryPayload.OperationID != replayedRetryPayload.OperationID ||
		firstRetryPayload.DesiredRevision != replayedRetryPayload.DesiredRevision {
		t.Fatalf("replayed retry envelope = %+v, first = %+v", replayedRetryPayload, firstRetryPayload)
	}
	retriedRow, found, err := store.GetCoordinatorRevision(t.Context(), "edge-failed", 1)
	if err != nil || !found || retriedRow.RetryCycle != 1 || retriedRow.State != storage.AgentRevisionStatePending {
		t.Fatalf("retried revision = %+v found=%v error=%v", retriedRow, found, err)
	}
}

func simulateCommittedActionBeforeEnvelopePersistence(t *testing.T, store *storage.GormStore, key string) {
	t.Helper()
	record, found, err := store.GetIdempotencyRecord(t.Context(), service.PanelIdempotencyScope, key)
	if err != nil || !found {
		t.Fatalf("GetIdempotencyRecord(%q) found=%v error=%v", key, found, err)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(record.ResponseJSON), &envelope); err != nil {
		t.Fatalf("decode action idempotency envelope %q: %v", key, err)
	}
	committed := make(map[string]json.RawMessage)
	for _, field := range []string{
		"operation_id", "agent_id", "desired_revision", "apply_status", "http_request_fingerprint",
	} {
		if value, ok := envelope[field]; ok {
			committed[field] = value
		}
	}
	encoded, err := json.Marshal(committed)
	if err != nil {
		t.Fatalf("encode committed action response %q: %v", key, err)
	}
	var operationID string
	if err := json.Unmarshal(committed["operation_id"], &operationID); err != nil || operationID == "" {
		t.Fatalf("decode committed action operation %q: %q error=%v", key, operationID, err)
	}
	updated, err := store.UpdateIdempotencyResponseJSON(
		t.Context(), service.PanelIdempotencyScope, key, operationID, string(encoded),
	)
	if err != nil || !updated {
		t.Fatalf("UpdateIdempotencyResponseJSON(%q) updated=%v error=%v", key, updated, err)
	}
}

func seedFailedRevisionForHTTP(t *testing.T, store *storage.GormStore, agentID string, revisionNumber int64) {
	t.Helper()
	now := time.Now().UTC()
	if err := store.SaveAgent(t.Context(), storage.AgentRow{
		ID: agentID, Name: agentID, AgentToken: agentID + "-token", Mode: "pull",
		CapabilitiesJSON: `["http_rules"]`, LastApplyStatus: "failed",
	}); err != nil {
		t.Fatalf("SaveAgent(%q) error = %v", agentID, err)
	}
	payload, digest, err := revision.CanonicalSnapshotPayload(storage.Snapshot{Revision: revisionNumber})
	if err != nil {
		t.Fatalf("CanonicalSnapshotPayload() error = %v", err)
	}
	artifactID := "snapshot-" + digest
	operationID := "operation-failed-" + agentID
	failedAt := now
	if err := store.CreateRevisionLedger(t.Context(), storage.RevisionLedgerWrite{
		Operation: storage.OperationRow{
			ID: operationID, Kind: "test.failed", Status: storage.OperationStatusFailed,
			PrimaryAgentID: agentID, CreatedAt: now, UpdatedAt: now,
		},
		Artifacts: []storage.GenerationArtifactRow{{
			ID: artifactID, Kind: "agent_snapshot", SHA256: digest,
			Payload: payload, SizeBytes: int64(len(payload)), CreatedAt: now,
		}},
		Revisions: []storage.AgentRevisionRow{{
			AgentID: agentID, Revision: revisionNumber, OperationID: operationID,
			State: storage.AgentRevisionStateFailed, SnapshotArtifactID: artifactID, SnapshotDigest: digest,
			ApplyTimeoutSeconds: 60, DrainTimeoutSeconds: 600,
			ErrorCode: "exhausted", ErrorMessage: "test failure", CreatedAt: now, UpdatedAt: now, FailedAt: &failedAt,
		}},
		Pointers: []storage.AgentRevisionPointerRow{{
			AgentID: agentID, DesiredRevision: revisionNumber, UpdatedAt: now,
		}},
		ArtifactRefs: []storage.AgentRevisionArtifactRow{{
			AgentID: agentID, Revision: revisionNumber, ArtifactID: artifactID,
			Role: "snapshot", CreatedAt: now,
		}},
	}); err != nil {
		t.Fatalf("CreateRevisionLedger(failed) error = %v", err)
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
