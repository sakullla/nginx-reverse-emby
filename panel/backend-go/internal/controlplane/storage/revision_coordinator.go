package storage

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	coordinatorMaxAttempts = 5
	coordinatorRetryBase   = time.Second
	coordinatorRetryLimit  = 30 * time.Second

	OperationStatusApplying   = "applying"
	OperationStatusFailed     = "failed"
	OperationStatusSuperseded = "superseded"

	AgentRevisionStateApplying   = "applying"
	AgentRevisionStateFailed     = "failed"
	AgentRevisionStateSuperseded = "superseded"

	AgentRevisionDrainStateDraining = "draining"
	AgentRevisionDrainStateDrained  = "drained"
	AgentRevisionDrainStateForced   = "forced"

	AgentRevisionAttemptStateLeased           = "leased"
	AgentRevisionAttemptStateStarted          = "started"
	AgentRevisionAttemptStateApplied          = "applied"
	AgentRevisionAttemptStateFailed           = "failed"
	AgentRevisionAttemptStateSuperseded       = "superseded"
	AgentRevisionAttemptStateExpiredUnstarted = "expired_unstarted"

	// CoordinatorDrainReportGracePeriod lets a locally forced drain report on
	// the next heartbeat after its lease timeout while keeping the lease bounded.
	CoordinatorDrainReportGracePeriod = time.Minute
)

func CoordinatorDrainReportDeadline(appliedAt time.Time, drainTimeoutSeconds int) time.Time {
	if drainTimeoutSeconds <= 0 {
		return time.Time{}
	}
	return appliedAt.Add(time.Duration(drainTimeoutSeconds) * time.Second).Add(CoordinatorDrainReportGracePeriod)
}

var (
	ErrCoordinatorLeaseConflict           = errors.New("revision coordinator lease conflict")
	ErrCoordinatorStateConflict           = errors.New("revision coordinator state conflict")
	ErrCoordinatorNotFound                = errors.New("revision coordinator record not found")
	ErrCoordinatorReconcileNeeded         = errors.New("revision coordinator reconciliation required")
	ErrCoordinatorDependencyClaimRequired = errors.New("revision coordinator dependency-scoped claim required")
)

type CoordinatorLease struct {
	AgentID             string
	Revision            int64
	RetryCycle          int
	Attempt             int
	LeaseID             string
	SnapshotArtifactID  string
	SnapshotDigest      string
	DesiredVersion      string
	ApplyTimeoutSeconds int
	DrainTimeoutSeconds int
	DeadlineAt          time.Time
}

type CoordinatorClaimRequest struct {
	AgentID                    string
	LeaseID                    string
	ExpectedOperationID        string
	ExpectedRevision           int64
	Now                        time.Time
	DefaultApplyTimeoutSeconds int
	DefaultDrainTimeoutSeconds int
}

type CoordinatorClaimResult struct {
	Lease               *CoordinatorLease
	Busy                bool
	SupersededRevisions []int64
}

type CoordinatorStartRequest struct {
	Lease                      CoordinatorLease
	GenerationID               string
	Now                        time.Time
	DefaultApplyTimeoutSeconds int
}

type CoordinatorStartResult struct {
	Revision AgentRevisionRow
	Attempt  AgentRevisionAttemptRow
}

type CoordinatorFailureRequest struct {
	Lease        CoordinatorLease
	GenerationID string
	Now          time.Time
	Jitter       float64
	ErrorCode    string
	ErrorMessage string
}

type CoordinatorFailureResult struct {
	Revision   AgentRevisionRow
	Attempt    AgentRevisionAttemptRow
	RetryDelay time.Duration
	Exhausted  bool
}

type CoordinatorApplyRequest struct {
	Lease        CoordinatorLease
	GenerationID string
	Now          time.Time
}

type CoordinatorApplyResult struct {
	Revision AgentRevisionRow
	Attempt  AgentRevisionAttemptRow
	Pointer  AgentRevisionPointerRow
}

type CoordinatorDrainRequest struct {
	AgentID      string
	GenerationID string
	Lease        CoordinatorLease
	Forced       bool
	ForceReason  string
	Now          time.Time
}

type CoordinatorActionIdempotency struct {
	Scope                  string
	Key                    string
	RequestFingerprint     string
	HTTPRequestFingerprint string
	ExpiresAt              time.Time
}

type CoordinatorRetryRequest struct {
	AgentID     string
	Revision    int64
	Now         time.Time
	Idempotency CoordinatorActionIdempotency
}

type CoordinatorRetryResult struct {
	Revision AgentRevisionRow
	Replayed bool
}

type CoordinatorReconcileRequest struct {
	AgentID string
	Now     time.Time
	Jitter  float64
}

type CoordinatorReconcileResult struct {
	AgentID          string
	Ready            bool
	AttemptsConsumed int
	Superseded       []int64
}

type CoordinatorRollbackRequest struct {
	AgentID                    string
	OperationID                string
	Now                        time.Time
	DefaultApplyTimeoutSeconds int
	DefaultDrainTimeoutSeconds int
	Idempotency                CoordinatorActionIdempotency
}

type CoordinatorRollbackResult struct {
	Operation OperationRow
	Revision  AgentRevisionRow
	Pointer   AgentRevisionPointerRow
	Replayed  bool
}

type CoordinatorJournalRequest struct {
	AgentID        string
	Revision       int64
	SnapshotDigest string
	GenerationID   string
	Now            time.Time
}

type CoordinatorJournalResult struct {
	Revision AgentRevisionRow
	Pointer  AgentRevisionPointerRow
	Stale    bool
}

func (s *GormStore) GetCoordinatorRevision(ctx context.Context, agentID string, revision int64) (AgentRevisionRow, bool, error) {
	var row AgentRevisionRow
	err := s.db.WithContext(ctx).
		Where("agent_id = ? AND revision = ?", strings.TrimSpace(agentID), revision).
		First(&row).Error
	if err == nil {
		return row, true, nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return AgentRevisionRow{}, false, nil
	}
	return AgentRevisionRow{}, false, err
}

func (s *GormStore) ListCoordinatorAttempts(ctx context.Context, agentID string, revision int64) ([]AgentRevisionAttemptRow, error) {
	var rows []AgentRevisionAttemptRow
	err := s.db.WithContext(ctx).
		Where("agent_id = ? AND revision = ?", strings.TrimSpace(agentID), revision).
		Order("retry_cycle, attempt").
		Find(&rows).Error
	return rows, err
}

func (s *GormStore) GetCoordinatorGeneration(ctx context.Context, agentID, generationID string) (AgentGenerationRow, bool, error) {
	var row AgentGenerationRow
	err := s.db.WithContext(ctx).
		Where("agent_id = ? AND generation_id = ?", strings.TrimSpace(agentID), strings.TrimSpace(generationID)).
		First(&row).Error
	if err == nil {
		return row, true, nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return AgentGenerationRow{}, false, nil
	}
	return AgentGenerationRow{}, false, err
}

