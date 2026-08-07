package http

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/authz"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func (d Dependencies) isPanelAuthorized(r *http.Request) bool {
	_, err := d.authenticatePanelRequest(r)
	return err == nil
}

func (d Dependencies) requirePanelToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actor, err := d.authenticatePanelRequest(r)
		if err != nil {
			if errors.Is(err, authz.ErrUnauthorized) || errors.Is(err, authz.ErrInvalidCredentials) {
				writeJSON(w, http.StatusUnauthorized, errorPayloadCode("authentication_required", "Unauthorized: invalid or missing session credential"))
			} else {
				writeAccessError(w, err)
			}
			return
		}
		ctx := context.WithValue(r.Context(), actorContextKey{}, actor)
		correlationID := strings.TrimSpace(r.Header.Get("X-Request-ID"))
		ctx = authz.WithCorrelationID(ctx, correlationID)
		ctx = storage.WithQuotaActor(ctx, storage.QuotaActor{UserID: actor.ID, SessionID: actor.SessionID, CorrelationID: correlationID, Bootstrap: actor.Bootstrap})
		if d.AccessManager != nil {
			ctx = service.WithResourceAuthorizer(ctx, func(checkCtx context.Context, kind, id string) error {
				if actor.Bootstrap {
					return nil
				}
				return d.AccessManager.AuthorizeResource(checkCtx, actor, authz.PermissionResourceWrite, kind, id)
			})
		}
		r = r.WithContext(ctx)
		if d.AccessManager != nil && !actor.Bootstrap {
			permission := requestPermission(r)
			if kind, id, ok := d.requestResource(r.Method, r.URL.Path); ok {
				err = d.AccessManager.AuthorizeResource(r.Context(), actor, permission, kind, id)
			} else {
				err = d.AccessManager.Authorize(r.Context(), actor, permission, "api", r.URL.Path, "")
			}
			if err != nil {
				specializedHandler := strings.Contains(r.URL.Path, "/access/") || strings.Contains(r.URL.Path, "/auth/")
				if !specializedHandler || errors.Is(err, authz.ErrAuditUnavailable) {
					writeAccessError(w, err)
					return
				}
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

func (d Dependencies) auditQuotaDenial(r *http.Request, err error, targetKind, targetID string) error {
	if !errors.Is(err, storage.ErrQuotaExceeded) || d.AccessManager == nil {
		return err
	}
	actor, ok := actorFromRequest(r)
	if !ok {
		return err
	}
	metadata := map[string]any(nil)
	resourceGroupID := ""
	var quotaErr *storage.QuotaExceededError
	if errors.As(err, &quotaErr) {
		metadata = map[string]any{"decision": quotaErr.Decision}
		resourceGroupID = quotaErr.Decision.ResourceGroupID
	}
	auditErr := d.AccessManager.Audit(r.Context(), actor, "quota.consume", targetKind, targetID, resourceGroupID, "denied", "quota_exceeded", metadata)
	return errors.Join(err, auditErr)
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
			if err := d.AccessManager.Audit(r.Context(), actor, "auth.bootstrap", "api", r.URL.Path, "", "success", "", nil); err != nil {
				return authz.Actor{}, err
			}
		}
		return actor, nil
	}
	if d.Config.PanelToken == "" && d.AccessManager == nil {
		return authz.BootstrapActor(), nil
	}
	return authz.Actor{}, authz.ErrUnauthorized
}

func requestPermission(r *http.Request) string {
	path := r.URL.Path
	if strings.Contains(path, "/system/backup/") || strings.Contains(path, "/pki/") || strings.Contains(path, "/marketplace/") || strings.Contains(path, "/plugins/") || strings.HasSuffix(strings.TrimRight(path, "/"), "/plugins") ||
		(strings.Contains(path, "/version-policies") && r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions) {
		return authz.PermissionSystemAdmin
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
		return authz.PermissionResourceWrite
	}
	return authz.PermissionResourceRead
}

func (d Dependencies) requestResource(method, path string) (string, string, bool) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) > 0 && (parts[0] == "api" || parts[0] == "panel-api") {
		parts = parts[1:]
	}
	if len(parts) == 1 {
		switch parts[0] {
		case "rules", "stats", "apply":
			return "agent", strings.TrimSpace(d.Config.LocalAgentID), true
		case "egress-profiles":
			if method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions {
				return "", "", false
			}
			return "resource_group", authz.DefaultResourceGroup, true
		}
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
			if nestedKind == "certificate" {
				return nestedKind, parts[3], true
			}
			return nestedKind, parts[1] + ":" + parts[3], true
		}
	}
	kind, ok := kinds[parts[0]]
	if ok && parts[1] != "" && parts[1] != "monitor-stream" && parts[1] != "register" && parts[1] != "heartbeat" {
		if parts[0] == "rules" {
			return kind, strings.TrimSpace(d.Config.LocalAgentID) + ":" + parts[1], true
		}
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

func quotaErrorPayload(err error) map[string]any {
	payload := errorPayloadCode("quota_exceeded", "quota exceeded")
	var quotaErr *storage.QuotaExceededError
	if errors.As(err, &quotaErr) {
		payload["quota_context"] = quotaErr.Decision
	}
	return payload
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
