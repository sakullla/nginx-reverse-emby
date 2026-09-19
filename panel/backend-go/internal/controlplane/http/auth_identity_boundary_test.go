package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/authz"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/revision"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestIdentityScopesPanelMutationIdempotency(t *testing.T) {
	store, err := storage.NewStore(storage.StoreConfig{Driver: "sqlite", DataRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	manager := authz.NewManager(store, authz.Options{})
	if err := manager.EnsureDefaults(t.Context()); err != nil {
		t.Fatal(err)
	}

	const password = "identity-boundary-password"
	for _, username := range []string{"identity-a", "identity-b"} {
		if _, err := manager.CreateUser(t.Context(), username, username, password, []string{authz.RoleOperator}); err != nil {
			t.Fatal(err)
		}
	}
	identityA, err := manager.Login(t.Context(), "identity-a", password)
	if err != nil {
		t.Fatal(err)
	}
	identityB, err := manager.Login(t.Context(), "identity-b", password)
	if err != nil {
		t.Fatal(err)
	}

	replayScopes := &identityReplayScopeRecorder{}
	deps := Dependencies{AccessManager: manager, RevisionService: replayScopes}
	captureScope := func(token string) string {
		t.Helper()
		var scope string
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var key string
			scope, key, _ = revision.MutationIdempotencyFromContext(r.Context())
			if key != "shared-key" {
				t.Fatalf("idempotency key = %q", key)
			}
			w.WriteHeader(http.StatusNoContent)
		})
		req := httptest.NewRequest(http.MethodPost, "/panel-api/auth/logout", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Idempotency-Key", "shared-key")
		resp := httptest.NewRecorder()
		deps.requirePanelToken(next).ServeHTTP(resp, req)
		if resp.Code != http.StatusNoContent {
			t.Fatalf("request status = %d body=%s", resp.Code, resp.Body.String())
		}
		return scope
	}

	scopeA := captureScope(identityA.Token)
	if scopeA == "" || scopeA == service.PanelIdempotencyScope {
		t.Fatalf("identity A scope = %q, want identity-bound scope", scopeA)
	}
	if repeated := captureScope(identityA.Token); repeated != scopeA {
		t.Fatalf("same identity scope changed from %q to %q", scopeA, repeated)
	}
	if scopeB := captureScope(identityB.Token); scopeB == scopeA {
		t.Fatalf("different sessions shared idempotency scope %q", scopeA)
	}
	if len(replayScopes.scopes) != 3 || replayScopes.scopes[0] != scopeA || replayScopes.scopes[1] != scopeA || replayScopes.scopes[2] == scopeA {
		t.Fatalf("replay lookup scopes = %q, want same-session stability and cross-session isolation", replayScopes.scopes)
	}
}

func TestIdentityScopesBootstrapTokenRotation(t *testing.T) {
	requestA := httptest.NewRequest(http.MethodPost, "/panel-api/rules", nil)
	requestA.Header.Set("X-Panel-Token", "bootstrap-token-a")
	requestB := httptest.NewRequest(http.MethodPost, "/panel-api/rules", nil)
	requestB.Header.Set("X-Panel-Token", "bootstrap-token-b")

	scopeA := panelIdentityIdempotencyScope(requestA, authz.BootstrapActor())
	scopeB := panelIdentityIdempotencyScope(requestB, authz.BootstrapActor())
	if scopeA == scopeB {
		t.Fatalf("rotated bootstrap credentials shared idempotency scope %q", scopeA)
	}
}

type identityReplayScopeRecorder struct {
	RevisionService
	scopes []string
}

func (r *identityReplayScopeRecorder) LoadMutationResponseByKey(_ context.Context, scope, _ string) (map[string]any, bool, error) {
	r.scopes = append(r.scopes, scope)
	return nil, false, nil
}
