//go:build integration

package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func retentionManager(t *testing.T) *UpdateManager {
	t.Helper()
	root := t.TempDir()
	m := testUpdateManager(root, filepath.Join(root, "installed-agent"), nil)
	m.runningPackageDigests = func() ([]string, error) { return nil, nil }
	return m
}

func retentionPackage(t *testing.T, m *UpdateManager, name string, old bool) PackagePointer {
	t.Helper()
	payload := []byte(name)
	source := writeTestBinary(t, m.root, "source-"+name, payload)
	path, err := m.Stage(t.Context(), testVersionPackage(source, payload))
	if err != nil {
		t.Fatal(err)
	}
	pointer, err := m.readPackage(path)
	if err != nil {
		t.Fatal(err)
	}
	if old {
		agePackage(t, path)
	}
	return pointer
}

func agePackage(t *testing.T, binary string) {
	t.Helper()
	old := time.Now().Add(-2 * updatePackageGracePeriod)
	if err := os.Chtimes(filepath.Dir(binary), old, old); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationPackageRetentionProtectsReferencesAndReclaimsHistory(t *testing.T) {
	t.Parallel()
	m := retentionManager(t)
	current := retentionPackage(t, m, "current", true)
	previous := retentionPackage(t, m, "previous", true)
	running := retentionPackage(t, m, "old-supervisor", true)
	obsolete := retentionPackage(t, m, "obsolete", true)
	recent := retentionPackage(t, m, "recent", false)
	staged := retentionPackage(t, m, "staged", true)
	for name, pointer := range map[string]PackagePointer{currentPointerFile: current, previousPointerFile: previous} {
		if err := m.writePointer(name, pointer); err != nil {
			t.Fatal(err)
		}
	}
	m.runningPackageDigests = func() ([]string, error) { return []string{running.Manifest.SHA256}, nil }
	if err := m.CleanupPackages(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(m.pointerPath(obsolete))); !os.IsNotExist(err) {
		t.Fatalf("obsolete package remains: %v", err)
	}
	for _, pointer := range []PackagePointer{current, previous, running, recent, staged} {
		if _, err := m.readPackage(m.pointerPath(pointer)); err != nil {
			t.Fatalf("retained package %s is unusable: %v", pointer.Manifest.SHA256, err)
		}
	}
	// A later startup releases the abandoned staging reference, and the grace
	// period eventually stops protecting an otherwise unreferenced download.
	m.stagedPackageSHA = ""
	agePackage(t, m.pointerPath(recent))
	if err := m.CleanupPackages(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, pointer := range []PackagePointer{recent, staged} {
		if _, err := os.Stat(m.pointerPath(pointer)); !os.IsNotExist(err) {
			t.Fatalf("unreferenced package was not reclaimed: %v", err)
		}
	}
}

func TestIntegrationStageReclaimsOldPackagesAndProtectsCachedTarget(t *testing.T) {
	t.Parallel()
	m := retentionManager(t)
	current := retentionPackage(t, m, "current", true)
	target := retentionPackage(t, m, "cached-target", true)
	obsolete := retentionPackage(t, m, "obsolete", true)
	if err := m.writePointer(currentPointerFile, current); err != nil {
		t.Fatal(err)
	}
	m.stagedPackageSHA = ""
	// No download is possible: Stage must preserve and reuse the old cache hit.
	pkg := testVersionPackage(filepath.Join(m.root, "missing-source"), []byte("cached-target"))
	path, err := m.Stage(t.Context(), pkg)
	if err != nil || path != m.pointerPath(target) {
		t.Fatalf("Stage(cache hit) = %q, %v", path, err)
	}
	if _, err := os.Stat(m.pointerPath(obsolete)); !os.IsNotExist(err) {
		t.Fatalf("Stage did not reclaim obsolete package: %v", err)
	}
}

func TestIntegrationPackageRetentionFailsClosedWithoutReliableReferences(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"process-error", "corrupt-current", "corrupt-previous", "missing-baseline", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			m := retentionManager(t)
			current := retentionPackage(t, m, "current", true)
			obsolete := retentionPackage(t, m, "obsolete", true)
			m.stagedPackageSHA = ""
			if err := m.writePointer(currentPointerFile, current); err != nil {
				t.Fatal(err)
			}
			ctx := t.Context()
			switch mode {
			case "process-error":
				m.runningPackageDigests = func() ([]string, error) { return nil, errors.New("procfs unavailable") }
			case "corrupt-current", "corrupt-previous":
				name := currentPointerFile
				if mode == "corrupt-previous" {
					name = previousPointerFile
				}
				if err := os.WriteFile(filepath.Join(m.stateRoot(), name), []byte("invalid"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "missing-baseline":
				if err := os.Remove(filepath.Join(m.stateRoot(), currentPointerFile)); err != nil {
					t.Fatal(err)
				}
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			err := m.CleanupPackages(ctx)
			if mode != "missing-baseline" && err == nil {
				t.Fatal("cleanup did not report unreliable retention evidence")
			}
			if _, err := m.readPackage(m.pointerPath(obsolete)); err != nil {
				t.Fatalf("cleanup removed package without reliable references: %v", err)
			}
		})
	}
}

func TestIntegrationPackageRetentionLeavesUnknownDirectoryContents(t *testing.T) {
	t.Parallel()
	m := retentionManager(t)
	current := retentionPackage(t, m, "current", true)
	unknown := retentionPackage(t, m, "unknown", true)
	if err := os.WriteFile(filepath.Join(filepath.Dir(m.pointerPath(unknown)), "operator-backup"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	agePackage(t, m.pointerPath(unknown))
	m.stagedPackageSHA = ""
	if err := m.writePointer(currentPointerFile, current); err != nil {
		t.Fatal(err)
	}
	if err := m.CleanupPackages(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := m.readPackage(m.pointerPath(unknown)); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationPackageRetentionInspectsActualRunningImage(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux procfs running image inspection")
	}
	m := retentionManager(t)
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	m.runningExecutablePath = path
	want, err := fileDigest("/proc/self/exe")
	if err != nil {
		t.Fatal(err)
	}
	digests, err := m.livePackageDigests()
	if err != nil || !strings.Contains(strings.Join(digests, ","), want) {
		t.Fatalf("running package inspection = %v, %v; want %s", digests, err, want)
	}
}

func TestIntegrationPackageRetentionDoesNotFollowSymlinks(t *testing.T) {
	t.Parallel()
	m := retentionManager(t)
	current := retentionPackage(t, m, "current", true)
	obsolete := retentionPackage(t, m, "obsolete", true)
	m.stagedPackageSHA = ""
	if err := m.writePointer(currentPointerFile, current); err != nil {
		t.Fatal(err)
	}
	outside := writeTestBinary(t, t.TempDir(), "outside", []byte("obsolete"))
	binary := m.pointerPath(obsolete)
	if err := os.Remove(binary); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, binary); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	agePackage(t, binary)
	if err := m.CleanupPackages(t.Context()); err == nil {
		t.Fatal("cleanup accepted a symlink as an immutable package binary")
	}
	if payload, err := os.ReadFile(outside); err != nil || string(payload) != "obsolete" {
		t.Fatalf("cleanup altered symlink target: %q, %v", payload, err)
	}
}

func TestIntegrationCleanupFailureDoesNotPreventPackageStage(t *testing.T) {
	t.Parallel()
	m := retentionManager(t)
	current := retentionPackage(t, m, "current", true)
	if err := m.writePointer(currentPointerFile, current); err != nil {
		t.Fatal(err)
	}
	m.runningPackageDigests = func() ([]string, error) { return nil, errors.New("procfs unavailable") }
	payload := []byte("new-agent")
	source := writeTestBinary(t, m.root, "new-source", payload)
	path, err := m.Stage(t.Context(), testVersionPackage(source, payload))
	if err != nil {
		t.Fatalf("cleanup failure blocked Stage: %v", err)
	}
	if _, err := m.readPackage(path); err != nil {
		t.Fatal(err)
	}
}
