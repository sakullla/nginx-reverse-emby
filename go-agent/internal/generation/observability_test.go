package generation

import (
	"context"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/observability"
)

func TestForcedDrainEmitsBoundedOutcome(t *testing.T) {
	controller := NewDrainController(newFakeClock(time.Unix(100, 0)))
	var events []observability.Event
	ctx := observability.WithObserver(t.Context(), observability.ObserverFunc(func(_ context.Context, event observability.Event) {
		events = append(events, event)
	}))
	if err := controller.Activate(ctx, Generation{ID: "generation-1", Revision: 1, Resource: &recordingResource{}}, nil, time.Minute); err != nil {
		t.Fatalf("Activate(first) error = %v", err)
	}
	if _, err := controller.RegisterSession("generation-1", EntityKey{Module: "http", ID: "rule-1"}, "session-1", &observabilitySession{}); err != nil {
		t.Fatalf("RegisterSession() error = %v", err)
	}
	if err := controller.Activate(ctx, Generation{ID: "generation-2", Revision: 2, Resource: &recordingResource{}}, nil, time.Minute); err != nil {
		t.Fatalf("Activate(second) error = %v", err)
	}
	if err := controller.force(ctx, "generation-1", model.GenerationForceReasonTimeout); err != nil {
		t.Fatalf("force() error = %v", err)
	}
	found := false
	for _, event := range events {
		if event.Name == observability.GenerationDrain && event.Outcome == "forced" && event.GenerationID == "generation-1" && event.Revision == 1 {
			found = true
		}
	}
	if !found {
		t.Fatalf("events = %+v", events)
	}
}

type observabilitySession struct{}

func (*observabilitySession) ForceClose(context.Context, string) error { return nil }
