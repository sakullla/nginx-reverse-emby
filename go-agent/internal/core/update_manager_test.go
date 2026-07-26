//go:build integration

package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func TestIntegrationStageRejectsIncompleteOrUnsupportedManifestBeforeMutation(t *testing.T) {
	payload := []byte("payload")
	for _, tc := range []struct {
		name   string
		mutate func(*model.VersionPackage)
	}{
		{name: "invalid digest", mutate: func(pkg *model.VersionPackage) { pkg.SHA256 = "deadbeef" }},
		{name: "wrong platform", mutate: func(pkg *model.VersionPackage) { pkg.Platform = "linux-arm64" }},
		{name: "unsupported platform", mutate: func(pkg *model.VersionPackage) { pkg.Platform = "darwin-amd64" }},
		{name: "path traversal filename", mutate: func(pkg *model.VersionPackage) { pkg.Filename = "../nre-agent" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			sourcePath := writeTestBinary(t, dir, "source-agent", payload)
			pkg := testVersionPackage(sourcePath, payload)
			tc.mutate(&pkg)
			mgr := testUpdateManager(dir, filepath.Join(dir, "current-agent"), nil)

			if _, err := mgr.Stage(t.Context(), pkg); err == nil {
				t.Fatal("Stage() error = nil, want manifest rejection")
			}
			if _, err := os.Stat(filepath.Join(dir, updateDirectory)); !os.IsNotExist(err) {
				t.Fatalf("updates directory exists after manifest rejection: %v", err)
			}
		})
	}
}

func TestIntegrationStageDerivesAndPersistsLegacyPackageSize(t *testing.T) {
	dir := t.TempDir()
	payload := []byte("legacy-package-without-size")
	sourcePath := writeTestBinary(t, dir, "source-agent", payload)
	pkg := testVersionPackage(sourcePath, payload)
	pkg.Size = 0
	mgr := testUpdateManager(dir, filepath.Join(dir, "current-agent"), nil)

	stagedPath, err := mgr.Stage(t.Context(), pkg)
	if err != nil {
		t.Fatal(err)
	}
	pointer, err := mgr.readPackage(stagedPath)
	if err != nil {
		t.Fatal(err)
	}
	if pointer.Manifest.Size != int64(len(payload)) {
		t.Fatalf("derived package size = %d, want %d", pointer.Manifest.Size, len(payload))
	}
}

