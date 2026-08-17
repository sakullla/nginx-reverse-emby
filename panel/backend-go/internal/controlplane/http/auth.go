package http

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

const panelTokenCookie = "nre_panel_token"

func (d Dependencies) isPanelAuthorized(r *http.Request) bool {
	if d.Config.PanelToken == "" {
		return true
	}
	if tokenMatches(d.Config.PanelToken, r.Header.Get("X-Panel-Token")) {
		return true
	}
	cookie, err := r.Cookie(panelTokenCookie)
	if err != nil {
		return false
	}
	presented, decodeErr := url.QueryUnescape(cookie.Value)
	if decodeErr != nil {
		presented = cookie.Value
	}
	return tokenMatches(d.Config.PanelToken, presented)
}

func (d Dependencies) requirePanelToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !d.isPanelAuthorized(r) {
			if shouldRedirectPluginPageToLogin(r) {
				http.Redirect(w, r, d.panelLoginLocation(r), http.StatusFound)
				return
			}
			writeJSON(w, http.StatusUnauthorized, errorPayload("Unauthorized: Invalid or missing X-Panel-Token"))
			return
		}
		if d.replayPanelMutation(w, r) {
			return
		}
		next.ServeHTTP(w, r)
	})
}

func shouldRedirectPluginPageToLogin(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	if !isPluginUIPage(r.URL.Path) {
		return false
	}
	accept := r.Header.Get("Accept")
	if strings.Contains(accept, "application/json") && !strings.Contains(accept, "text/html") {
		return false
	}
	return true
}

func (d Dependencies) panelLoginLocation(r *http.Request) string {
	base := strings.TrimRight(strings.TrimSpace(d.Config.PanelPublicPath), "/")
	return base + "/login?return=" + url.QueryEscape(r.URL.RequestURI())
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
