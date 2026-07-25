package http

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/revision"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
)

const maxIdempotentMutationBodyBytes = 32 << 20

func (d Dependencies) withMutationContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, _ := revision.WithMutationContext(r.Context(), revision.MutationContextOptions{
			IdempotencyScope: service.PanelIdempotencyScope,
			IdempotencyKey:   r.Header.Get("Idempotency-Key"),
		})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (d Dependencies) writeMutationResource(
	w http.ResponseWriter,
	r *http.Request,
	legacyStatus int,
	resourceField string,
	resource any,
	extra map[string]any,
) {
	capture, captured := revision.MutationCaptureFromContext(r.Context())
	result, hasResult := capture.Result()
	if !captured || !hasResult {
		payload := clonePayload(extra)
		payload["ok"] = true
		if resourceField != "" {
			payload[resourceField] = resource
		}
		writeJSON(w, legacyStatus, payload)
		return
	}

	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if result.Replayed {
		field, replayResource, ok, err := decodeMutationReplayResource(result)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errorPayload("persisted mutation replay resource is invalid"))
			return
		}
		if ok {
			resourceField = field
			resource = replayResource
		}
		replayExtra, ok, err := decodeMutationReplayExtra(result)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errorPayload("persisted mutation replay response fields are invalid"))
			return
		}
		if ok {
			extra = replayExtra
		}
	}
	payload, statusURL := buildAcceptedMutationPayload(r, result, resourceField, resource, extra)

	if idempotencyKey != "" && d.RevisionService != nil {
		cached := cachedMutationPayload(payload, result)
		if encoded, err := json.Marshal(cached); err == nil {
			_ = d.RevisionService.SaveMutationResponse(
				r.Context(), service.PanelIdempotencyScope, idempotencyKey, result.Operation.ID, encoded,
			)
		}
	}
	if result.Replayed {
		w.Header().Set("Idempotency-Replayed", "true")
	}
	w.Header().Set("Location", statusURL)
	writeJSON(w, http.StatusAccepted, payload)
}

func buildAcceptedMutationPayload(
	r *http.Request,
	result revision.MutationResult,
	resourceField string,
	resource any,
	extra map[string]any,
) (map[string]any, string) {
	prefix := requestAPIPrefix(r)
	statusURL := prefix + "/operations/" + url.PathEscape(result.Operation.ID)
	agents := make([]map[string]any, 0, len(result.Agents))
	primaryRevision := int64(0)
	for _, agent := range result.Agents {
		applyStatus := result.Operation.Status
		if applyStatus == "" {
			applyStatus = "pending"
		}
		agentStatusURL := prefix + "/agents/" + url.PathEscape(agent.AgentID) + "/revisions/" + strconv.FormatInt(agent.DesiredRevision, 10)
		agents = append(agents, map[string]any{
			"agent_id": agent.AgentID, "desired_revision": agent.DesiredRevision,
			"apply_status": applyStatus, "status_url": agentStatusURL,
			"snapshot_digest": agent.SnapshotDigest, "no_op": agent.NoOp,
		})
		if agent.AgentID == result.Operation.PrimaryAgentID {
			primaryRevision = agent.DesiredRevision
		}
	}
	if primaryRevision == 0 && len(result.Agents) > 0 {
		primaryRevision = result.Agents[0].DesiredRevision
	}
	payload := clonePayload(extra)
	payload["ok"] = true
	payload["operation_id"] = result.Operation.ID
	payload["agent_id"] = result.Operation.PrimaryAgentID
	payload["desired_revision"] = primaryRevision
	payload["apply_status"] = result.Operation.Status
	payload["status_url"] = statusURL
	payload["no_op"] = result.NoOp
	payload["replayed"] = result.Replayed
	payload["agents"] = agents
	if resourceField != "" {
		payload[resourceField] = resource
	}
	return payload, statusURL
}

func cachedMutationPayload(payload map[string]any, result revision.MutationResult) map[string]any {
	cached := clonePayload(payload)
	cached["operation"] = result.Operation
	if result.HTTPRequestFingerprint != "" {
		cached["http_request_fingerprint"] = result.HTTPRequestFingerprint
	}
	if result.ReplayResourceField != "" && len(result.ReplayResource) > 0 {
		cached["replay_resource_field"] = result.ReplayResourceField
		cached["replay_resource"] = result.ReplayResource
	}
	if len(result.ReplayExtra) > 0 {
		cached["replay_extra"] = result.ReplayExtra
	}
	return cached
}

