package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/control"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/observability"
)

type RevisionSyncClient interface {
	PullRevision(context.Context) (model.RevisionPull, error)
	StartRevision(context.Context, model.RevisionStart) error
	ReportRevision(context.Context, model.RevisionReport) error
}

type GenerationJournalStore interface {
	SaveGenerationJournal(model.GenerationJournal) error
	LoadGenerationJournal() (model.GenerationJournal, error)
	SaveLastKnownGoodSnapshot(model.Snapshot) error
	LoadLastKnownGoodSnapshot() (model.Snapshot, error)
}

func (c *SyncController) performRevisionSyncPlan(
	ctx context.Context,
	plan SyncPlan,
	client RevisionSyncClient,
	store GenerationJournalStore,
) error {
	if err := c.restoreDurableRevisionRuntime(ctx); err != nil {
		return c.recordRuntimeError(err)
	}
	journal, err := store.LoadGenerationJournal()
	if err != nil {
		return c.recordRuntimeError(err)
	}
	if err := validateGenerationJournal(journal); err != nil {
		return c.recordRuntimeError(err)
	}
	if err := c.authorizeJournalPluginLogRetirements(journal); err != nil {
		return c.recordRuntimeError(err)
	}
	acknowledgementErr := c.recoverActiveRevisionAcknowledgement(ctx, client, store, &journal)
	if acknowledgementErr == nil {
		if err := c.reportCompletedGenerationDrains(ctx, client, store, &journal); err != nil {
			return c.recordRuntimeError(err)
		}
	}
	heartbeatSnapshot, err := c.SyncClient.Sync(ctx, plan.Request)
	if err != nil {
		return c.recordSyncError(errors.Join(acknowledgementErr, err))
	}
	if err := c.commitHeartbeatTrafficRuntime(ctx, heartbeatSnapshot); err != nil {
		return c.recordRuntimeError(errors.Join(acknowledgementErr, err))
	}
	if len(plan.RuntimeMetadata) > 0 {
		if err := c.persistRuntimeMetadata(plan.RuntimeMetadata); err != nil {
			return c.recordRuntimeError(err)
		}
	}
	pull, err := client.PullRevision(ctx)
	if err != nil {
		return c.recordSyncError(errors.Join(acknowledgementErr, err))
	}
	if !pull.HasUpdate {
		// Revision mode still uses the heartbeat as the delivery channel for a
		// bundled fallback binary. Configuration payloads remain lease-gated, but
		// a package-only heartbeat must be handled when there is no revision to
		// apply or existing agents will never adopt control-plane upgrades.
		updateErr := c.handlePendingUpdate(ctx, heartbeatSnapshot)
		if errors.Is(updateErr, ErrRestartRequested) {
			if acknowledgementErr != nil {
				c.recordRuntimeError(acknowledgementErr)
			}
			return errors.Join(acknowledgementErr, updateErr)
		}
		combinedErr := errors.Join(acknowledgementErr, updateErr)
		if combinedErr != nil {
			return c.recordRuntimeError(combinedErr)
		}
		return c.clearLastSyncErrorAfterSuccessfulSync()
	}
	lease, snapshot, digest, err := validateRevisionPull(pull)
	if err != nil {
		return c.recordRuntimeError(err)
	}
	leaseCtx, cancelLease := context.WithDeadline(ctx, lease.DeadlineAt)
	defer cancelLease()
	if journal.Version == 0 {
		journal.Version = 1
		journal.AgentID = lease.AgentID
	}
	if journal.AgentID != "" && journal.AgentID != lease.AgentID {
		return c.recordRuntimeError(fmt.Errorf("generation journal belongs to agent %q, not %q", journal.AgentID, lease.AgentID))
	}
	journal.AgentID = lease.AgentID

	previousApplied := c.Runtime.ActiveSnapshot()
	lastKnownGood, err := store.LoadLastKnownGoodSnapshot()
	if err != nil {
		return c.recordRuntimeError(err)
	}
	durableRevision := durableRevisionFloor(previousApplied.Revision, lastKnownGood.Revision, journal)
	if snapshot.Revision < durableRevision {
		return c.recordRuntimeError(fmt.Errorf("stale revision %d cannot replace durable revision %d", snapshot.Revision, durableRevision))
	}
	if err := validateImmutableRevisionDigest(journal, snapshot.Revision, digest); err != nil {
		return c.recordRuntimeError(err)
	}
	previousApplied = c.Runtime.ActiveSnapshot()
	if failed := journal.Candidate; failed != nil && failed.Phase == model.GenerationPhaseFailed &&
		sameGenerationLease(*failed, lease) && strings.EqualFold(failed.SnapshotDigest, digest) {
		return c.replayFailedRevisionReport(ctx, client, store, journal, *failed)
	}
	if err := c.preflightPendingUpdate(snapshot); err != nil {
		return c.recordRuntimeError(err)
	}
	runtimeSnapshotHash := durableRuntimeSnapshotHashForRevision(journal, snapshot.Revision, digest)
	runtimeIdentity, managedGeneration, err := c.Runtime.CandidateGenerationIdentityWithSnapshotHash(previousApplied, snapshot, runtimeSnapshotHash)
	if err != nil {
		return c.recordRuntimeError(err)
	}
	activeDigestMatches := journal.Active != nil && journal.Active.Revision == lease.Revision &&
		strings.EqualFold(journal.Active.SnapshotDigest, digest)
	if journal.Active != nil && sameGenerationLease(*journal.Active, lease) &&
		strings.EqualFold(journal.Active.SnapshotDigest, digest) {
		identityChanged, identityErr := bindRuntimeGenerationIdentity(journal.Active, runtimeIdentity, managedGeneration)
		if identityErr != nil {
			return c.recordRuntimeError(identityErr)
		}
		if identityErr := c.validateActiveRuntimeGeneration(*journal.Active); identityErr != nil {
			return c.recordRuntimeError(identityErr)
		}
		if identityChanged {
			if err := store.SaveGenerationJournal(journal); err != nil {
				return c.recordRuntimeError(err)
			}
		}
		if journal.Active.Acknowledged || journal.Active.AppliedReportRejected {
			return nil
		}
		if err := c.resolveAppliedRevisionReport(ctx, client, journal.Active); err != nil {
			return c.recordRuntimeError(err)
		}
		return store.SaveGenerationJournal(journal)
	}

	generationID := revisionGenerationID(lease)
	ctx = observability.WithCorrelation(ctx, observability.Correlation{
		AgentID: lease.AgentID, Revision: lease.Revision, GenerationID: generationID, Attempt: lease.Attempt,
	})
	candidate := model.GenerationRecord{
		GenerationID: generationID, Revision: lease.Revision, SnapshotDigest: digest,
		Phase: model.GenerationPhasePrepared, Lease: lease, UpdatedAt: time.Now().UTC(),
	}
	if _, err := bindRuntimeGenerationIdentity(&candidate, runtimeIdentity, managedGeneration); err != nil {
		return c.recordRuntimeError(err)
	}
	resumePhase := ""
	if journal.Candidate != nil && sameGenerationLease(*journal.Candidate, lease) &&
		strings.EqualFold(journal.Candidate.SnapshotDigest, digest) {
		candidate = *journal.Candidate
		resumePhase = candidate.Phase
		identityChanged, identityErr := bindRuntimeGenerationIdentity(&candidate, runtimeIdentity, managedGeneration)
		if identityErr != nil {
			return c.recordRuntimeError(identityErr)
		}
		if identityChanged {
			candidate.UpdatedAt = time.Now().UTC()
			journal.Candidate = &candidate
			if err := store.SaveGenerationJournal(journal); err != nil {
				return c.recordRuntimeError(err)
			}
		}
	} else {
		journal.Candidate = &candidate
		if err := store.SaveGenerationJournal(journal); err != nil {
			return c.recordRuntimeError(err)
		}
	}
	if resumePhase == model.GenerationPhaseStarted && c.runtimeGenerationIsActive(candidate) {
		candidate.Phase = model.GenerationPhaseCutover
		candidate.UpdatedAt = time.Now().UTC()
		journal.Candidate = &candidate
		if err := store.SaveGenerationJournal(journal); err != nil {
			return c.recordRuntimeError(err)
		}
		resumePhase = model.GenerationPhaseCutover
	}
	durableCutover := resumePhase == model.GenerationPhaseCutover

	if durableCutover {
		applied, loadErr := c.Store.LoadAppliedSnapshot()
		if loadErr == nil {
			sameSnapshot, digestErr := sameRevisionSnapshot(applied, snapshot)
			if digestErr == nil && applied.Revision == lease.Revision && sameSnapshot {
				if identityErr := c.validateActiveRuntimeGeneration(candidate); identityErr != nil {
					return c.recordRuntimeError(identityErr)
				}
				if err := c.authorizePluginLogRetirements(applied); err != nil {
					return c.recordRuntimeError(err)
				}
				if err := c.persistRuntimeState(true); err != nil {
					return c.recordRuntimeError(err)
				}
				return c.finishRevisionAcknowledgement(ctx, client, store, journal, candidate, applied)
			}
		}
	}
	if resumePhase == model.GenerationPhaseStarting {
		// The prior start request may have committed remotely even if its response was lost.
		// Let the authoritative lease expire/reconcile instead of replaying a non-idempotent start.
		return nil
	}
	if resumePhase != model.GenerationPhaseStarted && resumePhase != model.GenerationPhaseCutover {
		candidate.Phase = model.GenerationPhaseStarting
		candidate.UpdatedAt = time.Now().UTC()
		journal.Candidate = &candidate
		if err := store.SaveGenerationJournal(journal); err != nil {
			return c.recordRuntimeError(err)
		}
		if err := client.StartRevision(leaseCtx, model.RevisionStart{
			AgentID: lease.AgentID, Revision: lease.Revision, RetryCycle: lease.RetryCycle,
			Attempt: lease.Attempt, LeaseID: lease.LeaseID, GenerationID: generationID,
		}); err != nil {
			return c.recordRuntimeError(err)
		}
		candidate.Phase = model.GenerationPhaseStarted
		candidate.UpdatedAt = time.Now().UTC()
		journal.Candidate = &candidate
		if err := store.SaveGenerationJournal(journal); err != nil {
			return c.recordRuntimeError(err)
		}
	}
	if activeDigestMatches {
		applied, loadErr := c.Store.LoadAppliedSnapshot()
		if loadErr != nil {
			return c.recordRuntimeError(loadErr)
		}
		sameSnapshot, digestErr := sameRevisionSnapshot(applied, snapshot)
		if digestErr != nil {
			return c.recordRuntimeError(digestErr)
		}
		if applied.Revision == lease.Revision && sameSnapshot {
			if identityErr := c.validateActiveRuntimeGeneration(candidate); identityErr != nil {
				return c.recordRuntimeError(identityErr)
			}
			if err := c.authorizePluginLogRetirements(applied); err != nil {
				return c.recordRuntimeError(err)
			}
			if err := c.persistRuntimeState(true); err != nil {
				return c.recordRuntimeError(err)
			}
			return c.finishRevisionAcknowledgement(ctx, client, store, journal, candidate, applied)
		}
	}

	if err := leaseCtx.Err(); err != nil {
		if durableCutover {
			return c.recordRuntimeError(err)
		}
		return c.failRevisionAttempt(ctx, client, store, journal, candidate, err)
	}
	if err := c.Store.SaveDesiredSnapshot(snapshot); err != nil {
		if durableCutover {
			return c.recordRuntimeError(err)
		}
		return c.failRevisionAttempt(ctx, client, store, journal, candidate, err)
	}
	if err := c.handlePendingUpdate(leaseCtx, snapshot); err != nil {
		if errors.Is(err, ErrRestartRequested) {
			return err
		}
		if durableCutover {
			return c.recordRuntimeError(err)
		}
		return c.failRevisionAttempt(ctx, client, store, journal, candidate, err)
	}

	previousApplied = c.Runtime.ActiveSnapshot()
	candidateApplied := snapshot
	var applyErr error
	if trafficRuntime, ok := heartbeatTrafficRuntime(heartbeatSnapshot); ok {
		applyErr = c.Runtime.ApplyWithTrafficRuntimeAndSnapshotHash(
			leaseCtx, previousApplied, candidateApplied,
			time.Duration(lease.DrainTimeoutSeconds)*time.Second, trafficRuntime, runtimeSnapshotHash,
		)
	} else {
		applyErr = c.Runtime.ApplyWithDrainTimeoutAndSnapshotHash(
			leaseCtx, previousApplied, candidateApplied,
			time.Duration(lease.DrainTimeoutSeconds)*time.Second, runtimeSnapshotHash,
		)
	}
	if applyErr != nil {
		if durableCutover {
			return c.recordRuntimeErrorWithRevision(applyErr, candidate.Revision)
		}
		if c.Runtime.UsesGenerationManager() {
			return c.failRevisionAttempt(ctx, client, store, journal, candidate, applyErr)
		}
		rollbackErr := c.rollbackRuntime(ctx, candidateApplied, previousApplied)
		if rollbackErr != nil {
			return c.recordRuntimeErrorWithRevision(errors.Join(applyErr, rollbackErr), candidate.Revision)
		}
		return c.failRevisionAttempt(ctx, client, store, journal, candidate, applyErr)
	}
	if err := c.validateActiveRuntimeGeneration(candidate); err != nil {
		return c.recordRuntimeErrorWithRevision(err, candidate.Revision)
	}
	candidate.Phase = model.GenerationPhaseCutover
	candidate.UpdatedAt = time.Now().UTC()
	journal.Candidate = &candidate
	if err := store.SaveGenerationJournal(journal); err != nil {
		if c.Runtime.UsesGenerationManager() || isFilesystemCommitUncertain(err) {
			return c.recordRuntimeError(err)
		}
		rollbackErr := c.rollbackRuntime(ctx, candidateApplied, previousApplied)
		if rollbackErr != nil {
			return c.recordRuntimeErrorWithRevision(errors.Join(err, rollbackErr), candidate.Revision)
		}
		return c.failRevisionAttempt(ctx, client, store, journal, candidate, err)
	}
	if err := c.Store.SaveAppliedSnapshot(candidateApplied); err != nil {
		if c.Runtime.UsesGenerationManager() || isFilesystemCommitUncertain(err) {
			return c.recordRuntimeError(err)
		}
		rollbackErr := c.rollbackRuntime(ctx, candidateApplied, previousApplied)
		if rollbackErr != nil {
			return c.recordRuntimeErrorWithRevision(errors.Join(err, rollbackErr), candidate.Revision)
		}
		return c.failRevisionAttempt(ctx, client, store, journal, candidate, err)
	}
	if err := c.authorizePluginLogRetirements(candidateApplied); err != nil {
		return c.recordRuntimeErrorWithRevision(err, candidate.Revision)
	}
	if err := c.persistRuntimeState(true); err != nil {
		if c.Runtime.UsesGenerationManager() || isFilesystemCommitUncertain(err) {
			return c.recordRuntimeError(err)
		}
		rollbackErr := c.rollbackRuntime(ctx, candidateApplied, previousApplied)
		restoreErr := c.Store.SaveAppliedSnapshot(previousApplied)
		if rollbackErr != nil || restoreErr != nil {
			return c.recordRuntimeErrorWithRevision(errors.Join(err, rollbackErr, restoreErr), candidate.Revision)
		}
		return c.failRevisionAttempt(ctx, client, store, journal, candidate, err)
	}
	return c.finishRevisionAcknowledgement(ctx, client, store, journal, candidate, candidateApplied)
}

