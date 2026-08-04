//go:build integration

package main

import (
	"os"
	"strings"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

const (
	integrationPKIClockFileEnv          = "NRE_INTEGRATION_PKI_CLOCK_FILE"
	integrationPKIAuthorityHeartbeatEnv = "NRE_INTEGRATION_PKI_AUTHORITY_HEARTBEAT_INTERVAL"
	integrationPKIRestoreCrashPointEnv  = "NRE_INTEGRATION_PKI_RESTORE_CRASH_POINT"
	integrationPKIRestoreCrashExitCode  = 86
)

var controlPlaneIntegrationPKIClock struct {
	sync.Mutex
	last time.Time
}

func controlPlanePKIAuthorityHeartbeatInterval() time.Duration {
	value, err := time.ParseDuration(strings.TrimSpace(os.Getenv(integrationPKIAuthorityHeartbeatEnv)))
	if err != nil || value <= 0 {
		return 0
	}
	return value
}

// controlPlanePKIRuntimeClock reloads the file on every observation so an
// external multi-process test can advance PKI time without a test HTTP API.
// A transient replace/read failure retains the last complete timestamp.
func controlPlanePKIRuntimeClock() time.Time {
	path := strings.TrimSpace(os.Getenv(integrationPKIClockFileEnv))
	if path == "" {
		return time.Now()
	}
	raw, err := os.ReadFile(path)
	var parsed time.Time
	if err == nil {
		parsed, err = time.Parse(time.RFC3339Nano, strings.TrimSpace(string(raw)))
	}
	controlPlaneIntegrationPKIClock.Lock()
	defer controlPlaneIntegrationPKIClock.Unlock()
	if err == nil && !parsed.IsZero() {
		controlPlaneIntegrationPKIClock.last = parsed
	}
	if !controlPlaneIntegrationPKIClock.last.IsZero() {
		return controlPlaneIntegrationPKIClock.last
	}
	return time.Now()
}

// These hooks model an abrupt process loss at the two durable restore
// boundaries. after_swap has promoted all paths but has no commit marker, so
// startup recovery must select the old generation. after_commit has a durable
// commit marker, so startup recovery must select the complete new generation.
func controlPlanePKIRestoreHooks() storage.PKISQLiteRestoreHooks {
	crash := func(point string) func() error {
		return func() error {
			if strings.EqualFold(strings.TrimSpace(os.Getenv(integrationPKIRestoreCrashPointEnv)), point) {
				os.Exit(integrationPKIRestoreCrashExitCode)
			}
			return nil
		}
	}
	return storage.PKISQLiteRestoreHooks{
		AfterSwap:   crash("after_swap"),
		AfterCommit: crash("after_commit"),
	}
}
