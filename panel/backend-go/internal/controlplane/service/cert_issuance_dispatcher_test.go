package service

import (
	"context"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestManagedCertificateDispatcherDedupsInFlight(t *testing.T) {
	d := newManagedCertificateDispatcher()

	release := make(chan struct{})
	started := make(chan struct{}, 1)
	d.SetSignFunc(func(ctx context.Context, certID int) error {
		started <- struct{}{}
		<-release
		return nil
	})

	if !d.Submit(7) {
		t.Fatal("first Submit should dispatch a new goroutine")
	}
	<-started
	if !d.InFlight(7) {
		t.Fatal("expected certificate 7 to be in flight while sign is blocked")
	}
	if d.Submit(7) {
		t.Fatal("second Submit for an in-flight certificate must be de-duplicated")
	}

	close(release)
	d.Wait()
	if d.InFlight(7) {
		t.Fatal("expected in-flight slot to be released after sign completes")
	}
}

func TestManagedCertificateDispatcherNilSignFuncIsNoOp(t *testing.T) {
	d := newManagedCertificateDispatcher()

	if !d.Submit(11) {
		t.Fatal("Submit should dispatch even before a sign function is wired")
	}
	d.Wait()
	if d.InFlight(11) {
		t.Fatal("expected in-flight slot to be released after the nil-sign no-op")
	}
}

func TestManagedCertificateDispatcherSignFuncReceivesBaseContext(t *testing.T) {
	d := newManagedCertificateDispatcher()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.SetBaseContext(ctx)

	got := make(chan context.Context, 1)
	d.SetSignFunc(func(c context.Context, certID int) error {
		got <- c
		return nil
	})

	if !d.Submit(21) {
		t.Fatal("Submit should dispatch a new goroutine")
	}
	d.Wait()

	select {
	case received := <-got:
		if received != ctx {
			t.Fatal("sign function must receive the dispatcher base context")
		}
	default:
		t.Fatal("sign function was not invoked")
	}
}

func TestManagedCertificateDispatcherRecoverDispatchesOnlyIssuing(t *testing.T) {
	d := newManagedCertificateDispatcher()

	var mu sync.Mutex
	var seen []int
	release := make(chan struct{})
	d.SetSignFunc(func(ctx context.Context, certID int) error {
		mu.Lock()
		seen = append(seen, certID)
		mu.Unlock()
		<-release
		return nil
	})

	store := &managedCertificateDispatcherListStoreStub{
		rows: []storage.ManagedCertificateRow{
			{ID: 1, Domain: "a.example", Status: "issuing"},
			{ID: 2, Domain: "b.example", Status: "active"},
			{ID: 3, Domain: "c.example", Status: "issuing"},
			{ID: 4, Domain: "d.example", Status: "error"},
		},
	}

	dispatched, err := d.Recover(context.Background(), store)
	if err != nil {
		t.Fatalf("Recover returned error: %v", err)
	}
	if dispatched != 2 {
		t.Fatalf("expected 2 certificates dispatched, got %d", dispatched)
	}

	close(release)
	d.Wait()

	mu.Lock()
	sort.Ints(seen)
	got := append([]int(nil), seen...)
	mu.Unlock()
	if !reflect.DeepEqual(got, []int{1, 3}) {
		t.Fatalf("expected sign invoked for [1 3], got %v", got)
	}
}

// TestManagedCertificateDispatcherSerializesWithIssuanceLock exercises the R6
// "no duplicate ACME order" contract through the dispatcher path: the injected sign
// function holds issuanceLock(certID) (mirroring renewSingleCertificate), so a
// dispatcher goroutine and a concurrent renewal-loop acquire of the same per-certificate
// lock must serialize rather than overlap. The cert async-issuance review recorded this
// concurrency invariant as a deferred integration test (T2a F2).
func TestManagedCertificateDispatcherSerializesWithIssuanceLock(t *testing.T) {
	d := newManagedCertificateDispatcher()

	const certID = 903
	entered := make(chan struct{})
	release := make(chan struct{})
	d.SetSignFunc(func(ctx context.Context, id int) error {
		unlock := issuanceLock(id)
		// Signal that we hold the per-certificate lock, then block until released.
		close(entered)
		<-release
		unlock()
		return nil
	})

	if !d.Submit(certID) {
		t.Fatal("Submit should dispatch a new goroutine")
	}

	// Wait for the dispatcher goroutine to be inside its locked section.
	<-entered

	// A concurrent acquirer of the same per-certificate lock (the renewal loop uses the
	// same issuanceLock) must block while the dispatcher holds it. Assert the lock is NOT
	// acquirable within a short window, proving serialization instead of overlap.
	acquired := make(chan struct{})
	go func() {
		unlock := issuanceLock(certID)
		close(acquired)
		unlock()
	}()
	select {
	case <-acquired:
		t.Fatal("concurrent issuanceLock acquired while dispatcher sign function held it; expected serialization")
	case <-time.After(50 * time.Millisecond):
		// Expected: still blocked because the dispatcher goroutine holds the lock.
	}

	// Releasing the dispatcher goroutine must unblock the concurrent acquirer, proving
	// both sides contend on the same per-certificate lock.
	close(release)
	select {
	case <-acquired:
		// Expected: concurrent acquirer got the lock once the dispatcher released it.
	case <-time.After(time.Second):
		t.Fatal("concurrent issuanceLock did not acquire after the dispatcher released it")
	}

	d.Wait()
}

type managedCertificateDispatcherListStoreStub struct {
	rows []storage.ManagedCertificateRow
}

func (s *managedCertificateDispatcherListStoreStub) ListManagedCertificates(context.Context) ([]storage.ManagedCertificateRow, error) {
	return s.rows, nil
}