func heartbeatTrafficRuntime(snapshot model.Snapshot) (model.AgentConfig, bool) {
	if snapshot.AgentConfig.TrafficStatsEnabled == nil {
		return model.AgentConfig{}, false
	}
	enabled := *snapshot.AgentConfig.TrafficStatsEnabled
	return model.AgentConfig{
		TrafficStatsEnabled: &enabled,
		TrafficBlocked:      snapshot.AgentConfig.TrafficBlocked,
		TrafficBlockReason:  strings.TrimSpace(snapshot.AgentConfig.TrafficBlockReason),
	}, true
}

func (c *SyncController) authorizePluginLogRetirements(applied model.Snapshot) error {
	if c == nil || c.Store == nil {
		return nil
	}
	store, ok := c.Store.(PluginLogRetirementCutoverStore)
	if !ok {
		return nil
	}
	if err := store.AuthorizePluginRuntimeLogRetirementIntents(applied); err != nil {
		return fmt.Errorf("authorize plugin runtime log retirement after durable cutover: %w", err)
	}
	return nil
}

func (c *SyncController) authorizeJournalPluginLogRetirements(journal model.GenerationJournal) error {
	if c == nil || c.Store == nil {
		return nil
	}
	var records []*model.GenerationRecord
	if journal.Candidate != nil && journal.Candidate.Phase == model.GenerationPhaseCutover {
		records = append(records, journal.Candidate)
	}
	if journal.Active != nil {
		records = append(records, journal.Active)
	}
	if len(records) == 0 {
		return nil
	}
	applied, err := c.Store.LoadAppliedSnapshot()
	if err != nil {
		return err
	}
	activeIdentity, managed := c.Runtime.ActiveGenerationIdentity()
	for _, record := range records {
		if record.Revision != applied.Revision {
			continue
		}
		if record.RuntimeGenerationID != "" || record.RuntimeSnapshotHash != "" {
			if managed && activeIdentity.ID == record.RuntimeGenerationID && activeIdentity.Revision == record.Revision &&
				strings.EqualFold(activeIdentity.SnapshotHash, record.RuntimeSnapshotHash) {
				return c.authorizePluginLogRetirements(applied)
			}
			continue
		}
		digest, err := revisionSnapshotDigest(applied)
		if err != nil {
			return err
		}
		if strings.EqualFold(record.SnapshotDigest, digest) {
			return c.authorizePluginLogRetirements(applied)
		}
	}
	return nil
}

