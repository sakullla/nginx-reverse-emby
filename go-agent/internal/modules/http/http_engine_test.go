package http

import (
	"net/http/httptest"
	"net/url"
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

func TestRewriteRequestPathPreservesTrailingSlash(t *testing.T) {
	t.Parallel()
	got := rewriteRequestPath("/api/admin/settings/", "/", "/")
	if got != "/api/admin/settings/" {
		t.Fatalf("rewriteRequestPath() = %q, want trailing slash preserved", got)
	}
}

func TestRewriteRequestPathPreservesTrailingSlashWithPathPrefixes(t *testing.T) {
	t.Parallel()
	got := rewriteRequestPath("/panel/api/admin/settings/", "/panel", "/komari")
	if got != "/komari/api/admin/settings/" {
		t.Fatalf("rewriteRequestPath() = %q, want prefixed path with trailing slash", got)
	}
}

func TestRewriteLocationEmptyFrontendOriginReturnsOriginal(t *testing.T) {
	t.Parallel()
	original := "https://backend.example/internal"
	got := rewriteLocation(original, "", "/")
	if got != original {
		t.Fatalf("expected original location, got %q", got)
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

func TestParseInternalRedirectTargetRejectsEncodedSchemeRelativePath(t *testing.T) {
	t.Parallel()
	if target, ok := parseInternalRedirectTarget("/__nre_redirect/https/streamer.example/%2f%2fevil.example/path", "/"); ok {
		t.Fatalf("expected unsafe redirect target to be rejected, got %+v", target)
	}
}

func TestResolveRelativeLocationUsesCurrentProxyTarget(t *testing.T) {
	t.Parallel()
	base, err := url.Parse("http://streamer.example/videos/stream.m3u8?sign=old")
	if err != nil {
		t.Fatalf("failed to parse base URL: %v", err)
	}

	got := resolveRelativeLocation("/tokenized/stream.m3u8?sign=next", base)
	if got != "http://streamer.example/tokenized/stream.m3u8?sign=next" {
		t.Fatalf("unexpected resolved location: %q", got)
	}
}
