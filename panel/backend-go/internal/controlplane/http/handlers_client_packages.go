package http

import (
	"encoding/json"
	"net/http"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
)

func (d Dependencies) handleClientPackages(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		packages, err := d.ClientPackageService.List(r.Context())
		if err != nil {
			status, payload := mapServiceError(err)
			writeJSON(w, status, payload)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":       true,
			"packages": packages,
		})
	case http.MethodPost:
		var payload service.ClientPackageInput
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeJSON(w, http.StatusBadRequest, errorPayload("invalid JSON body"))
			return
		}
		pkg, err := d.ClientPackageService.Create(r.Context(), payload)
		if err != nil {
			status, body := mapServiceError(err)
			writeJSON(w, status, body)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"ok":      true,
			"package": pkg,
		})
	default:
		http.NotFound(w, r)
	}
}

func (d Dependencies) handleClientPackage(w http.ResponseWriter, r *http.Request) {
	packageID := r.PathValue("id")
	if packageID == "" {
		writeJSON(w, http.StatusBadRequest, errorPayload("invalid client package id"))
		return
	}

	switch r.Method {
	case http.MethodPut:
		var payload service.ClientPackageInput
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeJSON(w, http.StatusBadRequest, errorPayload("invalid JSON body"))
			return
		}
		pkg, err := d.ClientPackageService.Update(r.Context(), packageID, payload)
		if err != nil {
			status, body := mapServiceError(err)
			writeJSON(w, status, body)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":      true,
			"package": pkg,
		})
	case http.MethodDelete:
		pkg, err := d.ClientPackageService.Delete(r.Context(), packageID)
		if err != nil {
			status, body := mapServiceError(err)
			writeJSON(w, status, body)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":      true,
			"package": pkg,
		})
	default:
		http.NotFound(w, r)
	}
}

func (d Dependencies) handleLatestClientPackage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}

	query := r.URL.Query()
	pkg, err := d.ClientPackageService.Latest(r.Context(), service.ClientPackageQuery{
		Platform: query.Get("platform"),
		Arch:     query.Get("arch"),
		Kind:     query.Get("kind"),
	})
	if err != nil {
		status, body := mapServiceError(err)
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"package": pkg,
	})
}