func (c *SyncController) commitHeartbeatTrafficRuntime(ctx context.Context, snapshot model.Snapshot) error {
	config, ok := heartbeatTrafficRuntime(snapshot)
	if !ok {
		return nil
	}
	state, err := c.runtimeStateForPersistence()
	if err != nil {
		return fmt.Errorf("load heartbeat traffic runtime state: %w", err)
	}
	previousState := state
	previousState.Metadata = cloneStringMap(state.Metadata)
	state.Metadata = ensureMetadata(state.Metadata)
	SetTrafficRuntimeMetadata(state.Metadata, config)
	if err := c.Store.SaveRuntimeState(state); err != nil {
		// A successfully authenticated block is security state. Even when the
		// first durable write fails, apply it fail-closed before returning and
		// retry the exact intent once so a transient persistence failure cannot
		// leave restart state behind the active provider.
		if !config.TrafficBlocked {
			return fmt.Errorf("persist heartbeat traffic runtime: %w", err)
		}
		reconcileErr := c.Runtime.ReconcileTrafficRuntime(context.WithoutCancel(ctx), config)
		retryErr := c.Store.SaveRuntimeState(state)
		if reconcileErr != nil || retryErr != nil {
			return errors.Join(
				fmt.Errorf("persist heartbeat traffic runtime: %w", err),
				wrapOptionalError("fail-closed heartbeat traffic runtime", reconcileErr),
				wrapOptionalError("retry heartbeat traffic runtime persistence", retryErr),
			)
		}
		return nil
	}
	if err := c.Runtime.ReconcileTrafficRuntime(context.WithoutCancel(ctx), config); err != nil {
		if config.TrafficBlocked {
			// The provider contract has already forced the active path closed and
			// Runtime recorded the same overlay; retain the durable blocked intent.
			return fmt.Errorf("reconcile heartbeat traffic runtime after fail-closed apply: %w", err)
		}
		if rollbackErr := c.Store.SaveRuntimeState(previousState); rollbackErr != nil {
			return errors.Join(
				fmt.Errorf("reconcile heartbeat traffic runtime: %w", err),
				fmt.Errorf("rollback heartbeat traffic runtime persistence: %w", rollbackErr),
			)
		}
		return fmt.Errorf("reconcile heartbeat traffic runtime: %w", err)
	}
	return nil
}