func (s *GormStore) ClaimLatestAgentRevision(ctx context.Context, request CoordinatorClaimRequest) (CoordinatorClaimResult, error) {
	request.AgentID = strings.TrimSpace(request.AgentID)
	request.LeaseID = strings.TrimSpace(request.LeaseID)
	request.ExpectedOperationID = strings.TrimSpace(request.ExpectedOperationID)
	if request.AgentID == "" || request.LeaseID == "" {
		return CoordinatorClaimResult{}, fmt.Errorf("agent id and lease id are required")
	}
	if (request.ExpectedOperationID == "") != (request.ExpectedRevision == 0) || request.ExpectedRevision < 0 {
		return CoordinatorClaimResult{}, fmt.Errorf("expected operation id and positive revision must be provided together")
	}
	request.Now = coordinatorTime(request.Now)
	var result CoordinatorClaimResult
	err := s.writeTransaction(ctx, func(tx *gorm.DB) error {
		pointer, err := lockCoordinatorPointer(tx, request.AgentID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		if request.ExpectedOperationID != "" {
			if pointer.DesiredRevision != request.ExpectedRevision {
				return nil
			}
			var expected AgentRevisionRow
			err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("agent_id = ? AND revision = ?", request.AgentID, request.ExpectedRevision).
				First(&expected).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			if err != nil {
				return err
			}
			if expected.OperationID != request.ExpectedOperationID {
				return nil
			}
		} else {
			var dependencyPlanRefs int64
			if err := tx.Model(&AgentRevisionArtifactRow{}).
				Where("agent_id = ? AND revision = ? AND role = ?", request.AgentID, pointer.DesiredRevision, RevisionArtifactRoleDependencyPlan).
				Count(&dependencyPlanRefs).Error; err != nil {
				return err
			}
			if dependencyPlanRefs > 0 {
				return fmt.Errorf("%w: agent %q revision %d", ErrCoordinatorDependencyClaimRequired, request.AgentID, pointer.DesiredRevision)
			}
		}

		active, err := lockActiveCoordinatorAttempts(tx, request.AgentID)
		if err != nil {
			return err
		}
		if len(active) > 1 {
			return coordinatorStateConflict("agent %q has %d active leases", request.AgentID, len(active))
		}
		if len(active) == 1 {
			attempt := active[0]
			switch {
			case attempt.Revision > pointer.DesiredRevision:
				return coordinatorStateConflict("agent %q active revision %d exceeds desired revision %d", request.AgentID, attempt.Revision, pointer.DesiredRevision)
			case attempt.Revision < pointer.DesiredRevision:
				if err := supersedeAttemptTx(tx, &attempt, request.Now); err != nil {
					return err
				}
				if err := supersedeRevisionTx(tx, request.AgentID, attempt.Revision, request.Now); err != nil {
					return err
				}
				result.SupersededRevisions = append(result.SupersededRevisions, attempt.Revision)
			case request.Now.Before(attempt.DeadlineAt):
				result.Busy = true
				return nil
			case attempt.State == AgentRevisionAttemptStateStarted:
				return fmt.Errorf("%w: agent %q revision %d attempt %d expired", ErrCoordinatorReconcileNeeded, request.AgentID, attempt.Revision, attempt.Attempt)
			case attempt.State == AgentRevisionAttemptStateLeased:
				if err := expireUnstartedAttemptTx(tx, &attempt, request.Now); err != nil {
					return err
				}
			}
		}

		superseded, err := supersedeOlderCoordinatorRevisionsTx(tx, pointer, request.Now)
		if err != nil {
			return err
		}
		result.SupersededRevisions = appendUniqueRevisions(result.SupersededRevisions, superseded...)

		var revision AgentRevisionRow
		err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("agent_id = ? AND revision = ?", request.AgentID, pointer.DesiredRevision).
			First(&revision).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if revision.State != AgentRevisionStatePending {
			return nil
		}
		if revision.NextAttemptAt != nil && request.Now.Before(*revision.NextAttemptAt) {
			return nil
		}

		// Leasing reserves the next ordinal, but AttemptCount advances only after prepare_started.
		// An unstarted expired lease can therefore reuse this row without consuming an attempt.
		attemptNumber := revision.AttemptCount + 1
		applyTimeout := resolvedCoordinatorTimeoutSeconds(revision.ApplyTimeoutSeconds, request.DefaultApplyTimeoutSeconds, 60)
		drainTimeout := resolvedCoordinatorTimeoutSeconds(revision.DrainTimeoutSeconds, request.DefaultDrainTimeoutSeconds, 600)
		deadline := request.Now.Add(time.Duration(applyTimeout) * time.Second)
		attempt := AgentRevisionAttemptRow{
			AgentID: request.AgentID, Revision: revision.Revision, RetryCycle: revision.RetryCycle,
			Attempt: attemptNumber, LeaseID: request.LeaseID, State: AgentRevisionAttemptStateLeased,
			StartedAt: request.Now, DeadlineAt: deadline,
		}
		var existing AgentRevisionAttemptRow
		err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("agent_id = ? AND revision = ? AND retry_cycle = ? AND attempt = ?", request.AgentID, revision.Revision, revision.RetryCycle, attemptNumber).
			First(&existing).Error
		switch {
		case err == nil:
			if existing.State != AgentRevisionAttemptStateExpiredUnstarted {
				return coordinatorStateConflict("agent %q revision %d attempt %d already exists in state %q", request.AgentID, revision.Revision, attemptNumber, existing.State)
			}
			if err := tx.Model(&AgentRevisionAttemptRow{}).
				Where("agent_id = ? AND revision = ? AND retry_cycle = ? AND attempt = ?", request.AgentID, revision.Revision, revision.RetryCycle, attemptNumber).
				Updates(map[string]any{
					"lease_id": request.LeaseID, "state": AgentRevisionAttemptStateLeased,
					"started_at": request.Now, "deadline_at": deadline, "finished_at": nil,
					"error_code": "", "error_message": "",
				}).Error; err != nil {
				return err
			}
		case errors.Is(err, gorm.ErrRecordNotFound):
			if err := tx.Create(&attempt).Error; err != nil {
				return err
			}
		default:
			return err
		}

		if err := appendCoordinatorEventTx(tx, revision, "revision_leased", request.Now, map[string]any{
			"retry_cycle": revision.RetryCycle, "attempt": attemptNumber,
			"lease_id": request.LeaseID, "deadline_at": deadline,
		}); err != nil {
			return err
		}
		result.Lease = &CoordinatorLease{
			AgentID: request.AgentID, Revision: revision.Revision, RetryCycle: revision.RetryCycle,
			Attempt: attemptNumber, LeaseID: request.LeaseID,
			SnapshotArtifactID: revision.SnapshotArtifactID, SnapshotDigest: revision.SnapshotDigest,
			DesiredVersion: revision.DesiredVersion, ApplyTimeoutSeconds: applyTimeout,
			DrainTimeoutSeconds: drainTimeout, DeadlineAt: deadline,
		}
		return nil
	})
	return result, err
}

func (s *GormStore) StartAgentRevisionAttempt(ctx context.Context, request CoordinatorStartRequest) (CoordinatorStartResult, error) {
	request.Now = coordinatorTime(request.Now)
	request.GenerationID = strings.TrimSpace(request.GenerationID)
	if err := validateCoordinatorLease(request.Lease); err != nil {
		return CoordinatorStartResult{}, err
	}
	if request.GenerationID == "" {
		return CoordinatorStartResult{}, fmt.Errorf("generation id is required")
	}
	var result CoordinatorStartResult
	var postCommitErr error
	err := s.writeTransaction(ctx, func(tx *gorm.DB) error {
		pointer, err := lockCoordinatorPointer(tx, request.Lease.AgentID)
		if err != nil {
			return err
		}
		attempt, err := lockCoordinatorAttempt(tx, request.Lease)
		if err != nil {
			return err
		}
		if attempt.LeaseID != request.Lease.LeaseID {
			return coordinatorLeaseConflict("lease %q is no longer current", request.Lease.LeaseID)
		}
		var revision AgentRevisionRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("agent_id = ? AND revision = ?", request.Lease.AgentID, request.Lease.Revision).
			First(&revision).Error; err != nil {
			return err
		}
		if attempt.State == AgentRevisionAttemptStateStarted {
			if !request.Now.Before(attempt.DeadlineAt) {
				return coordinatorLeaseConflict("lease %q expired", request.Lease.LeaseID)
			}
			if revision.GenerationID != request.GenerationID {
				return coordinatorStateConflict("lease %q started generation %q, not %q", request.Lease.LeaseID, revision.GenerationID, request.GenerationID)
			}
			result = CoordinatorStartResult{Revision: revision, Attempt: attempt}
			return nil
		}
		if attempt.State != AgentRevisionAttemptStateLeased {
			return coordinatorLeaseConflict("lease %q is in terminal state %q", request.Lease.LeaseID, attempt.State)
		}
		if !request.Now.Before(attempt.DeadlineAt) {
			if err := expireUnstartedAttemptTx(tx, &attempt, request.Now); err != nil {
				return err
			}
			postCommitErr = coordinatorLeaseConflict("lease %q expired", request.Lease.LeaseID)
			return nil
		}
		if pointer.DesiredRevision > request.Lease.Revision || revision.State == AgentRevisionStateSuperseded {
			if err := supersedeAttemptTx(tx, &attempt, request.Now); err != nil {
				return err
			}
			if err := supersedeRevisionTx(tx, request.Lease.AgentID, request.Lease.Revision, request.Now); err != nil {
				return err
			}
			postCommitErr = coordinatorLeaseConflict("revision %d was superseded by desired revision %d", request.Lease.Revision, pointer.DesiredRevision)
			return nil
		}
		if revision.State != AgentRevisionStatePending || request.Lease.Attempt != revision.AttemptCount+1 {
			return coordinatorStateConflict("revision %d cannot start attempt %d from state %q/count %d", revision.Revision, request.Lease.Attempt, revision.State, revision.AttemptCount)
		}
		applyTimeout := resolvedCoordinatorTimeoutSeconds(revision.ApplyTimeoutSeconds, request.DefaultApplyTimeoutSeconds, 60)
		deadline := request.Now.Add(time.Duration(applyTimeout) * time.Second)
		if err := tx.Model(&AgentRevisionAttemptRow{}).
			Where("agent_id = ? AND revision = ? AND retry_cycle = ? AND attempt = ? AND lease_id = ? AND state = ?",
				attempt.AgentID, attempt.Revision, attempt.RetryCycle, attempt.Attempt, attempt.LeaseID, AgentRevisionAttemptStateLeased).
			Updates(map[string]any{
				"state": AgentRevisionAttemptStateStarted, "started_at": request.Now,
				"deadline_at": deadline, "finished_at": nil,
			}).Error; err != nil {
			return err
		}
		if err := tx.Model(&AgentRevisionRow{}).
			Where("agent_id = ? AND revision = ? AND state = ? AND attempt_count = ?", revision.AgentID, revision.Revision, AgentRevisionStatePending, revision.AttemptCount).
			Updates(map[string]any{
				"state": AgentRevisionStateApplying, "attempt_count": request.Lease.Attempt,
				"next_attempt_at": nil, "generation_id": request.GenerationID,
				"error_code": "", "error_message": "", "failed_at": nil, "updated_at": request.Now,
			}).Error; err != nil {
			return err
		}
		attempt.State = AgentRevisionAttemptStateStarted
		attempt.StartedAt = request.Now
		attempt.DeadlineAt = deadline
		revision.State = AgentRevisionStateApplying
		revision.AttemptCount = request.Lease.Attempt
		revision.NextAttemptAt = nil
		revision.GenerationID = request.GenerationID
		revision.ErrorCode = ""
		revision.ErrorMessage = ""
		revision.FailedAt = nil
		revision.UpdatedAt = request.Now
		if err := appendCoordinatorEventTx(tx, revision, "prepare_started", request.Now, map[string]any{
			"retry_cycle": attempt.RetryCycle, "attempt": attempt.Attempt,
			"lease_id": attempt.LeaseID, "generation_id": request.GenerationID,
			"deadline_at": deadline,
		}); err != nil {
			return err
		}
		if err := refreshCoordinatorOperationTx(tx, revision.OperationID, request.Now); err != nil {
			return err
		}
		result = CoordinatorStartResult{Revision: revision, Attempt: attempt}
		return nil
	})
	if err != nil {
		return CoordinatorStartResult{}, err
	}
	if postCommitErr != nil {
		return CoordinatorStartResult{}, postCommitErr
	}
	return result, nil
}

func (s *GormStore) FailAgentRevisionAttempt(ctx context.Context, request CoordinatorFailureRequest) (CoordinatorFailureResult, error) {
	request.Now = coordinatorTime(request.Now)
	request.GenerationID = strings.TrimSpace(request.GenerationID)
	if err := validateCoordinatorLease(request.Lease); err != nil {
		return CoordinatorFailureResult{}, err
	}
	if request.GenerationID == "" {
		return CoordinatorFailureResult{}, fmt.Errorf("generation id is required")
	}
	var result CoordinatorFailureResult
	err := s.writeTransaction(ctx, func(tx *gorm.DB) error {
		pointer, err := lockCoordinatorPointer(tx, request.Lease.AgentID)
		if err != nil {
			return err
		}
		attempt, err := lockCoordinatorAttempt(tx, request.Lease)
		if err != nil {
			return err
		}
		if attempt.LeaseID != request.Lease.LeaseID || attempt.State != AgentRevisionAttemptStateStarted {
			return coordinatorLeaseConflict("lease %q is not the active started attempt", request.Lease.LeaseID)
		}
		if !request.Now.Before(attempt.DeadlineAt) {
			return coordinatorLeaseConflict("lease %q expired", request.Lease.LeaseID)
		}
		if pointer.DesiredRevision > request.Lease.Revision {
			return coordinatorLeaseConflict("revision %d is no longer desired", request.Lease.Revision)
		}
		var revision AgentRevisionRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("agent_id = ? AND revision = ?", request.Lease.AgentID, request.Lease.Revision).
			First(&revision).Error; err != nil {
			return err
		}
		if revision.State != AgentRevisionStateApplying || revision.AttemptCount != attempt.Attempt {
			return coordinatorStateConflict("revision %d does not match active attempt %d", revision.Revision, attempt.Attempt)
		}
		if revision.GenerationID != request.GenerationID {
			return coordinatorStateConflict("revision %d is applying generation %q, not %q", revision.Revision, revision.GenerationID, request.GenerationID)
		}
		failure, err := failStartedCoordinatorAttemptTx(tx, revision, attempt, request)
		if err != nil {
			return err
		}
		result = failure
		return nil
	})
	return result, err
}

func (s *GormStore) ApplyAgentRevisionAttempt(ctx context.Context, request CoordinatorApplyRequest) (CoordinatorApplyResult, error) {
	request.Now = coordinatorTime(request.Now)
	request.GenerationID = strings.TrimSpace(request.GenerationID)
	if err := validateCoordinatorLease(request.Lease); err != nil {
		return CoordinatorApplyResult{}, err
	}
	if request.GenerationID == "" {
		return CoordinatorApplyResult{}, fmt.Errorf("generation id is required")
	}
	var result CoordinatorApplyResult
	err := s.writeTransaction(ctx, func(tx *gorm.DB) error {
		pointer, err := lockCoordinatorPointer(tx, request.Lease.AgentID)
		if err != nil {
			return err
		}
		attempt, err := lockCoordinatorAttempt(tx, request.Lease)
		if err != nil {
			return err
		}
		if attempt.LeaseID != request.Lease.LeaseID || attempt.State != AgentRevisionAttemptStateStarted {
			return coordinatorLeaseConflict("lease %q is not the active started attempt", request.Lease.LeaseID)
		}
		if !request.Now.Before(attempt.DeadlineAt) {
			return coordinatorLeaseConflict("lease %q expired", request.Lease.LeaseID)
		}
		if pointer.DesiredRevision != request.Lease.Revision {
			return coordinatorLeaseConflict("revision %d is no longer desired; current desired revision is %d", request.Lease.Revision, pointer.DesiredRevision)
		}
		if pointer.AppliedRevision > request.Lease.Revision {
			return coordinatorLeaseConflict("revision %d is older than applied revision %d", request.Lease.Revision, pointer.AppliedRevision)
		}
		var revision AgentRevisionRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("agent_id = ? AND revision = ?", request.Lease.AgentID, request.Lease.Revision).
			First(&revision).Error; err != nil {
			return err
		}
		if revision.State != AgentRevisionStateApplying || revision.AttemptCount != attempt.Attempt {
			return coordinatorStateConflict("revision %d does not match active attempt %d", revision.Revision, attempt.Attempt)
		}
		if revision.GenerationID != request.GenerationID {
			return coordinatorStateConflict("revision %d is applying generation %q, not %q", revision.Revision, revision.GenerationID, request.GenerationID)
		}

		drainState, err := activateCoordinatorGenerationTx(tx, revision, request.GenerationID, request.Now)
		if err != nil {
			return err
		}
		if err := tx.Model(&AgentRevisionAttemptRow{}).
			Where("agent_id = ? AND revision = ? AND retry_cycle = ? AND attempt = ? AND lease_id = ? AND state = ?",
				attempt.AgentID, attempt.Revision, attempt.RetryCycle, attempt.Attempt, attempt.LeaseID, AgentRevisionAttemptStateStarted).
			Updates(map[string]any{
				"state": AgentRevisionAttemptStateApplied, "finished_at": request.Now,
				"error_code": "", "error_message": "",
			}).Error; err != nil {
			return err
		}
		if err := tx.Model(&AgentRevisionRow{}).
			Where("agent_id = ? AND revision = ?", revision.AgentID, revision.Revision).
			Updates(map[string]any{
				"state": AgentRevisionStateApplied, "generation_id": request.GenerationID,
				"drain_state": drainState, "next_attempt_at": nil,
				"error_code": "", "error_message": "", "failed_at": nil,
				"applied_at": request.Now, "updated_at": request.Now,
			}).Error; err != nil {
			return err
		}
		pointer.AppliedRevision = maxRevisionInt64(pointer.AppliedRevision, revision.Revision)
		pointer.LastKnownGoodRevision = maxRevisionInt64(pointer.LastKnownGoodRevision, revision.Revision)
		pointer.UpdatedAt = request.Now
		if err := updateCoordinatorPointerTx(tx, pointer); err != nil {
			return err
		}

		attempt.State = AgentRevisionAttemptStateApplied
		finishedAt := request.Now
		attempt.FinishedAt = &finishedAt
		revision.State = AgentRevisionStateApplied
		revision.GenerationID = request.GenerationID
		revision.DrainState = drainState
		revision.NextAttemptAt = nil
		revision.ErrorCode = ""
		revision.ErrorMessage = ""
		revision.FailedAt = nil
		revision.AppliedAt = &finishedAt
		revision.UpdatedAt = request.Now
		if err := appendCoordinatorEventTx(tx, revision, "revision_applied", request.Now, map[string]any{
			"retry_cycle": attempt.RetryCycle, "attempt": attempt.Attempt,
			"lease_id": attempt.LeaseID, "generation_id": request.GenerationID,
			"drain_state": drainState,
		}); err != nil {
			return err
		}
		if err := refreshCoordinatorOperationTx(tx, revision.OperationID, request.Now); err != nil {
			return err
		}
		result = CoordinatorApplyResult{Revision: revision, Attempt: attempt, Pointer: pointer}
		return nil
	})
	return result, err
}

func (s *GormStore) CompleteCoordinatorDrain(ctx context.Context, request CoordinatorDrainRequest) (AgentRevisionRow, error) {
	request.AgentID = strings.TrimSpace(request.AgentID)
	request.GenerationID = strings.TrimSpace(request.GenerationID)
	request.Now = coordinatorTime(request.Now)
	request.Lease.AgentID = strings.TrimSpace(request.Lease.AgentID)
	request.Lease.LeaseID = strings.TrimSpace(request.Lease.LeaseID)
	if request.Lease.AgentID == "" || request.Lease.Revision <= 0 || request.Lease.RetryCycle < 0 ||
		request.Lease.Attempt <= 0 || request.Lease.LeaseID == "" {
		return AgentRevisionRow{}, coordinatorLeaseConflict("drain lease identity is incomplete")
	}
	if request.AgentID == "" {
		request.AgentID = request.Lease.AgentID
	}
	if request.AgentID == "" || request.GenerationID == "" {
		return AgentRevisionRow{}, fmt.Errorf("agent id and generation id are required")
	}
	var result AgentRevisionRow
	err := s.writeTransaction(ctx, func(tx *gorm.DB) error {
		pointer, err := lockCoordinatorPointer(tx, request.AgentID)
		if err != nil {
			return err
		}
		var generation AgentGenerationRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("agent_id = ? AND generation_id = ?", request.AgentID, request.GenerationID).
			First(&generation).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("%w: generation %q", ErrCoordinatorNotFound, request.GenerationID)
			}
			return err
		}
		if err := validateCoordinatorDrainLeaseTx(tx, pointer, generation, request); err != nil {
			return err
		}
		if generation.State == AgentRevisionDrainStateDrained || generation.State == AgentRevisionDrainStateForced {
			return tx.Where("agent_id = ? AND revision = ?", request.AgentID, pointer.AppliedRevision).First(&result).Error
		}
		if generation.State != GenerationStateDraining {
			return coordinatorStateConflict("generation %q is in state %q", request.GenerationID, generation.State)
		}
		state := AgentRevisionDrainStateDrained
		if request.Forced {
			state = AgentRevisionDrainStateForced
		}
		if err := tx.Model(&AgentGenerationRow{}).
			Where("agent_id = ? AND generation_id = ? AND state = ?", request.AgentID, request.GenerationID, GenerationStateDraining).
			Updates(map[string]any{
				"state": state, "forced": request.Forced, "force_reason": strings.TrimSpace(request.ForceReason),
				"drained_at": request.Now, "updated_at": request.Now,
			}).Error; err != nil {
			return err
		}
		var remaining int64
		if err := tx.Model(&AgentGenerationRow{}).
			Where("agent_id = ? AND state = ?", request.AgentID, GenerationStateDraining).
			Count(&remaining).Error; err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("agent_id = ? AND revision = ?", request.AgentID, pointer.AppliedRevision).
			First(&result).Error; err != nil {
			return err
		}
		if remaining == 0 {
			result.DrainState = state
			result.UpdatedAt = request.Now
			if err := tx.Model(&AgentRevisionRow{}).
				Where("agent_id = ? AND revision = ?", result.AgentID, result.Revision).
				Updates(map[string]any{"drain_state": state, "updated_at": request.Now}).Error; err != nil {
				return err
			}
		}
		return appendCoordinatorEventTx(tx, result, "generation_"+state, request.Now, map[string]any{
			"generation_id": request.GenerationID, "forced": request.Forced,
			"force_reason": strings.TrimSpace(request.ForceReason),
		})
	})
	return result, err
}

