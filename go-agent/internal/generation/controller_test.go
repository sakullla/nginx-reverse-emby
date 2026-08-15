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
	status := waitForGenerationCleanup(t, controller, "g1", model.GenerationDrainStateDrained)
	if first.destroyed != 1 {
		t.Fatalf("natural drain = %+v/%d", status, first.destroyed)
	}
}

func TestDrainControllerProgressiveReplacementHasNoFixedForceTimer(t *testing.T) {
	clock := newFakeClock(time.Unix(100, 0))
	controller := NewDrainController(clock)
	first := &recordingResource{}
	if err := controller.Activate(context.Background(), Generation{ID: "g1", Revision: 1, Resource: first}, nil, time.Minute); err != nil {
		t.Fatal(err)
	}
	outer := &progressiveRecordingSession{active: true}
	outerHandle, err := controller.RegisterSession("g1", EntityKey{Module: "http", ID: "rule-1"}, "outer", outer)
	if err != nil {
		t.Fatal(err)
	}
	nested := &progressiveRecordingSession{active: true}
	nestedHandle, err := controller.RegisterSession("g1", EntityKey{Module: "http", ID: "rule-1"}, "nested", nested)
	if err != nil {
		t.Fatal(err)
	}
	ordinary := &recordingSession{}
	ordinaryHandle, err := controller.RegisterSession("g1", EntityKey{Module: "http", ID: "rule-2"}, "hung", ordinary)
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Activate(context.Background(), Generation{ID: "g2", Revision: 2, Resource: &recordingResource{}}, nil, -time.Minute); err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Minute)
	if first.destroyed != 0 || stateOf(t, controller.Snapshot(), "g1") != model.GenerationDrainStateDraining {
		t.Fatal("progressive provider stream was forced by elapsed replacement time")
	}
	if ordinary.closed != 1 {
		t.Fatalf("ordinary hung session closes = %d, want 1", ordinary.closed)
	}
	ordinaryHandle.Finish()
	outerHandle.Finish()
	nestedHandle.Finish()
	deadline := time.Now().Add(time.Second)
	for first.destroyed == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if first.destroyed != 1 {
		t.Fatal("progressive provider generation did not clean up after its final lease")
	}
}

func TestDrainControllerEntityRevokeForcesProgressiveSessions(t *testing.T) {
	controller := NewDrainController(newFakeClock(time.Unix(100, 0)))
	if err := controller.Activate(context.Background(), Generation{ID: "g1", Revision: 1, Resource: &recordingResource{}}, nil, time.Minute); err != nil {
		t.Fatal(err)
	}
	outer := &progressiveRecordingSession{active: true}
	nested := &progressiveRecordingSession{active: true}
	for id, session := range map[string]*progressiveRecordingSession{"outer": outer, "nested": nested} {
		if _, err := controller.RegisterSession("g1", EntityKey{Module: "http", ID: "rule-1"}, id, session); err != nil {
			t.Fatal(err)
		}
	}
	if err := controller.Activate(context.Background(), Generation{ID: "g2", Revision: 2, Resource: &recordingResource{}}, []EntityChange{{Entity: EntityKey{Module: "http", ID: "rule-1"}, Action: EntityDeleted}}, -time.Minute); err != nil {
		t.Fatal(err)
	}
	if outer.closed.Load() != 1 || nested.closed.Load() != 1 {
		t.Fatalf("revoked progressive closes = outer:%d nested:%d, want 1 each", outer.closed.Load(), nested.closed.Load())
	}
}

