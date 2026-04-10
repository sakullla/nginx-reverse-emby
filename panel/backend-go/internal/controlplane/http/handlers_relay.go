package http

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
)

func (d Dependencies) handleRelayListeners(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("agentID")

	switch r.Method {
	case http.MethodGet:
		listeners, err := d.RelayListenerService.List(r.Context(), agentID)
		if err != nil {
			status, payload := mapServiceError(err)
			writeJSON(w, status, payload)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":        true,
			"listeners": listeners,
		})
	case http.MethodPost:
		var payload service.RelayListenerInput
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeJSON(w, http.StatusBadRequest, errorPayload("invalid JSON body"))
			return
		}
		listener, err := d.RelayListenerService.Create(r.Context(), agentID, payload)
		if err != nil {
			status, body := mapServiceError(err)
			writeJSON(w, status, body)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"ok":       true,
			"listener": listener,
		})
	default:
		http.NotFound(w, r)
	}
}

func (d Dependencies) handleRelayListener(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("agentID")
	listenerID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || listenerID <= 0 {
		writeJSON(w, http.StatusBadRequest, errorPayload("invalid relay listener id"))
		return
	}

	switch r.Method {
	case http.MethodPut:
		var payload service.RelayListenerInput
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeJSON(w, http.StatusBadRequest, errorPayload("invalid JSON body"))
			return
		}
		listener, err := d.RelayListenerService.Update(r.Context(), agentID, listenerID, payload)
		if err != nil {
			status, body := mapServiceError(err)
			writeJSON(w, status, body)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":       true,
			"listener": listener,
		})
	case http.MethodDelete:
		listener, err := d.RelayListenerService.Delete(r.Context(), agentID, listenerID)
		if err != nil {
			status, body := mapServiceError(err)
			writeJSON(w, status, body)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":       true,
			"listener": listener,
		})
	default:
		http.NotFound(w, r)
	}
}
