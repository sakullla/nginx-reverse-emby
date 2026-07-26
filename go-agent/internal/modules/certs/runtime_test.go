package certs

import (
	"sync"
	"testing"
	"time"
)

// TestManagerIssuanceLockSerializesSameCertificateID asserts that concurrent
// issuances for one certificate ID run one at a time, and that the manager's
// issuanceByID map does not retain the entry after every holder/waiter has
// released it (R2: bounded refcount, no permanent retention of historical IDs).
func TestManagerIssuanceLockSerializesSameCertificateID(t *testing.T) {
	t.Parallel()
	manager := mustNewManager(t, t.TempDir())
	defer func() { _ = manager.Close() }()

	const id = 710001
	const contenders = 16
	unlockFirst := manager.issuanceLock(id)
	var wg sync.WaitGroup
	acquired := make(chan struct{}, contenders)
	for i := 0; i < contenders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock := manager.issuanceLock(id)
			acquired <- struct{}{}
			unlock()
		}()
	}

	waitForIssuanceWaiters(t, manager, id, contenders+1)
	select {
	case <-acquired:
		t.Fatal("same-ID contender acquired the issuance lock while the first holder was active")
	default:
	}
	unlockFirst()
	wg.Wait()

	if got := len(acquired); got != contenders {
		t.Fatalf("acquired contenders = %d, want %d", got, contenders)
	}

	manager.issuanceMu.Lock()
	_, leaked := manager.issuanceByID[id]
	manager.issuanceMu.Unlock()
	if leaked {
		t.Fatalf("expected issuanceByID[%d] removed once no goroutine holds or waits on it", id)
	}
}

func waitForIssuanceWaiters(t *testing.T, manager *Manager, id, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		manager.issuanceMu.Lock()
		got := 0
		if entry := manager.issuanceByID[id]; entry != nil {
			got = entry.waiters
		}
		manager.issuanceMu.Unlock()
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("issuance waiters for certificate %d = %d, want %d", id, got, want)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestManagerIssuanceLockRemovesEntryWhenLastHolderReleases verifies that an
// idle lock (no holder, no waiter) is deleted, which keeps the map bounded.
func TestManagerIssuanceLockRemovesEntryWhenLastHolderReleases(t *testing.T) {
	t.Parallel()
	manager := mustNewManager(t, t.TempDir())
	defer func() { _ = manager.Close() }()

	const id = 710002
	unlock := manager.issuanceLock(id)

	manager.issuanceMu.Lock()
	_, held := manager.issuanceByID[id]
	manager.issuanceMu.Unlock()
	if !held {
		t.Fatal("expected issuanceByID entry present while lock is held")
	}

	unlock()

	manager.issuanceMu.Lock()
	_, stillThere := manager.issuanceByID[id]
	manager.issuanceMu.Unlock()
	if stillThere {
		t.Fatal("expected issuanceByID entry removed after unlock with no remaining holders or waiters")
	}
}

// TestManagerIssuanceLockRetainsEntryUntilNoWaitersRemain verifies the entry
// survives while a waiter still holds the lock and is only reclaimed once the
// last waiter releases (refcount semantics, not "first unlock deletes").
func TestManagerIssuanceLockRetainsEntryUntilNoWaitersRemain(t *testing.T) {
	t.Parallel()
	manager := mustNewManager(t, t.TempDir())
	defer func() { _ = manager.Close() }()

	const id = 710003
	unlock1 := manager.issuanceLock(id)

	waiterAcquired := make(chan struct{})
	waiterRelease := make(chan struct{})
	waiterReleased := make(chan struct{})
	go func() {
		unlock2 := manager.issuanceLock(id)
		close(waiterAcquired)
		<-waiterRelease
		unlock2()
		close(waiterReleased)
	}()

	unlock1()
	<-waiterAcquired

	manager.issuanceMu.Lock()
	_, present := manager.issuanceByID[id]
	manager.issuanceMu.Unlock()
	if !present {
		t.Fatal("expected issuanceByID entry retained while a goroutine still holds the lock")
	}

	close(waiterRelease)
	<-waiterReleased

	manager.issuanceMu.Lock()
	_, presentAfter := manager.issuanceByID[id]
	manager.issuanceMu.Unlock()
	if presentAfter {
		t.Fatal("expected issuanceByID entry removed once the final waiter released")
	}
}