func wrapOptionalError(message string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", message, err)
}

func (c *SyncController) recoverActiveRevisionAcknowledgement(
	ctx context.Context,
	client RevisionSyncClient,
	store GenerationJournalStore,
	journal *model.GenerationJournal,
) error {
	if journal == nil || journal.Active == nil || journal.Active.Acknowledged || journal.Active.AppliedReportRejected {
		return nil
	}
	active := journal.Active
	if active.Phase != model.GenerationPhaseActive || active.Lease.Revision != active.Revision ||
		strings.TrimSpace(active.Lease.LeaseID) == "" || strings.TrimSpace(active.GenerationID) == "" {
		return errors.New("unacknowledged active generation is missing its applied report identity")
	}
	if err := c.validateActiveRuntimeGeneration(*active); err != nil {
		return err
	}
	// Applied reports are replayable: the first response may have been lost
	// after the coordinator committed the transition. The server accepts this
	// exact lease/generation identity idempotently.
	if err := c.resolveAppliedRevisionReport(ctx, client, active); err != nil {
		return err
	}
	return store.SaveGenerationJournal(*journal)
}

func (c *SyncController) resolveAppliedRevisionReport(ctx context.Context, client RevisionSyncClient, active *model.GenerationRecord) error {
	if active == nil {
		return errors.New("active generation is required for applied report")
	}
	var statuses []model.PluginRuntimeStatus
	if c != nil && c.Runtime != nil {
		statuses = c.Runtime.State().PluginStatuses
	}
	err := reportRevisionApplied(ctx, client, active.Lease, active.GenerationID, statuses)
	switch {
	case err == nil:
		active.Acknowledged = true
	case errors.Is(err, control.ErrRevisionLeaseConflict):
		// The coordinator has authoritatively moved past this lease. Preserve the
		// local runtime as the durable floor, but stop replaying an applied report
		// that can never commit.
		active.AppliedReportRejected = true
	default:
		return err
	}
	active.UpdatedAt = time.Now().UTC()
	return nil
}

