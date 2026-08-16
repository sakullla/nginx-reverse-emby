//go:build integration

package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestIntegrationLoadCoordinatorRuntimeSnapshotPreservesDispatchedRevisionIdentity(t *testing.T) {
	t.Parallel()
	store := newTrafficTestStore(t, true)
	now := time.Date(2026, 7, 21, 9, 30, 0, 0, time.UTC)
	testCases := []struct {
		name        string
		revision    AgentRevisionRow
		attempts    []AgentRevisionAttemptRow
		generations []AgentGenerationRow
	}{
		{
			name:     "leased",
			revision: AgentRevisionRow{State: AgentRevisionStatePending},
			attempts: []AgentRevisionAttemptRow{{
				RetryCycle: 0, Attempt: 1, LeaseID: "lease-preserve-leased",
				State: AgentRevisionAttemptStateLeased, StartedAt: now, DeadlineAt: now.Add(time.Minute),
			}},
		},
		{
			name: "started",
			revision: AgentRevisionRow{
				State: AgentRevisionStateApplying, AttemptCount: 1, GenerationID: "generation-preserve-started",
			},
			attempts: []AgentRevisionAttemptRow{{
				RetryCycle: 0, Attempt: 1, LeaseID: "lease-preserve-started",
				State: AgentRevisionAttemptStateStarted, StartedAt: now, DeadlineAt: now.Add(time.Minute),
			}},
		},
		{
			name: "applied",
			revision: AgentRevisionRow{
				State: AgentRevisionStateApplied, AttemptCount: 1, GenerationID: "generation-preserve-applied",
			},
			attempts: []AgentRevisionAttemptRow{{
				RetryCycle: 0, Attempt: 1, LeaseID: "lease-preserve-applied",
				State: AgentRevisionAttemptStateApplied, StartedAt: now, DeadlineAt: now.Add(time.Minute),
			}},
			generations: []AgentGenerationRow{{
				GenerationID: "generation-preserve-applied", State: GenerationStateActive,
				CreatedAt: now, UpdatedAt: now,
			}},
		},
		{
			name:     "generated",
			revision: AgentRevisionRow{State: AgentRevisionStatePending},
			generations: []AgentGenerationRow{{
				GenerationID: "generation-preserve-generated", State: GenerationStateActive,
				CreatedAt: now, UpdatedAt: now,
			}},
		},
	}

	for index, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			agentID := "edge-preserve-" + testCase.name
			operationID := "operation-preserve-" + testCase.name
			payload, err := json.Marshal(Snapshot{
				DesiredVersion: fmt.Sprintf("v%d", index+1), Revision: 1,
				RelayListeners: []RelayListener{{
					ID: index + 1, AgentID: agentID, Enabled: true, TransportMode: "unsupported",
				}},
			})
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}
			sum := sha256.Sum256(payload)
			digest := hex.EncodeToString(sum[:])
			artifactID := "artifact-preserve-" + testCase.name
			revision := testCase.revision
			revision.AgentID = agentID
			revision.Revision = 1
			revision.OperationID = operationID
			revision.SnapshotArtifactID = artifactID
			revision.SnapshotDigest = digest
			revision.ApplyTimeoutSeconds = 60
			revision.DrainTimeoutSeconds = 600
			revision.CreatedAt = now
			revision.UpdatedAt = now
			attempts := append([]AgentRevisionAttemptRow(nil), testCase.attempts...)
			for i := range attempts {
				attempts[i].AgentID = agentID
				attempts[i].Revision = 1
			}
			generations := append([]AgentGenerationRow(nil), testCase.generations...)
			for i := range generations {
				generations[i].AgentID = agentID
				generations[i].Revision = 1
			}
			pointer := AgentRevisionPointerRow{AgentID: agentID, DesiredRevision: 1, UpdatedAt: now}
			if revision.State == AgentRevisionStateApplied {
				appliedAt := now
				revision.AppliedAt = &appliedAt
				pointer.AppliedRevision = 1
				pointer.LastKnownGoodRevision = 1
			}
			if err := store.CreateRevisionLedger(t.Context(), RevisionLedgerWrite{
				Operation: OperationRow{
					ID: operationID, Kind: "test.preserve", Status: OperationStatusPending,
					PrimaryAgentID: agentID, CreatedAt: now, UpdatedAt: now,
				},
				Artifacts: []GenerationArtifactRow{{
					ID: artifactID, Kind: "agent_snapshot", SHA256: digest,
					Payload: payload, SizeBytes: int64(len(payload)), CreatedAt: now,
				}},
				Revisions: []AgentRevisionRow{revision}, Pointers: []AgentRevisionPointerRow{pointer},
				Attempts: attempts, Generations: generations,
			}); err != nil {
				t.Fatalf("CreateRevisionLedger() error = %v", err)
			}

			runtimeSnapshot, found, err := store.LoadCoordinatorRuntimeSnapshot(t.Context(), agentID, 1)
			if err != nil || !found || !runtimeSnapshot.Normalized || !runtimeSnapshot.RequiresNewRevision {
				t.Fatalf("LoadCoordinatorRuntimeSnapshot() = %+v found=%v error=%v", runtimeSnapshot, found, err)
			}
			if len(runtimeSnapshot.Snapshot.RelayListeners) != 0 {
				t.Fatalf("runtime snapshot = %+v, want filtered payload", runtimeSnapshot.Snapshot)
			}
			stored, found, err := store.GetCoordinatorRevision(t.Context(), agentID, 1)
			if err != nil || !found || stored.SnapshotArtifactID != artifactID || stored.SnapshotDigest != digest {
				t.Fatalf("stored revision = %+v found=%v error=%v", stored, found, err)
			}
			artifact, found, err := store.GetGenerationArtifact(t.Context(), artifactID)
			if err != nil || !found || string(artifact.Payload) != string(payload) {
				t.Fatalf("stored artifact = %+v found=%v error=%v", artifact, found, err)
			}
		})
	}
}

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

