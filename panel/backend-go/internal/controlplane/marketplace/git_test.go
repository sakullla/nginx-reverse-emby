package marketplace

import (
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/client"
	"github.com/go-git/go-git/v5/plumbing/transport/server"
)

func TestRepositorySourceBranchAndTagUseExactNamespaces(t *testing.T) {
	branch, err := sourceReferenceName(Source{RefKind: GitRefKindBranch, RefName: "release"})
	if err != nil || branch.String() != "refs/heads/release" {
		t.Fatalf("branch ref = %q, %v", branch, err)
	}
	tag, err := sourceReferenceName(Source{RefKind: GitRefKindTag, RefName: "release"})
	if err != nil || tag.String() != "refs/tags/release" {
		t.Fatalf("tag ref = %q, %v", tag, err)
	}
	if _, err := sourceReferenceName(Source{RefKind: GitRefKindBranch, RefName: "refs/tags/release"}); err == nil {
		t.Fatal("fully-qualified tag was accepted as a branch name")
	}
}

func TestRepositorySourceTagPeelsAnnotatedAndLightweightToCommit(t *testing.T) {
	root := t.TempDir()
	repository, err := git.PlainInit(root, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "file"), []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	worktree, _ := repository.Worktree()
	_, _ = worktree.Add("file")
	commitHash, err := worktree.Commit("fixture", &git.CommitOptions{Author: &object.Signature{Name: "test", Email: "test@example.com", When: time.Unix(1, 0)}})
	if err != nil {
		t.Fatal(err)
	}
	lightweight, err := repository.CreateTag("light", commitHash, nil)
	if err != nil {
		t.Fatal(err)
	}
	annotated, err := repository.CreateTag("annotated", commitHash, &git.CreateTagOptions{Tagger: &object.Signature{Name: "test", Email: "test@example.com", When: time.Unix(2, 0)}, Message: "release"})
	if err != nil {
		t.Fatal(err)
	}
	for name, hash := range map[string]plumbing.Hash{"lightweight": lightweight.Hash(), "annotated": annotated.Hash()} {
		commit, err := peelCommit(repository, hash, 8)
		if err != nil || commit.Hash != commitHash {
			t.Fatalf("%s peel = %v, %v", name, commit, err)
		}
	}
	commit, _ := repository.CommitObject(commitHash)
	tree, _ := commit.Tree()
	if _, err := peelCommit(repository, tree.Hash, 8); err == nil {
		t.Fatal("tree ref was accepted as a commit")
	}
}

func TestBareCloneRejectsPackWritesAtHardBudget(t *testing.T) {
	repositoryRoot := t.TempDir()
	repository, err := git.PlainInit(repositoryRoot, false)
	if err != nil {
		t.Fatal(err)
	}
	payload := make([]byte, 128<<10)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repositoryRoot, "large.bin"), payload, 0o600); err != nil {
		t.Fatal(err)
	}
	worktree, _ := repository.Worktree()
	if _, err := worktree.Add("large.bin"); err != nil {
		t.Fatal(err)
	}
	if _, err := worktree.Commit("large fixture", &git.CommitOptions{Author: &object.Signature{Name: "test", Email: "test@example.com", When: time.Unix(1, 0)}}); err != nil {
		t.Fatal(err)
	}
	endpoint, err := transport.NewEndpoint("budgettest:///large.git")
	if err != nil {
		t.Fatal(err)
	}
	loader := server.MapLoader{endpoint.String(): repository.Storer}
	client.InstallProtocol("budgettest", server.NewClient(loader))
	bareRoot := filepath.Join(t.TempDir(), "bare")
	if err := os.MkdirAll(bareRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	_, _, err = cloneBareBudgeted(context.Background(), bareRoot, &git.CloneOptions{URL: endpoint.String(), NoCheckout: true}, 4096)
	if err == nil || !strings.Contains(err.Error(), "byte budget") {
		t.Fatalf("oversized bare transfer error = %v", err)
	}
	var written int64
	if walkErr := filepath.WalkDir(bareRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type().IsRegular() {
			info, statErr := entry.Info()
			if statErr != nil {
				return statErr
			}
			written += info.Size()
		}
		return nil
	}); walkErr != nil {
		t.Fatal(walkErr)
	}
	if written > 4096 {
		t.Fatalf("bare transfer peak bytes = %d, want <= 4096", written)
	}
}

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
