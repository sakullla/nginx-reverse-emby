//go:build linux

package rpc

import (
	"os"
	"testing"
)

func TestStorageAttemptIdentityUsesStableUID(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("stable UID allocation requires root")
	}
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