func bindRuntimeGenerationIdentity(record *model.GenerationRecord, identity GenerationIdentity, managed bool) (bool, error) {
	if record == nil {
		return false, nil
	}
	if !managed {
		if record.RuntimeGenerationID != "" || record.RuntimeSnapshotHash != "" {
			return false, errors.New("durable runtime generation identity requires a generation manager")
		}
		return false, nil
	}
	if identity.ID == "" || identity.Revision != record.Revision || identity.SnapshotHash == "" {
		return false, errors.New("runtime generation identity is incomplete")
	}
	if record.RuntimeGenerationID != "" && record.RuntimeGenerationID != identity.ID {
		return false, fmt.Errorf("revision %d runtime generation changed from %s to %s", record.Revision, record.RuntimeGenerationID, identity.ID)
	}
	if record.RuntimeSnapshotHash != "" && !strings.EqualFold(record.RuntimeSnapshotHash, identity.SnapshotHash) {
		return false, fmt.Errorf("revision %d runtime snapshot hash changed", record.Revision)
	}
	changed := record.RuntimeGenerationID == "" || record.RuntimeSnapshotHash == ""
	record.RuntimeGenerationID = identity.ID
	record.RuntimeSnapshotHash = identity.SnapshotHash
	return changed, nil
}

func (c *SyncController) validateActiveRuntimeGeneration(record model.GenerationRecord) error {
	if record.RuntimeGenerationID == "" && record.RuntimeSnapshotHash == "" {
		return nil
	}
	identity, managed := c.Runtime.ActiveGenerationIdentity()
	if !managed || identity.ID == "" {
		return errors.New("durable generation record has no active runtime generation")
	}
	if identity.ID != record.RuntimeGenerationID || identity.Revision != record.Revision ||
		!strings.EqualFold(identity.SnapshotHash, record.RuntimeSnapshotHash) {
		return fmt.Errorf("active runtime generation does not match durable revision %d", record.Revision)
	}
	return nil
}

func (c *SyncController) runtimeGenerationIsActive(record model.GenerationRecord) bool {
	if record.RuntimeGenerationID == "" || record.RuntimeSnapshotHash == "" {
		return false
	}
	identity, managed := c.Runtime.ActiveGenerationIdentity()
	return managed && identity.ID == record.RuntimeGenerationID && identity.Revision == record.Revision &&
		strings.EqualFold(identity.SnapshotHash, record.RuntimeSnapshotHash)
}

func (c *SyncController) finishRevisionAcknowledgement(
	ctx context.Context,
	client RevisionSyncClient,
	store GenerationJournalStore,
	journal model.GenerationJournal,
	candidate model.GenerationRecord,
	applied model.Snapshot,
) error {
	previousActive := journal.Active
	candidate.Phase = model.GenerationPhaseActive
	candidate.Acknowledged = false
	candidate.UpdatedAt = time.Now().UTC()
	journal.Draining = appendDrainingGeneration(journal.Draining, previousActive, candidate)
	journal.Active = &candidate
	journal.LastKnownGood = &candidate
	journal.Candidate = nil
	if err := store.SaveLastKnownGoodSnapshot(applied); err != nil {
		return c.recordRuntimeError(err)
	}
	if err := store.SaveGenerationJournal(journal); err != nil {
		return c.recordRuntimeError(err)
	}
	if err := c.resolveAppliedRevisionReport(ctx, client, journal.Active); err != nil {
		return c.recordRuntimeError(err)
	}
	if err := store.SaveGenerationJournal(journal); err != nil {
		return c.recordRuntimeError(err)
	}
	if journal.Active.AppliedReportRejected {
		return nil
	}
	return c.reportCompletedGenerationDrains(ctx, client, store, &journal)
}

func appendDrainingGeneration(draining []model.GenerationRecord, previous *model.GenerationRecord, active model.GenerationRecord) []model.GenerationRecord {
	if previous == nil || previous.Revision >= active.Revision || strings.TrimSpace(previous.GenerationID) == "" ||
		previous.GenerationID == active.GenerationID {
		return draining
	}
	for i := range draining {
		if draining[i].GenerationID == previous.GenerationID {
			draining[i] = *previous
			return draining
		}
	}
	return append(draining, *previous)
}

