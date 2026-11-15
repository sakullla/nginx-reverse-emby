//go:build !integration

package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/core"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/hotrestart"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

type retirementUpdater struct{ activate func(context.Context) error }

func (*retirementUpdater) Stage(context.Context, model.VersionPackage) (string, error) {
	return "/staged/nre-agent", nil
}
func (u *retirementUpdater) Activate(ctx context.Context, _, _ string) error { return u.activate(ctx) }

func TestHotRestartRetirementDoesNotWaitForItsOwnPackageActivation(t *testing.T) {
	store, err := core.NewFilesystem(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	desired := Snapshot{Revision: 18}
	if err := store.SaveDesiredSnapshot(desired); err != nil {
		t.Fatal(err)
	}
	digest := mustHotRestartSnapshotDigest(t, desired)
	if err := store.SaveGenerationJournal(model.GenerationJournal{Version: 1, Active: &model.GenerationRecord{
		GenerationID: "generation-18", RuntimeGenerationID: "generation-18", Revision: 18,
		SnapshotDigest: digest, RuntimeSnapshotHash: digest, Phase: model.GenerationPhaseActive,
		Lease: model.RevisionLease{Revision: 18, LeaseID: "lease-18"},
	}}); err != nil {
		t.Fatal(err)
	}
	stages := core.NewPackageStageCoordinator()
	app := &App{store: store, cfg: Config{DataDir: t.TempDir()}, packageStages: stages, runCtx: t.Context()}
	var order []string
	app.hotRestartStart = func(context.Context, hotrestart.Launch) (hotRestartProcess, error) {
		return &recordingHotRestartProcess{order: &order}, nil
	}
	app.hotRestartDrain = func(context.Context, hotrestart.Identity) error { return nil }
	supervised := make(chan struct{})
	app.hotRestartSupervise = func(context.Context, hotRestartProcess, string, hotrestart.Identity) error {
		close(supervised)
		return nil
	}
	updater := &retirementUpdater{activate: func(ctx context.Context) error {
		return app.hotRestartReplacement(ctx, "/staged/nre-agent", nil, nil)
	}}
	pkg := model.VersionPackage{Platform: "linux-amd64", Filename: "nre-agent", URL: "https://example.test/agent", SHA256: strings.Repeat("a", 64), Size: 1024}
	done := make(chan error, 1)
	go func() {
		for {
			err := stages.Handle(t.Context(), updater, pkg, "next")
			if errors.Is(err, core.ErrRestartRequested) {
				done <- err
				return
			}
			select {
			case <-t.Context().Done():
				done <- t.Context().Err()
				return
			case <-time.After(time.Millisecond):
			}
		}
	}()
	select {
	case <-supervised:
	case <-time.After(2 * time.Second):
		t.Fatal("retired parent deadlocked before supervision inside package activation")
	}
	select {
	case err := <-done:
		if !errors.Is(err, core.ErrRestartRequested) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("package activation did not finish")
	}
	if err := app.Close(); err != nil {
		t.Fatal(err)
	}
	if stages.Pending() {
		t.Fatal("completed coordinator still owns an unfinished update")
	}
}
