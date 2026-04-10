package http

import "net/http"

func (d Dependencies) handleAgentRules(w http.ResponseWriter, r *http.Request) {
	rules, err := d.AgentService.ListHTTPRules(r.Context(), r.PathValue("agentID"))
	if err != nil {
		status, payload := mapServiceError(err)
		writeJSON(w, status, payload)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":    true,
		"rules": rules,
	})
}