func TestNaturalDrainCleanupDoesNotBlockLastSessionFinish(t *testing.T) {
	controller := NewDrainController(newFakeClock(time.Unix(100, 0)))
	resource := &blockingDestroyResource{entered: make(chan struct{}), release: make(chan struct{})}
	defer func() {
		select {
		case <-resource.release:
		default:
			close(resource.release)
		}
	}()

	if err := controller.Activate(context.Background(), Generation{ID: "g1", Revision: 1, Resource: resource}, nil, time.Minute); err != nil {
		t.Fatal(err)
	}
	handle, err := controller.RegisterSession("g1", EntityKey{Module: "http", ID: "rule-1"}, "s1", &recordingSession{})
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Activate(context.Background(), Generation{ID: "g2", Revision: 2, Resource: &recordingResource{}}, nil, time.Minute); err != nil {
		t.Fatal(err)
	}

	finishDone := make(chan struct{})
	go func() {
		handle.Finish()
		close(finishDone)
	}()
	select {
	case <-resource.entered:
	case <-time.After(time.Second):
		t.Fatal("natural drain cleanup did not start")
	}
	select {
	case <-finishDone:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("last session Finish blocked on generation cleanup")
	}

	close(resource.release)
	waitForGenerationCleanup(t, controller, "g1", model.GenerationDrainStateDrained)
}

