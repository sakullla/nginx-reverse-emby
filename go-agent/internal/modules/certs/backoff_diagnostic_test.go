//go:build !fast

package certs

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/pkg/acmeflow"
)

func TestApplyBackoffPreservesFailureAndRetryDeadline(t *testing.T) {
	if testing.Short() {
		t.Skip("certificate failure state lifecycle belongs to the full tier")
	}
	t.Parallel()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	issueAttempts := 0
	cause := acmeflow.WrapError(acmeflow.CategoryAuthorization, "wait_authorization", errors.New("provider detail must stay private"))
	manager := mustNewManager(t, t.TempDir(), withNow(func() time.Time { return now }),
		withACMEIssuerFactory(func(acmeIssueRequest) (acmeIssuer, error) {
			issueAttempts++
			return nil, cause
		}))
	t.Cleanup(func() { _ = manager.Close() })
	policy := model.ManagedCertificatePolicy{
		ID: 1, Domain: "media.example.com", Enabled: true, Scope: "domain",
		IssuerMode: "local_http01", CertificateType: "acme", Usage: "https",
	}
	if err := manager.Apply(context.Background(), nil, []model.ManagedCertificatePolicy{policy}); err == nil {
		t.Fatal("expected initial issuance failure")
	}
	statePath := manager.managedCertificateStatePath(policy.ID)
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	state, found, err := manager.loadManagedCertificateState(policy.ID)
	if err != nil || !found || state.ACME == nil {
		t.Fatalf("missing recorded failure state: found=%v err=%v", found, err)
	}
	retryAt := time.Unix(state.ACME.Renewal.BackoffRetryNext, 0).UTC()
	if !retryAt.After(now) {
		t.Fatal("initial failure did not schedule a future retry")
	}
	now = now.Add(time.Minute)
	err = manager.Apply(context.Background(), nil, []model.ManagedCertificatePolicy{policy})
	if err == nil || !strings.Contains(err.Error(), retryAt.Format(time.RFC3339)) || !strings.Contains(err.Error(), cause.Error()) {
		t.Fatalf("backoff hid the first failure or retry deadline: %v", err)
	}
	if strings.Contains(err.Error(), "provider detail") {
		t.Fatal("backoff exposed unsanitized provider detail")
	}
	after, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if issueAttempts != 1 || !bytes.Equal(before, after) {
		t.Fatal("deferred apply retried issuance or changed the persisted backoff")
	}
	now = retryAt
	_ = manager.Apply(context.Background(), nil, []model.ManagedCertificatePolicy{policy})
	if issueAttempts != 2 {
		t.Fatalf("issuance did not resume at the retry deadline: attempts=%d", issueAttempts)
	}
}
