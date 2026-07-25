package acmeflow

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestFilesystemPermissionsAndTraversalDefense(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, time.July, 25, 6, 0, 0, 0, time.UTC)
	root := filepath.Join(t.TempDir(), "state")
	store, err := OpenStateStore(root, WithStateClock(func() time.Time { return now }))
	if err != nil {
		t.Fatalf("OpenStateStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	lookup := AccountLookup{DirectoryURL: "https://ca.example/directory", Email: "ops@example.com"}
	if err := store.SaveAccountKey(ctx, lookup, mustRSAKeyPEM(t)); err != nil {
		t.Fatalf("SaveAccountKey() error = %v", err)
	}
	metadata := AccountMetadata{
		Version:      AccountMetadataVersion,
		DirectoryURL: lookup.DirectoryURL,
		Email:        lookup.Email,
		URI:          "https://ca.example/acct/7",
	}
	if err := store.SaveAccountMetadata(ctx, metadata); err != nil {
		t.Fatalf("SaveAccountMetadata() error = %v", err)
	}
	manifest, err := store.StageGeneration(ctx, testGenerationInput(t, 41, now))
	if err != nil {
		t.Fatalf("StageGeneration() error = %v", err)
	}
	if err := store.PromoteGeneration(ctx, manifest.ID, nil); err != nil {
		t.Fatalf("PromoteGeneration() error = %v", err)
	}

	if runtime.GOOS != "windows" {
		err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if info.Mode().Perm()&0o077 != 0 {
				t.Errorf("%s permissions = %04o, broader than owner-only", path, info.Mode().Perm())
			}
			return nil
		})
		if err != nil {
			t.Fatalf("Walk() error = %v", err)
		}
	}

	if _, err := store.LoadGeneration(ctx, "../../outside"); err == nil {
		t.Fatal("LoadGeneration() accepted path traversal")
	}
}

func TestFilesystemRejectsStateSymlinks(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store, err := OpenStateStore(root)
	if err != nil {
		t.Fatalf("OpenStateStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	lookup := AccountLookup{DirectoryURL: "https://ca.example/directory", Email: "ops@example.com"}
	if err := store.SaveAccountKey(ctx, lookup, mustRSAKeyPEM(t)); err != nil {
		t.Fatalf("SaveAccountKey() error = %v", err)
	}
	accountDir := filepath.Join(root, accountsDirectory, accountDirectoryName(lookup))
	metadataPath := filepath.Join(accountDir, accountMetadataFile)
	outside := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(outside, []byte(`{"version":1}`), 0o600); err != nil {
		t.Fatalf("WriteFile(outside) error = %v", err)
	}
	if err := os.Symlink(outside, metadataPath); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}
	if _, err := store.LoadAccount(ctx, lookup); err == nil {
		t.Fatal("LoadAccount() followed a state symlink")
	}
}