func validateCoordinatorDrainLeaseTx(
	tx *gorm.DB,
	pointer AgentRevisionPointerRow,
	generation AgentGenerationRow,
	request CoordinatorDrainRequest,
) error {
	lease := request.Lease
	if lease.AgentID == "" || lease.AgentID != request.AgentID || lease.Revision <= 0 ||
		lease.RetryCycle < 0 || lease.Attempt <= 0 || lease.LeaseID == "" {
		return coordinatorLeaseConflict("drain lease identity is incomplete")
	}
	if pointer.AppliedRevision != lease.Revision {
		return coordinatorLeaseConflict("revision %d is no longer the current applied revision %d", lease.Revision, pointer.AppliedRevision)
	}

	var revision AgentRevisionRow
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("agent_id = ? AND revision = ?", lease.AgentID, lease.Revision).
		First(&revision).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return coordinatorLeaseConflict("drain revision %d is not available", lease.Revision)
		}
		return err
	}
	if revision.State != AgentRevisionStateApplied || revision.RetryCycle != lease.RetryCycle ||
		revision.AttemptCount != lease.Attempt || revision.AppliedAt == nil {
		return coordinatorLeaseConflict("drain lease is not the current applied attempt")
	}

	var attempt AgentRevisionAttemptRow
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("agent_id = ? AND revision = ? AND retry_cycle = ? AND attempt = ?", lease.AgentID, lease.Revision, lease.RetryCycle, lease.Attempt).
		First(&attempt).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return coordinatorLeaseConflict("drain attempt is not available")
		}
		return err
	}
	if attempt.State != AgentRevisionAttemptStateApplied || !coordinatorLeaseIDEqual(attempt.LeaseID, lease.LeaseID) {
		return coordinatorLeaseConflict("drain lease is not the current applied attempt")
	}

	drainDeadline := CoordinatorDrainReportDeadline(*revision.AppliedAt, revision.DrainTimeoutSeconds)
	if drainDeadline.IsZero() || !request.Now.Before(drainDeadline) {
		return coordinatorLeaseConflict("drain report deadline expired")
	}
	if generation.State != GenerationStateDraining || generation.Revision >= lease.Revision {
		return coordinatorLeaseConflict("generation %q is not a draining predecessor for revision %d", generation.GenerationID, lease.Revision)
	}
	return nil
}

