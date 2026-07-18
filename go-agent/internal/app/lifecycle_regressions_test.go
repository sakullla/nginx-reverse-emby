package app

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/core"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/hotrestart"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func TestPackageOnlyHotRestartUsesDurableActiveGeneration(t *testing.T) {
	store, err := core.NewFilesystem(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	desired := Snapshot{Revision: 12, DesiredVersion: "1.0.0"}
	digest, err := hotRestartSnapshotDigest(desired)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveDesiredSnapshot(desired); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveGenerationJournal(model.GenerationJournal{Version: 1, Active: &model.GenerationRecord{
		GenerationID: "protocol-12", RuntimeGenerationID: "runtime-12", Revision: 12,
		SnapshotDigest: digest, Phase: model.GenerationPhaseActive, Acknowledged: true,
		Lease: model.RevisionLease{Revision: 12, LeaseID: "lease-12", DrainTimeoutSeconds: 23},
	}}); err != nil {
		t.Fatal(err)
	}

	process := &lifecycleHotRestartProcess{}
	app := &App{cfg: Config{DataDir: t.TempDir()}, store: store, runCtx: t.Context()}
	app.hotRestartStart = func(_ context.Context, launch hotrestart.Launch) (hotRestartProcess, error) {
		if launch.Identity.Revision != 12 || launch.Identity.GenerationID != "runtime-12" || launch.Identity.LeaseID != "lease-12" {
			t.Fatalf("package-only launch identity = %+v", launch.Identity)
		}
		return process, nil
	}
	app.hotRestartDrain = func(context.Context, hotrestart.Identity) error {
		if app.hotRestartDrainTimeout != 23*time.Second {
			t.Fatalf("hot restart drain timeout = %s, want 23s", app.hotRestartDrainTimeout)
		}
		return nil
	}

	err = app.hotRestartReplacement("/updates/new/nre-agent", nil, nil)
	if !errors.Is(err, core.ErrRestartRequested) {
		t.Fatalf("hotRestartReplacement() error = %v, want restart requested", err)
	}
	if process.waitCalls != 0 {
		t.Fatalf("retired parent waited for authoritative child %d time(s)", process.waitCalls)
	}
	identity := hotrestart.Identity{
		Revision: 12, SnapshotDigest: digest, GenerationID: "runtime-12", LeaseID: "lease-12", LaunchEpoch: "epoch-12",
	}
	if err := app.validateHotRestartIdentity(identity, desired); err != nil {
		t.Fatalf("validateHotRestartIdentity(active) error = %v", err)
	}
}

type lifecycleHotRestartProcess struct {
	waitCalls int
}

func (*lifecycleHotRestartProcess) Activate(context.Context) error          { return nil }
func (*lifecycleHotRestartProcess) TransferAuthority(context.Context) error { return nil }
func (p *lifecycleHotRestartProcess) Wait() error                           { p.waitCalls++; return nil }
func (*lifecycleHotRestartProcess) Signal(os.Signal) error                  { return nil }
func (*lifecycleHotRestartProcess) Abort() error                            { return nil }
