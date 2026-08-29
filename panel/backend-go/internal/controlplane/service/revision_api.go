package service

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/coordinator"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/dependency"
	revisionpkg "github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/revision"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

const (
	PanelIdempotencyScope     = "panel"
	panelActionIdempotencyTTL = 24 * time.Hour
)

var (
	ErrRevisionNotFound  = errors.New("revision record not found")
	ErrRevisionForbidden = errors.New("revision action forbidden")
)

type RevisionRepository interface {
	GetOperation(context.Context, string) (storage.OperationRow, bool, error)
	ListOperationRevisions(context.Context, string) ([]storage.AgentRevisionRow, error)
	GetAgentRevisionPointer(context.Context, string) (storage.AgentRevisionPointerRow, bool, error)
	GetCoordinatorRevision(context.Context, string, int64) (storage.AgentRevisionRow, bool, error)
	LoadCoordinatorRuntimeSnapshot(context.Context, string, int64) (storage.CoordinatorRuntimeSnapshot, bool, error)
	ListCoordinatorAttempts(context.Context, string, int64) ([]storage.AgentRevisionAttemptRow, error)
	GetCoordinatorGeneration(context.Context, string, string) (storage.AgentGenerationRow, bool, error)
	ListCoordinatorGenerations(context.Context, string) ([]storage.AgentGenerationRow, error)
	GetOperationDependencyArtifact(context.Context, string) (storage.GenerationArtifactRow, bool, error)
	ListRevisionEvents(context.Context, storage.RevisionEventQuery) ([]storage.RevisionEventRow, error)
	GetIdempotencyRecord(context.Context, string, string) (storage.IdempotencyRecordRow, bool, error)
	UpdateIdempotencyResponseJSON(context.Context, string, string, string, string) (bool, error)
}

type reportedAgentRevisionRepository interface {
	GetAgentReportedRevision(context.Context, string) (int64, bool, error)
}

type operationDismissRepository interface {
	DismissOperation(context.Context, string, time.Time) (storage.OperationRow, bool, error)
}

type supportedSnapshotRepairStore interface {
	revisionpkg.Store
	ListAgents(context.Context) ([]storage.AgentRow, error)
	LocalAgentID() string
}

type RevisionAPI struct {
	cfg                 config.Config
	repository          RevisionRepository
	coordinator         *coordinator.Coordinator
	snapshotRepairStore supportedSnapshotRepairStore
	pluginLifecycle     *PluginLifecycleReconciler
	now                 func() time.Time
}

func NewRevisionAPI(repository RevisionRepository, revisionCoordinator *coordinator.Coordinator) *RevisionAPI {
	return newRevisionAPI(config.Config{}, repository, revisionCoordinator)
}

func newRevisionAPI(cfg config.Config, repository RevisionRepository, revisionCoordinator *coordinator.Coordinator) *RevisionAPI {
	if repository == nil || revisionCoordinator == nil {
		return nil
	}
	api := &RevisionAPI{cfg: cfg, repository: repository, coordinator: revisionCoordinator, now: time.Now}
	if repairStore, ok := repository.(supportedSnapshotRepairStore); ok {
		api.snapshotRepairStore = repairStore
	}
	return api
}

type remoteLeasePhase int

const (
	remoteLeasePhaseLeased remoteLeasePhase = iota
	remoteLeasePhaseStarted
	remoteLeasePhaseApply
	remoteLeasePhaseDrain
)

type OperationStatus struct {
	OperationID            string                `json:"operation_id"`
	Kind                   string                `json:"kind"`
	ApplyStatus            string                `json:"apply_status"`
	PrimaryAgent           string                `json:"primary_agent_id"`
	NoOp                   bool                  `json:"no_op"`
	Degraded               bool                  `json:"degraded"`
	ErrorCode              string                `json:"error_code,omitempty"`
	ErrorMessage           string                `json:"error_message,omitempty"`
	Agents                 []AgentRevisionStatus `json:"agents"`
	CreatedAt              time.Time             `json:"created_at"`
	UpdatedAt              time.Time             `json:"updated_at"`
	CompletedAt            *time.Time            `json:"completed_at,omitempty"`
	Replayed               bool                  `json:"-"`
	HTTPRequestFingerprint string                `json:"-"`
}

type AgentRevisionStatus struct {
	OperationID            string               `json:"operation_id"`
	AgentID                string               `json:"agent_id"`
	DesiredRevision        int64                `json:"desired_revision"`
	AppliedRevision        int64                `json:"applied_revision"`
	LastKnownGoodRevision  int64                `json:"last_known_good_revision"`
	ApplyStatus            string               `json:"apply_status"`
	DrainStatus            string               `json:"drain_status,omitempty"`
	BlockedBy              []string             `json:"blocked_by"`
	NoOp                   bool                 `json:"no_op"`
	RetryCycle             int                  `json:"retry_cycle"`
	AttemptCount           int                  `json:"attempt_count"`
	NextAttemptAt          *time.Time           `json:"next_attempt_at,omitempty"`
	GenerationID           string               `json:"generation_id,omitempty"`
	ErrorCode              string               `json:"error_code,omitempty"`
	ErrorMessage           string               `json:"error_message,omitempty"`
	Attempts               []RevisionAttempt    `json:"attempts"`
	Generations            []RevisionGeneration `json:"generations"`
	CreatedAt              time.Time            `json:"created_at"`
	UpdatedAt              time.Time            `json:"updated_at"`
	AppliedAt              *time.Time           `json:"applied_at,omitempty"`
	FailedAt               *time.Time           `json:"failed_at,omitempty"`
	Replayed               bool                 `json:"-"`
	HTTPRequestFingerprint string               `json:"-"`
}

type RevisionAttempt struct {
	RetryCycle   int        `json:"retry_cycle"`
	Attempt      int        `json:"attempt"`
	State        string     `json:"state"`
	StartedAt    time.Time  `json:"started_at"`
	DeadlineAt   time.Time  `json:"deadline_at"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
	ErrorCode    string     `json:"error_code,omitempty"`
	ErrorMessage string     `json:"error_message,omitempty"`
}

type RevisionGeneration struct {
	GenerationID string     `json:"generation_id"`
	Revision     int64      `json:"revision"`
	State        string     `json:"state"`
	SessionCount int64      `json:"session_count"`
	Forced       bool       `json:"forced"`
	ForceReason  string     `json:"force_reason,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	DrainedAt    *time.Time `json:"drained_at,omitempty"`
}

type RevisionEventQuery struct {
	AfterID     uint64
	Limit       int
	OperationID string
	AgentID     string
}

type RevisionEvent struct {
	ID          uint64         `json:"id"`
	OperationID string         `json:"operation_id"`
	AgentID     string         `json:"agent_id"`
	Revision    int64          `json:"revision"`
	EventType   string         `json:"event_type"`
	Payload     map[string]any `json:"payload"`
	CreatedAt   time.Time      `json:"created_at"`
}

type RevisionEventPage struct {
	Events     []RevisionEvent `json:"events"`
	NextCursor uint64          `json:"next_cursor"`
	HasMore    bool            `json:"has_more"`
}

