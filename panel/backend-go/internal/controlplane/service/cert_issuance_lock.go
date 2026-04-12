package service

import "sync"

// issuanceMu protects the issuanceByID map itself.
var issuanceMu sync.Mutex

// issuanceByID holds per-certificate-ID mutexes so that concurrent
// ACME issue/renew operations for the same certificate are serialized.
// Package-level because the auto-renewal loop and HTTP handlers use
// separate certificateService instances.
// Entries are cleaned up when no goroutine holds the lock.
var issuanceByID = make(map[int]*sync.Mutex)

// issuanceLock returns a lock and unlock function for the given certificate ID.
// The caller must invoke the returned unlock function when the issuance is done.
// The unlock function releases the per-certificate lock and, if no other
// goroutine is waiting, removes the entry from the map to prevent unbounded growth.
func issuanceLock(id int) func() {
	issuanceMu.Lock()
	mu, ok := issuanceByID[id]
	if !ok {
		mu = &sync.Mutex{}
		issuanceByID[id] = mu
	}
	issuanceMu.Unlock()

	mu.Lock()
	return func() {
		mu.Unlock()
		issuanceMu.Lock()
		defer issuanceMu.Unlock()
		if mu.TryLock() {
			mu.Unlock()
			delete(issuanceByID, id)
		}
	}
}
