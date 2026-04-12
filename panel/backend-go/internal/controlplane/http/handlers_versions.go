package http

import (
	"encoding/json"
	"net/http"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
)

func (d Dependencies) handleVersionPolicies(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		policies, err := d.VersionPolicyService.List(r.Context())
		if err != nil {
			status, payload := mapServiceError(err)
			writeJSON(w, status, payload)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":       true,
			"policies": policies,
		})
	case http.MethodPost:
		var payload service.VersionPolicyInput
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeJSON(w, http.StatusBadRequest, errorPayload("invalid JSON body"))
			return
		}
		policy, err := d.VersionPolicyService.Create(r.Context(), payload)
		if err != nil {
			status, body := mapServiceError(err)
			writeJSON(w, status, body)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"ok":     true,
			"policy": policy,
		})
	default:
		http.NotFound(w, r)
	}
}

func (d Dependencies) handleVersionPolicy(w http.ResponseWriter, r *http.Request) {
	policyID := r.PathValue("id")
	if policyID == "" {
		writeJSON(w, http.StatusBadRequest, errorPayload("invalid policy id"))
		return
	}

	switch r.Method {
	case http.MethodPut:
		var payload service.VersionPolicyInput
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeJSON(w, http.StatusBadRequest, errorPayload("invalid JSON body"))
			return
		}
		policy, err := d.VersionPolicyService.Update(r.Context(), policyID, payload)
		if err != nil {
			status, body := mapServiceError(err)
			writeJSON(w, status, body)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":     true,
			"policy": policy,
		})
	case http.MethodDelete:
		policy, err := d.VersionPolicyService.Delete(r.Context(), policyID)
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
