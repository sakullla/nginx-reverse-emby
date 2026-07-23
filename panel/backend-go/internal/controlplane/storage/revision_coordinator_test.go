//go:build integration

package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"testing"
	"time"
)

func TestCoordinatorRetryDelayUsesCappedFullJitter(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		attempt int
		jitter  float64
		want    time.Duration
	}{
		{attempt: 1, jitter: 0, want: 0},
		{attempt: 1, jitter: 0.5, want: 500 * time.Millisecond},
		{attempt: 2, jitter: 0.5, want: time.Second},
		{attempt: 5, jitter: 0.5, want: 8 * time.Second},
		{attempt: 6, jitter: 0.5, want: 15 * time.Second},
		{attempt: 20, jitter: 0.5, want: 15 * time.Second},
		{attempt: 1, jitter: -1, want: 0},
		{attempt: 1, jitter: math.NaN(), want: 0},
	}
	for _, tc := range testCases {
		if got := coordinatorRetryDelay(tc.attempt, time.Second, 30*time.Second, tc.jitter); got != tc.want {
			t.Fatalf("coordinatorRetryDelay(%d, %v) = %v, want %v", tc.attempt, tc.jitter, got, tc.want)
		}
	}
	if got := coordinatorRetryDelay(20, time.Second, 30*time.Second, 1); got >= 30*time.Second || got < 29*time.Second {
		t.Fatalf("jitter=1 delay = %v, want clamped below 30s", got)
	}
}

