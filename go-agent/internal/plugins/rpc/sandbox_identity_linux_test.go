//go:build linux

package rpc

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
)

func isolateSandboxUIDTestState(t *testing.T) {
	t.Helper()
	oldRoot := attemptSandboxUIDLeaseRoot
	oldUsage := linuxUIDUsageForSandbox
	attemptSandboxUIDLeaseRoot = t.TempDir()
	if err := os.Chmod(attemptSandboxUIDLeaseRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	linuxUIDUsageForSandbox = func(int) (bool, bool) { return false, false }
	attemptSandboxUIDs.Lock()
	oldActive := attemptSandboxUIDs.active
	attemptSandboxUIDs.active = make(map[int]sandboxUIDLease)
	attemptSandboxUIDs.Unlock()
	t.Cleanup(func() {
		attemptSandboxUIDLeaseRoot = oldRoot
		linuxUIDUsageForSandbox = oldUsage
		attemptSandboxUIDs.Lock()
		attemptSandboxUIDs.active = oldActive
		attemptSandboxUIDs.Unlock()
	})
}

func TestStorageAttemptIdentityUsesStableUID(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("stable UID allocation requires root")
	}
	isolateSandboxUIDTestState(t)
	first, releaseFirst, err := allocateAttemptSandboxUID("instance-stable-storage")
	if err != nil {
		t.Fatal(err)
	}
	defer releaseFirst()
	second, releaseSecond, err := allocateAttemptSandboxUID("instance-stable-storage")
	if err != nil {
		t.Fatal(err)
	}
	defer releaseSecond()
	if first != second {
		t.Fatalf("stable identity UIDs = %d and %d", first, second)
	}
}

func TestStorageAttemptIdentitySurvivesHotRestartProcessBoundary(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("stable UID allocation requires root")
	}
	isolateSandboxUIDTestState(t)
	first, releaseFirst, err := allocateAttemptSandboxUID("instance-stable-storage")
	if err != nil {
		t.Fatal(err)
	}
	releaseFirst()
	if err := os.Remove(filepath.Join(attemptSandboxUIDLeaseRoot, strconv.Itoa(first))); err != nil {
		t.Fatal(err)
	}
	attemptSandboxUIDs.Lock()
	attemptSandboxUIDs.active = make(map[int]sandboxUIDLease)
	attemptSandboxUIDs.Unlock()
	linuxUIDUsageForSandbox = func(uid int) (bool, bool) { return uid == first, uid == first }
	second, releaseSecond, err := allocateAttemptSandboxUID("instance-stable-storage")
	if err != nil {
		t.Fatal(err)
	}
	releaseSecond()
	if first != second {
		t.Fatalf("hot restart stable identity UIDs = %d and %d", first, second)
	}
	attemptSandboxUIDs.Lock()
	attemptSandboxUIDs.active = make(map[int]sandboxUIDLease)
	attemptSandboxUIDs.Unlock()
	third, releaseThird, err := allocateAttemptSandboxUID("instance-stable-storage")
	if err != nil {
		t.Fatal(err)
	}
	defer releaseThird()
	if first != third {
		t.Fatalf("persisted hot restart identity UIDs = %d and %d", first, third)
	}
	claimed, _, _, err := claimPersistentSandboxUID(first, "different-instance")
	if err != nil {
		t.Fatal(err)
	}
	if claimed {
		t.Fatal("persistent uid lease accepted a different plugin identity")
	}
	if !reflect.DeepEqual(attemptSandboxUIDs.active[first], sandboxUIDLease{identity: "instance-stable-storage", refs: 1}) {
		t.Fatalf("active uid lease = %+v", attemptSandboxUIDs.active[first])
	}
}
