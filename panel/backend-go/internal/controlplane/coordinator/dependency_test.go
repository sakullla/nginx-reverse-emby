//go:build integration

package coordinator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/dependency"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type interleavingDependencyRepository struct {
	*storage.GormStore
	beforeClaim   chan struct{}
	continueClaim chan struct{}
	once          sync.Once
}

func (r *interleavingDependencyRepository) ClaimLatestAgentRevision(ctx context.Context, request storage.CoordinatorClaimRequest) (storage.CoordinatorClaimResult, error) {
	r.once.Do(func() { close(r.beforeClaim) })
	select {
	case <-r.continueClaim:
		return r.GormStore.ClaimLatestAgentRevision(ctx, request)
	case <-ctx.Done():
		return storage.CoordinatorClaimResult{}, ctx.Err()
	}
}

func TestIntegrationCoordinatorClaimsOnlyPersistedApplyFrontier(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 12, 23, 10, 0, 0, time.UTC)
	store := newCoordinatorTestStore(t)
	seedDependencyOperation(t, store, now)
	coord := newTestCoordinator(t, store, now, 0.5)

	loaded, err := coord.LoadDependencyPlan(t.Context(), "operation-dependency")
	if err != nil {
		t.Fatalf("LoadDependencyPlan() error = %v", err)
	}
	claimed, err := coord.ClaimDependencyFrontier(t.Context(), loaded.OperationID)
	if err != nil {
		t.Fatalf("ClaimDependencyFrontier() error = %v", err)
	}
	if len(claimed.Claims) != 1 || claimed.Claims[0].Node.AgentID != "edge-b" || claimed.Claims[0].Result.Lease == nil {
		t.Fatalf("claims = %+v, want one edge-b lease", claimed.Claims)
	}
	if attempts, err := store.ListCoordinatorAttempts(t.Context(), "edge-a", 1); err != nil {
		t.Fatalf("ListCoordinatorAttempts(edge-a) error = %v", err)
	} else if len(attempts) != 0 {
		t.Fatalf("non-frontier edge-a attempts = %+v, want none", attempts)
	}

	lease := *claimed.Claims[0].Result.Lease
	if _, err := coord.Start(t.Context(), StartRequest{Lease: lease, GenerationID: "generation-b"}); err != nil {
		t.Fatalf("Start(edge-b) error = %v", err)
	}
	if _, err := coord.Applied(t.Context(), AppliedReport{Lease: lease, GenerationID: "generation-b"}); err != nil {
		t.Fatalf("Applied(edge-b) error = %v", err)
	}
	claimed, err = coord.ClaimDependencyFrontier(t.Context(), loaded.OperationID)
	if err != nil {
		t.Fatalf("ClaimDependencyFrontier(after edge-b) error = %v", err)
	}
	if len(claimed.Claims) != 1 || claimed.Claims[0].Node.AgentID != "edge-a" || claimed.Claims[0].Result.Lease == nil {
		t.Fatalf("released claims = %+v, want one edge-a lease", claimed.Claims)
	}
}

func TestIntegrationCoordinatorOrdinaryClaimCannotBypassPersistedFrontier(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 12, 23, 12, 0, 0, time.UTC)
	store := newCoordinatorTestStore(t)
	seedDependencyOperation(t, store, now)
	coord := newTestCoordinator(t, store, now, 0.5)

	claimed, err := coord.Claim(t.Context(), "edge-a")
	if !errors.Is(err, ErrDependencyClaimRequired) {
		t.Fatalf("Claim(edge-a) = %+v, error %v; want dependency-scoped rejection", claimed, err)
	}
	if claimed.Lease != nil {
		t.Fatalf("ordinary claim bypassed frontier with lease %+v", claimed.Lease)
	}
	if attempts, listErr := store.ListCoordinatorAttempts(t.Context(), "edge-a", 1); listErr != nil {
		t.Fatalf("ListCoordinatorAttempts(edge-a) error = %v", listErr)
	} else if len(attempts) != 0 {
		t.Fatalf("ordinary claim created non-frontier attempts: %+v", attempts)
	}

	frontier, err := coord.ClaimDependencyFrontier(t.Context(), "operation-dependency")
	if err != nil {
		t.Fatalf("ClaimDependencyFrontier() error = %v", err)
	}
	if len(frontier.Claims) != 1 || frontier.Claims[0].Node.AgentID != "edge-b" || frontier.Claims[0].Result.Lease == nil {
		t.Fatalf("frontier claims = %+v, want one edge-b lease", frontier.Claims)
	}
}