type RemoteRevisionLease struct {
	AgentID             string    `json:"agent_id"`
	Revision            int64     `json:"revision"`
	RetryCycle          int       `json:"retry_cycle"`
	Attempt             int       `json:"attempt"`
	LeaseID             string    `json:"lease_id"`
	SnapshotDigest      string    `json:"snapshot_digest"`
	DesiredVersion      string    `json:"desired_version"`
	ApplyTimeoutSeconds int       `json:"apply_timeout_seconds"`
	DrainTimeoutSeconds int       `json:"drain_timeout_seconds"`
	DeadlineAt          time.Time `json:"deadline_at"`
}

type RemoteRevisionPull struct {
	HasUpdate       bool                 `json:"has_update"`
	DesiredRevision int64                `json:"desired_revision"`
	Lease           *RemoteRevisionLease `json:"lease,omitempty"`
	Snapshot        *storage.Snapshot    `json:"snapshot,omitempty"`
}

type RemoteRevisionStart struct {
	AgentID      string `json:"agent_id"`
	Revision     int64  `json:"revision"`
	RetryCycle   int    `json:"retry_cycle"`
	Attempt      int    `json:"attempt"`
	LeaseID      string `json:"lease_id"`
	GenerationID string `json:"generation_id"`
}

type RemoteRevisionReport struct {
	AgentID        string                           `json:"agent_id"`
	Revision       int64                            `json:"revision"`
	RetryCycle     int                              `json:"retry_cycle"`
	Attempt        int                              `json:"attempt"`
	LeaseID        string                           `json:"lease_id"`
	GenerationID   string                           `json:"generation_id"`
	Status         string                           `json:"status"`
	ErrorCode      string                           `json:"error_code,omitempty"`
	ErrorMessage   string                           `json:"error_message,omitempty"`
	Forced         bool                             `json:"forced,omitempty"`
	ForceReason    string                           `json:"force_reason,omitempty"`
	PluginStatuses []storage.PluginRuntimeStatus    `json:"plugin_statuses,omitempty"`
	PluginLogs     []storage.PluginRuntimeLogReport `json:"plugin_logs,omitempty"`
}

func (s *RevisionAPI) SetPluginLifecycleReconciler(reconciler *PluginLifecycleReconciler) {
	if s != nil {
		s.pluginLifecycle = reconciler
	}
}

func (s *RevisionAPI) reconcileStartup(ctx context.Context) error {
	if s == nil || s.coordinator == nil {
		return nil
	}
	_, coordinatorErr := s.coordinator.ReconcileStartup(ctx)
	var pluginErr error
	if s.pluginLifecycle != nil {
		pluginErr = s.pluginLifecycle.RecoverSupersededOperations(
			WithSystemMutationPrincipal(ctx, "system:revision-reconciler"),
		)
	}
	return errors.Join(coordinatorErr, pluginErr)
}

func (s *RevisionAPI) GetOperationStatus(ctx context.Context, operationID string) (OperationStatus, error) {
	operationID = strings.TrimSpace(operationID)
	if operationID == "" {
		return OperationStatus{}, fmt.Errorf("%w: operation id is required", ErrInvalidArgument)
	}
	operation, found, err := s.repository.GetOperation(ctx, operationID)
	if err != nil {
		return OperationStatus{}, err
	}
	if !found {
		return OperationStatus{}, fmt.Errorf("%w: operation %q", ErrRevisionNotFound, operationID)
	}
	if operation.CompletedAt != nil && (operation.Status == storage.OperationStatusPending || operation.Status == storage.OperationStatusApplying) {
		return OperationStatus{
			OperationID: operation.ID, Kind: operation.Kind, ApplyStatus: operation.Status,
			PrimaryAgent: operation.PrimaryAgentID, NoOp: operation.NoOp,
			ErrorCode: operation.ErrorCode, ErrorMessage: operation.ErrorMessage,
			CreatedAt: operation.CreatedAt, UpdatedAt: operation.UpdatedAt, CompletedAt: operation.CompletedAt,
		}, nil
	}
	revisions, err := s.repository.ListOperationRevisions(ctx, operationID)
	if err != nil {
		return OperationStatus{}, err
	}
	refs, err := s.operationAgentRevisionRefs(ctx, operation, revisions)
	if err != nil {
		return OperationStatus{}, err
	}

	blockedBy := make(map[string][]string)
	applyStatus := ""
	if _, hasPlan, planErr := s.repository.GetOperationDependencyArtifact(ctx, operationID); planErr != nil {
		return OperationStatus{}, planErr
	} else if hasPlan {
		evaluation, evalErr := s.coordinator.EvaluateDependencyOperation(ctx, operationID)
		if evalErr != nil {
			return OperationStatus{}, evalErr
		}
		applyStatus = string(evaluation.Status)
		for _, result := range evaluation.Nodes {
			blockedBy[result.Node.AgentID] = append([]string(nil), result.BlockedBy...)
		}
	}

	agents := make([]AgentRevisionStatus, 0, len(refs))
	for _, ref := range refs {
		status, statusErr := s.getAgentRevisionStatus(ctx, ref.AgentID, ref.Revision, blockedBy[ref.AgentID])
		if statusErr != nil {
			return OperationStatus{}, statusErr
		}
		status.NoOp = operation.NoOp && status.OperationID != operationID
		if operation.NoOp {
			status.NoOp = true
			status.OperationID = operationID
		}
		agents = append(agents, status)
	}
	if applyStatus == "" {
		applyStatus = aggregateRevisionStatus(agents, operation.Status)
	}
	return OperationStatus{
		OperationID: operation.ID, Kind: operation.Kind, ApplyStatus: applyStatus,
		PrimaryAgent: operation.PrimaryAgentID, NoOp: operation.NoOp,
		Degraded:  applyStatus == string(dependency.StatusDegraded),
		ErrorCode: operation.ErrorCode, ErrorMessage: operation.ErrorMessage,
		Agents: agents, CreatedAt: operation.CreatedAt, UpdatedAt: operation.UpdatedAt,
		CompletedAt: operation.CompletedAt,
	}, nil
}

func (s *RevisionAPI) DismissOperation(ctx context.Context, operationID string) (OperationStatus, error) {
	operationID = strings.TrimSpace(operationID)
	if operationID == "" {
		return OperationStatus{}, fmt.Errorf("%w: operation id is required", ErrInvalidArgument)
	}
	repository, ok := s.repository.(operationDismissRepository)
	if !ok {
		return OperationStatus{}, errors.New("operation dismissal is unavailable")
	}
	_, found, err := repository.DismissOperation(ctx, operationID, s.now().UTC())
	if err != nil {
		return OperationStatus{}, err
	}
	if !found {
		return OperationStatus{}, fmt.Errorf("%w: operation %q", ErrRevisionNotFound, operationID)
	}
	return s.GetOperationStatus(ctx, operationID)
}

func (s *RevisionAPI) GetAgentRevisionStatus(ctx context.Context, agentID string, revision int64) (AgentRevisionStatus, error) {
	return s.getAgentRevisionStatus(ctx, strings.TrimSpace(agentID), revision, nil)
}

