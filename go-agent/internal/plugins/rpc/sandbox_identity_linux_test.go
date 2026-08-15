//go:build linux

package rpc

import (
	"errors"
	"os"
	"testing"
)

func TestAttemptSandboxUIDLeaseIsCollisionFreeAndReleased(t *testing.T) {
	if !linuxUIDInUse(os.Geteuid()) {
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
	attemptSandboxUIDs.Lock()
	_, firstActive := attemptSandboxUIDs.active[first]
	_, secondActive := attemptSandboxUIDs.active[second]
	attemptSandboxUIDs.Unlock()
	if firstActive || secondActive {
		t.Fatal("released sandbox uid remained registered")
	}
}

func TestAttemptSandboxUIDLeaseSurvivesFailedCleanupRetry(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("sandbox UID leases are allocated by a root host")
	}
	runtimeDirectory := t.TempDir()
	cleanupErr := errors.New("injected attempt cleanup failure")
	cleanupCalls := 0
	attempt, err := provisionAttemptSecurityWithOps(runtimeDirectory, DialConfig{Network: "unix"}, attemptSecurityOps{
		cleanup: func(runtimeRoot, attemptRoot string) error {
			cleanupCalls++
			if cleanupCalls == 1 {
				return cleanupErr
			}
			return cleanupAttemptDirectory(runtimeRoot, attemptRoot)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := attempt.cleanup(); !errors.Is(err, cleanupErr) {
		t.Fatalf("first cleanup error = %v", err)
	}
	attemptSandboxUIDs.Lock()
	_, activeAfterFailure := attemptSandboxUIDs.active[attempt.sandboxUID]
	attemptSandboxUIDs.Unlock()
	if !activeAfterFailure {
		t.Fatal("failed cleanup released the sandbox UID lease")
	}
	if err := attempt.cleanup(); err != nil {
		t.Fatalf("cleanup retry: %v", err)
	}
	if err := attempt.cleanup(); err != nil {
		t.Fatalf("repeated successful cleanup: %v", err)
	}
	attemptSandboxUIDs.Lock()
	_, activeAfterSuccess := attemptSandboxUIDs.active[attempt.sandboxUID]
	attemptSandboxUIDs.Unlock()
	if activeAfterSuccess {
		t.Fatal("successful cleanup did not release the sandbox UID lease")
	}
}
