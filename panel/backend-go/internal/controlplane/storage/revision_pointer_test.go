//go:build integration

package storage

import (
	"strings"
	"testing"
	"time"
)

func TestIntegrationCreateRevisionLedgerRejectsLowerMonotonicPointer(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	store := newTrafficTestStore(t, true)
	now := time.Date(2026, 7, 12, 4, 0, 0, 0, time.UTC)
	if err := store.CreateRevisionLedger(ctx, RevisionLedgerWrite{
		Operation: OperationRow{ID: "op-pointer-1", Kind: "test", Status: OperationStatusPending, CreatedAt: now, UpdatedAt: now},
		Revisions: []AgentRevisionRow{{
			AgentID: "edge-1", Revision: 4, OperationID: "op-pointer-1", State: AgentRevisionStateApplied, CreatedAt: now, UpdatedAt: now,
		}},
		Pointers: []AgentRevisionPointerRow{{
			AgentID: "edge-1", DesiredRevision: 4, AppliedRevision: 4, LastKnownGoodRevision: 4, UpdatedAt: now,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	err := store.CreateRevisionLedger(ctx, RevisionLedgerWrite{
		Operation: OperationRow{ID: "op-pointer-2", Kind: "test", Status: OperationStatusPending, CreatedAt: now, UpdatedAt: now},
		Revisions: []AgentRevisionRow{{
			AgentID: "edge-1", Revision: 3, OperationID: "op-pointer-2", State: AgentRevisionStatePending, CreatedAt: now, UpdatedAt: now,
		}},
		Pointers: []AgentRevisionPointerRow{{
			AgentID: "edge-1", DesiredRevision: 3, AppliedRevision: 3, LastKnownGoodRevision: 3, UpdatedAt: now,
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("lower pointer err=%v, want stale", err)
	}
	if _, found, getErr := store.GetOperation(ctx, "op-pointer-2"); getErr != nil {
		t.Fatal(getErr)
	} else if found {
		t.Fatal("stale pointer operation survived")
	}
	pointer, found, err := store.GetAgentRevisionPointer(ctx, "edge-1")
	if err != nil || !found || pointer.DesiredRevision != 4 {
		t.Fatalf("pointer after stale write = %+v found=%v err=%v", pointer, found, err)
	}
}