func decodeMutationReplayResource(result revision.MutationResult) (string, any, bool, error) {
	field := strings.TrimSpace(result.ReplayResourceField)
	if field == "" || len(result.ReplayResource) == 0 {
		return "", nil, false, nil
	}
	var resource any
	if err := json.Unmarshal(result.ReplayResource, &resource); err != nil {
		return "", nil, false, err
	}
	return field, resource, true, nil
}

func decodeMutationReplayExtra(result revision.MutationResult) (map[string]any, bool, error) {
	if len(result.ReplayExtra) == 0 {
		return nil, false, nil
	}
	var extra map[string]any
	if err := json.Unmarshal(result.ReplayExtra, &extra); err != nil {
		return nil, false, err
	}
	if extra == nil {
		extra = map[string]any{}
	}
	return extra, true, nil
}

func (d Dependencies) replayPanelMutation(w http.ResponseWriter, r *http.Request) bool {
	if !isMutationMethod(r.Method) {
		return false
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		return false
	}
	if len(key) > 256 {
		writeJSON(w, http.StatusBadRequest, errorPayload("Idempotency-Key is too long"))
		return true
	}
	fingerprint, err := panelMutationRequestFingerprint(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorPayload(err.Error()))
		return true
	}
	*r = *r.WithContext(revision.WithMutationHTTPRequestFingerprint(r.Context(), fingerprint))
	if d.RevisionService == nil {
		return false
	}
	payload, found, err := d.RevisionService.LoadMutationResponseByKey(
		r.Context(), service.PanelIdempotencyScope, key,
	)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorPayload("failed to load idempotency response"))
		return true
	}
	if !found {
		return false
	}
	storedFingerprint, _ := payload["http_request_fingerprint"].(string)
	if storedFingerprint == "" {
		return false
	}
	if storedFingerprint != fingerprint {
		writeJSON(w, http.StatusConflict, errorPayload("idempotency key was already used with a different request"))
		return true
	}
	if _, complete := payload["status_url"]; !complete {
		encoded, err := json.Marshal(payload)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errorPayload("persisted idempotency response is invalid"))
			return true
		}
		var result revision.MutationResult
		if err := json.Unmarshal(encoded, &result); err != nil || result.Operation.ID == "" {
			writeJSON(w, http.StatusInternalServerError, errorPayload("persisted idempotency response is invalid"))
			return true
		}
		field, resource, hasResource, err := decodeMutationReplayResource(result)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errorPayload("persisted mutation replay resource is invalid"))
			return true
		}
		extra, hasExtra, err := decodeMutationReplayExtra(result)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errorPayload("persisted mutation replay response fields are invalid"))
			return true
		}
		if !hasResource && !hasExtra {
			writeJSON(w, http.StatusInternalServerError, errorPayload("persisted mutation replay response is incomplete"))
			return true
		}
		result.Replayed = true
		response, statusURL := buildAcceptedMutationPayload(r, result, field, resource, extra)
		if cached, err := json.Marshal(cachedMutationPayload(response, result)); err == nil {
			_ = d.RevisionService.SaveMutationResponse(
				r.Context(), service.PanelIdempotencyScope, key, result.Operation.ID, cached,
			)
		}
		w.Header().Set("Idempotency-Replayed", "true")
		w.Header().Set("Location", statusURL)
		writeJSON(w, http.StatusAccepted, response)
		return true
	}
	delete(payload, "operation")
	delete(payload, "http_request_fingerprint")
	delete(payload, "replay_resource_field")
	delete(payload, "replay_resource")
	delete(payload, "replay_extra")
	payload["replayed"] = true
	w.Header().Set("Idempotency-Replayed", "true")
	if statusURL, _ := payload["status_url"].(string); statusURL != "" {
		w.Header().Set("Location", statusURL)
	}
	writeJSON(w, http.StatusAccepted, payload)
	return true
}

