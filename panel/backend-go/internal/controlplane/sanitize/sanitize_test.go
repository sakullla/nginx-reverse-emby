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
