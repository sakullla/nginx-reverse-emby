package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/coordinator"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/dependency"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/revision"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestRevisionAPIReconstructsDegradedBlockedStatusAndEventCursor(t *testing.T) {
	t.Parallel()
	store, err := newServiceTestSQLiteStore(t, t.TempDir(), "local")
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

func TestRevisionAPIKeepsSupersededOperationReadableAfterAgentDeletion(t *testing.T) {
	t.Parallel()
	store, err := newServiceTestSQLiteStore(t, t.TempDir(), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ctx := t.Context()
	now := time.Date(2026, 7, 23, 6, 0, 0, 0, time.UTC)
	if err := store.SaveAgent(ctx, storage.AgentRow{
		ID: "edge-deleted", Name: "Deleted Edge", AgentToken: "token", Mode: "pull",
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	seedRevisionOperation(t, store, revisionOperationSeed{
		OperationID: "op-agent-deleted", Revision: 5, Now: now,
		States: map[string]string{"edge-deleted": storage.AgentRevisionStatePending},
	})
	if err := store.DeleteAgent(ctx, "edge-deleted"); err != nil {
		t.Fatalf("DeleteAgent() error = %v", err)
	}

	status, err := newRevisionAPITestService(t, store).GetOperationStatus(ctx, "op-agent-deleted")
	if err != nil {
		t.Fatalf("GetOperationStatus() error = %v", err)
	}
	if status.ApplyStatus != storage.OperationStatusSuperseded || status.CompletedAt == nil {
		t.Fatalf("operation status = %+v, want completed superseded", status)
	}
	if len(status.Agents) != 1 {
		t.Fatalf("operation agents = %+v, want one historical agent", status.Agents)
	}
	agent := status.Agents[0]
	if agent.AgentID != "edge-deleted" || agent.DesiredRevision != 5 ||
		agent.ApplyStatus != storage.AgentRevisionStateSuperseded || agent.ErrorCode != "agent_deleted" {
		t.Fatalf("agent status = %+v, want deleted revision history", agent)
	}
}

func TestRevisionAPIPullRepairsReportedRuntimeBehindAppliedPointer(t *testing.T) {
	t.Parallel()
	store, err := newServiceTestSQLiteStore(t, t.TempDir(), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	seedRevisionOperation(t, store, revisionOperationSeed{
		OperationID: "op-applied", Revision: 4, Now: now,
		States: map[string]string{"edge-reset": storage.AgentRevisionStateApplied},
	})
	if err := store.SaveAgent(t.Context(), storage.AgentRow{
		ID: "edge-reset", Name: "Reset Edge", AgentToken: "token", Mode: "pull",
		CurrentRevision: 0, LastApplyStatus: "success",
	}); err != nil {
		t.Fatalf("SaveAgent() error = %v", err)
	}
	api := newRevisionAPITestService(t, store)

	pull, err := api.PullRemoteRevision(t.Context(), "edge-reset")
	if err != nil {
		t.Fatalf("PullRemoteRevision() error = %v", err)
	}
	if !pull.HasUpdate || pull.Lease == nil || pull.Snapshot == nil ||
		pull.DesiredRevision != 5 || pull.Snapshot.Revision != 5 {
		t.Fatalf("repair pull = %+v", pull)
	}
	pointer, found, err := store.GetAgentRevisionPointer(t.Context(), "edge-reset")
	if err != nil || !found || pointer.DesiredRevision != 5 || pointer.AppliedRevision != 4 {
		t.Fatalf("repair pointer = %+v found=%v error=%v", pointer, found, err)
	}
	repairRevision, found, err := store.GetCoordinatorRevision(t.Context(), "edge-reset", 5)
	if err != nil || !found {
		t.Fatalf("repair revision = %+v found=%v error=%v", repairRevision, found, err)
	}
	operation, found, err := store.GetOperation(t.Context(), repairRevision.OperationID)
	if err != nil || !found || operation.Kind != "repair_runtime_state" {
		t.Fatalf("repair operation = %+v found=%v error=%v", operation, found, err)
	}

	replayed, err := api.PullRemoteRevision(t.Context(), "edge-reset")
	if err != nil || replayed.Lease == nil || replayed.Lease.LeaseID != pull.Lease.LeaseID {
		t.Fatalf("replayed repair pull = %+v error=%v", replayed, err)
	}
	revisions, err := store.ListAgentRevisions(t.Context(), "edge-reset")
	if err != nil || len(revisions) != 2 {
		t.Fatalf("repair revisions = %+v error=%v", revisions, err)
	}
}

func TestRevisionAPIRemotePullClaimsOnlyCallerFrontierAndRejectsStaleReport(t *testing.T) {
	t.Parallel()
	store, err := newServiceTestSQLiteStore(t, t.TempDir(), "local")
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

func TestRevisionAPIMapsAuthenticatedPluginStatusToLifecycleCompletion(t *testing.T) {
	store, err := newServiceTestSQLiteStore(t, t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	digest := strings.Repeat("a", 64)
	seedRevisionOperation(t, store, revisionOperationSeed{
		OperationID: "operation-plugin-runtime", Now: now,
		States: map[string]string{"edge-plugin": storage.AgentRevisionStatePending},
	})
	pluginOperation := storage.PluginOperationRow{ID: "operation-plugin-runtime", PluginID: "runtime.plugin", Kind: "enable", Status: "applying", TargetRevision: 1, ActorID: "admin", AgentResultsJSON: `{}`, CreatedAt: now}
	if err := store.RecordPluginOperation(t.Context(), pluginOperation, storage.AuditEventRow{ID: "audit-plugin-runtime", ActorID: "admin", Action: "plugin.enable", TargetKind: "plugin", TargetID: pluginOperation.PluginID, Result: "accepted", MetadataJSON: `{}`, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	staged := storage.PluginAgentRuntimeStatusRow{OperationID: pluginOperation.ID, AgentID: "edge-plugin", InstanceID: "instance-plugin", PluginID: pluginOperation.PluginID, Revision: 1, GenerationID: digest, PackageDigest: digest, ArtifactDigest: digest, ConfigVersion: 3}
	if err := store.StagePluginAgentRuntimeStatuses(t.Context(), []storage.PluginAgentRuntimeStatusRow{staged}); err != nil {
		t.Fatal(err)
	}
	completion := &lifecycleCompletionRecorder{}
	reconciler, err := NewPluginLifecycleReconciler(store, completion)
	if err != nil {
		t.Fatal(err)
	}
	api := newRevisionAPITestService(t, store)
	api.SetPluginLifecycleReconciler(reconciler)
	pull, err := api.PullRemoteRevision(t.Context(), "edge-plugin")
	if err != nil || pull.Lease == nil {
		t.Fatalf("pull = %+v err=%v", pull, err)
	}
	start := RemoteRevisionStart{AgentID: "edge-plugin", Revision: 1, RetryCycle: pull.Lease.RetryCycle, Attempt: pull.Lease.Attempt, LeaseID: pull.Lease.LeaseID, GenerationID: "runtime-generation"}
	if _, err := api.StartRemoteRevision(t.Context(), "edge-plugin", start); err != nil {
		t.Fatal(err)
	}
	report := RemoteRevisionReport{
		AgentID: "edge-plugin", Revision: 1, RetryCycle: pull.Lease.RetryCycle, Attempt: pull.Lease.Attempt,
		LeaseID: pull.Lease.LeaseID, GenerationID: start.GenerationID, Status: storage.AgentRevisionStateApplied,
		PluginStatuses: []storage.PluginRuntimeStatus{{
			InstanceID: staged.InstanceID, PluginID: staged.PluginID, OperationID: staged.OperationID, Revision: staged.Revision,
			GenerationID: digest, PackageDigest: digest, ArtifactDigest: digest, ConfigVersion: staged.ConfigVersion,
			RuntimeKind: "rpc-service", State: "active", Sequence: 1, SafeDetail: "runtime ready", Details: json.RawMessage(`{"healthy":true}`), Budget: json.RawMessage(`{}`),
		}},
	}
	if _, err := api.ReportRemoteRevision(t.Context(), "edge-plugin", report); err != nil {
		t.Fatal(err)
	}
	if _, replayed, err := store.RecordPluginAgentRuntimeReport(t.Context(), storage.PluginGenerationReport{
		OperationID: staged.OperationID, AgentID: staged.AgentID, InstanceID: staged.InstanceID, PluginID: staged.PluginID,
		Revision: staged.Revision, GenerationID: digest, PackageDigest: digest, ArtifactDigest: digest,
		State: "active", Sequence: 1, SafeDetail: "runtime ready", Details: json.RawMessage(`{"healthy":true}`),
		Budget: json.RawMessage(`{}`), ReportedAt: time.Now().UTC(),
	}); err != nil || !replayed {
		t.Fatalf("applied-report to heartbeat replay = %v, %v", replayed, err)
	}
	if completion.kind != "lifecycle" || !completion.result.Applied || completion.result.OperationID != pluginOperation.ID {
		t.Fatalf("completion = %+v", completion)
	}
}

type retryLifecycleCompletion struct {
	*lifecycleCompletionRecorder
	fail  bool
	calls int
}

func (r *retryLifecycleCompletion) CompleteLifecycleApply(ctx context.Context, result PluginApplyResult) (storage.InstalledPluginRow, error) {
	r.calls++
	if r.fail {
		return storage.InstalledPluginRow{}, errors.New("injected completion failure")
	}
	return r.lifecycleCompletionRecorder.CompleteLifecycleApply(ctx, result)
}

func TestRevisionAPIRetriesLifecycleCompletionFromPersistedTerminalStatus(t *testing.T) {
	store, err := newServiceTestSQLiteStore(t, t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	digest := strings.Repeat("c", 64)
	seedRevisionOperation(t, store, revisionOperationSeed{
		OperationID: "operation-plugin-retry", Now: now,
		States: map[string]string{"edge-plugin-retry": storage.AgentRevisionStatePending},
	})
	operation := storage.PluginOperationRow{ID: "operation-plugin-retry", PluginID: "retry.plugin", Kind: "enable", Status: "applying", TargetRevision: 1, ActorID: "admin", AgentResultsJSON: `{}`, CreatedAt: now}
	if err := store.RecordPluginOperation(t.Context(), operation, storage.AuditEventRow{ID: "audit-plugin-retry", ActorID: "admin", Action: "plugin.enable", TargetKind: "plugin", TargetID: operation.PluginID, Result: "accepted", MetadataJSON: `{}`, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	staged := storage.PluginAgentRuntimeStatusRow{OperationID: operation.ID, AgentID: "edge-plugin-retry", InstanceID: "instance-retry", PluginID: operation.PluginID, Revision: 1, GenerationID: digest, PackageDigest: digest, ArtifactDigest: digest, ConfigVersion: 1}
	if err := store.StagePluginAgentRuntimeStatuses(t.Context(), []storage.PluginAgentRuntimeStatusRow{staged}); err != nil {
		t.Fatal(err)
	}
	completion := &retryLifecycleCompletion{lifecycleCompletionRecorder: &lifecycleCompletionRecorder{}, fail: true}
	reconciler, err := NewPluginLifecycleReconciler(store, completion)
	if err != nil {
		t.Fatal(err)
	}
	api := newRevisionAPITestService(t, store)
	api.SetPluginLifecycleReconciler(reconciler)
	pull, err := api.PullRemoteRevision(t.Context(), staged.AgentID)
	if err != nil || pull.Lease == nil {
		t.Fatalf("pull = %+v err=%v", pull, err)
	}
	start := RemoteRevisionStart{AgentID: staged.AgentID, Revision: 1, RetryCycle: pull.Lease.RetryCycle, Attempt: pull.Lease.Attempt, LeaseID: pull.Lease.LeaseID, GenerationID: "runtime-generation-retry"}
	if _, err := api.StartRemoteRevision(t.Context(), staged.AgentID, start); err != nil {
		t.Fatal(err)
	}
	report := RemoteRevisionReport{
		AgentID: staged.AgentID, Revision: 1, RetryCycle: pull.Lease.RetryCycle, Attempt: pull.Lease.Attempt,
		LeaseID: pull.Lease.LeaseID, GenerationID: start.GenerationID, Status: storage.AgentRevisionStateApplied,
		PluginStatuses: []storage.PluginRuntimeStatus{{
			InstanceID: staged.InstanceID, PluginID: staged.PluginID, OperationID: staged.OperationID, Revision: staged.Revision,
			GenerationID: digest, PackageDigest: digest, ArtifactDigest: digest, ConfigVersion: staged.ConfigVersion,
			RuntimeKind: "rpc-service", State: "active", Sequence: 1, SafeDetail: "runtime ready", Details: json.RawMessage(`{"healthy":true}`), Budget: json.RawMessage(`{}`),
		}},
	}
	if _, err := api.ReportRemoteRevision(t.Context(), staged.AgentID, report); err == nil {
		t.Fatal("first applied report unexpectedly completed")
	}
	statuses, err := store.ListPluginAgentRuntimeStatuses(t.Context(), operation.ID)
	if err != nil || len(statuses) != 1 || statuses[0].State != "active" || statuses[0].ReportSequence != 1 {
		t.Fatalf("persisted terminal status = %+v, %v", statuses, err)
	}
	completion.fail = false
	if _, err := api.ReportRemoteRevision(t.Context(), staged.AgentID, report); err != nil {
		t.Fatalf("replayed applied report error = %v", err)
	}
	if completion.calls != 2 || completion.kind != "lifecycle" || !completion.result.Applied {
		t.Fatalf("completion retry = calls %d kind %q result %+v", completion.calls, completion.kind, completion.result)
	}
}

func TestRevisionAPIPullKeepsIssuedPKIArtifactAfterRotation(t *testing.T) {
	store, err := newServiceTestSQLiteStore(t, t.TempDir(), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	issuedPKI := &storage.PKISecuritySnapshot{PKIDomainID: "domain", PKIEpoch: 1, SecurityRevision: 2, SignerGeneration: 1, Signature: []byte("issued")}
	rotatedPKI := &storage.PKISecuritySnapshot{PKIDomainID: "domain", PKIEpoch: 1, SecurityRevision: 3, SignerGeneration: 2, Signature: []byte("rotated")}
	seedRevisionOperation(t, store, revisionOperationSeed{
		OperationID: "op-pki-artifact", Revision: 7, Now: time.Now().UTC(),
		States: map[string]string{"edge-pki-artifact": storage.AgentRevisionStatePending},
		Snapshots: map[string]storage.Snapshot{"edge-pki-artifact": {
			Revision: 7, PluginPolicies: []storage.PluginPolicy{}, PKISecurity: issuedPKI,
		}},
	})
	repository := &rotatedPKIRevisionRepository{GormStore: store, latest: rotatedPKI}
	coord, err := coordinator.New(repository, coordinator.Options{})
	if err != nil {
		t.Fatal(err)
	}
	api := NewRevisionAPI(repository, coord)
	pull, err := api.PullRemoteRevision(t.Context(), "edge-pki-artifact")
	if err != nil {
		t.Fatal(err)
	}
	if !pull.HasUpdate || pull.Lease == nil || pull.Snapshot == nil || pull.Snapshot.PKISecurity == nil {
		t.Fatalf("pull = %+v", pull)
	}
	if pull.Snapshot.PKISecurity.SecurityRevision != issuedPKI.SecurityRevision || string(pull.Snapshot.PKISecurity.Signature) != "issued" {
		t.Fatalf("pull PKI = %+v, want immutable issued artifact", pull.Snapshot.PKISecurity)
	}
	_, digest, err := revision.CanonicalSnapshotPayload(*pull.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if digest != pull.Lease.SnapshotDigest {
		t.Fatalf("wire/artifact digest = %q / %q", digest, pull.Lease.SnapshotDigest)
	}
	if repository.latestLoads != 0 {
		t.Fatalf("latest PKI loads during immutable pull = %d", repository.latestLoads)
	}
}

func TestRevisionAPIRejectsExpiredDrainReportWithoutMutation(t *testing.T) {
	t.Parallel()
	store, err := newServiceTestSQLiteStore(t, t.TempDir(), "local")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	now := time.Now().UTC()
	finishedAt := now.Add(-time.Hour)
	seedRevisionOperation(t, store, revisionOperationSeed{
		OperationID: "op-expired-drain",
		Now:         now.Add(-2 * time.Hour),
		States:      map[string]string{"edge-expired": storage.AgentRevisionStateApplied},
		Attempts: []storage.AgentRevisionAttemptRow{{
			AgentID: "edge-expired", Revision: 1, RetryCycle: 0, Attempt: 1,
			LeaseID: "lease-expired", State: storage.AgentRevisionAttemptStateApplied,
			StartedAt: now.Add(-2 * time.Hour), DeadlineAt: now.Add(-time.Hour), FinishedAt: &finishedAt,
		}},
		Generations: []storage.AgentGenerationRow{{
			AgentID: "edge-expired", GenerationID: "generation-expired", Revision: 1,
			State: storage.GenerationStateDraining, CreatedAt: now.Add(-2 * time.Hour), UpdatedAt: now.Add(-time.Hour),
		}},
	})
	api := newRevisionAPITestService(t, store)
	report := RemoteRevisionReport{
		AgentID: "edge-expired", Revision: 1, RetryCycle: 0, Attempt: 1,
		LeaseID: "lease-expired", GenerationID: "generation-expired",
		Status: storage.AgentRevisionDrainStateDrained,
	}
	if _, err := api.ReportRemoteRevision(t.Context(), "edge-expired", report); !errors.Is(err, coordinator.ErrLeaseConflict) {
		t.Fatalf("expired drain report error = %v, want lease conflict", err)
	}
	generation, found, err := store.GetCoordinatorGeneration(t.Context(), "edge-expired", "generation-expired")
	if err != nil || !found || generation.State != storage.GenerationStateDraining || generation.DrainedAt != nil {
		t.Fatalf("generation after expired report = %+v found=%v error=%v", generation, found, err)
	}
}

type drainInterleavingStore struct {
	*storage.GormStore
	beforeDrain func()
}

func (s *drainInterleavingStore) CompleteCoordinatorDrain(ctx context.Context, request storage.CoordinatorDrainRequest) (storage.AgentRevisionRow, error) {
	if s.beforeDrain != nil {
		beforeDrain := s.beforeDrain
		s.beforeDrain = nil
		beforeDrain()
	}
	return s.GormStore.CompleteCoordinatorDrain(ctx, request)
}

type rotatedPKIRevisionRepository struct {
	*storage.GormStore
	latest      *storage.PKISecuritySnapshot
	latestLoads int
}

func (r *rotatedPKIRevisionRepository) LoadLatestPKISecuritySnapshot(context.Context) (*storage.PKISecuritySnapshot, error) {
	r.latestLoads++
	return r.latest, nil
}

type revisionOperationSeed struct {
	OperationID string
	Revision    int64
	Now         time.Time
	States      map[string]string
	Snapshots   map[string]storage.Snapshot
	Edges       []dependency.Edge
	Events      []storage.RevisionEventRow
	Attempts    []storage.AgentRevisionAttemptRow
	Generations []storage.AgentGenerationRow
}

func seedRevisionOperation(t *testing.T, store *storage.GormStore, seed revisionOperationSeed) {
	t.Helper()
	agentIDs := make([]string, 0, len(seed.States))
	for agentID := range seed.States {
		agentIDs = append(agentIDs, agentID)
	}
	sortStrings(agentIDs)
	revisionNumber := seed.Revision
	if revisionNumber <= 0 {
		revisionNumber = 1
	}
	ledger := storage.RevisionLedgerWrite{
		Operation: storage.OperationRow{
			ID: seed.OperationID, Kind: "test.mutation", Status: storage.OperationStatusPending,
			PrimaryAgentID: agentIDs[0], CreatedAt: seed.Now, UpdatedAt: seed.Now,
		},
	}
	ledger.Attempts = append(ledger.Attempts, seed.Attempts...)
	ledger.Generations = append(ledger.Generations, seed.Generations...)
	nodes := make([]dependency.Node, 0, len(agentIDs))
	for _, agentID := range agentIDs {
		snapshot := storage.Snapshot{Revision: revisionNumber}
		if seeded, ok := seed.Snapshots[agentID]; ok {
			snapshot = seeded
			snapshot.Revision = revisionNumber
		}
		payload, digest, err := revision.CanonicalSnapshotPayload(snapshot)
		if err != nil {
			t.Fatalf("CanonicalSnapshotPayload() error = %v", err)
		}
		artifactID := "snapshot-" + digest
		ledger.Artifacts = append(ledger.Artifacts, storage.GenerationArtifactRow{
			ID: artifactID, Kind: "agent_snapshot", SHA256: digest,
			Payload: payload, SizeBytes: int64(len(payload)), CreatedAt: seed.Now,
		})
		row := storage.AgentRevisionRow{
			AgentID: agentID, Revision: revisionNumber, OperationID: seed.OperationID,
			State: seed.States[agentID], SnapshotArtifactID: artifactID, SnapshotDigest: digest,
			ApplyTimeoutSeconds: 60, DrainTimeoutSeconds: 600, CreatedAt: seed.Now, UpdatedAt: seed.Now,
		}
		for _, attempt := range seed.Attempts {
			if attempt.AgentID == agentID && attempt.Revision == revisionNumber &&
				attempt.State != storage.AgentRevisionAttemptStateLeased && attempt.Attempt > row.AttemptCount {
				row.RetryCycle = attempt.RetryCycle
				row.AttemptCount = attempt.Attempt
			}
		}
		for _, generation := range seed.Generations {
			if generation.AgentID == agentID && generation.Revision == revisionNumber && generation.State == storage.GenerationStateActive {
				row.GenerationID = generation.GenerationID
			}
		}
		pointer := storage.AgentRevisionPointerRow{AgentID: agentID, DesiredRevision: revisionNumber, UpdatedAt: seed.Now}
		if row.State == storage.AgentRevisionStateApplied {
			appliedAt := seed.Now
			row.AppliedAt = &appliedAt
			pointer.AppliedRevision = revisionNumber
			pointer.LastKnownGoodRevision = revisionNumber
		}
		if row.State == storage.AgentRevisionStateFailed {
			failedAt := seed.Now
			row.FailedAt = &failedAt
			row.ErrorCode = "prepare_failed"
		}
		ledger.Revisions = append(ledger.Revisions, row)
		ledger.Pointers = append(ledger.Pointers, pointer)
		nodes = append(nodes, dependency.Node{AgentID: agentID, Revision: revisionNumber})
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
	return newRevisionAPITestServiceWithClock(t, store, nil)
}

type revisionAPITestClock struct {
	now time.Time
}

func (c revisionAPITestClock) Now() time.Time {
	return c.now
}

func newRevisionAPITestServiceWithClock(t *testing.T, store *storage.GormStore, clock coordinator.Clock) *RevisionAPI {
	t.Helper()
	coord, err := coordinator.New(store, coordinator.Options{Clock: clock})
	if err != nil {
		t.Fatalf("coordinator.New() error = %v", err)
	}
	api := NewRevisionAPI(store, coord)
	if api == nil {
		t.Fatal("NewRevisionAPI() returned nil")
	}
	if clock != nil {
		api.now = clock.Now
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
