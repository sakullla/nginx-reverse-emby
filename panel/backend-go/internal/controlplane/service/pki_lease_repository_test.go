package service

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestPKILeaseGormRepositoryCoordinatesSharedSQLiteStores(t *testing.T) {
	if testing.Short() {
		t.Skip("shared SQLite PKI lease integration runs in the full test tier")
	}
	root := t.TempDir()
	dsn := filepath.Join(root, "panel.db")
	storeA, err := storage.NewStore(storage.StoreConfig{
		Driver: "sqlite", DSN: dsn, DataRoot: root, LocalAgentID: "local",
	})
	if err != nil {
		t.Fatalf("NewStore(A) error = %v", err)
	}
	t.Cleanup(func() { _ = storeA.Close() })
	storeB, err := storage.NewStore(storage.StoreConfig{
		Driver: "sqlite", DSN: dsn, DataRoot: root, LocalAgentID: "local", SkipBootstrapSchema: true,
	})
	if err != nil {
		t.Fatalf("NewStore(B) error = %v", err)
	}
	t.Cleanup(func() { _ = storeB.Close() })

	clock := &pkiLeaseTestClock{now: time.Date(2026, 8, 1, 6, 0, 0, 0, time.UTC)}
	if err := storeA.WithPKITransaction(t.Context(), func(tx *storage.PKITransaction) error {
		return tx.CreatePKISettings(t.Context(), storage.PKISettingsRow{
			PKIDomainID: "domain-shared", CALifetimeSeconds: int64(365 * 24 * time.Hour / time.Second),
			EndpointLifetimeSeconds: int64(90 * 24 * time.Hour / time.Second), AuditRetentionDays: 365,
			PKIEpoch: 2, SecurityRevision: 4, CreatedAt: clock.Now(), UpdatedAt: clock.Now(),
		})
	}); err != nil {
		t.Fatalf("seed PKI settings: %v", err)
	}
	repositoryA, err := NewGormPKILeaseRepository(storeA)
	if err != nil {
		t.Fatalf("NewGormPKILeaseRepository(A) error = %v", err)
	}
	repositoryB, err := NewGormPKILeaseRepository(storeB)
	if err != nil {
		t.Fatalf("NewGormPKILeaseRepository(B) error = %v", err)
	}
	serviceA := newPKILeaseTestService(t, repositoryA, clock, "instance-a")
	serviceB := newPKILeaseTestService(t, repositoryB, clock, "instance-b")
	type contender struct {
		id         string
		service    *PKILeaseService
		repository PKILeaseRepository
		grant      PKILeaseGrant
		err        error
	}
	start := make(chan struct{})
	results := make(chan contender, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for _, candidate := range []contender{
		{id: "instance-a", service: serviceA, repository: repositoryA},
		{id: "instance-b", service: serviceB, repository: repositoryB},
	} {
		candidate := candidate
		go func() {
			ready.Done()
			<-start
			candidate.grant, candidate.err = candidate.service.Acquire(t.Context())
			results <- candidate
		}()
	}
	ready.Wait()
	close(start)
	var winner, standby contender
	for range 2 {
		candidate := <-results
		if candidate.err == nil {
			if winner.service != nil {
				t.Fatalf("multiple initial lease winners: %q and %q", winner.id, candidate.id)
			}
			winner = candidate
			continue
		}
		if !errors.Is(candidate.err, ErrPKILeaseNotHeld) {
			t.Fatalf("Acquire(%s) error = %v", candidate.id, candidate.err)
		}
		standby = candidate
	}
	if winner.service == nil || standby.service == nil || winner.grant.LeaseTerm == "" {
		t.Fatalf("initial contenders = winner %+v, standby %+v", winner, standby)
	}
	clock.Advance(defaultPKILeaseTTL + time.Second)
	takeover, err := standby.service.Acquire(t.Context())
	if err != nil || takeover.LeaseTerm == "" || takeover.LeaseTerm == winner.grant.LeaseTerm {
		t.Fatalf("Acquire(%s after expiry) = (%+v, %v)", standby.id, takeover, err)
	}
	now := clock.Now()
	if _, renewed, err := winner.repository.RenewPKILease(
		t.Context(), winner.grant.InstanceID, winner.grant.LeaseTerm, winner.grant.PKIEpoch, now, now.Add(defaultPKILeaseTTL),
	); err != nil || renewed {
		t.Fatalf("RenewPKILease(old term after takeover) = (%v, %v), want fenced", renewed, err)
	}
	if relinquished, err := winner.repository.RelinquishPKILease(
		t.Context(), winner.grant.InstanceID, winner.grant.LeaseTerm, winner.grant.PKIEpoch, now,
	); err != nil || relinquished {
		t.Fatalf("RelinquishPKILease(old term after takeover) = (%v, %v), want fenced", relinquished, err)
	}
	canonical, err := standby.repository.ReadPKILease(t.Context())
	if err != nil || canonical.LeaseTerm != takeover.LeaseTerm || canonical.InstanceID != standby.id {
		t.Fatalf("canonical lease after stale mutations = (%+v, %v), want takeover", canonical, err)
	}
	if _, err := winner.service.RequirePKILease(t.Context()); !errors.Is(err, ErrPKILeaseNotHeld) {
		t.Fatalf("RequirePKILease(old winner after takeover) error = %v, want ErrPKILeaseNotHeld", err)
	}
	if _, err := standby.service.RequirePKILease(t.Context()); err != nil {
		t.Fatalf("RequirePKILease(new winner) error = %v", err)
	}
}
