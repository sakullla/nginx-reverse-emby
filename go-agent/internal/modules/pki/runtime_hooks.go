//go:build !integration

package pki

import "time"

// RuntimeClock is the PKI wall clock used by production agent wiring.
func RuntimeClock() time.Time {
	return time.Now()
}

func runtimePersistenceCheckpoint() func(string) error {
	return nil
}
