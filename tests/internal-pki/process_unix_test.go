//go:build integration && !windows

package internalpki

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

func configureProcessTree(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func cleanupProcessTreeFiles(path string) error {
	permissionErr := filepath.WalkDir(path, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		return os.Chmod(name, info.Mode().Perm()|0o700)
	})
	if errors.Is(permissionErr, os.ErrNotExist) {
		permissionErr = nil
	}
	return errors.Join(permissionErr, os.RemoveAll(path))
}

func TestCleanupProcessTreeFilesRestoresDirectoryWritePermission(t *testing.T) {
	root := t.TempDir()
	sealedParent := filepath.Join(root, "control", "plugins")
	if err := os.MkdirAll(filepath.Join(sealedParent, "packages"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sealedParent, 0o555); err != nil {
		t.Fatal(err)
	}

	if err := cleanupProcessTreeFiles(root); err != nil {
		t.Fatalf("cleanup read-only process tree: %v", err)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("process tree still exists after cleanup: %v", err)
	}
}

func terminateProcessTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
