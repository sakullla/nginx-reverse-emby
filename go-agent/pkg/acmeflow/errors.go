package acmeflow

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/acme"
)

// ErrorCategory is a stable, secret-free classification that callers may use
// for retry and status decisions. It deliberately does not expose provider or
// CA response bodies.
type ErrorCategory string

const (
	CategoryAccount       ErrorCategory = "account"
	CategoryOrder         ErrorCategory = "order"
	CategoryBadNonce      ErrorCategory = "bad_nonce"
	CategoryRateLimited   ErrorCategory = "rate_limited"
	CategoryAuthorization ErrorCategory = "authorization"
	CategoryChallenge     ErrorCategory = "challenge"
	CategoryProfile       ErrorCategory = "profile"
	CategoryNetwork       ErrorCategory = "network"
	CategoryTimeout       ErrorCategory = "timeout"
	CategoryCancelled     ErrorCategory = "cancelled"
	CategoryCleanup       ErrorCategory = "cleanup"
	CategoryMaterial      ErrorCategory = "material"
	CategoryProtocol      ErrorCategory = "protocol"
)

// SafeError retains a cause for programmatic inspection while rendering only
// a stable, curated summary from Error. Callers must not log the unwrapped
// cause because ACME and provider errors may contain response bodies.
type SafeError struct {
	Category      ErrorCategory
	Operation     string
	RetryAfter    time.Duration
	CleanupFailed bool

	cause error
}

func (e *SafeError) Error() string {
	if e == nil {
		return "acme operation failed"
	}
	message := categoryMessage(e.Category)
	if e.CleanupFailed && e.Category != CategoryCleanup {
		message += "; challenge cleanup also failed"
	}
	if operation := safeOperation(e.Operation); operation != "" {
		return fmt.Sprintf("acme %s: %s", operation, message)
	}
	return "acme: " + message
}

func (e *SafeError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// WrapError creates a typed error whose rendered text never includes cause.
func WrapError(category ErrorCategory, operation string, cause error) *SafeError {
	if category == "" {
		category = CategoryProtocol
	}
	return &SafeError{
		Category:  category,
		Operation: safeOperation(operation),
		cause:     cause,
	}
}

// ErrorCategoryOf returns the stable category of err, or an empty value when
// err is nil or has not passed through the package's safe error boundary.
func ErrorCategoryOf(err error) ErrorCategory {
	var safe *SafeError
	if errors.As(err, &safe) {
		return safe.Category
	}
	return ""
}

func normalizeError(operation string, err error) error {
	if err == nil {
		return nil
	}
	var safe *SafeError
	if errors.As(err, &safe) {
		return safe
	}

	switch {
	case errors.Is(err, context.Canceled):
		return WrapError(CategoryCancelled, operation, err)
	case errors.Is(err, context.DeadlineExceeded):
		return WrapError(CategoryTimeout, operation, err)
	}

	var authorizationErr *acme.AuthorizationError
	if errors.As(err, &authorizationErr) {
		return WrapError(CategoryAuthorization, operation, err)
	}
	var orderErr *acme.OrderError
	if errors.As(err, &orderErr) {
		if orderErr.Problem != nil {
			return normalizeACMEError(operation, orderErr.Problem, err)
		}
		return WrapError(CategoryOrder, operation, err)
	}
	var acmeErr *acme.Error
	if errors.As(err, &acmeErr) {
		return normalizeACMEError(operation, acmeErr, err)
	}
	var networkErr net.Error
	if errors.As(err, &networkErr) {
		if networkErr.Timeout() {
			return WrapError(CategoryTimeout, operation, err)
		}
		return WrapError(CategoryNetwork, operation, err)
	}

	return WrapError(defaultCategoryForOperation(operation), operation, err)
}

func normalizeACMEError(operation string, acmeErr *acme.Error, cause error) error {
	problemType := strings.ToLower(acmeErr.ProblemType)
	category := CategoryProtocol
	switch {
	case strings.HasSuffix(problemType, ":badnonce"):
		category = CategoryBadNonce
	case strings.HasSuffix(problemType, ":ratelimited") || acmeErr.StatusCode == http.StatusTooManyRequests:
		category = CategoryRateLimited
	case strings.HasSuffix(problemType, ":invalidprofile"):
		category = CategoryProfile
	default:
		category = defaultCategoryForOperation(operation)
	}
	safe := WrapError(category, operation, cause)
	if category == CategoryRateLimited {
		if retryAfter, ok := acme.RateLimit(acmeErr); ok {
			safe.RetryAfter = retryAfter
		}
		if safe.RetryAfter == 0 && acmeErr.Header != nil {
			safe.RetryAfter = parseRetryAfter(acmeErr.Header.Get("Retry-After"), time.Now())
		}
	}
	return safe
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds <= 0 {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}
	when, err := http.ParseTime(value)
	if err != nil || !when.After(now) {
		return 0
	}
	return when.Sub(now)
}

func mergeCleanupError(primary, cleanup error) error {
	if cleanup == nil {
		return primary
	}
	if primary == nil {
		return WrapError(CategoryCleanup, "challenge_cleanup", cleanup)
	}
	primary = normalizeError("challenge", primary)
	var safe *SafeError
	if !errors.As(primary, &safe) {
		return WrapError(CategoryProtocol, "challenge", primary)
	}
	clone := *safe
	clone.CleanupFailed = true
	return &clone
}

func defaultCategoryForOperation(operation string) ErrorCategory {
	switch {
	case strings.Contains(operation, "account"), strings.Contains(operation, "register"):
		return CategoryAccount
	case strings.Contains(operation, "profile"):
		return CategoryProfile
	case strings.Contains(operation, "authorization"):
		return CategoryAuthorization
	case strings.Contains(operation, "challenge"):
		return CategoryChallenge
	case strings.Contains(operation, "cleanup"):
		return CategoryCleanup
	case strings.Contains(operation, "material"), strings.Contains(operation, "certificate_key"), strings.Contains(operation, "csr"):
		return CategoryMaterial
	case strings.Contains(operation, "order"), strings.Contains(operation, "finalize"):
		return CategoryOrder
	default:
		return CategoryProtocol
	}
}

func categoryMessage(category ErrorCategory) string {
	switch category {
	case CategoryAccount:
		return "account operation failed"
	case CategoryOrder:
		return "order operation failed"
	case CategoryBadNonce:
		return "request nonce was rejected"
	case CategoryRateLimited:
		return "certificate authority rate limit reached"
	case CategoryAuthorization:
		return "identifier authorization failed"
	case CategoryChallenge:
		return "challenge operation failed"
	case CategoryProfile:
		return "requested certificate profile is unavailable or invalid"
	case CategoryNetwork:
		return "network request failed"
	case CategoryTimeout:
		return "operation timed out"
	case CategoryCancelled:
		return "operation cancelled"
	case CategoryCleanup:
		return "challenge cleanup failed"
	case CategoryMaterial:
		return "certificate material validation failed"
	default:
		return "protocol operation failed"
	}
}

func safeOperation(operation string) string {
	operation = strings.TrimSpace(operation)
	if operation == "" {
		return ""
	}
	if len(operation) > 64 {
		return "operation"
	}
	for _, r := range operation {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' || r == '/' {
			continue
		}
		return "operation"
	}
	return operation
}