func TestIntegrationPruneRevisionHistoryPreservesPointersAndDraining(t *testing.T) {
	t.Parallel()
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

func TestIntegrationCreateRevisionLedgerRejectsConcurrentStalePointers(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	store := newTrafficTestStore(t, true)
	clearRevisionLedgerForTest(t, store)
	now := time.Date(2026, 7, 12, 4, 0, 0, 0, time.UTC)

	if err := store.CreateRevisionLedger(ctx, revisionLedgerWriteForPointer("op-current", "edge-1", 10, now)); err != nil {
		t.Fatalf("CreateRevisionLedger(current) error = %v", err)
	}

	const staleWrites = 8
	errors := make(chan error, staleWrites)
	var workers sync.WaitGroup
	for revision := int64(1); revision <= staleWrites; revision++ {
		revision := revision
		workers.Add(1)
		go func() {
			defer workers.Done()
			errors <- store.CreateRevisionLedger(ctx, revisionLedgerWriteForPointer(
				fmt.Sprintf("op-stale-%d", revision),
				"edge-1",
				revision,
				now.Add(time.Duration(revision)*time.Second),
			))
		}()
	}
	workers.Wait()
	close(errors)
	for err := range errors {
		if err == nil {
			t.Fatal("CreateRevisionLedger(stale) error = nil")
		}
	}

	pointer, found, err := store.GetAgentRevisionPointer(ctx, "edge-1")
	if err != nil {
		t.Fatalf("GetAgentRevisionPointer() error = %v", err)
	}
	if !found || pointer.DesiredRevision != 10 || pointer.AppliedRevision != 10 || pointer.LastKnownGoodRevision != 10 {
		t.Fatalf("pointer = %+v, found = %v", pointer, found)
	}
	revisions, err := store.ListAgentRevisions(ctx, "edge-1")
	if err != nil {
		t.Fatalf("ListAgentRevisions() error = %v", err)
	}
	if len(revisions) != 1 || revisions[0].Revision != 10 {
		t.Fatalf("revisions = %+v, want only revision 10", revisions)
	}
}

func TestIntegrationCreateRevisionLedgerRequiresAndReferencesSnapshotArtifact(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	store := newTrafficTestStore(t, true)
	clearRevisionLedgerForTest(t, store)
	now := time.Date(2026, 7, 12, 4, 0, 0, 0, time.UTC)

	missingErr := store.CreateRevisionLedger(ctx, RevisionLedgerWrite{
		Operation: OperationRow{ID: "op-missing-artifact", Kind: "test", Status: OperationStatusPending, CreatedAt: now, UpdatedAt: now},
		Revisions: []AgentRevisionRow{{
			AgentID: "edge-1", Revision: 1, State: AgentRevisionStatePending,
			SnapshotArtifactID: "missing", SnapshotDigest: "missing",
			CreatedAt: now, UpdatedAt: now,
		}},
	})
	if missingErr == nil {
		t.Fatal("CreateRevisionLedger(missing artifact) error = nil")
	}
	if _, found, err := store.GetOperation(ctx, "op-missing-artifact"); err != nil {
		t.Fatalf("GetOperation(missing artifact) error = %v", err)
	} else if found {
		t.Fatal("operation survived missing artifact rollback")
	}

	artifact := generationArtifactForTest("snapshot-edge-1-2", []byte(`{"revision":2}`), now)
	if err := store.CreateRevisionLedger(ctx, RevisionLedgerWrite{
		Operation: OperationRow{ID: "op-with-artifact", Kind: "test", Status: OperationStatusPending, CreatedAt: now, UpdatedAt: now},
		Artifacts: []GenerationArtifactRow{artifact},
		Revisions: []AgentRevisionRow{{
			AgentID: "edge-1", Revision: 2, State: AgentRevisionStatePending,
			SnapshotArtifactID: artifact.ID, SnapshotDigest: artifact.SHA256,
			CreatedAt: now, UpdatedAt: now,
		}},
		Pointers: []AgentRevisionPointerRow{{
			AgentID: "edge-1", DesiredRevision: 2, UpdatedAt: now,
		}},
	}); err != nil {
		t.Fatalf("CreateRevisionLedger(with artifact) error = %v", err)
	}

	var refs []AgentRevisionArtifactRow
	if err := store.db.Where("agent_id = ? AND revision = ? AND role = ?", "edge-1", 2, "snapshot").Find(&refs).Error; err != nil {
		t.Fatalf("list canonical snapshot refs: %v", err)
	}
	if len(refs) != 1 || refs[0].ArtifactID != artifact.ID {
		t.Fatalf("canonical snapshot refs = %+v", refs)
	}

	if _, err := store.PruneRevisionHistory(ctx, RevisionRetentionPolicy{
		Now:         now.Add(60 * 24 * time.Hour),
		MaxAge:      24 * time.Hour,
		MaxPerAgent: 1,
	}); err != nil {
		t.Fatalf("PruneRevisionHistory() error = %v", err)
	}
	if _, found, err := store.GetGenerationArtifact(ctx, artifact.ID); err != nil {
		t.Fatalf("GetGenerationArtifact() error = %v", err)
	} else if !found {
		t.Fatal("pointer-pinned snapshot artifact was pruned")
	}
}

func TestIntegrationPruneRevisionHistoryHandlesEmptyLedgerAndOrphanArtifacts(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	store := newTrafficTestStore(t, true)
	clearRevisionLedgerForTest(t, store)
	now := time.Date(2026, 7, 12, 4, 0, 0, 0, time.UTC)

	if _, err := store.PruneRevisionHistory(ctx, RevisionRetentionPolicy{Now: now}); err != nil {
		t.Fatalf("PruneRevisionHistory(empty) error = %v", err)
	}

	orphan := generationArtifactForTest("orphan-artifact", []byte("orphan"), now.Add(-time.Hour))
	if err := store.db.Create(&orphan).Error; err != nil {
		t.Fatalf("create orphan artifact: %v", err)
	}
	if err := store.db.Create(&IdempotencyRecordRow{
		Scope: "panel", Key: "expired", RequestFingerprint: "request", OperationID: "missing-operation",
		CreatedAt: now.Add(-time.Hour), ExpiresAt: now.Add(-time.Minute),
	}).Error; err != nil {
		t.Fatalf("create expired idempotency record: %v", err)
	}

	result, err := store.PruneRevisionHistory(ctx, RevisionRetentionPolicy{Now: now})
	if err != nil {
		t.Fatalf("PruneRevisionHistory(orphan) error = %v", err)
	}
	if result.ArtifactsDeleted != 1 || result.IdempotencyRecordsDeleted != 1 {
		t.Fatalf("prune result = %+v", result)
	}
}

func TestIntegrationCreateRevisionLedgerPreservesAttemptHistory(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	store := newTrafficTestStore(t, true)
	clearRevisionLedgerForTest(t, store)
	now := time.Date(2026, 7, 12, 4, 0, 0, 0, time.UTC)
	finishedAt := now.Add(time.Second)

	if err := store.CreateRevisionLedger(ctx, RevisionLedgerWrite{
		Operation: OperationRow{ID: "op-attempts", Kind: "test", Status: OperationStatusPending, CreatedAt: now, UpdatedAt: now},
		Revisions: []AgentRevisionRow{{AgentID: "edge-1", Revision: 4, State: AgentRevisionStatePending, CreatedAt: now, UpdatedAt: now}},
		Pointers:  []AgentRevisionPointerRow{{AgentID: "edge-1", DesiredRevision: 4, UpdatedAt: now}},
		Attempts: []AgentRevisionAttemptRow{
			{AgentID: "edge-1", Revision: 4, RetryCycle: 0, Attempt: 1, LeaseID: "lease-1", State: "failed", StartedAt: now, DeadlineAt: now.Add(time.Minute), FinishedAt: &finishedAt, Error: "prepare failed"},
			{AgentID: "edge-1", Revision: 4, RetryCycle: 0, Attempt: 2, LeaseID: "lease-2", State: "started", StartedAt: now.Add(2 * time.Second), DeadlineAt: now.Add(time.Minute)},
		},
	}); err != nil {
		t.Fatalf("CreateRevisionLedger() error = %v", err)
	}

	var attempts []AgentRevisionAttemptRow
	if err := store.db.Where("agent_id = ? AND revision = ?", "edge-1", 4).Order("attempt").Find(&attempts).Error; err != nil {
		t.Fatalf("list attempts: %v", err)
	}
	if len(attempts) != 2 || attempts[0].LeaseID != "lease-1" || attempts[1].LeaseID != "lease-2" {
		t.Fatalf("attempts = %+v", attempts)
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

func TestIntegrationBootstrapSchemaFailurePreservesLegacyData(t *testing.T) {
	t.Parallel()
	requireStorageIntegration(t)
	dbPath := filepath.Join(t.TempDir(), "legacy.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open() error = %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB() error = %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.Exec("CREATE TABLE legacy_guard (id INTEGER PRIMARY KEY, value TEXT NOT NULL)").Error; err != nil {
		t.Fatalf("create legacy table: %v", err)
	}
	if err := db.Exec("INSERT INTO legacy_guard (id, value) VALUES (1, 'preserve-me')").Error; err != nil {
		t.Fatalf("seed legacy table: %v", err)
	}
	if err := db.Exec("CREATE TABLE operations (id TEXT PRIMARY KEY)").Error; err != nil {
		t.Fatalf("create incompatible operations table: %v", err)
	}
	if err := db.Exec("INSERT INTO operations (id) VALUES ('legacy-operation')").Error; err != nil {
		t.Fatalf("seed incompatible operations table: %v", err)
	}

	if err := BootstrapSchema(t.Context(), db, SchemaOptionsForDriver("sqlite", false)); err == nil {
		t.Fatal("BootstrapSchema() error = nil, want incompatible migration error")
	}
	var value string
	if err := db.Raw("SELECT value FROM legacy_guard WHERE id = 1").Scan(&value).Error; err != nil {
		t.Fatalf("read legacy table after failed migration: %v", err)
	}
	if value != "preserve-me" {
		t.Fatalf("legacy value = %q, want preserve-me", value)
	}
}

func revisionLedgerWriteForPointer(operationID, agentID string, revision int64, now time.Time) RevisionLedgerWrite {
	return RevisionLedgerWrite{
		Operation: OperationRow{ID: operationID, Kind: "test", Status: OperationStatusPending, CreatedAt: now, UpdatedAt: now},
		Revisions: []AgentRevisionRow{{AgentID: agentID, Revision: revision, State: AgentRevisionStateApplied, CreatedAt: now, UpdatedAt: now}},
		Pointers: []AgentRevisionPointerRow{{
			AgentID: agentID, DesiredRevision: revision, AppliedRevision: revision, LastKnownGoodRevision: revision, UpdatedAt: now,
		}},
	}
}

func generationArtifactForTest(id string, payload []byte, createdAt time.Time) GenerationArtifactRow {
	digest := sha256.Sum256(payload)
	return GenerationArtifactRow{
		ID: id, Kind: "agent_snapshot", SHA256: hex.EncodeToString(digest[:]),
		Payload: payload, SizeBytes: int64(len(payload)), CreatedAt: createdAt,
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

func TestIntegrationPruneRevisionHistoryDeletesOnlyExpiredOrphanOperations(t *testing.T) {
	t.Parallel()
	store, err := newStorageTestSQLiteStore(t, t.TempDir(), "local", true)
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

func TestIntegrationPruneRevisionHistoryBoundsNewOperationalTables(t *testing.T) {
	t.Parallel()
	store, err := newStorageTestSQLiteStore(t, t.TempDir(), "local", true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	old := now.Add(-120 * 24 * time.Hour)
	veryOld := now.Add(-400 * 24 * time.Hour)
	recent := now.Add(-24 * time.Hour)
	completedOld := old
	completedRecent := recent
	rows := []any{
		&SessionRow{ID: "expired-session", TokenHash: "expired-token", UserID: "user", CreatedAt: old, ExpiresAt: old, LastSeen: old},
		&SessionRow{ID: "recent-session", TokenHash: "recent-token", UserID: "user", CreatedAt: recent, ExpiresAt: now.Add(24 * time.Hour), LastSeen: recent},
		&AuditEventRow{ID: "audit-old", Action: "plugin.test", Result: "success", MetadataJSON: "{}", CreatedAt: veryOld},
		&AuditEventRow{ID: "audit-recent", Action: "plugin.test", Result: "success", MetadataJSON: "{}", CreatedAt: old},
		&MarketplaceRefreshOperationRow{ID: "refresh-old", SourceID: "source", Status: "succeeded", DiffJSON: "{}", Error: "", StartedAt: old, FinishedAt: &completedOld},
		&MarketplaceRefreshOperationRow{ID: "refresh-recent", SourceID: "source", Status: "succeeded", DiffJSON: "{}", Error: "", StartedAt: recent, FinishedAt: &completedRecent},
		&PluginOperationRow{ID: "plugin-op-old", PluginID: "plugin", Kind: "configure", Status: "succeeded", AgentResultsJSON: "{}", Error: "", ActorID: "admin", CreatedAt: old, CompletedAt: &completedOld},
		&PluginOperationRow{ID: "plugin-op-active", PluginID: "plugin", Kind: "enable", Status: "succeeded", AgentResultsJSON: "{}", Error: "", ActorID: "admin", CreatedAt: old, CompletedAt: &completedOld},
		&PluginOperationScopeRow{OperationID: "plugin-op-old", InstanceID: "instance-old", PluginID: "plugin", ResourceGroupID: "default", CreatedAt: old},
		&PluginOperationSecretRow{OperationID: "plugin-op-old", SecretID: "secret-old", InstanceID: "instance-old", Role: "retired", State: "retired", CreatedAt: old, RetiredAt: &completedOld},
		&PluginAgentRuntimeStatusRow{OperationID: "plugin-op-old", AgentID: "edge", InstanceID: "instance-old", PluginID: "plugin", GenerationID: "generation-old", State: "stopped", AuthoritySlot: "retired", UpdatedAt: old},
		&PluginAgentRuntimeStatusRow{OperationID: "plugin-op-old", AgentID: "edge-two", InstanceID: "instance-draining", PluginID: "plugin", GenerationID: "generation-draining", State: "draining", AuthoritySlot: "pending", UpdatedAt: old},
		&PluginAgentRuntimeStatusRow{OperationID: "plugin-op-active", AgentID: "edge", InstanceID: "instance-active", PluginID: "plugin", GenerationID: "generation-active", State: "active", AuthoritySlot: "active", UpdatedAt: old},
		&PluginRuntimeLogReportRow{AgentID: "edge", InstanceID: "instance-old", GenerationID: "generation-old", PluginID: "plugin", Sequence: 1, ReportDigest: strings.Repeat("a", 64), UpdatedAt: old},
		&PluginRuntimeLogReportRow{AgentID: "edge-two", InstanceID: "instance-draining", GenerationID: "generation-draining", PluginID: "plugin", Sequence: 1, ReportDigest: strings.Repeat("d", 64), UpdatedAt: old},
		&PluginRuntimeLogReportRow{AgentID: "edge", InstanceID: "instance-active", GenerationID: "generation-active", PluginID: "plugin", Sequence: 1, ReportDigest: strings.Repeat("b", 64), UpdatedAt: old},
		&PluginRuntimeLogRow{InstanceID: "instance-old", PluginID: "plugin", AgentID: "edge", ResourceGroupID: "default", Level: "info", Message: "old", CreatedAt: old},
		&PluginRuntimeLogRow{InstanceID: "instance-active", PluginID: "plugin", AgentID: "edge", ResourceGroupID: "default", Level: "info", Message: "recent", CreatedAt: recent},
		&PluginControlPlaneLogOutboxRow{EventID: "event-old", InstanceID: "instance-old", PluginID: "plugin", OperationID: "plugin-op-old", GenerationID: "generation-old", ResourceGroupID: "default", Level: "info", Message: "old", CreatedAt: old},
		&SecretRow{ID: "secret-old", Name: "secret-old", Purpose: "plugin-config:instance-old:value", ResourceGroupID: "default", ActiveVersion: 1, Fingerprint: "fingerprint", CreatedAt: old, RotatedAt: old, RetiredAt: &completedOld},
		&SecretVersionRow{SecretID: "secret-old", Version: 1, KeyID: "key", Nonce: []byte{}, Ciphertext: []byte{}, CreatedAt: old, DestroyedAt: &completedOld},
		&PluginDigestFenceRow{Digest: strings.Repeat("c", 64), UpdatedAt: old},
	}
	for _, row := range rows {
		if err := store.db.Create(row).Error; err != nil {
			t.Fatalf("create %T: %v", row, err)
		}
	}

	result, err := store.PruneRevisionHistory(t.Context(), RevisionRetentionPolicy{Now: now, MaxAge: 30 * 24 * time.Hour, OperationMaxAge: 90 * 24 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if result.SessionsDeleted != 1 || result.AuditEventsDeleted != 1 || result.MarketplaceOperationsDeleted != 1 || result.PluginOperationsDeleted != 1 || result.PluginRuntimeStatusesDeleted != 2 || result.PluginRuntimeLogsDeleted != 1 || result.PluginRuntimeLogReportsDeleted != 2 || result.PluginLogOutboxDeleted != 1 || result.SecretVersionsDeleted != 1 || result.SecretsDeleted != 1 || result.PluginDigestFencesDeleted != 1 {
		t.Fatalf("prune result = %+v", result)
	}

	for _, check := range []struct {
		model any
		where string
		args  []any
		want  int64
	}{
		{&SessionRow{}, "id = ?", []any{"recent-session"}, 1},
		{&AuditEventRow{}, "id = ?", []any{"audit-recent"}, 1},
		{&MarketplaceRefreshOperationRow{}, "id = ?", []any{"refresh-recent"}, 1},
		{&PluginOperationRow{}, "id = ?", []any{"plugin-op-active"}, 1},
		{&PluginOperationScopeRow{}, "operation_id = ?", []any{"plugin-op-old"}, 0},
		{&PluginOperationSecretRow{}, "operation_id = ?", []any{"plugin-op-old"}, 0},
		{&PluginRuntimeLogReportRow{}, "generation_id = ?", []any{"generation-active"}, 1},
		{&PluginRuntimeLogRow{}, "message = ?", []any{"recent"}, 1},
	} {
		var count int64
		if err := store.db.Model(check.model).Where(check.where, check.args...).Count(&count).Error; err != nil || count != check.want {
			t.Errorf("count %T where %q = %d, want %d (err=%v)", check.model, check.where, count, check.want, err)
		}
	}
}

func TestIntegrationDismissOperationUsesCompletedAtWithoutDedicatedColumn(t *testing.T) {
	t.Parallel()
	store, err := newStorageTestSQLiteStore(t, t.TempDir(), "local", true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if store.db.Migrator().HasColumn(&OperationRow{}, "dismissed_at") {
		t.Fatal("operations schema contains dismissed_at; dismissal must reuse completed_at")
	}
	now := time.Date(2026, 7, 25, 2, 0, 0, 0, time.UTC)
	if err := store.CreateRevisionLedger(t.Context(), RevisionLedgerWrite{
		Operation: OperationRow{
			ID: "op-dismiss", Kind: "test", Status: OperationStatusPending,
			PrimaryAgentID: "edge-test-1", CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
		},
		Revisions: []AgentRevisionRow{{
			AgentID: "edge-test-1", Revision: 1, OperationID: "op-dismiss",
			State: AgentRevisionStatePending, CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
		}},
	}); err != nil {
		t.Fatal(err)
	}

	operation, found, err := store.DismissOperation(t.Context(), "op-dismiss", now)
	if err != nil || !found {
		t.Fatalf("DismissOperation() = %+v, found=%v, error=%v", operation, found, err)
	}
	if operation.CompletedAt == nil || !operation.CompletedAt.Equal(now) || operation.Status != OperationStatusPending {
		t.Fatalf("dismissed operation = %+v, want pending with completed_at %s", operation, now)
	}

	again, found, err := store.DismissOperation(t.Context(), "op-dismiss", now.Add(time.Hour))
	if err != nil || !found || again.CompletedAt == nil || !again.CompletedAt.Equal(now) {
		t.Fatalf("second DismissOperation() = %+v, found=%v, error=%v; want original completed_at", again, found, err)
	}
	if err := store.writeTransaction(t.Context(), func(tx *gorm.DB) error {
		return refreshCoordinatorOperationTx(tx, "op-dismiss", now.Add(2*time.Hour))
	}); err != nil {
		t.Fatal(err)
	}
	afterRefresh, found, err := store.GetOperation(t.Context(), "op-dismiss")
	if err != nil || !found || afterRefresh.CompletedAt == nil || !afterRefresh.CompletedAt.Equal(now) {
		t.Fatalf("operation after coordinator refresh = %+v, found=%v, error=%v; want completed_at preserved", afterRefresh, found, err)
	}
	revisions, err := store.ListOperationRevisions(t.Context(), "op-dismiss")
	if err != nil || len(revisions) != 1 || revisions[0].State != AgentRevisionStatePending {
		t.Fatalf("revisions after dismiss = %+v, error=%v; want execution state unchanged", revisions, err)
	}
}