func coordinatorLeaseIDEqual(left, right string) bool {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if left == "" || len(left) != len(right) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

func (s *GormStore) ReconcileCoordinatorAgent(ctx context.Context, request CoordinatorReconcileRequest) (CoordinatorReconcileResult, error) {
	request.AgentID = strings.TrimSpace(request.AgentID)
	request.Now = coordinatorTime(request.Now)
	if request.AgentID == "" {
		return CoordinatorReconcileResult{}, fmt.Errorf("agent id is required")
	}
	var result = CoordinatorReconcileResult{AgentID: request.AgentID}
	err := s.writeTransaction(ctx, func(tx *gorm.DB) error {
		pointer, err := lockCoordinatorPointer(tx, request.AgentID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		active, err := lockActiveCoordinatorAttempts(tx, request.AgentID)
		if err != nil {
			return err
		}
		for i := range active {
			attempt := active[i]
			if attempt.Revision < pointer.DesiredRevision {
				if err := supersedeAttemptTx(tx, &attempt, request.Now); err != nil {
					return err
				}
				if err := supersedeRevisionTx(tx, request.AgentID, attempt.Revision, request.Now); err != nil {
					return err
				}
				result.Superseded = appendUniqueRevisions(result.Superseded, attempt.Revision)
				continue
			}
			if attempt.Revision > pointer.DesiredRevision {
				return coordinatorStateConflict("agent %q active revision %d exceeds desired revision %d", request.AgentID, attempt.Revision, pointer.DesiredRevision)
			}
			if request.Now.Before(attempt.DeadlineAt) {
				continue
			}
			switch attempt.State {
			case AgentRevisionAttemptStateLeased:
				if err := expireUnstartedAttemptTx(tx, &attempt, request.Now); err != nil {
					return err
				}
			case AgentRevisionAttemptStateStarted:
				var revision AgentRevisionRow
				if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
					Where("agent_id = ? AND revision = ?", request.AgentID, attempt.Revision).
					First(&revision).Error; err != nil {
					return err
				}
				_, err := failStartedCoordinatorAttemptTx(tx, revision, attempt, CoordinatorFailureRequest{
					Lease: CoordinatorLease{
						AgentID: attempt.AgentID, Revision: attempt.Revision, RetryCycle: attempt.RetryCycle,
						Attempt: attempt.Attempt, LeaseID: attempt.LeaseID,
					},
					Now: request.Now, Jitter: request.Jitter,
					ErrorCode: "apply_timeout", ErrorMessage: "apply attempt deadline expired",
				})
				if err != nil {
					return err
				}
				result.AttemptsConsumed++
			}
		}

		superseded, err := supersedeOlderCoordinatorRevisionsTx(tx, pointer, request.Now)
		if err != nil {
			return err
		}
		result.Superseded = appendUniqueRevisions(result.Superseded, superseded...)

		var desired AgentRevisionRow
		err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("agent_id = ? AND revision = ?", request.AgentID, pointer.DesiredRevision).
			First(&desired).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if desired.State == AgentRevisionStateApplying {
			var count int64
			if err := tx.Model(&AgentRevisionAttemptRow{}).
				Where("agent_id = ? AND revision = ? AND retry_cycle = ? AND state = ?", request.AgentID, desired.Revision, desired.RetryCycle, AgentRevisionAttemptStateStarted).
				Count(&count).Error; err != nil {
				return err
			}
			if count == 0 {
				if err := reconcileOrphanApplyingRevisionTx(tx, &desired, request); err != nil {
					return err
				}
				result.AttemptsConsumed++
			}
		}
		if desired.State == AgentRevisionStatePending && (desired.NextAttemptAt == nil || !request.Now.Before(*desired.NextAttemptAt)) {
			result.Ready = true
		}
		return nil
	})
	return result, err
}

func (s *GormStore) ListCoordinatorAgentIDs(ctx context.Context) ([]string, error) {
	ids := map[string]struct{}{}
	var revisionIDs []string
	if err := s.db.WithContext(ctx).Model(&AgentRevisionRow{}).
		Where("state IN ?", []string{AgentRevisionStatePending, AgentRevisionStateApplying}).
		Distinct("agent_id").Pluck("agent_id", &revisionIDs).Error; err != nil {
		return nil, err
	}
	for _, id := range revisionIDs {
		ids[id] = struct{}{}
	}
	var pointerIDs []string
	if err := s.db.WithContext(ctx).Model(&AgentRevisionPointerRow{}).
		Where("desired_revision > applied_revision").
		Distinct("agent_id").Pluck("agent_id", &pointerIDs).Error; err != nil {
		return nil, err
	}
	for _, id := range pointerIDs {
		ids[id] = struct{}{}
	}
	result := make([]string, 0, len(ids))
	for id := range ids {
		result = append(result, id)
	}
	sort.Strings(result)
	return result, nil
}

func (s *GormStore) RetryCoordinatorRevision(ctx context.Context, agentID string, revisionNumber int64, now time.Time) (AgentRevisionRow, error) {
	result, err := s.RetryCoordinatorRevisionIdempotent(ctx, CoordinatorRetryRequest{
		AgentID: agentID, Revision: revisionNumber, Now: now,
	})
	return result.Revision, err
}

func (s *GormStore) RetryCoordinatorRevisionIdempotent(ctx context.Context, request CoordinatorRetryRequest) (CoordinatorRetryResult, error) {
	request.AgentID = strings.TrimSpace(request.AgentID)
	request.Now = coordinatorTime(request.Now)
	if request.AgentID == "" || request.Revision < 0 {
		return CoordinatorRetryResult{}, fmt.Errorf("agent id and revision are required")
	}
	if err := validateCoordinatorActionIdempotency(request.Idempotency, request.Now); err != nil {
		return CoordinatorRetryResult{}, err
	}
	var result CoordinatorRetryResult
	err := s.writeTransaction(ctx, func(tx *gorm.DB) error {
		if replay, found, err := loadCoordinatorActionReplayTx(tx, request.Idempotency, request.Now); err != nil {
			return err
		} else if found {
			if replay.AgentID != request.AgentID || replay.DesiredRevision != request.Revision {
				return coordinatorStateConflict("idempotency response does not match retry target")
			}
			var revision AgentRevisionRow
			if err := tx.Where("agent_id = ? AND revision = ?", request.AgentID, request.Revision).First(&revision).Error; err != nil {
				return err
			}
			result = CoordinatorRetryResult{Revision: revision, Replayed: true}
			return nil
		}
		pointer, err := lockCoordinatorPointer(tx, request.AgentID)
		if err != nil {
			return err
		}
		if pointer.DesiredRevision != request.Revision {
			return coordinatorStateConflict("revision %d is not current desired revision %d", request.Revision, pointer.DesiredRevision)
		}
		active, err := lockActiveCoordinatorAttempts(tx, request.AgentID)
		if err != nil {
			return err
		}
		if len(active) > 0 {
			return coordinatorStateConflict("agent %q has an active attempt", request.AgentID)
		}
		var revision AgentRevisionRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("agent_id = ? AND revision = ?", request.AgentID, request.Revision).
			First(&revision).Error; err != nil {
			return err
		}
		if revision.State != AgentRevisionStateFailed {
			return coordinatorStateConflict("revision %d is in state %q, want failed", request.Revision, revision.State)
		}
		revision.RetryCycle++
		revision.AttemptCount = 0
		revision.State = AgentRevisionStatePending
		revision.NextAttemptAt = nil
		revision.ErrorCode = ""
		revision.ErrorMessage = ""
		revision.FailedAt = nil
		revision.UpdatedAt = request.Now
		if err := tx.Model(&AgentRevisionRow{}).
			Where("agent_id = ? AND revision = ? AND state = ?", request.AgentID, request.Revision, AgentRevisionStateFailed).
			Updates(map[string]any{
				"retry_cycle": revision.RetryCycle, "attempt_count": 0, "state": AgentRevisionStatePending,
				"next_attempt_at": nil, "error_code": "", "error_message": "", "failed_at": nil, "updated_at": request.Now,
			}).Error; err != nil {
			return err
		}
		if err := appendCoordinatorEventTx(tx, revision, "revision_retry_requested", request.Now, map[string]any{"retry_cycle": revision.RetryCycle}); err != nil {
			return err
		}
		if err := refreshCoordinatorOperationTx(tx, revision.OperationID, request.Now); err != nil {
			return err
		}
		if err := persistCoordinatorActionReplayTx(tx, request.Idempotency, coordinatorActionReplayPayload{
			OperationID: revision.OperationID, AgentID: revision.AgentID,
			DesiredRevision: revision.Revision, ApplyStatus: revision.State,
			HTTPRequestFingerprint: request.Idempotency.HTTPRequestFingerprint,
		}, request.Now); err != nil {
			return err
		}
		result = CoordinatorRetryResult{Revision: revision}
		return nil
	})
	if err != nil && coordinatorActionIdempotencyEnabled(request.Idempotency) {
		if replayed, found, replayErr := s.loadCoordinatorActionReplay(ctx, request.Idempotency, request.Now); replayErr != nil {
			return CoordinatorRetryResult{}, replayErr
		} else if found && replayed.AgentID == request.AgentID && replayed.DesiredRevision == request.Revision {
			revision, revisionFound, revisionErr := s.GetCoordinatorRevision(ctx, request.AgentID, request.Revision)
			if revisionErr != nil {
				return CoordinatorRetryResult{}, revisionErr
			}
			if revisionFound {
				return CoordinatorRetryResult{Revision: revision, Replayed: true}, nil
			}
		}
	}
	return result, err
}

func (s *GormStore) CopyLastKnownGoodCoordinatorRevision(ctx context.Context, request CoordinatorRollbackRequest) (CoordinatorRollbackResult, error) {
	request.AgentID = strings.TrimSpace(request.AgentID)
	request.OperationID = strings.TrimSpace(request.OperationID)
	request.Now = coordinatorTime(request.Now)
	if request.AgentID == "" || request.OperationID == "" {
		return CoordinatorRollbackResult{}, fmt.Errorf("agent id and operation id are required")
	}
	if err := validateCoordinatorActionIdempotency(request.Idempotency, request.Now); err != nil {
		return CoordinatorRollbackResult{}, err
	}
	var result CoordinatorRollbackResult
	err := s.writeTransaction(ctx, func(tx *gorm.DB) error {
		if replay, found, err := loadCoordinatorActionReplayTx(tx, request.Idempotency, request.Now); err != nil {
			return err
		} else if found {
			if replay.AgentID != request.AgentID || replay.OperationID == "" || replay.DesiredRevision <= 0 {
				return coordinatorStateConflict("idempotency response does not match rollback target")
			}
			var operation OperationRow
			if err := tx.Where("id = ?", replay.OperationID).First(&operation).Error; err != nil {
				return err
			}
			var revision AgentRevisionRow
			if err := tx.Where("agent_id = ? AND revision = ?", request.AgentID, replay.DesiredRevision).First(&revision).Error; err != nil {
				return err
			}
			pointer, err := lockCoordinatorPointer(tx, request.AgentID)
			if err != nil {
				return err
			}
			result = CoordinatorRollbackResult{Operation: operation, Revision: revision, Pointer: pointer, Replayed: true}
			return nil
		}
		pointer, err := lockCoordinatorPointer(tx, request.AgentID)
		if err != nil {
			return err
		}
		if pointer.LastKnownGoodRevision <= 0 {
			return fmt.Errorf("%w: agent %q has no last-known-good revision", ErrCoordinatorNotFound, request.AgentID)
		}
		var source AgentRevisionRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("agent_id = ? AND revision = ?", request.AgentID, pointer.LastKnownGoodRevision).
			First(&source).Error; err != nil {
			return err
		}
		if source.State != AgentRevisionStateApplied {
			return coordinatorStateConflict("last-known-good revision %d is in state %q", source.Revision, source.State)
		}
		if strings.TrimSpace(source.SnapshotArtifactID) == "" {
			return fmt.Errorf("%w: last-known-good revision %d has no snapshot artifact", ErrCoordinatorNotFound, source.Revision)
		}
		var artifact GenerationArtifactRow
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).
			Where("id = ?", source.SnapshotArtifactID).First(&artifact).Error; err != nil {
			return err
		}
		if err := validateGenerationArtifact(artifact); err != nil {
			return err
		}
		if source.SnapshotDigest == "" || !strings.EqualFold(source.SnapshotDigest, artifact.SHA256) {
			return coordinatorStateConflict("last-known-good revision %d snapshot digest is inconsistent", source.Revision)
		}
		var maxRevision int64
		if err := tx.Model(&AgentRevisionRow{}).Where("agent_id = ?", request.AgentID).
			Select("COALESCE(MAX(revision), 0)").Scan(&maxRevision).Error; err != nil {
			return err
		}
		newRevision := maxRevisionInt64(maxRevision, pointer.DesiredRevision) + 1
		payload, digest, err := copyCoordinatorSnapshotPayload(artifact.Payload, source.Revision, newRevision)
		if err != nil {
			return err
		}
		newArtifact := GenerationArtifactRow{
			ID: "snapshot-" + digest, Kind: "agent_snapshot", SHA256: digest,
			Payload: payload, SizeBytes: int64(len(payload)), CreatedAt: request.Now,
		}
		if err := createImmutableArtifact(tx, newArtifact); err != nil {
			return err
		}
		operation := OperationRow{
			ID: request.OperationID, Kind: "rollback_last_known_good", Status: OperationStatusPending,
			PrimaryAgentID: request.AgentID, RequestFingerprint: digest,
			CreatedAt: request.Now, UpdatedAt: request.Now,
		}
		if err := tx.Create(&operation).Error; err != nil {
			return err
		}
		revision := AgentRevisionRow{
			AgentID: request.AgentID, Revision: newRevision, OperationID: operation.ID,
			State: AgentRevisionStatePending, SnapshotArtifactID: newArtifact.ID, SnapshotDigest: digest,
			DesiredVersion:      source.DesiredVersion,
			ApplyTimeoutSeconds: resolvedCoordinatorTimeoutSeconds(source.ApplyTimeoutSeconds, request.DefaultApplyTimeoutSeconds, 60),
			DrainTimeoutSeconds: resolvedCoordinatorTimeoutSeconds(source.DrainTimeoutSeconds, request.DefaultDrainTimeoutSeconds, 600),
			CreatedAt:           request.Now, UpdatedAt: request.Now,
		}
		if err := tx.Create(&revision).Error; err != nil {
			return err
		}
		if err := tx.Create(&AgentRevisionArtifactRow{
			AgentID: request.AgentID, Revision: newRevision, ArtifactID: newArtifact.ID,
			Role: revisionSnapshotArtifactRole, CreatedAt: request.Now,
		}).Error; err != nil {
			return err
		}
		pointer.DesiredRevision = newRevision
		pointer.UpdatedAt = request.Now
		if err := updateCoordinatorPointerTx(tx, pointer); err != nil {
			return err
		}
		if err := appendCoordinatorEventTx(tx, revision, "rollback_revision_created", request.Now, map[string]any{
			"source_revision": source.Revision, "snapshot_digest": digest,
		}); err != nil {
			return err
		}
		if err := persistCoordinatorActionReplayTx(tx, request.Idempotency, coordinatorActionReplayPayload{
			OperationID: operation.ID, AgentID: revision.AgentID,
			DesiredRevision: revision.Revision, ApplyStatus: operation.Status,
			HTTPRequestFingerprint: request.Idempotency.HTTPRequestFingerprint,
		}, request.Now); err != nil {
			return err
		}
		result = CoordinatorRollbackResult{Operation: operation, Revision: revision, Pointer: pointer}
		return nil
	})
	if err != nil && coordinatorActionIdempotencyEnabled(request.Idempotency) {
		if replayed, found, replayErr := s.loadCoordinatorActionReplay(ctx, request.Idempotency, request.Now); replayErr != nil {
			return CoordinatorRollbackResult{}, replayErr
		} else if found && replayed.AgentID == request.AgentID && replayed.OperationID != "" && replayed.DesiredRevision > 0 {
			operation, operationFound, operationErr := s.GetOperation(ctx, replayed.OperationID)
			if operationErr != nil {
				return CoordinatorRollbackResult{}, operationErr
			}
			revision, revisionFound, revisionErr := s.GetCoordinatorRevision(ctx, request.AgentID, replayed.DesiredRevision)
			if revisionErr != nil {
				return CoordinatorRollbackResult{}, revisionErr
			}
			pointer, pointerFound, pointerErr := s.GetAgentRevisionPointer(ctx, request.AgentID)
			if pointerErr != nil {
				return CoordinatorRollbackResult{}, pointerErr
			}
			if operationFound && revisionFound && pointerFound {
				return CoordinatorRollbackResult{Operation: operation, Revision: revision, Pointer: pointer, Replayed: true}, nil
			}
		}
	}
	return result, err
}

type coordinatorActionReplayPayload struct {
	OperationID            string       `json:"operation_id"`
	AgentID                string       `json:"agent_id"`
	DesiredRevision        int64        `json:"desired_revision"`
	ApplyStatus            string       `json:"apply_status"`
	HTTPRequestFingerprint string       `json:"http_request_fingerprint,omitempty"`
	Operation              OperationRow `json:"operation,omitempty"`
}

func coordinatorActionIdempotencyEnabled(input CoordinatorActionIdempotency) bool {
	return strings.TrimSpace(input.Key) != ""
}

func validateCoordinatorActionIdempotency(input CoordinatorActionIdempotency, now time.Time) error {
	if !coordinatorActionIdempotencyEnabled(input) {
		return nil
	}
	if strings.TrimSpace(input.Scope) == "" || strings.TrimSpace(input.RequestFingerprint) == "" || !input.ExpiresAt.After(now) {
		return fmt.Errorf("idempotency scope, fingerprint, and future expiry are required")
	}
	return nil
}

func loadCoordinatorActionReplayTx(
	tx *gorm.DB,
	input CoordinatorActionIdempotency,
	now time.Time,
) (coordinatorActionReplayPayload, bool, error) {
	if !coordinatorActionIdempotencyEnabled(input) {
		return coordinatorActionReplayPayload{}, false, nil
	}
	var record IdempotencyRecordRow
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("scope = ? AND key = ?", strings.TrimSpace(input.Scope), strings.TrimSpace(input.Key)).
		First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return coordinatorActionReplayPayload{}, false, nil
	}
	if err != nil {
		return coordinatorActionReplayPayload{}, false, err
	}
	if !record.ExpiresAt.After(now) {
		if err := tx.Where("scope = ? AND key = ? AND operation_id = ?", record.Scope, record.Key, record.OperationID).
			Delete(&IdempotencyRecordRow{}).Error; err != nil {
			return coordinatorActionReplayPayload{}, false, err
		}
		return coordinatorActionReplayPayload{}, false, nil
	}
	if record.RequestFingerprint != strings.TrimSpace(input.RequestFingerprint) {
		return coordinatorActionReplayPayload{}, false, coordinatorStateConflict("idempotency key was already used with a different request")
	}
	payload, err := decodeCoordinatorActionReplay(record.ResponseJSON)
	return payload, err == nil, err
}