func (s *RevisionAPI) getAgentRevisionStatus(ctx context.Context, agentID string, revision int64, blockedBy []string) (AgentRevisionStatus, error) {
	if agentID == "" || revision < 0 {
		return AgentRevisionStatus{}, fmt.Errorf("%w: agent id and revision are required", ErrInvalidArgument)
	}
	row, found, err := s.repository.GetCoordinatorRevision(ctx, agentID, revision)
	if err != nil {
		return AgentRevisionStatus{}, err
	}
	if !found {
		return AgentRevisionStatus{}, fmt.Errorf("%w: revision %s/%d", ErrRevisionNotFound, agentID, revision)
	}
	pointer, pointerFound, err := s.repository.GetAgentRevisionPointer(ctx, agentID)
	if err != nil {
		return AgentRevisionStatus{}, err
	}
	if !pointerFound {
		switch row.State {
		case storage.AgentRevisionStateApplied:
			pointer.AppliedRevision = row.Revision
			pointer.LastKnownGoodRevision = row.Revision
		case storage.AgentRevisionStateFailed, storage.AgentRevisionStateSuperseded:
			// Deleted agents no longer have a live pointer, but their immutable
			// terminal revision history remains queryable.
		default:
			return AgentRevisionStatus{}, fmt.Errorf("%w: pointer for agent %q", ErrRevisionNotFound, agentID)
		}
	}
	attemptRows, err := s.repository.ListCoordinatorAttempts(ctx, agentID, revision)
	if err != nil {
		return AgentRevisionStatus{}, err
	}
	attempts := make([]RevisionAttempt, 0, len(attemptRows))
	for _, attempt := range attemptRows {
		attempts = append(attempts, RevisionAttempt{
			RetryCycle: attempt.RetryCycle, Attempt: attempt.Attempt, State: attempt.State,
			StartedAt: attempt.StartedAt, DeadlineAt: attempt.DeadlineAt, FinishedAt: attempt.FinishedAt,
			ErrorCode: attempt.ErrorCode, ErrorMessage: attempt.Error,
		})
	}
	generationRows, err := s.repository.ListCoordinatorGenerations(ctx, agentID)
	if err != nil {
		return AgentRevisionStatus{}, err
	}
	generations := make([]RevisionGeneration, 0, len(generationRows))
	for _, generation := range generationRows {
		if generation.Revision != revision &&
			generation.State != storage.GenerationStateDraining &&
			generation.State != storage.AgentRevisionDrainStateForced {
			continue
		}
		generations = append(generations, RevisionGeneration{
			GenerationID: generation.GenerationID, Revision: generation.Revision, State: generation.State,
			SessionCount: generation.SessionCount, Forced: generation.Forced, ForceReason: generation.ForceReason,
			CreatedAt: generation.CreatedAt, UpdatedAt: generation.UpdatedAt, DrainedAt: generation.DrainedAt,
		})
	}
	return AgentRevisionStatus{
		OperationID: row.OperationID, AgentID: row.AgentID, DesiredRevision: row.Revision,
		AppliedRevision: pointer.AppliedRevision, LastKnownGoodRevision: pointer.LastKnownGoodRevision,
		ApplyStatus: row.State, DrainStatus: row.DrainState,
		BlockedBy: append([]string(nil), blockedBy...), RetryCycle: row.RetryCycle,
		AttemptCount: row.AttemptCount, NextAttemptAt: row.NextAttemptAt,
		GenerationID: row.GenerationID, ErrorCode: row.ErrorCode, ErrorMessage: row.ErrorMessage,
		Attempts: attempts, Generations: generations, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		AppliedAt: row.AppliedAt, FailedAt: row.FailedAt,
	}, nil
}

func (s *RevisionAPI) Retry(ctx context.Context, agentID string, revision int64) (AgentRevisionStatus, error) {
	agentID = strings.TrimSpace(agentID)
	idempotency, err := s.panelActionIdempotency(ctx, "revision.retry", agentID, revision)
	if err != nil {
		return AgentRevisionStatus{}, err
	}
	result, err := s.coordinator.RetryIdempotent(ctx, agentID, revision, idempotency)
	if err != nil {
		return AgentRevisionStatus{}, err
	}
	status, err := s.GetAgentRevisionStatus(ctx, agentID, revision)
	if err != nil {
		return AgentRevisionStatus{}, err
	}
	status.Replayed = result.Replayed
	status.HTTPRequestFingerprint = idempotency.HTTPRequestFingerprint
	return status, nil
}

func (s *RevisionAPI) Rollback(ctx context.Context, agentID string) (OperationStatus, error) {
	agentID = strings.TrimSpace(agentID)
	idempotency, err := s.panelActionIdempotency(ctx, "revision.rollback", agentID, 0)
	if err != nil {
		return OperationStatus{}, err
	}
	result, err := s.coordinator.RollbackIdempotent(ctx, agentID, idempotency)
	if err != nil {
		return OperationStatus{}, err
	}
	status, err := s.GetOperationStatus(ctx, result.Operation.ID)
	if err != nil {
		return OperationStatus{}, err
	}
	status.Replayed = result.Replayed
	status.HTTPRequestFingerprint = idempotency.HTTPRequestFingerprint
	return status, nil
}

func (s *RevisionAPI) ListEvents(ctx context.Context, query RevisionEventQuery) (RevisionEventPage, error) {
	limit := query.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	rows, err := s.repository.ListRevisionEvents(ctx, storage.RevisionEventQuery{
		AfterID: query.AfterID, Limit: limit + 1,
		OperationID: strings.TrimSpace(query.OperationID), AgentID: strings.TrimSpace(query.AgentID),
	})
	if err != nil {
		return RevisionEventPage{}, err
	}
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	events := make([]RevisionEvent, 0, len(rows))
	for _, row := range rows {
		payload := map[string]any{}
		if strings.TrimSpace(row.PayloadJSON) != "" {
			if err := json.Unmarshal([]byte(row.PayloadJSON), &payload); err != nil {
				return RevisionEventPage{}, fmt.Errorf("decode revision event %d: %w", row.ID, err)
			}
		}
		delete(payload, "lease_id")
		events = append(events, RevisionEvent{
			ID: row.ID, OperationID: row.OperationID, AgentID: row.AgentID,
			Revision: row.Revision, EventType: row.EventType, Payload: payload, CreatedAt: row.CreatedAt,
		})
	}
	nextCursor := query.AfterID
	if len(events) > 0 {
		nextCursor = events[len(events)-1].ID
	}
	return RevisionEventPage{Events: events, NextCursor: nextCursor, HasMore: hasMore}, nil
}

