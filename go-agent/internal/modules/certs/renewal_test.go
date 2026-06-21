package certs

import (
	"context"
	"errors"
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
	// The renewal failure recorded a failure-backoff window. The concurrent Apply's
	// re-issuance is therefore deferred (it must not burn LE quota by re-attempting
	// immediately), and Apply returns an error without overwriting the certificate's
	// last valid material or corrupting state. This is the intended R4 behavior; the
	// renewal loop / next heartbeat retries once the backoff window elapses.
	if err := <-applyDone; err == nil {
		t.Fatal("expected concurrent apply to be deferred by failure backoff")
	}

	finalState, ok, err := manager.loadManagedCertificateState(9204)
	if err != nil {
		t.Fatalf("load final state failed: %v", err)
	}
	if !ok || finalState.ACME == nil {
		t.Fatal("expected final managed acme state")
	}
	if got := finalState.ACME.Renewal.LastAttemptStatus; got != "error" {
		t.Fatalf("expected renewal failure to remain recorded during backoff, got %q", got)
	}
	if got := finalState.ACME.Renewal.BackoffClass; got == "" {
		t.Fatal("expected failure backoff class to be recorded after renewal failure")
	}
	if got := finalState.ACME.Renewal.BackoffRetryNext; got <= now.Unix() {
		t.Fatalf("expected backoff next-retry in the future, got %d (now=%d)", got, now.Unix())
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

// TestRenewalBackoffClassification mirrors the control-plane classification
// heuristic so both ACME paths share the same Retry-After / error-class curve (R4).
func TestRenewalBackoffClassification(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"nil is transient", nil, backoffClassTransient},
		{"rate limit", errors.New("rate limit exceeded registering account"), backoffClassRateLimited},
		{"too many", errors.New("too many requests"), backoffClassRateLimited},
		{"retry-after header", errors.New("server returned Retry-After: 3600"), backoffClassRateLimited},
		{"connection refused", errors.New("connection refused"), backoffClassTransient},
		{"i/o timeout", errors.New("read tcp: i/o timeout"), backoffClassTransient},
		{"no such host", errors.New("dial tcp: lookup acme.invalid: no such host"), backoffClassTransient},
		{"service unavailable", errors.New("503 service unavailable"), backoffClassTransient},
		{"durable auth failure", errors.New("invalid cloudflare api token"), backoffClassPersistent},
		{"unknown durable", errors.New("unexpected ACME response"), backoffClassPersistent},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := classifyRenewalError(tc.err); got != tc.want {
				t.Fatalf("classifyRenewalError(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}

func TestExtractRenewalRetryAfter(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want time.Duration
	}{
		{"nil", nil, 0},
		{"no marker", errors.New("connection refused"), 0},
		{"colon seconds", errors.New("Retry-After: 3600"), 3600 * time.Second},
		{"equals seconds", errors.New("retry-after=120"), 120 * time.Second},
		{"embedded in sentence", errors.New("rate limited; retry-after: 7200 seconds"), 7200 * time.Second},
		{"non-numeric", errors.New("retry-after: soon"), 0},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := extractRenewalRetryAfter(tc.err); got != tc.want {
				t.Fatalf("extractRenewalRetryAfter(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestRenewalBackoffDelay pins the exact retry curve. Values mirror the
// control-plane managedCertificateBackoffDelay tests so master and local share
// one curve per R4 (transient 5s->5m, persistent 1h->32h, rate-limited
// max(retryAfter,1h)->32h, deterministic quarter jitter, MaxShift=6).
func TestRenewalBackoffDelay(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		class      string
		retryAfter time.Duration
		retryCount int
		want       time.Duration
	}{
		{"transient r1", backoffClassTransient, 0, 1, 6*time.Second + 250*time.Millisecond},
		{"transient r2", backoffClassTransient, 0, 2, 15 * time.Second},
		{"transient r3", backoffClassTransient, 0, 3, 35 * time.Second},
		{"transient r7 capped+jitter", backoffClassTransient, 0, 7, 375 * time.Second},
		{"persistent r1", backoffClassPersistent, 0, 1, time.Hour + 15*time.Minute},
		{"persistent r6 capped", backoffClassPersistent, 0, 6, 40 * time.Hour},
		{"persistent r7 capped", backoffClassPersistent, 0, 7, 40 * time.Hour},
		{"rate_limited no retry-after", backoffClassRateLimited, 0, 1, time.Hour + 15*time.Minute},
		{"rate_limited retry-after 2h", backoffClassRateLimited, 2 * time.Hour, 1, 2*time.Hour + 30*time.Minute},
		{"rate_limited retry-after above cap", backoffClassRateLimited, 100 * time.Hour, 1, 40 * time.Hour},
		{"unknown class falls back to persistent", "bogus", 0, 1, time.Hour + 15*time.Minute},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := renewalBackoffDelay(tc.class, tc.retryAfter, tc.retryCount)
			if got != tc.want {
				t.Fatalf("renewalBackoffDelay(%q, %v, %d) = %v, want %v", tc.class, tc.retryAfter, tc.retryCount, got, tc.want)
			}
		})
	}
}

func TestRenewalBackoffJitterFraction(t *testing.T) {
	t.Parallel()
	cases := []struct {
		retryCount int
		want       int64
	}{
		{0, 0}, {1, 1}, {2, 2}, {3, 3}, {4, 0}, {5, 1}, {8, 0},
	}
	for _, tc := range cases {
		if got := renewalBackoffJitterFraction(tc.retryCount); got != tc.want {
			t.Fatalf("renewalBackoffJitterFraction(%d) = %d, want %d", tc.retryCount, got, tc.want)
		}
	}
}

// transientFailingIssuer always fails with a transient error and counts attempts.
type transientFailingIssuer struct {
	calls int
}

func (i *transientFailingIssuer) Issue(_ context.Context, _ acmeIssueRequest) (acmeIssueResult, error) {
	i.calls++
	return acmeIssueResult{}, errors.New("connection refused")
}

// TestRenewalBackoffDefersFirstIssuanceWithinWindow verifies the backoff guard
// in loadOrIssueACMEUnlocked gates the heartbeat-driven first-issuance path: a
// failed first issuance records backoff and is NOT re-attempted until the
// backoff window elapses, so LE quota is not burned (R5② / R4, requirement #4).
func TestRenewalBackoffDefersFirstIssuanceWithinWindow(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC)
	nowPtr := &now
	issuer := &transientFailingIssuer{}
	manager := mustNewManager(
		t,
		t.TempDir(),
		withNow(func() time.Time { return *nowPtr }),
		withACMEIssuerFactory(func(request acmeIssueRequest) (acmeIssuer, error) {
			return issuer, nil
		}),
	)
	policy := model.ManagedCertificatePolicy{
		ID:              9210,
		Domain:          "backoff.example.com",
		Enabled:         true,
		Scope:           "domain",
		IssuerMode:      "local_http01",
		CertificateType: "acme",
		Usage:           "https",
	}

	// First Apply attempts issuance and fails; backoff is recorded.
	if err := manager.Apply(context.Background(), nil, []model.ManagedCertificatePolicy{policy}); err == nil {
		t.Fatal("expected first apply to fail with a transient issuance error")
	}
	if issuer.calls != 1 {
		t.Fatalf("expected one issuance attempt on first apply, got %d", issuer.calls)
	}
	state, ok, err := manager.loadManagedCertificateState(9210)
	if err != nil || !ok || state.ACME == nil {
		t.Fatalf("expected persisted acme state after failure: ok=%v err=%v", ok, err)
	}
	if got := state.ACME.Renewal.BackoffClass; got != backoffClassTransient {
		t.Fatalf("expected transient backoff class, got %q", got)
	}
	nextRetry := state.ACME.Renewal.BackoffRetryNext
	if nextRetry <= now.Unix() {
		t.Fatalf("expected backoff next-retry in the future, got %d (now=%d)", nextRetry, now.Unix())
	}

	// Second Apply within the backoff window must NOT re-attempt issuance.
	if err := manager.Apply(context.Background(), nil, []model.ManagedCertificatePolicy{policy}); err == nil {
		t.Fatal("expected second apply to be deferred by failure backoff")
	}
	if issuer.calls != 1 {
		t.Fatalf("expected no new issuance attempt during backoff window, got %d", issuer.calls)
	}

	// Advance the clock past the backoff window; issuance resumes.
	*nowPtr = time.Unix(nextRetry, 0).Add(time.Second)
	_ = manager.Apply(context.Background(), nil, []model.ManagedCertificatePolicy{policy})
	if issuer.calls != 2 {
		t.Fatalf("expected issuance to resume after backoff window, got %d", issuer.calls)
	}
}
