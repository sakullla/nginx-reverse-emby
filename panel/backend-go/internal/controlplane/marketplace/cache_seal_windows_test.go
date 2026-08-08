//go:build windows

package marketplace

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsCacheSealACLRejectsModifyAndPEExecution(t *testing.T) {
	root := t.TempDir()
	sourcePath, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	targetPath := filepath.Join(root, "cached-plugin.exe")
	source, err := os.Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	target, err := os.OpenFile(targetPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		_ = source.Close()
		t.Fatal(err)
	}
	_, copyErr := io.Copy(target, source)
	closeErr := errors.Join(source.Close(), target.Close())
	if copyErr != nil || closeErr != nil {
		t.Fatal(errors.Join(copyErr, closeErr))
	}
	t.Cleanup(func() { _ = unsealCacheTree(root) })
	if err := sealCacheTree(root); err != nil {
		t.Fatal(err)
	}
	if err := sealCacheTree(root); err != nil {
		t.Fatalf("reseal existing cache tree: %v", err)
	}
	if err := verifyCachePathSealed(targetPath, false); err != nil {
		t.Fatal(err)
	}
	if file, err := os.OpenFile(targetPath, os.O_WRONLY, 0); err == nil {
		_ = file.Close()
		t.Fatal("sealed cache PE remained writable")
	} else if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		t.Fatalf("write failed for an unexpected reason: %v", err)
	}
	if err := exec.Command(targetPath, "-test.run=^$").Run(); err == nil {
		t.Fatal("sealed cache PE executed despite deny-execute ACL")
	} else if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		t.Fatalf("execution failed for an unexpected reason: %v", err)
	}
	if err := unsealCacheTree(root); err != nil {
		t.Fatalf("unseal cache tree: %v", err)
	}
	if err := os.Remove(targetPath); err != nil {
		t.Fatalf("remove unsealed cache PE: %v", err)
	}
}
