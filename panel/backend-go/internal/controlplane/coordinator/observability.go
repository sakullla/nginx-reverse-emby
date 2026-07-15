package coordinator

import (
	"context"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/observability"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func (c *Coordinator) Claim(ctx context.Context, agentID string) (ClaimResult, error) {
	started := time.Now()
	result, err := c.claim(ctx, agentID)
	outcome := "queued"
	revision := int64(0)
	if err != nil {
		outcome = "failed"
	} else if result.Lease == nil {
		outcome = "noop"
	} else {
		revision = result.Lease.Revision
	}
	observability.Observe(ctx, observability.Event{
		Name: observability.RevisionQueue, Outcome: outcome, AgentID: agentID,
		Revision: revision, Duration: time.Since(started),
	})
	return result, err
}

func (c *Coordinator) Start(ctx context.Context, request StartRequest) (StartResult, error) {
	started := time.Now()
	result, err := c.start(ctx, request)
	observeRevisionApply(ctx, request.Lease, result.Revision.OperationID, request.GenerationID, "started", err, started)
	return result, err
}

func (c *Coordinator) Fail(ctx context.Context, report FailureReport) (FailureResult, error) {
	started := time.Now()
	result, err := c.fail(ctx, report)
	observeRevisionApply(ctx, report.Lease, result.Revision.OperationID, report.GenerationID, "failed", err, started)
	return result, err
}

func (c *Coordinator) Applied(ctx context.Context, report AppliedReport) (AppliedResult, error) {
	started := time.Now()
	result, err := c.applied(ctx, report)
	observeRevisionApply(ctx, report.Lease, result.Revision.OperationID, report.GenerationID, "applied", err, started)
	return result, err
}

func (c *Coordinator) Drained(ctx context.Context, report DrainReport) (storage.AgentRevisionRow, error) {
	started := time.Now()
	result, err := c.drained(ctx, report)
	outcome := "drained"
	if report.Forced {
		outcome = "forced"
	}
	if err != nil {
		outcome = "failed"
	}
	observability.Observe(ctx, observability.Event{
		Name: observability.GenerationDrain, Outcome: outcome, AgentID: report.Lease.AgentID,
		OperationID: result.OperationID, Revision: report.Lease.Revision, GenerationID: report.GenerationID,
		Attempt: report.Lease.Attempt, Duration: time.Since(started),
	})
	return result, err
}

func observeRevisionApply(ctx context.Context, lease Lease, operationID, generationID, outcome string, err error, started time.Time) {
	if err != nil {
		outcome = "failed"
	}
	observability.Observe(ctx, observability.Event{
		Name: observability.RevisionApply, Outcome: outcome, AgentID: lease.AgentID,
		OperationID: operationID, Revision: lease.Revision, GenerationID: generationID, Attempt: lease.Attempt,
		Duration: time.Since(started),
	})
}
