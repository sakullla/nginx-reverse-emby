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

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
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
	if _, err := c.SyncClient.Sync(ctx, plan.Request); err != nil {
		return c.recordRuntimeError(err)
	}
	if len(plan.RuntimeMetadata) > 0 {
		if err := c.persistRuntimeMetadata(plan.RuntimeMetadata); err != nil {
			return c.recordRuntimeError(err)
		}
	}
	pull, err := client.PullRevision(ctx)
	if err != nil {
		return c.recordRuntimeError(err)
	}
	if !pull.HasUpdate {
		return nil
	}
	lease, snapshot, digest, err := validateRevisionPull(pull)
	if err != nil {
		return c.recordRuntimeError(err)
	}
	journal, err := store.LoadGenerationJournal()
	if err != nil {
		return c.recordRuntimeError(err)
	}
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
	activeDigestMatches := journal.Active != nil && journal.Active.Revision == lease.Revision &&
		strings.EqualFold(journal.Active.SnapshotDigest, digest)
	if journal.Active != nil && sameGenerationLease(*journal.Active, lease) &&
		strings.EqualFold(journal.Active.SnapshotDigest, digest) {
		if journal.Active.Acknowledged {
			return nil
		}
		if err := reportRevisionApplied(ctx, client, lease, journal.Active.GenerationID); err != nil {
			return c.recordRuntimeError(err)
		}
		journal.Active.Acknowledged = true
		journal.Active.UpdatedAt = time.Now().UTC()
		return store.SaveGenerationJournal(journal)
	}

	generationID := revisionGenerationID(lease)
	candidate := model.GenerationRecord{
		GenerationID: generationID, Revision: lease.Revision, SnapshotDigest: digest,
		Phase: model.GenerationPhasePrepared, Lease: lease, UpdatedAt: time.Now().UTC(),
	}
	resumePhase := ""
	if journal.Candidate != nil && sameGenerationLease(*journal.Candidate, lease) &&
		strings.EqualFold(journal.Candidate.SnapshotDigest, digest) {
		candidate = *journal.Candidate
		resumePhase = candidate.Phase
	} else {
		journal.Candidate = &candidate
		if err := store.SaveGenerationJournal(journal); err != nil {
			return c.recordRuntimeError(err)
		}
	}

	if resumePhase == model.GenerationPhaseCutover {
		applied, loadErr := c.Store.LoadAppliedSnapshot()
		if loadErr == nil {
			sameSnapshot, digestErr := sameRevisionSnapshot(applied, snapshot)
			if digestErr == nil && applied.Revision == lease.Revision && sameSnapshot {
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
		if err := client.StartRevision(ctx, model.RevisionStart{
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
			if err := c.persistRuntimeState(true); err != nil {
				return c.recordRuntimeError(err)
			}
			return c.finishRevisionAcknowledgement(ctx, client, store, journal, candidate, applied)
		}
	}

	existingDesired, err := c.Store.LoadDesiredSnapshot()
	if err != nil {
		return c.failRevisionAttempt(ctx, client, store, journal, candidate, err)
	}
	persistedSnapshot := MergeSnapshotPayload(snapshot, existingDesired)
	if err := c.Store.SaveDesiredSnapshot(persistedSnapshot); err != nil {
		return c.failRevisionAttempt(ctx, client, store, journal, candidate, err)
	}
	if err := c.handlePendingUpdate(ctx, persistedSnapshot); err != nil {
		return c.failRevisionAttempt(ctx, client, store, journal, candidate, err)
	}

	previousApplied = c.Runtime.ActiveSnapshot()
	candidateApplied := MergeSnapshotPayload(snapshot, previousApplied)
	if err := c.Runtime.Apply(ctx, previousApplied, candidateApplied); err != nil {
		rollbackErr := c.rollbackRuntime(ctx, candidateApplied, previousApplied)
		return c.failRevisionAttempt(ctx, client, store, journal, candidate, errors.Join(err, rollbackErr))
	}
	candidate.Phase = model.GenerationPhaseCutover
	candidate.UpdatedAt = time.Now().UTC()
	journal.Candidate = &candidate
	if err := store.SaveGenerationJournal(journal); err != nil {
		if isFilesystemCommitUncertain(err) {
			return c.recordRuntimeError(err)
		}
		rollbackErr := c.rollbackRuntime(ctx, candidateApplied, previousApplied)
		return c.failRevisionAttempt(ctx, client, store, journal, candidate, errors.Join(err, rollbackErr))
	}
	if err := c.Store.SaveAppliedSnapshot(candidateApplied); err != nil {
		if isFilesystemCommitUncertain(err) {
			return c.recordRuntimeError(err)
		}
		rollbackErr := c.rollbackRuntime(ctx, candidateApplied, previousApplied)
		return c.failRevisionAttempt(ctx, client, store, journal, candidate, errors.Join(err, rollbackErr))
	}
	if err := c.persistRuntimeState(true); err != nil {
		if isFilesystemCommitUncertain(err) {
			return c.recordRuntimeError(err)
		}
		rollbackErr := c.rollbackRuntime(ctx, candidateApplied, previousApplied)
		restoreErr := c.Store.SaveAppliedSnapshot(previousApplied)
		return c.failRevisionAttempt(ctx, client, store, journal, candidate, errors.Join(err, rollbackErr, restoreErr))
	}
	return c.finishRevisionAcknowledgement(ctx, client, store, journal, candidate, candidateApplied)
}

func (c *SyncController) finishRevisionAcknowledgement(
	ctx context.Context,
	client RevisionSyncClient,
	store GenerationJournalStore,
	journal model.GenerationJournal,
	candidate model.GenerationRecord,
	applied model.Snapshot,
) error {
	candidate.Phase = model.GenerationPhaseActive
	candidate.Acknowledged = false
	candidate.UpdatedAt = time.Now().UTC()
	journal.Active = &candidate
	journal.LastKnownGood = &candidate
	journal.Candidate = nil
	if err := store.SaveLastKnownGoodSnapshot(applied); err != nil {
		return c.recordRuntimeError(err)
	}
	if err := store.SaveGenerationJournal(journal); err != nil {
		return c.recordRuntimeError(err)
	}
	if err := reportRevisionApplied(ctx, client, candidate.Lease, candidate.GenerationID); err != nil {
		return c.recordRuntimeError(err)
	}
	journal.Active.Acknowledged = true
	journal.Active.UpdatedAt = time.Now().UTC()
	if err := store.SaveGenerationJournal(journal); err != nil {
		return c.recordRuntimeError(err)
	}
	return nil
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
	candidate.UpdatedAt = time.Now().UTC()
	journal.Candidate = &candidate
	journalErr := store.SaveGenerationJournal(journal)
	reportErr := client.ReportRevision(ctx, model.RevisionReport{
		AgentID: candidate.Lease.AgentID, Revision: candidate.Lease.Revision,
		RetryCycle: candidate.Lease.RetryCycle, Attempt: candidate.Lease.Attempt,
		LeaseID: candidate.Lease.LeaseID, GenerationID: candidate.GenerationID,
		Status: "failed", ErrorCode: "apply_failed", ErrorMessage: applyErr.Error(),
	})
	return c.recordRuntimeErrorWithRevision(errors.Join(applyErr, journalErr, reportErr), candidate.Revision)
}

func validateRevisionPull(pull model.RevisionPull) (model.RevisionLease, model.Snapshot, string, error) {
	if pull.Lease == nil || pull.Snapshot == nil {
		return model.RevisionLease{}, model.Snapshot{}, "", errors.New("revision pull returned an incomplete update")
	}
	lease := *pull.Lease
	snapshot := *pull.Snapshot
	if strings.TrimSpace(lease.AgentID) == "" || lease.Revision <= 0 || lease.Attempt <= 0 ||
		strings.TrimSpace(lease.LeaseID) == "" || snapshot.Revision != lease.Revision ||
		pull.DesiredRevision != lease.Revision {
		return model.RevisionLease{}, model.Snapshot{}, "", errors.New("revision pull identity is inconsistent")
	}
	if !lease.DeadlineAt.IsZero() && !time.Now().UTC().Before(lease.DeadlineAt) {
		return model.RevisionLease{}, model.Snapshot{}, "", errors.New("revision lease is expired")
	}
	digest := strings.TrimSpace(pull.VerifiedSnapshotDigest)
	if strings.TrimSpace(lease.SnapshotDigest) == "" || !strings.EqualFold(digest, lease.SnapshotDigest) {
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
	if journal.Candidate != nil && journal.Candidate.Phase == model.GenerationPhaseCutover {
		floor = max(floor, journal.Candidate.Revision)
	}
	return floor
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

func reportRevisionApplied(ctx context.Context, client RevisionSyncClient, lease model.RevisionLease, generationID string) error {
	return client.ReportRevision(ctx, model.RevisionReport{
		AgentID: lease.AgentID, Revision: lease.Revision, RetryCycle: lease.RetryCycle,
		Attempt: lease.Attempt, LeaseID: lease.LeaseID, GenerationID: generationID,
		Status: "applied",
	})
}
