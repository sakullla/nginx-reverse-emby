//go:build integration

package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"errors"
	"os"
	"path/filepath"

	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func TestIntegrationStageVerifiesHashAndExactSize(t *testing.T) {
	t.Parallel()
	payload := []byte("payload")
	for _, tc := range []struct {
		name   string
		mutate func(*model.VersionPackage)
		match  string
	}{
		{name: "hash", mutate: func(pkg *model.VersionPackage) { pkg.SHA256 = strings.Repeat("0", 64) }, match: "sha256 mismatch"},
		{name: "short", mutate: func(pkg *model.VersionPackage) { pkg.Size++ }, match: "size mismatch"},
		{name: "long", mutate: func(pkg *model.VersionPackage) { pkg.Size-- }, match: "size mismatch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			sourcePath := writeTestBinary(t, dir, "source-agent", payload)
			pkg := testVersionPackage(sourcePath, payload)
			tc.mutate(&pkg)
			mgr := testUpdateManager(dir, filepath.Join(dir, "current-agent"), nil)

			_, err := mgr.Stage(t.Context(), pkg)
			if err == nil || !strings.Contains(err.Error(), tc.match) {
				t.Fatalf("Stage() error = %v, want %q", err, tc.match)
			}
		})
	}
}

func TestIntegrationActivateExecFailureRestoresPreviousPointer(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	targetPath := writeTestBinary(t, dir, "nre-agent", []byte("old-agent"))
	sourcePath := writeTestBinary(t, dir, "source-agent", []byte("new-agent"))
	execErr := errors.New("exec failed")
	mgr := testUpdateManager(dir, targetPath, func(context.Context, string, []string, []string) error { return execErr })
	stagedPath, err := mgr.Stage(t.Context(), testVersionPackage(sourcePath, []byte("new-agent")))
	if err != nil {
		t.Fatal(err)
	}

	err = mgr.Activate(t.Context(), stagedPath, "2.0.0")
	if !errors.Is(err, execErr) {
		t.Fatalf("Activate() error = %v", err)
	}
	current, err := mgr.CurrentPackage()
	if err != nil || current.Manifest.SHA256 != sumSHA256([]byte("old-agent")) {
		t.Fatalf("restored current pointer = %+v, %v", current, err)
	}
	previous, err := mgr.PreviousPackage()
	if err != nil || previous.Manifest.SHA256 != sumSHA256([]byte("new-agent")) {
		t.Fatalf("retained failed pointer = %+v, %v", previous, err)
	}
}

func TestIntegrationActivateRecoversWhenCurrentPointerWriteFailsAfterPrevious(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	targetPath := writeTestBinary(t, dir, "nre-agent", []byte("old-agent"))
	sourcePath := writeTestBinary(t, dir, "source-agent", []byte("new-agent"))
	mgr := testUpdateManager(dir, targetPath, func(context.Context, string, []string, []string) error { return ErrRestartRequested })
	stagedPath, err := mgr.Stage(t.Context(), testVersionPackage(sourcePath, []byte("new-agent")))
	if err != nil {
		t.Fatal(err)
	}
	writeErr := errors.New("current pointer persistence failed")
	var hook func(string, PackagePointer) error
	hook = func(name string, pointer PackagePointer) error {
		if name == currentPointerFile {
			return writeErr
		}
		mgr.savePointer = nil
		err := mgr.writePointer(name, pointer)
		mgr.savePointer = hook
		return err
	}
	mgr.savePointer = hook

	err = mgr.Activate(t.Context(), stagedPath, "2.0.0")
	if !errors.Is(err, writeErr) {
		t.Fatalf("Activate() error = %v", err)
	}
	if _, err := mgr.CurrentPackage(); !os.IsNotExist(err) {
		t.Fatalf("current pointer after interrupted promotion error = %v, want not exist", err)
	}
	previous, err := mgr.PreviousPackage()
	if err != nil || previous.Manifest.SHA256 != sumSHA256([]byte("old-agent")) {
		t.Fatalf("previous pointer after interrupted promotion = %+v, %v", previous, err)
	}

	mgr.savePointer = nil
	if err := mgr.Activate(t.Context(), stagedPath, "2.0.0"); !errors.Is(err, ErrRestartRequested) {
		t.Fatalf("Activate(recovery) error = %v", err)
	}
	current, err := mgr.CurrentPackage()
	if err != nil || current.Manifest.SHA256 != sumSHA256([]byte("new-agent")) {
		t.Fatalf("recovered current pointer = %+v, %v", current, err)
	}
}

