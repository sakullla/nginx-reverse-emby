package core

import (
	"context"
	"errors"
	"fmt"
	"log"
	"reflect"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/control"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

type revisionDrainTimeoutContextKey struct{}

// WithRevisionDrainTimeout carries an authoritative lease drain timeout through
// the embedded sync adapter without exposing revision snapshots to periodic
// heartbeat applies.
func WithRevisionDrainTimeout(ctx context.Context, timeout time.Duration) context.Context {
	if ctx == nil || timeout <= 0 {
		return ctx
	}
	return context.WithValue(ctx, revisionDrainTimeoutContextKey{}, timeout)
}

func revisionDrainTimeout(ctx context.Context) time.Duration {
	if ctx == nil {
		return 0
	}
	timeout, _ := ctx.Value(revisionDrainTimeoutContextKey{}).(time.Duration)
	return timeout
}

func (c *SyncController) PerformSync(ctx context.Context, req control.SyncRequest) error {
	return c.PerformSyncPlan(ctx, SyncPlan{Request: req})
}

func (c *SyncController) PerformSyncPlan(ctx context.Context, plan SyncPlan) error {
	if revisionClient, ok := c.SyncClient.(RevisionSyncClient); ok {
		journalStore, ok := c.Store.(GenerationJournalStore)
		if !ok {
			return c.recordRuntimeError(errors.New("revision sync requires a durable generation journal store"))
		}
		return c.performRevisionSyncPlan(ctx, plan, revisionClient, journalStore)
	}
	return c.performLegacySyncPlan(ctx, plan)
}

func (c *SyncController) performLegacySyncPlan(ctx context.Context, plan SyncPlan) error {
	snapshot, err := c.SyncClient.Sync(ctx, plan.Request)
	if err != nil {
		log.Printf("[agent] sync error: %v", err)
		return c.recordSyncError(err)
	}
	if len(plan.RuntimeMetadata) > 0 {
		if err := c.persistRuntimeMetadata(plan.RuntimeMetadata); err != nil {
			return c.recordRuntimeError(err)
		}
	}
	existingDesired, err := c.Store.LoadDesiredSnapshot()
	if err != nil {
		return c.recordRuntimeError(err)
	}
	persistedSnapshot := MergeSnapshotPayload(snapshot, existingDesired)
	if err := c.Store.SaveDesiredSnapshot(persistedSnapshot); err != nil {
		return c.recordRuntimeError(err)
	}
	if err := c.handlePendingUpdate(ctx, persistedSnapshot); err != nil {
		if errors.Is(err, errPackageStagePending) {
			return c.clearLastSyncErrorAfterSuccessfulSync()
		}
		return err
	}

	previousApplied := c.Runtime.ActiveSnapshot()
	candidateApplied := MergeSnapshotPayload(snapshot, previousApplied)
	if err := c.Runtime.ApplyWithDrainTimeout(ctx, previousApplied, candidateApplied, revisionDrainTimeout(ctx)); err != nil {
		log.Printf("[agent] runtime apply error at revision %d: %v", candidateApplied.Revision, err)
		if c.Runtime.UsesGenerationManager() {
			return c.recordRuntimeErrorWithRevision(err, candidateApplied.Revision)
		}
		rollbackErr := c.rollbackRuntime(ctx, candidateApplied, previousApplied)
		return c.recordRuntimeErrorWithRevision(errors.Join(err, rollbackErr), candidateApplied.Revision)
	}
	if err := c.Store.SaveAppliedSnapshot(candidateApplied); err != nil {
		log.Printf("[agent] save applied snapshot error at revision %d: %v", candidateApplied.Revision, err)
		if c.Runtime.UsesGenerationManager() {
			return c.recordPersistedRuntimeErrorWithRevision(err, candidateApplied.Revision)
		}
		rollbackErr := c.rollbackRuntime(ctx, candidateApplied, previousApplied)
		return c.recordPersistedRuntimeErrorWithRevision(errors.Join(err, rollbackErr), candidateApplied.Revision)
	}
	if err := c.persistRuntimeState(true); err != nil {
		if c.Runtime.UsesGenerationManager() {
			return err
		}
		rollbackErr := c.rollbackRuntime(ctx, candidateApplied, previousApplied)
		restoreErr := c.Store.SaveAppliedSnapshot(previousApplied)
		return c.recordPersistedRuntimeErrorWithRevision(errors.Join(err, rollbackErr, restoreErr), candidateApplied.Revision)
	}
	return nil
}

func (c *SyncController) rollbackRuntime(ctx context.Context, previousApplied, targetApplied model.Snapshot) error {
	if reflect.DeepEqual(previousApplied, targetApplied) {
		return nil
	}
	var errs []error
	if err := c.Runtime.Rollback(ctx, previousApplied, targetApplied); err != nil {
		errs = append(errs, fmt.Errorf("runtime rollback: %w", err))
	}
	return errors.Join(errs...)
}
