package http

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
)

func (d Dependencies) handleWireGuardProfiles(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("agentID")

	switch r.Method {
	case http.MethodGet:
		profiles, err := d.WireGuardProfileService.List(r.Context(), agentID)
		if err != nil {
			status, payload := mapServiceError(err)
			writeJSON(w, status, payload)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":       true,
			"profiles": profiles,
		})
	case http.MethodPost:
		var payload service.WireGuardProfileInput
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeJSON(w, http.StatusBadRequest, errorPayload("invalid JSON body"))
			return
		}
		profile, err := d.WireGuardProfileService.Create(r.Context(), agentID, payload)
		if err != nil {
			status, body := mapServiceError(err)
			writeJSON(w, status, body)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"ok":      true,
			"profile": profile,
		})
	default:
		http.NotFound(w, r)
	}
}

func (d Dependencies) handleWireGuardProfile(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("agentID")
	profileID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || profileID <= 0 {
		writeJSON(w, http.StatusBadRequest, errorPayload("invalid wireguard profile id"))
		return
	}

	switch r.Method {
	case http.MethodPut:
		var payload service.WireGuardProfileInput
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeJSON(w, http.StatusBadRequest, errorPayload("invalid JSON body"))
			return
		}
		profile, err := d.WireGuardProfileService.Update(r.Context(), agentID, profileID, payload)
		if err != nil {
			status, body := mapServiceError(err)
			writeJSON(w, status, body)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":      true,
			"profile": profile,
		})
	case http.MethodDelete:
		profile, err := d.WireGuardProfileService.Delete(r.Context(), agentID, profileID)
		if err != nil {
			status, body := mapServiceError(err)
			writeJSON(w, status, body)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":      true,
			"profile": profile,
		})
	default:
		http.NotFound(w, r)
	}
}
