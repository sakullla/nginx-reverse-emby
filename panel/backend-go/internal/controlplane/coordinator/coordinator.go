package coordinator

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

const (
	defaultApplyTimeout = time.Minute
	defaultDrainTimeout = 10 * time.Minute
)

var (
	ErrLeaseConflict = storage.ErrCoordinatorLeaseConflict
	ErrStateConflict = storage.ErrCoordinatorStateConflict
	ErrNotFound      = storage.ErrCoordinatorNotFound
)

type Clock interface {
	Now() time.Time
}

type Random interface {
	Float64() float64
}

type IDGenerator func(prefix string) (string, error)

type Repository interface {
	ClaimLatestAgentRevision(context.Context, storage.CoordinatorClaimRequest) (storage.CoordinatorClaimResult, error)
	StartAgentRevisionAttempt(context.Context, storage.CoordinatorStartRequest) (storage.CoordinatorStartResult, error)
	FailAgentRevisionAttempt(context.Context, storage.CoordinatorFailureRequest) (storage.CoordinatorFailureResult, error)
	ApplyAgentRevisionAttempt(context.Context, storage.CoordinatorApplyRequest) (storage.CoordinatorApplyResult, error)
	CompleteCoordinatorDrain(context.Context, storage.CoordinatorDrainRequest) (storage.AgentRevisionRow, error)
	ReconcileCoordinatorAgent(context.Context, storage.CoordinatorReconcileRequest) (storage.CoordinatorReconcileResult, error)
	ListCoordinatorAgentIDs(context.Context) ([]string, error)
	RetryCoordinatorRevision(context.Context, string, int64, time.Time) (storage.AgentRevisionRow, error)
	CopyLastKnownGoodCoordinatorRevision(context.Context, storage.CoordinatorRollbackRequest) (storage.CoordinatorRollbackResult, error)
	ReconcileCoordinatorJournal(context.Context, storage.CoordinatorJournalRequest) (storage.CoordinatorJournalResult, error)
}

type idempotentActionRepository interface {
	RetryCoordinatorRevisionIdempotent(context.Context, storage.CoordinatorRetryRequest) (storage.CoordinatorRetryResult, error)
}

type Options struct {
	Clock        Clock
	Random       Random
	NewID        IDGenerator
	ApplyTimeout time.Duration
	DrainTimeout time.Duration
}

func OptionsFromConfig(cfg config.RevisionCoordinatorConfig) Options {
	return Options{
		ApplyTimeout: cfg.ApplyTimeout,
		DrainTimeout: cfg.DrainTimeout,
	}
}

type Coordinator struct {
	repository   Repository
	clock        Clock
	random       Random
	newID        IDGenerator
	applyTimeout time.Duration
	drainTimeout time.Duration
}

func New(repository Repository, options Options) (*Coordinator, error) {
	if repository == nil {
		return nil, fmt.Errorf("coordinator repository is required")
	}
	if options.Clock == nil {
		options.Clock = systemClock{}
	}
	if options.Random == nil {
		options.Random = cryptoRandom{}
	}
	if options.NewID == nil {
		options.NewID = randomID
	}
	if options.ApplyTimeout <= 0 {
		options.ApplyTimeout = defaultApplyTimeout
	}
	if options.DrainTimeout <= 0 {
		options.DrainTimeout = defaultDrainTimeout
	}
	return &Coordinator{
		repository: repository, clock: options.Clock, random: options.Random, newID: options.NewID,
		applyTimeout: options.ApplyTimeout, drainTimeout: options.DrainTimeout,
	}, nil
}

type Lease = storage.CoordinatorLease

type ClaimResult = storage.CoordinatorClaimResult

func (c *Coordinator) claim(ctx context.Context, agentID string) (ClaimResult, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return ClaimResult{}, fmt.Errorf("agent id is required")
	}
	for attempt := 0; attempt < 2; attempt++ {
		leaseID, err := c.newID("lease")
		if err != nil {
			return ClaimResult{}, fmt.Errorf("generate lease id: %w", err)
		}
		result, err := c.repository.ClaimLatestAgentRevision(ctx, storage.CoordinatorClaimRequest{
			AgentID: agentID, LeaseID: leaseID, Now: c.now(),
			DefaultApplyTimeoutSeconds: durationSeconds(c.applyTimeout),
			DefaultDrainTimeoutSeconds: durationSeconds(c.drainTimeout),
		})
		if err == nil {
			return result, nil
		}
		if !errors.Is(err, storage.ErrCoordinatorReconcileNeeded) {
			return ClaimResult{}, err
		}
		if _, reconcileErr := c.Reconcile(ctx, agentID); reconcileErr != nil {
			return ClaimResult{}, reconcileErr
		}
	}
	return ClaimResult{}, fmt.Errorf("%w: agent %q remained unreconciled", ErrStateConflict, agentID)
}

