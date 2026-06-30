package certs

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestManagerIssuanceLockSerializesSameCertificateID asserts that concurrent
// issuances for one certificate ID run one at a time, and that the manager's
// issuanceByID map does not retain the entry after every holder/waiter has
// released it (R2: bounded refcount, no permanent retention of historical IDs).
func TestManagerIssuanceLockSerializesSameCertificateID(t *testing.T) {
	manager := mustNewManager(t, t.TempDir())
	defer func() { _ = manager.Close() }()

	const id = 710001
	var current, maxConcurrent int32
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock := manager.issuanceLock(id)
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

	manager.issuanceMu.Lock()
	_, leaked := manager.issuanceByID[id]
	manager.issuanceMu.Unlock()
	if leaked {
		t.Fatalf("expected issuanceByID[%d] removed once no goroutine holds or waits on it", id)
	}
}

// TestManagerIssuanceLockRemovesEntryWhenLastHolderReleases verifies that an
// idle lock (no holder, no waiter) is deleted, which keeps the map bounded.
func TestManagerIssuanceLockRemovesEntryWhenLastHolderReleases(t *testing.T) {
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
	manager := mustNewManager(t, t.TempDir())
	defer func() { _ = manager.Close() }()

	const id = 710003
	unlock1 := manager.issuanceLock(id)

	waiterAcquired := make(chan struct{})
	waiterReleased := make(chan struct{})
	go func() {
		unlock2 := manager.issuanceLock(id)
		close(waiterAcquired)
		time.Sleep(15 * time.Millisecond)
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

	<-waiterReleased

	manager.issuanceMu.Lock()
	_, presentAfter := manager.issuanceByID[id]
	manager.issuanceMu.Unlock()
	if presentAfter {
		t.Fatal("expected issuanceByID entry removed once the final waiter released")
	}
}
