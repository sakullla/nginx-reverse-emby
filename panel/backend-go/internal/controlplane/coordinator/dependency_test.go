package coordinator

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/dependency"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestCoordinatorRebuildsDependencyPlanFromPersistedRevisionArtifacts(t *testing.T) {
	now := time.Date(2026, 7, 12, 20, 0, 0, 0, time.UTC)
	store := newCoordinatorTestStore(t)
	seedDependencyOperation(t, store, now)
	coord := newTestCoordinator(t, store, now, 0.5)
	nodes := []dependency.Node{{AgentID: "edge-a", Revision: 1}, {AgentID: "edge-b", Revision: 1}}

	plan, err := coord.RebuildDependencyPlan(t.Context(), "operation-dependency", dependency.ActionApply, nodes)
	if err != nil {
		t.Fatalf("RebuildDependencyPlan() error = %v", err)
	}
	evaluation, err := coord.EvaluateDependencyPlan(t.Context(), plan)
	if err != nil {
		t.Fatalf("EvaluateDependencyPlan() error = %v", err)
	}
	if len(evaluation.Frontier) != 1 || evaluation.Frontier[0].AgentID != "edge-b" {
		t.Fatalf("initial frontier = %+v, want edge-b", evaluation.Frontier)
	}

	lease := mustClaim(t, coord, "edge-b")
	if _, err := coord.Start(t.Context(), StartRequest{Lease: lease, GenerationID: "generation-b"}); err != nil {
		t.Fatalf("Start(edge-b) error = %v", err)
	}
	if _, err := coord.Applied(t.Context(), AppliedReport{Lease: lease, GenerationID: "generation-b"}); err != nil {
		t.Fatalf("Applied(edge-b) error = %v", err)
	}
	evaluation, err = coord.EvaluateDependencyPlan(t.Context(), plan)
	if err != nil {
		t.Fatalf("EvaluateDependencyPlan(after edge-b) error = %v", err)
	}
	if len(evaluation.Frontier) != 1 || evaluation.Frontier[0].AgentID != "edge-a" {
		t.Fatalf("released frontier = %+v, want edge-a", evaluation.Frontier)
	}
}

func TestCoordinatorRebuildsDeletePlanFromPreviousSnapshots(t *testing.T) {
	now := time.Date(2026, 7, 12, 21, 0, 0, 0, time.UTC)
	store := newCoordinatorTestStore(t)
	seedDependencyDeleteOperation(t, store, now)
	coord := newTestCoordinator(t, store, now, 0.5)
	nodes := []dependency.Node{{AgentID: "edge-a", Revision: 2}, {AgentID: "edge-b", Revision: 2}}

	plan, err := coord.RebuildDependencyPlan(t.Context(), "operation-dependency-delete", dependency.ActionDelete, nodes)
	if err != nil {
		t.Fatalf("RebuildDependencyPlan(delete) error = %v", err)
	}
	evaluation, err := coord.EvaluateDependencyPlan(t.Context(), plan)
	if err != nil {
		t.Fatalf("EvaluateDependencyPlan(delete) error = %v", err)
	}
	if len(evaluation.Frontier) != 1 || evaluation.Frontier[0].AgentID != "edge-a" {
		t.Fatalf("delete frontier = %+v, want caller edge-a before dependency edge-b", evaluation.Frontier)
	}
}

func TestCoordinatorRebuildsDeletePlanFromZeroBaselineSnapshot(t *testing.T) {
	now := time.Date(2026, 7, 12, 21, 30, 0, 0, time.UTC)
	store := newCoordinatorTestStore(t)
	createDependencyLedger(t, store, now, "operation-dependency-zero-baseline", 0, map[string]storage.Snapshot{
		"edge-a": {
			Revision: 0,
			Rules:    []storage.HTTPRule{{ID: 1, AgentID: "edge-a", RelayLayers: [][]int{{10}}}},
		},
		"edge-b": {
			Revision:       0,
			RelayListeners: []storage.RelayListener{{ID: 10, AgentID: "edge-b", Enabled: true}},
		},
	}, 0)
	createDependencyLedger(t, store, now.Add(time.Second), "operation-dependency-delete-zero-baseline", 1, map[string]storage.Snapshot{
		"edge-a": {Revision: 1},
		"edge-b": {Revision: 1},
	}, 0)
	coord := newTestCoordinator(t, store, now, 0.5)

	plan, err := coord.RebuildDependencyPlan(t.Context(), "operation-dependency-delete-zero-baseline", dependency.ActionDelete,
		[]dependency.Node{{AgentID: "edge-a", Revision: 1}, {AgentID: "edge-b", Revision: 1}},
	)
	if err != nil {
		t.Fatalf("RebuildDependencyPlan(delete zero baseline) error = %v", err)
	}
	evaluation, err := coord.EvaluateDependencyPlan(t.Context(), plan)
	if err != nil {
		t.Fatalf("EvaluateDependencyPlan(delete zero baseline) error = %v", err)
	}
	if len(evaluation.Frontier) != 1 || evaluation.Frontier[0].AgentID != "edge-a" {
		t.Fatalf("delete zero-baseline frontier = %+v, want edge-a", evaluation.Frontier)
	}
}

