package http

import "net/http"

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
