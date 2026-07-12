package coordinator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestClaimLatestSupersedesIntermediateAndSerializesAgent(t *testing.T) {
	now := time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)
	store := newCoordinatorTestStore(t)
	seedRevisions(t, store, now, "edge-1", 4, 1, map[int64]string{
		1: storage.AgentRevisionStateApplied,
		2: storage.AgentRevisionStatePending,
		3: storage.AgentRevisionStatePending,
		4: storage.AgentRevisionStatePending,
	})
	coord := newTestCoordinator(t, store, now, 0.5)

	claimed, err := coord.Claim(t.Context(), "edge-1")
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if claimed.Lease == nil || claimed.Busy {
		t.Fatalf("Claim() = %+v, want a new lease", claimed)
	}
	if claimed.Lease.Revision != 4 || claimed.Lease.Attempt != 1 {
		t.Fatalf("lease = %+v, want revision 4 attempt 1", claimed.Lease)
	}
	if claimed.Lease.ApplyTimeoutSeconds != 60 || claimed.Lease.DrainTimeoutSeconds != 600 {
		t.Fatalf("lease timeouts = (%d,%d), want (60,600)", claimed.Lease.ApplyTimeoutSeconds, claimed.Lease.DrainTimeoutSeconds)
	}
	if !claimed.Lease.DeadlineAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("lease deadline = %v, want %v", claimed.Lease.DeadlineAt, now.Add(time.Minute))
	}

	for _, revision := range []int64{2, 3} {
		row := mustRevision(t, store, "edge-1", revision)
		if row.State != storage.AgentRevisionStateSuperseded {
			t.Fatalf("revision %d state = %q, want superseded", revision, row.State)
		}
	}

	second, err := coord.Claim(t.Context(), "edge-1")
	if err != nil {
		t.Fatalf("second Claim() error = %v", err)
	}
	if !second.Busy || second.Lease != nil {
		t.Fatalf("second Claim() = %+v, want busy without another lease", second)
	}
	attempts := mustAttempts(t, store, "edge-1", 4)
	if len(attempts) != 1 || attempts[0].State != storage.AgentRevisionAttemptStateLeased {
		t.Fatalf("attempts = %+v, want one leased row", attempts)
	}
}

func TestPrepareStartIsAttemptBoundaryAndExpiredLeaseIsFenced(t *testing.T) {
	now := time.Date(2026, 7, 12, 11, 0, 0, 0, time.UTC)
	store := newCoordinatorTestStore(t)
	seedRevisions(t, store, now, "edge-1", 2, 1, map[int64]string{
		1: storage.AgentRevisionStateApplied,
		2: storage.AgentRevisionStatePending,
	})
	clock := &fakeClock{now: now}
	coord := newTestCoordinatorWithClock(t, store, clock, 0.5)

	first := mustClaim(t, coord, "edge-1")
	if row := mustRevision(t, store, "edge-1", 2); row.AttemptCount != 0 {
		t.Fatalf("AttemptCount after lease = %d, want 0", row.AttemptCount)
	}

	clock.now = first.DeadlineAt
	result, err := coord.Reconcile(t.Context(), "edge-1")
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if result.AttemptsConsumed != 0 || !result.Ready {
		t.Fatalf("Reconcile() = %+v, want ready without consuming an attempt", result)
	}
	row := mustRevision(t, store, "edge-1", 2)
	if row.AttemptCount != 0 || row.State != storage.AgentRevisionStatePending {
		t.Fatalf("revision after expired unstarted lease = %+v", row)
	}

	second := mustClaim(t, coord, "edge-1")
	if second.LeaseID == first.LeaseID {
		t.Fatalf("reclaimed lease id = %q, want a new fence token", second.LeaseID)
	}
	if _, err := coord.Start(t.Context(), StartRequest{Lease: first, GenerationID: "generation-stale"}); !errors.Is(err, ErrLeaseConflict) {
		t.Fatalf("Start(stale lease) error = %v, want ErrLeaseConflict", err)
	}
	started, err := coord.Start(t.Context(), StartRequest{Lease: second, GenerationID: "generation-2"})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if started.Revision.AttemptCount != 1 || started.Revision.State != storage.AgentRevisionStateApplying {
		t.Fatalf("started revision = %+v", started.Revision)
	}
	if !started.Attempt.DeadlineAt.Equal(clock.now.Add(time.Minute)) {
		t.Fatalf("started deadline = %v, want full timeout from ack", started.Attempt.DeadlineAt)
	}
}

