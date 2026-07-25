package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/pkg/acmeflow"
)

type managedCertificateSafeACMEError struct {
	safe *acmeflow.SafeError
}

func (e *managedCertificateSafeACMEError) Error() string {
	if e == nil || e.safe == nil {
		return "certificate operation failed"
	}
	if e.safe.RetryAfter <= 0 {
		return e.safe.Error()
	}
	seconds := int64((e.safe.RetryAfter + time.Second - 1) / time.Second)
	return fmt.Sprintf("%s; retry-after=%d", e.safe.Error(), seconds)
}

func (e *managedCertificateSafeACMEError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.safe
}

func normalizeManagedCertificateACMEError(operation string, category acmeflow.ErrorCategory, err error) error {
	if err == nil {
		return nil
	}

	var safe *acmeflow.SafeError
	if !errors.As(err, &safe) {
		switch {
		case errors.Is(err, context.Canceled):
			category = acmeflow.CategoryCancelled
		case errors.Is(err, context.DeadlineExceeded):
			category = acmeflow.CategoryTimeout
		}
		safe = acmeflow.WrapError(category, operation, err)
	}
	return &managedCertificateSafeACMEError{safe: safe}
}
