package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/coordinator"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/dependency"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/revision"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestRevisionAPIReconstructsDegradedBlockedStatusAndEventCursor(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	now := time.Date(2026, 7, 13, 2, 0, 0, 0, time.UTC)
	seedRevisionOperation(t, store, revisionOperationSeed{
		OperationID: "op-degraded",
		Now:         now,
		States: map[string]string{
			"edge-applied": storage.AgentRevisionStateApplied,
			"edge-failed":  storage.AgentRevisionStateFailed,
			"edge-blocked": storage.AgentRevisionStatePending,
		},
		Edges: []dependency.Edge{{
			FromAgentID: "edge-blocked", ToAgentID: "edge-failed",
			Kind: dependency.EdgeKindRelayLayer, Resource: "listener:7",
		}},
		Events: []storage.RevisionEventRow{
			{AgentID: "edge-applied", Revision: 1, EventType: "revision_applied", PayloadJSON: `{"lease_id":"secret-lease","result":"ok"}`},
			{AgentID: "edge-failed", Revision: 1, EventType: "revision_failed", PayloadJSON: `{"error_code":"prepare_failed"}`},
		},
	})

	api := newRevisionAPITestService(t, store)
	status, err := api.GetOperationStatus(t.Context(), "op-degraded")
	if err != nil {
		t.Fatalf("GetOperationStatus() error = %v", err)
	}
	if status.ApplyStatus != string(dependency.StatusDegraded) || !status.Degraded {
		t.Fatalf("operation status = %+v, want degraded", status)
	}
	blocked := findAgentRevisionStatus(t, status.Agents, "edge-blocked")
	if len(blocked.BlockedBy) != 1 || blocked.BlockedBy[0] != "edge-failed" {
		t.Fatalf("blocked_by = %+v, want edge-failed", blocked.BlockedBy)
	}

	first, err := api.ListEvents(t.Context(), RevisionEventQuery{OperationID: "op-degraded", Limit: 1})
	if err != nil {
		t.Fatalf("ListEvents(first) error = %v", err)
	}
	if len(first.Events) != 1 || !first.HasMore || first.NextCursor == 0 {
		t.Fatalf("first event page = %+v", first)
	}
	if _, exposed := first.Events[0].Payload["lease_id"]; exposed {
		t.Fatalf("event payload exposed lease: %+v", first.Events[0].Payload)
	}
	second, err := api.ListEvents(t.Context(), RevisionEventQuery{
		OperationID: "op-degraded", AfterID: first.NextCursor, Limit: 10,
	})
	if err != nil {
		t.Fatalf("ListEvents(second) error = %v", err)
	}
	if len(second.Events) != 1 || second.Events[0].ID <= first.NextCursor {
		t.Fatalf("second event page = %+v", second)
	}
}