type StartRequest struct {
	Lease        Lease
	GenerationID string
}

type StartResult = storage.CoordinatorStartResult

func (c *Coordinator) start(ctx context.Context, request StartRequest) (StartResult, error) {
	return c.repository.StartAgentRevisionAttempt(ctx, storage.CoordinatorStartRequest{
		Lease: request.Lease, GenerationID: request.GenerationID, Now: c.now(),
		DefaultApplyTimeoutSeconds: durationSeconds(c.applyTimeout),
	})
}

type FailureReport struct {
	Lease        Lease
	GenerationID string
	ErrorCode    string
	ErrorMessage string
}

type FailureResult = storage.CoordinatorFailureResult

func (c *Coordinator) fail(ctx context.Context, report FailureReport) (FailureResult, error) {
	return c.repository.FailAgentRevisionAttempt(ctx, storage.CoordinatorFailureRequest{
		Lease: report.Lease, GenerationID: report.GenerationID, Now: c.now(), Jitter: c.random.Float64(),
		ErrorCode: report.ErrorCode, ErrorMessage: report.ErrorMessage,
	})
}

type AppliedReport struct {
	Lease        Lease
	GenerationID string
}

type AppliedResult = storage.CoordinatorApplyResult

func (c *Coordinator) applied(ctx context.Context, report AppliedReport) (AppliedResult, error) {
	return c.repository.ApplyAgentRevisionAttempt(ctx, storage.CoordinatorApplyRequest{
		Lease: report.Lease, GenerationID: report.GenerationID, Now: c.now(),
	})
}

type DrainReport struct {
	GenerationID string
	Lease        Lease
	Forced       bool
	ForceReason  string
}

func (c *Coordinator) drained(ctx context.Context, report DrainReport) (storage.AgentRevisionRow, error) {
	lease := storage.CoordinatorLease(report.Lease)
	return c.repository.CompleteCoordinatorDrain(ctx, storage.CoordinatorDrainRequest{
		AgentID: lease.AgentID, GenerationID: report.GenerationID, Lease: lease,
		Forced: report.Forced, ForceReason: report.ForceReason, Now: c.now(),
	})
}

type ReconcileResult = storage.CoordinatorReconcileResult

func (c *Coordinator) Reconcile(ctx context.Context, agentID string) (ReconcileResult, error) {
	return c.repository.ReconcileCoordinatorAgent(ctx, storage.CoordinatorReconcileRequest{
		AgentID: agentID, Now: c.now(), Jitter: c.random.Float64(),
	})
}

type StartupReconcileResult struct {
	Agents []ReconcileResult
}

func (c *Coordinator) ReconcileStartup(ctx context.Context) (StartupReconcileResult, error) {
	agentIDs, err := c.repository.ListCoordinatorAgentIDs(ctx)
	if err != nil {
		return StartupReconcileResult{}, err
	}
	result := StartupReconcileResult{Agents: make([]ReconcileResult, 0, len(agentIDs))}
	var reconcileErrors []error
	for _, agentID := range agentIDs {
		reconciled, err := c.Reconcile(ctx, agentID)
		if err != nil {
			reconcileErrors = append(reconcileErrors, fmt.Errorf("reconcile agent %q: %w", agentID, err))
			continue
		}
		result.Agents = append(result.Agents, reconciled)
	}
	return result, errors.Join(reconcileErrors...)
}

func (c *Coordinator) Retry(ctx context.Context, agentID string, revision int64) (storage.AgentRevisionRow, error) {
	result, err := c.RetryIdempotent(ctx, agentID, revision, storage.CoordinatorActionIdempotency{})
	return result.Revision, err
}

