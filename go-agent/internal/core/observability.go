package core

import (
	"context"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/observability"
)

func (m *GenerationManager) Apply(ctx context.Context, previous, next model.Snapshot) (GenerationCutover, error) {
	return m.applyWithDrainTimeout(ctx, previous, next, 0)
}

func (m *GenerationManager) ApplyWithDrainTimeout(ctx context.Context, previous, next model.Snapshot, drainTimeout time.Duration) (GenerationCutover, error) {
	return m.applyWithDrainTimeout(ctx, previous, next, drainTimeout)
}

func (m *GenerationManager) applyWithDrainTimeout(ctx context.Context, previous, next model.Snapshot, drainTimeout time.Duration) (GenerationCutover, error) {
	return m.applyWithTrafficRuntime(ctx, previous, next, drainTimeout, nil)
}

func (m *GenerationManager) ApplyWithTrafficRuntime(ctx context.Context, previous, next model.Snapshot, drainTimeout time.Duration, config model.AgentConfig) (GenerationCutover, error) {
	return m.applyWithTrafficRuntime(ctx, previous, next, drainTimeout, &config)
}

func (m *GenerationManager) applyWithTrafficRuntime(ctx context.Context, previous, next model.Snapshot, drainTimeout time.Duration, config *model.AgentConfig) (GenerationCutover, error) {
	started := time.Now()
	before := m.ActiveIdentity()
	cutover, err := m.apply(ctx, previous, next, drainTimeout, config)
	outcome := "succeeded"
	if err != nil {
		outcome = "failed"
	} else if cutover.Active != nil && before.ID != "" && cutover.Active.ID() == before.ID {
		outcome = "noop"
	}
	generationID := ""
	if cutover.Active != nil {
		generationID = cutover.Active.ID()
	}
	observability.Observe(ctx, observability.Event{
		Name: observability.GenerationCutover, Outcome: outcome, Revision: next.Revision,
		GenerationID: generationID, Duration: time.Since(started),
	})
	return cutover, err
}
