package http

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
)

func (d Dependencies) handleAgentTrafficPolicy(w http.ResponseWriter, r *http.Request) {
	if d.writeTrafficDisabledIfNeeded(w) {
		return
	}
	agentID := r.PathValue("agentID")
	switch r.Method {
	case http.MethodGet:
		policy, err := d.TrafficService.GetPolicy(r.Context(), agentID)
		if err != nil {
			status, payload := mapServiceError(err)
			writeJSON(w, status, payload)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":     true,
			"policy": policy,
		})
	case http.MethodPatch:
		var payload service.TrafficPolicy
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeJSON(w, http.StatusBadRequest, errorPayload("invalid JSON body"))
			return
		}
		policy, err := d.TrafficService.UpdatePolicy(r.Context(), agentID, payload)
		if err != nil {
			status, body := mapServiceError(err)
			writeJSON(w, status, body)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":     true,
			"policy": policy,
		})
	default:
		http.NotFound(w, r)
	}
}

func (d Dependencies) handleAgentTrafficSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	if d.writeTrafficDisabledIfNeeded(w) {
		return
	}
	summary, err := d.TrafficService.Summary(r.Context(), r.PathValue("agentID"))
	if err != nil {
		status, payload := mapServiceError(err)
		writeJSON(w, status, payload)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"summary": summary,
	})
}

func (d Dependencies) handleAgentTrafficTrend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	if d.writeTrafficDisabledIfNeeded(w) {
		return
	}
	query := r.URL.Query()
	granularity := query.Get("granularity")
	switch granularity {
	case "", "hour", "day", "month":
	default:
		status, payload := mapServiceError(fmt.Errorf("%w: unsupported traffic granularity %q", service.ErrInvalidArgument, granularity))
		writeJSON(w, status, payload)
		return
	}
	points, err := d.TrafficService.Trend(r.Context(), service.TrafficTrendQuery{
		AgentID:     r.PathValue("agentID"),
		Granularity: granularity,
		From:        query.Get("from"),
		To:          query.Get("to"),
		ScopeType:   query.Get("scope_type"),
		ScopeID:     query.Get("scope_id"),
	})
	if err != nil {
		status, payload := mapServiceError(err)
		writeJSON(w, status, payload)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"points": points,
	})
}

func (d Dependencies) handleAgentTrafficCalibration(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	if d.writeTrafficDisabledIfNeeded(w) {
		return
	}
	var payload service.TrafficCalibrationRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, errorPayload("invalid JSON body"))
		return
	}
	summary, err := d.TrafficService.Calibrate(r.Context(), r.PathValue("agentID"), payload)
	if err != nil {
		status, body := mapServiceError(err)
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"summary": summary,
	})
}

func (d Dependencies) handleAgentTrafficCleanup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	if d.writeTrafficDisabledIfNeeded(w) {
		return
	}
	result, err := d.TrafficService.Cleanup(r.Context(), r.PathValue("agentID"))
	if err != nil {
		status, payload := mapServiceError(err)
		writeJSON(w, status, payload)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"result": result,
	})
}

func (d Dependencies) writeTrafficDisabledIfNeeded(w http.ResponseWriter) bool {
	if d.Config.TrafficStatsEnabled {
		return false
	}
	writeJSON(w, http.StatusNotFound, trafficStatsDisabledPayload())
	return true
}

func (d Dependencies) handleTrafficOverview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	if d.writeTrafficDisabledIfNeeded(w) {
		return
	}
	agentFilter := r.URL.Query().Get("agent_id")
	granularity := r.URL.Query().Get("granularity")
	switch granularity {
	case "", "hour", "day", "month":
	default:
		status, payload := mapServiceError(fmt.Errorf("%w: unsupported traffic granularity %q", service.ErrInvalidArgument, granularity))
		writeJSON(w, status, payload)
		return
	}
	if granularity == "" {
		granularity = "day"
	}
	agents, err := d.AgentService.List(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorPayload("failed to list agents"))
		return
	}
	agentNames := make(map[string]string, len(agents))
	for _, a := range agents {
		agentNames[a.ID] = a.Name
	}
	result, err := d.TrafficService.Overview(r.Context(), agentFilter, granularity, agentNames)
	if err != nil {
		status, payload := mapServiceError(err)
		writeJSON(w, status, payload)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"agents":     result.Agents,
		"trend":      result.Trend,
		"host_trend": result.HostTrend,
	})
}