func (c *Coordinator) RetryIdempotent(
	ctx context.Context,
	agentID string,
	revision int64,
	idempotency storage.CoordinatorActionIdempotency,
) (storage.CoordinatorRetryResult, error) {
	if repository, ok := c.repository.(idempotentActionRepository); ok {
		return repository.RetryCoordinatorRevisionIdempotent(ctx, storage.CoordinatorRetryRequest{
			AgentID: agentID, Revision: revision, Now: c.now(), Idempotency: idempotency,
		})
	}
	if strings.TrimSpace(idempotency.Key) != "" {
		return storage.CoordinatorRetryResult{}, fmt.Errorf("coordinator repository does not support idempotent retry")
	}
	revisionRow, err := c.repository.RetryCoordinatorRevision(ctx, agentID, revision, c.now())
	return storage.CoordinatorRetryResult{Revision: revisionRow}, err
}

type RollbackResult = storage.CoordinatorRollbackResult

func (c *Coordinator) Rollback(ctx context.Context, agentID string) (RollbackResult, error) {
	return c.RollbackIdempotent(ctx, agentID, storage.CoordinatorActionIdempotency{})
}

func (c *Coordinator) RollbackIdempotent(
	ctx context.Context,
	agentID string,
	idempotency storage.CoordinatorActionIdempotency,
) (RollbackResult, error) {
	operationID, err := c.newID("rollback")
	if err != nil {
		return RollbackResult{}, fmt.Errorf("generate rollback operation id: %w", err)
	}
	return c.repository.CopyLastKnownGoodCoordinatorRevision(ctx, storage.CoordinatorRollbackRequest{
		AgentID: agentID, OperationID: operationID, Now: c.now(),
		DefaultApplyTimeoutSeconds: durationSeconds(c.applyTimeout),
		DefaultDrainTimeoutSeconds: durationSeconds(c.drainTimeout),
		Idempotency:                idempotency,
	})
}

func (c *Coordinator) RepairRuntime(ctx context.Context, agentID string, appliedRevision int64) (RollbackResult, error) {
	if appliedRevision <= 0 {
		return RollbackResult{}, fmt.Errorf("repair source revision is required")
	}
	operationID, err := c.newID("repair")
	if err != nil {
		return RollbackResult{}, fmt.Errorf("generate repair operation id: %w", err)
	}
	return c.repository.CopyLastKnownGoodCoordinatorRevision(ctx, storage.CoordinatorRollbackRequest{
		AgentID: agentID, OperationID: operationID, Now: c.now(),
		DefaultApplyTimeoutSeconds: durationSeconds(c.applyTimeout),
		DefaultDrainTimeoutSeconds: durationSeconds(c.drainTimeout),
		SourceRevision:             appliedRevision,
		RequireSourceCurrent:       true,
		OperationKind:              "repair_runtime_state",
		CreatedEventType:           "repair_revision_created",
	})
}

type JournalReport struct {
	AgentID        string
	Revision       int64
	SnapshotDigest string
	GenerationID   string
}

type JournalResult = storage.CoordinatorJournalResult

func (c *Coordinator) ReconcileJournal(ctx context.Context, report JournalReport) (JournalResult, error) {
	return c.repository.ReconcileCoordinatorJournal(ctx, storage.CoordinatorJournalRequest{
		AgentID: report.AgentID, Revision: report.Revision, SnapshotDigest: report.SnapshotDigest,
		GenerationID: report.GenerationID, Now: c.now(),
	})
}

func (c *Coordinator) now() time.Time {
	return c.clock.Now().UTC()
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC() }

type cryptoRandom struct{}

func (cryptoRandom) Float64() float64 {
	var buffer [8]byte
	if _, err := cryptorand.Read(buffer[:]); err != nil {
		return 0.5
	}
	const precision = uint64(1) << 53
	return float64(binary.LittleEndian.Uint64(buffer[:])&(precision-1)) / float64(precision)
}

func randomID(prefix string) (string, error) {
	var buffer [16]byte
	if _, err := cryptorand.Read(buffer[:]); err != nil {
		return "", err
	}
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = "id"
	}
	return prefix + "-" + hex.EncodeToString(buffer[:]), nil
}

func durationSeconds(value time.Duration) int {
	if value <= 0 {
		return 0
	}
	return int((value + time.Second - 1) / time.Second)
}
