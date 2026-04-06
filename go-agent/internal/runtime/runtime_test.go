package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func TestApplyFailureKeepsPreviousSnapshot(t *testing.T) {
	r := New()
	ctx := context.Background()
	initial := model.Snapshot{DesiredVersion: "v1", Revision: 1}
	if err := r.Apply(ctx, model.Snapshot{}, initial); err != nil {
		t.Fatalf("failed to prime runtime: %v", err)
	}

	r.SetFailNextApply(errors.New("activation failed"))
	next := model.Snapshot{DesiredVersion: "v2", Revision: 2}

	if err := r.Apply(ctx, initial, next); err == nil {
		t.Fatalf("expected apply to fail")
	}

	got := r.ActiveSnapshot()
	if got.DesiredVersion != initial.DesiredVersion || got.Revision != initial.Revision {
		t.Fatalf("active snapshot mutated on failure: got %+v want %+v", got, initial)
	}

	state := r.State()
	if state.CurrentRevision != initial.Revision {
		t.Fatalf("current revision advanced on failure: got %d want %d", state.CurrentRevision, initial.Revision)
	}

	if state.Status != "error" {
		t.Fatalf("runtime state not error after failure: got %q", state.Status)
	}
}

func TestApplySuccessSwapsSnapshot(t *testing.T) {
	r := New()
	ctx := context.Background()
	first := model.Snapshot{DesiredVersion: "stable", Revision: 1}
	if err := r.Apply(ctx, model.Snapshot{}, first); err != nil {
		t.Fatalf("failed to prime runtime: %v", err)
	}

	next := model.Snapshot{DesiredVersion: "stable-next", Revision: 2}
	if err := r.Apply(ctx, first, next); err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	if got := r.ActiveSnapshot(); got.DesiredVersion != next.DesiredVersion || got.Revision != next.Revision {
		t.Fatalf("active snapshot not updated on success: got %+v want %+v", got, next)
	}

	state := r.State()
	if state.CurrentRevision != next.Revision {
		t.Fatalf("current revision not advanced: got %d want %d", state.CurrentRevision, next.Revision)
	}

	if state.Status != "active" {
		t.Fatalf("runtime state not active after success: got %q", state.Status)
	}
}
