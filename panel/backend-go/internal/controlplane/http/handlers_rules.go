package http

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
)

func (d Dependencies) handleAgentRules(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("agentID")

	switch r.Method {
	case http.MethodGet:
		rules, err := d.RuleService.List(r.Context(), agentID)
		if err != nil {
			status, payload := mapServiceError(err)
			writeJSON(w, status, payload)
			return
		}
		rules, err = d.filterHTTPRules(r.Context(), rules)
		if err != nil {
			writeAccessError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":    true,
			"rules": rules,
		})
	case http.MethodPost:
		var payload service.HTTPRuleInput
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeJSON(w, http.StatusBadRequest, errorPayload("invalid JSON body"))
			return
		}
		rule, err := d.RuleService.Create(r.Context(), agentID, payload)
		if err != nil {
			err = d.auditQuotaDenial(r, err, "agent", agentID)
			status, body := mapServiceError(err)
			writeJSON(w, status, body)
			return
		}
		d.writeMutationResource(w, r, http.StatusCreated, "rule", rule, nil)
	default:
		http.NotFound(w, r)
	}
}

func (d Dependencies) handleLocalRules(w http.ResponseWriter, r *http.Request) {
	r = r.Clone(r.Context())
	r.SetPathValue("agentID", d.Config.LocalAgentID)
	d.handleAgentRules(w, r)
}

func (d Dependencies) handleAgentRule(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("agentID")
	ruleID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || ruleID <= 0 {
		writeJSON(w, http.StatusBadRequest, errorPayload("invalid rule id"))
		return
	}

	switch r.Method {
	case http.MethodPut:
		var payload service.HTTPRuleInput
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeJSON(w, http.StatusBadRequest, errorPayload("invalid JSON body"))
			return
		}
		rule, err := d.RuleService.Update(r.Context(), agentID, ruleID, payload)
		if err != nil {
			status, body := mapServiceError(err)
			writeJSON(w, status, body)
			return
		}
		d.writeMutationResource(w, r, http.StatusOK, "rule", rule, nil)
	case http.MethodDelete:
		rule, err := d.RuleService.Delete(r.Context(), agentID, ruleID)
		if err != nil {
			status, body := mapServiceError(err)
			writeJSON(w, status, body)
			return
		}
		d.writeMutationResource(w, r, http.StatusOK, "rule", rule, nil)
	default:
		http.NotFound(w, r)
	}
}

func (d Dependencies) handleLocalRule(w http.ResponseWriter, r *http.Request) {
	r = r.Clone(r.Context())
	r.SetPathValue("agentID", d.Config.LocalAgentID)
	d.handleAgentRule(w, r)
}

func (d Dependencies) handleHTTPRulesList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	rules, meta, err := d.RuleService.ListPage(r.Context(), parseListQuery(r))
	if err != nil {
		status, payload := mapServiceError(err)
		writeJSON(w, status, payload)
		return
	}
	rules, err = d.filterHTTPRules(r.Context(), rules)
	if err != nil {
		writeAccessError(w, err)
		return
	}
	if d.accessFilteringActive(r.Context()) {
		meta.Total = len(rules)
	}
	writeListPageJSON(w, "rules", rules, meta)
}
