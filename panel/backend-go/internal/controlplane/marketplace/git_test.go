package marketplace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFetchTreeBudgetAndGitMetadataCleanup(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git", "objects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "objects", "large"), make([]byte, 2048), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "market.yaml"), []byte("schema_version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := enforceFetchTreeBudget(root, 10, 1024); err == nil {
		t.Fatal("fetch budget ignored Git object bytes")
	}
	if err := removeGitMetadata(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".git")); !os.IsNotExist(err) {
		t.Fatalf("Git metadata remains: %v", err)
	}
	if err := enforceFetchTreeBudget(root, 1, 1024); err != nil {
		t.Fatalf("clean working tree rejected: %v", err)
	}
}
