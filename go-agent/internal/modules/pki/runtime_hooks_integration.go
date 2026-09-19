//go:build integration

package pki

import (
	"os"
	"strings"
	"sync"
	"time"
)

const (
	integrationPKIClockFileEnv             = "NRE_INTEGRATION_PKI_CLOCK_FILE"
	integrationPKIPersistenceCrashPointEnv = "NRE_INTEGRATION_PKI_PERSISTENCE_CRASH_POINT"
	integrationPKIPersistenceCrashExitCode = 87
)

var agentIntegrationPKIClock struct {
	sync.Mutex
	last time.Time
}

// RuntimeClock reloads the external clock on each observation. Atomic file
// replacement by the E2E harness cannot expose a partial timestamp because a
// transient read/parse failure falls back to the last complete value.
func RuntimeClock() time.Time {
	path := strings.TrimSpace(os.Getenv(integrationPKIClockFileEnv))
	if path == "" {
		return time.Now()
	}
	raw, err := os.ReadFile(path)
	var parsed time.Time
	if err == nil {
		parsed, err = time.Parse(time.RFC3339Nano, strings.TrimSpace(string(raw)))
	}
	agentIntegrationPKIClock.Lock()
	defer agentIntegrationPKIClock.Unlock()
	if err == nil && !parsed.IsZero() {
		agentIntegrationPKIClock.last = parsed
	}
	if !agentIntegrationPKIClock.last.IsZero() {
		return agentIntegrationPKIClock.last
	}
	return time.Now()
}

func runtimePersistenceCheckpoint() func(string) error {
	point := strings.TrimSpace(os.Getenv(integrationPKIPersistenceCrashPointEnv))
	if point == "" {
		return nil
	}
	return func(actual string) error {
		if actual == point {
			os.Exit(integrationPKIPersistenceCrashExitCode)
		}
		return nil
	}
}
