//go:build linux

package pluginhost

import (
	"errors"
	"os"
	"testing"
)

func TestControlAttemptSandboxUIDLeaseIsCollisionFreeAndReleased(t *testing.T) {
	if !controlLinuxUIDInUse(os.Geteuid()) {
		t.Fatal("current process uid was not detected as in use")
	}
	if os.Geteuid() != 0 {
		uid, release, err := allocateAttemptSandboxUID()
		if err != nil || uid != 0 {
			t.Fatalf("non-root sandbox uid lease = %d, %v", uid, err)
		}
		release()
		return
	}
	first, releaseFirst, err := allocateAttemptSandboxUID()
	if err != nil {
		t.Fatal(err)
	}
	second, releaseSecond, err := allocateAttemptSandboxUID()
	if err != nil {
		releaseFirst()
		t.Fatal(err)
	}
	if first == 0 || second == 0 || first == second {
		t.Fatalf("sandbox uid leases = %d, %d", first, second)
	}
	releaseFirst()
	releaseFirst()
	releaseSecond()
	controlSandboxUIDs.Lock()
	_, firstActive := controlSandboxUIDs.active[first]
	_, secondActive := controlSandboxUIDs.active[second]
	controlSandboxUIDs.Unlock()
	if firstActive || secondActive {
		t.Fatal("released sandbox uid remained registered")
	}
}

func TestControlAttemptSandboxUIDLeaseSurvivesFailedCleanupRetry(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("control sandbox UID leases are allocated by a root host")
	}
	runtimeDirectory := t.TempDir()
	cleanupErr := errors.New("injected control attempt cleanup failure")
	cleanupCalls := 0
	attempt, err := provisionControlAttemptSecurityWithOps(runtimeDirectory, Endpoint{Network: "unix"}, controlAttemptSecurityOps{
		cleanup: func(runtimeRoot, attemptRoot string) error {
			cleanupCalls++
			if cleanupCalls == 1 {
				return cleanupErr
			}
			return cleanupControlAttemptDirectory(runtimeRoot, attemptRoot)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := attempt.cleanup(); !errors.Is(err, cleanupErr) {
		t.Fatalf("first cleanup error = %v", err)
	}
	controlSandboxUIDs.Lock()
	_, activeAfterFailure := controlSandboxUIDs.active[attempt.sandboxUID]
	controlSandboxUIDs.Unlock()
	if !activeAfterFailure {
		t.Fatal("failed cleanup released the control sandbox UID lease")
	}
	if err := attempt.cleanup(); err != nil {
		t.Fatalf("control cleanup retry: %v", err)
	}
	if err := attempt.cleanup(); err != nil {
		t.Fatalf("repeated successful control cleanup: %v", err)
	}
	controlSandboxUIDs.Lock()
	_, activeAfterSuccess := controlSandboxUIDs.active[attempt.sandboxUID]
	controlSandboxUIDs.Unlock()
	if activeAfterSuccess {
		t.Fatal("successful cleanup did not release the control sandbox UID lease")
	}
}