func TestRevisionAPIRemotePullClaimsOnlyCallerFrontierAndRejectsStaleReport(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir(), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	seedRevisionOperation(t, store, revisionOperationSeed{
		OperationID: "op-frontier",
		Now:         time.Now().UTC(),
		States: map[string]string{
			"edge-caller":      storage.AgentRevisionStatePending,
			"edge-dependency":  storage.AgentRevisionStatePending,
			"edge-independent": storage.AgentRevisionStatePending,
		},
		Edges: []dependency.Edge{{
			FromAgentID: "edge-caller", ToAgentID: "edge-dependency",
			Kind: dependency.EdgeKindRelayLayer, Resource: "listener:9",
		}},
	})
	api := newRevisionAPITestService(t, store)

	blocked, err := api.PullRemoteRevision(t.Context(), "edge-caller")
	if err != nil {
		t.Fatalf("PullRemoteRevision(blocked) error = %v", err)
	}
	if blocked.HasUpdate || blocked.Lease != nil {
		t.Fatalf("blocked pull = %+v, want no lease", blocked)
	}
	callerAttempts, err := store.ListCoordinatorAttempts(t.Context(), "edge-caller", 1)
	if err != nil || len(callerAttempts) != 0 {
		t.Fatalf("blocked caller attempts = %+v, error = %v", callerAttempts, err)
	}
	independentAttempts, err := store.ListCoordinatorAttempts(t.Context(), "edge-independent", 1)
	if err != nil || len(independentAttempts) != 0 {
		t.Fatalf("unrelated frontier attempts = %+v, error = %v", independentAttempts, err)
	}
	independentPull, err := api.PullRemoteRevision(t.Context(), "edge-independent")
	if err != nil || independentPull.Lease == nil || !independentPull.HasUpdate {
		t.Fatalf("independent pull = %+v, error = %v", independentPull, err)
	}

	dependencyPull, err := api.PullRemoteRevision(t.Context(), "edge-dependency")
	if err != nil || dependencyPull.Lease == nil || !dependencyPull.HasUpdate {
		t.Fatalf("dependency pull = %+v, error = %v", dependencyPull, err)
	}
	replayedPull, err := api.PullRemoteRevision(t.Context(), "edge-dependency")
	if err != nil || replayedPull.Lease == nil || replayedPull.Lease.LeaseID != dependencyPull.Lease.LeaseID {
		t.Fatalf("replayed dependency pull = %+v, error = %v", replayedPull, err)
	}
	dependencyAttempts, err := store.ListCoordinatorAttempts(t.Context(), "edge-dependency", 1)
	if err != nil || len(dependencyAttempts) != 1 {
		t.Fatalf("dependency attempts after replayed pull = %+v, error=%v", dependencyAttempts, err)
	}
	leasedRevision, found, err := store.GetCoordinatorRevision(t.Context(), "edge-dependency", 1)
	if err != nil || !found || leasedRevision.AttemptCount != 0 {
		t.Fatalf("leased revision = %+v, found=%v error=%v; pull must not consume an attempt", leasedRevision, found, err)
	}
	dependencyStart := RemoteRevisionStart{
		AgentID: "edge-dependency", Revision: dependencyPull.Lease.Revision,
		RetryCycle: dependencyPull.Lease.RetryCycle, Attempt: dependencyPull.Lease.Attempt,
		LeaseID: dependencyPull.Lease.LeaseID, GenerationID: "generation-dependency",
	}
	if _, err := api.StartRemoteRevision(t.Context(), "edge-dependency", dependencyStart); err != nil {
		t.Fatalf("StartRemoteRevision() error = %v", err)
	}
	dependencyReport := RemoteRevisionReport{
		AgentID: "edge-dependency", Revision: dependencyPull.Lease.Revision,
		RetryCycle: dependencyPull.Lease.RetryCycle, Attempt: dependencyPull.Lease.Attempt,
		LeaseID: dependencyPull.Lease.LeaseID, GenerationID: "generation-dependency",
		Status: storage.AgentRevisionStateApplied,
	}
	if _, err := api.ReportRemoteRevision(t.Context(), "edge-dependency", dependencyReport); err != nil {
		t.Fatalf("ReportRemoteRevision(applied) error = %v", err)
	}

	callerPull, err := api.PullRemoteRevision(t.Context(), "edge-caller")
	if err != nil || callerPull.Lease == nil || !callerPull.HasUpdate {
		t.Fatalf("caller pull after dependency = %+v, error = %v", callerPull, err)
	}

	dependencyReport.Status = storage.AgentRevisionStateFailed
	dependencyReport.ErrorCode = "stale"
	if _, err := api.ReportRemoteRevision(t.Context(), "edge-dependency", dependencyReport); !errors.Is(err, coordinator.ErrLeaseConflict) {
		t.Fatalf("stale report error = %v, want lease conflict", err)
	}
	row, found, err := store.GetCoordinatorRevision(t.Context(), "edge-dependency", 1)
	if err != nil || !found || row.State != storage.AgentRevisionStateApplied {
		t.Fatalf("dependency revision after stale report = %+v, found=%v error=%v", row, found, err)
	}
}

type revisionOperationSeed struct {
	OperationID string
	Now         time.Time
	States      map[string]string
	Edges       []dependency.Edge
	Events      []storage.RevisionEventRow
}

