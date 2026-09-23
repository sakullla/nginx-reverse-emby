package service

import (
	"context"
	"sync"
)

// issuanceMu guards the issuanceByID map as well as each entry's waiter
// refcount. It is never held while an inner per-ID token is contended.
var issuanceMu sync.Mutex

// issuanceLockEntry is a per-certificate-ID lock carrying a refcount of the
// goroutines currently holding or waiting on it. The token channel makes
// acquisition cancellable, so a renewal pass cannot wait forever behind a
// manual/background issuance. An entry is removed from issuanceByID once its
// refcount drops to zero, so the map is bounded by the number of in-flight
// issuances instead of growing without bound as new certificate IDs are issued
// over the process lifetime.
type issuanceLockEntry struct {
	token   chan struct{}
	waiters int
	mu      sync.Mutex
}

// issuanceByID holds per-certificate-ID locks so that concurrent ACME
// issue/renew operations for the same certificate are serialized. Package-level
// because the auto-renewal loop and HTTP handlers use separate
// certificateService instances.
var issuanceByID = make(map[int]*issuanceLockEntry)

// issuanceLock returns an unlock function for the given certificate ID. It is
// retained for callers that do not need cancellation; new scheduling paths
// should use issuanceLockContext.
func issuanceLock(id int) func() {
	unlock, _ := issuanceLockContext(context.Background(), id)
	return unlock
}

// issuanceLockContext acquires the per-certificate issuance slot while
// honoring ctx. The returned unlock function must be called exactly once after
// a successful acquisition.
func issuanceLockContext(ctx context.Context, id int) (func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}

	issuanceMu.Lock()
	entry, ok := issuanceByID[id]
	if !ok {
		entry = &issuanceLockEntry{token: make(chan struct{}, 1)}
		entry.token <- struct{}{}
		issuanceByID[id] = entry
	}
	entry.waiters++
	issuanceMu.Unlock()

	select {
	case <-ctx.Done():
		releaseIssuanceLockReference(id, entry)
		return nil, ctx.Err()
	case <-entry.token:
		// Prefer cancellation if it raced with acquisition. This prevents a
		// caller whose deadline has already elapsed from entering the ACME
		// operation and makes the lock contract deterministic for schedulers.
		if err := ctx.Err(); err != nil {
			entry.token <- struct{}{}
			releaseIssuanceLockReference(id, entry)
			return nil, err
		}
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			entry.token <- struct{}{}
			releaseIssuanceLockReference(id, entry)
		})
	}, nil
}

func releaseIssuanceLockReference(id int, entry *issuanceLockEntry) {
	issuanceMu.Lock()
	entry.waiters--
	if entry.waiters == 0 {
		delete(issuanceByID, id)
	}
	issuanceMu.Unlock()
}