func TestCoordinatorEvaluationPreservesSupersededRevisionFacts(t *testing.T) {
	now := time.Date(2026, 7, 12, 22, 0, 0, 0, time.UTC)
	store := newCoordinatorTestStore(t)
	seedDependencyOperation(t, store, now)
	coord := newTestCoordinator(t, store, now, 0.5)
	nodes := []dependency.Node{{AgentID: "edge-a", Revision: 1}, {AgentID: "edge-b", Revision: 1}}
	plan, err := coord.RebuildDependencyPlan(t.Context(), "operation-dependency", dependency.ActionApply, nodes)
	if err != nil {
		t.Fatalf("RebuildDependencyPlan() error = %v", err)
	}

	createDependencyLedger(t, store, now.Add(time.Second), "operation-dependency-newer", 2, map[string]storage.Snapshot{
		"edge-a": {Revision: 2},
		"edge-b": {Revision: 2},
	}, 0)
	for _, agentID := range []string{"edge-a", "edge-b"} {
		lease := mustClaim(t, coord, agentID)
		generationID := "generation-newer-" + agentID
		if _, err := coord.Start(t.Context(), StartRequest{Lease: lease, GenerationID: generationID}); err != nil {
			t.Fatalf("Start(%s) error = %v", agentID, err)
		}
		if _, err := coord.Applied(t.Context(), AppliedReport{Lease: lease, GenerationID: generationID}); err != nil {
			t.Fatalf("Applied(%s) error = %v", agentID, err)
		}
	}

	evaluation, err := coord.EvaluateDependencyPlan(t.Context(), plan)
	if err != nil {
		t.Fatalf("EvaluateDependencyPlan() error = %v", err)
	}
	if evaluation.Status != dependency.StatusSuperseded {
		t.Fatalf("evaluation status = %q, want %q", evaluation.Status, dependency.StatusSuperseded)
	}
	for _, agentID := range []string{"edge-a", "edge-b"} {
		result, ok := evaluation.Result(agentID)
		if !ok || result.State != dependency.StateSuperseded {
			t.Fatalf("result %s = %+v, found %v; want superseded", agentID, result, ok)
		}
	}
}

func seedDependencyOperation(t *testing.T, store *storage.GormStore, now time.Time) {
	t.Helper()
	snapshots := map[string]storage.Snapshot{
		"edge-a": {
			Revision: 1,
			Rules: []storage.HTTPRule{{
				ID: 1, AgentID: "edge-a", RelayLayers: [][]int{{10}},
			}},
		},
		"edge-b": {
			Revision:       1,
			RelayListeners: []storage.RelayListener{{ID: 10, AgentID: "edge-b", Enabled: true}},
		},
	}
	ledger := storage.RevisionLedgerWrite{
		Operation: storage.OperationRow{
			ID: "operation-dependency", Kind: "test_dependency", Status: storage.OperationStatusPending,
			PrimaryAgentID: "edge-a", CreatedAt: now, UpdatedAt: now,
		},
	}
	for _, agentID := range []string{"edge-a", "edge-b"} {
		payload, err := json.Marshal(snapshots[agentID])
		if err != nil {
			t.Fatalf("marshal %s snapshot: %v", agentID, err)
		}
		digestBytes := sha256.Sum256(payload)
		digest := hex.EncodeToString(digestBytes[:])
		artifactID := "snapshot-dependency-" + agentID
		ledger.Artifacts = append(ledger.Artifacts, storage.GenerationArtifactRow{
			ID: artifactID, Kind: "agent_snapshot", SHA256: digest,
			Payload: payload, SizeBytes: int64(len(payload)), CreatedAt: now,
		})
		ledger.Revisions = append(ledger.Revisions, storage.AgentRevisionRow{
			AgentID: agentID, Revision: 1, OperationID: ledger.Operation.ID,
			State: storage.AgentRevisionStatePending, SnapshotArtifactID: artifactID, SnapshotDigest: digest,
			ApplyTimeoutSeconds: 60, DrainTimeoutSeconds: 600, CreatedAt: now, UpdatedAt: now,
		})
		ledger.Pointers = append(ledger.Pointers, storage.AgentRevisionPointerRow{
			AgentID: agentID, DesiredRevision: 1, UpdatedAt: now,
		})
		ledger.ArtifactRefs = append(ledger.ArtifactRefs, storage.AgentRevisionArtifactRow{
			AgentID: agentID, Revision: 1, ArtifactID: artifactID, Role: "snapshot", CreatedAt: now,
		})
	}
	if err := store.CreateRevisionLedger(t.Context(), ledger); err != nil {
		t.Fatalf("CreateRevisionLedger(dependency) error = %v", err)
	}
}

