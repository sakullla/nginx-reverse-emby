package storage

import (
	"fmt"
	"testing"
	"time"

	"gorm.io/gorm"
)

func TestBootstrapRevisionLedgerCreatesPendingDesiredAndIsIdempotent(t *testing.T) {
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

func TestCreateRevisionLedgerRollsBackAtomically(t *testing.T) {
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

func TestPruneRevisionHistoryPreservesPointersAndDraining(t *testing.T) {
	ctx := t.Context()
	store := newTrafficTestStore(t, true)
	clearRevisionLedgerForTest(t, store)
	now := time.Date(2026, 7, 12, 4, 0, 0, 0, time.UTC)
	old := now.Add(-45 * 24 * time.Hour)
	recent := now.Add(-24 * time.Hour)

	for revision := int64(1); revision <= 6; revision++ {
		createdAt := old
		if revision >= 4 {
			createdAt = recent
		}
		artifactID := fmt.Sprintf("artifact-%d", revision)
		if err := store.db.Create(&GenerationArtifactRow{
			ID:        artifactID,
			Kind:      "snapshot",
			SHA256:    artifactID,
			Payload:   []byte{byte(revision)},
			SizeBytes: 1,
			CreatedAt: createdAt,
		}).Error; err != nil {
			t.Fatalf("create artifact %d: %v", revision, err)
		}
		if err := store.db.Create(&AgentRevisionRow{
			AgentID:            "edge-1",
			Revision:           revision,
			OperationID:        "op-prune",
			State:              AgentRevisionStateApplied,
			SnapshotArtifactID: artifactID,
			SnapshotDigest:     artifactID,
			CreatedAt:          createdAt,
			UpdatedAt:          createdAt,
		}).Error; err != nil {
			t.Fatalf("create revision %d: %v", revision, err)
		}
		if err := store.db.Create(&AgentRevisionArtifactRow{
			AgentID:    "edge-1",
			Revision:   revision,
			ArtifactID: artifactID,
			Role:       "snapshot",
			CreatedAt:  createdAt,
		}).Error; err != nil {
			t.Fatalf("create artifact ref %d: %v", revision, err)
		}
	}
	if err := store.db.Create(&AgentRevisionPointerRow{
		AgentID:               "edge-1",
		DesiredRevision:       6,
		AppliedRevision:       3,
		LastKnownGoodRevision: 3,
		UpdatedAt:             now,
	}).Error; err != nil {
		t.Fatalf("create pointer: %v", err)
	}
	if err := store.db.Create(&AgentGenerationRow{
		AgentID:      "edge-1",
		GenerationID: "generation-2",
		Revision:     2,
		State:        GenerationStateDraining,
		CreatedAt:    old,
		UpdatedAt:    now,
	}).Error; err != nil {
		t.Fatalf("create draining generation: %v", err)
	}
	if err := store.db.Create(&IdempotencyRecordRow{
		Scope:              "panel",
		Key:                "expired",
		RequestFingerprint: "request-1",
		OperationID:        "op-prune",
		CreatedAt:          old,
		ExpiresAt:          now.Add(-time.Minute),
	}).Error; err != nil {
		t.Fatalf("create expired idempotency record: %v", err)
	}
	if err := store.db.Create(&IdempotencyRecordRow{
		Scope:              "panel",
		Key:                "live",
		RequestFingerprint: "request-2",
		OperationID:        "op-prune",
		CreatedAt:          recent,
		ExpiresAt:          now.Add(time.Hour),
	}).Error; err != nil {
		t.Fatalf("create live idempotency record: %v", err)
	}

	result, err := store.PruneRevisionHistory(ctx, RevisionRetentionPolicy{
		Now:         now,
		MaxAge:      30 * 24 * time.Hour,
		MaxPerAgent: 3,
	})
	if err != nil {
		t.Fatalf("PruneRevisionHistory() error = %v", err)
	}
	if result.RevisionsDeleted != 1 || result.ArtifactsDeleted != 1 || result.IdempotencyRecordsDeleted != 1 {
		t.Fatalf("prune result = %+v", result)
	}

	revisions, err := store.ListAgentRevisions(ctx, "edge-1")
	if err != nil {
		t.Fatalf("ListAgentRevisions() error = %v", err)
	}
	want := []int64{2, 3, 4, 5, 6}
	if len(revisions) != len(want) {
		t.Fatalf("revisions = %+v, want %v", revisions, want)
	}
	for i, revision := range revisions {
		if revision.Revision != want[i] {
			t.Fatalf("revisions[%d].Revision = %d, want %d", i, revision.Revision, want[i])
		}
	}
	if _, found, err := store.GetGenerationArtifact(ctx, "artifact-1"); err != nil {
		t.Fatalf("GetGenerationArtifact(artifact-1) error = %v", err)
	} else if found {
		t.Fatal("unreferenced artifact-1 was not pruned")
	}
	if _, found, err := store.GetGenerationArtifact(ctx, "artifact-2"); err != nil {
		t.Fatalf("GetGenerationArtifact(artifact-2) error = %v", err)
	} else if !found {
		t.Fatal("draining artifact-2 was pruned")
	}
	var idempotencyKeys []string
	if err := store.db.Model(&IdempotencyRecordRow{}).Order("key").Pluck("key", &idempotencyKeys).Error; err != nil {
		t.Fatalf("list idempotency keys: %v", err)
	}
	if len(idempotencyKeys) != 1 || idempotencyKeys[0] != "live" {
		t.Fatalf("idempotency keys = %v, want [live]", idempotencyKeys)
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
