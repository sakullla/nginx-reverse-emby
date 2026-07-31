package storage

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
)

func TestPKIInstanceLeaseRepositoryFencesTermsAndSerializesContenders(t *testing.T) {
	if testing.Short() {
		t.Skip("real SQLite PKI lease contention runs in the full test tier")
	}
	root := t.TempDir()
	dsn := filepath.Join(root, "panel.db")
	storeA, err := NewStore(StoreConfig{Driver: "sqlite", DSN: dsn, DataRoot: root, LocalAgentID: "local"})
	if err != nil {
		t.Fatalf("NewStore(A) error = %v", err)
	}
	t.Cleanup(func() { _ = storeA.Close() })
	storeB, err := NewStore(StoreConfig{
		Driver: "sqlite", DSN: dsn, DataRoot: root, LocalAgentID: "local", SkipBootstrapSchema: true,
	})
	if err != nil {
		t.Fatalf("NewStore(B) error = %v", err)
	}
	t.Cleanup(func() { _ = storeB.Close() })

	now := time.Date(2026, 8, 1, 4, 0, 0, 0, time.UTC)
	if err := storeA.WithPKITransaction(t.Context(), func(tx *PKITransaction) error {
		return tx.CreatePKISettings(t.Context(), PKISettingsRow{
			PKIDomainID: "domain-1", CALifetimeSeconds: int64(365 * 24 * time.Hour / time.Second),
			EndpointLifetimeSeconds: int64(90 * 24 * time.Hour / time.Second), AuditRetentionDays: 365,
			PKIEpoch: 4, SecurityRevision: 9, CreatedAt: now, UpdatedAt: now,
		})
	}); err != nil {
		t.Fatalf("seed PKI settings: %v", err)
	}

	first, acquired, err := storeA.TryAcquirePKIInstanceLease(t.Context(), "instance-a", "term-a1", now, now.Add(30*time.Second))
	if err != nil || !acquired || first.LeaseTerm != "term-a1" || first.PKIEpoch != 4 {
		t.Fatalf("first acquisition = (%+v, %v, %v)", first, acquired, err)
	}
	if _, acquired, err := storeB.TryAcquirePKIInstanceLease(t.Context(), "instance-b", "term-b1", now.Add(time.Second), now.Add(31*time.Second)); err != nil || acquired {
		t.Fatalf("live competing acquisition = (%v, %v), want denied", acquired, err)
	}

	reacquired, acquired, err := storeA.TryAcquirePKIInstanceLease(t.Context(), "instance-a", "term-a2", now.Add(2*time.Second), now.Add(32*time.Second))
	if err != nil || !acquired || reacquired.LeaseTerm != "term-a2" {
		t.Fatalf("same-instance reacquisition = (%+v, %v, %v)", reacquired, acquired, err)
	}
	if _, renewed, err := storeA.RenewPKIInstanceLease(t.Context(), "instance-a", "term-a1", 4, now.Add(3*time.Second), now.Add(33*time.Second)); err != nil || renewed {
		t.Fatalf("old-term renewal = (%v, %v), want fenced", renewed, err)
	}
	if relinquished, err := storeA.RelinquishPKIInstanceLease(t.Context(), "instance-a", "term-a1", 4, now.Add(3*time.Second)); err != nil || relinquished {
		t.Fatalf("old-term relinquish = (%v, %v), want fenced", relinquished, err)
	}
	if _, renewed, err := storeA.RenewPKIInstanceLease(t.Context(), "instance-a", "term-a2", 4, now.Add(4*time.Second), now.Add(34*time.Second)); err != nil || !renewed {
		t.Fatalf("current-term renewal = (%v, %v), want success", renewed, err)
	}
	if _, acquired, err := storeB.TryAcquirePKIInstanceLease(t.Context(), "instance-b", "term-b2", now.Add(35*time.Second), now.Add(65*time.Second)); err != nil || !acquired {
		t.Fatalf("post-expiry acquisition = (%v, %v), want success", acquired, err)
	}
	if _, renewed, err := storeA.RenewPKIInstanceLease(t.Context(), "instance-a", "term-a2", 4, now.Add(36*time.Second), now.Add(66*time.Second)); err != nil || renewed {
		t.Fatalf("superseded-holder renewal = (%v, %v), want fenced", renewed, err)
	}

	if relinquished, err := storeB.RelinquishPKIInstanceLease(t.Context(), "instance-b", "term-b2", 4, now.Add(36*time.Second)); err != nil || !relinquished {
		t.Fatalf("relinquish current holder = (%v, %v)", relinquished, err)
	}

	type contenderResult struct {
		instance string
		acquired bool
		err      error
	}
	start := make(chan struct{})
	results := make(chan contenderResult, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	contend := func(store *GormStore, instance, term string) {
		ready.Done()
		<-start
		_, won, acquireErr := store.TryAcquirePKIInstanceLease(
			t.Context(), instance, term, now.Add(37*time.Second), now.Add(67*time.Second),
		)
		results <- contenderResult{instance: instance, acquired: won, err: acquireErr}
	}
	go contend(storeA, "instance-c", "term-c1")
	go contend(storeB, "instance-d", "term-d1")
	ready.Wait()
	close(start)
	winners := 0
	for range 2 {
		result := <-results
		if result.err != nil {
			t.Fatalf("contender %s error = %v", result.instance, result.err)
		}
		if result.acquired {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("concurrent winners = %d, want exactly one", winners)
	}
	snapshot, err := storeA.ReadPKIInstanceLease(t.Context())
	if err != nil || !snapshot.Exists || snapshot.LeaseTerm == "" || snapshot.State != PKIInstanceLeaseStateHeld {
		t.Fatalf("final snapshot = %+v, error = %v", snapshot, err)
	}
	if err := storeA.writeTransaction(t.Context(), func(tx *gorm.DB) error {
		return tx.Model(&PKISettingsRow{}).
			Where("id = ?", PKISettingsSingletonID).
			Updates(map[string]any{"pki_epoch": 5, "security_revision": 0, "updated_at": now.Add(38 * time.Second)}).Error
	}); err != nil {
		t.Fatalf("advance PKI epoch: %v", err)
	}
	epochGrant, acquired, err := storeB.TryAcquirePKIInstanceLease(
		t.Context(), "instance-e", "term-e1", now.Add(39*time.Second), now.Add(69*time.Second),
	)
	if err != nil || !acquired || epochGrant.PKIEpoch != 5 || epochGrant.InstanceID != "instance-e" {
		t.Fatalf("higher-epoch acquisition = (%+v, %v, %v)", epochGrant, acquired, err)
	}
}
