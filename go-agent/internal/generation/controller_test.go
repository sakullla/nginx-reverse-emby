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

func TestDrainControllerAppliesImmediatelyAndNaturallyDrainsPrevious(t *testing.T) {
	clock := newFakeClock(time.Unix(100, 0))
	controller := NewDrainController(clock)
	first := &recordingResource{}
	second := &recordingResource{}
	if err := controller.Activate(context.Background(), Generation{ID: "g1", Revision: 1, Resource: first}, nil, time.Minute); err != nil {
		t.Fatal(err)
	}
	session := &recordingSession{}
	handle, err := controller.RegisterSession("g1", EntityKey{Module: "http", ID: "rule-1"}, "s1", session)
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Activate(context.Background(), Generation{ID: "g2", Revision: 2, Resource: second}, []EntityChange{{Entity: EntityKey{Module: "http", ID: "rule-1"}, Action: EntityModified}}, time.Minute); err != nil {
		t.Fatal(err)
	}
	snapshot := controller.Snapshot()
	if snapshot.ActiveGenerationID != "g2" || stateOf(t, snapshot, "g1") != model.GenerationDrainStateDraining {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	if first.destroyed != 0 {
		t.Fatal("previous resource destroyed before session finished")
	}
	handle.Finish()
	if first.destroyed != 1 || stateOf(t, controller.Snapshot(), "g1") != model.GenerationDrainStateDrained {
		t.Fatalf("natural drain = %+v/%d", controller.Snapshot(), first.destroyed)
	}
}

func TestDrainControllerRevokesOnlyDeletedOrDisabledEntities(t *testing.T) {
	controller := NewDrainController(newFakeClock(time.Unix(100, 0)))
	first := &recordingResource{}
	_ = controller.Activate(context.Background(), Generation{ID: "g1", Revision: 1, Resource: first}, nil, time.Minute)
	deleted, modified := &recordingSession{}, &recordingSession{}
	deletedHandle, _ := controller.RegisterSession("g1", EntityKey{Module: "l4", ID: "deleted"}, "d", deleted)
	modifiedHandle, _ := controller.RegisterSession("g1", EntityKey{Module: "l4", ID: "modified"}, "m", modified)
	_ = controller.Activate(context.Background(), Generation{ID: "g2", Revision: 2, Resource: &recordingResource{}}, []EntityChange{
		{Entity: EntityKey{Module: "l4", ID: "deleted"}, Action: EntityDeleted},
		{Entity: EntityKey{Module: "l4", ID: "modified"}, Action: EntityModified},
	}, time.Minute)
	if deleted.closed != 1 || modified.closed != 0 {
		t.Fatalf("closed deleted/modified = %d/%d", deleted.closed, modified.closed)
	}
	deletedHandle.Finish()
	if first.destroyed != 0 {
		t.Fatal("unaffected modified session did not retain generation")
	}
	modifiedHandle.Finish()
	if first.destroyed != 1 {
		t.Fatal("previous generation did not drain")
	}
}

func TestDrainControllerUsesDistinctDeleteAndDisableReasons(t *testing.T) {
	controller := NewDrainController(newFakeClock(time.Unix(100, 0)))
	_ = controller.Activate(context.Background(), Generation{ID: "g1", Revision: 1, Resource: &recordingResource{}}, nil, time.Minute)
	deleted := &reasonRecordingSession{}
	disabled := &reasonRecordingSession{}
	_, _ = controller.RegisterSession("g1", EntityKey{Module: "http", ID: "deleted"}, "d", deleted)
	_, _ = controller.RegisterSession("g1", EntityKey{Module: "http", ID: "disabled"}, "x", disabled)

	err := controller.Activate(context.Background(), Generation{ID: "g2", Revision: 2, Resource: &recordingResource{}}, []EntityChange{
		{Entity: EntityKey{Module: "http", ID: "deleted"}, Action: EntityDeleted},
		{Entity: EntityKey{Module: "http", ID: "disabled"}, Action: EntityDisabled},
	}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if deleted.reason != model.GenerationForceReasonEntityDeleted || disabled.reason != model.GenerationForceReasonEntityDisabled {
		t.Fatalf("force reasons = %q/%q", deleted.reason, disabled.reason)
	}
	if _, err := controller.RegisterSession("g1", EntityKey{Module: "http", ID: "deleted"}, "late", &recordingSession{}); err == nil {
		t.Fatal("deleted entity accepted a late session")
	}
}

func TestDrainControllerTimeoutAndThirdGenerationForceOldest(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		clock := newFakeClock(time.Unix(100, 0))
		controller := NewDrainController(clock)
		first := &recordingResource{}
		_ = controller.Activate(context.Background(), Generation{ID: "g1", Revision: 1, Resource: first}, nil, 10*time.Second)
		session := &recordingSession{}
		_, _ = controller.RegisterSession("g1", EntityKey{Module: "relay", ID: "1"}, "s", session)
		_ = controller.Activate(context.Background(), Generation{ID: "g2", Revision: 2, Resource: &recordingResource{}}, nil, 10*time.Second)
		clock.Advance(10 * time.Second)
		status := statusOf(t, controller.Snapshot(), "g1")
		if status.State != model.GenerationDrainStateForced || status.ForceReason != model.GenerationForceReasonTimeout || session.closed != 1 || first.destroyed != 1 {
			t.Fatalf("timeout = %+v/%d/%d", status, session.closed, first.destroyed)
		}
	})
	t.Run("third generation", func(t *testing.T) {
		clock := newFakeClock(time.Unix(100, 0))
		controller := NewDrainController(clock)
		first := &recordingResource{}
		_ = controller.Activate(context.Background(), Generation{ID: "g1", Revision: 1, Resource: first}, nil, time.Hour)
		session := &recordingSession{}
		_, _ = controller.RegisterSession("g1", EntityKey{Module: "wg", ID: "1"}, "s", session)
		_ = controller.Activate(context.Background(), Generation{ID: "g2", Revision: 2, Resource: &recordingResource{}}, nil, time.Hour)
		_, _ = controller.RegisterSession("g2", EntityKey{Module: "wg", ID: "2"}, "s2", &recordingSession{})
		_ = controller.Activate(context.Background(), Generation{ID: "g3", Revision: 3, Resource: &recordingResource{}}, nil, time.Hour)
		status := statusOf(t, controller.Snapshot(), "g1")
		if status.State != model.GenerationDrainStateForced || status.ForceReason != model.GenerationForceReasonGenerationLimit || first.destroyed != 1 {
			t.Fatalf("oldest = %+v/%d", status, first.destroyed)
		}
		if stateOf(t, controller.Snapshot(), "g2") != model.GenerationDrainStateDraining {
			t.Fatalf("g2 not draining: %+v", controller.Snapshot())
		}
	})
}

