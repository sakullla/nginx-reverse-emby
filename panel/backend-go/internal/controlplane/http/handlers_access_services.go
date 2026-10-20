package http

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
)

func (d Dependencies) handleAccessServices(w http.ResponseWriter, r *http.Request) {
	if d.AccessService == nil {
		writeJSON(w, http.StatusServiceUnavailable, errorPayload("access service is unavailable"))
		return
	}
	switch r.Method {
	case http.MethodGet:
		views, err := d.AccessService.List(r.Context())
		if err != nil {
			d.writeAccessResult(w, service.AccessServiceView{}, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "services": views})
	case http.MethodPost:
		body, input, ok := readAccessServiceBody(w, r)
		if !ok {
			return
		}
		view, err := d.AccessService.Create(r.Context(), r.Header.Get("Idempotency-Key"), body, input)
		d.writeAccessResult(w, view, err)
	default:
		w.Header().Set("Allow", "GET, POST")
		writeJSON(w, http.StatusMethodNotAllowed, errorPayload("method not allowed"))
	}
}

func (d Dependencies) handleAccessService(w http.ResponseWriter, r *http.Request) {
	if d.AccessService == nil {
		writeJSON(w, http.StatusServiceUnavailable, errorPayload("access service is unavailable"))
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	switch r.Method {
	case http.MethodGet:
		view, err := d.AccessService.Get(r.Context(), id)
		d.writeAccessResult(w, view, err)
	case http.MethodPut:
		body, input, ok := readAccessServiceBody(w, r)
		if !ok {
			return
		}
		view, err := d.AccessService.Update(r.Context(), id, r.Header.Get("Idempotency-Key"), body, input)
		d.writeAccessResult(w, view, err)
	case http.MethodDelete:
		body, ok := readAccessRawBody(w, r)
		if !ok {
			return
		}
		view, err := d.AccessService.Delete(r.Context(), id, r.Header.Get("Idempotency-Key"), body)
		d.writeAccessResult(w, view, err)
	default:
		w.Header().Set("Allow", "GET, PUT, DELETE")
		writeJSON(w, http.StatusMethodNotAllowed, errorPayload("method not allowed"))
	}
}

func (d Dependencies) handleAccessServiceCorrection(w http.ResponseWriter, r *http.Request) {
	d.handleAccessServiceAction(w, r, func(body []byte, input service.AccessServiceInput) (service.AccessServiceView, error) {
		return d.AccessService.Correct(r.Context(), r.PathValue("id"), r.Header.Get("Idempotency-Key"), body, input)
	})
}

func (d Dependencies) handleAccessServiceRetry(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, errorPayload("method not allowed"))
		return
	}
	d.handleAccessServiceAction(w, r, func(body []byte, _ service.AccessServiceInput) (service.AccessServiceView, error) {
		return d.AccessService.Retry(r.Context(), r.PathValue("id"), r.Header.Get("Idempotency-Key"), body)
	})
}

func (d Dependencies) handleAccessServiceRetire(w http.ResponseWriter, r *http.Request) {
	d.handleAccessServiceAction(w, r, func(body []byte, _ service.AccessServiceInput) (service.AccessServiceView, error) {
		return d.AccessService.Retire(r.Context(), r.PathValue("id"), r.Header.Get("Idempotency-Key"), body)
	})
}

func (d Dependencies) handleAccessServiceAbandon(w http.ResponseWriter, r *http.Request) {
	d.handleAccessServiceAction(w, r, func(body []byte, _ service.AccessServiceInput) (service.AccessServiceView, error) {
		return d.AccessService.Abandon(r.Context(), r.PathValue("id"), r.Header.Get("Idempotency-Key"), body)
	})
}

func (d Dependencies) handleAccessServiceConfirmation(w http.ResponseWriter, r *http.Request) {
	if d.AccessService == nil {
		writeJSON(w, http.StatusServiceUnavailable, errorPayload("access service is unavailable"))
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, errorPayload("method not allowed"))
		return
	}
	body, ok := readAccessRawBody(w, r)
	if !ok {
		return
	}
	var input service.AccessClientConfirmationInput
	if len(strings.TrimSpace(string(body))) > 0 {
		if err := json.Unmarshal(body, &input); err != nil {
			writeJSON(w, http.StatusBadRequest, errorPayload("invalid JSON body"))
			return
		}
	}
	view, err := d.AccessService.ConfirmClient(r.Context(), r.PathValue("id"), r.Header.Get("Idempotency-Key"), body, input)
	d.writeAccessResult(w, view, err)
}

func (d Dependencies) handleAccessServiceAction(
	w http.ResponseWriter,
	r *http.Request,
	run func([]byte, service.AccessServiceInput) (service.AccessServiceView, error),
) {
	if d.AccessService == nil {
		writeJSON(w, http.StatusServiceUnavailable, errorPayload("access service is unavailable"))
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, errorPayload("method not allowed"))
		return
	}
	body, input, ok := readAccessServiceBody(w, r)
	if !ok {
		return
	}
	view, err := run(body, input)
	d.writeAccessResult(w, view, err)
}

func (d Dependencies) writeAccessResult(w http.ResponseWriter, view service.AccessServiceView, err error) {
	if err == nil {
		writeJSON(w, http.StatusOK, view)
		return
	}
	var statusErr *service.AccessStatusError
	if errors.As(err, &statusErr) {
		status, body := mapServiceError(statusErr.Err)
		if statusErr.Status != 0 {
			status = statusErr.Status
		}
		if statusErr.HasView {
			body["service"] = statusErr.View
		}
		writeJSON(w, status, body)
		return
	}
	status, body := mapServiceError(err)
	writeJSON(w, status, body)
}

func readAccessServiceBody(w http.ResponseWriter, r *http.Request) ([]byte, service.AccessServiceInput, bool) {
	body, ok := readAccessRawBody(w, r)
	if !ok {
		return nil, service.AccessServiceInput{}, false
	}
	var input service.AccessServiceInput
	if len(strings.TrimSpace(string(body))) > 0 {
		if err := json.Unmarshal(body, &input); err != nil {
			writeJSON(w, http.StatusBadRequest, errorPayload("invalid JSON body"))
			return nil, service.AccessServiceInput{}, false
		}
	}
	return body, input, true
}

func readAccessRawBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorPayload("invalid JSON body"))
		return nil, false
	}
	return body, true
}
