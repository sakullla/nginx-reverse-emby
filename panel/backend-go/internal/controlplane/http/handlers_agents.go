package http

import (
	"encoding/json"
	"net/http"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
)

func (d Dependencies) handleRegisterAgent(w http.ResponseWriter, r *http.Request) {
	var payload service.RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, errorPayload("invalid JSON body"))
		return
	}
	if !d.isRegisterAuthorized(r, payload.RegisterToken) {
		writeJSON(w, http.StatusUnauthorized, errorPayload("Unauthorized: Invalid or missing register token"))
		return
	}

	agent, err := d.AgentService.Register(r.Context(), payload, r.Header.Get("X-Agent-Token"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorPayload(err.Error()))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":    true,
		"agent": agent,
	})
}

func (d Dependencies) handleAgents(w http.ResponseWriter, r *http.Request) {
	agents, err := d.AgentService.List(r.Context())
	if err != nil {
		status, payload := mapServiceError(err)
		writeJSON(w, status, payload)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"agents": agents,
	})
}
