package service

import "sync"

// issuanceMu protects the issuanceByID map itself.
var issuanceMu sync.Mutex

// issuanceByID holds per-certificate-ID mutexes so that concurrent
// ACME issue/renew operations for the same certificate are serialized.
// Package-level because the auto-renewal loop and HTTP handlers use
// separate certificateService instances.
var issuanceByID = make(map[int]*sync.Mutex)

// issuanceLock returns an unlock function for the given certificate ID.
// The caller must invoke the returned unlock function when the issuance is done.
func issuanceLock(id int) func() {
	issuanceMu.Lock()
	mu, ok := issuanceByID[id]
	if !ok {
		mu = &sync.Mutex{}
		issuanceByID[id] = mu
	}
	issuanceMu.Unlock()

	mu.Lock()
	return func() { mu.Unlock() }
}
