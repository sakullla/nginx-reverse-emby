package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
)

func TestPasswordLoginNoLongerAuthenticates(t *testing.T) {
	t.Parallel()
	deps := Dependencies{Config: config.Config{PanelToken: "panel-secret"}}
	mux := http.NewServeMux()
	for _, prefix := range []string{"/api", "/panel-api"} {
		mux.Handle(prefix+"/auth/login", http.HandlerFunc(deps.handleLogin))
		mux.Handle(prefix+"/auth/verify", http.HandlerFunc(deps.handleVerify))
	}

	credentials := `{"username":"alice","password":"correct-horse-battery"}`
	for _, path := range []string{"/api/auth/login", "/panel-api/auth/login"} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(credentials))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s status=%d body=%s", path, rec.Code, rec.Body.String())
		}
		var payload map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("%s decode: %v body=%s", path, err, rec.Body.String())
		}
		if payload["ok"] != false {
			t.Fatalf("%s ok=%v body=%s", path, payload["ok"], rec.Body.String())
		}
		if _, hasSession := payload["session"]; hasSession {
			t.Fatalf("%s returned session: %s", path, rec.Body.String())
		}
	}

	for _, path := range []string{"/api/auth/verify", "/panel-api/auth/verify"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("X-Panel-Token", "panel-secret")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", path, rec.Code, rec.Body.String())
		}
		var payload map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("%s decode: %v body=%s", path, err, rec.Body.String())
		}
		if payload["ok"] != true {
			t.Fatalf("%s payload=%v", path, payload)
		}
	}
}
