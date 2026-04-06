package runtime

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func TestApplyFailureKeepsPreviousSnapshot(t *testing.T) {
	ctx := context.Background()

	failingActivator := func(previous, next model.Snapshot) error {
		if next.Revision == 2 {
			return errors.New("activation failed")
		}
		return nil
	}

	r := newRuntimeWithActivator(failingActivator)
	initial := model.Snapshot{DesiredVersion: "v1", Revision: 1}
	if err := r.Apply(ctx, model.Snapshot{}, initial); err != nil {
		t.Fatalf("priming apply failed: %v", err)
	}

	next := model.Snapshot{DesiredVersion: "v2", Revision: 2}
	if err := r.Apply(ctx, initial, next); err == nil {
		t.Fatalf("expected apply to fail")
	}

	got := r.ActiveSnapshot()
	if got != initial {
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
		t.Fatalf("priming apply failed: %v", err)
	}

	next := model.Snapshot{DesiredVersion: "stable-next", Revision: 2}
	if err := r.Apply(ctx, first, next); err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	if got := r.ActiveSnapshot(); got != next {
		t.Fatalf("active snapshot not updated on success: got %+v want %+v", got, next)
	}

	state := r.State()
	if state.CurrentRevision != next.Revision {
		t.Fatalf("current revision not advanced: got %d want %d", state.CurrentRevision, next.Revision)
	}

	if state.Status != "active" {
		t.Fatalf("runtime state not active after success: got %q", state.Status)
	}

	if value, ok := state.Metadata["current_revision"]; !ok || value != strconv.FormatInt(next.Revision, 10) {
		t.Fatalf("expected metadata current_revision to match revision, got %q", value)
	}
}

func TestApplyPreviousMismatchReportsError(t *testing.T) {
	r := New()
	ctx := context.Background()
	base := model.Snapshot{DesiredVersion: "base", Revision: 1}
	if err := r.Apply(ctx, model.Snapshot{}, base); err != nil {
		t.Fatalf("priming apply failed: %v", err)
	}

	prev := model.Snapshot{DesiredVersion: "mismatch", Revision: 999}
	if err := r.Apply(ctx, prev, model.Snapshot{DesiredVersion: "next", Revision: 2}); err == nil {
		t.Fatalf("expected previous mismatch to return error")
	}

	state := r.State()
	if value, ok := state.Metadata["current_revision"]; !ok || value != strconv.FormatInt(base.Revision, 10) {
		t.Fatalf("current revision metadata changed after mismatch: %v", state.Metadata["current_revision"])
	}

	if state.Status != "error" {
		t.Fatalf("runtime state not error after mismatch: got %q", state.Status)
	}
}

func TestStateReturnsMetadataCopy(t *testing.T) {
	r := New()
	ctx := context.Background()
	initial := model.Snapshot{DesiredVersion: "copy", Revision: 1}
	if err := r.Apply(ctx, model.Snapshot{}, initial); err != nil {
		t.Fatalf("priming apply failed: %v", err)
	}

	state := r.State()
	state.Metadata["leak"] = "mutated"

	second := r.State()
	if _, ok := second.Metadata["leak"]; ok {
		t.Fatalf("metadata copy leaked: %v", second.Metadata)
	}
}
