package service

import "sync"

// issuanceMu guards the issuanceByID map as well as each entry's waiter
// refcount. It is never held while an inner per-ID mutex is contended.
var issuanceMu sync.Mutex

// issuanceLockEntry is a per-certificate-ID lock carrying a refcount of the
// goroutines currently holding or waiting on it. An entry is removed from
// issuanceByID once its refcount drops to zero, so the map is bounded by the
// number of in-flight issuances instead of growing without bound as new
// certificate IDs are issued over the process lifetime.
type issuanceLockEntry struct {
	mu      sync.Mutex
	waiters int
}

// issuanceByID holds per-certificate-ID locks so that concurrent ACME
// issue/renew operations for the same certificate are serialized. Package-level
// because the auto-renewal loop and HTTP handlers use separate
// certificateService instances.
var issuanceByID = make(map[int]*issuanceLockEntry)

// issuanceLock returns an unlock function for the given certificate ID. The
// caller must invoke the returned unlock function exactly once when the
// issuance is done. Concurrent calls for the same ID serialize on that ID's
// mutex; once no goroutine holds or waits on an ID its entry is released.
func issuanceLock(id int) func() {
	issuanceMu.Lock()
	entry, ok := issuanceByID[id]
	if !ok {
		entry = &issuanceLockEntry{}
		issuanceByID[id] = entry
	}
	entry.waiters++
	issuanceMu.Unlock()

	entry.mu.Lock()

	return func() {
		entry.mu.Unlock()
		issuanceMu.Lock()
		entry.waiters--
		if entry.waiters == 0 {
			delete(issuanceByID, id)
		}
		issuanceMu.Unlock()
	}
}
