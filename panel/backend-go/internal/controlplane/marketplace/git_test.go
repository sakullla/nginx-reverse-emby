package marketplace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
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

func TestBudgetedCheckoutRejectsBlobBeforeWritingDestination(t *testing.T) {
	repositoryRoot := t.TempDir()
	repository, err := git.PlainInit(repositoryRoot, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repositoryRoot, "large.json"), []byte(strings.Repeat("x", 8192)), 0o600); err != nil {
		t.Fatal(err)
	}
	worktree, _ := repository.Worktree()
	if _, err := worktree.Add("large.json"); err != nil {
		t.Fatal(err)
	}
	hash, err := worktree.Commit("fixture", &git.CommitOptions{Author: &object.Signature{Name: "test", Email: "test@example.com", When: time.Unix(1, 0)}})
	if err != nil {
		t.Fatal(err)
	}
	commit, _ := repository.CommitObject(hash)
	tree, _ := commit.Tree()
	destination := filepath.Join(t.TempDir(), "checkout")
	if err := checkoutBudgetedTree(context.Background(), tree, destination, 10, 1024); err == nil {
		t.Fatal("oversized decompressed Git blob was checked out")
	}
	if entries, err := os.ReadDir(destination); err != nil && !os.IsNotExist(err) || err == nil && len(entries) != 0 {
		t.Fatalf("destination peak was not bounded before checkout: %v, %v", entries, err)
	}
}
