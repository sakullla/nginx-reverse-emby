package http

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
)

func (d Dependencies) handleCertificates(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("agentID")

	switch r.Method {
	case http.MethodGet:
		certs, err := d.CertificateService.List(r.Context(), agentID)
		if err != nil {
			status, payload := mapServiceError(err)
			writeJSON(w, status, payload)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":           true,
			"certificates": certs,
		})
	case http.MethodPost:
		var payload service.ManagedCertificateInput
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeJSON(w, http.StatusBadRequest, errorPayload("invalid JSON body"))
			return
		}
		cert, err := d.CertificateService.Create(r.Context(), agentID, payload)
		if err != nil {
			status, body := mapServiceError(err)
			writeJSON(w, status, body)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"ok":          true,
			"certificate": cert,
		})
	default:
		http.NotFound(w, r)
	}
}

func (d Dependencies) handleCertificate(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("agentID")
	certID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || certID <= 0 {
		writeJSON(w, http.StatusBadRequest, errorPayload("invalid certificate id"))
		return
	}

	switch r.Method {
	case http.MethodPut:
		var payload service.ManagedCertificateInput
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeJSON(w, http.StatusBadRequest, errorPayload("invalid JSON body"))
			return
		}
		cert, err := d.CertificateService.Update(r.Context(), agentID, certID, payload)
		if err != nil {
			status, body := mapServiceError(err)
			writeJSON(w, status, body)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":          true,
			"certificate": cert,
		})
	case http.MethodDelete:
		cert, err := d.CertificateService.Delete(r.Context(), agentID, certID)
		if err != nil {
			status, body := mapServiceError(err)
			writeJSON(w, status, body)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":          true,
			"certificate": cert,
		})
	default:
		http.NotFound(w, r)
	}
}

func (d Dependencies) handleIssueCertificate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}

	agentID := r.PathValue("agentID")
	certID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || certID <= 0 {
		writeJSON(w, http.StatusBadRequest, errorPayload("invalid certificate id"))
		return
	}

	cert, err := d.CertificateService.Issue(r.Context(), agentID, certID)
	if err != nil {
		status, body := mapServiceError(err)
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"certificate": cert,
	})
}