func (s *GormStore) loadCoordinatorActionReplay(
	ctx context.Context,
	input CoordinatorActionIdempotency,
	now time.Time,
) (coordinatorActionReplayPayload, bool, error) {
	if !coordinatorActionIdempotencyEnabled(input) {
		return coordinatorActionReplayPayload{}, false, nil
	}
	record, found, err := s.GetIdempotencyRecord(ctx, input.Scope, input.Key)
	if err != nil || !found || !record.ExpiresAt.After(now) {
		return coordinatorActionReplayPayload{}, false, err
	}
	if record.RequestFingerprint != strings.TrimSpace(input.RequestFingerprint) {
		return coordinatorActionReplayPayload{}, false, coordinatorStateConflict("idempotency key was already used with a different request")
	}
	payload, err := decodeCoordinatorActionReplay(record.ResponseJSON)
	return payload, err == nil, err
}

func decodeCoordinatorActionReplay(responseJSON string) (coordinatorActionReplayPayload, error) {
	var payload coordinatorActionReplayPayload
	if err := json.Unmarshal([]byte(responseJSON), &payload); err != nil {
		return coordinatorActionReplayPayload{}, fmt.Errorf("decode coordinator action idempotency response: %w", err)
	}
	if payload.OperationID == "" {
		payload.OperationID = payload.Operation.ID
	}
	if payload.OperationID == "" || payload.AgentID == "" || payload.DesiredRevision <= 0 {
		return coordinatorActionReplayPayload{}, fmt.Errorf("coordinator action idempotency response is incomplete")
	}
	return payload, nil
}

