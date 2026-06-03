package certs

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func TestRenewalLoopRenewsExpiredLocalHTTP01Certificate(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC)
	first := mustCreateTLSMaterial(t, certificateSpec{
		commonName: "renew-loop.example.com",
		notBefore:  now.Add(-24 * time.Hour),
		notAfter:   now.Add(2 * time.Hour),
	})
	second := mustCreateTLSMaterial(t, certificateSpec{
		commonName: "renew-loop.example.com",
		notBefore:  now.Add(-time.Hour),
		notAfter:   now.Add(90 * 24 * time.Hour),
	})
	fake := &fakeACMEIssuer{
		results: []acmeIssueResult{
			{CertPEM: first.CertPEM, KeyPEM: first.KeyPEM},
			{CertPEM: second.CertPEM, KeyPEM: second.KeyPEM},
		},
	}

	manager := mustNewManager(
		t,
		t.TempDir(),
		withNow(func() time.Time { return now }),
		withRenewBefore(24*time.Hour),
		withACMEIssuerFactory(func(request acmeIssueRequest) (acmeIssuer, error) {
			return fake, nil
		}),
	)
	policy := model.ManagedCertificatePolicy{
		ID:              9201,
		Domain:          "renew-loop.example.com",
		Enabled:         true,
		Scope:           "domain",
		IssuerMode:      "local_http01",
		CertificateType: "acme",
		Usage:           "https",
	}

	if err := manager.Apply(context.Background(), nil, []model.ManagedCertificatePolicy{policy}); err != nil {
		t.Fatalf("initial apply failed: %v", err)
	}
	if len(fake.requests) != 1 {
		t.Fatalf("expected one initial acme request, got %d", len(fake.requests))
	}

	if err := manager.runRenewalLoopIteration(context.Background()); err != nil {
		t.Fatalf("renewal iteration failed: %v", err)
	}

	if len(fake.requests) != 2 {
		t.Fatalf("expected renewal loop to issue a second certificate, got %d requests", len(fake.requests))
	}
	info, err := manager.CertificateInfo(9201)
	if err != nil {
		t.Fatalf("certificate info failed: %v", err)
	}
	if info.Fingerprint != second.Fingerprint {
		t.Fatalf("expected renewed fingerprint, got %q want %q", info.Fingerprint, second.Fingerprint)
	}
}

func TestRenewalLoopLifecycleStartsAndStopsOnManagerClose(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC)
	first := mustCreateTLSMaterial(t, certificateSpec{
		commonName: "renew-loop-lifecycle.example.com",
		notBefore:  now.Add(-24 * time.Hour),
		notAfter:   now.Add(2 * time.Hour),
	})
	reissued := mustCreateTLSMaterial(t, certificateSpec{
		commonName: "renew-loop-lifecycle.example.com",
		notBefore:  now.Add(-24 * time.Hour),
		notAfter:   now.Add(2 * time.Hour),
	})
	issuer := &threadSafeIssuer{
		results: []acmeIssueResult{
			{CertPEM: first.CertPEM, KeyPEM: first.KeyPEM},
			{CertPEM: reissued.CertPEM, KeyPEM: reissued.KeyPEM},
			{CertPEM: reissued.CertPEM, KeyPEM: reissued.KeyPEM},
			{CertPEM: reissued.CertPEM, KeyPEM: reissued.KeyPEM},
		},
	}
	manager := mustNewManager(
		t,
		t.TempDir(),
		withNow(func() time.Time { return now }),
		withRenewBefore(24*time.Hour),
		withRenewalLoopInterval(20*time.Millisecond),
		withACMEIssuerFactory(func(request acmeIssueRequest) (acmeIssuer, error) {
			return issuer, nil
		}),
	)
	policy := model.ManagedCertificatePolicy{
		ID:              9202,
		Domain:          "renew-loop-lifecycle.example.com",
		Enabled:         true,
		Scope:           "domain",
		IssuerMode:      "local_http01",
		CertificateType: "acme",
		Usage:           "https",
	}
	if err := manager.Apply(context.Background(), nil, []model.ManagedCertificatePolicy{policy}); err != nil {
		t.Fatalf("initial apply failed: %v", err)
	}

	waitForRenewalRequests(t, time.Second, func() bool {
		return issuer.requestCount() >= 2
	})

	if err := manager.Close(); err != nil {
		t.Fatalf("manager close failed: %v", err)
	}
	countAfterClose := issuer.requestCount()
	time.Sleep(80 * time.Millisecond)
	if got := issuer.requestCount(); got != countAfterClose {
		t.Fatalf("expected renewal loop to stop after close, requests before=%d after=%d", countAfterClose, got)
	}
}

func TestLoadOrIssueACMESingleFlightsPerCertificateID(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC)
	issued := mustCreateTLSMaterial(t, certificateSpec{
		commonName: "singleflight.example.com",
		notBefore:  now.Add(-time.Hour),
		notAfter:   now.Add(90 * 24 * time.Hour),
	})
	issuer := &blockingIssuer{
		result: acmeIssueResult{CertPEM: issued.CertPEM, KeyPEM: issued.KeyPEM},
	}
	manager := mustNewManager(
		t,
		t.TempDir(),
		withNow(func() time.Time { return now }),
		withRenewBefore(24*time.Hour),
		withACMEIssuerFactory(func(request acmeIssueRequest) (acmeIssuer, error) {
			return issuer, nil
		}),
	)
	policy := model.ManagedCertificatePolicy{
		ID:              9203,
		Domain:          "singleflight.example.com",
		Enabled:         true,
		Scope:           "domain",
		IssuerMode:      "local_http01",
		CertificateType: "acme",
		Usage:           "https",
	}

	var wg sync.WaitGroup
	wg.Add(2)
	errCh := make(chan error, 2)
	go func() {
		defer wg.Done()
		_, _, err := manager.loadOrIssueACME(context.Background(), policy)
		errCh <- err
	}()
	go func() {
		defer wg.Done()
		_, _, err := manager.loadOrIssueACME(context.Background(), policy)
		errCh <- err
	}()
	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Fatalf("unexpected loadOrIssueACME error: %v", err)
		}
	}
	if got := issuer.callCount(); got != 1 {
		t.Fatalf("expected one issuance call for concurrent same-id issuance, got %d", got)
	}
}

