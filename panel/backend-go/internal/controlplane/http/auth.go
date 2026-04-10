package http

import (
	"encoding/json"
	"net/http"
)

func (d Dependencies) isPanelAuthorized(r *http.Request) bool {
	if d.Config.PanelToken == "" {
		return true
	}
	return r.Header.Get("X-Panel-Token") == d.Config.PanelToken
}

func (d Dependencies) requirePanelToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !d.isPanelAuthorized(r) {
			writeJSON(w, http.StatusUnauthorized, errorPayload("Unauthorized: Invalid or missing X-Panel-Token"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (d Dependencies) isRegisterAuthorized(r *http.Request, registerToken string) bool {
	if d.Config.RegisterToken == "" {
		return true
	}
	if r.Header.Get("X-Register-Token") == d.Config.RegisterToken {
		return true
	}
	return registerToken == d.Config.RegisterToken
}

func errorPayload(message string) map[string]any {
	return map[string]any{
		"ok":      false,
		"message": message,
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