func TestFailurePersistsFullJitterAndStopsAfterFiveActualAttempts(t *testing.T) {
	now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	store := newCoordinatorTestStore(t)
	seedRevisions(t, store, now, "edge-1", 2, 1, map[int64]string{
		1: storage.AgentRevisionStateApplied,
		2: storage.AgentRevisionStatePending,
	})
	clock := &fakeClock{now: now}
	coord := newTestCoordinatorWithClock(t, store, clock, 0.5, 0.5, 0.5, 0.5, 0.5)

	for attempt := 1; attempt <= 5; attempt++ {
		lease := mustClaim(t, coord, "edge-1")
		if lease.Attempt != attempt {
			t.Fatalf("claim attempt = %d, want %d", lease.Attempt, attempt)
		}
		if _, err := coord.Start(t.Context(), StartRequest{Lease: lease, GenerationID: fmt.Sprintf("generation-%d", attempt)}); err != nil {
			t.Fatalf("Start(attempt %d) error = %v", attempt, err)
		}
		failedAt := clock.now
		failure, err := coord.Fail(t.Context(), FailureReport{
			Lease: lease, GenerationID: fmt.Sprintf("generation-%d", attempt),
			ErrorCode: "prepare_failed", ErrorMessage: "injected failure",
		})
		if err != nil {
			t.Fatalf("Fail(attempt %d) error = %v", attempt, err)
		}
		if attempt == 5 {
			if !failure.Exhausted || failure.Revision.State != storage.AgentRevisionStateFailed || failure.Revision.NextAttemptAt != nil {
				t.Fatalf("fifth failure = %+v, want exhausted failed revision", failure)
			}
			break
		}

		wantDelay := (time.Second << (attempt - 1)) / 2
		if failure.Exhausted || failure.RetryDelay != wantDelay {
			t.Fatalf("attempt %d retry = %+v, want delay %v", attempt, failure, wantDelay)
		}
		if failure.Revision.NextAttemptAt == nil || !failure.Revision.NextAttemptAt.Equal(failedAt.Add(wantDelay)) {
			t.Fatalf("attempt %d next_attempt_at = %v, want %v", attempt, failure.Revision.NextAttemptAt, failedAt.Add(wantDelay))
		}
		if early, err := coord.Claim(t.Context(), "edge-1"); err != nil || early.Lease != nil {
			t.Fatalf("early Claim() = %+v, %v, want no work", early, err)
		}
		clock.now = failedAt.Add(wantDelay)
	}

	pointer := mustPointer(t, store, "edge-1")
	if pointer.DesiredRevision != 2 || pointer.AppliedRevision != 1 || pointer.LastKnownGoodRevision != 1 {
		t.Fatalf("pointer after failures = %+v, want desired preserved and LKG unchanged", pointer)
	}
	if automatic, err := coord.Claim(t.Context(), "edge-1"); err != nil || automatic.Lease != nil {
		t.Fatalf("Claim(after fifth failure) = %+v, %v, want no automatic sixth attempt", automatic, err)
	}
	retried, err := coord.Retry(t.Context(), "edge-1", 2)
	if err != nil {
		t.Fatalf("Retry() error = %v", err)
	}
	if retried.RetryCycle != 1 || retried.AttemptCount != 0 {
		t.Fatalf("retried revision = %+v, want retry cycle 1 with zero actual attempts", retried)
	}
	attempts := mustAttempts(t, store, "edge-1", 2)
	if len(attempts) != 5 {
		t.Fatalf("attempt history length = %d, want 5", len(attempts))
	}
	for _, attempt := range attempts {
		if attempt.RetryCycle != 0 || attempt.State != storage.AgentRevisionAttemptStateFailed {
			t.Fatalf("preserved attempt = %+v, want failed cycle 0 history", attempt)
		}
	}
}

func TestManualRetryStartsNewCycleAndRollbackCopiesLKGAsNewRevision(t *testing.T) {
	now := time.Date(2026, 7, 12, 13, 0, 0, 0, time.UTC)
	store := newCoordinatorTestStore(t)
	artifact := snapshotArtifact(t, "edge-1-lkg", storage.Snapshot{
		DesiredVersion:      "v1.2.3",
		Revision:            1,
		Rules:               []storage.HTTPRule{},
		L4Rules:             []storage.L4Rule{},
		RelayListeners:      []storage.RelayListener{},
		WireGuardProfiles:   []storage.WireGuardProfile{},
		EgressProfiles:      []storage.EgressProfile{},
		Certificates:        []storage.ManagedCertificateBundle{},
		CertificatePolicies: []storage.ManagedCertificatePolicy{},
	}, now)
	seedRevisionsWithArtifact(t, store, now, "edge-1", artifact, 2, 1, map[int64]string{
		1: storage.AgentRevisionStateApplied,
		2: storage.AgentRevisionStateFailed,
	})
	coord := newTestCoordinator(t, store, now, 0.5)

	retried, err := coord.Retry(t.Context(), "edge-1", 2)
	if err != nil {
		t.Fatalf("Retry() error = %v", err)
	}
	if retried.RetryCycle != 1 || retried.AttemptCount != 0 || retried.State != storage.AgentRevisionStatePending {
		t.Fatalf("retried revision = %+v", retried)
	}

	rolledBack, err := coord.Rollback(t.Context(), "edge-1")
	if err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}
	if rolledBack.Revision.Revision != 3 || rolledBack.Revision.State != storage.AgentRevisionStatePending {
		t.Fatalf("rollback revision = %+v, want new pending revision 3", rolledBack.Revision)
	}
	pointer := mustPointer(t, store, "edge-1")
	if pointer.DesiredRevision != 3 || pointer.AppliedRevision != 1 || pointer.LastKnownGoodRevision != 1 {
		t.Fatalf("pointer after rollback = %+v", pointer)
	}
	copyArtifact, found, err := store.GetGenerationArtifact(t.Context(), rolledBack.Revision.SnapshotArtifactID)
	if err != nil || !found {
		t.Fatalf("GetGenerationArtifact(rollback) = found %v, error %v", found, err)
	}
	var copied storage.Snapshot
	if err := json.Unmarshal(copyArtifact.Payload, &copied); err != nil {
		t.Fatalf("unmarshal rollback snapshot: %v", err)
	}
	if copied.Revision != 3 || copied.DesiredVersion != "v1.2.3" {
		t.Fatalf("rollback snapshot = %+v, want copied LKG content at revision 3", copied)
	}
	if rolledBack.Revision.SnapshotDigest == artifact.SHA256 {
		t.Fatal("rollback snapshot digest reused the old revision-bearing payload")
	}
}