func TestIntegrationStageVerifiesHashAndExactSize(t *testing.T) {
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

func TestIntegrationStageStoresImmutableContentAddressedPackageAndManifest(t *testing.T) {
	dir := t.TempDir()
	payload := []byte("new-agent")
	sourcePath := writeTestBinary(t, dir, "source-agent", payload)
	pkg := testVersionPackage(sourcePath, payload)
	mgr := testUpdateManager(dir, filepath.Join(dir, "current-agent"), nil)

	stagedPath, err := mgr.Stage(t.Context(), pkg)
	if err != nil {
		t.Fatalf("Stage() error = %v", err)
	}
	wantPath := filepath.Join(dir, updateDirectory, updatePackageDir, pkg.SHA256, packageBinaryFile)
	if stagedPath != wantPath {
		t.Fatalf("staged path = %q, want %q", stagedPath, wantPath)
	}
	got, err := os.ReadFile(stagedPath)
	if err != nil || !reflect.DeepEqual(got, payload) {
		t.Fatalf("staged payload = %q, error = %v", got, err)
	}
	manifestPayload, err := os.ReadFile(filepath.Join(filepath.Dir(stagedPath), packageManifestFile))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest model.PackageManifest
	if err := json.Unmarshal(manifestPayload, &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if manifest.SchemaVersion != model.PackageManifestVersion || manifest.SHA256 != pkg.SHA256 || manifest.Size != int64(len(payload)) || manifest.Platform != "linux-amd64" {
		t.Fatalf("manifest = %+v", manifest)
	}

	again, err := mgr.Stage(t.Context(), pkg)
	if err != nil || again != stagedPath {
		t.Fatalf("idempotent Stage() = %q, %v", again, err)
	}
	if err := os.Remove(filepath.Join(filepath.Dir(stagedPath), packageManifestFile)); err != nil {
		t.Fatal(err)
	}
	recovered, err := mgr.Stage(t.Context(), pkg)
	if err != nil || recovered != stagedPath {
		t.Fatalf("interrupted manifest recovery Stage() = %q, %v", recovered, err)
	}
	if err := os.Remove(sourcePath); err != nil {
		t.Fatal(err)
	}
	cached, err := mgr.Stage(t.Context(), pkg)
	if err != nil || cached != stagedPath {
		t.Fatalf("offline cached Stage() = %q, %v", cached, err)
	}
	if err := os.Chmod(stagedPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stagedPath, []byte("tampered"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.Stage(t.Context(), pkg); err == nil {
		t.Fatal("Stage() accepted a corrupted immutable package")
	}
}

func TestIntegrationActivateUsesPointersAndPromotesInstalledExecutable(t *testing.T) {
	dir := t.TempDir()
	targetPath := writeTestBinary(t, dir, "nre-agent", []byte("old-agent"))
	sourcePath := writeTestBinary(t, dir, "source-agent", []byte("new-agent"))
	var gotBinary string
	var gotArgv, gotEnv []string
	mgr := testUpdateManager(dir, targetPath, func(_ context.Context, binary string, argv []string, env []string) error {
		gotBinary = binary
		gotArgv = append([]string(nil), argv...)
		gotEnv = append([]string(nil), env...)
		return ErrRestartRequested
	})
	mgr.argv = []string{targetPath, "--flag"}
	mgr.env = []string{"PATH=/bin"}
	stagedPath, err := mgr.Stage(t.Context(), testVersionPackage(sourcePath, []byte("new-agent")))
	if err != nil {
		t.Fatal(err)
	}

	err = mgr.Activate(t.Context(), stagedPath, "2.0.0")
	if !errors.Is(err, ErrRestartRequested) {
		t.Fatalf("Activate() error = %v", err)
	}
	if gotBinary != stagedPath || !reflect.DeepEqual(gotArgv, []string{stagedPath, "--flag"}) {
		t.Fatalf("exec binary/argv = %q/%+v", gotBinary, gotArgv)
	}
	if !containsEnv(gotEnv, "NRE_AGENT_VERSION=2.0.0") {
		t.Fatalf("exec env = %+v", gotEnv)
	}
	if got, _ := os.ReadFile(targetPath); string(got) != "new-agent" {
		t.Fatalf("installed executable was not promoted: %q", got)
	}
	current, err := mgr.CurrentPackage()
	if err != nil || current.Manifest.SHA256 != sumSHA256([]byte("new-agent")) || current.DesiredVersion != "2.0.0" {
		t.Fatalf("current pointer = %+v, %v", current, err)
	}
	previous, err := mgr.PreviousPackage()
	if err != nil || previous.Manifest.SHA256 != sumSHA256([]byte("old-agent")) {
		t.Fatalf("previous pointer = %+v, %v", previous, err)
	}
}

func TestIntegrationActivateRecoversMissingInstalledEntrypointFromRunningPackage(t *testing.T) {
	dir := t.TempDir()
	installedPath := filepath.Join(dir, "bin", "nre-agent")
	if err := os.MkdirAll(filepath.Dir(installedPath), 0o755); err != nil {
		t.Fatalf("mkdir installed executable parent: %v", err)
	}

	runningPayload := []byte("currently-running-agent")
	runningSource := writeTestBinary(t, dir, "running-agent", runningPayload)
	setup := testUpdateManager(dir, installedPath, nil)
	runningPath, err := setup.Stage(t.Context(), testVersionPackage(runningSource, runningPayload))
	if err != nil {
		t.Fatalf("stage running package: %v", err)
	}

	nextPayload := []byte("next-agent")
	nextSource := writeTestBinary(t, dir, "next-agent", nextPayload)
	manager := NewUpdateManager(
		dir,
		runningPath,
		nil,
		[]string{installExecutableEnv + "=" + installedPath},
		func(context.Context, string, []string, []string) error { return ErrRestartRequested },
		nil,
	)
	manager.platform = "linux-amd64"

	stagedPath, err := manager.Stage(t.Context(), testVersionPackage(nextSource, nextPayload))
	if err != nil {
		t.Fatalf("stage next package: %v", err)
	}
	if err := manager.Activate(t.Context(), stagedPath, "1.1.0"); !errors.Is(err, ErrRestartRequested) {
		t.Fatalf("Activate() error = %v, want ErrRestartRequested", err)
	}

	installedPayload, err := os.ReadFile(installedPath)
	if err != nil {
		t.Fatalf("read restored installed executable: %v", err)
	}
	if !reflect.DeepEqual(installedPayload, nextPayload) {
		t.Fatalf("installed executable = %q, want %q", installedPayload, nextPayload)
	}
	previous, err := manager.PreviousPackage()
	if err != nil || previous.Manifest.SHA256 != sumSHA256(runningPayload) {
		t.Fatalf("previous pointer = %+v, %v", previous, err)
	}
	current, err := manager.CurrentPackage()
	if err != nil || current.Manifest.SHA256 != sumSHA256(nextPayload) {
		t.Fatalf("current pointer = %+v, %v", current, err)
	}
}

func TestIntegrationActivateBootstrapsMatchingRunningContentWithoutManifestConflict(t *testing.T) {
	dir := t.TempDir()
	payload := []byte("same-agent")
	targetPath := writeTestBinary(t, dir, "nre-agent", payload)
	sourcePath := writeTestBinary(t, dir, "release-asset", payload)
	mgr := testUpdateManager(dir, targetPath, func(context.Context, string, []string, []string) error { return ErrRestartRequested })
	stagedPath, err := mgr.Stage(t.Context(), testVersionPackage(sourcePath, payload))
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.Activate(t.Context(), stagedPath, "2.0.0"); !errors.Is(err, ErrRestartRequested) {
		t.Fatalf("Activate() error = %v", err)
	}
	current, err := mgr.CurrentPackage()
	if err != nil || current.Manifest.SHA256 != sumSHA256(payload) || current.DesiredVersion != "2.0.0" {
		t.Fatalf("current pointer = %+v, %v", current, err)
	}
}

func TestIntegrationStageRejectsSymlinkedStoreAncestors(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated Windows privileges")
	}
	for _, ancestor := range []string{"updates", "packages", "digest"} {
		t.Run(ancestor, func(t *testing.T) {
			dir := t.TempDir()
			payload := []byte("payload")
			sourcePath := writeTestBinary(t, dir, "source-agent", payload)
			pkg := testVersionPackage(sourcePath, payload)
			outside := t.TempDir()
			var linkPath string
			switch ancestor {
			case "updates":
				linkPath = filepath.Join(dir, updateDirectory)
			case "packages":
				if err := os.Mkdir(filepath.Join(dir, updateDirectory), 0o755); err != nil {
					t.Fatal(err)
				}
				linkPath = filepath.Join(dir, updateDirectory, updatePackageDir)
			case "digest":
				if err := os.MkdirAll(filepath.Join(dir, updateDirectory, updatePackageDir), 0o755); err != nil {
					t.Fatal(err)
				}
				linkPath = filepath.Join(dir, updateDirectory, updatePackageDir, pkg.SHA256)
			}
			if err := os.Symlink(outside, linkPath); err != nil {
				t.Fatal(err)
			}
			mgr := testUpdateManager(dir, filepath.Join(dir, "current-agent"), nil)
			if _, err := mgr.Stage(t.Context(), pkg); err == nil || !strings.Contains(strings.ToLower(err.Error()), "symlink") && !strings.Contains(err.Error(), "real directory") {
				t.Fatalf("Stage() error = %v, want ancestor symlink rejection", err)
			}
			entries, err := os.ReadDir(outside)
			if err != nil || len(entries) != 0 {
				t.Fatalf("outside directory mutated: entries=%v error=%v", entries, err)
			}
		})
	}
}

func TestIntegrationActivateRejectsSymlinkedStateDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated Windows privileges")
	}
	dir := t.TempDir()
	payload := []byte("new-agent")
	targetPath := writeTestBinary(t, dir, "nre-agent", []byte("old-agent"))
	sourcePath := writeTestBinary(t, dir, "source-agent", payload)
	execCalls := 0
	mgr := testUpdateManager(dir, targetPath, func(context.Context, string, []string, []string) error {
		execCalls++
		return ErrRestartRequested
	})
	stagedPath, err := mgr.Stage(t.Context(), testVersionPackage(sourcePath, payload))
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, mgr.stateRoot()); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Activate(t.Context(), stagedPath, "2.0.0"); err == nil || !strings.Contains(strings.ToLower(err.Error()), "symlink") {
		t.Fatalf("Activate() error = %v, want state symlink rejection", err)
	}
	if execCalls != 0 {
		t.Fatalf("exec calls = %d, want 0", execCalls)
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatalf("outside state directory mutated: entries=%v error=%v", entries, err)
	}
}

func TestIntegrationActivateExecFailureRestoresPreviousPointer(t *testing.T) {
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

func TestIntegrationRestorePreviousReconcilesUncertainCurrentPointer(t *testing.T) {
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
	failUpdateStateDirectorySync(mgr, 1)
	if err := mgr.RestorePrevious(); err != nil {
		t.Fatalf("RestorePrevious() error = %v", err)
	}
	current, _ := mgr.CurrentPackage()
	previous, _ := mgr.PreviousPackage()
	if current.Manifest.SHA256 != sumSHA256([]byte("old-agent")) || previous.Manifest.SHA256 != sumSHA256([]byte("new-agent")) {
		t.Fatalf("reconciled restore current/previous = %+v/%+v", current, previous)
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

func containsEnv(env []string, needle string) bool {
	for _, entry := range env {
		if entry == needle {
			return true
		}
	}
	return false
}