func panelMutationRequestFingerprint(r *http.Request) (string, error) {
	var body []byte
	if r.Body != nil {
		limited := io.LimitReader(r.Body, maxIdempotentMutationBodyBytes+1)
		var err error
		body, err = io.ReadAll(limited)
		if err != nil {
			return "", fmt.Errorf("failed to read idempotent request body")
		}
		if len(body) > maxIdempotentMutationBodyBytes {
			return "", fmt.Errorf("idempotent request body is too large")
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
	}
	canonicalBody := body
	contentTypeJSON := strings.Contains(strings.ToLower(r.Header.Get("Content-Type")), "json")
	if len(body) > 0 && (contentTypeJSON || json.Valid(body)) {
		if !json.Valid(body) {
			return "", fmt.Errorf("invalid JSON body")
		}
		var value any
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.UseNumber()
		if err := decoder.Decode(&value); err != nil {
			return "", fmt.Errorf("invalid JSON body")
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return "", fmt.Errorf("invalid JSON body")
		}
		canonicalBody = encoded
	}
	path := strings.TrimPrefix(r.URL.Path, requestAPIPrefix(r))
	digest := sha256.New()
	_, _ = digest.Write([]byte(strings.ToUpper(r.Method)))
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write([]byte(path))
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write([]byte(r.URL.Query().Encode()))
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write(canonicalBody)
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func isMutationMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func (d Dependencies) writeRevisionAccepted(w http.ResponseWriter, r *http.Request, status service.AgentRevisionStatus, extra map[string]any) {
	prefix := requestAPIPrefix(r)
	statusURL := prefix + "/operations/" + url.PathEscape(status.OperationID)
	payload := clonePayload(extra)
	payload["ok"] = true
	payload["operation_id"] = status.OperationID
	payload["agent_id"] = status.AgentID
	payload["desired_revision"] = status.DesiredRevision
	payload["apply_status"] = status.ApplyStatus
	payload["status_url"] = statusURL
	payload["agents"] = []service.AgentRevisionStatus{status}
	payload["replayed"] = status.Replayed
	d.persistActionResponse(r, status.OperationID, status.HTTPRequestFingerprint, payload)
	if status.Replayed {
		w.Header().Set("Idempotency-Replayed", "true")
	}
	w.Header().Set("Location", statusURL)
	writeJSON(w, http.StatusAccepted, payload)
}

func (d Dependencies) writeOperationAccepted(w http.ResponseWriter, r *http.Request, status service.OperationStatus, extra map[string]any) {
	prefix := requestAPIPrefix(r)
	statusURL := prefix + "/operations/" + url.PathEscape(status.OperationID)
	desiredRevision := int64(0)
	if len(status.Agents) > 0 {
		desiredRevision = status.Agents[0].DesiredRevision
		for _, agent := range status.Agents {
			if agent.AgentID == status.PrimaryAgent {
				desiredRevision = agent.DesiredRevision
				break
			}
		}
	}
	payload := clonePayload(extra)
	payload["ok"] = true
	payload["operation_id"] = status.OperationID
	payload["agent_id"] = status.PrimaryAgent
	payload["desired_revision"] = desiredRevision
	payload["apply_status"] = status.ApplyStatus
	payload["status_url"] = statusURL
	payload["no_op"] = status.NoOp
	payload["agents"] = status.Agents
	payload["replayed"] = status.Replayed
	d.persistActionResponse(r, status.OperationID, status.HTTPRequestFingerprint, payload)
	if status.Replayed {
		w.Header().Set("Idempotency-Replayed", "true")
	}
	w.Header().Set("Location", statusURL)
	writeJSON(w, http.StatusAccepted, payload)
}

func (d Dependencies) persistActionResponse(r *http.Request, operationID, fingerprint string, payload map[string]any) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" || d.RevisionService == nil {
		return
	}
	cached := clonePayload(payload)
	if fingerprint != "" {
		cached["http_request_fingerprint"] = fingerprint
	}
	encoded, err := json.Marshal(cached)
	if err != nil {
		return
	}
	_ = d.RevisionService.SaveMutationResponse(
		r.Context(), service.PanelIdempotencyScope, key, operationID, encoded,
	)
}

func clonePayload(input map[string]any) map[string]any {
	result := make(map[string]any, len(input)+8)
	for key, value := range input {
		result[key] = value
	}
	return result
}

func requestAPIPrefix(r *http.Request) string {
	if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/api" {
		return "/api"
	}
	return "/panel-api"
}

func (d Dependencies) handleOperationStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	if !d.requireRevisionService(w) {
		return
	}
	status, err := d.RevisionService.GetOperationStatus(r.Context(), r.PathValue("operationID"))
	if err != nil {
		d.writeRevisionError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "operation": status})
}

func (d Dependencies) handleOperationDismiss(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	if !d.requireRevisionService(w) {
		return
	}
	status, err := d.RevisionService.DismissOperation(r.Context(), r.PathValue("operationID"))
	if err != nil {
		d.writeRevisionError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "operation": status})
}

func (d Dependencies) handleAgentRevisionStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	if !d.requireRevisionService(w) {
		return
	}
	revisionNumber, ok := parseRevisionPath(w, r.PathValue("revision"))
	if !ok {
		return
	}
	status, err := d.RevisionService.GetAgentRevisionStatus(r.Context(), r.PathValue("agentID"), revisionNumber)
	if err != nil {
		d.writeRevisionError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "revision": status})
}