func (s *RevisionAPI) PullRemoteRevision(ctx context.Context, agentID string) (RemoteRevisionPull, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return RemoteRevisionPull{}, fmt.Errorf("%w: agent id is required", ErrInvalidArgument)
	}
	pointer, found, err := s.repository.GetAgentRevisionPointer(ctx, agentID)
	if err != nil {
		return RemoteRevisionPull{}, err
	}
	if found && pointer.DesiredRevision <= pointer.AppliedRevision {
		pointer, found, err = s.repairRemoteRuntimeRevision(ctx, agentID, pointer)
		if err != nil {
			return RemoteRevisionPull{}, err
		}
	}
	if !found || pointer.DesiredRevision <= pointer.AppliedRevision {
		return RemoteRevisionPull{DesiredRevision: pointer.DesiredRevision}, nil
	}
	runtimeSnapshot, found, err := s.repository.LoadCoordinatorRuntimeSnapshot(ctx, agentID, pointer.DesiredRevision)
	if err != nil {
		return RemoteRevisionPull{}, err
	}
	if !found {
		return RemoteRevisionPull{}, fmt.Errorf("%w: revision %s/%d", ErrRevisionNotFound, agentID, pointer.DesiredRevision)
	}
	if runtimeSnapshot.RequiresNewRevision {
		pointer, found, err = s.repairSupportedSnapshotOperation(ctx, runtimeSnapshot.Revision)
		if err != nil {
			return RemoteRevisionPull{}, err
		}
		if !found || pointer.DesiredRevision <= pointer.AppliedRevision {
			return RemoteRevisionPull{DesiredRevision: pointer.DesiredRevision}, nil
		}
		runtimeSnapshot, found, err = s.repository.LoadCoordinatorRuntimeSnapshot(ctx, agentID, pointer.DesiredRevision)
		if err != nil {
			return RemoteRevisionPull{}, err
		}
		if !found {
			return RemoteRevisionPull{}, fmt.Errorf("%w: revision %s/%d", ErrRevisionNotFound, agentID, pointer.DesiredRevision)
		}
		if runtimeSnapshot.RequiresNewRevision {
			return RemoteRevisionPull{}, fmt.Errorf("revision %s/%d still requires a new snapshot identity", agentID, pointer.DesiredRevision)
		}
	}
	revisionRow := runtimeSnapshot.Revision
	snapshot := runtimeSnapshot.Snapshot
	if lease, found, err := s.currentRemoteLease(ctx, revisionRow); err != nil {
		return RemoteRevisionPull{}, err
	} else if found {
		remoteLease := remoteLeaseFromCoordinator(lease)
		return RemoteRevisionPull{
			HasUpdate: true, DesiredRevision: pointer.DesiredRevision, Lease: &remoteLease, Snapshot: &snapshot,
		}, nil
	}
	var claim coordinator.ClaimResult
	if _, hasPlan, planErr := s.repository.GetOperationDependencyArtifact(ctx, revisionRow.OperationID); planErr != nil {
		return RemoteRevisionPull{}, planErr
	} else if hasPlan {
		claimed, claimErr := s.coordinator.ClaimDependencyAgent(ctx, revisionRow.OperationID, agentID)
		if claimErr != nil {
			return RemoteRevisionPull{}, claimErr
		}
		if !claimed.Eligible {
			return RemoteRevisionPull{DesiredRevision: pointer.DesiredRevision}, nil
		}
		claim = claimed.Claim
	} else {
		claim, err = s.coordinator.Claim(ctx, agentID)
		if err != nil {
			return RemoteRevisionPull{}, err
		}
	}
	if claim.Lease == nil {
		return RemoteRevisionPull{DesiredRevision: pointer.DesiredRevision}, nil
	}
	if claim.Lease.SnapshotArtifactID != revisionRow.SnapshotArtifactID ||
		!strings.EqualFold(claim.Lease.SnapshotDigest, revisionRow.SnapshotDigest) {
		return RemoteRevisionPull{}, fmt.Errorf("claimed revision snapshot does not match desired revision")
	}
	lease := remoteLeaseFromCoordinator(*claim.Lease)
	return RemoteRevisionPull{
		HasUpdate: true, DesiredRevision: pointer.DesiredRevision, Lease: &lease, Snapshot: &snapshot,
	}, nil
}

func (s *RevisionAPI) repairSupportedSnapshotOperation(
	ctx context.Context,
	source storage.AgentRevisionRow,
) (storage.AgentRevisionPointerRow, bool, error) {
	if s.snapshotRepairStore == nil {
		return storage.AgentRevisionPointerRow{}, false, fmt.Errorf("snapshot repair mutation store is unavailable")
	}
	sourceOperationID := strings.TrimSpace(source.OperationID)
	if sourceOperationID == "" {
		return storage.AgentRevisionPointerRow{}, false, fmt.Errorf("source revision operation id is required")
	}
	currentPointer, found, err := s.repository.GetAgentRevisionPointer(ctx, source.AgentID)
	if err != nil {
		return storage.AgentRevisionPointerRow{}, false, err
	}
	if found && currentPointer.DesiredRevision > source.Revision {
		return currentPointer, true, nil
	}
	revisions, err := s.repository.ListOperationRevisions(ctx, sourceOperationID)
	if err != nil {
		return storage.AgentRevisionPointerRow{}, false, err
	}
	if len(revisions) == 0 {
		return storage.AgentRevisionPointerRow{}, false, fmt.Errorf("%w: operation %s", ErrRevisionNotFound, sourceOperationID)
	}

	agents, err := s.snapshotRepairStore.ListAgents(ctx)
	if err != nil {
		return storage.AgentRevisionPointerRow{}, false, err
	}
	registeredAgents := make(map[string]storage.AgentRow, len(agents))
	for _, agent := range agents {
		registeredAgents[strings.TrimSpace(agent.ID)] = agent
	}

	revisionsByAgent := make(map[string]storage.AgentRevisionRow, len(revisions))
	agentQueue := make([]string, 0, len(revisions))
	for _, row := range revisions {
		agentID := strings.TrimSpace(row.AgentID)
		if _, exists := revisionsByAgent[agentID]; exists {
			return storage.AgentRevisionPointerRow{}, false, fmt.Errorf("operation %q contains multiple revisions for agent %q", sourceOperationID, agentID)
		}
		revisionsByAgent[agentID] = row
		agentQueue = append(agentQueue, agentID)
	}

	expectedDesired := make(map[string]int64, len(revisionsByAgent))
	for index := 0; index < len(agentQueue); index++ {
		agentID := agentQueue[index]
		pointer, found, err := s.repository.GetAgentRevisionPointer(ctx, agentID)
		if err != nil {
			return storage.AgentRevisionPointerRow{}, false, err
		}
		if !found {
			return storage.AgentRevisionPointerRow{}, false, fmt.Errorf("%w: revision pointer for agent %s", ErrRevisionNotFound, agentID)
		}
		expectedDesired[agentID] = pointer.DesiredRevision
		if pointer.DesiredRevision <= 0 {
			continue
		}
		current, found, err := s.repository.GetCoordinatorRevision(ctx, agentID, pointer.DesiredRevision)
		if err != nil {
			return storage.AgentRevisionPointerRow{}, false, err
		}
		if !found {
			return storage.AgentRevisionPointerRow{}, false, fmt.Errorf("%w: revision %s/%d", ErrRevisionNotFound, agentID, pointer.DesiredRevision)
		}
		revisionsByAgent[agentID] = current
		currentOperationID := strings.TrimSpace(current.OperationID)
		if currentOperationID == "" {
			continue
		}
		peers, err := s.repository.ListOperationRevisions(ctx, currentOperationID)
		if err != nil {
			return storage.AgentRevisionPointerRow{}, false, err
		}
		for _, peer := range peers {
			peerID := strings.TrimSpace(peer.AgentID)
			if peerID == "" {
				return storage.AgentRevisionPointerRow{}, false, fmt.Errorf("operation %q contains an empty agent id", currentOperationID)
			}
			if _, exists := revisionsByAgent[peerID]; exists {
				continue
			}
			revisionsByAgent[peerID] = peer
			agentQueue = append(agentQueue, peerID)
		}
	}

	localAgentID := strings.TrimSpace(s.snapshotRepairStore.LocalAgentID())
	targets := make([]revisionpkg.Target, 0, len(revisionsByAgent))
	for agentID, row := range revisionsByAgent {
		agent, registered := registeredAgents[agentID]
		local := agentID == localAgentID || (registered && agent.IsLocal)
		if !registered && !local {
			continue
		}
		platform := agent.Platform
		var capabilities []string
		if local {
			capabilities = append([]string(nil), defaultLocalCapabilities...)
			if strings.TrimSpace(platform) == "" {
				platform = runtime.GOOS + "-" + runtime.GOARCH
			}
		}
		targets = append(targets, revisionpkg.Target{
			AgentID: agentID, Local: local, Platform: platform, Capabilities: capabilities,
			DesiredVersion:      row.DesiredVersion,
			ApplyTimeoutSeconds: row.ApplyTimeoutSeconds,
			DrainTimeoutSeconds: row.DrainTimeoutSeconds,
		})
	}

	executor := newRevisionExecutor(s.cfg, s.snapshotRepairStore)
	_, err = executor.Execute(ctx, revisionpkg.MutationRequest{
		Kind:             "repair_supported_snapshot",
		DependencyAction: revisionpkg.DependencyActionApply,
		ForceRevision:    true,
		IdempotencyScope: "revision_snapshot_repair",
		IdempotencyKey:   sourceOperationID,
		Request: map[string]any{
			"source_operation_id": sourceOperationID,
		},
		Targets: targets,
		ResourceState: func(ctx context.Context, tx *storage.GormStore, target revisionpkg.Target) (any, error) {
			pointer, found, err := tx.GetAgentRevisionPointer(ctx, target.AgentID)
			if err != nil {
				return nil, err
			}
			expected := expectedDesired[target.AgentID]
			if !found || pointer.DesiredRevision != expected {
				return nil, fmt.Errorf("agent %q desired revision changed during snapshot repair", target.AgentID)
			}
			return map[string]any{
				"source_operation_id": sourceOperationID,
				"desired_revision":    expected,
			}, nil
		},
		Mutate: func(context.Context, *storage.GormStore, map[string]int64) error {
			return nil
		},
	})
	if err != nil {
		pointer, found, pointerErr := s.repository.GetAgentRevisionPointer(ctx, source.AgentID)
		if pointerErr == nil && found && pointer.DesiredRevision > source.Revision {
			return pointer, true, nil
		}
		return storage.AgentRevisionPointerRow{}, false, err
	}
	return s.repository.GetAgentRevisionPointer(ctx, source.AgentID)
}

