//go:build integration

package http

import (
	"net/http/httptest"

	"testing"
)

func TestRewriteLocationUsesFrontendOrigin(t *testing.T) {
	t.Parallel()
	got := rewriteLocation("https://backend.example/internal", "https://frontend.example", "/")
	if got != "https://frontend.example/internal" {
		t.Fatalf("unexpected location rewrite: %q", got)
	}
}

func TestRewriteLocationPreservesFrontendPathPrefix(t *testing.T) {
	t.Parallel()
	got := rewriteLocation("https://backend.example/videos/1/original.mp4", "https://frontend.example/emby", "/")
	if got != "https://frontend.example/emby/videos/1/original.mp4" {
		t.Fatalf("unexpected location rewrite with prefix: %q", got)
	}
}

func TestApplyHeaderOverridesHostUpdatesRequestHost(t *testing.T) {
	t.Parallel()
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

func TestRewriteExternalLocationToProxyPath(t *testing.T) {
	t.Parallel()
	got := rewriteExternalLocationToProxyPath(
		"https://streamer.example/stream?sign=abc",
		"https://frontend.example/emby",
	)
	if got != "https://frontend.example/emby/__nre_redirect/https/streamer.example/stream?sign=abc" {
		t.Fatalf("unexpected external redirect rewrite: %q", got)
	}
}