func TestNaturalDrainCleanupTimeoutIsAuditableAndRetryable(t *testing.T) {
	controller := NewDrainController(newFakeClock(time.Unix(100, 0)))
	resource := &blockingDestroyResource{entered: make(chan struct{}), release: make(chan struct{})}
	defer func() {
		select {
		case <-resource.release:
		default:
			close(resource.release)
		}
	}()

	if err := controller.Activate(context.Background(), Generation{ID: "g1", Revision: 1, Resource: resource}, nil, time.Minute); err != nil {
		t.Fatal(err)
	}
	handle, err := controller.RegisterSession("g1", EntityKey{Module: "http", ID: "rule-1"}, "s1", &recordingSession{})
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Activate(context.Background(), Generation{ID: "g2", Revision: 2, Resource: &recordingResource{}}, nil, 20*time.Millisecond); err != nil {
		t.Fatal(err)
	}

	handle.Finish()
	select {
	case <-resource.entered:
	case <-time.After(time.Second):
		t.Fatal("natural drain cleanup did not start")
	}
	failed := waitForGenerationCleanup(t, controller, "g1", model.GenerationDrainStateCleanupFailed)
	if failed.CleanupError != context.DeadlineExceeded.Error() || !failed.CompletedAt.IsZero() {
		t.Fatalf("timed-out cleanup status = %+v", failed)
	}

	retryContext, cancelRetry := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelRetry()
	if err := controller.RetryCleanup(retryContext, "g1"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("RetryCleanup() while destroy is blocked error = %v, want context deadline exceeded", err)
	}

	close(resource.release)
	if err := controller.RetryCleanup(context.Background(), "g1"); err != nil {
		t.Fatalf("RetryCleanup() after destroy completion error = %v", err)
	}
	waitForGenerationCleanup(t, controller, "g1", model.GenerationDrainStateDrained)
	if attempts := resource.attempts.Load(); attempts != 1 {
		t.Fatalf("Destroy() attempts = %d, want 1", attempts)
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
	waitForGenerationCleanup(t, controller, "g1", model.GenerationDrainStateDrained)
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
		_, _ = controller.RegisterSession("g1", EntityKey{Module: "test-module", ID: "1"}, "s", session)
		_ = controller.Activate(context.Background(), Generation{ID: "g2", Revision: 2, Resource: &recordingResource{}}, nil, time.Hour)
		_, _ = controller.RegisterSession("g2", EntityKey{Module: "test-module", ID: "2"}, "s2", &recordingSession{})
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

func TestSelectiveCloseFailureKeepsSessionAndResourceOwned(t *testing.T) {
	controller := NewDrainController(newFakeClock(time.Unix(100, 0)))
	resource := &recordingResource{}
	_ = controller.Activate(context.Background(), Generation{ID: "g1", Revision: 1, Resource: resource}, nil, time.Minute)
	handle, _ := controller.RegisterSession("g1", EntityKey{Module: "http", ID: "deleted"}, "s", &errorRecordingSession{err: errors.New("close failed")})

	err := controller.Activate(context.Background(), Generation{ID: "g2", Revision: 2, Resource: &recordingResource{}}, []EntityChange{{
		Entity: EntityKey{Module: "http", ID: "deleted"},
		Action: EntityDeleted,
	}}, time.Minute)
	if err == nil {
		t.Fatal("Activate did not report selective close failure")
	}
	status := statusOf(t, controller.Snapshot(), "g1")
	if status.State != model.GenerationDrainStateDraining || status.SessionCount != 1 || resource.destroyed != 0 {
		t.Fatalf("failed revoke lost ownership: %+v destroyed=%d", status, resource.destroyed)
	}

	handle.Finish()
	status = waitForGenerationCleanup(t, controller, "g1", model.GenerationDrainStateDrained)
	if status.State != model.GenerationDrainStateDrained || resource.destroyed != 1 {
		t.Fatalf("natural completion did not release retained ownership: %+v destroyed=%d", status, resource.destroyed)
	}
}

func TestNaturalFinishDuringFailedSelectiveCloseReleasesOwnership(t *testing.T) {
	controller := NewDrainController(newFakeClock(time.Unix(100, 0)))
	resource := &recordingResource{}
	_ = controller.Activate(context.Background(), Generation{ID: "g1", Revision: 1, Resource: resource}, nil, time.Minute)
	session := &blockingErrorSession{entered: make(chan struct{}), release: make(chan struct{}), err: errors.New("close failed")}
	handle, _ := controller.RegisterSession("g1", EntityKey{Module: "http", ID: "deleted"}, "s", session)

	done := make(chan error, 1)
	go func() {
		done <- controller.Activate(context.Background(), Generation{ID: "g2", Revision: 2, Resource: &recordingResource{}}, []EntityChange{{
			Entity: EntityKey{Module: "http", ID: "deleted"},
			Action: EntityDeleted,
		}}, time.Minute)
	}()
	<-session.entered
	handle.Finish()
	close(session.release)
	if err := <-done; err == nil {
		t.Fatal("Activate did not report selective close failure")
	}
	status := waitForGenerationCleanup(t, controller, "g1", model.GenerationDrainStateDrained)
	if status.State != model.GenerationDrainStateDrained || status.SessionCount != 0 || resource.destroyed != 1 {
		t.Fatalf("finish during failed close did not release ownership: %+v destroyed=%d", status, resource.destroyed)
	}
}

func TestTerminalForceWaitsForInFlightSelectiveClose(t *testing.T) {
	clock := newFakeClock(time.Unix(100, 0))
	controller := NewDrainController(clock)
	resource := &recordingResource{}
	_ = controller.Activate(context.Background(), Generation{ID: "g1", Revision: 1, Resource: resource}, nil, time.Minute)
	session := &blockingErrorSession{entered: make(chan struct{}), release: make(chan struct{}), err: errors.New("close failed")}
	handle, _ := controller.RegisterSession("g1", EntityKey{Module: "http", ID: "deleted"}, "s", session)

	activateDone := make(chan error, 1)
	go func() {
		activateDone <- controller.Activate(context.Background(), Generation{ID: "g2", Revision: 2, Resource: &recordingResource{}}, []EntityChange{{
			Entity: EntityKey{Module: "http", ID: "deleted"},
			Action: EntityDeleted,
		}}, time.Second)
	}()
	<-session.entered
	forceDone := make(chan struct{})
	go func() {
		clock.Advance(time.Second)
		close(forceDone)
	}()
	waitForTerminalForce(t, handle)
	if resource.destroyed != 0 {
		t.Fatal("terminal force destroyed resources while selective close was in flight")
	}

	close(session.release)
	if err := <-activateDone; err == nil {
		t.Fatal("Activate did not report selective close failure")
	}
	<-forceDone
	status := statusOf(t, controller.Snapshot(), "g1")
	if status.State != model.GenerationDrainStateForced || status.ForceReason != model.GenerationForceReasonTimeout || status.ForcedSessionCount != 1 || status.SessionCount != 0 || resource.destroyed != 1 {
		t.Fatalf("terminal force overlap = %+v destroyed=%d", status, resource.destroyed)
	}
}

func TestThirdGenerationWaitsForInFlightTimeoutForce(t *testing.T) {
	clock := newFakeClock(time.Unix(100, 0))
	controller := NewDrainController(clock)
	first := &recordingResource{}
	session := &blockingErrorSession{entered: make(chan struct{}), release: make(chan struct{})}
	_ = controller.Activate(context.Background(), Generation{ID: "g1", Revision: 1, Resource: first}, nil, time.Minute)
	_, _ = controller.RegisterSession("g1", EntityKey{Module: "http", ID: "first"}, "s1", session)
	_ = controller.Activate(context.Background(), Generation{ID: "g2", Revision: 2, Resource: &recordingResource{}}, nil, time.Second)
	_, _ = controller.RegisterSession("g2", EntityKey{Module: "http", ID: "second"}, "s2", &recordingSession{})

	timeoutDone := make(chan struct{})
	go func() {
		clock.Advance(time.Second)
		close(timeoutDone)
	}()
	<-session.entered
	activateDone := make(chan error, 1)
	go func() {
		activateDone <- controller.Activate(context.Background(), Generation{ID: "g3", Revision: 3, Resource: &recordingResource{}}, nil, time.Minute)
	}()
	waitForActiveGeneration(t, controller, "g3")
	select {
	case err := <-activateDone:
		t.Fatalf("third generation returned before timeout force completed: %v", err)
	default:
	}
	if first.destroyed != 0 {
		t.Fatal("third generation destroyed resources before timeout force completed")
	}

	close(session.release)
	<-timeoutDone
	if err := <-activateDone; err != nil {
		t.Fatal(err)
	}
	status := statusOf(t, controller.Snapshot(), "g1")
	if status.State != model.GenerationDrainStateForced || status.ForceReason != model.GenerationForceReasonTimeout || first.destroyed != 1 {
		t.Fatalf("timeout/limit overlap = %+v destroyed=%d", status, first.destroyed)
	}
}

func TestGenerationLimitAccountsForCleanupFailedResources(t *testing.T) {
	clock := newFakeClock(time.Unix(100, 0))
	controller := NewDrainController(clock)
	first := &retryResource{failures: 100}
	second := &recordingResource{}
	secondSession := &recordingSession{}
	_ = controller.Activate(context.Background(), Generation{ID: "g1", Revision: 1, Resource: first}, nil, time.Minute)
	_, _ = controller.RegisterSession("g1", EntityKey{Module: "http", ID: "first"}, "s1", &recordingSession{})
	_ = controller.Activate(context.Background(), Generation{ID: "g2", Revision: 2, Resource: second}, nil, time.Second)
	_, _ = controller.RegisterSession("g2", EntityKey{Module: "http", ID: "second"}, "s2", secondSession)
	clock.Advance(time.Second)
	if stateOf(t, controller.Snapshot(), "g1") != model.GenerationDrainStateCleanupFailed {
		t.Fatalf("g1 did not retain failed cleanup: %+v", controller.Snapshot())
	}

	err := controller.Activate(context.Background(), Generation{ID: "g3", Revision: 3, Resource: &recordingResource{}}, nil, time.Minute)
	if err == nil {
		t.Fatal("third generation did not report oldest cleanup failure")
	}
	firstStatus := statusOf(t, controller.Snapshot(), "g1")
	secondStatus := statusOf(t, controller.Snapshot(), "g2")
	if firstStatus.State != model.GenerationDrainStateCleanupFailed || first.attempts != 2 {
		t.Fatalf("oldest cleanup retry = %+v attempts=%d", firstStatus, first.attempts)
	}
	if secondStatus.State != model.GenerationDrainStateForced || secondStatus.ForceReason != model.GenerationForceReasonGenerationLimit || second.destroyed != 1 || secondSession.closed != 1 {
		t.Fatalf("fallback generation release = %+v destroyed=%d closed=%d", secondStatus, second.destroyed, secondSession.closed)
	}
	if snapshot := controller.Snapshot(); snapshot.ActiveGenerationID != "g3" || stateOf(t, snapshot, "g3") != model.GenerationDrainStateApplied {
		t.Fatalf("active generation after cleanup failure = %+v", snapshot)
	}
}

func waitForActiveGeneration(t *testing.T, controller *DrainController, id string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if controller.Snapshot().ActiveGenerationID == id {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("generation %s did not become active", id)
}

func waitForGenerationCleanup(t *testing.T, controller *DrainController, id, wantState string) model.GenerationDrainStatus {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		status := statusOf(t, controller.Snapshot(), id)
		if status.State == wantState && (wantState == model.GenerationDrainStateCleanupFailed || !status.CompletedAt.IsZero()) {
			return status
		}
		time.Sleep(time.Millisecond)
	}
	status := statusOf(t, controller.Snapshot(), id)
	t.Fatalf("generation %s cleanup did not reach %s: %+v", id, wantState, status)
	return model.GenerationDrainStatus{}
}

func waitForTerminalForce(t *testing.T, handle *SessionHandle) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		handle.mu.Lock()
		terminal := handle.terminalForce
		handle.mu.Unlock()
		if terminal {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("terminal force did not claim the in-flight session")
}

func TestNaturalDestroyFailureIsAuditableAndRetryable(t *testing.T) {
	controller := NewDrainController(newFakeClock(time.Unix(100, 0)))
	resource := &retryResource{failures: 1}
	_ = controller.Activate(context.Background(), Generation{ID: "g1", Revision: 1, Resource: resource}, nil, time.Minute)
	handle, _ := controller.RegisterSession("g1", EntityKey{Module: "http", ID: "1"}, "s", &recordingSession{})
	_ = controller.Activate(context.Background(), Generation{ID: "g2", Revision: 2, Resource: &recordingResource{}}, nil, time.Minute)
	handle.Finish()

	failed := waitForGenerationCleanup(t, controller, "g1", model.GenerationDrainStateCleanupFailed)
	if failed.State != model.GenerationDrainStateCleanupFailed || failed.CleanupError == "" || !failed.CompletedAt.IsZero() || resource.attempts != 1 {
		t.Fatalf("cleanup failure = %+v attempts=%d", failed, resource.attempts)
	}
	if err := controller.RetryCleanup(context.Background(), "g1"); err != nil {
		t.Fatal(err)
	}
	completed := statusOf(t, controller.Snapshot(), "g1")
	if completed.State != model.GenerationDrainStateDrained || completed.CleanupError != "" || completed.CompletedAt.IsZero() || resource.attempts != 2 {
		t.Fatalf("cleanup retry = %+v attempts=%d", completed, resource.attempts)
	}
}

func TestConcurrentCleanupRetriesCommitLatestSuccessfulAttempt(t *testing.T) {
	controller := NewDrainController(newFakeClock(time.Unix(100, 0)))
	resource := &retryResource{failures: 2}
	_ = controller.Activate(context.Background(), Generation{ID: "g1", Revision: 1, Resource: resource}, nil, time.Minute)
	handle, _ := controller.RegisterSession("g1", EntityKey{Module: "http", ID: "1"}, "s", &recordingSession{})
	_ = controller.Activate(context.Background(), Generation{ID: "g2", Revision: 2, Resource: &recordingResource{}}, nil, time.Minute)
	handle.Finish()
	waitForGenerationCleanup(t, controller, "g1", model.GenerationDrainStateCleanupFailed)

	start := make(chan struct{})
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			results <- controller.RetryCleanup(context.Background(), "g1")
		}()
	}
	close(start)
	var failures int
	for i := 0; i < 2; i++ {
		if <-results != nil {
			failures++
		}
	}
	status := statusOf(t, controller.Snapshot(), "g1")
	if failures != 1 || status.State != model.GenerationDrainStateDrained || status.CleanupError != "" || status.CompletedAt.IsZero() || resource.attempts != 3 {
		t.Fatalf("concurrent cleanup retries: failures=%d status=%+v attempts=%d", failures, status, resource.attempts)
	}
}