func (s *RevisionAPI) repairRemoteRuntimeRevision(
	ctx context.Context,
	agentID string,
	pointer storage.AgentRevisionPointerRow,
) (storage.AgentRevisionPointerRow, bool, error) {
	repository, ok := s.repository.(reportedAgentRevisionRepository)
	if !ok || pointer.AppliedRevision <= 0 {
		return pointer, true, nil
	}
	reportedRevision, found, err := repository.GetAgentReportedRevision(ctx, agentID)
	if err != nil || !found || reportedRevision >= pointer.AppliedRevision {
		return pointer, true, err
	}
	repaired, err := s.coordinator.RepairRuntime(ctx, agentID, pointer.AppliedRevision)
	if err == nil {
		return repaired.Pointer, true, nil
	}
	if !errors.Is(err, coordinator.ErrStateConflict) {
		return storage.AgentRevisionPointerRow{}, false, err
	}
	// Another mutation or concurrent repair advanced the pointer after this
	// pull observed it. Reload and deliver that authoritative revision.
	return s.repository.GetAgentRevisionPointer(ctx, agentID)
}

func (s *RevisionAPI) StartRemoteRevision(ctx context.Context, agentID string, input RemoteRevisionStart) (AgentRevisionStatus, error) {
	if err := requireRemoteAgentIdentity(agentID, input.AgentID); err != nil {
		return AgentRevisionStatus{}, err
	}
	lease, err := s.loadAuthoritativeLease(ctx, agentID, input.Revision, input.RetryCycle, input.Attempt, input.LeaseID, remoteLeasePhaseLeased)
	if err != nil {
		return AgentRevisionStatus{}, err
	}
	generationID := strings.TrimSpace(input.GenerationID)
	if generationID == "" {
		return AgentRevisionStatus{}, fmt.Errorf("%w: generation id is required", ErrInvalidArgument)
	}
	if _, err := s.coordinator.Start(ctx, coordinator.StartRequest{Lease: lease, GenerationID: generationID}); err != nil {
		return AgentRevisionStatus{}, err
	}
	return s.GetAgentRevisionStatus(ctx, agentID, input.Revision)
}

func (s *RevisionAPI) ReportRemoteRevision(ctx context.Context, agentID string, input RemoteRevisionReport) (AgentRevisionStatus, error) {
	if err := requireRemoteAgentIdentity(agentID, input.AgentID); err != nil {
		return AgentRevisionStatus{}, err
	}
	generationID := strings.TrimSpace(input.GenerationID)
	if generationID == "" {
		return AgentRevisionStatus{}, fmt.Errorf("%w: generation id is required", ErrInvalidArgument)
	}
	switch strings.ToLower(strings.TrimSpace(input.Status)) {
	case storage.AgentRevisionStateFailed:
		lease, err := s.loadAuthoritativeLease(ctx, agentID, input.Revision, input.RetryCycle, input.Attempt, input.LeaseID, remoteLeasePhaseStarted)
		if err != nil {
			return AgentRevisionStatus{}, err
		}
		if _, err := s.coordinator.Fail(ctx, coordinator.FailureReport{
			Lease: lease, GenerationID: generationID,
			ErrorCode: strings.TrimSpace(input.ErrorCode), ErrorMessage: strings.TrimSpace(input.ErrorMessage),
		}); err != nil {
			return AgentRevisionStatus{}, err
		}
		if err := s.reconcilePluginRevisionReport(ctx, agentID, input, storage.AgentRevisionStateFailed); err != nil {
			return AgentRevisionStatus{}, err
		}
	case storage.AgentRevisionStateApplied, storage.AgentRevisionDrainStateDraining:
		lease, err := s.loadAuthoritativeLease(ctx, agentID, input.Revision, input.RetryCycle, input.Attempt, input.LeaseID, remoteLeasePhaseApply)
		if err != nil {
			return AgentRevisionStatus{}, err
		}
		if _, err := s.coordinator.Applied(ctx, coordinator.AppliedReport{Lease: lease, GenerationID: generationID}); err != nil {
			return AgentRevisionStatus{}, err
		}
		if err := s.reconcilePluginRevisionReport(ctx, agentID, input, storage.AgentRevisionStateApplied); err != nil {
			return AgentRevisionStatus{}, err
		}
	case storage.AgentRevisionDrainStateDrained, storage.AgentRevisionDrainStateForced:
		lease, err := s.loadAuthoritativeLease(ctx, agentID, input.Revision, input.RetryCycle, input.Attempt, input.LeaseID, remoteLeasePhaseDrain)
		if err != nil {
			return AgentRevisionStatus{}, err
		}
		forced := input.Forced || strings.EqualFold(input.Status, storage.AgentRevisionDrainStateForced)
		row, err := s.coordinator.Drained(ctx, coordinator.DrainReport{
			Lease: lease, GenerationID: generationID, Forced: forced,
			ForceReason: strings.TrimSpace(input.ForceReason),
		})
		if err != nil {
			return AgentRevisionStatus{}, err
		}
		if err := s.reconcilePluginRevisionReport(ctx, agentID, input, "drained"); err != nil {
			return AgentRevisionStatus{}, err
		}
		return s.GetAgentRevisionStatus(ctx, agentID, row.Revision)
	default:
		return AgentRevisionStatus{}, fmt.Errorf("%w: unsupported revision report status %q", ErrInvalidArgument, input.Status)
	}
	return s.GetAgentRevisionStatus(ctx, agentID, input.Revision)
}