func (c *SyncController) reportCompletedGenerationDrains(
	ctx context.Context,
	client RevisionSyncClient,
	store GenerationJournalStore,
	journal *model.GenerationJournal,
) error {
	if journal == nil || journal.Active == nil || !journal.Active.Acknowledged || len(journal.Draining) == 0 {
		return nil
	}
	lease := journal.Active.Lease
	if lease.Revision != journal.Active.Revision || strings.TrimSpace(lease.LeaseID) == "" {
		return errors.New("active generation is missing its authoritative drain lease")
	}

	runtimeDrains, managed := c.Runtime.GenerationDrainSnapshot()
	for index := 0; index < len(journal.Draining); {
		predecessor := journal.Draining[index]
		status, terminal := completedRuntimeDrain(runtimeDrains, managed, predecessor, *journal.Active)
		if !terminal {
			index++
			continue
		}
		if predecessor.AppliedReportRejected {
			journal.Draining = append(journal.Draining[:index], journal.Draining[index+1:]...)
			if err := store.SaveGenerationJournal(*journal); err != nil {
				return err
			}
			continue
		}
		report := status.RevisionReport(lease)
		// Runtime generation IDs are derived from the snapshot hash; the
		// coordinator tracks the lease-derived protocol generation ID instead.
		report.GenerationID = predecessor.GenerationID
		if err := client.ReportRevision(ctx, report); err != nil {
			return err
		}
		journal.Draining = append(journal.Draining[:index], journal.Draining[index+1:]...)
		if err := store.SaveGenerationJournal(*journal); err != nil {
			return err
		}
	}
	return nil
}

func completedRuntimeDrain(
	snapshot model.GenerationDrainSnapshot,
	managed bool,
	predecessor model.GenerationRecord,
	active model.GenerationRecord,
) (model.GenerationDrainStatus, bool) {
	if predecessor.Revision >= active.Revision {
		return model.GenerationDrainStatus{}, false
	}
	if !managed {
		return model.GenerationDrainStatus{
			GenerationID: predecessor.GenerationID,
			Revision:     predecessor.Revision,
			State:        model.GenerationDrainStateDrained,
			CompletedAt:  time.Now().UTC(),
		}, true
	}
	for _, status := range snapshot.Generations {
		matchesRuntimeID := predecessor.RuntimeGenerationID != "" && status.GenerationID == predecessor.RuntimeGenerationID
		if !matchesRuntimeID && predecessor.RuntimeGenerationID == "" && status.Revision == predecessor.Revision {
			matchesRuntimeID = true
		}
		if !matchesRuntimeID {
			continue
		}
		if (status.State == model.GenerationDrainStateDrained || status.State == model.GenerationDrainStateForced) &&
			!status.CompletedAt.IsZero() {
			return status, true
		}
		return model.GenerationDrainStatus{}, false
	}
	// Absence is not terminal evidence: during hot restart the stable supervisor
	// may still own the predecessor and its live sessions. The coordinator's
	// drain deadline provides the bounded recovery path when no runtime owner can
	// report a natural completion.
	return model.GenerationDrainStatus{}, false
}

func (c *SyncController) failRevisionAttempt(
	ctx context.Context,
	client RevisionSyncClient,
	store GenerationJournalStore,
	journal model.GenerationJournal,
	candidate model.GenerationRecord,
	applyErr error,
) error {
	candidate.Phase = model.GenerationPhaseFailed
	candidate.Acknowledged = false
	candidate.ErrorCode = "apply_failed"
	candidate.ErrorMessage = applyErr.Error()
	candidate.UpdatedAt = time.Now().UTC()
	journal.Candidate = &candidate
	journalErr := store.SaveGenerationJournal(journal)
	if journalErr != nil {
		return c.recordRuntimeErrorWithRevision(errors.Join(applyErr, journalErr), candidate.Revision)
	}
	reportErr := reportRevisionFailed(ctx, client, candidate)
	if reportErr != nil {
		return c.recordRuntimeErrorWithRevision(errors.Join(applyErr, reportErr), candidate.Revision)
	}
	candidate.Acknowledged = true
	candidate.UpdatedAt = time.Now().UTC()
	journal.Candidate = &candidate
	acknowledgementErr := store.SaveGenerationJournal(journal)
	return c.recordRuntimeErrorWithRevision(errors.Join(applyErr, acknowledgementErr), candidate.Revision)
}

func (c *SyncController) replayFailedRevisionReport(
	ctx context.Context,
	client RevisionSyncClient,
	store GenerationJournalStore,
	journal model.GenerationJournal,
	candidate model.GenerationRecord,
) error {
	if candidate.Acknowledged {
		return nil
	}
	if err := reportRevisionFailed(ctx, client, candidate); err != nil {
		return c.recordRuntimeErrorWithRevision(err, candidate.Revision)
	}
	candidate.Acknowledged = true
	candidate.UpdatedAt = time.Now().UTC()
	journal.Candidate = &candidate
	if err := store.SaveGenerationJournal(journal); err != nil {
		return c.recordRuntimeErrorWithRevision(err, candidate.Revision)
	}
	return nil
}

func reportRevisionFailed(ctx context.Context, client RevisionSyncClient, candidate model.GenerationRecord) error {
	errorCode := strings.TrimSpace(candidate.ErrorCode)
	if errorCode == "" {
		errorCode = "apply_failed"
	}
	errorMessage := strings.TrimSpace(candidate.ErrorMessage)
	if errorMessage == "" {
		errorMessage = "revision apply failed"
	}
	return client.ReportRevision(ctx, model.RevisionReport{
		AgentID: candidate.Lease.AgentID, Revision: candidate.Lease.Revision,
		RetryCycle: candidate.Lease.RetryCycle, Attempt: candidate.Lease.Attempt,
		LeaseID: candidate.Lease.LeaseID, GenerationID: candidate.GenerationID,
		Status: "failed", ErrorCode: errorCode, ErrorMessage: errorMessage,
	})
}