func TestCoordinatorActionsAreDurablyIdempotentAcrossConcurrentStores(t *testing.T) {
	now := time.Date(2026, 7, 13, 4, 0, 0, 0, time.UTC)
	dbPath := filepath.Join(t.TempDir(), "coordinator-actions.db")
	storeA := openCoordinatorTestStore(t, dbPath)
	seedRevisions(t, storeA, now, "edge-retry", 2, 1, map[int64]string{
		1: storage.AgentRevisionStateApplied,
		2: storage.AgentRevisionStateFailed,
	})
	artifact := snapshotArtifact(t, "edge-rollback-lkg", storage.Snapshot{
		Revision:            1,
		Rules:               []storage.HTTPRule{},
		L4Rules:             []storage.L4Rule{},
		RelayListeners:      []storage.RelayListener{},
		WireGuardProfiles:   []storage.WireGuardProfile{},
		EgressProfiles:      []storage.EgressProfile{},
		Certificates:        []storage.ManagedCertificateBundle{},
		CertificatePolicies: []storage.ManagedCertificatePolicy{},
	}, now)
	seedRevisionsWithArtifact(t, storeA, now, "edge-rollback", artifact, 1, 1, map[int64]string{
		1: storage.AgentRevisionStateApplied,
	})
	storeB := openCoordinatorTestStore(t, dbPath)

	newCoordinator := func(store *storage.GormStore, suffix string) *Coordinator {
		t.Helper()
		coord, err := New(store, Options{
			Clock:  &fakeClock{now: now},
			Random: &sequenceRandom{values: []float64{0.5}},
			NewID:  func(prefix string) (string, error) { return prefix + "-" + suffix, nil },
		})
		if err != nil {
			t.Fatalf("New(%s) error = %v", suffix, err)
		}
		return coord
	}
	coordA := newCoordinator(storeA, "store-a")
	coordB := newCoordinator(storeB, "store-b")

	retryIdempotency := storage.CoordinatorActionIdempotency{
		Scope: "panel", Key: "retry-once", RequestFingerprint: "retry-fingerprint",
		HTTPRequestFingerprint: "retry-http-fingerprint", ExpiresAt: now.Add(time.Hour),
	}
	retryStart := make(chan struct{})
	retryResults := make(chan storage.CoordinatorRetryResult, 2)
	retryErrors := make(chan error, 2)
	var retryGroup sync.WaitGroup
	for _, coord := range []*Coordinator{coordA, coordB} {
		retryGroup.Add(1)
		go func(coord *Coordinator) {
			defer retryGroup.Done()
			<-retryStart
			result, err := coord.RetryIdempotent(context.Background(), "edge-retry", 2, retryIdempotency)
			retryResults <- result
			retryErrors <- err
		}(coord)
	}
	close(retryStart)
	retryGroup.Wait()
	close(retryResults)
	close(retryErrors)
	for err := range retryErrors {
		if err != nil {
			t.Fatalf("concurrent RetryIdempotent() error = %v", err)
		}
	}
	retryReplays := 0
	for result := range retryResults {
		if result.Revision.Revision != 2 || result.Revision.RetryCycle != 1 || result.Revision.State != storage.AgentRevisionStatePending {
			t.Fatalf("concurrent retry result = %+v", result)
		}
		if result.Replayed {
			retryReplays++
		}
	}
	if retryReplays != 1 {
		t.Fatalf("concurrent retry replay count = %d, want 1", retryReplays)
	}
	if row, found, err := storeA.GetCoordinatorRevision(t.Context(), "edge-retry", 2); err != nil || !found || row.RetryCycle != 1 {
		t.Fatalf("retry revision after concurrent calls = %+v found=%v error=%v", row, found, err)
	}
	conflictingRetry := retryIdempotency
	conflictingRetry.RequestFingerprint = "different-retry-fingerprint"
	if _, err := coordA.RetryIdempotent(t.Context(), "edge-retry", 2, conflictingRetry); !errors.Is(err, ErrStateConflict) {
		t.Fatalf("RetryIdempotent(different fingerprint) error = %v, want ErrStateConflict", err)
	}

	rollbackIdempotency := storage.CoordinatorActionIdempotency{
		Scope: "panel", Key: "rollback-once", RequestFingerprint: "rollback-fingerprint",
		HTTPRequestFingerprint: "rollback-http-fingerprint", ExpiresAt: now.Add(time.Hour),
	}
	rollbackStart := make(chan struct{})
	rollbackResults := make(chan RollbackResult, 2)
	rollbackErrors := make(chan error, 2)
	var rollbackGroup sync.WaitGroup
	for _, coord := range []*Coordinator{coordA, coordB} {
		rollbackGroup.Add(1)
		go func(coord *Coordinator) {
			defer rollbackGroup.Done()
			<-rollbackStart
			result, err := coord.RollbackIdempotent(context.Background(), "edge-rollback", rollbackIdempotency)
			rollbackResults <- result
			rollbackErrors <- err
		}(coord)
	}
	close(rollbackStart)
	rollbackGroup.Wait()
	close(rollbackResults)
	close(rollbackErrors)
	for err := range rollbackErrors {
		if err != nil {
			t.Fatalf("concurrent RollbackIdempotent() error = %v", err)
		}
	}
	rollbackOperationID := ""
	rollbackReplays := 0
	for result := range rollbackResults {
		if result.Revision.Revision != 2 || result.Operation.ID == "" {
			t.Fatalf("concurrent rollback result = %+v", result)
		}
		if rollbackOperationID == "" {
			rollbackOperationID = result.Operation.ID
		} else if result.Operation.ID != rollbackOperationID {
			t.Fatalf("concurrent rollback operation IDs = %q and %q", rollbackOperationID, result.Operation.ID)
		}
		if result.Replayed {
			rollbackReplays++
		}
	}
	if rollbackReplays != 1 {
		t.Fatalf("concurrent rollback replay count = %d, want 1", rollbackReplays)
	}
	if pointer := mustPointer(t, storeA, "edge-rollback"); pointer.DesiredRevision != 2 || pointer.AppliedRevision != 1 {
		t.Fatalf("rollback pointer after concurrent calls = %+v", pointer)
	}
	if revisions, err := storeA.ListAgentRevisions(t.Context(), "edge-rollback"); err != nil || len(revisions) != 2 {
		t.Fatalf("rollback revisions after concurrent calls = %+v error=%v, want exactly 2", revisions, err)
	}
	conflictingRollback := rollbackIdempotency
	conflictingRollback.RequestFingerprint = "different-rollback-fingerprint"
	if _, err := coordA.RollbackIdempotent(t.Context(), "edge-rollback", conflictingRollback); !errors.Is(err, ErrStateConflict) {
		t.Fatalf("RollbackIdempotent(different fingerprint) error = %v, want ErrStateConflict", err)
	}
}

