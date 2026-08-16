//go:build !integration

package http

import (
	"testing"
)

func TestTokenMatchesRequiresExactSecret(t *testing.T) {
	t.Parallel()
	if tokenMatches("secret", "secret-extra") || !tokenMatches("secret", "secret") {
		t.Fatal("token matching is not exact")
	}
}
