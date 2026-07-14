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
	runtimeIdentity, managedGeneration, err := c.Runtime.CandidateGenerationIdentity(previousApplied, snapshot)
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
	if resumePhase == model.GenerationPhaseFailed {
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
			if identityErr := c.validateActiveRuntimeGeneration(candidate); identityErr != nil {
				return c.recordRuntimeError(identityErr)
			}
			if err := c.persistRuntimeState(true); err != nil {
				return c.recordRuntimeError(err)
			}
			return c.finishRevisionAcknowledgement(ctx, client, store, journal, candidate, applied)
		}
	}

	if err := c.Store.SaveDesiredSnapshot(snapshot); err != nil {
		if durableCutover {
			return c.recordRuntimeError(err)
		}
		return c.failRevisionAttempt(ctx, client, store, journal, candidate, err)
	}
	if err := c.handlePendingUpdate(ctx, snapshot); err != nil {
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
	if err := c.Runtime.Apply(ctx, previousApplied, candidateApplied); err != nil {
		if durableCutover {
			return c.recordRuntimeErrorWithRevision(err, candidate.Revision)
		}
		if c.Runtime.UsesGenerationManager() {
			return c.failRevisionAttempt(ctx, client, store, journal, candidate, err)
		}
		rollbackErr := c.rollbackRuntime(ctx, candidateApplied, previousApplied)
		if rollbackErr != nil {
			return c.recordRuntimeErrorWithRevision(errors.Join(err, rollbackErr), candidate.Revision)
		}
		return c.failRevisionAttempt(ctx, client, store, journal, candidate, err)
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
	if journalErr != nil {
		return c.recordRuntimeErrorWithRevision(errors.Join(applyErr, journalErr), candidate.Revision)
	}
	reportErr := client.ReportRevision(ctx, model.RevisionReport{
		AgentID: candidate.Lease.AgentID, Revision: candidate.Lease.Revision,
		RetryCycle: candidate.Lease.RetryCycle, Attempt: candidate.Lease.Attempt,
		LeaseID: candidate.Lease.LeaseID, GenerationID: candidate.GenerationID,
		Status: "failed", ErrorCode: "apply_failed", ErrorMessage: applyErr.Error(),
	})
	return c.recordRuntimeErrorWithRevision(errors.Join(applyErr, reportErr), candidate.Revision)
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
	if journal.Candidate != nil {
		floor = max(floor, journal.Candidate.Revision)
	}
	return floor
}

func validateImmutableRevisionDigest(journal model.GenerationJournal, revision int64, digest string) error {
	for _, record := range []*model.GenerationRecord{journal.Active, journal.LastKnownGood, journal.Candidate} {
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
		if journal.Active != nil || journal.Candidate != nil || journal.LastKnownGood != nil {
			return errors.New("unversioned generation journal contains records")
		}
		return nil
	}
	if journal.Version != 1 {
		return fmt.Errorf("unsupported generation journal version %d", journal.Version)
	}
	for _, entry := range []struct {
		name   string
		record *model.GenerationRecord
	}{
		{name: "active", record: journal.Active},
		{name: "last-known-good", record: journal.LastKnownGood},
		{name: "candidate", record: journal.Candidate},
	} {
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
	if err := c.Runtime.Apply(ctx, model.Snapshot{}, applied); err != nil {
		return fmt.Errorf("restore durable applied snapshot: %w", err)
	}
	return c.persistRuntimeState(false)
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
