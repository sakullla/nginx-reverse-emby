package http

import (
	"net/url"
	"testing"
)

func mustParseBackendURL(t *testing.T, raw string) *url.URL {
	t.Helper()

	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("failed to parse backend URL %q: %v", raw, err)
	}
	return parsed
}
