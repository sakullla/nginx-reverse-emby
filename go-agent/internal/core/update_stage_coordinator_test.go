//go:build !integration

package core

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

type coordinatorTestUpdater struct {
	mu            sync.Mutex
	started       map[string]chan struct{}
	canceled      map[string]chan struct{}
	release       map[string]chan struct{}
	stageErr      map[string]error
	stageCalls    map[string]int
	activateCalls []coordinatorActivation
	activateErr   error
}

type coordinatorActivation struct {
	stagedPath     string
	desiredVersion string
}

func newCoordinatorTestUpdater(targets ...string) *coordinatorTestUpdater {
	updater := &coordinatorTestUpdater{
		started:    make(map[string]chan struct{}, len(targets)),
		canceled:   make(map[string]chan struct{}, len(targets)),
		release:    make(map[string]chan struct{}, len(targets)),
		stageErr:   make(map[string]error),
		stageCalls: make(map[string]int),
	}
	for _, target := range targets {
		updater.started[target] = make(chan struct{})
		updater.canceled[target] = make(chan struct{})
		updater.release[target] = make(chan struct{})
	}
	return updater
}

func (u *coordinatorTestUpdater) Stage(ctx context.Context, pkg model.VersionPackage) (string, error) {
	u.mu.Lock()
	u.stageCalls[pkg.URL]++
	started := u.started[pkg.URL]
	release := u.release[pkg.URL]
	err := u.stageErr[pkg.URL]
	u.mu.Unlock()
	close(started)
	if err != nil {
		return "", err
	}
	select {
	case <-ctx.Done():
		close(u.canceled[pkg.URL])
		<-release
		return "", ctx.Err()
	case <-release:
		return "staged/" + pkg.URL, nil
	}
}

func (u *coordinatorTestUpdater) Activate(_ context.Context, stagedPath, desiredVersion string) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.activateCalls = append(u.activateCalls, coordinatorActivation{stagedPath: stagedPath, desiredVersion: desiredVersion})
	return u.activateErr
}

func (u *coordinatorTestUpdater) calls(target string) int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.stageCalls[target]
}

func (u *coordinatorTestUpdater) activations() []coordinatorActivation {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]coordinatorActivation(nil), u.activateCalls...)
}

func TestPackageStageCoordinatorReusesCanonicalIdentityAcrossLocatorAndVersionChanges(t *testing.T) {
	oldURL := "https://old.example.test/nre-agent"
	newURL := "https://new.example.test/nre-agent"
	updater := newCoordinatorTestUpdater(oldURL, newURL)
	coordinator := NewPackageStageCoordinator()
	oldPackage := coordinatorTestPackage(oldURL, "a")
	oldPackage.Filename = "  nre-agent-linux-amd64  "
	newPackage := coordinatorTestPackage(newURL, "a")

	if err := coordinator.Handle(t.Context(), updater, oldPackage, "2.0.0"); !errors.Is(err, errPackageStagePending) {
		t.Fatalf("Handle(old) error = %v, want staging pending", err)
	}
	waitForCoordinatorSignal(t, updater.started[oldURL])
	if err := coordinator.Handle(t.Context(), updater, newPackage, "3.0.0"); !errors.Is(err, errPackageStagePending) {
		t.Fatalf("Handle(new) error = %v, want staging pending", err)
	}
	select {
	case <-updater.started[newURL]:
		t.Fatal("URL-only change started a second Stage for the same immutable package")
	default:
	}
	close(updater.release[oldURL])

	waitForCoordinatorResult(t, func() error {
		return coordinator.Handle(t.Context(), updater, newPackage, "3.0.0")
	}, ErrRestartRequested)
	if updater.calls(oldURL) != 1 || updater.calls(newURL) != 0 {
		t.Fatalf("Stage calls old/new locator = %d/%d, want 1/0", updater.calls(oldURL), updater.calls(newURL))
	}
	if got := updater.activations(); len(got) != 1 || got[0].stagedPath != "staged/"+oldURL || got[0].desiredVersion != "3.0.0" {
		t.Fatalf("activations = %+v, want reused old locator staged path fenced by current version", got)
	}
}

func TestPackageStageCoordinatorWaitsForCanceledWorkerBeforeReplacement(t *testing.T) {
	oldURL := "https://updates.example.test/old-agent"
	newURL := "https://updates.example.test/new-agent"
	updater := newCoordinatorTestUpdater(oldURL, newURL)
	coordinator := NewPackageStageCoordinator()
	oldPackage := coordinatorTestPackage(oldURL, "a")
	newPackage := coordinatorTestPackage(newURL, "b")

	if err := coordinator.Handle(t.Context(), updater, oldPackage, "2.0.0"); !errors.Is(err, errPackageStagePending) {
		t.Fatalf("Handle(old) error = %v, want staging pending", err)
	}
	waitForCoordinatorSignal(t, updater.started[oldURL])
	if err := coordinator.Handle(t.Context(), updater, newPackage, "3.0.0"); !errors.Is(err, errPackageStagePending) {
		t.Fatalf("Handle(new) error = %v, want staging pending", err)
	}
	waitForCoordinatorSignal(t, updater.canceled[oldURL])
	select {
	case <-updater.started[newURL]:
		t.Fatal("replacement Stage started before canceled worker converged")
	default:
	}
	close(updater.release[oldURL])
	waitForCoordinatorStart(t, func() error {
		return coordinator.Handle(t.Context(), updater, newPackage, "3.0.0")
	}, updater.started[newURL])
	close(updater.release[newURL])
	waitForCoordinatorResult(t, func() error {
		return coordinator.Handle(t.Context(), updater, newPackage, "3.0.0")
	}, ErrRestartRequested)
	if updater.calls(oldURL) != 1 || updater.calls(newURL) != 1 {
		t.Fatalf("Stage calls old/new identity = %d/%d, want 1/1", updater.calls(oldURL), updater.calls(newURL))
	}
}

