//go:build integration

package generation

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/observability"
)

func TestReleasedGenerationsDropResourcesAndStayBounded(t *testing.T) {
	controller := NewDrainController(nil)
	resources := make([]*retentionResource, maxRetainedTerminalGenerations+8)
	for index := range resources {
		resources[index] = &retentionResource{}
		if err := controller.Activate(t.Context(), Generation{
			ID: fmt.Sprintf("generation-%02d", index+1), Revision: int64(index + 1), Resource: resources[index],
		}, nil, time.Minute); err != nil {
			t.Fatalf("Activate(%d) error = %v", index+1, err)
		}
	}

	controller.mu.Lock()
	defer controller.mu.Unlock()
	if len(controller.entries) > maxRetainedTerminalGenerations+1 {
		t.Fatalf("retained generation entries = %d, want <= %d", len(controller.entries), maxRetainedTerminalGenerations+1)
	}
	if len(controller.order) != len(controller.entries) {
		t.Fatalf("generation order/entry sizes = %d/%d", len(controller.order), len(controller.entries))
	}
	for id, entry := range controller.entries {
		if entry.released && entry.generation.Resource != nil {
			t.Fatalf("released generation %s retained its resource", id)
		}
	}
	for index := 0; index < len(resources)-1; index++ {
		if resources[index].destroyed != 1 {
			t.Fatalf("resource %d destroy count = %d, want 1", index+1, resources[index].destroyed)
		}
	}
}

func TestNaturalDrainEmitsCompletionWithActivationCorrelation(t *testing.T) {
	controller := NewDrainController(nil)
	events := make(chan observability.Event, 1)
	ctx := observability.WithObserver(t.Context(), observability.ObserverFunc(func(_ context.Context, event observability.Event) {
		events <- event
	}))
	ctx = observability.WithCorrelation(ctx, observability.Correlation{
		OperationID: "operation-7", AgentID: "edge-1", Revision: 1, GenerationID: "generation-1", Attempt: 3,
	})
	if err := controller.Activate(ctx, Generation{ID: "generation-1", Revision: 1, Resource: &retentionResource{}}, nil, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := controller.Activate(t.Context(), Generation{ID: "generation-2", Revision: 2, Resource: &retentionResource{}}, nil, time.Minute); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-events:
		if event.Name != observability.GenerationDrain || event.Outcome != "drained" ||
			event.OperationID != "operation-7" || event.AgentID != "edge-1" || event.Attempt != 3 {
			t.Fatalf("drain completion event = %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("natural drain completion event was not emitted")
	}
}

func TestCleanupFailureRetriesWithoutAnotherRollout(t *testing.T) {
	clock := newCleanupRetryClock(time.Unix(100, 0))
	controller := NewDrainController(clock)
	resource := &transientCleanupResource{failures: 1}
	if err := controller.Activate(t.Context(), Generation{ID: "generation-1", Revision: 1, Resource: resource}, nil, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := controller.Activate(t.Context(), Generation{ID: "generation-2", Revision: 2, Resource: &retentionResource{}}, nil, time.Minute); err != nil {
		t.Fatal(err)
	}
	if state := cleanupState(t, controller, "generation-1"); state != model.GenerationDrainStateCleanupFailed {
		t.Fatalf("cleanup state = %q", state)
	}

	clock.Advance(cleanupRetryBase)
	if state := cleanupState(t, controller, "generation-1"); state != model.GenerationDrainStateDrained {
		t.Fatalf("retried cleanup state = %q", state)
	}
	if resource.attempts != 2 {
		t.Fatalf("cleanup attempts = %d, want 2", resource.attempts)
	}
}

func TestRetiredActiveGenerationIsForcedAfterDrainTimeout(t *testing.T) {
	clock := newCleanupRetryClock(time.Unix(100, 0))
	controller := NewDrainController(clock)
	resource := &retentionResource{}
	ctx, cancel := context.WithCancel(t.Context())
	if err := controller.Activate(ctx, Generation{
		ID: "generation-1", Revision: 1, Resource: resource,
	}, nil, time.Minute); err != nil {
		t.Fatal(err)
	}
	session := &retentionSession{}
	if _, err := controller.RegisterSession("generation-1", EntityKey{Module: "http", ID: "1"}, "session-1", session); err != nil {
		t.Fatal(err)
	}
	if err := controller.RetireActive(ctx, "generation-1", time.Minute); err != nil {
		t.Fatal(err)
	}
	cancel()
	clock.Advance(time.Minute)

	var status model.GenerationDrainStatus
	for _, candidate := range controller.Snapshot().Generations {
		if candidate.GenerationID == "generation-1" {
			status = candidate
		}
	}
	if status.State != model.GenerationDrainStateForced || status.ForceReason != model.GenerationForceReasonTimeout || status.CompletedAt.IsZero() {
		t.Fatalf("retired generation status = %+v", status)
	}
	if session.forceCalls != 1 || session.reason != model.GenerationForceReasonTimeout || session.contextErr != nil {
		t.Fatalf("forced session calls/reason/context = %d/%q/%v", session.forceCalls, session.reason, session.contextErr)
	}
	if resource.destroyed != 1 {
		t.Fatalf("retired resource destroy calls = %d, want 1", resource.destroyed)
	}
}

type retentionResource struct {
	destroyed int
}

type retentionSession struct {
	forceCalls int
	reason     string
	contextErr error
}

func (s *retentionSession) ForceClose(ctx context.Context, reason string) error {
	s.forceCalls++
	s.reason = reason
	s.contextErr = ctx.Err()
	return nil
}

func (r *retentionResource) Destroy(context.Context) error {
	r.destroyed++
	return nil
}

type transientCleanupResource struct {
	failures int
	attempts int
}

func (r *transientCleanupResource) Destroy(context.Context) error {
	r.attempts++
	if r.failures > 0 {
		r.failures--
		return errors.New("temporary cleanup failure")
	}
	return nil
}

type cleanupRetryClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*cleanupRetryTimer
}

type cleanupRetryTimer struct {
	due     time.Time
	fn      func()
	stopped bool
}

func newCleanupRetryClock(now time.Time) *cleanupRetryClock { return &cleanupRetryClock{now: now} }

func (c *cleanupRetryClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *cleanupRetryClock) AfterFunc(delay time.Duration, fn func()) Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	timer := &cleanupRetryTimer{due: c.now.Add(delay), fn: fn}
	c.timers = append(c.timers, timer)
	return timer
}

func (c *cleanupRetryClock) Advance(delay time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(delay)
	var ready []func()
	for _, timer := range c.timers {
		if !timer.stopped && !timer.due.After(c.now) {
			timer.stopped = true
			ready = append(ready, timer.fn)
		}
	}
	c.mu.Unlock()
	for _, fn := range ready {
		fn()
	}
}

func (t *cleanupRetryTimer) Stop() bool {
	if t.stopped {
		return false
	}
	t.stopped = true
	return true
}

func cleanupState(t *testing.T, controller *DrainController, id string) string {
	t.Helper()
	for _, status := range controller.Snapshot().Generations {
		if status.GenerationID == id {
			return status.State
		}
	}
	t.Fatalf("generation %s not found", id)
	return ""
}