func TestDrainControllerEnforcesGenerationLimitAfterEntityCloseError(t *testing.T) {
	controller := NewDrainController(newFakeClock(time.Unix(100, 0)))
	first := &recordingResource{}
	_ = controller.Activate(context.Background(), Generation{ID: "g1", Revision: 1, Resource: first}, nil, time.Hour)
	_, _ = controller.RegisterSession("g1", EntityKey{Module: "http", ID: "keep"}, "s1", &recordingSession{})
	_ = controller.Activate(context.Background(), Generation{ID: "g2", Revision: 2, Resource: &recordingResource{}}, nil, time.Hour)
	_, _ = controller.RegisterSession("g2", EntityKey{Module: "http", ID: "deleted"}, "s2", &errorRecordingSession{err: errors.New("close failed")})
	_, _ = controller.RegisterSession("g2", EntityKey{Module: "http", ID: "keep"}, "s3", &recordingSession{})

	err := controller.Activate(context.Background(), Generation{ID: "g3", Revision: 3, Resource: &recordingResource{}}, []EntityChange{{
		Entity: EntityKey{Module: "http", ID: "deleted"},
		Action: EntityDeleted,
	}}, time.Hour)
	if err == nil {
		t.Fatal("Activate did not report the entity close error")
	}
	status := statusOf(t, controller.Snapshot(), "g1")
	if status.State != model.GenerationDrainStateForced || status.ForceReason != model.GenerationForceReasonGenerationLimit || first.destroyed != 1 {
		t.Fatalf("oldest generation did not converge after close error: %+v destroyed=%d", status, first.destroyed)
	}
}

func TestSessionFinishIsIdempotentUnderConcurrency(t *testing.T) {
	controller := NewDrainController(newFakeClock(time.Unix(100, 0)))
	_ = controller.Activate(context.Background(), Generation{ID: "g1", Revision: 1, Resource: &recordingResource{}}, nil, time.Minute)
	handle, err := controller.RegisterSession("g1", EntityKey{Module: "http", ID: "1"}, "s", &recordingSession{})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); handle.Finish() }()
	}
	wg.Wait()
	if got := controller.Registry().GenerationCount("g1"); got != 0 {
		t.Fatalf("GenerationCount = %d", got)
	}
}

