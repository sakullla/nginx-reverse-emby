package generation

import (
	"context"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/observability"
)

func (c *DrainController) Activate(ctx context.Context, next Generation, changes []EntityChange, timeout time.Duration) error {
	err := c.activate(ctx, next, changes, timeout)
	if err != nil {
		observability.Observe(ctx, observability.Event{
			Name: observability.GenerationDrain, Outcome: "failed", Revision: next.Revision,
			GenerationID: next.ID,
		})
	}
	return err
}

func (c *DrainController) observeDrainCompletion(entry *drainEntry, err error) {
	if entry == nil {
		return
	}
	outcome := "drained"
	if err != nil {
		outcome = "failed"
	}
	duration := entry.status.CompletedAt.Sub(entry.status.DrainStartedAt)
	if duration < 0 {
		duration = 0
	}
	observability.Observe(entry.observabilityCtx, observability.Event{
		Name: observability.GenerationDrain, Outcome: outcome, Revision: entry.generation.Revision,
		GenerationID: entry.generation.ID, Duration: duration,
	})
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
	c.mu.Lock()
	entry := c.entries[id]
	c.mu.Unlock()
	c.clearObservabilityContext(entry)
	return err
}