func persistCoordinatorActionReplayTx(
	tx *gorm.DB,
	input CoordinatorActionIdempotency,
	payload coordinatorActionReplayPayload,
	now time.Time,
) error {
	if !coordinatorActionIdempotencyEnabled(input) {
		return nil
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode coordinator action idempotency response: %w", err)
	}
	return tx.Create(&IdempotencyRecordRow{
		Scope: strings.TrimSpace(input.Scope), Key: strings.TrimSpace(input.Key),
		RequestFingerprint: strings.TrimSpace(input.RequestFingerprint), OperationID: payload.OperationID,
		ResponseJSON: string(encoded), CreatedAt: now, ExpiresAt: input.ExpiresAt.UTC(),
	}).Error
}

func (s *GormStore) ReconcileCoordinatorJournal(ctx context.Context, request CoordinatorJournalRequest) (CoordinatorJournalResult, error) {
	request.AgentID = strings.TrimSpace(request.AgentID)
	request.SnapshotDigest = strings.TrimSpace(request.SnapshotDigest)
	request.GenerationID = strings.TrimSpace(request.GenerationID)
	request.Now = coordinatorTime(request.Now)
	if request.AgentID == "" || request.Revision < 0 || request.GenerationID == "" {
		return CoordinatorJournalResult{}, fmt.Errorf("agent id, revision, and generation id are required")
	}
	var result CoordinatorJournalResult
	err := s.writeTransaction(ctx, func(tx *gorm.DB) error {
		pointer, err := lockCoordinatorPointer(tx, request.AgentID)
		if err != nil {
			return err
		}
		if request.Revision < pointer.AppliedRevision {
			result = CoordinatorJournalResult{Pointer: pointer, Stale: true}
			return nil
		}
		if request.Revision > pointer.DesiredRevision {
			return coordinatorStateConflict("journal revision %d exceeds desired revision %d", request.Revision, pointer.DesiredRevision)
		}
		var revision AgentRevisionRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("agent_id = ? AND revision = ?", request.AgentID, request.Revision).
			First(&revision).Error; err != nil {
			return err
		}
		if request.SnapshotDigest != "" && !strings.EqualFold(request.SnapshotDigest, revision.SnapshotDigest) {
			return coordinatorStateConflict("journal digest does not match revision %d", request.Revision)
		}
		if revision.SnapshotDigest != "" && request.SnapshotDigest == "" {
			return coordinatorStateConflict("journal digest is required for revision %d", request.Revision)
		}
		drainState, err := activateCoordinatorGenerationTx(tx, revision, request.GenerationID, request.Now)
		if err != nil {
			return err
		}
		finishedAt := request.Now
		if err := tx.Model(&AgentRevisionAttemptRow{}).
			Where("agent_id = ? AND revision = ? AND state IN ?", request.AgentID, request.Revision,
				[]string{AgentRevisionAttemptStateLeased, AgentRevisionAttemptStateStarted}).
			Updates(map[string]any{
				"state": AgentRevisionAttemptStateApplied, "finished_at": finishedAt,
				"error_code": "", "error_message": "",
			}).Error; err != nil {
			return err
		}
		if err := tx.Model(&AgentRevisionRow{}).
			Where("agent_id = ? AND revision = ?", request.AgentID, request.Revision).
			Updates(map[string]any{
				"state": AgentRevisionStateApplied, "generation_id": request.GenerationID,
				"drain_state": drainState, "next_attempt_at": nil,
				"error_code": "", "error_message": "", "failed_at": nil,
				"applied_at": finishedAt, "updated_at": request.Now,
			}).Error; err != nil {
			return err
		}
		pointer.AppliedRevision = maxRevisionInt64(pointer.AppliedRevision, request.Revision)
		pointer.LastKnownGoodRevision = maxRevisionInt64(pointer.LastKnownGoodRevision, request.Revision)
		pointer.UpdatedAt = request.Now
		if err := updateCoordinatorPointerTx(tx, pointer); err != nil {
			return err
		}
		revision.State = AgentRevisionStateApplied
		revision.GenerationID = request.GenerationID
		revision.DrainState = drainState
		revision.NextAttemptAt = nil
		revision.ErrorCode = ""
		revision.ErrorMessage = ""
		revision.FailedAt = nil
		revision.AppliedAt = &finishedAt
		revision.UpdatedAt = request.Now
		if err := appendCoordinatorEventTx(tx, revision, "revision_reconciled_applied", request.Now, map[string]any{
			"generation_id": request.GenerationID, "snapshot_digest": request.SnapshotDigest,
		}); err != nil {
			return err
		}
		if err := refreshCoordinatorOperationTx(tx, revision.OperationID, request.Now); err != nil {
			return err
		}
		result = CoordinatorJournalResult{Revision: revision, Pointer: pointer}
		return nil
	})
	return result, err
}

func lockCoordinatorPointer(tx *gorm.DB, agentID string) (AgentRevisionPointerRow, error) {
	var row AgentRevisionPointerRow
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("agent_id = ?", strings.TrimSpace(agentID)).First(&row).Error
	return row, err
}

func lockCoordinatorAttempt(tx *gorm.DB, lease CoordinatorLease) (AgentRevisionAttemptRow, error) {
	var row AgentRevisionAttemptRow
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("agent_id = ? AND revision = ? AND retry_cycle = ? AND attempt = ?",
			lease.AgentID, lease.Revision, lease.RetryCycle, lease.Attempt).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return AgentRevisionAttemptRow{}, coordinatorLeaseConflict("lease %q was not found", lease.LeaseID)
	}
	return row, err
}

