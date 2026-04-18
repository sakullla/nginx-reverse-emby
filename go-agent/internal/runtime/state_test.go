package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func TestRollbackReappliesTargetSnapshotAfterActivationFailure(t *testing.T) {
	ctx := context.Background()

	type transition struct {
		previousRevision int64
		nextRevision     int64
	}
	var transitions []transition
	activator := func(_ context.Context, previous, next model.Snapshot) error {
		transitions = append(transitions, transition{
			previousRevision: previous.Revision,
			nextRevision:     next.Revision,
		})
		if next.Revision == 2 {
			return errors.New("activation failed")
		}
		return nil
	}

	r := newRuntimeWithActivator(activator)
	stable := model.Snapshot{
		DesiredVersion: "stable",
		Revision:       1,
		Rules: []model.HTTPRule{{
			FrontendURL: "http://stable.example.test:18080",
			BackendURL:  "http://127.0.0.1:8096",
			Revision:    1,
		}},
	}
	failed := model.Snapshot{
		DesiredVersion: "next",
		Revision:       2,
		Rules: []model.HTTPRule{{
			FrontendURL: "http://next.example.test:18080",
			BackendURL:  "http://127.0.0.1:8096",
			Revision:    2,
		}},
	}

	if err := r.Apply(ctx, model.Snapshot{}, stable); err != nil {
		t.Fatalf("priming apply failed: %v", err)
	}
	if err := r.Apply(ctx, stable, failed); err == nil {
		t.Fatal("expected activation failure")
	}
	if err := r.Rollback(ctx, failed, stable); err != nil {
		t.Fatalf("rollback failed: %v", err)
	}

	if len(transitions) != 3 {
		t.Fatalf("expected 3 activation attempts (startup, failed apply, rollback), got %d", len(transitions))
	}
	if transitions[2].previousRevision != failed.Revision || transitions[2].nextRevision != stable.Revision {
		t.Fatalf("unexpected rollback transition: %+v", transitions[2])
	}
	if got := r.ActiveSnapshot(); !snapshotEqual(got, stable) {
		t.Fatalf("expected active snapshot restored to stable, got %+v", got)
	}

	state := r.State()
	if state.Status != "active" {
		t.Fatalf("expected runtime status active after rollback, got %q", state.Status)
	}
	if state.CurrentRevision != stable.Revision {
		t.Fatalf("expected current revision %d after rollback, got %d", stable.Revision, state.CurrentRevision)
	}
	if state.Metadata["current_revision"] != "1" {
		t.Fatalf("expected metadata current_revision 1 after rollback, got %q", state.Metadata["current_revision"])
	}
}