func TestJournalReconciliationAndAppliedPointersAreMonotonic(t *testing.T) {
	now := time.Date(2026, 7, 12, 14, 0, 0, 0, time.UTC)
	store := newCoordinatorTestStore(t)
	artifact := snapshotArtifact(t, "edge-journal", storage.Snapshot{
		Revision: 2, Rules: []storage.HTTPRule{}, L4Rules: []storage.L4Rule{},
		RelayListeners: []storage.RelayListener{}, WireGuardProfiles: []storage.WireGuardProfile{},
		EgressProfiles: []storage.EgressProfile{}, Certificates: []storage.ManagedCertificateBundle{},
		CertificatePolicies: []storage.ManagedCertificatePolicy{},
	}, now)
	seedRevisionsWithArtifactAt(t, store, now, "edge-1", artifact, 2, 2, 1, map[int64]string{
		1: storage.AgentRevisionStateApplied,
		2: storage.AgentRevisionStatePending,
	})
	coord := newTestCoordinator(t, store, now, 0.5)
	lease := mustClaim(t, coord, "edge-1")
	if _, err := coord.Start(t.Context(), StartRequest{Lease: lease, GenerationID: "generation-2"}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	reconciled, err := coord.ReconcileJournal(t.Context(), JournalReport{
		AgentID: "edge-1", Revision: 2, GenerationID: "generation-2",
	})
	if !errors.Is(err, ErrStateConflict) {
		t.Fatalf("ReconcileJournal(missing digest) error = %v, want ErrStateConflict", err)
	}

	reconciled, err = coord.ReconcileJournal(t.Context(), JournalReport{
		AgentID: "edge-1", Revision: 2, SnapshotDigest: artifact.SHA256, GenerationID: "generation-2",
	})
	if err != nil {
		t.Fatalf("ReconcileJournal() error = %v", err)
	}
	if reconciled.Stale || reconciled.Revision.State != storage.AgentRevisionStateApplied {
		t.Fatalf("journal result = %+v", reconciled)
	}
	pointer := mustPointer(t, store, "edge-1")
	if pointer.AppliedRevision != 2 || pointer.LastKnownGoodRevision != 2 {
		t.Fatalf("pointer after journal = %+v", pointer)
	}

	stale, err := coord.ReconcileJournal(t.Context(), JournalReport{
		AgentID: "edge-1", Revision: 1, GenerationID: "generation-1",
	})
	if err != nil {
		t.Fatalf("ReconcileJournal(stale) error = %v", err)
	}
	if !stale.Stale {
		t.Fatalf("stale journal result = %+v, want Stale", stale)
	}
	pointer = mustPointer(t, store, "edge-1")
	if pointer.AppliedRevision != 2 || pointer.LastKnownGoodRevision != 2 {
		t.Fatalf("stale journal regressed pointer: %+v", pointer)
	}
}

func TestStartupReconcilePersistsExpiredStartedRetryAcrossRestart(t *testing.T) {
	now := time.Date(2026, 7, 12, 15, 0, 0, 0, time.UTC)
	dbPath := filepath.Join(t.TempDir(), "coordinator.db")
	store := openCoordinatorTestStore(t, dbPath)
	seedRevisions(t, store, now, "edge-1", 2, 1, map[int64]string{
		1: storage.AgentRevisionStateApplied,
		2: storage.AgentRevisionStatePending,
	})
	clock := &fakeClock{now: now}
	coord := newTestCoordinatorWithClock(t, store, clock, 0.5)
	lease := mustClaim(t, coord, "edge-1")
	started, err := coord.Start(t.Context(), StartRequest{Lease: lease, GenerationID: "generation-2"})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	clock.now = started.Attempt.DeadlineAt

	reconciled, err := coord.ReconcileStartup(t.Context())
	if err != nil {
		t.Fatalf("ReconcileStartup() error = %v", err)
	}
	if len(reconciled.Agents) == 0 || reconciled.Agents[0].AttemptsConsumed != 1 {
		t.Fatalf("startup reconcile = %+v, want one consumed timed-out attempt", reconciled)
	}
	row := mustRevision(t, store, "edge-1", 2)
	if row.NextAttemptAt == nil || !row.NextAttemptAt.Equal(clock.now.Add(500*time.Millisecond)) {
		t.Fatalf("next_attempt_at = %v, want persisted 500ms jitter", row.NextAttemptAt)
	}
	persistedNext := *row.NextAttemptAt
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened := openCoordinatorTestStore(t, dbPath)
	restarted := newTestCoordinatorWithClock(t, reopened, clock, 0.1)
	if _, err := restarted.ReconcileStartup(t.Context()); err != nil {
		t.Fatalf("ReconcileStartup(restarted) error = %v", err)
	}
	row = mustRevision(t, reopened, "edge-1", 2)
	if row.NextAttemptAt == nil || !row.NextAttemptAt.Equal(persistedNext) {
		t.Fatalf("restart changed next_attempt_at from %v to %v", persistedNext, row.NextAttemptAt)
	}
}

func TestHigherDesiredSupersedesStartedLeaseBeforeClaimingLatest(t *testing.T) {
	now := time.Date(2026, 7, 12, 16, 0, 0, 0, time.UTC)
	store := newCoordinatorTestStore(t)
	seedRevisions(t, store, now, "edge-1", 2, 1, map[int64]string{
		1: storage.AgentRevisionStateApplied,
		2: storage.AgentRevisionStatePending,
	})
	coord := newTestCoordinator(t, store, now, 0.5)
	oldLease := mustClaim(t, coord, "edge-1")
	if _, err := coord.Start(t.Context(), StartRequest{Lease: oldLease, GenerationID: "generation-2"}); err != nil {
		t.Fatalf("Start(revision 2) error = %v", err)
	}
	appendPendingRevision(t, store, now.Add(time.Second), "edge-1", 3, 1, 60, 600)
	if _, err := coord.Applied(t.Context(), AppliedReport{Lease: oldLease, GenerationID: "generation-2"}); !errors.Is(err, ErrLeaseConflict) {
		t.Fatalf("Applied(old lease after desired advance) error = %v, want ErrLeaseConflict", err)
	}
	pointer := mustPointer(t, store, "edge-1")
	if pointer.DesiredRevision != 3 || pointer.AppliedRevision != 1 || pointer.LastKnownGoodRevision != 1 {
		t.Fatalf("old apply changed pointer: %+v", pointer)
	}
	if _, found, err := store.GetCoordinatorGeneration(t.Context(), "edge-1", "generation-2"); err != nil || found {
		t.Fatalf("GetCoordinatorGeneration(old apply) = found %v, error %v; want no generation", found, err)
	}

	latest := mustClaim(t, coord, "edge-1")
	if latest.Revision != 3 {
		t.Fatalf("latest lease revision = %d, want 3", latest.Revision)
	}
	if row := mustRevision(t, store, "edge-1", 2); row.State != storage.AgentRevisionStateSuperseded {
		t.Fatalf("revision 2 state = %q, want superseded", row.State)
	}
	if _, err := coord.Start(t.Context(), StartRequest{Lease: oldLease, GenerationID: "generation-stale"}); !errors.Is(err, ErrLeaseConflict) {
		t.Fatalf("Start(superseded lease) error = %v, want ErrLeaseConflict", err)
	}
	attempts := mustAttempts(t, store, "edge-1", 2)
	if len(attempts) != 1 || attempts[0].State != storage.AgentRevisionAttemptStateSuperseded {
		t.Fatalf("old attempts = %+v, want superseded", attempts)
	}
}

func TestRollbackFencesInFlightApplyBeforeActivatingGeneration(t *testing.T) {
	now := time.Date(2026, 7, 12, 16, 30, 0, 0, time.UTC)
	store := newCoordinatorTestStore(t)
	artifact := snapshotArtifact(t, "edge-rollback-race", storage.Snapshot{
		DesiredVersion: "v1.2.3", Revision: 1,
		Rules: []storage.HTTPRule{}, L4Rules: []storage.L4Rule{},
		RelayListeners: []storage.RelayListener{}, WireGuardProfiles: []storage.WireGuardProfile{},
		EgressProfiles: []storage.EgressProfile{}, Certificates: []storage.ManagedCertificateBundle{},
		CertificatePolicies: []storage.ManagedCertificatePolicy{},
	}, now)
	seedRevisionsWithArtifact(t, store, now, "edge-1", artifact, 2, 1, map[int64]string{
		1: storage.AgentRevisionStateApplied,
		2: storage.AgentRevisionStatePending,
	})
	coord := newTestCoordinator(t, store, now, 0.5)
	oldLease := mustClaim(t, coord, "edge-1")
	if _, err := coord.Start(t.Context(), StartRequest{Lease: oldLease, GenerationID: "generation-2"}); err != nil {
		t.Fatalf("Start(revision 2) error = %v", err)
	}
	rollback, err := coord.Rollback(t.Context(), "edge-1")
	if err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}
	if rollback.Revision.Revision != 3 {
		t.Fatalf("rollback revision = %d, want 3", rollback.Revision.Revision)
	}
	if _, err := coord.Applied(t.Context(), AppliedReport{Lease: oldLease, GenerationID: "generation-2"}); !errors.Is(err, ErrLeaseConflict) {
		t.Fatalf("Applied(old lease after rollback) error = %v, want ErrLeaseConflict", err)
	}
	pointer := mustPointer(t, store, "edge-1")
	if pointer.DesiredRevision != 3 || pointer.AppliedRevision != 1 || pointer.LastKnownGoodRevision != 1 {
		t.Fatalf("old apply changed rollback pointer: %+v", pointer)
	}
	if row := mustRevision(t, store, "edge-1", 2); row.State != storage.AgentRevisionStateApplying {
		t.Fatalf("old revision state = %q, want applying until claim/reconcile fences it", row.State)
	}
	if _, found, err := store.GetCoordinatorGeneration(t.Context(), "edge-1", "generation-2"); err != nil || found {
		t.Fatalf("GetCoordinatorGeneration(old rollback apply) = found %v, error %v; want no generation", found, err)
	}
	latest := mustClaim(t, coord, "edge-1")
	if latest.Revision != 3 {
		t.Fatalf("latest lease revision = %d, want rollback revision 3", latest.Revision)
	}
}