func (d Dependencies) handleRevisionRetry(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	if !d.requireRevisionService(w) {
		return
	}
	revisionNumber, ok := parseRevisionPath(w, r.PathValue("revision"))
	if !ok {
		return
	}
	status, err := d.RevisionService.Retry(r.Context(), r.PathValue("agentID"), revisionNumber)
	if err != nil {
		d.writeRevisionError(w, err)
		return
	}
	d.writeRevisionAccepted(w, r, status, nil)
}

func (d Dependencies) handleRevisionRollback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	if !d.requireRevisionService(w) {
		return
	}
	status, err := d.RevisionService.Rollback(r.Context(), r.PathValue("agentID"))
	if err != nil {
		d.writeRevisionError(w, err)
		return
	}
	d.writeOperationAccepted(w, r, status, nil)
}

func (d Dependencies) handleRevisionEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	if !d.requireRevisionService(w) {
		return
	}
	after, err := parseUintQuery(r, "after")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorPayload(err.Error()))
		return
	}
	limit, err := parseIntQuery(r, "limit")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorPayload(err.Error()))
		return
	}
	page, err := d.RevisionService.ListEvents(r.Context(), service.RevisionEventQuery{
		AfterID: after, Limit: limit, OperationID: r.URL.Query().Get("operation_id"),
		AgentID: r.URL.Query().Get("agent_id"),
	})
	if err != nil {
		d.writeRevisionError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "events": page.Events, "next_cursor": page.NextCursor, "has_more": page.HasMore,
	})
}

func (d Dependencies) handleRemoteRevisionPull(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	agent, ok := d.authenticateRevisionAgent(w, r)
	if !ok || !d.requireRevisionService(w) {
		return
	}
	result, err := d.RevisionService.PullRemoteRevision(r.Context(), agent.ID)
	if err != nil {
		d.writeRevisionError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "revision": result})
}

func (d Dependencies) handleRemoteRevisionStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	agent, ok := d.authenticateRevisionAgent(w, r)
	if !ok || !d.requireRevisionService(w) {
		return
	}
	revisionNumber, ok := parseRevisionPath(w, r.PathValue("revision"))
	if !ok {
		return
	}
	var input service.RemoteRevisionStart
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, errorPayload("invalid JSON body"))
		return
	}
	if input.Revision != revisionNumber {
		writeJSON(w, http.StatusBadRequest, errorPayload("revision in body must match path"))
		return
	}
	status, err := d.RevisionService.StartRemoteRevision(r.Context(), agent.ID, input)
	if err != nil {
		d.writeRevisionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "revision": status})
}

func (d Dependencies) handleRemoteRevisionReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	agent, ok := d.authenticateRevisionAgent(w, r)
	if !ok || !d.requireRevisionService(w) {
		return
	}
	revisionNumber, ok := parseRevisionPath(w, r.PathValue("revision"))
	if !ok {
		return
	}
	var input service.RemoteRevisionReport
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, errorPayload("invalid JSON body"))
		return
	}
	if input.Revision != revisionNumber {
		writeJSON(w, http.StatusBadRequest, errorPayload("revision in body must match path"))
		return
	}
	status, err := d.RevisionService.ReportRemoteRevision(r.Context(), agent.ID, input)
	if err != nil {
		d.writeRevisionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "revision": status})
}

func (d Dependencies) authenticateRevisionAgent(w http.ResponseWriter, r *http.Request) (service.AgentSummary, bool) {
	token := strings.TrimSpace(r.Header.Get("X-Agent-Token"))
	if token == "" {
		writeJSON(w, http.StatusUnauthorized, errorPayload("Unauthorized: missing agent token"))
		return service.AgentSummary{}, false
	}
	agent, err := d.AgentService.GetByToken(r.Context(), token)
	if err != nil {
		status, payload := mapServiceError(err)
		writeJSON(w, status, payload)
		return service.AgentSummary{}, false
	}
	return agent, true
}

func (d Dependencies) requireRevisionService(w http.ResponseWriter) bool {
	if d.RevisionService != nil {
		return true
	}
	writeJSON(w, http.StatusServiceUnavailable, errorPayload("revision service unavailable"))
	return false
}

func (d Dependencies) writeRevisionError(w http.ResponseWriter, err error) {
	status, payload := mapServiceError(err)
	writeJSON(w, status, payload)
}

func parseRevisionPath(w http.ResponseWriter, raw string) (int64, bool) {
	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || value < 0 {
		writeJSON(w, http.StatusBadRequest, errorPayload("invalid revision"))
		return 0, false
	}
	return value, true
}

func parseUintQuery(r *http.Request, name string) (uint64, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid %s cursor", name)
	}
	return value, nil
}

func parseIntQuery(r *http.Request, name string) (int, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("invalid %s", name)
	}
	return value, nil
}
