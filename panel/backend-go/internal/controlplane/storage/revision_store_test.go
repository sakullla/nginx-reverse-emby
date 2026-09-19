//go:build integration

package storage

import (
	"testing"
	"time"

	"gorm.io/gorm"
)

func TestIntegrationBootstrapRevisionLedgerCreatesPendingDesiredAndIsIdempotent(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	store := newTrafficTestStore(t, true)

	clearRevisionLedgerForTest(t, store)
	if err := store.SaveAgent(ctx, AgentRow{
		ID:              "edge-1",
		Name:            "edge-1",
		Platform:        "linux-amd64",
		DesiredRevision: 7,
		CurrentRevision: 3,
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	if err := store.SaveHTTPRules(ctx, "edge-1", []HTTPRuleRow{{
		ID:           1,
		AgentID:      "edge-1",
		FrontendURL:  "https://edge.example.com",
		BackendsJSON: `[{"url":"http://127.0.0.1:8080"}]`,
		Enabled:      true,
		Revision:     7,
	}}); err != nil {
		t.Fatalf("SaveHTTPRules() error = %v", err)
	}

	if err := store.BootstrapRevisionLedger(ctx); err != nil {
		t.Fatalf("BootstrapRevisionLedger() error = %v", err)
	}

	pointer, found, err := store.GetAgentRevisionPointer(ctx, "edge-1")
	if err != nil {
		t.Fatalf("GetAgentRevisionPointer() error = %v", err)
	}
	if !found {
		t.Fatal("GetAgentRevisionPointer() found = false")
	}
	if pointer.DesiredRevision != 7 || pointer.AppliedRevision != 3 || pointer.LastKnownGoodRevision != 3 {
		t.Fatalf("pointer = %+v", pointer)
	}

	revisions, err := store.ListAgentRevisions(ctx, "edge-1")
	if err != nil {
		t.Fatalf("ListAgentRevisions() error = %v", err)
	}
	if len(revisions) != 2 {
		t.Fatalf("len(revisions) = %d, want 2: %+v", len(revisions), revisions)
	}
	if revisions[0].Revision != 3 || revisions[0].State != AgentRevisionStateApplied || !revisions[0].LegacyBaseline {
		t.Fatalf("applied baseline = %+v", revisions[0])
	}
	if revisions[0].SnapshotArtifactID != "" {
		t.Fatalf("old applied baseline artifact = %q, want empty because legacy runtime cannot be reconstructed", revisions[0].SnapshotArtifactID)
	}
	if revisions[1].Revision != 7 || revisions[1].State != AgentRevisionStatePending || revisions[1].SnapshotArtifactID == "" {
		t.Fatalf("desired baseline = %+v", revisions[1])
	}

	artifact, found, err := store.GetGenerationArtifact(ctx, revisions[1].SnapshotArtifactID)
	if err != nil {
		t.Fatalf("GetGenerationArtifact() error = %v", err)
	}
	if !found || len(artifact.Payload) == 0 {
		t.Fatalf("desired artifact found=%v row=%+v", found, artifact)
	}
	runtimeSnapshot, found, err := store.LoadCoordinatorRuntimeSnapshot(ctx, "edge-1", 7)
	if err != nil || !found {
		t.Fatalf("LoadCoordinatorRuntimeSnapshot(edge-1/7) = %+v found=%v error=%v", runtimeSnapshot, found, err)
	}
	if runtimeSnapshot.Revision.Revision != 7 || runtimeSnapshot.Snapshot.Revision != 7 {
		t.Fatalf("runtime snapshot = %+v", runtimeSnapshot)
	}

	if err := store.BootstrapRevisionLedger(ctx); err != nil {
		t.Fatalf("BootstrapRevisionLedger(second) error = %v", err)
	}
	again, err := store.ListAgentRevisions(ctx, "edge-1")
	if err != nil {
		t.Fatalf("ListAgentRevisions(second) error = %v", err)
	}
	if len(again) != len(revisions) {
		t.Fatalf("second bootstrap revisions = %d, want %d", len(again), len(revisions))
	}
}

func TestIntegrationCreateRevisionLedgerRollsBackAtomically(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	store := newTrafficTestStore(t, true)
	now := time.Date(2026, 7, 12, 4, 0, 0, 0, time.UTC)

	err := store.CreateRevisionLedger(ctx, RevisionLedgerWrite{
		Operation: OperationRow{
			ID:        "op-atomic",
			Kind:      "test",
			Status:    OperationStatusPending,
			CreatedAt: now,
			UpdatedAt: now,
		},
		Revisions: []AgentRevisionRow{
			{AgentID: "edge-1", Revision: 9, OperationID: "op-atomic", State: AgentRevisionStatePending, CreatedAt: now, UpdatedAt: now},
			{AgentID: "edge-1", Revision: 9, OperationID: "op-atomic", State: AgentRevisionStatePending, CreatedAt: now, UpdatedAt: now},
		},
	})
	if err == nil {
		t.Fatal("CreateRevisionLedger() error = nil, want duplicate revision error")
	}

	if _, found, getErr := store.GetOperation(ctx, "op-atomic"); getErr != nil {
		t.Fatalf("GetOperation() error = %v", getErr)
	} else if found {
		t.Fatal("operation survived failed ledger transaction")
	}
	if revisions, listErr := store.ListAgentRevisions(ctx, "edge-1"); listErr != nil {
		t.Fatalf("ListAgentRevisions() error = %v", listErr)
	} else if len(revisions) != 0 {
		t.Fatalf("revisions survived failed transaction: %+v", revisions)
	}
}

func TestIntegrationBootstrapRevisionLedgerIsIdempotentAcrossFileReopen(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dataRoot := t.TempDir()
	config := StoreConfig{Driver: "sqlite", DataRoot: dataRoot, LocalAgentID: "local"}

	store, err := NewStore(config)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	clearRevisionLedgerForTest(t, store)
	if err := store.SaveAgent(ctx, AgentRow{ID: "edge-reopen", Name: "edge-reopen", DesiredRevision: 5, CurrentRevision: 2}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close(first) error = %v", err)
	}

	reopened, err := NewStore(config)
	if err != nil {
		t.Fatalf("NewStore(reopened) error = %v", err)
	}
	pointer, found, err := reopened.GetAgentRevisionPointer(ctx, "edge-reopen")
	if err != nil {
		t.Fatalf("GetAgentRevisionPointer(reopened) error = %v", err)
	}
	if !found || pointer.DesiredRevision != 5 || pointer.AppliedRevision != 2 {
		t.Fatalf("reopened pointer = %+v, found = %v", pointer, found)
	}
	before, err := reopened.ListAgentRevisions(ctx, "edge-reopen")
	if err != nil {
		t.Fatalf("ListAgentRevisions(reopened) error = %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatalf("Close(reopened) error = %v", err)
	}

	again, err := NewStore(config)
	if err != nil {
		t.Fatalf("NewStore(again) error = %v", err)
	}
	t.Cleanup(func() { _ = again.Close() })
	after, err := again.ListAgentRevisions(ctx, "edge-reopen")
	if err != nil {
		t.Fatalf("ListAgentRevisions(again) error = %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("revisions after second reopen = %d, want %d", len(after), len(before))
	}
}

func clearRevisionLedgerForTest(t *testing.T, store *GormStore) {
	t.Helper()
	for _, model := range []any{
		&AgentRevisionArtifactRow{},
		&AgentRevisionAttemptRow{},
		&AgentGenerationRow{},
		&RevisionEventRow{},
		&IdempotencyRecordRow{},
		&AgentRevisionPointerRow{},
		&AgentRevisionRow{},
		&GenerationArtifactRow{},
		&OperationRow{},
	} {
		if err := store.db.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(model).Error; err != nil {
			t.Fatalf("clear %T: %v", model, err)
		}
	}
	if err := store.db.Where("key = ?", revisionLedgerBaselineMarkerKey).Delete(&MetaRow{}).Error; err != nil {
		t.Fatalf("delete baseline marker: %v", err)
	}
}
