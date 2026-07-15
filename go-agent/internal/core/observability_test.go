package core

import (
	"context"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/observability"
)

func TestGenerationManagerFailureEmitsCutoverOutcomeWithoutMaskingError(t *testing.T) {
	var events []observability.Event
	ctx := observability.WithObserver(t.Context(), observability.ObserverFunc(func(_ context.Context, event observability.Event) {
		events = append(events, event)
	}))
	_, err := NewGenerationManager(nil).Apply(ctx, model.Snapshot{}, model.Snapshot{Revision: 7})
	if err == nil {
		t.Fatal("Apply() unexpectedly succeeded")
	}
	if len(events) != 1 || events[0].Name != observability.GenerationCutover || events[0].Outcome != "failed" || events[0].Revision != 7 {
		t.Fatalf("events = %+v", events)
	}
}