func TestAppliedTransitionIsMonotonicAndDrainCompletesSeparately(t *testing.T) {
	now := time.Date(2026, 7, 12, 17, 0, 0, 0, time.UTC)
	store := newCoordinatorTestStore(t)
	seedRevisions(t, store, now, "edge-1", 2, 1, map[int64]string{
		1: storage.AgentRevisionStateApplied,
		2: storage.AgentRevisionStatePending,
	})
	coord := newTestCoordinator(t, store, now, 0.5)
	lease2 := mustClaim(t, coord, "edge-1")
	if _, err := coord.Start(t.Context(), StartRequest{Lease: lease2, GenerationID: "generation-2"}); err != nil {
		t.Fatalf("Start(revision 2) error = %v", err)
	}
	applied2, err := coord.Applied(t.Context(), AppliedReport{Lease: lease2, GenerationID: "generation-2"})
	if err != nil {
		t.Fatalf("Applied(revision 2) error = %v", err)
	}
	if applied2.Revision.DrainState != storage.AgentRevisionDrainStateDrained {
		t.Fatalf("first applied drain state = %q, want drained", applied2.Revision.DrainState)
	}
	operation, found, err := store.GetOperation(t.Context(), "seed-edge-1")
	if err != nil || !found || operation.Status != storage.OperationStatusApplied {
		t.Fatalf("operation after apply = %+v, found %v, error %v", operation, found, err)
	}

	appendPendingRevision(t, store, now.Add(time.Second), "edge-1", 3, 2, 60, 600)
	lease3 := mustClaim(t, coord, "edge-1")
	if _, err := coord.Start(t.Context(), StartRequest{Lease: lease3, GenerationID: "generation-3"}); err != nil {
		t.Fatalf("Start(revision 3) error = %v", err)
	}
	if _, err := coord.Applied(t.Context(), AppliedReport{Lease: lease3, GenerationID: "generation-wrong"}); !errors.Is(err, ErrStateConflict) {
		t.Fatalf("Applied(wrong generation) error = %v, want ErrStateConflict", err)
	}
	applied3, err := coord.Applied(t.Context(), AppliedReport{Lease: lease3, GenerationID: "generation-3"})
	if err != nil {
		t.Fatalf("Applied(revision 3) error = %v", err)
	}
	if applied3.Revision.DrainState != storage.AgentRevisionDrainStateDraining {
		t.Fatalf("second applied drain state = %q, want draining", applied3.Revision.DrainState)
	}
	replayed, err := coord.ReconcileJournal(t.Context(), JournalReport{
		AgentID: "edge-1", Revision: 3, GenerationID: "generation-3",
	})
	if err != nil {
		t.Fatalf("ReconcileJournal(replay) error = %v", err)
	}
	if replayed.Revision.DrainState != storage.AgentRevisionDrainStateDraining {
		t.Fatalf("journal replay drain state = %q, want draining", replayed.Revision.DrainState)
	}
	drained, err := coord.Drained(t.Context(), DrainReport{AgentID: "edge-1", GenerationID: "generation-2"})
	if err != nil {
		t.Fatalf("Drained() error = %v", err)
	}
	if drained.Revision != 3 || drained.DrainState != storage.AgentRevisionDrainStateDrained {
		t.Fatalf("drained revision = %+v, want revision 3 drained", drained)
	}
	if _, err := coord.Fail(t.Context(), FailureReport{Lease: lease2, GenerationID: "generation-2", ErrorCode: "late", ErrorMessage: "late report"}); !errors.Is(err, ErrLeaseConflict) {
		t.Fatalf("Fail(late applied lease) error = %v, want ErrLeaseConflict", err)
	}
	pointer := mustPointer(t, store, "edge-1")
	if pointer.DesiredRevision != 3 || pointer.AppliedRevision != 3 || pointer.LastKnownGoodRevision != 3 {
		t.Fatalf("final pointer = %+v", pointer)
	}
}