func (s *RevisionAPI) reconcilePluginRevisionReport(ctx context.Context, agentID string, input RemoteRevisionReport, terminalState string) error {
	if s == nil || s.pluginLifecycle == nil {
		return nil
	}
	if logStore, ok := s.pluginLifecycle.store.(interface {
		RecordPluginRuntimeLogReport(context.Context, string, storage.PluginRuntimeLogReport) (bool, error)
	}); ok {
		for _, report := range input.PluginLogs {
			if _, err := logStore.RecordPluginRuntimeLogReport(ctx, agentID, report); err != nil {
				if pluginGenerationReportIgnorable(err) {
					continue
				}
				return err
			}
		}
	} else if len(input.PluginLogs) > 0 {
		return errors.New("plugin runtime log ingestion is unavailable")
	}
	revisionRow, found, err := s.repository.GetCoordinatorRevision(ctx, agentID, input.Revision)
	if err != nil || !found {
		return err
	}
	for _, status := range input.PluginStatuses {
		report := storage.PluginGenerationReport{
			OperationID: status.OperationID, AgentID: agentID, InstanceID: status.InstanceID, PluginID: status.PluginID,
			Revision: status.Revision, GenerationID: status.GenerationID, PackageDigest: status.PackageDigest,
			ArtifactDigest: status.ArtifactDigest, State: status.State, Sequence: status.Sequence,
			ErrorCode: status.ErrorCode, SafeDetail: status.SafeDetail,
			Details: append(json.RawMessage(nil), status.Details...), Budget: append(json.RawMessage(nil), status.Budget...),
		}
		if _, err := s.pluginLifecycle.Reconcile(ctx, report, agentID); err != nil && !pluginGenerationReportIgnorable(err) {
			return err
		}
	}
	rows, err := s.pluginLifecycle.store.ListPluginAgentRuntimeStatuses(ctx, revisionRow.OperationID)
	if err != nil {
		return err
	}
	expected := make(map[string]storage.PluginAgentRuntimeStatusRow)
	for _, row := range rows {
		if row.AgentID == agentID && row.Revision == input.Revision {
			expected[row.InstanceID] = row
		}
	}
	if len(expected) == 0 {
		return s.reconcilePluginRevisionWithoutRuntimeStatuses(ctx, revisionRow.OperationID)
	}
	reported := make(map[string]storage.PluginRuntimeStatus, len(input.PluginStatuses))
	for _, status := range input.PluginStatuses {
		if _, duplicate := reported[status.InstanceID]; duplicate {
			return storage.ErrPluginGenerationConflict
		}
		reported[status.InstanceID] = status
	}
	for instanceID, row := range expected {
		if (row.State == "active" || row.State == "drained" || row.State == "failed" || row.State == "degraded") && row.ReportSequence > 0 {
			report := storage.PluginGenerationReport{
				OperationID: row.OperationID, AgentID: row.AgentID, InstanceID: row.InstanceID, PluginID: row.PluginID,
				Revision: row.Revision, GenerationID: row.GenerationID, PackageDigest: row.PackageDigest,
				ArtifactDigest: row.ArtifactDigest, State: row.State, Sequence: row.ReportSequence,
				ErrorCode: row.ErrorCode, Details: json.RawMessage(row.DetailsJSON), Budget: json.RawMessage(row.BudgetJSON),
			}
			if _, err := s.pluginLifecycle.Reconcile(ctx, report, agentID); err != nil && !pluginGenerationReportIgnorable(err) {
				return err
			}
			continue
		}
		report := storage.PluginGenerationReport{
			OperationID: row.OperationID, AgentID: agentID, InstanceID: row.InstanceID, PluginID: row.PluginID,
			Revision: row.Revision, GenerationID: row.GenerationID, PackageDigest: row.PackageDigest,
			ArtifactDigest: row.ArtifactDigest, Sequence: row.ReportSequence + 1,
		}
		switch terminalState {
		case storage.AgentRevisionStateApplied:
			if row.State == "draining" {
				continue
			}
			status, ok := reported[instanceID]
			if !ok || status.OperationID != row.OperationID || status.PluginID != row.PluginID ||
				status.Revision != row.Revision || status.GenerationID != row.GenerationID ||
				!strings.EqualFold(status.PackageDigest, row.PackageDigest) || !strings.EqualFold(status.ArtifactDigest, row.ArtifactDigest) ||
				status.ConfigVersion != row.ConfigVersion || status.Sequence == 0 {
				return storage.ErrPluginGenerationStale
			}
			report.State, report.Sequence, report.ErrorCode = status.State, status.Sequence, status.ErrorCode
			report.SafeDetail = status.SafeDetail
			report.Details, report.Budget = append(json.RawMessage(nil), status.Details...), append(json.RawMessage(nil), status.Budget...)
		case storage.AgentRevisionStateFailed:
			report.State, report.ErrorCode = "failed", strings.TrimSpace(input.ErrorCode)
			report.Details = json.RawMessage(`{"safe_detail":"revision apply failed"}`)
		case "drained":
			if row.State != "draining" {
				continue
			}
			report.State = "drained"
		default:
			return storage.ErrPluginGenerationConflict
		}
		if _, err := s.pluginLifecycle.Reconcile(ctx, report, agentID); err != nil && !pluginGenerationReportIgnorable(err) {
			return err
		}
	}
	return nil
}

func pluginGenerationReportIgnorable(err error) bool {
	return errors.Is(err, storage.ErrPluginGenerationStale) || errors.Is(err, storage.ErrPluginGenerationConflict)
}

func (s *RevisionAPI) reconcilePluginRevisionWithoutRuntimeStatuses(ctx context.Context, operationID string) error {
	operation, found, err := s.pluginLifecycle.store.GetPluginOperation(ctx, operationID)
	if err != nil || !found {
		return err
	}
	if operation.Status == "succeeded" || operation.Status == "failed" {
		return nil
	}
	if operation.Status != "applying" && operation.Status != "staged" {
		return storage.ErrPluginGenerationConflict
	}
	revisions, err := s.repository.ListOperationRevisions(ctx, operationID)
	if err != nil {
		return err
	}
	if len(revisions) == 0 {
		return storage.ErrPluginGenerationStale
	}
	applied := true
	agentResults := make(map[string]any, len(revisions))
	for _, revision := range revisions {
		state := revision.State
		switch state {
		case storage.AgentRevisionStateApplied:
			if operation.Kind == "disable" && revision.DrainState != storage.AgentRevisionDrainStateDrained && revision.DrainState != storage.AgentRevisionDrainStateForced {
				return nil
			}
		case storage.AgentRevisionStateFailed, storage.AgentRevisionStateSuperseded:
			applied = false
		default:
			return nil
		}
		agentResults[revision.AgentID] = map[string]any{
			"state": state, "revision": revision.Revision, "drain_state": revision.DrainState,
		}
	}
	return s.pluginLifecycle.completeTrustedRevisionOperation(ctx, operation, applied, agentResults)
}