func validateRevisionPull(pull model.RevisionPull) (model.RevisionLease, model.Snapshot, string, error) {
	if pull.Lease == nil || pull.Snapshot == nil {
		return model.RevisionLease{}, model.Snapshot{}, "", errors.New("revision pull returned an incomplete update")
	}
	lease := *pull.Lease
	snapshot := *pull.Snapshot
	if strings.TrimSpace(lease.AgentID) == "" || lease.Revision <= 0 || lease.RetryCycle < 0 || lease.Attempt <= 0 ||
		lease.ApplyTimeoutSeconds <= 0 || lease.DrainTimeoutSeconds <= 0 ||
		strings.TrimSpace(lease.LeaseID) == "" || snapshot.Revision != lease.Revision ||
		pull.DesiredRevision != lease.Revision || snapshot.DesiredVersion != lease.DesiredVersion {
		return model.RevisionLease{}, model.Snapshot{}, "", errors.New("revision pull identity is inconsistent")
	}
	if lease.DeadlineAt.IsZero() || !time.Now().UTC().Before(lease.DeadlineAt) {
		return model.RevisionLease{}, model.Snapshot{}, "", errors.New("revision lease deadline is missing or expired")
	}
	if !snapshot.HasFullRevisionPayload() {
		return model.RevisionLease{}, model.Snapshot{}, "", errors.New("revision pull snapshot is not a full snapshot")
	}
	digest := strings.TrimSpace(pull.VerifiedSnapshotDigest)
	decodedDigest, digestErr := hex.DecodeString(digest)
	if digestErr != nil || len(decodedDigest) != sha256.Size ||
		strings.TrimSpace(lease.SnapshotDigest) == "" || !strings.EqualFold(digest, lease.SnapshotDigest) {
		return model.RevisionLease{}, model.Snapshot{}, "", errors.New("revision snapshot digest does not match lease")
	}
	return lease, snapshot, digest, nil
}

