//go:build !integration

package core

import (
	"context"
	"errors"
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
	activateCalls []string
	activateErr   error
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
		return "", ctx.Err()
	case <-release:
		return "staged/" + pkg.URL, nil
	}
}

func (u *coordinatorTestUpdater) Activate(_ context.Context, stagedPath, _ string) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.activateCalls = append(u.activateCalls, stagedPath)
	return u.activateErr
}

func (u *coordinatorTestUpdater) calls(target string) int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.stageCalls[target]
}

func (u *coordinatorTestUpdater) activations() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.activateCalls...)
}

func TestPackageStageCoordinatorCancelsChangedTargetAndIsolatesOldAttempt(t *testing.T) {
	updater := newCoordinatorTestUpdater("old", "new")
	coordinator := NewPackageStageCoordinator()
	oldPackage := model.VersionPackage{URL: "old", SHA256: "old-sha"}
	newPackage := model.VersionPackage{URL: "new", SHA256: "new-sha"}

	if err := coordinator.Handle(t.Context(), updater, oldPackage, "2.0.0"); !errors.Is(err, errPackageStagePending) {
		t.Fatalf("Handle(old) error = %v, want staging pending", err)
	}
	waitForCoordinatorSignal(t, updater.started["old"])
	if err := coordinator.Handle(t.Context(), updater, newPackage, "3.0.0"); !errors.Is(err, errPackageStagePending) {
		t.Fatalf("Handle(new) error = %v, want staging pending", err)
	}
	waitForCoordinatorSignal(t, updater.started["new"])
	waitForCoordinatorSignal(t, updater.canceled["old"])
	close(updater.release["new"])

	waitForCoordinatorResult(t, func() error {
		return coordinator.Handle(t.Context(), updater, newPackage, "3.0.0")
	}, ErrRestartRequested)
	if updater.calls("old") != 1 || updater.calls("new") != 1 {
		t.Fatalf("Stage calls old/new = %d/%d, want 1/1", updater.calls("old"), updater.calls("new"))
	}
	if got := updater.activations(); len(got) != 1 || got[0] != "staged/new" {
		t.Fatalf("activations = %v, want only staged/new", got)
	}
}

func TestPackageStageCoordinatorReportsFailureWithoutActivation(t *testing.T) {
	stageErr := errors.New("checksum mismatch")
	updater := newCoordinatorTestUpdater("failed")
	updater.stageErr["failed"] = stageErr
	coordinator := NewPackageStageCoordinator()
	pkg := model.VersionPackage{URL: "failed", SHA256: "bad-sha"}

	if err := coordinator.Handle(t.Context(), updater, pkg, "2.0.0"); !errors.Is(err, errPackageStagePending) {
		t.Fatalf("Handle() error = %v, want staging pending", err)
	}
	waitForCoordinatorSignal(t, updater.started["failed"])
	waitForCoordinatorResult(t, func() error {
		return coordinator.Handle(t.Context(), updater, pkg, "2.0.0")
	}, stageErr)
	if updater.calls("failed") != 1 || len(updater.activations()) != 0 {
		t.Fatalf("failed target Stage/Activate calls = %d/%d, want 1/0", updater.calls("failed"), len(updater.activations()))
	}
}

func TestPackageStageCoordinatorDoesNotReportFailedActivationAsSuccess(t *testing.T) {
	activateErr := errors.New("child activation failed")
	updater := newCoordinatorTestUpdater("activate")
	updater.activateErr = activateErr
	coordinator := NewPackageStageCoordinator()
	pkg := model.VersionPackage{URL: "activate", SHA256: "activate-sha"}

	if err := coordinator.Handle(t.Context(), updater, pkg, "2.0.0"); !errors.Is(err, errPackageStagePending) {
		t.Fatalf("Handle() error = %v, want staging pending", err)
	}
	waitForCoordinatorSignal(t, updater.started["activate"])
	close(updater.release["activate"])
	waitForCoordinatorResult(t, func() error {
		return coordinator.Handle(t.Context(), updater, pkg, "2.0.0")
	}, activateErr)
	if got := updater.activations(); len(got) != 1 || got[0] != "staged/activate" {
		t.Fatalf("failed activations = %v, want one staged/activate attempt", got)
	}
}

func TestPackageStageCoordinatorCloseCancelsStage(t *testing.T) {
	updater := newCoordinatorTestUpdater("shutdown")
	coordinator := NewPackageStageCoordinator()
	pkg := model.VersionPackage{URL: "shutdown", SHA256: "shutdown-sha"}

	if err := coordinator.Handle(t.Context(), updater, pkg, "2.0.0"); !errors.Is(err, errPackageStagePending) {
		t.Fatalf("Handle() error = %v, want staging pending", err)
	}
	waitForCoordinatorSignal(t, updater.started["shutdown"])
	coordinator.Close()
	waitForCoordinatorSignal(t, updater.canceled["shutdown"])
	if err := coordinator.Handle(t.Context(), updater, pkg, "2.0.0"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Handle(after Close) error = %v, want context cancellation", err)
	}
	if len(updater.activations()) != 0 {
		t.Fatalf("shutdown target activations = %v, want none", updater.activations())
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
