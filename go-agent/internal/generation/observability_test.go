package generation

import (
	"context"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/observability"
)

func TestForcedDrainEmitsBoundedOutcome(t *testing.T) {
	clock := newFakeClock(time.Unix(100, 0))
	controller := NewDrainController(clock)
	var events []observability.Event
	ctx := observability.WithCorrelation(t.Context(), observability.Correlation{AgentID: "agent-timeout", Attempt: 5})
	ctx = observability.WithObserver(ctx, observability.ObserverFunc(func(_ context.Context, event observability.Event) {
		events = append(events, event)
	}))
	if err := controller.Activate(ctx, Generation{ID: "generation-1", Revision: 1, Resource: &recordingResource{}}, nil, time.Minute); err != nil {
		t.Fatalf("Activate(first) error = %v", err)
	}
	handle, err := controller.RegisterSession("generation-1", EntityKey{Module: "http", ID: "rule-1"}, "session-1", &observabilitySession{})
	if err != nil {
		t.Fatalf("RegisterSession() error = %v", err)
	}
	if err := controller.Activate(ctx, Generation{ID: "generation-2", Revision: 2, Resource: &recordingResource{}}, nil, time.Minute); err != nil {
		t.Fatalf("Activate(second) error = %v", err)
	}
	clock.Advance(time.Minute)
	found := false
	for _, event := range events {
		if event.Name == observability.GenerationDrain && event.Outcome == "forced" && event.GenerationID == "generation-1" && event.Revision == 1 && event.AgentID == "agent-timeout" && event.Attempt == 5 {
			found = true
		}
	}
	if !found {
		t.Fatalf("events = %+v", events)
	}
	handle.Finish()
}

func TestNaturalDrainEmitsOnlyAtTerminalBoundaryWithCorrelationAndDuration(t *testing.T) {
	clock := newFakeClock(time.Unix(200, 0))
	controller := NewDrainController(clock)
	events := make(chan observability.Event, 2)
	ctx := observability.WithCorrelation(t.Context(), observability.Correlation{AgentID: "agent-1", Attempt: 4})
	ctx = observability.WithObserver(ctx, observability.ObserverFunc(func(_ context.Context, event observability.Event) { events <- event }))
	if err := controller.Activate(ctx, Generation{ID: "generation-1", Revision: 1, Resource: &recordingResource{}}, nil, time.Minute); err != nil {
		t.Fatal(err)
	}
	handle, err := controller.RegisterSession("generation-1", EntityKey{Module: "http", ID: "rule-1"}, "session-1", &observabilitySession{})
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Activate(ctx, Generation{ID: "generation-2", Revision: 2, Resource: &recordingResource{}}, nil, time.Minute); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-events:
		t.Fatalf("premature event = %+v", event)
	default:
	}
	clock.Advance(5 * time.Second)
	handle.Finish()
	select {
	case event := <-events:
		if event.Outcome != "drained" || event.GenerationID != "generation-1" || event.Duration != 5*time.Second || event.AgentID != "agent-1" || event.Attempt != 4 {
			t.Fatalf("event = %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("natural drain event was not emitted")
	}
	select {
	case event := <-events:
		t.Fatalf("duplicate natural drain event = %+v", event)
	case <-time.After(20 * time.Millisecond):
	}
}

type observabilitySession struct{}

func (*observabilitySession) ForceClose(context.Context, string) error { return nil }
