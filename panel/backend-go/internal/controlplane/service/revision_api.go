package service

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/coordinator"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/dependency"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

const PanelIdempotencyScope = "panel"

var (
	ErrRevisionNotFound  = errors.New("revision record not found")
	ErrRevisionForbidden = errors.New("revision action forbidden")
)

type RevisionRepository interface {
	GetOperation(context.Context, string) (storage.OperationRow, bool, error)
	ListOperationRevisions(context.Context, string) ([]storage.AgentRevisionRow, error)
	GetAgentRevisionPointer(context.Context, string) (storage.AgentRevisionPointerRow, bool, error)
	GetCoordinatorRevision(context.Context, string, int64) (storage.AgentRevisionRow, bool, error)
	ListCoordinatorAttempts(context.Context, string, int64) ([]storage.AgentRevisionAttemptRow, error)
	GetCoordinatorGeneration(context.Context, string, string) (storage.AgentGenerationRow, bool, error)
	ListCoordinatorGenerations(context.Context, string) ([]storage.AgentGenerationRow, error)
	GetGenerationArtifact(context.Context, string) (storage.GenerationArtifactRow, bool, error)
	GetOperationDependencyArtifact(context.Context, string) (storage.GenerationArtifactRow, bool, error)
	ListRevisionEvents(context.Context, storage.RevisionEventQuery) ([]storage.RevisionEventRow, error)
	GetIdempotencyRecord(context.Context, string, string) (storage.IdempotencyRecordRow, bool, error)
	UpdateIdempotencyResponseJSON(context.Context, string, string, string, string) (bool, error)
}

type RevisionAPI struct {
	repository  RevisionRepository
	coordinator *coordinator.Coordinator
}

func NewRevisionAPI(repository RevisionRepository, revisionCoordinator *coordinator.Coordinator) *RevisionAPI {
	if repository == nil || revisionCoordinator == nil {
		return nil
	}
	return &RevisionAPI{repository: repository, coordinator: revisionCoordinator}
}

type OperationStatus struct {
	OperationID  string                `json:"operation_id"`
	Kind         string                `json:"kind"`
	ApplyStatus  string                `json:"apply_status"`
	PrimaryAgent string                `json:"primary_agent_id"`
	NoOp         bool                  `json:"no_op"`
	Degraded     bool                  `json:"degraded"`
	ErrorCode    string                `json:"error_code,omitempty"`
	ErrorMessage string                `json:"error_message,omitempty"`
	Agents       []AgentRevisionStatus `json:"agents"`
	CreatedAt    time.Time             `json:"created_at"`
	UpdatedAt    time.Time             `json:"updated_at"`
	CompletedAt  *time.Time            `json:"completed_at,omitempty"`
}

