package core

import (
	"context"
	"reflect"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func TestRuntimeApplyInvokesActivatorWithPreviousAndNextSnapshots(t *testing.T) {
	ctx := context.Background()
	previous := model.Snapshot{DesiredVersion: "v1", Revision: 1}
	next := model.Snapshot{DesiredVersion: "v2", Revision: 2}

	var gotPrevious model.Snapshot
	var gotNext model.Snapshot
	var calls int
	r := NewRuntimeWithActivator(func(_ context.Context, previous, next model.Snapshot) error {
		calls++
		gotPrevious = previous
		gotNext = next
		return nil
	})

	if err := r.Apply(ctx, model.Snapshot{}, previous); err != nil {
		t.Fatalf("priming apply failed: %v", err)
	}
	calls = 0

	if err := r.Apply(ctx, previous, next); err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	if calls != 1 {
		t.Fatalf("activator calls = %d, want 1", calls)
	}
	if !reflect.DeepEqual(gotPrevious, previous) {
		t.Fatalf("previous snapshot = %+v, want %+v", gotPrevious, previous)
	}
	if !reflect.DeepEqual(gotNext, next) {
		t.Fatalf("next snapshot = %+v, want %+v", gotNext, next)
	}
}