func TestForcedDestroyFailureRemainsForcedAndAuditableAfterRetry(t *testing.T) {
	clock := newFakeClock(time.Unix(100, 0))
	controller := NewDrainController(clock)
	resource := &retryResource{failures: 1}
	_ = controller.Activate(context.Background(), Generation{ID: "g1", Revision: 1, Resource: resource}, nil, time.Minute)
	_, _ = controller.RegisterSession("g1", EntityKey{Module: "relay", ID: "1"}, "s", &recordingSession{})
	_ = controller.Activate(context.Background(), Generation{ID: "g2", Revision: 2, Resource: &recordingResource{}}, nil, time.Second)
	clock.Advance(time.Second)

	failed := statusOf(t, controller.Snapshot(), "g1")
	report := failed.RevisionReport(model.RevisionLease{AgentID: "agent", Revision: 1})
	if failed.State != model.GenerationDrainStateCleanupFailed || failed.ForceReason != model.GenerationForceReasonTimeout || failed.ForcedSessionCount != 1 || !failed.CompletedAt.IsZero() {
		t.Fatalf("forced cleanup failure = %+v", failed)
	}
	if !report.Forced || report.ErrorCode != "generation_cleanup_failed" || report.ErrorMessage == "" {
		t.Fatalf("forced cleanup report = %+v", report)
	}
	if err := controller.RetryCleanup(context.Background(), "g1"); err != nil {
		t.Fatal(err)
	}
	completed := statusOf(t, controller.Snapshot(), "g1")
	if completed.State != model.GenerationDrainStateForced || completed.ForceReason != model.GenerationForceReasonTimeout || completed.CompletedAt.IsZero() {
		t.Fatalf("forced cleanup retry = %+v", completed)
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
	for i := 0; i < 100; i++ {
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
		}
		waitForGenerationCleanup(t, controller, "g1", model.GenerationDrainStateDrained)
		if resource.destroyed.Load() != 1 {
			t.Fatalf("iteration %d drain cleanup count = %d", i, resource.destroyed.Load())
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
	completed := waitForGenerationCleanup(t, controller, "g1", model.GenerationDrainStateDrained).CompletedAt
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
	_, _ = controller.RegisterSession("g1", EntityKey{Module: "test-module", ID: "1"}, "s", &recordingSession{})
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

type blockingDestroyResource struct {
	entered  chan struct{}
	release  chan struct{}
	attempts atomic.Int32
}

func (r *blockingDestroyResource) Destroy(context.Context) error {
	r.attempts.Add(1)
	close(r.entered)
	<-r.release
	return nil
}

type recordingSession struct{ closed int }

func (s *recordingSession) ForceClose(context.Context, string) error { s.closed++; return nil }

type progressiveRecordingSession struct {
	active bool
	closed atomic.Int32
}

func (s *progressiveRecordingSession) ProgressiveDrainActive() bool { return s.active }
func (s *progressiveRecordingSession) ForceClose(context.Context, string) error {
	s.closed.Add(1)
	return nil
}

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

type blockingErrorSession struct {
	entered chan struct{}
	release chan struct{}
	err     error
}

func (s *blockingErrorSession) ForceClose(context.Context, string) error {
	close(s.entered)
	<-s.release
	return s.err
}

type retryResource struct {
	failures int
	attempts int
}

func (r *retryResource) Destroy(context.Context) error {
	r.attempts++
	if r.attempts <= r.failures {
		return errors.New("destroy failed")
	}
	return nil
}

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
