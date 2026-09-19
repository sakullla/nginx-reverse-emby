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
		listeners, err = d.filterRelayListeners(r.Context(), listeners)
		if err != nil {
			writeAccessError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":        true,
			"listeners": listeners,
		})
	case http.MethodPost:
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, errorPayload("invalid JSON body"))
			return
		}
		var payload service.RelayListenerInput
		if err := decodeRawMessageMap(body, &payload); err != nil {
			writeJSON(w, http.StatusBadRequest, errorPayload("invalid JSON body"))
			return
		}
		_, payload.HasCertificateID = body["certificate_id"]
		_, payload.HasTLSMode = body["tls_mode"]
		_, payload.HasPinSet = body["pin_set"]
		_, payload.HasTrustedCACertificateIDs = body["trusted_ca_certificate_ids"]
		_, payload.HasAllowSelfSigned = body["allow_self_signed"]
		listener, err := d.RelayListenerService.Create(r.Context(), agentID, payload)
		if err != nil {
			err = d.auditQuotaDenial(r, err, "agent", agentID)
			status, body := mapServiceError(err)
			writeJSON(w, status, body)
			return
		}
		d.writeMutationResource(w, r, http.StatusCreated, "listener", listener, nil)
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
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, errorPayload("invalid JSON body"))
			return
		}
		var payload service.RelayListenerInput
		if err := decodeRawMessageMap(body, &payload); err != nil {
			writeJSON(w, http.StatusBadRequest, errorPayload("invalid JSON body"))
			return
		}
		_, payload.HasCertificateID = body["certificate_id"]
		_, payload.HasTLSMode = body["tls_mode"]
		_, payload.HasPinSet = body["pin_set"]
		_, payload.HasTrustedCACertificateIDs = body["trusted_ca_certificate_ids"]
		_, payload.HasAllowSelfSigned = body["allow_self_signed"]
		listener, err := d.RelayListenerService.Update(r.Context(), agentID, listenerID, payload)
		if err != nil {
			status, body := mapServiceError(err)
			writeJSON(w, status, body)
			return
		}
		d.writeMutationResource(w, r, http.StatusOK, "listener", listener, nil)
	case http.MethodDelete:
		listener, err := d.RelayListenerService.Delete(r.Context(), agentID, listenerID)
		if err != nil {
			status, body := mapServiceError(err)
			writeJSON(w, status, body)
			return
		}
		d.writeMutationResource(w, r, http.StatusOK, "listener", listener, nil)
	default:
		http.NotFound(w, r)
	}
}

func (d Dependencies) handleRelayListenersList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	query := parseListQuery(r)
	var listeners []service.RelayListener
	var meta service.PageMeta
	var err error
	if d.accessFilteringActive(r.Context()) {
		listeners, meta, err = authorizedListPage(query, func(q service.ListQuery) ([]service.RelayListener, service.PageMeta, error) {
			return d.RelayListenerService.ListPage(r.Context(), q)
		}, func(items []service.RelayListener) ([]service.RelayListener, error) {
			return d.filterRelayListeners(r.Context(), items)
		})
	} else {
		listeners, meta, err = d.RelayListenerService.ListPage(r.Context(), query)
	}
	if err != nil {
		status, payload := mapServiceError(err)
		writeJSON(w, status, payload)
		return
	}
	writeListPageJSON(w, "listeners", listeners, meta)
}
