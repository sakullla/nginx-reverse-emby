//go:build integration

package acmeflow

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/acme"
)

func TestSafeErrorClassifiesBadNonceWithoutLeakingDetail(t *testing.T) {
	const canary = "super-secret-token-canary"
	err := normalizeError("new_order", &acme.Error{
		StatusCode:  http.StatusBadRequest,
		ProblemType: "urn:ietf:params:acme:error:badNonce",
		Detail:      "Authorization: Bearer " + canary,
	})

	var safe *SafeError
	if !errors.As(err, &safe) {
		t.Fatalf("normalizeError() error type = %T, want *SafeError", err)
	}
	if safe.Category != CategoryBadNonce {
		t.Fatalf("category = %q, want %q", safe.Category, CategoryBadNonce)
	}
	if strings.Contains(err.Error(), canary) || strings.Contains(err.Error(), "Authorization") {
		t.Fatalf("safe error leaked raw ACME detail: %q", err)
	}
}

func TestSafeErrorPreservesRetryAfter(t *testing.T) {
	err := normalizeError("new_order", &acme.Error{
		StatusCode:  http.StatusTooManyRequests,
		ProblemType: "urn:ietf:params:acme:error:rateLimited",
		Header:      http.Header{"Retry-After": []string{"120"}},
	})

	var safe *SafeError
	if !errors.As(err, &safe) {
		t.Fatalf("normalizeError() error type = %T, want *SafeError", err)
	}
	if safe.Category != CategoryRateLimited {
		t.Fatalf("category = %q, want %q", safe.Category, CategoryRateLimited)
	}
	if safe.RetryAfter != 2*time.Minute {
		t.Fatalf("retry after = %s, want 2m", safe.RetryAfter)
	}
}

func TestSafeErrorClassifiesAuthorizationAndCancellation(t *testing.T) {
	authzErr := normalizeError("wait_authorization", &acme.AuthorizationError{
		Identifier: "example.com",
		Errors: []error{&acme.Error{
			StatusCode:  http.StatusBadRequest,
			ProblemType: "urn:ietf:params:acme:error:connection",
			Detail:      "provider body token-canary",
		}},
	})
	if got := ErrorCategoryOf(authzErr); got != CategoryAuthorization {
		t.Fatalf("authorization category = %q, want %q", got, CategoryAuthorization)
	}
	if strings.Contains(authzErr.Error(), "token-canary") {
		t.Fatalf("authorization error leaked nested detail: %q", authzErr)
	}
	if !strings.Contains(authzErr.Error(), "challenge connection failed") {
		t.Fatalf("authorization error omitted safe challenge diagnostic: %q", authzErr)
	}

	cancelErr := normalizeError("challenge_wait", context.Canceled)
	if got := ErrorCategoryOf(cancelErr); got != CategoryCancelled {
		t.Fatalf("cancellation category = %q, want %q", got, CategoryCancelled)
	}
	if !errors.Is(cancelErr, context.Canceled) {
		t.Fatal("normalized cancellation no longer unwraps to context.Canceled")
	}
}
