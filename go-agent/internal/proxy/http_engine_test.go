package proxy

import (
	"net/http/httptest"
	"testing"
)

func TestRewriteLocationUsesFrontendOrigin(t *testing.T) {
	got := rewriteLocation("https://backend.example/internal", "https://frontend.example")
	if got != "https://frontend.example/internal" {
		t.Fatalf("unexpected location rewrite: %q", got)
	}
}

func TestRewriteLocationEmptyFrontendOriginReturnsOriginal(t *testing.T) {
	original := "https://backend.example/internal"
	got := rewriteLocation(original, "")
	if got != original {
		t.Fatalf("expected original location, got %q", got)
	}
}

func TestApplyHeaderOverridesHostUpdatesRequestHost(t *testing.T) {
	req := httptest.NewRequest("GET", "https://frontend.example/test", nil)
	req.Host = "frontend.example"

	ApplyHeaderOverrides(req, map[string]string{
		"host":          "override.example",
		"X-Test-Header": "abc",
	})

	if req.Host != "override.example" {
		t.Fatalf("expected req.Host override, got %q", req.Host)
	}
	if req.Header.Get("X-Test-Header") != "abc" {
		t.Fatalf("expected header override, got %q", req.Header.Get("X-Test-Header"))
	}
}
