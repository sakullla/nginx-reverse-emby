package http

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/pluginhost"
)

func pluginDeclaration() pluginhost.Declaration {
	return pluginhost.Declaration{
		PluginID:        "cloudflare-dns",
		ExtensionPoints: []string{"ui.route", "resource.group"},
		UIRouteID:       "cloudflare-dns",
		ResourceGroupID: "cloudflare-dns",
		Metadata: map[string]string{
			"ui.nav.group":               "基础设施",
			"ui.nav.label":               "域名 Token",
			"resource.group.ref":         "resource-group/cloudflare-dns",
			"resource.group.label":       "Cloudflare DNS",
			"resource.group.description": "按域名后缀隔离 Token 映射",
		},
	}
}

func newPluginUIRouter(t *testing.T) http.Handler {
	t.Helper()
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.PanelToken = "panel-secret"
	router, err := NewRouter(Dependencies{Config: cfg})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}
	if closer, ok := router.(interface{ Close() error }); ok {
		t.Cleanup(func() { _ = closer.Close() })
	}
	return router
}

func TestPluginUIMountRejectsMissingPanelTokenWithoutEchoingSecret(t *testing.T) {
	pluginhost.Register(pluginDeclaration(), http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("plugin must not run without a panel token")
	}))
	t.Cleanup(func() { pluginhost.Unregister("cloudflare-dns") })

	router := newPluginUIRouter(t)
	req := httptest.NewRequest(http.MethodPost, "/panel-api/plugins/cloudflare-dns/api/mappings", strings.NewReader(`{"suffix":"example.com","token":"cf-secret-must-not-echo"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body=%s, want 401", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Unauthorized") {
		t.Fatalf("unauthorized body = %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "cf-secret-must-not-echo") {
		t.Fatalf("unauthorized response echoed Cloudflare token: %s", rec.Body.String())
	}
}

func TestPluginUIMountUnavailableWithoutPlugin(t *testing.T) {
	pluginhost.Unregister("cloudflare-dns")
	router := newPluginUIRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/panel-api/plugins/cloudflare-dns/api/mappings", nil)
	req.Header.Set("X-Panel-Token", "panel-secret")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body=%s, want 503", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "cloudflare-dns plugin is unavailable") {
		t.Fatalf("unavailable body = %s", rec.Body.String())
	}
}

func TestPluginUIMountForwardsAuthorizedRequestToPlugin(t *testing.T) {
	var gotPath, gotActor, gotGroup, gotClientActor string
	pluginhost.Register(pluginDeclaration(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotActor = r.Header.Get(nreActorHeader)
		gotGroup = r.Header.Get(nreResourceGroupHeader)
		gotClientActor = r.Header.Get("X-Client-Actor")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"mappings":[{"suffix":"example.com","configured":true}]}`)
	}))
	t.Cleanup(func() { pluginhost.Unregister("cloudflare-dns") })

	router := newPluginUIRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/panel-api/plugins/cloudflare-dns/api/mappings", nil)
	req.Header.Set("X-Panel-Token", "panel-secret")
	req.Header.Set(nreActorHeader, "attacker/spoof")
	req.Header.Set(nreResourceGroupHeader, "attacker/group")
	req.Header.Set("X-Client-Actor", "kept")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	if gotPath != "/api/mappings" {
		t.Fatalf("forwarded path = %q, want /api/mappings", gotPath)
	}
	if gotActor != panelSessionActor {
		t.Fatalf("actor = %q, want session %q", gotActor, panelSessionActor)
	}
	if gotGroup != "resource-group/cloudflare-dns" {
		t.Fatalf("group = %q, want declared ref", gotGroup)
	}
	if gotClientActor != "kept" {
		t.Fatalf("unrelated header dropped: %q", gotClientActor)
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode plugin payload: %v", err)
	}
	if _, hasToken := payload["token"]; hasToken {
		t.Fatalf("list payload included token: %s", rec.Body.String())
	}
}

func TestPluginCatalogFollowsRegistration(t *testing.T) {
	pluginhost.Unregister("cloudflare-dns")
	router := newPluginUIRouter(t)

	emptyRoutes := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/panel-api/plugin-ui-routes", nil)
	req.Header.Set("X-Panel-Token", "panel-secret")
	router.ServeHTTP(emptyRoutes, req)
	if emptyRoutes.Code != http.StatusOK || strings.Contains(emptyRoutes.Body.String(), "cloudflare-dns") {
		t.Fatalf("undeclared routes = %s", emptyRoutes.Body.String())
	}

	pluginhost.Register(pluginDeclaration(), http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(func() { pluginhost.Unregister("cloudflare-dns") })

	listed := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/panel-api/plugin-ui-routes", nil)
	req.Header.Set("X-Panel-Token", "panel-secret")
	router.ServeHTTP(listed, req)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), "/panel-api/plugins/cloudflare-dns/") {
		t.Fatalf("routes = %s", listed.Body.String())
	}

	groups := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/panel-api/plugin-resource-groups", nil)
	req.Header.Set("X-Panel-Token", "panel-secret")
	router.ServeHTTP(groups, req)
	if groups.Code != http.StatusOK || !strings.Contains(groups.Body.String(), "resource-group/cloudflare-dns") {
		t.Fatalf("groups = %s", groups.Body.String())
	}
}

func TestPluginUIPageWithoutTokenRedirectsToLogin(t *testing.T) {
	pluginhost.Register(pluginDeclaration(), http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("plugin must not run without a panel token")
	}))
	t.Cleanup(func() { pluginhost.Unregister("cloudflare-dns") })

	router := newPluginUIRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/panel-api/plugins/cloudflare-dns/", nil)
	req.Header.Set("Accept", "text/html")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d body=%s, want 302", rec.Code, rec.Body.String())
	}
	location := rec.Header().Get("Location")
	if !strings.Contains(location, "/login?return=") || !strings.Contains(location, "plugins") {
		t.Fatalf("location = %q", location)
	}
}

func TestPluginUIMountAcceptsPanelTokenCookie(t *testing.T) {
	pluginhost.Register(pluginDeclaration(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("plugin-page"))
	}))
	t.Cleanup(func() { pluginhost.Unregister("cloudflare-dns") })

	router := newPluginUIRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/panel-api/plugins/cloudflare-dns/", nil)
	req.AddCookie(&http.Cookie{Name: panelTokenCookie, Value: "panel-secret"})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "plugin-page") {
		t.Fatalf("cookie auth status=%d body=%s", rec.Code, rec.Body.String())
	}
}