func seedDependencyDeleteOperation(t *testing.T, store *storage.GormStore, now time.Time) {
	t.Helper()
	createDependencyLedger(t, store, now, "operation-dependency-before-delete", 1, map[string]storage.Snapshot{
		"edge-a": {
			Revision: 1,
			Rules:    []storage.HTTPRule{{ID: 1, AgentID: "edge-a", RelayLayers: [][]int{{10}}}},
		},
		"edge-b": {
			Revision:       1,
			RelayListeners: []storage.RelayListener{{ID: 10, AgentID: "edge-b", Enabled: true}},
		},
	}, 1)
	createDependencyLedger(t, store, now.Add(time.Second), "operation-dependency-delete", 2, map[string]storage.Snapshot{
		"edge-a": {Revision: 2},
		"edge-b": {Revision: 2},
	}, 1)
}

func createDependencyLedger(t *testing.T, store *storage.GormStore, now time.Time, operationID string, revision int64, snapshots map[string]storage.Snapshot, applied int64) {
	t.Helper()
	ledger := storage.RevisionLedgerWrite{
		Operation: storage.OperationRow{
			ID: operationID, Kind: "test_dependency", Status: storage.OperationStatusPending,
			PrimaryAgentID: "edge-a", CreatedAt: now, UpdatedAt: now,
		},
	}
	for _, agentID := range []string{"edge-a", "edge-b"} {
		payload, err := json.Marshal(snapshots[agentID])
		if err != nil {
			t.Fatalf("marshal %s snapshot: %v", agentID, err)
		}
		digestBytes := sha256.Sum256(payload)
		digest := hex.EncodeToString(digestBytes[:])
		artifactID := "snapshot-" + digest
		ledger.Artifacts = append(ledger.Artifacts, storage.GenerationArtifactRow{
			ID: artifactID, Kind: "agent_snapshot", SHA256: digest,
			Payload: payload, SizeBytes: int64(len(payload)), CreatedAt: now,
		})
		state := storage.AgentRevisionStatePending
		var appliedAt *time.Time
		if revision <= applied {
			state = storage.AgentRevisionStateApplied
			value := now
			appliedAt = &value
		}
		ledger.Revisions = append(ledger.Revisions, storage.AgentRevisionRow{
			AgentID: agentID, Revision: revision, OperationID: operationID,
			State: state, SnapshotArtifactID: artifactID, SnapshotDigest: digest,
			ApplyTimeoutSeconds: 60, DrainTimeoutSeconds: 600,
			CreatedAt: now, UpdatedAt: now, AppliedAt: appliedAt,
		})
		ledger.Pointers = append(ledger.Pointers, storage.AgentRevisionPointerRow{
			AgentID: agentID, DesiredRevision: revision, AppliedRevision: applied,
			LastKnownGoodRevision: applied, UpdatedAt: now,
		})
		ledger.ArtifactRefs = append(ledger.ArtifactRefs, storage.AgentRevisionArtifactRow{
			AgentID: agentID, Revision: revision, ArtifactID: artifactID, Role: "snapshot", CreatedAt: now,
		})
	}
	if err := store.CreateRevisionLedger(t.Context(), ledger); err != nil {
		t.Fatalf("CreateRevisionLedger(%s) error = %v", operationID, err)
	}
}