func TestRenewalFailureDoesNotOverwriteConcurrentApplySuccess(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC)
	initial := mustCreateTLSMaterial(t, certificateSpec{
		commonName: "renew-race.example.com",
		notBefore:  now.Add(-24 * time.Hour),
		notAfter:   now.Add(2 * time.Hour),
	})
	renewed := mustCreateTLSMaterial(t, certificateSpec{
		commonName: "renew-race.example.com",
		notBefore:  now.Add(-time.Hour),
		notAfter:   now.Add(90 * 24 * time.Hour),
	})
	renewalStarted := make(chan struct{})
	releaseRenewal := make(chan struct{})
	issuer := &sequencedIssuer{
		onIssue: func(call int) acmeIssueResult {
			switch call {
			case 1:
				return acmeIssueResult{CertPEM: initial.CertPEM, KeyPEM: initial.KeyPEM}
			case 2:
				close(renewalStarted)
				<-releaseRenewal
				return acmeIssueResult{Err: assertUnreachableError{message: "synthetic renewal failure"}}
			case 3:
				return acmeIssueResult{CertPEM: renewed.CertPEM, KeyPEM: renewed.KeyPEM}
			default:
				return acmeIssueResult{Err: assertUnreachableError{message: "unexpected extra issuance call"}}
			}
		},
	}
	manager := mustNewManager(
		t,
		t.TempDir(),
		withNow(func() time.Time { return now }),
		withACMEIssuerFactory(func(request acmeIssueRequest) (acmeIssuer, error) {
			return issuer, nil
		}),
	)
	policy := model.ManagedCertificatePolicy{
		ID:              9204,
		Domain:          "renew-race.example.com",
		Enabled:         true,
		Scope:           "domain",
		IssuerMode:      "local_http01",
		CertificateType: "acme",
		Usage:           "https",
	}
	if err := manager.Apply(context.Background(), nil, []model.ManagedCertificatePolicy{policy}); err != nil {
		t.Fatalf("initial apply failed: %v", err)
	}

	renewalDone := make(chan error, 1)
	go func() {
		renewalDone <- manager.runRenewalLoopIteration(context.Background())
	}()

	<-renewalStarted

	applyDone := make(chan error, 1)
	go func() {
		applyDone <- manager.Apply(context.Background(), nil, []model.ManagedCertificatePolicy{policy})
	}()

	close(releaseRenewal)
	if err := <-renewalDone; err == nil {
		t.Fatal("expected renewal iteration to report error")
	}
	if err := <-applyDone; err != nil {
		t.Fatalf("concurrent apply failed: %v", err)
	}

	finalState, ok, err := manager.loadManagedCertificateState(9204)
	if err != nil {
		t.Fatalf("load final state failed: %v", err)
	}
	if !ok || finalState.ACME == nil {
		t.Fatal("expected final managed acme state")
	}
	if got := finalState.ACME.Renewal.LastAttemptStatus; got != "success" {
		t.Fatalf("expected concurrent apply success to remain authoritative, got %q", got)
	}
	if got := finalState.ACME.Renewal.LastAttemptError; got != "" {
		t.Fatalf("expected no lingering renewal error after apply success, got %q", got)
	}
}

func waitForRenewalRequests(t *testing.T, timeout time.Duration, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if done() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out waiting for renewal requests")
}

type threadSafeIssuer struct {
	mu       sync.Mutex
	requests []acmeIssueRequest
	results  []acmeIssueResult
}

func (i *threadSafeIssuer) Issue(_ context.Context, request acmeIssueRequest) (acmeIssueResult, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.requests = append(i.requests, request)
	if len(i.results) == 0 {
		return acmeIssueResult{}, assertUnreachableError{message: "unexpected acme issue call"}
	}
	result := i.results[0]
	i.results = i.results[1:]
	if result.Err != nil {
		return acmeIssueResult{}, result.Err
	}
	return result, nil
}

func (i *threadSafeIssuer) requestCount() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	return len(i.requests)
}

type blockingIssuer struct {
	started atomic.Int32
	result  acmeIssueResult
}

func (i *blockingIssuer) Issue(_ context.Context, _ acmeIssueRequest) (acmeIssueResult, error) {
	i.started.Add(1)
	time.Sleep(40 * time.Millisecond)
	if i.result.Err != nil {
		return acmeIssueResult{}, i.result.Err
	}
	return i.result, nil
}

func (i *blockingIssuer) callCount() int {
	return int(i.started.Load())
}

type sequencedIssuer struct {
	mu      sync.Mutex
	calls   int
	onIssue func(call int) acmeIssueResult
}

func (i *sequencedIssuer) Issue(_ context.Context, _ acmeIssueRequest) (acmeIssueResult, error) {
	i.mu.Lock()
	i.calls++
	call := i.calls
	handler := i.onIssue
	i.mu.Unlock()

	result := handler(call)
	if result.Err != nil {
		return acmeIssueResult{}, result.Err
	}
	return result, nil
}
