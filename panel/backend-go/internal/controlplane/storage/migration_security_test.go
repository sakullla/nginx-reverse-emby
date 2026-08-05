package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPKIMigrationRejectsSymlinkInTargetDirectoryChain(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	realTarget := filepath.Join(root, "outside")
	if err := os.Mkdir(realTarget, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "linked-target")
	if err := os.Symlink(realTarget, link); err != nil {
		t.Skipf("symbolic links are unavailable on this host: %v", err)
	}
	linkedPKI := filepath.Join(link, "pki")
	if err := os.Mkdir(linkedPKI, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := ensurePKIMigrationDirectory(linkedPKI); err == nil {
		t.Fatal("target directory chain containing a symbolic link was accepted")
	}
}
