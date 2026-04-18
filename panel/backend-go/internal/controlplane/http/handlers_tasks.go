package http

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
)

func (d Dependencies) handleAgentRuleDiagnose(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}

	agentID := r.PathValue("agentID")
	ruleID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || ruleID <= 0 {
		writeJSON(w, http.StatusBadRequest, errorPayload("invalid rule id"))
		return
	}

	if _, err := d.RuleService.Get(r.Context(), agentID, ruleID); err != nil {
		status, body := mapServiceError(err)
		writeJSON(w, status, body)
		return
	}

	task, err := d.TaskService.CreateAndDispatch(service.TaskCreateRequest{
		AgentID: agentID,
		Type:    service.TaskTypeDiagnoseHTTPRule,
		Payload: map[string]any{
			"rule_id":   ruleID,
			"rule_kind": "http",
		},
	})
	if err != nil {
		status, body := mapServiceError(err)
		writeJSON(w, status, body)
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]any{
		"ok":      true,
		"task_id": task.ID,
		"task":    task,
	})
}

func (d Dependencies) handleAgentL4RuleDiagnose(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}

	agentID := r.PathValue("agentID")
	ruleID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || ruleID <= 0 {
		writeJSON(w, http.StatusBadRequest, errorPayload("invalid rule id"))
		return
	}

	rule, err := d.L4RuleService.Get(r.Context(), agentID, ruleID)
	if err != nil {
		status, body := mapServiceError(err)
		writeJSON(w, status, body)
		return
	}
	if !strings.EqualFold(strings.TrimSpace(rule.Protocol), "tcp") {
		writeJSON(w, http.StatusBadRequest, errorPayload("only tcp l4 rules support diagnostics"))
		return
	}

	task, err := d.TaskService.CreateAndDispatch(service.TaskCreateRequest{
		AgentID: agentID,
		Type:    service.TaskTypeDiagnoseL4TCPRule,
		Payload: map[string]any{
			"rule_id":   ruleID,
			"rule_kind": "l4_tcp",
		},
	})
	if err != nil {
		status, body := mapServiceError(err)
		writeJSON(w, status, body)
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]any{
		"ok":      true,
		"task_id": task.ID,
		"task":    task,
	})
}

func (d Dependencies) handleAgentTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}

	task, err := d.TaskService.Get(r.Context(), r.PathValue("agentID"), r.PathValue("taskID"))
	if err != nil {
		status, body := mapServiceError(err)
		writeJSON(w, status, body)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"task": task,
	})
}

func (d Dependencies) handleAgentTaskUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}

	agent, ok := d.authenticateAgentRequest(w, r)
	if !ok {
		return
	}

	var payload struct {
		State  string         `json:"state"`
		Result map[string]any `json:"result"`
		Error  string         `json:"error"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, errorPayload("invalid JSON body"))
		return
	}

	err := d.TaskService.ApplyUpdate(r.Context(), service.TaskUpdateInput{
		AgentID: agent.ID,
		TaskID:  r.PathValue("taskID"),
		State:   strings.TrimSpace(payload.State),
		Result:  payload.Result,
		Error:   strings.TrimSpace(payload.Error),
	})
	if err != nil {
		status, body := mapServiceError(err)
		writeJSON(w, status, body)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (d Dependencies) handleAgentTaskSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	agent, ok := d.authenticateAgentRequest(w, r)
	if !ok {
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, errorPayload("streaming unsupported"))
		return
	}

	session := newSSETaskSession(w, flusher)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	if err := d.TaskService.RegisterSession(service.TaskSessionRegistration{
		AgentID:    agent.ID,
		SessionID:  strings.TrimSpace(r.URL.Query().Get("session_id")),
		Session:    session,
		RemoteAddr: remoteIPFromRequest(r),
	}); err != nil {
		status, body := mapServiceError(err)
		writeJSON(w, status, body)
		return
	}
	defer session.Close()

	fmt.Fprintf(w, ": task-session-open %s\n\n", time.Now().UTC().Format(time.RFC3339))
	flusher.Flush()

	<-r.Context().Done()
}

func (d Dependencies) authenticateAgentRequest(w http.ResponseWriter, r *http.Request) (service.AgentSummary, bool) {
	agentToken := strings.TrimSpace(r.Header.Get("X-Agent-Token"))
	if agentToken == "" {
		writeJSON(w, http.StatusUnauthorized, errorPayload("Unauthorized: missing agent token"))
		return service.AgentSummary{}, false
	}

	agent, err := d.AgentService.GetByToken(r.Context(), agentToken)
	if err != nil {
		status, body := mapServiceError(err)
		writeJSON(w, status, body)
		return service.AgentSummary{}, false
	}
	return agent, true
}

type sseTaskSession struct {
	writer  http.ResponseWriter
	flusher http.Flusher
	closed  bool
}

func newSSETaskSession(writer http.ResponseWriter, flusher http.Flusher) *sseTaskSession {
	return &sseTaskSession{
		writer:  writer,
		flusher: flusher,
	}
}

func (s *sseTaskSession) SendTask(task service.TaskEnvelope) error {
	if s.closed {
		return fmt.Errorf("%w: session closed", service.ErrInvalidArgument)
	}
	payload, err := json.Marshal(map[string]any{
		"task_id":    task.ID,
		"task_type":  task.Type,
		"payload":    task.Payload,
		"deadline":   task.Deadline.UTC().Format(time.RFC3339),
		"created_at": task.CreatedAt.UTC().Format(time.RFC3339),
	})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(s.writer, "event: task\ndata: %s\n\n", payload)
	if err != nil {
		return err
	}
	s.flusher.Flush()
	return nil
}

func (s *sseTaskSession) Close() error {
	s.closed = true
	return nil
}
