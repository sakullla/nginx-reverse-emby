package http

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
)

func (d Dependencies) handleRegisterAgent(w http.ResponseWriter, r *http.Request) {
	var body map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, errorPayload("invalid JSON body"))
		return
	}
	var payload service.RegisterRequest
	if err := decodeRawMessageMap(body, &payload); err != nil {
		writeJSON(w, http.StatusBadRequest, errorPayload("invalid JSON body"))
		return
	}
	_, payload.HasCapabilities = body["capabilities"]
	// A CSR-bearing request uses the canonical one-time PKI token inside the
	// enrollment transaction. Legacy registration keeps the configured static
	// register-token check for compatibility; neither path adds a listener.
	if strings.TrimSpace(payload.TunnelCSRPEM) == "" && !d.isRegisterAuthorized(r, payload.RegisterToken) {
		if _, err := d.AgentService.GetByToken(r.Context(), r.Header.Get("X-Agent-Token")); err != nil {
			writeJSON(w, http.StatusUnauthorized, errorPayload("Unauthorized: Invalid or missing register token"))
			return
		}
	}

	agent, err := d.AgentService.Register(r.Context(), payload, r.Header.Get("X-Agent-Token"))
	if err != nil {
		status, body := mapServiceError(err)
		writeJSON(w, status, body)
		return
	}

	response := map[string]any{
		"ok":    true,
		"agent": redactAgentSummary(agent),
	}
	if agent.PKIRegistration != nil {
		response["pki"] = agent.PKIRegistration
	}
	writeJSON(w, http.StatusOK, response)
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
		"agents": redactAgentSummaries(agents),
	})
}

func (d Dependencies) handleAgent(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("agentID")

	switch r.Method {
	case http.MethodGet:
		agent, err := d.AgentService.Get(r.Context(), agentID)
		if err != nil {
			status, payload := mapServiceError(err)
			writeJSON(w, status, payload)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":    true,
			"agent": redactAgentSummary(agent),
		})
	case http.MethodPut, http.MethodPatch:
		var payload service.UpdateAgentRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeJSON(w, http.StatusBadRequest, errorPayload("invalid JSON body"))
			return
		}
		agent, err := d.AgentService.Update(r.Context(), agentID, payload)
		if err != nil {
			status, body := mapServiceError(err)
			writeJSON(w, status, body)
			return
		}
		d.writeMutationResource(w, r, http.StatusOK, "agent", redactAgentSummary(agent), nil)
	case http.MethodDelete:
		agent, err := d.AgentService.Delete(r.Context(), agentID)
		if err != nil {
			status, body := mapServiceError(err)
			writeJSON(w, status, body)
			return
		}
		d.writeMutationResource(w, r, http.StatusOK, "agent", redactAgentSummary(agent), nil)
	default:
		http.NotFound(w, r)
	}
}

func redactAgentSummaries(agents []service.AgentSummary) []service.AgentSummary {
	if agents == nil {
		return nil
	}
	out := make([]service.AgentSummary, len(agents))
	for i, agent := range agents {
		out[i] = redactAgentSummary(agent)
	}
	return out
}

// redactAgentSummary strips secrets from an agent summary before it leaves the
// process. Agent tokens never enter the summary (they live only on AgentRow),
// and the DDNS fields (domain/status/reported IPs) are non-sensitive display
// and runtime state — they carry no Cloudflare credential (R7), so they need no
// redaction here. Only the outbound proxy password is masked.
func redactAgentSummary(agent service.AgentSummary) service.AgentSummary {
	return service.RedactAgentSummary(agent)
}

func (d Dependencies) handleAgentStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}

	stats, err := d.AgentService.Stats(r.Context(), r.PathValue("agentID"))
	if err != nil {
		status, payload := mapServiceError(err)
		writeJSON(w, status, payload)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":    true,
		"stats": stats,
	})
}

func (d Dependencies) handleLocalStats(w http.ResponseWriter, r *http.Request) {
	r = r.Clone(r.Context())
	r.SetPathValue("agentID", d.Config.LocalAgentID)
	d.handleAgentStats(w, r)
}

func (d Dependencies) handleApplyAgent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}

	result, err := d.AgentService.Apply(r.Context(), r.PathValue("agentID"))
	if err != nil {
		status, payload := mapServiceError(err)
		writeJSON(w, status, payload)
		return
	}
	if d.RevisionService != nil {
		status, statusErr := d.RevisionService.GetAgentRevisionStatus(r.Context(), r.PathValue("agentID"), result.DesiredRevision)
		if statusErr != nil {
			d.writeRevisionError(w, statusErr)
			return
		}
		d.writeRevisionAccepted(w, r, status, map[string]any{"message": result.Message})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": result.Message})
}

func (d Dependencies) handleLocalApply(w http.ResponseWriter, r *http.Request) {
	r = r.Clone(r.Context())
	r.SetPathValue("agentID", d.Config.LocalAgentID)
	d.handleApplyAgent(w, r)
}
