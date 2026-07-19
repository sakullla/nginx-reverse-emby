package storage

import (
	"testing"
	"time"
)

func TestPruneRevisionHistoryDeletesOnlyExpiredOrphanOperations(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	old := now.Add(-10 * 24 * time.Hour)
	recent := now.Add(-24 * time.Hour)
	createOperation := func(operation OperationRow, revisions []AgentRevisionRow, events []RevisionEventRow, idempotency []IdempotencyRecordRow) {
		t.Helper()
		if err := store.CreateRevisionLedger(t.Context(), RevisionLedgerWrite{
			Operation: operation, Revisions: revisions, Events: events, IdempotencyRecords: idempotency,
		}); err != nil {
			t.Fatal(err)
		}
	}
	completedOperation := func(id string, completedAt time.Time) OperationRow {
		t.Helper()
		return OperationRow{
			ID: id, Kind: "test", Status: OperationStatusApplied, PrimaryAgentID: "edge-1",
			CreatedAt: completedAt, UpdatedAt: completedAt, CompletedAt: &completedAt,
		}
	}

	createOperation(completedOperation("expired", old), []AgentRevisionRow{{
		AgentID: "edge-expired", Revision: 1, State: AgentRevisionStateApplied, CreatedAt: old, UpdatedAt: old,
	}}, []RevisionEventRow{{
		OperationID: "expired", AgentID: "edge-expired", Revision: 1, EventType: "applied", CreatedAt: old,
	}}, nil)
	createOperation(completedOperation("recent", recent), nil, nil, nil)
	createOperation(OperationRow{
		ID: "pending", Kind: "test", Status: OperationStatusPending, PrimaryAgentID: "edge-1",
		CreatedAt: old, UpdatedAt: old,
	}, nil, nil, nil)
	createOperation(completedOperation("referenced", old), nil, nil, []IdempotencyRecordRow{{
		Scope: "test", Key: "referenced", RequestFingerprint: "fingerprint", OperationID: "referenced",
		CreatedAt: old, ExpiresAt: now.Add(24 * time.Hour),
	}})

	result, err := store.PruneRevisionHistory(t.Context(), RevisionRetentionPolicy{
		Now: now, MaxAge: 2 * 24 * time.Hour, OperationMaxAge: 7 * 24 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.RevisionsDeleted != 1 || result.OperationsDeleted != 1 {
		t.Fatalf("prune result = %+v", result)
	}

	for _, test := range []struct {
		id   string
		want bool
	}{
		{id: "expired", want: false},
		{id: "recent", want: true},
		{id: "pending", want: true},
		{id: "referenced", want: true},
	} {
		_, found, err := store.GetOperation(t.Context(), test.id)
		if err != nil {
			t.Fatal(err)
		}
		if found != test.want {
			t.Errorf("operation %q found = %v, want %v", test.id, found, test.want)
		}
	}
}
