package localagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	goagentembedded "github.com/sakullla/nginx-reverse-emby/go-agent/embedded"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

const defaultRevisionPollInterval = 250 * time.Millisecond

type RevisionClient interface {
	PullRemoteRevision(context.Context, string) (service.RemoteRevisionPull, error)
	GetAgentRevisionStatus(context.Context, string, int64) (service.AgentRevisionStatus, error)
	StartRemoteRevision(context.Context, string, service.RemoteRevisionStart) (service.AgentRevisionStatus, error)
	ReportRemoteRevision(context.Context, string, service.RemoteRevisionReport) (service.AgentRevisionStatus, error)
}

type RevisionLedger interface {
	GetAgentRevisionPointer(context.Context, string) (storage.AgentRevisionPointerRow, bool, error)
	GetCoordinatorRevision(context.Context, string, int64) (storage.AgentRevisionRow, bool, error)
	ListCoordinatorAttempts(context.Context, string, int64) ([]storage.AgentRevisionAttemptRow, error)
	ListCoordinatorGenerations(context.Context, string) ([]storage.AgentGenerationRow, error)
	LoadLocalRuntimeState(context.Context) (storage.RuntimeState, error)
}

type RevisionApplier interface {
	ApplyRevisionWithDrainTimeout(context.Context, storage.Snapshot, time.Duration) error
}

type revisionDrainStateReader interface {
	GenerationDrainSnapshot() goagentembedded.GenerationDrainSnapshot
}

type RevisionRuntime interface {
	RevisionApplier
	Start(context.Context) error
}

type RevisionWorker struct {
	agentID      string
	client       RevisionClient
	ledger       RevisionLedger
	runtime      RevisionApplier
	pollInterval time.Duration
	wake         chan struct{}
	reported     int64
}

func NewRevisionWorker(agentID string, client RevisionClient, ledger RevisionLedger, runtime RevisionApplier) (*RevisionWorker, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil, errors.New("local agent id is required")
	}
	if client == nil {
		return nil, errors.New("revision client is required")
	}
	if ledger == nil {
		return nil, errors.New("revision ledger is required")
	}
	if runtime == nil {
		return nil, errors.New("revision runtime is required")
	}
	return &RevisionWorker{
		agentID: agentID, client: client, ledger: ledger, runtime: runtime,
		pollInterval: defaultRevisionPollInterval, wake: make(chan struct{}, 1),
	}, nil
}