func TestPackageStageCoordinatorReportsFailureWithoutActivation(t *testing.T) {
	stageErr := errors.New("checksum mismatch")
	packageURL := "https://updates.example.test/failed-agent"
	updater := newCoordinatorTestUpdater(packageURL)
	updater.stageErr[packageURL] = stageErr
	coordinator := NewPackageStageCoordinator()
	pkg := coordinatorTestPackage(packageURL, "c")

	if err := coordinator.Handle(t.Context(), updater, pkg, "2.0.0"); !errors.Is(err, errPackageStagePending) {
		t.Fatalf("Handle() error = %v, want staging pending", err)
	}
	waitForCoordinatorSignal(t, updater.started[packageURL])
	waitForCoordinatorResult(t, func() error {
		return coordinator.Handle(t.Context(), updater, pkg, "2.0.0")
	}, stageErr)
	if updater.calls(packageURL) != 1 || len(updater.activations()) != 0 {
		t.Fatalf("failed target Stage/Activate calls = %d/%d, want 1/0", updater.calls(packageURL), len(updater.activations()))
	}
}

func TestPackageStageCoordinatorDoesNotReportFailedActivationAsSuccess(t *testing.T) {
	activateErr := errors.New("child activation failed")
	packageURL := "https://updates.example.test/activate-agent"
	updater := newCoordinatorTestUpdater(packageURL)
	updater.activateErr = activateErr
	coordinator := NewPackageStageCoordinator()
	pkg := coordinatorTestPackage(packageURL, "d")

	if err := coordinator.Handle(t.Context(), updater, pkg, "2.0.0"); !errors.Is(err, errPackageStagePending) {
		t.Fatalf("Handle() error = %v, want staging pending", err)
	}
	waitForCoordinatorSignal(t, updater.started[packageURL])
	close(updater.release[packageURL])
	waitForCoordinatorResult(t, func() error {
		return coordinator.Handle(t.Context(), updater, pkg, "2.0.0")
	}, activateErr)
	if got := updater.activations(); len(got) != 1 || got[0].stagedPath != "staged/"+packageURL {
		t.Fatalf("failed activations = %+v, want one staged package attempt", got)
	}
}

func TestPackageStageCoordinatorCloseCancelsStage(t *testing.T) {
	packageURL := "https://updates.example.test/shutdown-agent"
	updater := newCoordinatorTestUpdater(packageURL)
	coordinator := NewPackageStageCoordinator()
	pkg := coordinatorTestPackage(packageURL, "e")

	if err := coordinator.Handle(t.Context(), updater, pkg, "2.0.0"); !errors.Is(err, errPackageStagePending) {
		t.Fatalf("Handle() error = %v, want staging pending", err)
	}
	waitForCoordinatorSignal(t, updater.started[packageURL])
	closeDone := make(chan struct{})
	go func() {
		coordinator.Close()
		close(closeDone)
	}()
	waitForCoordinatorSignal(t, updater.canceled[packageURL])
	select {
	case <-closeDone:
		t.Fatal("Close returned before the Stage worker converged")
	default:
	}
	close(updater.release[packageURL])
	waitForCoordinatorSignal(t, closeDone)
	if err := coordinator.Handle(t.Context(), updater, pkg, "2.0.0"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Handle(after Close) error = %v, want context cancellation", err)
	}
	if len(updater.activations()) != 0 {
		t.Fatalf("shutdown target activations = %v, want none", updater.activations())
	}
}

func coordinatorTestPackage(rawURL, digestByte string) model.VersionPackage {
	return model.VersionPackage{
		URL: rawURL, SHA256: strings.Repeat(digestByte, 64), Platform: "linux-amd64",
		Filename: "nre-agent-linux-amd64", Size: 1024,
	}
}

func waitForCoordinatorSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatal("coordinator operation did not start")
	}
}

func waitForCoordinatorResult(t *testing.T, operation func() error, want error) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		err := operation()
		if errors.Is(err, want) {
			return
		}
		if !errors.Is(err, errPackageStagePending) {
			t.Fatalf("coordinator result = %v, want %v", err, want)
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("coordinator did not return %v before timeout", want)
}

func waitForCoordinatorStart(t *testing.T, operation func() error, started <-chan struct{}) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if err := operation(); !errors.Is(err, errPackageStagePending) {
			t.Fatalf("coordinator start result = %v, want staging pending", err)
		}
		select {
		case <-started:
			return
		default:
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("replacement Stage did not start after canceled worker converged")
}