func TestPersistedRevisionTimeoutsOverrideNewCoordinatorDefaults(t *testing.T) {
	now := time.Date(2026, 7, 12, 18, 0, 0, 0, time.UTC)
	store := newCoordinatorTestStore(t)
	seedRevisions(t, store, now, "edge-1", 1, 1, map[int64]string{1: storage.AgentRevisionStateApplied})
	appendPendingRevision(t, store, now.Add(time.Second), "edge-1", 2, 1, 17, 91)
	clock := &fakeClock{now: now.Add(2 * time.Second)}
	ids := &sequenceIDs{}
	coord, err := New(store, Options{
		Clock: clock, Random: &sequenceRandom{values: []float64{0.5}}, NewID: ids.New,
		ApplyTimeout: 5 * time.Second, DrainTimeout: 6 * time.Second,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	lease := mustClaim(t, coord, "edge-1")
	if lease.ApplyTimeoutSeconds != 17 || lease.DrainTimeoutSeconds != 91 {
		t.Fatalf("lease timeouts = (%d,%d), want persisted (17,91)", lease.ApplyTimeoutSeconds, lease.DrainTimeoutSeconds)
	}
	if !lease.DeadlineAt.Equal(clock.now.Add(17 * time.Second)) {
		t.Fatalf("lease deadline = %v, want %v", lease.DeadlineAt, clock.now.Add(17*time.Second))
	}
}

func TestConcurrentStoresIssueOnlyOneValidLeasePerAgent(t *testing.T) {
	now := time.Date(2026, 7, 12, 19, 0, 0, 0, time.UTC)
	dbPath := filepath.Join(t.TempDir(), "coordinator.db")
	storeA := openCoordinatorTestStore(t, dbPath)
	seedRevisions(t, storeA, now, "edge-1", 2, 1, map[int64]string{
		1: storage.AgentRevisionStateApplied,
		2: storage.AgentRevisionStatePending,
	})
	storeB := openCoordinatorTestStore(t, dbPath)
	clock := &fakeClock{now: now}
	coordA, err := New(storeA, Options{
		Clock: clock, Random: &sequenceRandom{values: []float64{0.5}},
		NewID: func(prefix string) (string, error) { return prefix + "-store-a", nil },
	})
	if err != nil {
		t.Fatalf("New(A) error = %v", err)
	}
	coordB, err := New(storeB, Options{
		Clock: clock, Random: &sequenceRandom{values: []float64{0.5}},
		NewID: func(prefix string) (string, error) { return prefix + "-store-b", nil },
	})
	if err != nil {
		t.Fatalf("New(B) error = %v", err)
	}

	start := make(chan struct{})
	results := make(chan ClaimResult, 2)
	errorsCh := make(chan error, 2)
	var group sync.WaitGroup
	for _, coord := range []*Coordinator{coordA, coordB} {
		group.Add(1)
		go func(coord *Coordinator) {
			defer group.Done()
			<-start
			result, err := coord.Claim(context.Background(), "edge-1")
			results <- result
			errorsCh <- err
		}(coord)
	}
	close(start)
	group.Wait()
	close(results)
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Fatalf("concurrent Claim() error = %v", err)
		}
	}
	leases, busy := 0, 0
	for result := range results {
		if result.Lease != nil {
			leases++
		}
		if result.Busy {
			busy++
		}
	}
	if leases != 1 || busy != 1 {
		t.Fatalf("concurrent results = leases %d, busy %d; want 1 and 1", leases, busy)
	}
	attempts := mustAttempts(t, storeA, "edge-1", 2)
	if len(attempts) != 1 || attempts[0].State != storage.AgentRevisionAttemptStateLeased {
		t.Fatalf("persisted attempts = %+v, want one lease", attempts)
	}
}

