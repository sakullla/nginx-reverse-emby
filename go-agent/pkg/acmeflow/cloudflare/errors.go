package cloudflare

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/pkg/acmeflow"
)

var (
	errProviderRejected = errors.New("cloudflare API rejected the request")
	errProviderResponse = errors.New("cloudflare API returned an invalid response")
	errDNSResponse      = errors.New("DNS server returned an invalid response")
	errRecordNotFound   = errors.New("cloudflare DNS record was not found")
)

type providerHTTPStatusError struct {
	status int
}

func (*providerHTTPStatusError) Error() string {
	return errProviderRejected.Error()
}

func (*providerHTTPStatusError) Unwrap() error {
	return errProviderRejected
}

func providerError(category acmeflow.ErrorCategory, operation string, cause error) error {
	if cause == nil {
		cause = errProviderRejected
	}
	if errors.Is(cause, context.Canceled) {
		return acmeflow.WrapError(acmeflow.CategoryCancelled, operation, context.Canceled)
	}
	if errors.Is(cause, context.DeadlineExceeded) {
		return acmeflow.WrapError(acmeflow.CategoryTimeout, operation, context.DeadlineExceeded)
	}
	var safe *acmeflow.SafeError
	if errors.As(cause, &safe) {
		return safe
	}
	var networkError net.Error
	if errors.As(cause, &networkError) {
		if networkError.Timeout() {
			return acmeflow.WrapError(acmeflow.CategoryTimeout, operation, errors.New("network timeout"))
		}
		return acmeflow.WrapError(acmeflow.CategoryNetwork, operation, errors.New("network request failed"))
	}
	if category == "" {
		category = acmeflow.CategoryChallenge
	}
	return acmeflow.WrapError(category, operation, cause)
}

func providerHTTPError(operation string, status int, retryAfter string, now time.Time, defaultCategory acmeflow.ErrorCategory) error {
	category := defaultCategory
	if category == "" {
		category = acmeflow.CategoryChallenge
	}
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		category = acmeflow.CategoryAuthorization
	case status == http.StatusTooManyRequests:
		category = acmeflow.CategoryRateLimited
	case status >= 500:
		category = acmeflow.CategoryNetwork
	}
	safe := acmeflow.WrapError(category, operation, &providerHTTPStatusError{status: status})
	if category == acmeflow.CategoryRateLimited {
		safe.RetryAfter = parseProviderRetryAfter(retryAfter, now)
	}
	return safe
}

func definitiveProviderCreateFailure(err error) bool {
	var statusErr *providerHTTPStatusError
	if errors.As(err, &statusErr) {
		return statusErr.status >= 400 && statusErr.status < 500 && statusErr.status != http.StatusRequestTimeout
	}
	switch acmeflow.ErrorCategoryOf(err) {
	case acmeflow.CategoryAuthorization, acmeflow.CategoryRateLimited:
		return true
	default:
		return false
	}
}

func parseProviderRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	seconds, err := strconv.ParseInt(value, 10, 64)
	if err == nil {
		if seconds <= 0 {
			return 0
		}
		if seconds > int64(time.Duration(1<<63-1)/time.Second) {
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

func contextFailure(ctx context.Context, operation string) error {
	if ctx == nil {
		return providerError(acmeflow.CategoryProtocol, operation, errors.New("context is nil"))
	}
	if err := ctx.Err(); err != nil {
		return providerError("", operation, err)
	}
	return nil
}
