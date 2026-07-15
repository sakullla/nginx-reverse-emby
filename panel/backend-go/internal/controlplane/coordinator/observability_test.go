package coordinator

import (
	"context"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/observability"
)

func TestCoordinatorEmitsQueueOutcomeWithoutChangingEmptyClaim(t *testing.T) {
	coordinator := newTestCoordinator(t, newCoordinatorTestStore(t), time.Unix(100, 0).UTC())
	var events []observability.Event
	ctx := observability.WithObserver(t.Context(), observability.ObserverFunc(func(_ context.Context, event observability.Event) {
		events = append(events, event)
	}))
	result, err := coordinator.Claim(ctx, "agent-empty")
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if result.Lease != nil || result.Busy {
		t.Fatalf("Claim() = %+v, want empty frontier", result)
	}
	if len(events) != 1 || events[0].Name != observability.RevisionQueue || events[0].Outcome != "noop" || events[0].AgentID != "agent-empty" {
		t.Fatalf("events = %+v", events)
	}
}
