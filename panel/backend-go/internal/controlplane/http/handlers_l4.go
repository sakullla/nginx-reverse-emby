package http

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
)

func (d Dependencies) handleAgentL4Rules(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("agentID")

	switch r.Method {
	case http.MethodGet:
		rules, err := d.L4RuleService.List(r.Context(), agentID)
		if err != nil {
			status, payload := mapServiceError(err)
			writeJSON(w, status, payload)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":    true,
			"rules": redactL4Rules(rules),
		})
	case http.MethodPost:
		var payload service.L4RuleInput
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeJSON(w, http.StatusBadRequest, errorPayload("invalid JSON body"))
			return
		}
		rule, err := d.L4RuleService.Create(r.Context(), agentID, payload)
		if err != nil {
			status, body := mapServiceError(err)
			writeJSON(w, status, body)
			return
		}
		d.writeMutationResource(w, r, http.StatusCreated, "rule", redactL4Rule(rule), nil)
	default:
		http.NotFound(w, r)
	}
}

func (d Dependencies) handleAgentL4Rule(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("agentID")
	ruleID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || ruleID <= 0 {
		writeJSON(w, http.StatusBadRequest, errorPayload("invalid rule id"))
		return
	}

	switch r.Method {
	case http.MethodPut:
		var payload service.L4RuleInput
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeJSON(w, http.StatusBadRequest, errorPayload("invalid JSON body"))
			return
		}
		rule, err := d.L4RuleService.Update(r.Context(), agentID, ruleID, payload)
		if err != nil {
			status, body := mapServiceError(err)
			writeJSON(w, status, body)
			return
		}
		d.writeMutationResource(w, r, http.StatusOK, "rule", redactL4Rule(rule), nil)
	case http.MethodDelete:
		rule, err := d.L4RuleService.Delete(r.Context(), agentID, ruleID)
		if err != nil {
			status, body := mapServiceError(err)
			writeJSON(w, status, body)
			return
		}
		d.writeMutationResource(w, r, http.StatusOK, "rule", redactL4Rule(rule), nil)
	default:
		http.NotFound(w, r)
	}
}

func redactL4Rules(rules []service.L4Rule) []service.L4Rule {
	if rules == nil {
		return nil
	}
	out := make([]service.L4Rule, len(rules))
	for i, rule := range rules {
		out[i] = redactL4Rule(rule)
	}
	return out
}

func redactL4Rule(rule service.L4Rule) service.L4Rule {
	rule.ProxyEntryAuth.Password = ""
	return rule
}

func (d Dependencies) handleL4RulesList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	rules, meta, err := d.L4RuleService.ListPage(r.Context(), parseListQuery(r))
	if err != nil {
		status, payload := mapServiceError(err)
		writeJSON(w, status, payload)
		return
	}
	writeListPageJSON(w, "rules", redactL4Rules(rules), meta)
}