func (s *RevisionAPI) SaveMutationResponse(ctx context.Context, scope, key, operationID string, response []byte) error {
	if strings.TrimSpace(key) == "" {
		return nil
	}
	if strings.TrimSpace(scope) == "" {
		scope = PanelIdempotencyScope
	}
	var payload struct {
		Operation   storage.OperationRow `json:"operation"`
		OperationID string               `json:"operation_id"`
	}
	if err := json.Unmarshal(response, &payload); err != nil {
		return fmt.Errorf("decode mutation response: %w", err)
	}
	persistedOperationID := strings.TrimSpace(payload.Operation.ID)
	if persistedOperationID == "" {
		persistedOperationID = strings.TrimSpace(payload.OperationID)
	}
	if persistedOperationID != strings.TrimSpace(operationID) {
		return fmt.Errorf("mutation response operation does not match idempotency record")
	}
	updated, err := s.repository.UpdateIdempotencyResponseJSON(ctx, scope, key, operationID, string(response))
	if err != nil {
		return err
	}
	if !updated {
		return fmt.Errorf("idempotency response record was not found")
	}
	return nil
}

func (s *RevisionAPI) panelActionIdempotency(
	ctx context.Context,
	kind, agentID string,
	revision int64,
) (storage.CoordinatorActionIdempotency, error) {
	scope, key, enabled := revisionpkg.MutationIdempotencyFromContext(ctx)
	if !enabled {
		return storage.CoordinatorActionIdempotency{}, nil
	}
	if scope == "" {
		scope = PanelIdempotencyScope
	}
	httpFingerprint := revisionpkg.MutationHTTPRequestFingerprintFromContext(ctx)
	fingerprint := httpFingerprint
	if fingerprint == "" {
		var err error
		fingerprint, err = revisionpkg.RequestFingerprint(struct {
			Kind     string `json:"kind"`
			AgentID  string `json:"agent_id"`
			Revision int64  `json:"revision,omitempty"`
		}{Kind: kind, AgentID: agentID, Revision: revision})
		if err != nil {
			return storage.CoordinatorActionIdempotency{}, err
		}
	}
	now := s.nowUTC()
	return storage.CoordinatorActionIdempotency{
		Scope: scope, Key: key, RequestFingerprint: fingerprint,
		HTTPRequestFingerprint: httpFingerprint, ExpiresAt: now.Add(panelActionIdempotencyTTL),
	}, nil
}

func (s *RevisionAPI) LoadMutationResponse(ctx context.Context, scope, key, operationID string) (map[string]any, bool, error) {
	payload, found, err := s.LoadMutationResponseByKey(ctx, scope, key)
	if err != nil || !found {
		return nil, false, err
	}
	operationJSON, err := json.Marshal(payload["operation"])
	if err != nil {
		return nil, false, fmt.Errorf("decode persisted mutation operation: %w", err)
	}
	var operation storage.OperationRow
	if err := json.Unmarshal(operationJSON, &operation); err != nil {
		return nil, false, fmt.Errorf("decode persisted mutation operation: %w", err)
	}
	if operation.ID != strings.TrimSpace(operationID) {
		return nil, false, nil
	}
	return payload, true, nil
}

func (s *RevisionAPI) LoadMutationResponseByKey(ctx context.Context, scope, key string) (map[string]any, bool, error) {
	if strings.TrimSpace(scope) == "" {
		scope = PanelIdempotencyScope
	}
	record, found, err := s.repository.GetIdempotencyRecord(ctx, scope, strings.TrimSpace(key))
	if err != nil || !found {
		return nil, false, err
	}
	if !record.ExpiresAt.After(s.nowUTC()) {
		return nil, false, nil
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(record.ResponseJSON), &payload); err != nil {
		return nil, false, fmt.Errorf("decode persisted mutation response: %w", err)
	}
	_, isEnvelope := payload["status_url"]
	_, hasFingerprint := payload["http_request_fingerprint"]
	_, hasReplayResource := payload["replay_resource"]
	_, hasReplayExtra := payload["replay_extra"]
	return payload, isEnvelope || (hasFingerprint && (hasReplayResource || hasReplayExtra)), nil
}

func (s *RevisionAPI) operationAgentRevisionRefs(ctx context.Context, operation storage.OperationRow, revisions []storage.AgentRevisionRow) ([]operationAgentRevisionRef, error) {
	refs := make(map[string]int64, len(revisions))
	for _, row := range revisions {
		refs[row.AgentID] = row.Revision
	}
	after := uint64(0)
	for {
		rows, err := s.repository.ListRevisionEvents(ctx, storage.RevisionEventQuery{
			AfterID: after, Limit: 1000, OperationID: operation.ID,
		})
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			if row.AgentID != "" && row.Revision >= refs[row.AgentID] {
				refs[row.AgentID] = row.Revision
			}
			after = row.ID
		}
		if len(rows) < 1000 {
			break
		}
	}
	if len(refs) == 0 && operation.PrimaryAgentID != "" {
		// Revisions and events are retained for less time than operation rows.
		// Once both immutable sources are gone, the operation is expired; binding
		// it to the agent's mutable current pointer would report an unrelated
		// rollout and make historical status change over time.
		return nil, fmt.Errorf("%w: operation %q revision history expired", ErrRevisionNotFound, operation.ID)
	}
	result := make([]operationAgentRevisionRef, 0, len(refs))
	for agentID, revision := range refs {
		result = append(result, operationAgentRevisionRef{AgentID: agentID, Revision: revision})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].AgentID < result[j].AgentID })
	return result, nil
}

type operationAgentRevisionRef struct {
	AgentID  string
	Revision int64
}

func aggregateRevisionStatus(agents []AgentRevisionStatus, fallback string) string {
	if len(agents) == 0 {
		return fallback
	}
	applied, failed, applying, pending, superseded := 0, 0, 0, 0, 0
	for _, agent := range agents {
		switch agent.ApplyStatus {
		case storage.AgentRevisionStateApplied:
			applied++
		case storage.AgentRevisionStateFailed:
			failed++
		case storage.AgentRevisionStateApplying:
			applying++
		case storage.AgentRevisionStateSuperseded:
			superseded++
		default:
			pending++
		}
	}
	switch {
	case failed > 0 && applied > 0:
		return string(dependency.StatusDegraded)
	case applying > 0 || (applied > 0 && pending > 0):
		return storage.OperationStatusApplying
	case failed > 0:
		return storage.OperationStatusFailed
	case pending > 0:
		return storage.OperationStatusPending
	case applied == len(agents):
		return storage.OperationStatusApplied
	case superseded == len(agents):
		return storage.OperationStatusSuperseded
	default:
		return fallback
	}
}