func lockActiveCoordinatorAttempts(tx *gorm.DB, agentID string) ([]AgentRevisionAttemptRow, error) {
	var rows []AgentRevisionAttemptRow
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("agent_id = ? AND state IN ?", strings.TrimSpace(agentID),
			[]string{AgentRevisionAttemptStateLeased, AgentRevisionAttemptStateStarted}).
		Order("revision, retry_cycle, attempt").Find(&rows).Error
	return rows, err
}

func supersedeOlderCoordinatorRevisionsTx(tx *gorm.DB, pointer AgentRevisionPointerRow, now time.Time) ([]int64, error) {
	var rows []AgentRevisionRow
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("agent_id = ? AND revision < ? AND state IN ?", pointer.AgentID, pointer.DesiredRevision,
			[]string{AgentRevisionStatePending, AgentRevisionStateApplying}).
		Order("revision").Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]int64, 0, len(rows))
	for i := range rows {
		row := rows[i]
		if err := tx.Model(&AgentRevisionAttemptRow{}).
			Where("agent_id = ? AND revision = ? AND state IN ?", row.AgentID, row.Revision,
				[]string{AgentRevisionAttemptStateLeased, AgentRevisionAttemptStateStarted}).
			Updates(map[string]any{
				"state": AgentRevisionAttemptStateSuperseded, "finished_at": now,
				"error_code": "superseded", "error_message": "a higher desired revision exists",
			}).Error; err != nil {
			return nil, err
		}
		if err := supersedeRevisionTx(tx, row.AgentID, row.Revision, now); err != nil {
			return nil, err
		}
		result = append(result, row.Revision)
	}
	return result, nil
}

func supersedeAttemptTx(tx *gorm.DB, attempt *AgentRevisionAttemptRow, now time.Time) error {
	if attempt.State != AgentRevisionAttemptStateLeased && attempt.State != AgentRevisionAttemptStateStarted {
		return nil
	}
	if err := tx.Model(&AgentRevisionAttemptRow{}).
		Where("agent_id = ? AND revision = ? AND retry_cycle = ? AND attempt = ? AND lease_id = ? AND state IN ?",
			attempt.AgentID, attempt.Revision, attempt.RetryCycle, attempt.Attempt, attempt.LeaseID,
			[]string{AgentRevisionAttemptStateLeased, AgentRevisionAttemptStateStarted}).
		Updates(map[string]any{
			"state": AgentRevisionAttemptStateSuperseded, "finished_at": now,
			"error_code": "superseded", "error_message": "a higher desired revision exists",
		}).Error; err != nil {
		return err
	}
	finishedAt := now
	attempt.State = AgentRevisionAttemptStateSuperseded
	attempt.FinishedAt = &finishedAt
	attempt.ErrorCode = "superseded"
	attempt.Error = "a higher desired revision exists"
	return nil
}

func supersedeRevisionTx(tx *gorm.DB, agentID string, revisionNumber int64, now time.Time) error {
	var revision AgentRevisionRow
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("agent_id = ? AND revision = ?", agentID, revisionNumber).First(&revision).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if revision.State != AgentRevisionStatePending && revision.State != AgentRevisionStateApplying {
		return nil
	}
	if err := tx.Model(&AgentRevisionRow{}).
		Where("agent_id = ? AND revision = ? AND state IN ?", agentID, revisionNumber,
			[]string{AgentRevisionStatePending, AgentRevisionStateApplying}).
		Updates(map[string]any{
			"state": AgentRevisionStateSuperseded, "next_attempt_at": nil,
			"error_code": "superseded", "error_message": "a higher desired revision exists", "updated_at": now,
		}).Error; err != nil {
		return err
	}
	revision.State = AgentRevisionStateSuperseded
	revision.NextAttemptAt = nil
	revision.ErrorCode = "superseded"
	revision.ErrorMessage = "a higher desired revision exists"
	revision.UpdatedAt = now
	if err := appendCoordinatorEventTx(tx, revision, "revision_superseded", now, map[string]any{}); err != nil {
		return err
	}
	return refreshCoordinatorOperationTx(tx, revision.OperationID, now)
}

func expireUnstartedAttemptTx(tx *gorm.DB, attempt *AgentRevisionAttemptRow, now time.Time) error {
	if attempt.State != AgentRevisionAttemptStateLeased {
		return nil
	}
	if err := tx.Model(&AgentRevisionAttemptRow{}).
		Where("agent_id = ? AND revision = ? AND retry_cycle = ? AND attempt = ? AND lease_id = ? AND state = ?",
			attempt.AgentID, attempt.Revision, attempt.RetryCycle, attempt.Attempt, attempt.LeaseID, AgentRevisionAttemptStateLeased).
		Updates(map[string]any{
			"state": AgentRevisionAttemptStateExpiredUnstarted, "finished_at": now,
			"error_code": "lease_expired", "error_message": "lease expired before prepare_started",
		}).Error; err != nil {
		return err
	}
	finishedAt := now
	attempt.State = AgentRevisionAttemptStateExpiredUnstarted
	attempt.FinishedAt = &finishedAt
	attempt.ErrorCode = "lease_expired"
	attempt.Error = "lease expired before prepare_started"
	var revision AgentRevisionRow
	if err := tx.Where("agent_id = ? AND revision = ?", attempt.AgentID, attempt.Revision).First(&revision).Error; err != nil {
		return err
	}
	return appendCoordinatorEventTx(tx, revision, "lease_expired_unstarted", now, map[string]any{
		"retry_cycle": attempt.RetryCycle, "attempt": attempt.Attempt, "lease_id": attempt.LeaseID,
	})
}

func failStartedCoordinatorAttemptTx(tx *gorm.DB, revision AgentRevisionRow, attempt AgentRevisionAttemptRow, request CoordinatorFailureRequest) (CoordinatorFailureResult, error) {
	errorCode := strings.TrimSpace(request.ErrorCode)
	if errorCode == "" {
		errorCode = "apply_failed"
	}
	errorMessage := strings.TrimSpace(request.ErrorMessage)
	if errorMessage == "" {
		errorMessage = "apply attempt failed"
	}
	if err := tx.Model(&AgentRevisionAttemptRow{}).
		Where("agent_id = ? AND revision = ? AND retry_cycle = ? AND attempt = ? AND lease_id = ? AND state = ?",
			attempt.AgentID, attempt.Revision, attempt.RetryCycle, attempt.Attempt, attempt.LeaseID, AgentRevisionAttemptStateStarted).
		Updates(map[string]any{
			"state": AgentRevisionAttemptStateFailed, "finished_at": request.Now,
			"error_code": errorCode, "error_message": errorMessage,
		}).Error; err != nil {
		return CoordinatorFailureResult{}, err
	}
	finishedAt := request.Now
	attempt.State = AgentRevisionAttemptStateFailed
	attempt.FinishedAt = &finishedAt
	attempt.ErrorCode = errorCode
	attempt.Error = errorMessage

	exhausted := revision.AttemptCount >= coordinatorMaxAttempts
	retryDelay := time.Duration(0)
	updates := map[string]any{
		"error_code": errorCode, "error_message": errorMessage,
		"generation_id": "", "updated_at": request.Now,
	}
	if exhausted {
		revision.State = AgentRevisionStateFailed
		revision.NextAttemptAt = nil
		revision.FailedAt = &finishedAt
		updates["state"] = AgentRevisionStateFailed
		updates["next_attempt_at"] = nil
		updates["failed_at"] = request.Now
	} else {
		retryDelay = coordinatorRetryDelay(revision.AttemptCount, coordinatorRetryBase, coordinatorRetryLimit, request.Jitter)
		nextAttemptAt := request.Now.Add(retryDelay)
		revision.State = AgentRevisionStatePending
		revision.NextAttemptAt = &nextAttemptAt
		revision.FailedAt = nil
		updates["state"] = AgentRevisionStatePending
		updates["next_attempt_at"] = nextAttemptAt
		updates["failed_at"] = nil
	}
	revision.GenerationID = ""
	revision.ErrorCode = errorCode
	revision.ErrorMessage = errorMessage
	revision.UpdatedAt = request.Now
	if err := tx.Model(&AgentRevisionRow{}).
		Where("agent_id = ? AND revision = ? AND state = ? AND attempt_count = ?",
			revision.AgentID, revision.Revision, AgentRevisionStateApplying, revision.AttemptCount).
		Updates(updates).Error; err != nil {
		return CoordinatorFailureResult{}, err
	}
	eventType := "revision_retry_scheduled"
	if exhausted {
		eventType = "revision_failed"
	}
	if err := appendCoordinatorEventTx(tx, revision, eventType, request.Now, map[string]any{
		"retry_cycle": attempt.RetryCycle, "attempt": attempt.Attempt,
		"lease_id": attempt.LeaseID, "error_code": errorCode,
		"error_message": errorMessage, "retry_delay_ms": retryDelay.Milliseconds(),
		"exhausted": exhausted,
	}); err != nil {
		return CoordinatorFailureResult{}, err
	}
	if err := refreshCoordinatorOperationTx(tx, revision.OperationID, request.Now); err != nil {
		return CoordinatorFailureResult{}, err
	}
	return CoordinatorFailureResult{
		Revision: revision, Attempt: attempt, RetryDelay: retryDelay, Exhausted: exhausted,
	}, nil
}

