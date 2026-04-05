package proxy

import "testing"

func TestRewriteLocationUsesFrontendOrigin(t *testing.T) {
	got := rewriteLocation("https://backend.example/internal", "https://frontend.example")
	if got != "https://frontend.example/internal" {
		t.Fatalf("unexpected location rewrite: %q", got)
	}
}
