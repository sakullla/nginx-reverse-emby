package service

import (
	"context"
	"reflect"
	"sort"
	"sync"
	"testing"

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

type managedCertificateDispatcherListStoreStub struct {
	rows []storage.ManagedCertificateRow
}

func (s *managedCertificateDispatcherListStoreStub) ListManagedCertificates(context.Context) ([]storage.ManagedCertificateRow, error) {
	return s.rows, nil
}
