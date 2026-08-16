//go:build !integration

package acmeflow

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

func TestFilesystemDurableDirectoryCreationAndFaultMatrix(t *testing.T) {
	ctx := context.Background()
	parent := t.TempDir()
	root := filepath.Join(parent, "nested", "state")
	var points []PersistenceFaultPoint
	store, err := OpenStateStore(root, WithPersistenceFaultInjector(func(point PersistenceFaultPoint) error {
		points = append(points, point)
		return nil
	}))
	if err != nil {
		t.Fatalf("OpenStateStore() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	requireFaultPointOrder(t, points,
		"state.root.created",
		"state.root.permissions_restricted",
		"state.root.directory_synced",
		"state.root.parent_synced",
		"directory.accounts.created",
		"directory.accounts.permissions_restricted",
		"directory.accounts.directory_synced",
		"directory.accounts.parent_synced",
	)

	rootPoints := faultPointsWithPrefix(points, "state.root.")
	if len(rootPoints) != 4 {
		t.Fatalf("root persistence points = %v", rootPoints)
	}
	structuralPoints := append([]PersistenceFaultPoint(nil), rootPoints...)
	accountsPoints := faultPointsWithPrefix(points, "directory.accounts.")
	if len(accountsPoints) != 4 {
		t.Fatalf("top-level accounts persistence points = %v", accountsPoints)
	}
	structuralPoints = append(structuralPoints, accountsPoints...)
	for _, point := range structuralPoints {
		point := point
		t.Run("root_"+strings.ReplaceAll(string(point), ".", "_"), func(t *testing.T) {
			injected := errors.New("injected directory fault")
			faultRoot := filepath.Join(t.TempDir(), "state")
			failed, err := OpenStateStore(faultRoot, WithPersistenceFaultInjector(func(actual PersistenceFaultPoint) error {
				if actual == point {
					return injected
				}
				return nil
			}))
			if failed != nil {
				_ = failed.Close()
			}
			if !errors.Is(err, injected) {
				t.Fatalf("OpenStateStore() error = %v, want injected fault at %q", err, point)
			}
			reopened, err := OpenStateStore(faultRoot)
			if err != nil {
				t.Fatalf("OpenStateStore(retry) error = %v", err)
			}
			if err := reopened.Close(); err != nil {
				t.Fatalf("Close(retry) error = %v", err)
			}
		})
	}

	lookup := AccountLookup{DirectoryURL: "https://ca.example/directory", Email: "ops@example.com"}
	keyPEM := mustRSAKeyPEM(t)
	accountOperation := "directory." + strings.ReplaceAll(
		statePath(accountsDirectory, accountDirectoryName(lookup)),
		"/",
		"_",
	)
	var accountPoints []PersistenceFaultPoint
	discovery, err := OpenStateStore(t.TempDir(), WithPersistenceFaultInjector(func(point PersistenceFaultPoint) error {
		accountPoints = append(accountPoints, point)
		return nil
	}))
	if err != nil {
		t.Fatalf("OpenStateStore(account discovery) error = %v", err)
	}
	accountPoints = nil
	if err := discovery.SaveAccountKey(ctx, lookup, keyPEM); err != nil {
		t.Fatalf("SaveAccountKey(discovery) error = %v", err)
	}
	if err := discovery.Close(); err != nil {
		t.Fatalf("Close(account discovery) error = %v", err)
	}
	accountPoints = faultPointsWithPrefix(accountPoints, accountOperation+".")
	if len(accountPoints) != 4 {
		t.Fatalf("account directory persistence points = %v", accountPoints)
	}
	for _, point := range accountPoints {
		point := point
		t.Run("account_"+strings.ReplaceAll(string(point), ".", "_"), func(t *testing.T) {
			injected := errors.New("injected account-directory fault")
			faultRoot := t.TempDir()
			armed := false
			faultStore, err := OpenStateStore(faultRoot, WithPersistenceFaultInjector(func(actual PersistenceFaultPoint) error {
				if armed && actual == point {
					return injected
				}
				return nil
			}))
			if err != nil {
				t.Fatalf("OpenStateStore() error = %v", err)
			}
			armed = true
			err = faultStore.SaveAccountKey(ctx, lookup, keyPEM)
			if !errors.Is(err, injected) {
				t.Fatalf("SaveAccountKey() error = %v, want injected fault at %q", err, point)
			}
			if err := faultStore.Close(); err != nil {
				t.Fatalf("Close() error = %v", err)
			}

			reopened, err := OpenStateStore(faultRoot)
			if err != nil {
				t.Fatalf("OpenStateStore(retry) error = %v", err)
			}
			t.Cleanup(func() { _ = reopened.Close() })
			if err := reopened.SaveAccountKey(ctx, lookup, keyPEM); err != nil {
				t.Fatalf("SaveAccountKey(retry) error = %v", err)
			}
			record, err := reopened.LoadAccount(ctx, lookup)
			if err != nil {
				t.Fatalf("LoadAccount(retry) error = %v", err)
			}
			if !bytes.Equal(record.KeyPEM, keyPEM) {
				t.Fatal("recovered account key differs")
			}
		})
	}
}

func TestFilesystemRejectsIntermediateSymlinkConfinement(t *testing.T) {
	ctx := context.Background()
	parent := t.TempDir()
	root := filepath.Join(parent, "state")
	store, err := OpenStateStore(root)
	if err != nil {
		t.Fatalf("OpenStateStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	insideTarget := filepath.Join(root, "inside-target")
	if err := os.Mkdir(insideTarget, 0o700); err != nil {
		t.Fatalf("Mkdir(inside target) error = %v", err)
	}
	insideSentinel := filepath.Join(insideTarget, "sentinel.txt")
	if err := os.WriteFile(insideSentinel, []byte("inside-sentinel"), 0o600); err != nil {
		t.Fatalf("WriteFile(inside sentinel) error = %v", err)
	}
	if err := os.Symlink("inside-target", filepath.Join(root, "inside-link")); err != nil {
		t.Skipf("intermediate symlink creation is unavailable: %v", err)
	}

	outsideTarget := filepath.Join(parent, "outside-target")
	if err := os.Mkdir(outsideTarget, 0o700); err != nil {
		t.Fatalf("Mkdir(outside target) error = %v", err)
	}
	outsideSentinel := filepath.Join(outsideTarget, "sentinel.txt")
	if err := os.WriteFile(outsideSentinel, []byte("outside-sentinel"), 0o600); err != nil {
		t.Fatalf("WriteFile(outside sentinel) error = %v", err)
	}
	if err := os.Symlink("../outside-target", filepath.Join(root, "outside-link")); err != nil {
		t.Skipf("escaping symlink creation is unavailable: %v", err)
	}

	for _, name := range []string{"inside-link/sentinel.txt", "outside-link/sentinel.txt"} {
		if _, err := store.fs.readRegularFile(name, 1024); err == nil {
			t.Errorf("readRegularFile(%q) followed an intermediate symlink", name)
		}
		if err := store.fs.writeFileAtomic(strings.TrimSuffix(name, "sentinel.txt")+"created.txt", []byte("created"), "test.confinement"); err == nil {
			t.Errorf("writeFileAtomic(%q) followed an intermediate symlink", name)
		}
		if err := store.fs.removeFile(name, "test.confinement"); err == nil {
			t.Errorf("removeFile(%q) followed an intermediate symlink", name)
		}
		if err := store.fs.removeAll(strings.TrimSuffix(name, "sentinel.txt")+"child", "test.confinement"); err == nil {
			t.Errorf("removeAll(%q) followed an intermediate symlink", name)
		}
	}
	if data, err := os.ReadFile(insideSentinel); err != nil || string(data) != "inside-sentinel" {
		t.Fatalf("inside sentinel changed: data=%q err=%v", data, err)
	}
	if data, err := os.ReadFile(outsideSentinel); err != nil || string(data) != "outside-sentinel" {
		t.Fatalf("outside sentinel changed: data=%q err=%v", data, err)
	}
	if _, err := os.Stat(filepath.Join(insideTarget, "created.txt")); !os.IsNotExist(err) {
		t.Fatalf("inside target was modified: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outsideTarget, "created.txt")); !os.IsNotExist(err) {
		t.Fatalf("outside target was modified: %v", err)
	}

	lookup := AccountLookup{DirectoryURL: "https://other-ca.example/directory", Email: "other@example.com"}
	accountTarget := filepath.Join(root, accountsDirectory, "account-target")
	if err := os.Mkdir(accountTarget, 0o700); err != nil {
		t.Fatalf("Mkdir(account target) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(accountTarget, accountKeyFile), mustRSAKeyPEM(t), 0o600); err != nil {
		t.Fatalf("WriteFile(account target key) error = %v", err)
	}
	if err := os.Symlink("account-target", filepath.Join(root, accountsDirectory, accountDirectoryName(lookup))); err != nil {
		t.Skipf("account symlink creation is unavailable: %v", err)
	}
	if _, err := store.LoadAccount(ctx, lookup); err == nil {
		t.Fatal("LoadAccount() followed an intermediate account-directory symlink")
	}
}

func requireFaultPointOrder(t *testing.T, points []PersistenceFaultPoint, expected ...PersistenceFaultPoint) {
	t.Helper()
	next := 0
	for _, point := range points {
		if next < len(expected) && point == expected[next] {
			next++
		}
	}
	if next != len(expected) {
		t.Fatalf("persistence point order = %v, want subsequence %v", points, expected)
	}
}

func faultPointsWithPrefix(points []PersistenceFaultPoint, prefix string) []PersistenceFaultPoint {
	var result []PersistenceFaultPoint
	for _, point := range uniqueFaultPoints(points) {
		if strings.HasPrefix(string(point), prefix) {
			result = append(result, point)
		}
	}
	return result
}
