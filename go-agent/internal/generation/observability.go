package generation

import (
	"context"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/observability"
)

func (c *DrainController) Activate(ctx context.Context, next Generation, changes []EntityChange, timeout time.Duration) error {
	started := time.Now()
	err := c.activate(ctx, next, changes, timeout)
	outcome := "drained"
	if err != nil {
		outcome = "failed"
	}
	observability.Observe(ctx, observability.Event{
		Name: observability.GenerationDrain, Outcome: outcome, Revision: next.Revision,
		GenerationID: next.ID, Duration: time.Since(started),
	})
	return err
}

func (c *DrainController) force(ctx context.Context, id, reason string) error {
	started := time.Now()
	err := c.forceGeneration(ctx, id, reason)
	outcome := "forced"
	if err != nil {
		outcome = "failed"
	}
	revision := int64(0)
	for _, status := range c.Snapshot().Generations {
		if status.GenerationID == id {
			revision = status.Revision
			break
		}
	}
	observability.Observe(ctx, observability.Event{
		Name: observability.GenerationDrain, Outcome: outcome, Revision: revision,
		GenerationID: id, Duration: time.Since(started),
	})
	return err
}
