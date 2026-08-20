//go:build !windows

package marketplace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPublishSealedCacheTreeWithinTargetContainer(t *testing.T) {
	container := t.TempDir()
	staging, err := os.MkdirTemp(container, ".package-")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "artifact.bin"), []byte("verified"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unsealCacheTree(container) })
	if err := sealCacheTree(staging); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(container, "digest")
	if err := publishSealedCacheTree(staging, target); err != nil {
		t.Fatalf("publishSealedCacheTree() error = %v", err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o222 != 0 {
		t.Fatalf("published cache root retained write permission: %v", info.Mode())
	}
}

func TestPublishSealedCacheTreeRejectsCrossContainerRename(t *testing.T) {
	root := t.TempDir()
	staging := filepath.Join(root, "staging", ".package")
	target := filepath.Join(root, "target", "digest")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unsealCacheTree(root) })
	if err := sealCacheTree(staging); err != nil {
		t.Fatal(err)
	}

	if err := publishSealedCacheTree(staging, target); err == nil {
		t.Fatal("publishSealedCacheTree() accepted a cross-container rename")
	}
}