func revisionSnapshotDigest(snapshot model.Snapshot) (string, error) {
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func sameRevisionSnapshot(left, right model.Snapshot) (bool, error) {
	leftDigest, err := revisionSnapshotDigest(left)
	if err != nil {
		return false, err
	}
	rightDigest, err := revisionSnapshotDigest(right)
	if err != nil {
		return false, err
	}
	return strings.EqualFold(leftDigest, rightDigest), nil
}

func durableRevisionFloor(runtimeRevision, lastKnownGoodRevision int64, journal model.GenerationJournal) int64 {
	floor := max(runtimeRevision, lastKnownGoodRevision)
	if journal.Active != nil {
		floor = max(floor, journal.Active.Revision)
	}
	if journal.LastKnownGood != nil {
		floor = max(floor, journal.LastKnownGood.Revision)
	}
	if journal.Candidate != nil {
		floor = max(floor, journal.Candidate.Revision)
	}
	for i := range journal.Draining {
		floor = max(floor, journal.Draining[i].Revision)
	}
	return floor
}

func validateImmutableRevisionDigest(journal model.GenerationJournal, revision int64, digest string) error {
	records := []*model.GenerationRecord{journal.Active, journal.LastKnownGood, journal.Candidate}
	for i := range journal.Draining {
		records = append(records, &journal.Draining[i])
	}
	for _, record := range records {
		if record == nil || record.Revision != revision || strings.TrimSpace(record.SnapshotDigest) == "" {
			continue
		}
		if !strings.EqualFold(record.SnapshotDigest, digest) {
			return fmt.Errorf("immutable revision %d changed snapshot digest", revision)
		}
	}
	return nil
}

func validateGenerationJournal(journal model.GenerationJournal) error {
	if journal.Version == 0 {
		if journal.Active != nil || journal.Candidate != nil || journal.LastKnownGood != nil || len(journal.Draining) > 0 {
			return errors.New("unversioned generation journal contains records")
		}
		return nil
	}
	if journal.Version != 1 {
		return fmt.Errorf("unsupported generation journal version %d", journal.Version)
	}
	entries := []struct {
		name   string
		record *model.GenerationRecord
	}{
		{name: "active", record: journal.Active},
		{name: "last-known-good", record: journal.LastKnownGood},
		{name: "candidate", record: journal.Candidate},
	}
	for i := range journal.Draining {
		entries = append(entries, struct {
			name   string
			record *model.GenerationRecord
		}{name: fmt.Sprintf("draining[%d]", i), record: &journal.Draining[i]})
	}
	for _, entry := range entries {
		name, record := entry.name, entry.record
		if record == nil {
			continue
		}
		generationIDSet := strings.TrimSpace(record.RuntimeGenerationID) != ""
		snapshotHashSet := strings.TrimSpace(record.RuntimeSnapshotHash) != ""
		if generationIDSet != snapshotHashSet || generationIDSet != (record.RuntimeGenerationID != "") ||
			snapshotHashSet != (record.RuntimeSnapshotHash != "") {
			return fmt.Errorf("%s generation has incomplete runtime identity", name)
		}
	}
	if journal.Active != nil && journal.Active.Phase != model.GenerationPhaseActive {
		return fmt.Errorf("active generation has invalid phase %q", journal.Active.Phase)
	}
	if journal.LastKnownGood != nil && journal.LastKnownGood.Phase != model.GenerationPhaseActive {
		return fmt.Errorf("last-known-good generation has invalid phase %q", journal.LastKnownGood.Phase)
	}
	if journal.Candidate != nil {
		switch journal.Candidate.Phase {
		case model.GenerationPhasePrepared, model.GenerationPhaseStarting, model.GenerationPhaseStarted,
			model.GenerationPhaseCutover, model.GenerationPhaseFailed:
		default:
			return fmt.Errorf("candidate generation has invalid phase %q", journal.Candidate.Phase)
		}
	}
	for i := range journal.Draining {
		if journal.Draining[i].Phase != model.GenerationPhaseActive {
			return fmt.Errorf("draining generation has invalid phase %q", journal.Draining[i].Phase)
		}
		if journal.Active != nil && journal.Draining[i].Revision >= journal.Active.Revision {
			return fmt.Errorf("draining generation revision %d is not older than active revision %d", journal.Draining[i].Revision, journal.Active.Revision)
		}
	}
	return nil
}

func (c *SyncController) restoreDurableRevisionRuntime(ctx context.Context) error {
	if !isZeroSnapshot(c.Runtime.ActiveSnapshot()) {
		return nil
	}
	applied, err := c.Store.LoadAppliedSnapshot()
	if err != nil {
		return err
	}
	if isZeroSnapshot(applied) {
		return nil
	}
	runtimeState, err := c.Store.LoadRuntimeState()
	if err != nil {
		return err
	}
	trafficRuntime, hasTrafficRuntime, err := trafficRuntimeConfigFromMetadata(runtimeState.Metadata, applied.AgentConfig)
	if err != nil {
		return err
	}
	snapshotHash := ""
	if store, ok := c.Store.(GenerationJournalStore); ok {
		journal, loadErr := store.LoadGenerationJournal()
		if loadErr != nil {
			return loadErr
		}
		snapshotHash = durableAppliedRuntimeSnapshotHash(journal, applied.Revision)
	}
	if hasTrafficRuntime {
		if snapshotHash == "" {
			err = c.Runtime.ApplyWithTrafficRuntime(ctx, model.Snapshot{}, applied, 0, trafficRuntime)
		} else {
			err = c.Runtime.ApplyWithTrafficRuntimeAndSnapshotHash(ctx, model.Snapshot{}, applied, 0, trafficRuntime, snapshotHash)
		}
	} else {
		if snapshotHash == "" {
			err = c.Runtime.Apply(ctx, model.Snapshot{}, applied)
		} else {
			err = c.Runtime.ApplyWithSnapshotHash(ctx, model.Snapshot{}, applied, snapshotHash)
		}
	}
	if err != nil {
		return fmt.Errorf("restore durable applied snapshot: %w", err)
	}
	return c.persistRuntimeState(false)
}

func durableRuntimeSnapshotHashForRevision(journal model.GenerationJournal, revision int64, snapshotDigest string) string {
	for _, record := range []*model.GenerationRecord{journal.Candidate, journal.Active, journal.LastKnownGood} {
		if record != nil && record.Revision == revision && strings.EqualFold(record.SnapshotDigest, snapshotDigest) &&
			strings.TrimSpace(record.RuntimeSnapshotHash) != "" {
			return record.RuntimeSnapshotHash
		}
	}
	return snapshotDigest
}

func durableAppliedRuntimeSnapshotHash(journal model.GenerationJournal, revision int64) string {
	if journal.Candidate != nil && journal.Candidate.Revision == revision && journal.Candidate.Phase == model.GenerationPhaseCutover &&
		strings.TrimSpace(journal.Candidate.RuntimeSnapshotHash) != "" {
		return journal.Candidate.RuntimeSnapshotHash
	}
	for _, record := range []*model.GenerationRecord{journal.Active, journal.LastKnownGood} {
		if record != nil && record.Revision == revision && record.Phase == model.GenerationPhaseActive &&
			strings.TrimSpace(record.RuntimeSnapshotHash) != "" {
			return record.RuntimeSnapshotHash
		}
	}
	return ""
}

func revisionGenerationID(lease model.RevisionLease) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%d\x00%d\x00%s", lease.AgentID, lease.Revision, lease.RetryCycle, lease.Attempt, lease.LeaseID)))
	return fmt.Sprintf("generation-%d-%s", lease.Revision, hex.EncodeToString(digest[:8]))
}

func sameGenerationLease(record model.GenerationRecord, lease model.RevisionLease) bool {
	return record.Revision == lease.Revision && record.Lease.AgentID == lease.AgentID &&
		record.Lease.RetryCycle == lease.RetryCycle && record.Lease.Attempt == lease.Attempt &&
		record.Lease.LeaseID == lease.LeaseID
}

func reportRevisionApplied(ctx context.Context, client RevisionSyncClient, lease model.RevisionLease, generationID string, statuses []model.PluginRuntimeStatus) error {
	return client.ReportRevision(ctx, model.RevisionReport{
		AgentID: lease.AgentID, Revision: lease.Revision, RetryCycle: lease.RetryCycle,
		Attempt: lease.Attempt, LeaseID: lease.LeaseID, GenerationID: generationID,
		Status: "applied", PluginStatuses: append([]model.PluginRuntimeStatus(nil), statuses...),
	})
}