func seedRevisionOperation(t *testing.T, store *storage.GormStore, seed revisionOperationSeed) {
	t.Helper()
	agentIDs := make([]string, 0, len(seed.States))
	for agentID := range seed.States {
		agentIDs = append(agentIDs, agentID)
	}
	sortStrings(agentIDs)
	ledger := storage.RevisionLedgerWrite{
		Operation: storage.OperationRow{
			ID: seed.OperationID, Kind: "test.mutation", Status: storage.OperationStatusPending,
			PrimaryAgentID: agentIDs[0], CreatedAt: seed.Now, UpdatedAt: seed.Now,
		},
	}
	nodes := make([]dependency.Node, 0, len(agentIDs))
	for _, agentID := range agentIDs {
		payload, digest, err := revision.CanonicalSnapshotPayload(storage.Snapshot{Revision: 1})
		if err != nil {
			t.Fatalf("CanonicalSnapshotPayload() error = %v", err)
		}
		artifactID := "snapshot-" + digest
		ledger.Artifacts = append(ledger.Artifacts, storage.GenerationArtifactRow{
			ID: artifactID, Kind: "agent_snapshot", SHA256: digest,
			Payload: payload, SizeBytes: int64(len(payload)), CreatedAt: seed.Now,
		})
		row := storage.AgentRevisionRow{
			AgentID: agentID, Revision: 1, OperationID: seed.OperationID,
			State: seed.States[agentID], SnapshotArtifactID: artifactID, SnapshotDigest: digest,
			ApplyTimeoutSeconds: 60, DrainTimeoutSeconds: 600, CreatedAt: seed.Now, UpdatedAt: seed.Now,
		}
		pointer := storage.AgentRevisionPointerRow{AgentID: agentID, DesiredRevision: 1, UpdatedAt: seed.Now}
		if row.State == storage.AgentRevisionStateApplied {
			appliedAt := seed.Now
			row.AppliedAt = &appliedAt
			pointer.AppliedRevision = 1
			pointer.LastKnownGoodRevision = 1
		}
		if row.State == storage.AgentRevisionStateFailed {
			failedAt := seed.Now
			row.FailedAt = &failedAt
			row.ErrorCode = "prepare_failed"
		}
		ledger.Revisions = append(ledger.Revisions, row)
		ledger.Pointers = append(ledger.Pointers, pointer)
		nodes = append(nodes, dependency.Node{AgentID: agentID, Revision: 1})
	}
	plan, err := dependency.NewPlan(seed.OperationID, dependency.ActionApply, nodes, seed.Edges)
	if err != nil {
		t.Fatalf("NewPlan() error = %v", err)
	}
	planPayload, err := plan.Marshal()
	if err != nil {
		t.Fatalf("plan.Marshal() error = %v", err)
	}
	planArtifactID := "dependency-plan-" + plan.Digest()
	ledger.Artifacts = append(ledger.Artifacts, storage.GenerationArtifactRow{
		ID: planArtifactID, Kind: storage.GenerationArtifactKindDependencyPlan,
		SHA256: plan.Digest(), Payload: planPayload, SizeBytes: int64(len(planPayload)), CreatedAt: seed.Now,
	})
	for _, node := range nodes {
		ledger.ArtifactRefs = append(ledger.ArtifactRefs, storage.AgentRevisionArtifactRow{
			AgentID: node.AgentID, Revision: node.Revision, ArtifactID: planArtifactID,
			Role: storage.RevisionArtifactRoleDependencyPlan, CreatedAt: seed.Now,
		})
	}
	for _, event := range seed.Events {
		event.OperationID = seed.OperationID
		event.CreatedAt = seed.Now
		if event.PayloadJSON == "" {
			event.PayloadJSON = `{}`
		}
		ledger.Events = append(ledger.Events, event)
	}
	if err := store.CreateRevisionLedger(context.Background(), ledger); err != nil {
		encoded, _ := json.Marshal(ledger)
		t.Fatalf("CreateRevisionLedger() error = %v ledger=%s", err, encoded)
	}
}

func newRevisionAPITestService(t *testing.T, store *storage.GormStore) *RevisionAPI {
	t.Helper()
	coord, err := coordinator.New(store, coordinator.Options{})
	if err != nil {
		t.Fatalf("coordinator.New() error = %v", err)
	}
	api := NewRevisionAPI(store, coord)
	if api == nil {
		t.Fatal("NewRevisionAPI() returned nil")
	}
	return api
}

func findAgentRevisionStatus(t *testing.T, statuses []AgentRevisionStatus, agentID string) AgentRevisionStatus {
	t.Helper()
	for _, status := range statuses {
		if status.AgentID == agentID {
			return status
		}
	}
	t.Fatalf("agent %q missing from statuses: %+v", agentID, statuses)
	return AgentRevisionStatus{}
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}