type AgentRevisionStatus struct {
	OperationID           string               `json:"operation_id"`
	AgentID               string               `json:"agent_id"`
	DesiredRevision       int64                `json:"desired_revision"`
	AppliedRevision       int64                `json:"applied_revision"`
	LastKnownGoodRevision int64                `json:"last_known_good_revision"`
	ApplyStatus           string               `json:"apply_status"`
	DrainStatus           string               `json:"drain_status,omitempty"`
	BlockedBy             []string             `json:"blocked_by"`
	NoOp                  bool                 `json:"no_op"`
	RetryCycle            int                  `json:"retry_cycle"`
	AttemptCount          int                  `json:"attempt_count"`
	NextAttemptAt         *time.Time           `json:"next_attempt_at,omitempty"`
	GenerationID          string               `json:"generation_id,omitempty"`
	ErrorCode             string               `json:"error_code,omitempty"`
	ErrorMessage          string               `json:"error_message,omitempty"`
	Attempts              []RevisionAttempt    `json:"attempts"`
	Generations           []RevisionGeneration `json:"generations"`
	CreatedAt             time.Time            `json:"created_at"`
	UpdatedAt             time.Time            `json:"updated_at"`
	AppliedAt             *time.Time           `json:"applied_at,omitempty"`
	FailedAt              *time.Time           `json:"failed_at,omitempty"`
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
	AgentID      string `json:"agent_id"`
	Revision     int64  `json:"revision"`
	RetryCycle   int    `json:"retry_cycle"`
	Attempt      int    `json:"attempt"`
	LeaseID      string `json:"lease_id"`
	GenerationID string `json:"generation_id"`
	Status       string `json:"status"`
	ErrorCode    string `json:"error_code,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
	Forced       bool   `json:"forced,omitempty"`
	ForceReason  string `json:"force_reason,omitempty"`
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
		return AgentRevisionStatus{}, fmt.Errorf("%w: pointer for agent %q", ErrRevisionNotFound, agentID)
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
	if _, err := s.coordinator.Retry(ctx, strings.TrimSpace(agentID), revision); err != nil {
		return AgentRevisionStatus{}, err
	}
	return s.GetAgentRevisionStatus(ctx, agentID, revision)
}

func (s *RevisionAPI) Rollback(ctx context.Context, agentID string) (OperationStatus, error) {
	result, err := s.coordinator.Rollback(ctx, strings.TrimSpace(agentID))
	if err != nil {
		return OperationStatus{}, err
	}
	return s.GetOperationStatus(ctx, result.Operation.ID)
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
	if !found || pointer.DesiredRevision <= pointer.AppliedRevision {
		return RemoteRevisionPull{DesiredRevision: pointer.DesiredRevision}, nil
	}
	revisionRow, found, err := s.repository.GetCoordinatorRevision(ctx, agentID, pointer.DesiredRevision)
	if err != nil {
		return RemoteRevisionPull{}, err
	}
	if !found {
		return RemoteRevisionPull{}, fmt.Errorf("%w: revision %s/%d", ErrRevisionNotFound, agentID, pointer.DesiredRevision)
	}
	snapshot, err := s.loadRemoteSnapshot(ctx, revisionRow.SnapshotArtifactID, revisionRow.SnapshotDigest, revisionRow.Revision)
	if err != nil {
		return RemoteRevisionPull{}, err
	}
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

func (s *RevisionAPI) StartRemoteRevision(ctx context.Context, agentID string, input RemoteRevisionStart) (AgentRevisionStatus, error) {
	if err := requireRemoteAgentIdentity(agentID, input.AgentID); err != nil {
		return AgentRevisionStatus{}, err
	}
	lease, err := s.loadAuthoritativeLease(ctx, agentID, input.Revision, input.RetryCycle, input.Attempt, input.LeaseID)
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
	lease, err := s.loadAuthoritativeLease(ctx, agentID, input.Revision, input.RetryCycle, input.Attempt, input.LeaseID)
	if err != nil {
		return AgentRevisionStatus{}, err
	}
	generationID := strings.TrimSpace(input.GenerationID)
	if generationID == "" {
		return AgentRevisionStatus{}, fmt.Errorf("%w: generation id is required", ErrInvalidArgument)
	}
	switch strings.ToLower(strings.TrimSpace(input.Status)) {
	case storage.AgentRevisionStateFailed:
		if _, err := s.coordinator.Fail(ctx, coordinator.FailureReport{
			Lease: lease, GenerationID: generationID,
			ErrorCode: strings.TrimSpace(input.ErrorCode), ErrorMessage: strings.TrimSpace(input.ErrorMessage),
		}); err != nil {
			return AgentRevisionStatus{}, err
		}
	case storage.AgentRevisionStateApplied, storage.AgentRevisionDrainStateDraining:
		if _, err := s.coordinator.Applied(ctx, coordinator.AppliedReport{Lease: lease, GenerationID: generationID}); err != nil {
			return AgentRevisionStatus{}, err
		}
	case storage.AgentRevisionDrainStateDrained, storage.AgentRevisionDrainStateForced:
		generation, found, err := s.repository.GetCoordinatorGeneration(ctx, agentID, generationID)
		if err != nil {
			return AgentRevisionStatus{}, err
		}
		if !found || generation.Revision != input.Revision {
			return AgentRevisionStatus{}, fmt.Errorf("%w: generation %q", ErrRevisionNotFound, generationID)
		}
		forced := input.Forced || strings.EqualFold(input.Status, storage.AgentRevisionDrainStateForced)
		row, err := s.coordinator.Drained(ctx, coordinator.DrainReport{
			AgentID: agentID, GenerationID: generationID, Forced: forced,
			ForceReason: strings.TrimSpace(input.ForceReason),
		})
		if err != nil {
			return AgentRevisionStatus{}, err
		}
		return s.GetAgentRevisionStatus(ctx, agentID, row.Revision)
	default:
		return AgentRevisionStatus{}, fmt.Errorf("%w: unsupported revision report status %q", ErrInvalidArgument, input.Status)
	}
	return s.GetAgentRevisionStatus(ctx, agentID, input.Revision)
}

func (s *RevisionAPI) SaveMutationResponse(ctx context.Context, scope, key, operationID string, response []byte) error {
	if strings.TrimSpace(key) == "" {
		return nil
	}
	if strings.TrimSpace(scope) == "" {
		scope = PanelIdempotencyScope
	}
	var payload struct {
		Operation storage.OperationRow `json:"operation"`
	}
	if err := json.Unmarshal(response, &payload); err != nil {
		return fmt.Errorf("decode mutation response: %w", err)
	}
	if payload.Operation.ID != strings.TrimSpace(operationID) {
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
	if !record.ExpiresAt.After(time.Now().UTC()) {
		return nil, false, nil
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(record.ResponseJSON), &payload); err != nil {
		return nil, false, fmt.Errorf("decode persisted mutation response: %w", err)
	}
	_, isEnvelope := payload["status_url"]
	return payload, isEnvelope, nil
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
		pointer, found, err := s.repository.GetAgentRevisionPointer(ctx, operation.PrimaryAgentID)
		if err != nil {
			return nil, err
		}
		if found {
			refs[operation.PrimaryAgentID] = pointer.DesiredRevision
		}
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
	now := time.Now().UTC()
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

func (s *RevisionAPI) loadRemoteSnapshot(ctx context.Context, artifactID, expectedDigest string, expectedRevision int64) (storage.Snapshot, error) {
	artifact, found, err := s.repository.GetGenerationArtifact(ctx, artifactID)
	if err != nil {
		return storage.Snapshot{}, err
	}
	if !found {
		return storage.Snapshot{}, fmt.Errorf("%w: snapshot artifact %q", ErrRevisionNotFound, artifactID)
	}
	digest := sha256.Sum256(artifact.Payload)
	digestText := hex.EncodeToString(digest[:])
	if !strings.EqualFold(digestText, artifact.SHA256) || !strings.EqualFold(digestText, expectedDigest) {
		return storage.Snapshot{}, fmt.Errorf("snapshot artifact digest is inconsistent")
	}
	var snapshot storage.Snapshot
	if err := json.Unmarshal(artifact.Payload, &snapshot); err != nil {
		return storage.Snapshot{}, fmt.Errorf("decode revision snapshot: %w", err)
	}
	if snapshot.Revision != expectedRevision {
		return storage.Snapshot{}, fmt.Errorf("snapshot revision %d does not match desired revision %d", snapshot.Revision, expectedRevision)
	}
	return snapshot, nil
}

func (s *RevisionAPI) loadAuthoritativeLease(ctx context.Context, agentID string, revision int64, retryCycle, attempt int, leaseID string) (coordinator.Lease, error) {
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
	attempts, err := s.repository.ListCoordinatorAttempts(ctx, agentID, revision)
	if err != nil {
		return coordinator.Lease{}, err
	}
	for _, candidate := range attempts {
		if candidate.RetryCycle != retryCycle || candidate.Attempt != attempt || !constantTimeStringEqual(candidate.LeaseID, leaseID) {
			continue
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
