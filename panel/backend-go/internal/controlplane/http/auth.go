package http

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"os"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/authz"
)

func (d Dependencies) isPanelAuthorized(r *http.Request) bool {
	_, err := d.authenticatePanelRequest(r)
	return err == nil
}

func (d Dependencies) requirePanelToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actor, err := d.authenticatePanelRequest(r)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, errorPayloadCode("authentication_required", "Unauthorized: invalid or missing session credential"))
			return
		}
		ctx := context.WithValue(r.Context(), actorContextKey{}, actor)
		correlationID := strings.TrimSpace(r.Header.Get("X-Request-ID"))
		ctx = authz.WithCorrelationID(ctx, correlationID)
		r = r.WithContext(ctx)
		if d.AccessManager != nil && !actor.Bootstrap {
			permission := authz.PermissionResourceRead
			if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
				permission = authz.PermissionResourceWrite
			}
			if kind, id, ok := requestResource(r.URL.Path); ok {
				err = d.AccessManager.AuthorizeResource(r.Context(), actor, permission, kind, id)
			} else {
				err = d.AccessManager.Authorize(r.Context(), actor, permission, "api", r.URL.Path, "")
			}
			if err != nil && !strings.Contains(r.URL.Path, "/access/") && !strings.Contains(r.URL.Path, "/auth/") {
				writeJSON(w, http.StatusForbidden, errorPayloadCode("permission_denied", "permission denied"))
				return
			}
		}
		if d.replayPanelMutation(w, r) {
			return
		}
		next.ServeHTTP(w, r)
	})
}

type actorContextKey struct{}

func actorFromRequest(r *http.Request) (authz.Actor, bool) {
	actor, ok := r.Context().Value(actorContextKey{}).(authz.Actor)
	return actor, ok
}

func (d Dependencies) authenticatePanelRequest(r *http.Request) (authz.Actor, error) {
	sessionToken := strings.TrimSpace(r.Header.Get("X-Panel-Session"))
	if authorization := strings.TrimSpace(r.Header.Get("Authorization")); strings.HasPrefix(strings.ToLower(authorization), "bearer ") {
		sessionToken = strings.TrimSpace(authorization[len("Bearer "):])
	}
	if sessionToken != "" && d.AccessManager != nil {
		return d.AccessManager.AuthenticateSession(r.Context(), sessionToken)
	}
	bootstrapEnabled := !strings.EqualFold(strings.TrimSpace(os.Getenv("PANEL_BOOTSTRAP_TOKEN_ENABLED")), "false")
	if bootstrapEnabled && d.Config.PanelToken != "" && tokenMatches(d.Config.PanelToken, r.Header.Get("X-Panel-Token")) {
		actor := authz.BootstrapActor()
		if d.AccessManager != nil {
			d.AccessManager.Audit(r.Context(), actor, "auth.bootstrap", "api", r.URL.Path, "", "success", "", nil)
		}
		return actor, nil
	}
	if d.Config.PanelToken == "" && d.AccessManager == nil {
		return authz.BootstrapActor(), nil
	}
	return authz.Actor{}, authz.ErrUnauthorized
}

func requestResource(path string) (string, string, bool) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) > 0 && (parts[0] == "api" || parts[0] == "panel-api") {
		parts = parts[1:]
	}
	if len(parts) < 2 {
		return "", "", false
	}
	kinds := map[string]string{
		"agents": "agent", "rules": "http_rule", "http-rules": "http_rule", "l4-rules": "l4_rule",
		"relay-listeners": "relay_listener", "certificates": "certificate", "egress-profiles": "egress_profile",
	}
	if parts[0] == "agents" && len(parts) >= 4 {
		if nestedKind, found := kinds[parts[2]]; found {
			return nestedKind, parts[1] + ":" + parts[3], true
		}
	}
	kind, ok := kinds[parts[0]]
	if ok && parts[1] != "" && parts[1] != "monitor-stream" && parts[1] != "register" && parts[1] != "heartbeat" {
		return kind, parts[1], true
	}
	return "", "", false
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

func errorPayloadCode(code, message string) map[string]any {
	return map[string]any{"ok": false, "code": code, "message": message}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