func TestSessionNaturalFinishRacingForceClosesAtMostOnce(t *testing.T) {
	for i := 0; i < 500; i++ {
		controller := NewDrainController(newFakeClock(time.Unix(100, 0)))
		_ = controller.Activate(context.Background(), Generation{ID: "g1", Revision: 1, Resource: &recordingResource{}}, nil, time.Minute)
		session := &atomicRecordingSession{}
		handle, _ := controller.RegisterSession("g1", EntityKey{Module: "l4", ID: "1"}, "s", session)
		_ = controller.Activate(context.Background(), Generation{ID: "g2", Revision: 2, Resource: &recordingResource{}}, nil, time.Hour)

		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			handle.Finish()
		}()
		go func() {
			defer wg.Done()
			<-start
			_ = controller.force(context.Background(), "g1", model.GenerationForceReasonTimeout)
		}()
		close(start)
		wg.Wait()
		if got := session.closed.Load(); got > 1 {
			t.Fatalf("iteration %d ForceClose calls = %d", i, got)
		}
		if got := controller.Registry().GenerationCount("g1"); got != 0 {
			t.Fatalf("iteration %d GenerationCount = %d", i, got)
		}
	}
}

func TestRegisterRacingDrainCompletionNeverUsesDestroyedResource(t *testing.T) {
	entity := EntityKey{Module: "relay", ID: "1"}
	for i := 0; i < 500; i++ {
		controller := NewDrainController(newFakeClock(time.Unix(100, 0)))
		resource := &atomicRecordingResource{}
		_ = controller.Activate(context.Background(), Generation{ID: "g1", Revision: 1, Resource: resource}, nil, time.Minute)
		first, _ := controller.RegisterSession("g1", entity, "first", &recordingSession{})
		_ = controller.Activate(context.Background(), Generation{ID: "g2", Revision: 2, Resource: &recordingResource{}}, nil, time.Hour)

		start := make(chan struct{})
		var late *SessionHandle
		var registerErr error
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			first.Finish()
		}()
		go func() {
			defer wg.Done()
			<-start
			late, registerErr = controller.RegisterSession("g1", entity, "late", &recordingSession{})
		}()
		close(start)
		wg.Wait()

		if registerErr == nil {
			if resource.destroyed.Load() != 0 {
				t.Fatalf("iteration %d accepted session after resource destruction", i)
			}
			late.Finish()
		} else if resource.destroyed.Load() != 1 {
			t.Fatalf("iteration %d rejected late session without completing drain", i)
		}
	}
}

func TestDrainControllerRejectsTerminalAndStaleGenerationRegistration(t *testing.T) {
	clock := newFakeClock(time.Unix(100, 0))
	controller := NewDrainController(clock)
	_ = controller.Activate(context.Background(), Generation{ID: "g1", Revision: 1, Resource: &recordingResource{}}, nil, time.Minute)
	handle, _ := controller.RegisterSession("g1", EntityKey{Module: "http", ID: "1"}, "s", &recordingSession{})
	_ = controller.Activate(context.Background(), Generation{ID: "g2", Revision: 2, Resource: &recordingResource{}}, nil, time.Hour)
	handle.Finish()
	if _, err := controller.RegisterSession("g1", EntityKey{Module: "http", ID: "1"}, "late", &recordingSession{}); err == nil {
		t.Fatal("drained generation accepted a session")
	}
	if err := controller.Activate(context.Background(), Generation{ID: "stale", Revision: 2, Resource: &recordingResource{}}, nil, time.Minute); err == nil {
		t.Fatal("controller accepted a non-increasing revision")
	}

	forced, _ := controller.RegisterSession("g2", EntityKey{Module: "http", ID: "2"}, "s", &recordingSession{})
	_ = controller.Activate(context.Background(), Generation{ID: "g3", Revision: 3, Resource: &recordingResource{}}, nil, time.Second)
	clock.Advance(time.Second)
	forced.Finish()
	if _, err := controller.RegisterSession("g2", EntityKey{Module: "http", ID: "2"}, "late", &recordingSession{}); err == nil {
		t.Fatal("forced generation accepted a session")
	}
}

