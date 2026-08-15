//go:build linux

package rpc

import (
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
