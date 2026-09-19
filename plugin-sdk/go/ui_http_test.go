package pluginsdk

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestServePluginUIAssetOwnsOnlyRootFiles(t *testing.T) {
	assets := fstest.MapFS{
		"assets/ui/index.html": &fstest.MapFile{Data: []byte("page")},
		"assets/ui/app.js":     &fstest.MapFile{Data: []byte("script")},
	}
	for requestPath, wantBody := range map[string]string{"/": "page", "/app.js": "script"} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, requestPath, nil)
		if !ServePluginUIAsset(recorder, request, assets, "assets/ui") || !strings.Contains(recorder.Body.String(), wantBody) {
			t.Fatalf("asset %s = %d/%q", requestPath, recorder.Code, recorder.Body.String())
		}
	}
	for _, requestPath := range []string{"/api/items", "/missing.js", "/nested/file.js"} {
		if ServePluginUIAsset(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, requestPath, nil), assets, "assets/ui") {
			t.Fatalf("non-asset path %q was handled", requestPath)
		}
	}
}

func TestPluginUIHTTPHelpersApplySecurityIdentityAndJSON(t *testing.T) {
	recorder := httptest.NewRecorder()
	SetPluginUIResponseHeaders(recorder.Header())
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set(HeaderPluginActor, " panel/admin ")
	actor, ok := PluginUIActor(request)
	if !ok || actor != "panel/admin" {
		t.Fatalf("actor = %q/%t", actor, ok)
	}
	if recorder.Header().Get("Content-Security-Policy") != PluginUIContentSecurityPolicy {
		t.Fatal("plugin UI CSP was not applied")
	}
	if err := WritePluginUIJSON(recorder, http.StatusCreated, map[string]bool{"ok": true}); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusCreated || recorder.Header().Get("Content-Type") != "application/json" || !strings.Contains(recorder.Body.String(), `"ok":true`) {
		t.Fatalf("JSON response = %d %q", recorder.Code, recorder.Body.String())
	}
}