func remoteLeaseFromCoordinator(lease coordinator.Lease) RemoteRevisionLease {
	return RemoteRevisionLease{
		AgentID: lease.AgentID, Revision: lease.Revision, RetryCycle: lease.RetryCycle,
		Attempt: lease.Attempt, LeaseID: lease.LeaseID, SnapshotDigest: lease.SnapshotDigest,
		DesiredVersion: lease.DesiredVersion, ApplyTimeoutSeconds: lease.ApplyTimeoutSeconds,
		DrainTimeoutSeconds: lease.DrainTimeoutSeconds, DeadlineAt: lease.DeadlineAt,
	}
}

func (s *RevisionAPI) currentRemoteLease(ctx context.Context, row storage.AgentRevisionRow) (coordinator.Lease, bool, error) {
	attempts, err := s.repository.ListCoordinatorAttempts(ctx, row.AgentID, row.Revision)
	if err != nil {
		return coordinator.Lease{}, false, err
	}
	now := s.nowUTC()
	for index := len(attempts) - 1; index >= 0; index-- {
		attempt := attempts[index]
		if attempt.RetryCycle != row.RetryCycle || !attempt.DeadlineAt.After(now) {
			continue
		}
		if attempt.State != storage.AgentRevisionAttemptStateLeased && attempt.State != storage.AgentRevisionAttemptStateStarted {
			continue
		}
		return coordinator.Lease{
			AgentID: row.AgentID, Revision: row.Revision, RetryCycle: attempt.RetryCycle,
			Attempt: attempt.Attempt, LeaseID: attempt.LeaseID,
			SnapshotArtifactID: row.SnapshotArtifactID, SnapshotDigest: row.SnapshotDigest,
			DesiredVersion: row.DesiredVersion, ApplyTimeoutSeconds: row.ApplyTimeoutSeconds,
			DrainTimeoutSeconds: row.DrainTimeoutSeconds, DeadlineAt: attempt.DeadlineAt,
		}, true, nil
	}
	return coordinator.Lease{}, false, nil
}

func (s *RevisionAPI) loadAuthoritativeLease(
	ctx context.Context,
	agentID string,
	revision int64,
	retryCycle, attempt int,
	leaseID string,
	phase remoteLeasePhase,
) (coordinator.Lease, error) {
	agentID = strings.TrimSpace(agentID)
	leaseID = strings.TrimSpace(leaseID)
	if agentID == "" || revision < 0 || retryCycle < 0 || attempt <= 0 || leaseID == "" {
		return coordinator.Lease{}, fmt.Errorf("%w: lease identity is incomplete", ErrInvalidArgument)
	}
	row, found, err := s.repository.GetCoordinatorRevision(ctx, agentID, revision)
	if err != nil {
		return coordinator.Lease{}, err
	}
	if !found {
		return coordinator.Lease{}, fmt.Errorf("%w: revision %s/%d", ErrRevisionNotFound, agentID, revision)
	}
	if row.RetryCycle != retryCycle {
		return coordinator.Lease{}, fmt.Errorf("%w: lease retry cycle is stale", coordinator.ErrLeaseConflict)
	}
	attempts, err := s.repository.ListCoordinatorAttempts(ctx, agentID, revision)
	if err != nil {
		return coordinator.Lease{}, err
	}
	for _, candidate := range attempts {
		if candidate.RetryCycle != retryCycle || candidate.Attempt != attempt || !constantTimeStringEqual(candidate.LeaseID, leaseID) {
			continue
		}
		now := s.nowUTC()
		switch phase {
		case remoteLeasePhaseLeased:
			if candidate.State != storage.AgentRevisionAttemptStateLeased || candidate.Attempt != row.AttemptCount+1 || !now.Before(candidate.DeadlineAt) {
				return coordinator.Lease{}, fmt.Errorf("%w: lease is not the current unexpired leased attempt", coordinator.ErrLeaseConflict)
			}
		case remoteLeasePhaseStarted:
			if candidate.State != storage.AgentRevisionAttemptStateStarted || row.State != storage.AgentRevisionStateApplying || candidate.Attempt != row.AttemptCount || !now.Before(candidate.DeadlineAt) {
				return coordinator.Lease{}, fmt.Errorf("%w: lease is not the current unexpired started attempt", coordinator.ErrLeaseConflict)
			}
		case remoteLeasePhaseApply:
			started := candidate.State == storage.AgentRevisionAttemptStateStarted && row.State == storage.AgentRevisionStateApplying &&
				candidate.Attempt == row.AttemptCount && now.Before(candidate.DeadlineAt)
			appliedReplay := candidate.State == storage.AgentRevisionAttemptStateApplied && row.State == storage.AgentRevisionStateApplied &&
				candidate.Attempt == row.AttemptCount && row.AppliedAt != nil
			if !started && !appliedReplay {
				return coordinator.Lease{}, fmt.Errorf("%w: lease is not the current started or applied attempt", coordinator.ErrLeaseConflict)
			}
		case remoteLeasePhaseDrain:
			if candidate.State != storage.AgentRevisionAttemptStateApplied || row.State != storage.AgentRevisionStateApplied || candidate.Attempt != row.AttemptCount || row.AppliedAt == nil {
				return coordinator.Lease{}, fmt.Errorf("%w: lease is not the current applied attempt", coordinator.ErrLeaseConflict)
			}
			pointer, found, pointerErr := s.repository.GetAgentRevisionPointer(ctx, agentID)
			if pointerErr != nil {
				return coordinator.Lease{}, pointerErr
			}
			if !found || pointer.AppliedRevision != revision {
				return coordinator.Lease{}, fmt.Errorf("%w: revision %d is no longer the current applied revision", coordinator.ErrLeaseConflict, revision)
			}
		default:
			return coordinator.Lease{}, fmt.Errorf("%w: unsupported lease phase", coordinator.ErrLeaseConflict)
		}
		return coordinator.Lease{
			AgentID: agentID, Revision: revision, RetryCycle: retryCycle, Attempt: attempt,
			LeaseID: candidate.LeaseID, SnapshotArtifactID: row.SnapshotArtifactID,
			SnapshotDigest: row.SnapshotDigest, DesiredVersion: row.DesiredVersion,
			ApplyTimeoutSeconds: row.ApplyTimeoutSeconds, DrainTimeoutSeconds: row.DrainTimeoutSeconds,
			DeadlineAt: candidate.DeadlineAt,
		}, nil
	}
	return coordinator.Lease{}, fmt.Errorf("%w: lease is not current", coordinator.ErrLeaseConflict)
}

func (s *RevisionAPI) nowUTC() time.Time {
	if s != nil && s.now != nil {
		return s.now().UTC()
	}
	return time.Now().UTC()
}

func requireRemoteAgentIdentity(authenticatedAgentID, reportedAgentID string) error {
	authenticatedAgentID = strings.TrimSpace(authenticatedAgentID)
	reportedAgentID = strings.TrimSpace(reportedAgentID)
	if authenticatedAgentID == "" {
		return fmt.Errorf("%w: authenticated agent id is required", ErrRevisionForbidden)
	}
	if reportedAgentID != "" && reportedAgentID != authenticatedAgentID {
		return fmt.Errorf("%w: agent may only report its own revision", ErrRevisionForbidden)
	}
	return nil
}

func constantTimeStringEqual(left, right string) bool {
	if len(left) != len(right) || left == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}