type fakeClock struct {
	now time.Time
}

func (c *fakeClock) Now() time.Time { return c.now }

type sequenceRandom struct {
	values []float64
	index  int
}

func (r *sequenceRandom) Float64() float64 {
	if len(r.values) == 0 {
		return 0
	}
	value := r.values[r.index%len(r.values)]
	r.index++
	return value
}

type sequenceIDs struct {
	counts map[string]int
}

func (s *sequenceIDs) New(prefix string) (string, error) {
	if s.counts == nil {
		s.counts = map[string]int{}
	}
	s.counts[prefix]++
	return fmt.Sprintf("%s-%d", prefix, s.counts[prefix]), nil
}

func newTestCoordinator(t *testing.T, store *storage.GormStore, now time.Time, random ...float64) *Coordinator {
	t.Helper()
	return newTestCoordinatorWithClock(t, store, &fakeClock{now: now}, random...)
}

func newTestCoordinatorWithClock(t *testing.T, store *storage.GormStore, clock *fakeClock, random ...float64) *Coordinator {
	t.Helper()
	ids := &sequenceIDs{}
	coord, err := New(store, Options{
		Clock:  clock,
		Random: &sequenceRandom{values: random},
		NewID:  ids.New,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return coord
}

func newCoordinatorTestStore(t *testing.T) *storage.GormStore {
	t.Helper()
	return openCoordinatorTestStore(t, filepath.Join(t.TempDir(), "coordinator.db"))
}

func openCoordinatorTestStore(t *testing.T, dbPath string) *storage.GormStore {
	t.Helper()
	store, err := storage.NewStore(storage.StoreConfig{
		Driver: "sqlite", DSN: dbPath, DataRoot: filepath.Dir(dbPath), LocalAgentID: "local",
	})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func seedRevisions(t *testing.T, store *storage.GormStore, now time.Time, agentID string, desired, applied int64, states map[int64]string) {
	t.Helper()
	seedRevisionsWithArtifact(t, store, now, agentID, storage.GenerationArtifactRow{}, desired, applied, states)
}

func seedRevisionsWithArtifact(t *testing.T, store *storage.GormStore, now time.Time, agentID string, artifact storage.GenerationArtifactRow, desired, applied int64, states map[int64]string) {
	t.Helper()
	seedRevisionsWithArtifactAt(t, store, now, agentID, artifact, applied, desired, applied, states)
}

func seedRevisionsWithArtifactAt(t *testing.T, store *storage.GormStore, now time.Time, agentID string, artifact storage.GenerationArtifactRow, artifactRevision, desired, applied int64, states map[int64]string) {
	t.Helper()
	revisions := make([]storage.AgentRevisionRow, 0, len(states))
	for revision := int64(1); revision <= desired; revision++ {
		state, ok := states[revision]
		if !ok {
			continue
		}
		row := storage.AgentRevisionRow{
			AgentID: agentID, Revision: revision, OperationID: "seed-" + agentID,
			State: state, ApplyTimeoutSeconds: 60, DrainTimeoutSeconds: 600,
			CreatedAt: now, UpdatedAt: now,
		}
		if revision == artifactRevision && artifact.ID != "" {
			row.SnapshotArtifactID = artifact.ID
			row.SnapshotDigest = artifact.SHA256
			row.DesiredVersion = "v1.2.3"
			appliedAt := now
			row.AppliedAt = &appliedAt
		}
		if state == storage.AgentRevisionStateFailed {
			failedAt := now
			row.FailedAt = &failedAt
			row.AttemptCount = 5
			row.ErrorCode = "attempts_exhausted"
		}
		revisions = append(revisions, row)
	}
	ledger := storage.RevisionLedgerWrite{
		Operation: storage.OperationRow{
			ID: "seed-" + agentID, Kind: "test_seed", Status: storage.OperationStatusPending,
			PrimaryAgentID: agentID, CreatedAt: now, UpdatedAt: now,
		},
		Revisions: revisions,
		Pointers: []storage.AgentRevisionPointerRow{{
			AgentID: agentID, DesiredRevision: desired, AppliedRevision: applied,
			LastKnownGoodRevision: applied, UpdatedAt: now,
		}},
	}
	if artifact.ID != "" {
		ledger.Artifacts = []storage.GenerationArtifactRow{artifact}
		ledger.ArtifactRefs = []storage.AgentRevisionArtifactRow{{
			AgentID: agentID, Revision: artifactRevision, ArtifactID: artifact.ID, Role: "snapshot", CreatedAt: now,
		}}
	}
	if err := store.CreateRevisionLedger(t.Context(), ledger); err != nil {
		t.Fatalf("CreateRevisionLedger() error = %v", err)
	}
}

func appendPendingRevision(t *testing.T, store *storage.GormStore, now time.Time, agentID string, revision, applied int64, applyTimeout, drainTimeout int) {
	t.Helper()
	operationID := fmt.Sprintf("append-%s-%d", agentID, revision)
	if err := store.CreateRevisionLedger(t.Context(), storage.RevisionLedgerWrite{
		Operation: storage.OperationRow{
			ID: operationID, Kind: "test_append", Status: storage.OperationStatusPending,
			PrimaryAgentID: agentID, CreatedAt: now, UpdatedAt: now,
		},
		Revisions: []storage.AgentRevisionRow{{
			AgentID: agentID, Revision: revision, OperationID: operationID,
			State: storage.AgentRevisionStatePending, ApplyTimeoutSeconds: applyTimeout,
			DrainTimeoutSeconds: drainTimeout, CreatedAt: now, UpdatedAt: now,
		}},
		Pointers: []storage.AgentRevisionPointerRow{{
			AgentID: agentID, DesiredRevision: revision, AppliedRevision: applied,
			LastKnownGoodRevision: applied, UpdatedAt: now,
		}},
	}); err != nil {
		t.Fatalf("append pending revision %d: %v", revision, err)
	}
}

func snapshotArtifact(t *testing.T, id string, snapshot storage.Snapshot, now time.Time) storage.GenerationArtifactRow {
	t.Helper()
	payload, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("json.Marshal(snapshot) error = %v", err)
	}
	digest := sha256.Sum256(payload)
	return storage.GenerationArtifactRow{
		ID: id, Kind: "agent_snapshot", SHA256: hex.EncodeToString(digest[:]),
		Payload: payload, SizeBytes: int64(len(payload)), CreatedAt: now,
	}
}

func mustClaim(t *testing.T, coord *Coordinator, agentID string) Lease {
	t.Helper()
	result, err := coord.Claim(t.Context(), agentID)
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if result.Lease == nil {
		t.Fatalf("Claim() = %+v, want lease", result)
	}
	return *result.Lease
}

func mustRevision(t *testing.T, store *storage.GormStore, agentID string, revision int64) storage.AgentRevisionRow {
	t.Helper()
	row, found, err := store.GetCoordinatorRevision(t.Context(), agentID, revision)
	if err != nil || !found {
		t.Fatalf("GetCoordinatorRevision(%s,%d) = found %v, error %v", agentID, revision, found, err)
	}
	return row
}

func mustPointer(t *testing.T, store *storage.GormStore, agentID string) storage.AgentRevisionPointerRow {
	t.Helper()
	row, found, err := store.GetAgentRevisionPointer(t.Context(), agentID)
	if err != nil || !found {
		t.Fatalf("GetAgentRevisionPointer(%s) = found %v, error %v", agentID, found, err)
	}
	return row
}

func mustAttempts(t *testing.T, store *storage.GormStore, agentID string, revision int64) []storage.AgentRevisionAttemptRow {
	t.Helper()
	rows, err := store.ListCoordinatorAttempts(t.Context(), agentID, revision)
	if err != nil {
		t.Fatalf("ListCoordinatorAttempts() error = %v", err)
	}
	return rows
}
