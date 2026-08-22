//go:build integration

package storage

import (
	"context"
	"testing"
	"time"
)

func TestIntegrationLoadAgentHeartbeatSnapshotUsesCoordinatorDesiredRevision(t *testing.T) {
	store := newTrafficTestStore(t, true)
	ctx := context.Background()
	if err := store.SaveAgent(ctx, AgentRow{
		ID: "edge-heartbeat", Name: "edge-heartbeat", Platform: "linux-amd64",
		DesiredRevision: 2, CurrentRevision: 419,
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}

	now := time.Date(2026, 8, 22, 0, 43, 0, 0, time.UTC)
	if err := store.CreateRevisionLedger(ctx, RevisionLedgerWrite{
		Operation: OperationRow{
			ID: "op-heartbeat-420", Kind: "plugin.upgrade", Status: OperationStatusPending,
			PrimaryAgentID: "edge-heartbeat", CreatedAt: now, UpdatedAt: now,
		},
		Revisions: []AgentRevisionRow{{
			AgentID: "edge-heartbeat", Revision: 420, OperationID: "op-heartbeat-420",
			State: AgentRevisionStatePending, CreatedAt: now, UpdatedAt: now,
		}},
		Pointers: []AgentRevisionPointerRow{{
			AgentID: "edge-heartbeat", DesiredRevision: 420, AppliedRevision: 419,
			LastKnownGoodRevision: 419, UpdatedAt: now,
		}},
	}); err != nil {
		t.Fatalf("CreateRevisionLedger() error = %v", err)
	}

	snapshot, err := store.LoadAgentHeartbeatSnapshot(ctx, "edge-heartbeat", nil)
	if err != nil {
		t.Fatalf("LoadAgentHeartbeatSnapshot() error = %v", err)
	}
	if snapshot.Metadata.DesiredRevision != 420 {
		t.Fatalf("metadata desired revision = %d, want 420", snapshot.Metadata.DesiredRevision)
	}
	if snapshot.Snapshot.Revision != 420 {
		t.Fatalf("snapshot revision = %d, want 420", snapshot.Snapshot.Revision)
	}
}