func (w *RevisionWorker) Wake() {
	if w == nil {
		return
	}
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (w *RevisionWorker) Run(ctx context.Context) error {
	if w == nil {
		return errors.New("revision worker is required")
	}
	if ctx.Err() != nil {
		return nil
	}
	interval := w.pollInterval
	if interval <= 0 {
		interval = defaultRevisionPollInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		if err := w.runCycle(ctx); err != nil && ctx.Err() == nil {
			log.Printf("[local-agent] revision worker cycle failed: %v", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-w.wake:
		case <-ticker.C:
		}
	}
}

func (w *RevisionWorker) runCycle(ctx context.Context) error {
	if ctx.Err() != nil {
		return nil
	}
	if err := w.retryAppliedReport(ctx); err != nil {
		return err
	}
	drainErr := w.completeOutstandingDrains(ctx)
	for ctx.Err() == nil {
		processed, err := w.processNext(ctx)
		if err != nil {
			return errors.Join(drainErr, err)
		}
		if !processed {
			return drainErr
		}
	}
	return drainErr
}

func (w *RevisionWorker) processNext(ctx context.Context) (bool, error) {
	pull, err := w.client.PullRemoteRevision(ctx, w.agentID)
	if err != nil {
		return false, err
	}
	if !pull.HasUpdate {
		return false, nil
	}
	if pull.Lease == nil || pull.Snapshot == nil {
		return false, errors.New("revision pull returned an incomplete update")
	}
	lease := *pull.Lease
	if lease.AgentID != w.agentID || lease.Revision <= 0 || lease.Revision != pull.Snapshot.Revision ||
		lease.Attempt <= 0 || strings.TrimSpace(lease.LeaseID) == "" ||
		lease.ApplyTimeoutSeconds <= 0 || lease.DrainTimeoutSeconds <= 0 ||
		lease.DeadlineAt.IsZero() || !time.Now().UTC().Before(lease.DeadlineAt) {
		return false, errors.New("revision pull returned an invalid lease or snapshot")
	}

	status, err := w.client.GetAgentRevisionStatus(ctx, w.agentID, lease.Revision)
	if err != nil {
		return false, err
	}
	attemptState := matchingAttemptState(status.Attempts, lease.RetryCycle, lease.Attempt)
	generationID := embeddedGenerationID(lease)
	switch attemptState {
	case storage.AgentRevisionAttemptStateLeased:
		if _, err := w.client.StartRemoteRevision(ctx, w.agentID, service.RemoteRevisionStart{
			AgentID: w.agentID, Revision: lease.Revision, RetryCycle: lease.RetryCycle,
			Attempt: lease.Attempt, LeaseID: lease.LeaseID, GenerationID: generationID,
		}); err != nil {
			return false, err
		}
	case storage.AgentRevisionAttemptStateStarted:
		// A process restart may resume the exact still-valid started lease.
	default:
		return false, fmt.Errorf("revision lease attempt is in unexpected state %q", attemptState)
	}

	if err := applyRevisionWithinLease(ctx, w.runtime, *pull.Snapshot, lease); err != nil {
		if ctx.Err() != nil {
			return false, nil
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return false, err
		}
		_, reportErr := w.client.ReportRemoteRevision(ctx, w.agentID, service.RemoteRevisionReport{
			AgentID: w.agentID, Revision: lease.Revision, RetryCycle: lease.RetryCycle,
			Attempt: lease.Attempt, LeaseID: lease.LeaseID, GenerationID: generationID,
			Status: storage.AgentRevisionStateFailed, ErrorCode: "embedded_apply_failed", ErrorMessage: err.Error(),
		})
		return false, errors.Join(err, reportErr)
	}
	pluginStatuses, err := w.pluginStatusesForRevision(ctx, lease.Revision)
	if err != nil {
		return false, err
	}
	status, err = w.client.ReportRemoteRevision(ctx, w.agentID, service.RemoteRevisionReport{
		AgentID: w.agentID, Revision: lease.Revision, RetryCycle: lease.RetryCycle,
		Attempt: lease.Attempt, LeaseID: lease.LeaseID, GenerationID: generationID,
		Status: storage.AgentRevisionStateApplied, PluginStatuses: pluginStatuses,
	})
	if err != nil {
		return false, err
	}
	w.reported = lease.Revision
	if err := w.completeDrainingGenerations(ctx, lease, status.Generations); err != nil {
		return false, err
	}
	return true, nil
}

func (w *RevisionWorker) retryAppliedReport(ctx context.Context) error {
	pointer, found, err := w.ledger.GetAgentRevisionPointer(ctx, w.agentID)
	if err != nil || !found || pointer.AppliedRevision <= 0 || pointer.AppliedRevision <= w.reported {
		return err
	}
	revision, found, err := w.ledger.GetCoordinatorRevision(ctx, w.agentID, pointer.AppliedRevision)
	if err != nil || !found || revision.State != storage.AgentRevisionStateApplied || strings.TrimSpace(revision.GenerationID) == "" {
		return err
	}
	attempts, err := w.ledger.ListCoordinatorAttempts(ctx, w.agentID, revision.Revision)
	if err != nil {
		return err
	}
	var appliedAttempt storage.AgentRevisionAttemptRow
	for _, attempt := range attempts {
		if attempt.RetryCycle == revision.RetryCycle && attempt.Attempt == revision.AttemptCount &&
			attempt.State == storage.AgentRevisionAttemptStateApplied {
			appliedAttempt = attempt
			break
		}
	}
	if strings.TrimSpace(appliedAttempt.LeaseID) == "" {
		return errors.New("applied local revision is missing its authoritative lease")
	}
	pluginStatuses, err := w.pluginStatusesForRevision(ctx, revision.Revision)
	if err != nil {
		return err
	}
	if _, err := w.client.ReportRemoteRevision(ctx, w.agentID, service.RemoteRevisionReport{
		AgentID: w.agentID, Revision: revision.Revision, RetryCycle: revision.RetryCycle,
		Attempt: appliedAttempt.Attempt, LeaseID: appliedAttempt.LeaseID, GenerationID: revision.GenerationID,
		Status: storage.AgentRevisionStateApplied, PluginStatuses: pluginStatuses,
	}); err != nil {
		return err
	}
	w.reported = revision.Revision
	return nil
}

func (w *RevisionWorker) pluginStatusesForRevision(ctx context.Context, revision int64) ([]storage.PluginRuntimeStatus, error) {
	state, err := w.ledger.LoadLocalRuntimeState(ctx)
	if err != nil {
		return nil, err
	}
	statuses := make([]storage.PluginRuntimeStatus, 0, len(state.PluginStatuses))
	for _, status := range state.PluginStatuses {
		if status.Revision != revision {
			continue
		}
		copyValue := status
		copyValue.Details = append([]byte(nil), status.Details...)
		copyValue.Budget = append([]byte(nil), status.Budget...)
		statuses = append(statuses, copyValue)
	}
	return statuses, nil
}

func applyRevisionWithinLease(ctx context.Context, runtime RevisionApplier, snapshot storage.Snapshot, lease service.RemoteRevisionLease) error {
	if runtime == nil {
		return errors.New("revision runtime is required")
	}
	if lease.DeadlineAt.IsZero() || lease.DrainTimeoutSeconds <= 0 {
		return errors.New("revision lease timing is invalid")
	}
	applyCtx, cancel := context.WithDeadline(ctx, lease.DeadlineAt)
	defer cancel()
	return runtime.ApplyRevisionWithDrainTimeout(applyCtx, snapshot, time.Duration(lease.DrainTimeoutSeconds)*time.Second)
}

func (w *RevisionWorker) completeOutstandingDrains(ctx context.Context) error {
	pointer, found, err := w.ledger.GetAgentRevisionPointer(ctx, w.agentID)
	if err != nil || !found || pointer.AppliedRevision <= 0 {
		return err
	}
	revision, found, err := w.ledger.GetCoordinatorRevision(ctx, w.agentID, pointer.AppliedRevision)
	if err != nil || !found || revision.State != storage.AgentRevisionStateApplied ||
		revision.DrainState != storage.AgentRevisionDrainStateDraining {
		return err
	}
	attempts, err := w.ledger.ListCoordinatorAttempts(ctx, w.agentID, revision.Revision)
	if err != nil {
		return err
	}
	var appliedAttempt storage.AgentRevisionAttemptRow
	for _, attempt := range attempts {
		if attempt.RetryCycle == revision.RetryCycle && attempt.Attempt == revision.AttemptCount &&
			attempt.State == storage.AgentRevisionAttemptStateApplied {
			appliedAttempt = attempt
			break
		}
	}
	if strings.TrimSpace(appliedAttempt.LeaseID) == "" {
		return errors.New("applied local revision is missing its authoritative lease")
	}
	generations, err := w.ledger.ListCoordinatorGenerations(ctx, w.agentID)
	if err != nil {
		return err
	}
	lease := service.RemoteRevisionLease{
		AgentID: w.agentID, Revision: revision.Revision, RetryCycle: revision.RetryCycle,
		Attempt: appliedAttempt.Attempt, LeaseID: appliedAttempt.LeaseID,
	}
	converted := make([]service.RevisionGeneration, 0, len(generations))
	for _, generation := range generations {
		converted = append(converted, service.RevisionGeneration{
			GenerationID: generation.GenerationID, Revision: generation.Revision, State: generation.State,
		})
	}
	return w.completeDrainingGenerations(ctx, lease, converted)
}

func (w *RevisionWorker) completeDrainingGenerations(ctx context.Context, lease service.RemoteRevisionLease, generations []service.RevisionGeneration) error {
	reader, ok := w.runtime.(revisionDrainStateReader)
	if !ok {
		// Production embedded runtimes expose drain state. A custom applier
		// without that contract cannot safely acknowledge asynchronous resources.
		return nil
	}
	runtimeDrains := reader.GenerationDrainSnapshot()
	for _, generation := range generations {
		if generation.State != storage.GenerationStateDraining || generation.Revision >= lease.Revision {
			continue
		}
		runtimeDrain, terminal := completedEmbeddedDrain(runtimeDrains, generation.Revision, lease.Revision)
		if !terminal {
			continue
		}
		status := storage.AgentRevisionDrainStateDrained
		forced := false
		if runtimeDrain.State == goagentembedded.GenerationDrainStateForced {
			status = storage.AgentRevisionDrainStateForced
			forced = true
		}
		if _, err := w.client.ReportRemoteRevision(ctx, w.agentID, service.RemoteRevisionReport{
			AgentID: w.agentID, Revision: lease.Revision, RetryCycle: lease.RetryCycle,
			Attempt: lease.Attempt, LeaseID: lease.LeaseID, GenerationID: generation.GenerationID,
			Status: status, Forced: forced, ForceReason: runtimeDrain.ForceReason,
		}); err != nil {
			return err
		}
	}
	return nil
}

func completedEmbeddedDrain(snapshot goagentembedded.GenerationDrainSnapshot, predecessorRevision, activeRevision int64) (goagentembedded.GenerationDrainStatus, bool) {
	if predecessorRevision >= activeRevision {
		return goagentembedded.GenerationDrainStatus{}, false
	}
	for _, status := range snapshot.Generations {
		if status.Revision != predecessorRevision {
			continue
		}
		if (status.State == goagentembedded.GenerationDrainStateDrained || status.State == goagentembedded.GenerationDrainStateForced) &&
			!status.CompletedAt.IsZero() {
			return status, true
		}
		return goagentembedded.GenerationDrainStatus{}, false
	}
	// A restarted embedded runtime has no predecessor sessions or resources.
	// Infer completion only after the authoritative active revision is visible.
	for _, status := range snapshot.Generations {
		if status.GenerationID == snapshot.ActiveGenerationID && status.Revision >= activeRevision &&
			status.State == goagentembedded.GenerationDrainStateApplied {
			return goagentembedded.GenerationDrainStatus{
				Revision: predecessorRevision, State: goagentembedded.GenerationDrainStateDrained,
				CompletedAt: time.Now().UTC(),
			}, true
		}
	}
	return goagentembedded.GenerationDrainStatus{}, false
}

func matchingAttemptState(attempts []service.RevisionAttempt, retryCycle, attemptNumber int) string {
	for _, attempt := range attempts {
		if attempt.RetryCycle == retryCycle && attempt.Attempt == attemptNumber {
			return attempt.State
		}
	}
	return ""
}

func embeddedGenerationID(lease service.RemoteRevisionLease) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%d\x00%d\x00%s", lease.AgentID, lease.Revision, lease.RetryCycle, lease.Attempt, lease.LeaseID)))
	return fmt.Sprintf("embedded-%d-%s", lease.Revision, hex.EncodeToString(digest[:8]))
}

func RunRevisionRuntime(ctx context.Context, runtime RevisionRuntime, worker *RevisionWorker) error {
	if runtime == nil || worker == nil {
		return errors.New("revision runtime and worker are required")
	}
	if ctx.Err() != nil {
		return runtime.Start(ctx)
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	runtimeErr := make(chan error, 1)
	workerErr := make(chan error, 1)
	go func() { runtimeErr <- runtime.Start(runCtx) }()
	go func() { workerErr <- worker.Run(runCtx) }()

	select {
	case <-ctx.Done():
		cancel()
		return errors.Join(<-runtimeErr, <-workerErr)
	case err := <-runtimeErr:
		cancel()
		return errors.Join(err, <-workerErr)
	case err := <-workerErr:
		cancel()
		return errors.Join(err, <-runtimeErr)
	}
}
