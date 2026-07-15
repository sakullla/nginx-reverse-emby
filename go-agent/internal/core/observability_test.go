package core

import (
	"context"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/observability"
)

func TestGenerationManagerFailureEmitsCutoverOutcomeWithoutMaskingError(t *testing.T) {
	var events []observability.Event
	ctx := observability.WithCorrelation(t.Context(), observability.Correlation{AgentID: "agent-7", Attempt: 3})
	ctx = observability.WithObserver(ctx, observability.ObserverFunc(func(_ context.Context, event observability.Event) {
		events = append(events, event)
	}))
	_, err := NewGenerationManager(nil).Apply(ctx, model.Snapshot{}, model.Snapshot{Revision: 7})
	if err == nil {
		t.Fatal("Apply() unexpectedly succeeded")
	}
	if len(events) != 1 || events[0].Name != observability.GenerationCutover || events[0].Outcome != "failed" || events[0].Revision != 7 || events[0].AgentID != "agent-7" || events[0].Attempt != 3 {
		t.Fatalf("events = %+v", events)
	}
}
