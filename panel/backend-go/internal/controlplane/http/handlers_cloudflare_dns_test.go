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

func newCloudflareDNSRouter(t *testing.T) http.Handler {
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

func TestCloudflareDNSMountRejectsMissingPanelTokenWithoutEchoingSecret(t *testing.T) {
	pluginhost.SetCloudflareDNSHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("plugin must not run without a panel token")
	}))
	t.Cleanup(func() { pluginhost.SetCloudflareDNSHandler(nil) })

	router := newCloudflareDNSRouter(t)
	req := httptest.NewRequest(http.MethodPost, "/panel-api/cloudflare-dns/api/mappings", strings.NewReader(`{"suffix":"example.com","token":"cf-secret-must-not-echo"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body=%s, want 401", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Unauthorized") {
		t.Fatalf("unauthorized body = %s, want an explicit rejection", body)
	}
	if strings.Contains(body, "cf-secret-must-not-echo") {
		t.Fatalf("unauthorized response echoed Cloudflare token: %s", body)
	}
}

func TestCloudflareDNSMountUnavailableWithoutPlugin(t *testing.T) {
	pluginhost.SetCloudflareDNSHandler(nil)
	router := newCloudflareDNSRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/panel-api/cloudflare-dns/api/mappings", nil)
	req.Header.Set("X-Panel-Token", "panel-secret")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body=%s, want 503", rec.Code, rec.Body.String())
	}
	if strings.TrimSpace(rec.Body.String()) == "" {
		t.Fatal("unavailable response was blank")
	}
	if !strings.Contains(rec.Body.String(), "cloudflare-dns plugin is unavailable") {
		t.Fatalf("unavailable body = %s", rec.Body.String())
	}
}

func TestCloudflareDNSMountForwardsAuthorizedRequestToPlugin(t *testing.T) {
	var gotPath, gotActor, gotGroup, gotClientActor string
	pluginhost.SetCloudflareDNSHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotActor = r.Header.Get(cloudflareDNSActorHeader)
		gotGroup = r.Header.Get(cloudflareDNSGroupHeader)
		gotClientActor = r.Header.Get("X-Client-Actor")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"mappings":[{"suffix":"example.com","configured":true,"updated_at":1}],"access":{"can_read":true,"can_write":true,"can_rotate":true}}`)
	}))
	t.Cleanup(func() { pluginhost.SetCloudflareDNSHandler(nil) })

	router := newCloudflareDNSRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/panel-api/cloudflare-dns/api/mappings", nil)
	req.Header.Set("X-Panel-Token", "panel-secret")
	req.Header.Set(cloudflareDNSActorHeader, "attacker/spoof")
	req.Header.Set("X-Client-Actor", "kept")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	if gotPath != "/api/mappings" {
		t.Fatalf("forwarded path = %q, want /api/mappings", gotPath)
	}
	if gotActor != cloudflareDNSActor {
		t.Fatalf("actor = %q, want host-owned %q", gotActor, cloudflareDNSActor)
	}
	if gotGroup != cloudflareDNSResourceGroup {
		t.Fatalf("group = %q, want %q", gotGroup, cloudflareDNSResourceGroup)
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

func TestStripCloudflareDNSPrefix(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"/panel-api/cloudflare-dns":                      "/",
		"/panel-api/cloudflare-dns/":                     "/",
		"/api/cloudflare-dns/api/mappings":               "/api/mappings",
		"/panel-api/cloudflare-dns/api/mappings/ex.com":  "/api/mappings/ex.com",
		"/api/cloudflare-dns/api/mappings/ex.com/rotate": "/api/mappings/ex.com/rotate",
	}
	for input, want := range cases {
		if got := stripCloudflareDNSPrefix(input); got != want {
			t.Fatalf("stripCloudflareDNSPrefix(%q) = %q, want %q", input, got, want)
		}
	}
}
