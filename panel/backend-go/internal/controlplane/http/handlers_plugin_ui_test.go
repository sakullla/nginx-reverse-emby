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
		PluginID:        "sample-plugin",
		ExtensionPoints: []string{"ui.route", "resource.group"},
		UIRouteID:       "sample-plugin",
		ResourceGroupID: "sample-plugin",
		Metadata: map[string]string{
			"ui.nav.group":               "基础设施",
			"ui.nav.label":               "示例页面",
			"resource.group.ref":         "resource-group/sample-plugin",
			"resource.group.label":       "示例资源",
			"resource.group.description": "插件声明的示例资源组",
		},
	}
}

func newPluginUIRouter(t *testing.T) http.Handler {
	t.Helper()
	cfg := config.Default()
	cfg.PanelToken = "panel-secret"
	d := Dependencies{Config: cfg}
	mux := http.NewServeMux()
	mux.Handle("/panel-api/plugin-ui-routes", d.requirePanelToken(http.HandlerFunc(d.handlePluginUIRoutes)))
	mux.Handle("/panel-api/plugin-resource-groups", d.requirePanelToken(http.HandlerFunc(d.handlePluginResourceGroups)))
	mux.Handle("/panel-api/plugins/", d.requirePanelToken(http.HandlerFunc(d.handlePluginUI)))
	mux.Handle("/panel-api/plugins/{id}/{action}", d.requirePanelToken(http.HandlerFunc(d.handlePluginAction)))
	return mux
}

func TestPluginUIMountRejectsMissingPanelTokenWithoutEchoingSecret(t *testing.T) {
	pluginhost.Register(pluginDeclaration(), http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("plugin must not run without a panel token")
	}))
	t.Cleanup(func() { pluginhost.Unregister("sample-plugin") })

	router := newPluginUIRouter(t)
	req := httptest.NewRequest(http.MethodPost, "/panel-api/plugins/sample-plugin/api/items", strings.NewReader(`{"value":"secret-must-not-echo"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body=%s, want 401", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Unauthorized") {
		t.Fatalf("unauthorized body = %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "secret-must-not-echo") {
		t.Fatalf("unauthorized response echoed request secret: %s", rec.Body.String())
	}
}

func TestPluginUIMountUnavailableWithoutPlugin(t *testing.T) {
	pluginhost.Unregister("sample-plugin")
	router := newPluginUIRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/panel-api/plugins/sample-plugin/api/items", nil)
	req.Header.Set("X-Panel-Token", "panel-secret")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body=%s, want 503", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "sample-plugin plugin is unavailable") {
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
		_, _ = io.WriteString(w, `{"items":[{"id":"example","configured":true}]}`)
	}))
	t.Cleanup(func() { pluginhost.Unregister("sample-plugin") })

	router := newPluginUIRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/panel-api/plugins/sample-plugin/api/items", nil)
	req.Header.Set("X-Panel-Token", "panel-secret")
	req.Header.Set(nreActorHeader, "attacker/spoof")
	req.Header.Set(nreResourceGroupHeader, "attacker/group")
	req.Header.Set("X-Client-Actor", "kept")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	if gotPath != "/api/items" {
		t.Fatalf("forwarded path = %q, want /api/items", gotPath)
	}
	if gotActor != panelSessionActor {
		t.Fatalf("actor = %q, want session %q", gotActor, panelSessionActor)
	}
	if gotGroup != "resource-group/sample-plugin" {
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

func TestPluginUIMountForwardsSingleSegmentStaticAssetPastActionRouter(t *testing.T) {
	pluginhost.Register(pluginDeclaration(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/style.css" {
			t.Fatalf("forwarded path = %q, want /style.css", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/css")
		_, _ = io.WriteString(w, ".plugin { display: block; }")
	}))
	t.Cleanup(func() { pluginhost.Unregister("sample-plugin") })

	router := newPluginUIRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/panel-api/plugins/sample-plugin/style.css", nil)
	req.Header.Set("X-Panel-Token", "panel-secret")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Type"), "text/css") {
		t.Fatalf("static asset status=%d type=%q body=%s", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}
}

func TestPluginCatalogFollowsRegistration(t *testing.T) {
	pluginhost.Unregister("sample-plugin")
	router := newPluginUIRouter(t)

	emptyRoutes := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/panel-api/plugin-ui-routes", nil)
	req.Header.Set("X-Panel-Token", "panel-secret")
	router.ServeHTTP(emptyRoutes, req)
	if emptyRoutes.Code != http.StatusOK || strings.Contains(emptyRoutes.Body.String(), "sample-plugin") {
		t.Fatalf("undeclared routes = %s", emptyRoutes.Body.String())
	}

	pluginhost.Register(pluginDeclaration(), http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(func() { pluginhost.Unregister("sample-plugin") })

	listed := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/panel-api/plugin-ui-routes", nil)
	req.Header.Set("X-Panel-Token", "panel-secret")
	router.ServeHTTP(listed, req)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), "/panel-api/plugins/sample-plugin/") {
		t.Fatalf("routes = %s", listed.Body.String())
	}

	groups := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/panel-api/plugin-resource-groups", nil)
	req.Header.Set("X-Panel-Token", "panel-secret")
	router.ServeHTTP(groups, req)
	if groups.Code != http.StatusOK || !strings.Contains(groups.Body.String(), "resource-group/sample-plugin") {
		t.Fatalf("groups = %s", groups.Body.String())
	}
}

func TestPluginUIPageWithoutTokenRedirectsToLogin(t *testing.T) {
	pluginhost.Register(pluginDeclaration(), http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("plugin must not run without a panel token")
	}))
	t.Cleanup(func() { pluginhost.Unregister("sample-plugin") })

	router := newPluginUIRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/panel-api/plugins/sample-plugin/", nil)
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
	t.Cleanup(func() { pluginhost.Unregister("sample-plugin") })

	router := newPluginUIRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/panel-api/plugins/sample-plugin/", nil)
	req.AddCookie(&http.Cookie{Name: panelTokenCookie, Value: "panel-secret"})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "plugin-page") {
		t.Fatalf("cookie auth status=%d body=%s", rec.Code, rec.Body.String())
	}
}
