package core

import (
	"context"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/observability"
)

func (m *GenerationManager) Apply(ctx context.Context, previous, next model.Snapshot) (GenerationCutover, error) {
	started := time.Now()
	before := m.ActiveIdentity()
	cutover, err := m.apply(ctx, previous, next)
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