func TestStoppedAndStaleTimersDoNotChangeTerminalStatus(t *testing.T) {
	clock := newFakeClock(time.Unix(100, 0))
	controller := NewDrainController(clock)
	_ = controller.Activate(context.Background(), Generation{ID: "g1", Revision: 1, Resource: &recordingResource{}}, nil, time.Second)
	handle, _ := controller.RegisterSession("g1", EntityKey{Module: "http", ID: "1"}, "s", &recordingSession{})
	_ = controller.Activate(context.Background(), Generation{ID: "g2", Revision: 2, Resource: &recordingResource{}}, nil, time.Second)
	handle.Finish()
	completed := statusOf(t, controller.Snapshot(), "g1").CompletedAt
	clock.Advance(time.Second)
	status := statusOf(t, controller.Snapshot(), "g1")
	if status.State != model.GenerationDrainStateDrained || status.ForceReason != "" || !status.CompletedAt.Equal(completed) {
		t.Fatalf("stale timer changed drained status: %+v", status)
	}
}

func TestDrainSnapshotAndRevisionReportAreMonotonicAndAuditable(t *testing.T) {
	clock := newFakeClock(time.Unix(100, 0))
	controller := NewDrainController(clock)
	_ = controller.Activate(context.Background(), Generation{ID: "g1", Revision: 1, Resource: &recordingResource{}}, nil, time.Minute)
	_, _ = controller.RegisterSession("g1", EntityKey{Module: "wg", ID: "1"}, "s", &recordingSession{})
	_ = controller.Activate(context.Background(), Generation{ID: "g2", Revision: 2, Resource: &recordingResource{}}, nil, time.Second)
	before := statusOf(t, controller.Snapshot(), "g1")
	clock.Advance(time.Second)
	after := statusOf(t, controller.Snapshot(), "g1")
	if before.State != model.GenerationDrainStateDraining || after.State != model.GenerationDrainStateForced || after.ForcedSessionCount != 1 {
		t.Fatalf("status transition = %+v -> %+v", before, after)
	}
	lease := model.RevisionLease{AgentID: "agent-1", Revision: 1, RetryCycle: 2, Attempt: 3, LeaseID: "lease-1"}
	report := after.RevisionReport(lease)
	if report.AgentID != lease.AgentID || report.Revision != 1 || report.RetryCycle != 2 || report.Attempt != 3 || report.LeaseID != lease.LeaseID || report.GenerationID != "g1" || report.Status != model.GenerationDrainStateForced || !report.Forced || report.ForceReason != model.GenerationForceReasonTimeout {
		t.Fatalf("RevisionReport = %+v", report)
	}
}

func stateOf(t *testing.T, snapshot model.GenerationDrainSnapshot, id string) string {
	return statusOf(t, snapshot, id).State
}
func statusOf(t *testing.T, snapshot model.GenerationDrainSnapshot, id string) model.GenerationDrainStatus {
	t.Helper()
	for _, s := range snapshot.Generations {
		if s.GenerationID == id {
			return s
		}
	}
	t.Fatalf("missing %s", id)
	return model.GenerationDrainStatus{}
}

type recordingResource struct{ destroyed int }

func (r *recordingResource) Destroy(context.Context) error { r.destroyed++; return nil }

type recordingSession struct{ closed int }

func (s *recordingSession) ForceClose(context.Context, string) error { s.closed++; return nil }

type reasonRecordingSession struct {
	reason string
}

func (s *reasonRecordingSession) ForceClose(_ context.Context, reason string) error {
	s.reason = reason
	return nil
}

type atomicRecordingSession struct{ closed atomic.Int32 }

func (s *atomicRecordingSession) ForceClose(context.Context, string) error {
	s.closed.Add(1)
	return nil
}

type errorRecordingSession struct{ err error }

func (s *errorRecordingSession) ForceClose(context.Context, string) error { return s.err }

type atomicRecordingResource struct{ destroyed atomic.Int32 }

func (r *atomicRecordingResource) Destroy(context.Context) error {
	if r.destroyed.Add(1) != 1 {
		return errors.New("resource destroyed more than once")
	}
	return nil
}

type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
}
type fakeTimer struct {
	mu      sync.Mutex
	at      time.Time
	fn      func()
	stopped bool
}

func newFakeClock(now time.Time) *fakeClock { return &fakeClock{now: now} }
func (c *fakeClock) Now() time.Time         { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *fakeClock) AfterFunc(d time.Duration, fn func()) Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{at: c.now.Add(d), fn: fn}
	c.timers = append(c.timers, t)
	return t
}
func (t *fakeTimer) Stop() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopped {
		return false
	}
	t.stopped = true
	return true
}
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	var run []func()
	for _, t := range c.timers {
		t.mu.Lock()
		if !t.stopped && !t.at.After(c.now) {
			t.stopped = true
			run = append(run, t.fn)
		}
		t.mu.Unlock()
	}
	c.mu.Unlock()
	for _, fn := range run {
		fn()
	}
}