func TestIntegrationCoordinatorClaimsEveryIndependentFrontierNode(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 12, 23, 15, 0, 0, time.UTC)
	store := newCoordinatorTestStore(t)
	plan, err := dependency.NewPlan("operation-independent", dependency.ActionApply, []dependency.Node{
		{AgentID: "edge-a", Revision: 1}, {AgentID: "edge-b", Revision: 1},
	}, nil)
	if err != nil {
		t.Fatalf("NewPlan() error = %v", err)
	}
	createDependencyLedger(t, store, now, plan.OperationID, 1, map[string]storage.Snapshot{
		"edge-a": {Revision: 1}, "edge-b": {Revision: 1},
	}, 0, plan)
	coord := newTestCoordinator(t, store, now, 0.5)

	claimed, err := coord.ClaimDependencyFrontier(t.Context(), plan.OperationID)
	if err != nil {
		t.Fatalf("ClaimDependencyFrontier() error = %v", err)
	}
	if len(claimed.Claims) != 2 {
		t.Fatalf("claims = %+v, want both independent agents", claimed.Claims)
	}
	for _, claim := range claimed.Claims {
		if claim.Result.Lease == nil || claim.Result.Lease.Revision != 1 {
			t.Fatalf("claim = %+v, want revision 1 lease", claim)
		}
	}
}

func TestIntegrationCoordinatorConcurrentDesiredAdvanceFencesStaleFrontierClaim(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 12, 23, 18, 0, 0, time.UTC)
	store := newCoordinatorTestStore(t)
	seedDependencyOperation(t, store, now)
	repository := &interleavingDependencyRepository{
		GormStore: store, beforeClaim: make(chan struct{}), continueClaim: make(chan struct{}),
	}
	ids := &sequenceIDs{}
	coord, err := New(repository, Options{
		Clock: &fakeClock{now: now}, Random: &sequenceRandom{values: []float64{0.5}}, NewID: ids.New,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	type claimOutcome struct {
		result DependencyFrontierClaimResult
		err    error
	}
	outcome := make(chan claimOutcome, 1)
	claimCtx, cancelClaim := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancelClaim()
	go func() {
		result, claimErr := coord.ClaimDependencyFrontier(claimCtx, "operation-dependency")
		outcome <- claimOutcome{result: result, err: claimErr}
	}()

	select {
	case <-repository.beforeClaim:
	case <-claimCtx.Done():
		t.Fatalf("frontier claim did not reach the controlled interleaving: %v", claimCtx.Err())
	}
	appendPendingRevision(t, store, now.Add(time.Second), "edge-b", 2, 0, 60, 600)
	close(repository.continueClaim)
	var claimed claimOutcome
	select {
	case claimed = <-outcome:
	case <-claimCtx.Done():
		t.Fatalf("frontier claim did not complete after interleaving: %v", claimCtx.Err())
	}
	if claimed.err != nil {
		t.Fatalf("ClaimDependencyFrontier() error = %v", claimed.err)
	}
	if len(claimed.result.Claims) != 1 || claimed.result.Claims[0].Node.AgentID != "edge-b" {
		t.Fatalf("claims = %+v, want stale edge-b frontier attempt", claimed.result.Claims)
	}
	if claimed.result.Claims[0].Result.Lease != nil {
		t.Fatalf("stale plan claimed newer desired revision: %+v", claimed.result.Claims[0].Result.Lease)
	}
	for _, revision := range []int64{1, 2} {
		attempts, err := store.ListCoordinatorAttempts(t.Context(), "edge-b", revision)
		if err != nil {
			t.Fatalf("ListCoordinatorAttempts(%d) error = %v", revision, err)
		}
		if len(attempts) != 0 {
			t.Fatalf("edge-b revision %d attempts = %+v, want none", revision, attempts)
		}
	}
}

func TestIntegrationCoordinatorClaimsPersistedDeletePlanInReverseOrder(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 12, 23, 20, 0, 0, time.UTC)
	store := newCoordinatorTestStore(t)
	seedDependencyDeleteOperation(t, store, now)
	coord := newTestCoordinator(t, store, now, 0.5)

	claimed, err := coord.ClaimDependencyFrontier(t.Context(), "operation-dependency-delete")
	if err != nil {
		t.Fatalf("ClaimDependencyFrontier(delete) error = %v", err)
	}
	if len(claimed.Claims) != 1 || claimed.Claims[0].Node.AgentID != "edge-a" || claimed.Claims[0].Result.Lease == nil {
		t.Fatalf("delete claims = %+v, want caller edge-a first", claimed.Claims)
	}
	if attempts, err := store.ListCoordinatorAttempts(t.Context(), "edge-b", 2); err != nil {
		t.Fatalf("ListCoordinatorAttempts(edge-b) error = %v", err)
	} else if len(attempts) != 0 {
		t.Fatalf("delete dependency edge-b attempts = %+v, want none", attempts)
	}
}

