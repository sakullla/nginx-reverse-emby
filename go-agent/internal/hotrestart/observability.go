package hotrestart

import (
	"context"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/observability"
)

func (s Supervisor) Start(ctx context.Context, launch Launch) (*ChildProcess, error) {
	started := time.Now()
	process, err := s.start(ctx, launch)
	outcome := "started"
	if err != nil {
		outcome = "failed"
	} else {
		process.observabilityCtx = ctx
	}
	observability.Observe(ctx, observability.Event{
		Name: observability.HotRestartUpgrade, Outcome: outcome,
		Revision: launch.Identity.Revision, GenerationID: launch.Identity.GenerationID,
		Duration: time.Since(started),
	})
	return process, err
}

func (p *ChildProcess) TransferAuthority(ctx context.Context) error {
	started := time.Now()
	err := p.transferAuthority(ctx)
	outcome := "succeeded"
	if err != nil {
		outcome = "failed"
	}
	if p != nil {
		observability.Observe(ctx, observability.Event{
			Name: observability.HotRestartUpgrade, Outcome: outcome,
			Revision: p.identity.Revision, GenerationID: p.identity.GenerationID,
			Duration: time.Since(started),
		})
	}
	return err
}

func (p *ChildProcess) Abort() error {
	started := time.Now()
	err := p.abort()
	if p != nil {
		ctx := p.observabilityCtx
		if ctx == nil {
			ctx = context.Background()
		}
		observability.Observe(ctx, observability.Event{
			Name: observability.HotRestartUpgrade, Outcome: "failed",
			Revision: p.identity.Revision, GenerationID: p.identity.GenerationID,
			Duration: time.Since(started),
		})
	}
	return err
}
