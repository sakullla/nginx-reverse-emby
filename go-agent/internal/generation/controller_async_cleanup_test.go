package generation

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

// An accepted HTTP connection can still be waiting for its first request and
// own no registered session. Destroy must finish draining it independently of
// revision acknowledgement and the next heartbeat.
func TestEmptyGenerationCleanupDoesNotBlockPublication(t *testing.T) {
	for _, retire := range []bool{false, true} {
		name := "activate"
		if retire {
			name = "retire"
		}
		t.Run(name, func(t *testing.T) {
			controller := NewDrainController(nil)
			resource := &pendingDispatchResource{started: make(chan struct{}), release: make(chan struct{})}
			var release sync.Once
			defer release.Do(func() { close(resource.release) })
			if err := controller.Activate(t.Context(), Generation{ID: "old", Revision: 1, Resource: resource}, nil, time.Minute); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				if retire {
					done <- controller.RetireActive(t.Context(), "old", time.Minute)
				} else {
					done <- controller.Activate(t.Context(), Generation{ID: "new", Revision: 2, Resource: &retentionResource{}}, nil, time.Minute)
				}
			}()
			select {
			case <-resource.started:
			case <-time.After(time.Second):
				t.Fatal("old generation cleanup did not start")
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("publication waited for old pending HTTP dispatches")
			}
			for _, status := range controller.Snapshot().Generations {
				if status.GenerationID == "old" && !status.CompletedAt.IsZero() {
					t.Fatal("cleanup was reported complete before Destroy returned")
				}
			}

			// Later rollouts and shutdown must not wait indefinitely for the
			// lifecycle lock held by background cleanup either.
			for _, action := range []string{"force", "retry", "close"} {
				ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
				result := make(chan error, 1)
				go func() {
					switch action {
					case "force":
						result <- controller.force(ctx, "old", model.GenerationForceReasonGenerationLimit)
					case "retry":
						result <- controller.RetryCleanup(ctx, "old")
					case "close":
						result <- controller.Close(ctx)
					}
				}()
				select {
				case err := <-result:
					if !errors.Is(err, context.DeadlineExceeded) {
						t.Errorf("%s error = %v, want deadline exceeded", action, err)
					}
				case <-time.After(time.Second):
					t.Errorf("%s ignored its deadline while cleanup held the lifecycle lock", action)
				}
				cancel()
			}
			release.Do(func() { close(resource.release) })
			waitGenerationCleanup(t, controller, "old", model.GenerationDrainStateDrained)
			if got := resource.calls.Load(); got != 1 {
				t.Fatalf("Destroy calls = %d, want 1", got)
			}
		})
	}
}

type pendingDispatchResource struct {
	started chan struct{}
	release chan struct{}
	calls   atomic.Int32
}

func (r *pendingDispatchResource) Destroy(context.Context) error {
	if r.calls.Add(1) == 1 {
		close(r.started)
	}
	<-r.release
	return nil
}

func waitGenerationCleanup(t *testing.T, controller *DrainController, id, state string) {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(time.Millisecond)
	defer poll.Stop()
	for {
		controller.mu.Lock()
		entry := controller.entries[id]
		ready := entry != nil && entry.status.State == state &&
			(entry.released || (state == model.GenerationDrainStateCleanupFailed && entry.cleanupRetry != nil))
		controller.mu.Unlock()
		if ready {
			return
		}
		select {
		case <-poll.C:
		case <-deadline.C:
			t.Fatalf("generation %s did not reach cleanup state %s: %+v", id, state, controller.Snapshot())
		}
	}
}
