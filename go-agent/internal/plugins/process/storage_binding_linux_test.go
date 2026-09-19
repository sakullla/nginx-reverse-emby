//go:build linux

package process

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestOpenLinuxDirectoryBindingCreatesStableOwnedDirectory(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("ownership verification requires root")
	}
	root := filepath.Join(t.TempDir(), "share")
	binding := DirectoryBinding{HostPath: root, GuestPath: root}
	file, identity, err := openLinuxDirectoryBinding(binding, 1601234567)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if identity.Mode&syscall.S_IFMT != syscall.S_IFDIR {
		t.Fatalf("identity = %#v", identity)
	}
	info, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	stat := info.Sys().(*syscall.Stat_t)
	if stat.Uid != 1601234567 || info.Mode().Perm() != 0o700 {
		t.Fatalf("directory owner/mode = %d/%o", stat.Uid, info.Mode().Perm())
	}
}

func TestLinuxDirectoryBindingRejectsProtectedGuestPath(t *testing.T) {
	if err := validateDirectoryBinding(DirectoryBinding{HostPath: "/proc/data", GuestPath: "/proc/data"}); err == nil {
		t.Fatal("protected storage path was accepted")
	}
}
