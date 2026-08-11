package sanitize

import (
	"strings"
	"testing"
)

func TestTextRedactsStructuredEscapedAndExactSecrets(t *testing.T) {
	secret := "arbitrary \"value\" with spaces"
	input := `{"nested":{"token":"guest-token","note":"arbitrary \"value\" with spaces"},"items":["Bearer abc"]}`
	got := Text(input, []string{secret})
	for _, forbidden := range []string{"guest-token", "arbitrary", "abc"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("sanitized text contains %q: %s", forbidden, got)
		}
	}
}

func TestTextRedactsCredentialKeySeparatorsCamelCaseAndMalformedJSON(t *testing.T) {
	for _, input := range []string{
		`{"api-key":"hyphen-secret"}`,
		`{"api.key":"dot-secret"}`,
		`{"apiKey":"camel-secret"}`,
		`{"privateKey":"private-secret"}`,
		`{"api-key":"malformed-secret"`,
		`prefix "api_key" : "quoted-secret" suffix`,
	} {
		got := Text(input, nil)
		if strings.Contains(got, "secret") && !strings.Contains(got, Redacted) {
			t.Fatalf("Text(%q) did not redact credential value: %s", input, got)
		}
		for _, forbidden := range []string{"hyphen-secret", "dot-secret", "camel-secret", "private-secret", "malformed-secret", "quoted-secret"} {
			if strings.Contains(got, forbidden) {
				t.Fatalf("Text(%q) contains %q: %s", input, forbidden, got)
			}
		}
	}
}
