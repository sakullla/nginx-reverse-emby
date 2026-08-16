//go:build !integration

package coordinator

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type fakeClock struct{ now time.Time }

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

type sequenceIDs struct{ counts map[string]int }

func (s *sequenceIDs) New(prefix string) (string, error) {
	if s.counts == nil {
		s.counts = map[string]int{}
	}
	s.counts[prefix]++
	return fmt.Sprintf("%s-%d", prefix, s.counts[prefix]), nil
}

func newTestCoordinatorWithClock(t *testing.T, store *storage.GormStore, clock *fakeClock, random ...float64) *Coordinator {
	t.Helper()
	ids := &sequenceIDs{}
	coord, err := New(store, Options{Clock: clock, Random: &sequenceRandom{values: random}, NewID: ids.New})
	if err != nil {
		t.Fatal(err)
	}
	return coord
}

func newCoordinatorTestStore(t *testing.T) *storage.GormStore {
	t.Helper()
	if testing.Short() {
		t.Skip("SQLite-backed coordinator scenarios run in the full test tier")
	}
	dbPath := filepath.Join(t.TempDir(), "coordinator.db")
	if err := ensureCoordinatorSQLiteFixture(dbPath); err != nil {
		t.Fatal(err)
	}
	store, err := storage.NewStore(storage.StoreConfig{
		Driver: "sqlite", DSN: coordinatorSQLiteDSN(dbPath), DataRoot: filepath.Dir(dbPath), LocalAgentID: "local",
		SkipBootstrapSchema: true, TrafficStatsEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func seedRevisions(t *testing.T, store *storage.GormStore, now time.Time, agentID string, desired, applied int64, states map[int64]string) {
	t.Helper()
	revisions := make([]storage.AgentRevisionRow, 0, len(states))
	for revision := int64(1); revision <= desired; revision++ {
		state, ok := states[revision]
		if !ok {
			continue
		}
		revisions = append(revisions, storage.AgentRevisionRow{
			AgentID: agentID, Revision: revision, OperationID: "seed-" + agentID,
			State: state, ApplyTimeoutSeconds: 60, DrainTimeoutSeconds: 600,
			CreatedAt: now, UpdatedAt: now,
		})
	}
	if err := store.CreateRevisionLedger(t.Context(), storage.RevisionLedgerWrite{
		Operation: storage.OperationRow{
			ID: "seed-" + agentID, Kind: "test_seed", Status: storage.OperationStatusPending,
			PrimaryAgentID: agentID, CreatedAt: now, UpdatedAt: now,
		},
		Revisions: revisions,
		Pointers: []storage.AgentRevisionPointerRow{{
			AgentID: agentID, DesiredRevision: desired, AppliedRevision: applied,
			LastKnownGoodRevision: applied, UpdatedAt: now,
		}},
	}); err != nil {
		t.Fatal(err)
	}
}

func mustRevision(t *testing.T, store *storage.GormStore, agentID string, revision int64) storage.AgentRevisionRow {
	t.Helper()
	row, found, err := store.GetCoordinatorRevision(t.Context(), agentID, revision)
	if err != nil || !found {
		t.Fatalf("revision %s/%d found=%v err=%v", agentID, revision, found, err)
	}
	return row
}

func TestIntegrationCoordinatorClaimStartApplyAndExpiredLeaseFence(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	store := newCoordinatorTestStore(t)
	seedRevisions(t, store, now, "edge-1", 2, 1, map[int64]string{
		1: storage.AgentRevisionStateApplied,
		2: storage.AgentRevisionStatePending,
	})
	clock := &fakeClock{now: now}
	coord := newTestCoordinatorWithClock(t, store, clock, 0.5)

	first, err := coord.Claim(t.Context(), "edge-1")
	if err != nil || first.Lease == nil || first.Lease.Revision != 2 {
		t.Fatalf("first claim = %+v err=%v", first, err)
	}
	second, err := coord.Claim(t.Context(), "edge-1")
	if err != nil || !second.Busy || second.Lease != nil {
		t.Fatalf("busy claim = %+v err=%v", second, err)
	}

	clock.now = first.Lease.DeadlineAt
	if result, err := coord.Reconcile(t.Context(), "edge-1"); err != nil || !result.Ready {
		t.Fatalf("reconcile expired lease = %+v err=%v", result, err)
	}
	reclaimed, err := coord.Claim(t.Context(), "edge-1")
	if err != nil || reclaimed.Lease == nil || reclaimed.Lease.LeaseID == first.Lease.LeaseID {
		t.Fatalf("reclaim = %+v err=%v", reclaimed, err)
	}
	if _, err := coord.Start(t.Context(), StartRequest{Lease: *first.Lease, GenerationID: "stale"}); !errors.Is(err, ErrLeaseConflict) {
		t.Fatalf("stale start err=%v", err)
	}
	started, err := coord.Start(t.Context(), StartRequest{Lease: *reclaimed.Lease, GenerationID: "gen-2"})
	if err != nil || started.Revision.State != storage.AgentRevisionStateApplying {
		t.Fatalf("start = %+v err=%v", started, err)
	}
	applied, err := coord.Applied(t.Context(), AppliedReport{Lease: *reclaimed.Lease, GenerationID: "gen-2"})
	if err != nil || applied.Revision.Revision != 2 {
		t.Fatalf("applied = %+v err=%v", applied, err)
	}
	if row := mustRevision(t, store, "edge-1", 2); row.State == storage.AgentRevisionStatePending {
		t.Fatalf("revision remained pending: %+v", row)
	}
}

func TestIntegrationCoordinatorRetryAndRollbackRemainIdempotent(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 16, 11, 0, 0, 0, time.UTC)
	store := newCoordinatorTestStore(t)
	seedRevisions(t, store, now, "edge-1", 1, 1, map[int64]string{1: storage.AgentRevisionStateApplied})
	if err := store.CreateRevisionLedger(t.Context(), storage.RevisionLedgerWrite{
		Operation: storage.OperationRow{
			ID: "append-edge-1-2", Kind: "test_append", Status: storage.OperationStatusPending,
			PrimaryAgentID: "edge-1", CreatedAt: now, UpdatedAt: now,
		},
		Revisions: []storage.AgentRevisionRow{{
			AgentID: "edge-1", Revision: 2, OperationID: "append-edge-1-2",
			State: storage.AgentRevisionStateFailed, ApplyTimeoutSeconds: 60, DrainTimeoutSeconds: 600,
			AttemptCount: 5, ErrorCode: "attempts_exhausted", CreatedAt: now, UpdatedAt: now,
		}},
		Pointers: []storage.AgentRevisionPointerRow{{
			AgentID: "edge-1", DesiredRevision: 2, AppliedRevision: 1, LastKnownGoodRevision: 1, UpdatedAt: now,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	coord := newTestCoordinatorWithClock(t, store, &fakeClock{now: now}, 0.1)
	idempotency := storage.CoordinatorActionIdempotency{
		Scope: "panel", Key: "retry-1", RequestFingerprint: "retry-edge-1-2", ExpiresAt: now.Add(time.Hour),
	}
	first, err := coord.RetryIdempotent(t.Context(), "edge-1", 2, idempotency)
	if err != nil {
		t.Fatal(err)
	}
	second, err := coord.RetryIdempotent(t.Context(), "edge-1", 2, idempotency)
	if err != nil || second.Revision.Revision != first.Revision.Revision {
		t.Fatalf("retry replay = %+v err=%v", second, err)
	}
}
