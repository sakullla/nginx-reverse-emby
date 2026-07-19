package service

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestCertificateServiceRunRenewalPassSingleFlightsPerCertificateID(t *testing.T) {
	const certID = 60
	now := time.Date(2026, 4, 11, 1, 2, 3, 0, time.UTC)
	renewedMaterial := mustCreateSelfSignedCA(t, "SingleFlight Material")

	blocked := make(chan struct{})
	proceed := make(chan struct{})
	issuer := &blockingManagedCertificateRenewalIssuer{
		blocked: blocked,
		proceed: proceed,
		result: managedCertificateRenewalResult{
			Changed:      true,
			LastIssueAt:  "2026-04-11T01:02:03Z",
			MaterialHash: "single-flight-hash",
			ACMEInfo: ManagedCertificateACMEInfo{
				MainDomain: "single.example.com",
				CA:         "LetsEncrypt",
				Renew:      "2026-07-10T00:00:00Z",
			},
			Material: storage.ManagedCertificateBundle{
				CertPEM: strings.TrimSpace(renewedMaterial.CertPEM),
				KeyPEM:  strings.TrimSpace(renewedMaterial.KeyPEM),
			},
		},
	}

	store := &relayCertStore{
		managedCerts: []storage.ManagedCertificateRow{{
			ID:              certID,
			Domain:          "single.example.com",
			Enabled:         true,
			Scope:           "domain",
			IssuerMode:      "master_cf_dns",
			TargetAgentIDs:  `["local"]`,
			Status:          "active",
			ACMEInfo:        `{"Main_Domain":"single.example.com","Renew":"2026-04-10T00:00:00Z"}`,
			CertificateType: "acme",
			Usage:           "https",
			Revision:        1,
		}},
	}

	svc1 := newCertificateServiceWithRenewal(config.Config{LocalAgentID: "local"}, store, issuer)
	svc1.now = func() time.Time { return now }
	svc2 := newCertificateServiceWithRenewal(config.Config{LocalAgentID: "local"}, store, issuer)
	svc2.now = func() time.Time { return now }

	var err1, err2 error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		err1 = svc1.RunRenewalPass(context.Background())
	}()
	go func() {
		defer wg.Done()
		err2 = svc2.RunRenewalPass(context.Background())
	}()

	// Wait until the first issuer call is blocked.
	<-blocked

	// Let the first call proceed.
	close(proceed)

	wg.Wait()

	// At least one must succeed.
	if err1 != nil && err2 != nil {
		t.Fatalf("both calls failed: err1=%v, err2=%v", err1, err2)
	}

	// Exactly one issuance should have occurred.
	if issuer.callCount() != 1 {
		t.Fatalf("issuer callCount = %d, want 1", issuer.callCount())
	}
}

type blockingManagedCertificateRenewalIssuer struct {
	blocked chan struct{}
	proceed chan struct{}
	mu      sync.Mutex
	calls   []int
	result  managedCertificateRenewalResult
}

func (b *blockingManagedCertificateRenewalIssuer) Issue(_ context.Context, cert ManagedCertificate) (managedCertificateRenewalResult, error) {
	return b.issueOrRenew(cert)
}

func (b *blockingManagedCertificateRenewalIssuer) Renew(_ context.Context, cert ManagedCertificate) (managedCertificateRenewalResult, error) {
	return b.issueOrRenew(cert)
}

func (b *blockingManagedCertificateRenewalIssuer) issueOrRenew(cert ManagedCertificate) (managedCertificateRenewalResult, error) {
	b.mu.Lock()
	first := len(b.calls) == 0
	b.calls = append(b.calls, cert.ID)
	b.mu.Unlock()

	if first {
		// Signal that we are blocked and wait to proceed.
		b.blocked <- struct{}{}
		<-b.proceed
	}
	return b.result, nil
}

func (b *blockingManagedCertificateRenewalIssuer) callCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.calls)
}

// TestIssuanceLockSerializesSameCertificateID asserts that concurrent issuances
// for one certificate ID run one at a time, and that the package-level map does
// not retain the entry after every holder/waiter has released it (R2: bounded
// refcount, no permanent retention of historical IDs).
func TestIssuanceLockSerializesSameCertificateID(t *testing.T) {
	const id = 900001
	var current, maxConcurrent int32
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock := issuanceLock(id)
			n := atomic.AddInt32(&current, 1)
			for {
				m := atomic.LoadInt32(&maxConcurrent)
				if n <= m || atomic.CompareAndSwapInt32(&maxConcurrent, m, n) {
					break
				}
			}
			time.Sleep(time.Millisecond)
			atomic.AddInt32(&current, -1)
			unlock()
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt32(&maxConcurrent); got != 1 {
		t.Fatalf("max concurrent issuance holders = %d, want 1 (same ID must serialize)", got)
	}

	issuanceMu.Lock()
	_, leaked := issuanceByID[id]
	issuanceMu.Unlock()
	if leaked {
		t.Fatalf("expected issuanceByID[%d] removed once no goroutine holds or waits on it", id)
	}
}

// TestIssuanceLockRemovesEntryWhenLastHolderReleases verifies that an idle lock
// (no holder, no waiter) is deleted, which is what keeps the map bounded.
func TestIssuanceLockRemovesEntryWhenLastHolderReleases(t *testing.T) {
	id := 900002
	unlock := issuanceLock(id)

	issuanceMu.Lock()
	_, held := issuanceByID[id]
	issuanceMu.Unlock()
	if !held {
		t.Fatal("expected issuanceByID entry present while lock is held")
	}

	unlock()

	issuanceMu.Lock()
	_, stillThere := issuanceByID[id]
	issuanceMu.Unlock()
	if stillThere {
		t.Fatal("expected issuanceByID entry removed after unlock with no remaining holders or waiters")
	}
}

// TestIssuanceLockRetainsEntryUntilNoWaitersRemain verifies the entry survives
// while a waiter still holds the lock and is only reclaimed once the last
// waiter releases (refcount semantics, not "first unlock deletes").
func TestIssuanceLockRetainsEntryUntilNoWaitersRemain(t *testing.T) {
	id := 900003

	unlock1 := issuanceLock(id)

	waiterAcquired := make(chan struct{})
	waiterReleased := make(chan struct{})
	go func() {
		unlock2 := issuanceLock(id)
		close(waiterAcquired)
		time.Sleep(15 * time.Millisecond)
		unlock2()
		close(waiterReleased)
	}()

	unlock1()
	<-waiterAcquired

	issuanceMu.Lock()
	_, present := issuanceByID[id]
	issuanceMu.Unlock()
	if !present {
		t.Fatal("expected issuanceByID entry retained while a goroutine still holds the lock")
	}

	<-waiterReleased

	issuanceMu.Lock()
	_, presentAfter := issuanceByID[id]
	issuanceMu.Unlock()
	if presentAfter {
		t.Fatal("expected issuanceByID entry removed once the final waiter released")
	}
}