func reconcileOrphanApplyingRevisionTx(tx *gorm.DB, revision *AgentRevisionRow, request CoordinatorReconcileRequest) error {
	if revision.AttemptCount <= 0 {
		revision.State = AgentRevisionStatePending
		revision.NextAttemptAt = nil
		revision.GenerationID = ""
		revision.ErrorCode = "orphaned_apply"
		revision.ErrorMessage = "applying revision had no active attempt"
		revision.UpdatedAt = request.Now
		return tx.Model(&AgentRevisionRow{}).
			Where("agent_id = ? AND revision = ? AND state = ?", revision.AgentID, revision.Revision, AgentRevisionStateApplying).
			Updates(map[string]any{
				"state": AgentRevisionStatePending, "next_attempt_at": nil, "generation_id": "",
				"error_code": revision.ErrorCode, "error_message": revision.ErrorMessage, "updated_at": request.Now,
			}).Error
	}
	updates := map[string]any{
		"generation_id": "", "error_code": "orphaned_apply",
		"error_message": "applying revision had no active attempt", "updated_at": request.Now,
	}
	if revision.AttemptCount >= coordinatorMaxAttempts {
		revision.State = AgentRevisionStateFailed
		revision.NextAttemptAt = nil
		failedAt := request.Now
		revision.FailedAt = &failedAt
		updates["state"] = AgentRevisionStateFailed
		updates["next_attempt_at"] = nil
		updates["failed_at"] = request.Now
	} else {
		delay := coordinatorRetryDelay(revision.AttemptCount, coordinatorRetryBase, coordinatorRetryLimit, request.Jitter)
		next := request.Now.Add(delay)
		revision.State = AgentRevisionStatePending
		revision.NextAttemptAt = &next
		revision.FailedAt = nil
		updates["state"] = AgentRevisionStatePending
		updates["next_attempt_at"] = next
		updates["failed_at"] = nil
	}
	revision.GenerationID = ""
	revision.ErrorCode = "orphaned_apply"
	revision.ErrorMessage = "applying revision had no active attempt"
	revision.UpdatedAt = request.Now
	if err := tx.Model(&AgentRevisionRow{}).
		Where("agent_id = ? AND revision = ? AND state = ?", revision.AgentID, revision.Revision, AgentRevisionStateApplying).
		Updates(updates).Error; err != nil {
		return err
	}
	if err := appendCoordinatorEventTx(tx, *revision, "orphaned_apply_reconciled", request.Now, map[string]any{}); err != nil {
		return err
	}
	return refreshCoordinatorOperationTx(tx, revision.OperationID, request.Now)
}

func activateCoordinatorGenerationTx(tx *gorm.DB, revision AgentRevisionRow, generationID string, now time.Time) (string, error) {
	var prior []AgentGenerationRow
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("agent_id = ? AND generation_id <> ? AND state = ?", revision.AgentID, generationID, GenerationStateActive).
		Find(&prior).Error; err != nil {
		return "", err
	}
	if len(prior) > 0 {
		if err := tx.Model(&AgentGenerationRow{}).
			Where("agent_id = ? AND generation_id <> ? AND state = ?", revision.AgentID, generationID, GenerationStateActive).
			Updates(map[string]any{"state": GenerationStateDraining, "updated_at": now}).Error; err != nil {
			return "", err
		}
	}
	row := AgentGenerationRow{
		AgentID: revision.AgentID, GenerationID: generationID, Revision: revision.Revision,
		State: GenerationStateActive, CreatedAt: now, UpdatedAt: now,
	}
	if err := tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "agent_id"}, {Name: "generation_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"revision": revision.Revision, "state": GenerationStateActive,
			"forced": false, "force_reason": "", "drained_at": nil, "updated_at": now,
		}),
	}).Create(&row).Error; err != nil {
		return "", err
	}
	var draining int64
	if err := tx.Model(&AgentGenerationRow{}).
		Where("agent_id = ? AND state = ?", revision.AgentID, GenerationStateDraining).
		Count(&draining).Error; err != nil {
		return "", err
	}
	if draining > 0 {
		return AgentRevisionDrainStateDraining, nil
	}
	return AgentRevisionDrainStateDrained, nil
}

func updateCoordinatorPointerTx(tx *gorm.DB, pointer AgentRevisionPointerRow) error {
	result := tx.Model(&AgentRevisionPointerRow{}).
		Where("agent_id = ? AND desired_revision <= ? AND applied_revision <= ? AND last_known_good_revision <= ?",
			pointer.AgentID, pointer.DesiredRevision, pointer.AppliedRevision, pointer.LastKnownGoodRevision).
		Updates(map[string]any{
			"desired_revision": pointer.DesiredRevision, "applied_revision": pointer.AppliedRevision,
			"last_known_good_revision": pointer.LastKnownGoodRevision, "updated_at": pointer.UpdatedAt,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 1 {
		return nil
	}
	var current AgentRevisionPointerRow
	if err := tx.Where("agent_id = ?", pointer.AgentID).First(&current).Error; err != nil {
		return err
	}
	if current.DesiredRevision == pointer.DesiredRevision &&
		current.AppliedRevision == pointer.AppliedRevision &&
		current.LastKnownGoodRevision == pointer.LastKnownGoodRevision {
		return nil
	}
	return coordinatorStateConflict("agent %q revision pointer changed concurrently", pointer.AgentID)
}

func refreshCoordinatorOperationTx(tx *gorm.DB, operationID string, now time.Time) error {
	operationID = strings.TrimSpace(operationID)
	if operationID == "" {
		return nil
	}
	var revisions []AgentRevisionRow
	if err := tx.Where("operation_id = ?", operationID).Find(&revisions).Error; err != nil {
		return err
	}
	if len(revisions) == 0 {
		return nil
	}
	status := OperationStatusApplied
	completed := true
	var errorCode, errorMessage string
	for _, revision := range revisions {
		switch revision.State {
		case AgentRevisionStateFailed:
			status = OperationStatusFailed
			errorCode, errorMessage = revision.ErrorCode, revision.ErrorMessage
		case AgentRevisionStateApplying:
			if status != OperationStatusFailed {
				status = OperationStatusApplying
			}
			completed = false
		case AgentRevisionStatePending:
			if status != OperationStatusFailed && status != OperationStatusApplying {
				status = OperationStatusPending
			}
			completed = false
		case AgentRevisionStateSuperseded:
			if status == OperationStatusApplied {
				status = OperationStatusSuperseded
			}
		}
	}
	updates := map[string]any{
		"status": status, "error_code": errorCode, "error_message": errorMessage, "updated_at": now,
	}
	if completed {
		updates["completed_at"] = now
	} else {
		updates["completed_at"] = nil
	}
	return tx.Model(&OperationRow{}).Where("id = ?", operationID).Updates(updates).Error
}

func appendCoordinatorEventTx(tx *gorm.DB, revision AgentRevisionRow, eventType string, now time.Time, payload map[string]any) error {
	if payload == nil {
		payload = map[string]any{}
	}
	payload["state"] = revision.State
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return tx.Create(&RevisionEventRow{
		OperationID: revision.OperationID, AgentID: revision.AgentID, Revision: revision.Revision,
		EventType: eventType, PayloadJSON: string(payloadJSON), CreatedAt: now,
	}).Error
}

func copyCoordinatorSnapshotPayload(payload []byte, sourceRevision, targetRevision int64) ([]byte, string, error) {
	var snapshot Snapshot
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		return nil, "", fmt.Errorf("decode last-known-good snapshot: %w", err)
	}
	if snapshot.Revision != sourceRevision {
		return nil, "", coordinatorStateConflict("last-known-good snapshot revision is %d, want %d", snapshot.Revision, sourceRevision)
	}
	snapshot.Revision = targetRevision
	copyPayload, err := json.Marshal(snapshot)
	if err != nil {
		return nil, "", fmt.Errorf("encode rollback snapshot: %w", err)
	}
	digest := sha256.Sum256(copyPayload)
	return copyPayload, hex.EncodeToString(digest[:]), nil
}

// coordinatorRetryDelay implements uniform(0, min(limit, base*2^(attempt-1))).
func coordinatorRetryDelay(attempt int, base, limit time.Duration, jitter float64) time.Duration {
	if base <= 0 {
		base = time.Second
	}
	if limit <= 0 {
		limit = 30 * time.Second
	}
	if limit < base {
		limit = base
	}
	bound := base
	for current := 1; current < attempt && bound < limit; current++ {
		if bound > limit/2 {
			bound = limit
			break
		}
		bound *= 2
	}
	if bound > limit {
		bound = limit
	}
	if math.IsNaN(jitter) || jitter < 0 {
		jitter = 0
	}
	if jitter >= 1 {
		jitter = math.Nextafter(1, 0)
	}
	return time.Duration(float64(bound) * jitter)
}

func validateCoordinatorLease(lease CoordinatorLease) error {
	if strings.TrimSpace(lease.AgentID) == "" || lease.Revision < 0 || lease.RetryCycle < 0 || lease.Attempt <= 0 || strings.TrimSpace(lease.LeaseID) == "" {
		return fmt.Errorf("coordinator lease identity is invalid")
	}
	return nil
}

func resolvedCoordinatorTimeoutSeconds(value, configured, fallback int) int {
	if value > 0 {
		return value
	}
	if configured > 0 {
		return configured
	}
	return fallback
}

func coordinatorTime(value time.Time) time.Time {
	if value.IsZero() {
		return time.Now().UTC()
	}
	return value.UTC()
}

func coordinatorLeaseConflict(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrCoordinatorLeaseConflict, fmt.Sprintf(format, args...))
}

func coordinatorStateConflict(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrCoordinatorStateConflict, fmt.Sprintf(format, args...))
}

func appendUniqueRevisions(existing []int64, additions ...int64) []int64 {
	seen := make(map[int64]struct{}, len(existing)+len(additions))
	for _, revision := range existing {
		seen[revision] = struct{}{}
	}
	for _, revision := range additions {
		if _, ok := seen[revision]; ok {
			continue
		}
		existing = append(existing, revision)
		seen[revision] = struct{}{}
	}
	sort.Slice(existing, func(i, j int) bool { return existing[i] < existing[j] })
	return existing
}

func maxRevisionInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}