func TestCopyCoordinatorSnapshotPayloadFiltersUnsupportedResources(t *testing.T) {
	t.Parallel()
	unsupportedID := 99
	payload, err := json.Marshal(Snapshot{
		Revision: 3,
		Rules: []HTTPRule{
			{ID: 1},
			{ID: 2, EgressProfileID: &unsupportedID},
		},
		L4Rules: []L4Rule{
			{ID: 3, Protocol: "tcp", ListenMode: "tcp"},
			{ID: 4, Protocol: "tcp", ListenMode: "unsupported"},
		},
		EgressProfiles: []EgressProfile{{ID: unsupportedID, Type: "unsupported"}},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	copyPayload, digest, err := copyCoordinatorSnapshotPayload(payload, 3, 4)
	if err != nil {
		t.Fatalf("copyCoordinatorSnapshotPayload() error = %v", err)
	}
	var copied Snapshot
	if err := json.Unmarshal(copyPayload, &copied); err != nil {
		t.Fatalf("json.Unmarshal(copy) error = %v", err)
	}
	if copied.Revision != 4 || len(copied.Rules) != 1 || copied.Rules[0].ID != 1 || len(copied.L4Rules) != 1 || copied.L4Rules[0].ID != 3 || len(copied.EgressProfiles) != 0 {
		t.Fatalf("copied snapshot = %+v", copied)
	}
	wantDigest := sha256.Sum256(copyPayload)
	if digest != hex.EncodeToString(wantDigest[:]) {
		t.Fatalf("copy digest = %q, want %q", digest, hex.EncodeToString(wantDigest[:]))
	}
}

func TestDeleteAgentTerminatesCoordinatorWorkAndKeepsOperationReadable(t *testing.T) {
	requireStorageIntegration(t)
	now := time.Date(2026, 7, 23, 5, 0, 0, 0, time.UTC)
	appliedAt := now.Add(-time.Minute)
	store := newTrafficTestStore(t, true)
	ctx := t.Context()
	if err := store.SaveAgent(ctx, AgentRow{ID: "edge-deleted", Name: "edge-deleted", AgentToken: "token"}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	if err := store.CreateRevisionLedger(ctx, RevisionLedgerWrite{
		Operation: OperationRow{
			ID: "operation-delete-agent", Kind: "l4_rule.delete", Status: OperationStatusPending,
			PrimaryAgentID: "edge-deleted", CreatedAt: now, UpdatedAt: now,
		},
		Revisions: []AgentRevisionRow{
			{
				AgentID: "edge-deleted", Revision: 4, OperationID: "operation-delete-agent",
				State: AgentRevisionStateApplied, GenerationID: "generation-4",
				DrainState: AgentRevisionDrainStateDraining, AppliedAt: &appliedAt,
				ApplyTimeoutSeconds: 60, DrainTimeoutSeconds: 600,
				CreatedAt: appliedAt, UpdatedAt: appliedAt,
			},
			{
				AgentID: "edge-deleted", Revision: 5, OperationID: "operation-delete-agent",
				State: AgentRevisionStatePending, ApplyTimeoutSeconds: 60, DrainTimeoutSeconds: 600,
				CreatedAt: now, UpdatedAt: now,
			},
		},
		Pointers: []AgentRevisionPointerRow{{
			AgentID: "edge-deleted", DesiredRevision: 5, AppliedRevision: 4,
			LastKnownGoodRevision: 4, UpdatedAt: now,
		}},
		Attempts: []AgentRevisionAttemptRow{{
			AgentID: "edge-deleted", Revision: 5, Attempt: 1, LeaseID: "lease-deleted",
			State: AgentRevisionAttemptStateLeased, StartedAt: now, DeadlineAt: now.Add(time.Minute),
		}},
		Generations: []AgentGenerationRow{{
			AgentID: "edge-deleted", GenerationID: "generation-3", Revision: 3,
			State: GenerationStateDraining, SessionCount: 1, CreatedAt: now, UpdatedAt: now,
		}},
	}); err != nil {
		t.Fatalf("CreateRevisionLedger() error = %v", err)
	}

	if err := store.DeleteAgent(ctx, "edge-deleted"); err != nil {
		t.Fatalf("DeleteAgent() error = %v", err)
	}

	revision, found, err := store.GetCoordinatorRevision(ctx, "edge-deleted", 5)
	if err != nil || !found {
		t.Fatalf("GetCoordinatorRevision() = %+v, found %v, error %v", revision, found, err)
	}
	if revision.State != AgentRevisionStateSuperseded || revision.ErrorCode != "agent_deleted" {
		t.Fatalf("revision after delete = %+v, want superseded/agent_deleted", revision)
	}
	appliedRevision, found, err := store.GetCoordinatorRevision(ctx, "edge-deleted", 4)
	if err != nil || !found {
		t.Fatalf("GetCoordinatorRevision(applied) = %+v, found %v, error %v", appliedRevision, found, err)
	}
	if appliedRevision.DrainState != AgentRevisionDrainStateForced {
		t.Fatalf("applied revision after delete = %+v, want forced drain state", appliedRevision)
	}
	attempts, err := store.ListCoordinatorAttempts(ctx, "edge-deleted", 5)
	if err != nil || len(attempts) != 1 {
		t.Fatalf("ListCoordinatorAttempts() = %+v, error %v", attempts, err)
	}
	if attempts[0].State != AgentRevisionAttemptStateSuperseded || attempts[0].ErrorCode != "agent_deleted" || attempts[0].FinishedAt == nil {
		t.Fatalf("attempt after delete = %+v, want terminal agent_deleted", attempts[0])
	}
	if pointer, found, err := store.GetAgentRevisionPointer(ctx, "edge-deleted"); err != nil || found {
		t.Fatalf("GetAgentRevisionPointer() = %+v, found %v, error %v; want removed", pointer, found, err)
	}
	var generation AgentGenerationRow
	if err := store.db.WithContext(ctx).Where("agent_id = ? AND generation_id = ?", "edge-deleted", "generation-3").First(&generation).Error; err != nil {
		t.Fatalf("load generation: %v", err)
	}
	if generation.State != AgentRevisionDrainStateForced || !generation.Forced || generation.ForceReason != "agent_deleted" || generation.DrainedAt == nil {
		t.Fatalf("generation after delete = %+v, want forced agent_deleted", generation)
	}
	operation, found, err := store.GetOperation(ctx, "operation-delete-agent")
	if err != nil || !found {
		t.Fatalf("GetOperation() = %+v, found %v, error %v", operation, found, err)
	}
	if operation.Status != OperationStatusSuperseded || operation.CompletedAt == nil {
		t.Fatalf("operation after delete = %+v, want completed superseded", operation)
	}
	if agentIDs, err := store.ListCoordinatorAgentIDs(ctx); err != nil || len(agentIDs) != 0 {
		t.Fatalf("ListCoordinatorAgentIDs() = %+v, error %v; want empty", agentIDs, err)
	}
	agents, err := store.ListAgents(ctx)
	if err != nil || len(agents) != 0 {
		t.Fatalf("ListAgents() = %+v, error %v; want deleted", agents, err)
	}

	if err := store.SaveAgent(ctx, AgentRow{ID: "edge-deleted", Name: "edge-restored", AgentToken: "restored-token"}); err != nil {
		t.Fatalf("SaveAgent(restored) error = %v", err)
	}
	recreated, err := store.LockAgentRevisionPointer(ctx, "edge-deleted", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("LockAgentRevisionPointer(restored) error = %v", err)
	}
	if recreated.DesiredRevision != 5 {
		t.Fatalf("recreated pointer = %+v, want retained revision floor 5", recreated)
	}
}

func TestCoordinatorClaimFencesExpectedOperationAndRevision(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 12, 23, 30, 0, 0, time.UTC)
	store := newTrafficTestStore(t, true)
	for _, seed := range []struct {
		revision    int64
		operationID string
	}{{revision: 1, operationID: "operation-old"}, {revision: 2, operationID: "operation-new"}} {
		revision, operationID := seed.revision, seed.operationID
		if err := store.CreateRevisionLedger(t.Context(), RevisionLedgerWrite{
			Operation: OperationRow{
				ID: operationID, Kind: "test_claim_fence", Status: OperationStatusPending,
				PrimaryAgentID: "edge-a", CreatedAt: now, UpdatedAt: now,
			},
			Revisions: []AgentRevisionRow{{
				AgentID: "edge-a", Revision: revision, OperationID: operationID,
				State: AgentRevisionStatePending, ApplyTimeoutSeconds: 60,
				DrainTimeoutSeconds: 600, CreatedAt: now, UpdatedAt: now,
			}},
			Pointers: []AgentRevisionPointerRow{{
				AgentID: "edge-a", DesiredRevision: revision, UpdatedAt: now,
			}},
		}); err != nil {
			t.Fatalf("CreateRevisionLedger(%s) error = %v", operationID, err)
		}
	}

	result, err := store.ClaimLatestAgentRevision(t.Context(), CoordinatorClaimRequest{
		AgentID: "edge-a", LeaseID: "lease-old-plan", Now: now,
		ExpectedOperationID: "operation-old", ExpectedRevision: 1,
	})
	if err != nil {
		t.Fatalf("ClaimLatestAgentRevision() error = %v", err)
	}
	if result.Lease != nil || result.Busy || len(result.SupersededRevisions) != 0 {
		t.Fatalf("claim result = %+v, want fenced no-op", result)
	}
	if attempts, err := store.ListCoordinatorAttempts(t.Context(), "edge-a", 2); err != nil {
		t.Fatalf("ListCoordinatorAttempts() error = %v", err)
	} else if len(attempts) != 0 {
		t.Fatalf("newer revision attempts = %+v, want none", attempts)
	}
	old, found, err := store.GetCoordinatorRevision(t.Context(), "edge-a", 1)
	if err != nil || !found {
		t.Fatalf("GetCoordinatorRevision(old) = %+v, found %v, error %v", old, found, err)
	}
	if old.State != AgentRevisionStatePending {
		t.Fatalf("old revision state = %q, want unchanged pending", old.State)
	}
}
