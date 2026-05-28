package http

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
)

func (d Dependencies) handleEgressProfiles(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		profiles, err := d.EgressProfileService.List(r.Context())
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
		var payload service.EgressProfileInput
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeJSON(w, http.StatusBadRequest, errorPayload("invalid JSON body"))
			return
		}
		profile, err := d.EgressProfileService.Create(r.Context(), payload)
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

func (d Dependencies) handleEgressProfile(w http.ResponseWriter, r *http.Request) {
	profileID, ok := parseEgressProfilePathID(w, r.PathValue("id"))
	if !ok {
		return
	}

	switch r.Method {
	case http.MethodGet:
		profile, err := d.EgressProfileService.Get(r.Context(), profileID)
		if err != nil {
			status, body := mapServiceError(err)
			writeJSON(w, status, body)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":      true,
			"profile": profile,
		})
	case http.MethodPut:
		var payload service.EgressProfileInput
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeJSON(w, http.StatusBadRequest, errorPayload("invalid JSON body"))
			return
		}
		profile, err := d.EgressProfileService.Update(r.Context(), profileID, payload)
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
		profile, err := d.EgressProfileService.Delete(r.Context(), profileID)
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

func parseEgressProfilePathID(w http.ResponseWriter, raw string) (int, bool) {
	id, err := strconv.Atoi(raw)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, errorPayload("invalid egress profile id"))
		return 0, false
	}
	return id, true
}
