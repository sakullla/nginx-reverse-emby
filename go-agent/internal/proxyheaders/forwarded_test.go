package proxyheaders

import "testing"

func TestForwardedBuildsCompleteTrustedProxyHeaderSet(t *testing.T) {
	t.Parallel()
	header := Forwarded("http", "localhost", "203.0.113.7:3210")
	for key, want := range map[string]string{
		"X-Forwarded-Host":  "localhost",
		"X-Forwarded-Port":  "80",
		"X-Forwarded-Proto": "http",
		"X-Forwarded-For":   "203.0.113.7",
		"X-Real-IP":         "203.0.113.7",
	} {
		if got := header.Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}