func TestIntegrationCoordinatorRebuildsIdenticalDegradedAuditAfterRestart(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 12, 23, 25, 0, 0, time.UTC)
	dbPath := filepath.Join(t.TempDir(), "dependency-restart.db")
	store := openCoordinatorTestStore(t, dbPath)
	plan, err := dependency.NewPlan("operation-degraded", dependency.ActionApply, []dependency.Node{
		{AgentID: "edge-a", Revision: 1},
		{AgentID: "edge-b", Revision: 1},
		{AgentID: "edge-c", Revision: 1},
	}, []dependency.Edge{
		{FromAgentID: "edge-a", ToAgentID: "edge-b", Kind: dependency.EdgeKindRelayLayer},
		{FromAgentID: "edge-c", ToAgentID: "edge-a", Kind: dependency.EdgeKindRelayLayer},
	})
	if err != nil {
		t.Fatalf("NewPlan() error = %v", err)
	}
	seedDependencyAuditLedger(t, store, now, plan)
	coord := newTestCoordinator(t, store, now, 0.5)

	before, err := coord.EvaluateDependencyOperation(t.Context(), plan.OperationID)
	if err != nil {
		t.Fatalf("EvaluateDependencyOperation() error = %v", err)
	}
	if before.Status != dependency.StatusDegraded {
		t.Fatalf("status = %q, want degraded", before.Status)
	}
	blocked, found := before.Result("edge-c")
	if !found || !reflect.DeepEqual(blocked.BlockedBy, []string{"edge-a"}) {
		t.Fatalf("edge-c result = %+v, found %v; want blocked_by edge-a", blocked, found)
	}
	if succeeded, found := before.Result("edge-b"); !found || succeeded.State != dependency.StateSucceeded {
		t.Fatalf("edge-b result = %+v, found %v; successful fact was not preserved", succeeded, found)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened := openCoordinatorTestStore(t, dbPath)
	restarted := newTestCoordinator(t, reopened, now, 0.5)
	after, err := restarted.EvaluateDependencyOperation(t.Context(), plan.OperationID)
	if err != nil {
		t.Fatalf("EvaluateDependencyOperation(restarted) error = %v", err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("restart evaluation = %+v, want %+v", after, before)
	}
}

func TestIntegrationCoordinatorRebuildsDependencyPlanFromPersistedRevisionArtifacts(t *testing.T) {
	t.Parallel()
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

	claimed, err := coord.ClaimDependencyFrontier(t.Context(), plan.OperationID)
	if err != nil {
		t.Fatalf("ClaimDependencyFrontier() error = %v", err)
	}
	if len(claimed.Claims) != 1 || claimed.Claims[0].Node.AgentID != "edge-b" || claimed.Claims[0].Result.Lease == nil {
		t.Fatalf("dependency claims = %+v, want one edge-b lease", claimed.Claims)
	}
	lease := *claimed.Claims[0].Result.Lease
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

func TestIntegrationCoordinatorLoadDependencyPlanRebuildsUnsupportedPersistedGraph(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 21, 8, 0, 0, 0, time.UTC)
	store := newCoordinatorTestStore(t)
	snapshots := map[string]storage.Snapshot{
		"edge-a": {
			Revision: 1,
			Rules:    []storage.HTTPRule{{ID: 1, AgentID: "edge-a", RelayLayers: [][]int{{90}}}},
		},
		"edge-b": {
			Revision: 1,
			RelayListeners: []storage.RelayListener{{
				ID: 90, AgentID: "edge-b", Enabled: true, TransportMode: "unsupported",
			}},
		},
	}
	persisted, err := dependency.BuildPlan("operation-unsupported-dependency", dependency.ActionApply, []dependency.SnapshotRevision{
		{AgentID: "edge-a", Revision: 1, Snapshot: snapshots["edge-a"]},
		{AgentID: "edge-b", Revision: 1, Snapshot: snapshots["edge-b"]},
	})
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	if len(persisted.Edges) != 1 {
		t.Fatalf("persisted edges = %+v, want one legacy edge", persisted.Edges)
	}
	createDependencyLedger(t, store, now, persisted.OperationID, 1, snapshots, 0, persisted)
	coord := newTestCoordinator(t, store, now, 0.5)

	loaded, err := coord.LoadDependencyPlan(t.Context(), persisted.OperationID)
	if err != nil {
		t.Fatalf("LoadDependencyPlan() error = %v", err)
	}
	if len(loaded.Edges) != 0 {
		t.Fatalf("loaded edges = %+v, want unsupported dependency removed", loaded.Edges)
	}
	for _, agentID := range []string{"edge-a", "edge-b"} {
		row, found, err := store.GetCoordinatorRevision(t.Context(), agentID, 1)
		if err != nil || !found {
			t.Fatalf("GetCoordinatorRevision(%s) = %+v found=%v error=%v", agentID, row, found, err)
		}
		artifact, found, err := store.GetGenerationArtifact(t.Context(), row.SnapshotArtifactID)
		if err != nil || !found || artifact.SHA256 != row.SnapshotDigest {
			t.Fatalf("GetGenerationArtifact(%s) = %+v found=%v error=%v", agentID, artifact, found, err)
		}
		var snapshot storage.Snapshot
		if err := json.Unmarshal(artifact.Payload, &snapshot); err != nil {
			t.Fatalf("json.Unmarshal(%s snapshot) error = %v", agentID, err)
		}
		if len(snapshot.Rules) != 0 || len(snapshot.RelayListeners) != 0 {
			t.Fatalf("persisted snapshot %s = %+v, want unsupported graph removed", agentID, snapshot)
		}
	}
	evaluation, err := coord.EvaluateDependencyPlan(t.Context(), loaded)
	if err != nil {
		t.Fatalf("EvaluateDependencyPlan() error = %v", err)
	}
	if len(evaluation.Frontier) != 2 {
		t.Fatalf("frontier = %+v, want both ordinary nodes independent", evaluation.Frontier)
	}
}

func TestIntegrationCoordinatorRebuildsDeletePlanFromPreviousSnapshots(t *testing.T) {
	t.Parallel()
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

func TestIntegrationCoordinatorRebuildsDeletePlanFromZeroBaselineSnapshot(t *testing.T) {
	t.Parallel()
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

func TestIntegrationCoordinatorEvaluationPreservesSupersededRevisionFacts(t *testing.T) {
	t.Parallel()
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
	plan, err := dependency.BuildPlan(ledger.Operation.ID, dependency.ActionApply, []dependency.SnapshotRevision{
		{AgentID: "edge-a", Revision: 1, Snapshot: snapshots["edge-a"]},
		{AgentID: "edge-b", Revision: 1, Snapshot: snapshots["edge-b"]},
	})
	if err != nil {
		t.Fatalf("BuildPlan(dependency) error = %v", err)
	}
	appendDependencyPlanArtifact(t, &ledger, plan, now)
	if err := store.CreateRevisionLedger(t.Context(), ledger); err != nil {
		t.Fatalf("CreateRevisionLedger(dependency) error = %v", err)
	}
}

func seedDependencyDeleteOperation(t *testing.T, store *storage.GormStore, now time.Time) {
	t.Helper()
	before := map[string]storage.Snapshot{
		"edge-a": {
			Revision: 1,
			Rules:    []storage.HTTPRule{{ID: 1, AgentID: "edge-a", RelayLayers: [][]int{{10}}}},
		},
		"edge-b": {
			Revision:       1,
			RelayListeners: []storage.RelayListener{{ID: 10, AgentID: "edge-b", Enabled: true}},
		},
	}
	createDependencyLedger(t, store, now, "operation-dependency-before-delete", 1, before, 1)
	plan, err := dependency.BuildPlan("operation-dependency-delete", dependency.ActionDelete, []dependency.SnapshotRevision{
		{AgentID: "edge-a", Revision: 2, Snapshot: before["edge-a"]},
		{AgentID: "edge-b", Revision: 2, Snapshot: before["edge-b"]},
	})
	if err != nil {
		t.Fatalf("BuildPlan(delete) error = %v", err)
	}
	createDependencyLedger(t, store, now.Add(time.Second), "operation-dependency-delete", 2, map[string]storage.Snapshot{
		"edge-a": {Revision: 2},
		"edge-b": {Revision: 2},
	}, 1, plan)
}

func createDependencyLedger(t *testing.T, store *storage.GormStore, now time.Time, operationID string, revision int64, snapshots map[string]storage.Snapshot, applied int64, plans ...dependency.Plan) {
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
	for _, plan := range plans {
		appendDependencyPlanArtifact(t, &ledger, plan, now)
	}
	if err := store.CreateRevisionLedger(t.Context(), ledger); err != nil {
		t.Fatalf("CreateRevisionLedger(%s) error = %v", operationID, err)
	}
}

func appendDependencyPlanArtifact(t *testing.T, ledger *storage.RevisionLedgerWrite, plan dependency.Plan, now time.Time) {
	t.Helper()
	payload, err := plan.Marshal()
	if err != nil {
		t.Fatalf("Marshal(dependency plan) error = %v", err)
	}
	digest := plan.Digest()
	artifactID := "dependency-plan-" + digest
	ledger.Artifacts = append(ledger.Artifacts, storage.GenerationArtifactRow{
		ID: artifactID, Kind: storage.GenerationArtifactKindDependencyPlan, SHA256: digest,
		Payload: payload, SizeBytes: int64(len(payload)), CreatedAt: now,
	})
	for _, revision := range ledger.Revisions {
		ledger.ArtifactRefs = append(ledger.ArtifactRefs, storage.AgentRevisionArtifactRow{
			AgentID: revision.AgentID, Revision: revision.Revision, ArtifactID: artifactID,
			Role: storage.RevisionArtifactRoleDependencyPlan, CreatedAt: now,
		})
	}
}

func seedDependencyAuditLedger(t *testing.T, store *storage.GormStore, now time.Time, plan dependency.Plan) {
	t.Helper()
	ledger := storage.RevisionLedgerWrite{Operation: storage.OperationRow{
		ID: plan.OperationID, Kind: "test_dependency", Status: storage.OperationStatusFailed,
		PrimaryAgentID: "edge-a", CreatedAt: now, UpdatedAt: now,
	}}
	states := map[string]string{
		"edge-a": storage.AgentRevisionStateFailed,
		"edge-b": storage.AgentRevisionStateApplied,
		"edge-c": storage.AgentRevisionStatePending,
	}
	for _, node := range plan.Nodes {
		snapshot := storage.Snapshot{Revision: node.Revision}
		payload, err := json.Marshal(snapshot)
		if err != nil {
			t.Fatalf("marshal %s snapshot: %v", node.AgentID, err)
		}
		digestBytes := sha256.Sum256(payload)
		digest := hex.EncodeToString(digestBytes[:])
		artifactID := "snapshot-" + digest
		ledger.Artifacts = append(ledger.Artifacts, storage.GenerationArtifactRow{
			ID: artifactID, Kind: "agent_snapshot", SHA256: digest,
			Payload: payload, SizeBytes: int64(len(payload)), CreatedAt: now,
		})
		revision := storage.AgentRevisionRow{
			AgentID: node.AgentID, Revision: node.Revision, OperationID: plan.OperationID,
			State: states[node.AgentID], SnapshotArtifactID: artifactID, SnapshotDigest: digest,
			ApplyTimeoutSeconds: 60, DrainTimeoutSeconds: 600, CreatedAt: now, UpdatedAt: now,
		}
		applied := int64(0)
		if revision.State == storage.AgentRevisionStateApplied {
			applied = node.Revision
			appliedAt := now
			revision.AppliedAt = &appliedAt
		}
		if revision.State == storage.AgentRevisionStateFailed {
			failedAt := now
			revision.AttemptCount = 5
			revision.FailedAt = &failedAt
		}
		ledger.Revisions = append(ledger.Revisions, revision)
		ledger.Pointers = append(ledger.Pointers, storage.AgentRevisionPointerRow{
			AgentID: node.AgentID, DesiredRevision: node.Revision,
			AppliedRevision: applied, LastKnownGoodRevision: applied, UpdatedAt: now,
		})
	}
	appendDependencyPlanArtifact(t, &ledger, plan, now)
	if err := store.CreateRevisionLedger(t.Context(), ledger); err != nil {
		t.Fatalf("CreateRevisionLedger(degraded audit) error = %v", err)
	}
}
