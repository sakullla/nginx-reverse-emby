package localagent

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLocalLiteralProbeKeepsHTTPStatusDistinct(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)

	result, err := probeLocalBackendReachability(t.Context(), "local", map[string]any{"target_url": server.URL + "/emby"})
	if err != nil {
		t.Fatal(err)
	}
	if result["status"] != "http_status" || result["http_status_code"] != http.StatusUnauthorized {
		t.Fatalf("result = %#v, want http_status 401", result)
	}
	if result["status"] == "connected" {
		t.Fatal("literal HTTP probe reported connected")
	}
	if _, ok := result["relay_listener_id"]; ok {
		t.Fatalf("literal probe included relay listener: %#v", result)
	}
}

func TestLocalProbeRejectsBadURLAsUntested(t *testing.T) {
	result, err := probeLocalBackendReachability(t.Context(), "local", map[string]any{"target_url": "ftp://files.example/media"})
	if err != nil {
		t.Fatal(err)
	}
	if result["status"] != "untested" || result["status"] == "connected" {
		t.Fatalf("status = %#v, want untested", result["status"])
	}
	if _, ok := result["http_status_code"]; ok {
		t.Fatalf("bad url included an HTTP status: %#v", result)
	}
}

func TestLocalRelayHopWithoutTrustStaysUntested(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	result, err := probeLocalBackendReachability(t.Context(), "local", map[string]any{
		"target_url":        server.URL,
		"relay_listener_id": 7,
	})
	if err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("relay hop dialed the literal target")
	}
	if result["status"] != "untested" || result["relay_listener_id"] != 7 {
		t.Fatalf("result = %#v, want untested listener 7", result)
	}
	if result["status"] == "connected" {
		t.Fatal("missing trust was reported as connected")
	}
}
