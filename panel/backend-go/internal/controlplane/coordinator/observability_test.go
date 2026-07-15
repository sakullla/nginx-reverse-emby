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

func TestCoordinatorStartUsesAuthoritativeOperationCorrelation(t *testing.T) {
	now := time.Date(2026, 7, 12, 11, 0, 0, 0, time.UTC)
	store := newCoordinatorTestStore(t)
	seedRevisions(t, store, now, "edge-observed", 2, 1, map[int64]string{1: "applied", 2: "pending"})
	coordinator := newTestCoordinator(t, store, now)
	lease := mustClaim(t, coordinator, "edge-observed")
	var events []observability.Event
	ctx := observability.WithObserver(t.Context(), observability.ObserverFunc(func(_ context.Context, event observability.Event) { events = append(events, event) }))
	result, err := coordinator.Start(ctx, StartRequest{Lease: lease, GenerationID: "generation-2"})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if len(events) != 1 || events[0].OperationID == "" || events[0].OperationID != result.Revision.OperationID || events[0].AgentID != "edge-observed" || events[0].Attempt != lease.Attempt {
		t.Fatalf("events = %+v result = %+v", events, result)
	}
}
