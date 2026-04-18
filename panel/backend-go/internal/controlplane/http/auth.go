package http

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
)

func (d Dependencies) isPanelAuthorized(r *http.Request) bool {
	if d.Config.PanelToken == "" {
		return true
	}
	return tokenMatches(d.Config.PanelToken, r.Header.Get("X-Panel-Token"))
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
	if tokenMatches(d.Config.RegisterToken, r.Header.Get("X-Register-Token")) {
		return true
	}
	return tokenMatches(d.Config.RegisterToken, registerToken)
}

func tokenMatches(expected string, presented string) bool {
	if expected == "" || presented == "" {
		return false
	}
	if len(expected) != len(presented) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(expected), []byte(presented)) == 1
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