func TestIntegrationActivateReconcilesUncertainPointerDirectorySync(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		failAt int
	}{
		{name: "previous pointer", failAt: 1},
		{name: "current pointer", failAt: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			targetPath := writeTestBinary(t, dir, "nre-agent", []byte("old-agent"))
			sourcePath := writeTestBinary(t, dir, "source-agent", []byte("new-agent"))
			execCalls := 0
			mgr := testUpdateManager(dir, targetPath, func(context.Context, string, []string, []string) error {
				execCalls++
				return ErrRestartRequested
			})
			stagedPath, err := mgr.Stage(t.Context(), testVersionPackage(sourcePath, []byte("new-agent")))
			if err != nil {
				t.Fatal(err)
			}
			failUpdateStateDirectorySync(mgr, tc.failAt)

			if err := mgr.Activate(t.Context(), stagedPath, "2.0.0"); !errors.Is(err, ErrRestartRequested) {
				t.Fatalf("Activate() error = %v", err)
			}
			current, currentErr := mgr.CurrentPackage()
			previous, previousErr := mgr.PreviousPackage()
			if currentErr != nil || previousErr != nil || current.Manifest.SHA256 != sumSHA256([]byte("new-agent")) || previous.Manifest.SHA256 != sumSHA256([]byte("old-agent")) {
				t.Fatalf("reconciled current/previous = %+v/%+v, errors = %v/%v", current, previous, currentErr, previousErr)
			}
			if execCalls != 1 {
				t.Fatalf("exec calls = %d, want 1", execCalls)
			}
		})
	}
}

func TestIntegrationRestorePreviousSwapsDurablePackagePointers(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	targetPath := writeTestBinary(t, dir, "nre-agent", []byte("old-agent"))
	sourcePath := writeTestBinary(t, dir, "source-agent", []byte("new-agent"))
	mgr := testUpdateManager(dir, targetPath, func(context.Context, string, []string, []string) error { return ErrRestartRequested })
	stagedPath, err := mgr.Stage(t.Context(), testVersionPackage(sourcePath, []byte("new-agent")))
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.Activate(t.Context(), stagedPath, "2.0.0"); !errors.Is(err, ErrRestartRequested) {
		t.Fatal(err)
	}
	if err := mgr.RestorePrevious(); err != nil {
		t.Fatalf("RestorePrevious() error = %v", err)
	}
	current, _ := mgr.CurrentPackage()
	previous, _ := mgr.PreviousPackage()
	if current.Manifest.SHA256 != sumSHA256([]byte("old-agent")) || previous.Manifest.SHA256 != sumSHA256([]byte("new-agent")) {
		t.Fatalf("restored pointers current/previous = %+v/%+v", current, previous)
	}
}

func failUpdateStateDirectorySync(mgr *UpdateManager, failAt int) {
	original := mgr.syncDirectory
	calls := 0
	mgr.syncDirectory = func(path string) error {
		if filepath.Clean(path) == filepath.Clean(mgr.stateRoot()) {
			calls++
			if calls == failAt {
				return errors.New("injected update state directory sync failure")
			}
		}
		return original(path)
	}
}

func testUpdateManager(root, executablePath string, execFn ExecFunc) *UpdateManager {
	mgr := NewUpdateManager(root, executablePath, nil, nil, execFn, nil)
	mgr.platform = "linux-amd64"
	return mgr
}

func testVersionPackage(sourcePath string, payload []byte) model.VersionPackage {
	return model.VersionPackage{
		URL:      fileURL(sourcePath),
		SHA256:   sumSHA256(payload),
		Platform: "linux-amd64",
		Filename: "nre-agent-linux-amd64",
		Size:     int64(len(payload)),
	}
}

func writeTestBinary(t *testing.T, dir, name string, payload []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, payload, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func sumSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func fileURL(path string) string {
	return "file:///" + filepath.ToSlash(path)
}
