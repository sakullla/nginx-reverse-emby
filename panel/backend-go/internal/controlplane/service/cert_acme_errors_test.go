//go:build !integration

package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/pkg/acmeflow"
)

func TestManagedCertificateACMEErrorRedactsDetailsAndPreservesCategory(t *testing.T) {
	raw := errors.New("provider response contained dns-token-canary")
	err := normalizeManagedCertificateACMEError("master_issue", acmeflow.CategoryAuthorization, raw)
	if err == nil {
		t.Fatal("normalizeManagedCertificateACMEError() error = nil")
	}
	var safe *acmeflow.SafeError
	if !errors.As(err, &safe) {
		t.Fatalf("error type = %T, want wrapped *acmeflow.SafeError", err)
	}
	if safe.Category != acmeflow.CategoryAuthorization {
		t.Fatalf("safe.Category = %q, want %q", safe.Category, acmeflow.CategoryAuthorization)
	}
	if strings.Contains(err.Error(), "dns-token-canary") || strings.Contains(err.Error(), "provider response") {
		t.Fatalf("safe error exposed provider details: %v", err)
	}

	cancelled := normalizeManagedCertificateACMEError("master_issue", acmeflow.CategoryProtocol, context.Canceled)
	if category := acmeflow.ErrorCategoryOf(cancelled); category != acmeflow.CategoryCancelled {
		t.Fatalf("cancelled category = %q, want %q", category, acmeflow.CategoryCancelled)
	}
}

func TestManagedCertificateACMEErrorProjectsSafeRetryAfter(t *testing.T) {
	safe := acmeflow.WrapError(acmeflow.CategoryRateLimited, "new_order", errors.New("raw-ca-body-canary"))
	safe.RetryAfter = 90 * time.Second
	err := normalizeManagedCertificateACMEError("master_issue", acmeflow.CategoryProtocol, safe)
	if strings.Contains(err.Error(), "raw-ca-body-canary") {
		t.Fatalf("rate-limit error exposed raw CA body: %v", err)
	}
	if class := classifyManagedCertificateIssueError(err); class != managedCertificateBackoffClassRateLimited {
		t.Fatalf("classifyManagedCertificateIssueError() = %q, want %q", class, managedCertificateBackoffClassRateLimited)
	}
	if retryAfter := extractManagedCertificateRetryAfter(err); retryAfter != 90*time.Second {
		t.Fatalf("extractManagedCertificateRetryAfter() = %s, want 90s (err = %v)", retryAfter, err)
	}
}

func TestManagedCertificateACMEErrorProjectsNetworkAsTransient(t *testing.T) {
	const networkCanary = "raw-network-provider-body-canary"
	safe := acmeflow.WrapError(acmeflow.CategoryNetwork, "cloudflare_request", errors.New(networkCanary))
	err := normalizeManagedCertificateACMEError("master_issue", acmeflow.CategoryProtocol, safe)
	if category := acmeflow.ErrorCategoryOf(err); category != acmeflow.CategoryNetwork {
		t.Fatalf("network error category = %q, want %q", category, acmeflow.CategoryNetwork)
	}
	if class := classifyManagedCertificateIssueError(err); class != managedCertificateBackoffClassTransient {
		t.Fatalf("classifyManagedCertificateIssueError() = %q, want %q (err=%v)", class, managedCertificateBackoffClassTransient, err)
	}
	if strings.Contains(err.Error(), networkCanary) {
		t.Fatalf("network error exposed provider details: %v", err)
	}
}
