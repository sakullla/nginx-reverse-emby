package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/core"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/hotrestart"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

const hotRestartDrainTimeout = 10 * time.Minute

func (a *App) hotRestartReplacement(binary string, argv, env []string) error {
	if a == nil || a.store == nil {
		return errors.New("hot restart app store is required")
	}
	if a.hotRestartStart == nil {
		return errors.New("hot restart supervisor is required")
	}
	identity, err := a.hotRestartLaunchIdentity()
	if err != nil {
		return err
	}
	ctx := a.hotRestartContext()
	process, err := a.hotRestartStart(ctx, hotrestart.Launch{
		Binary: binary, Argv: argv, Env: env, Identity: identity,
		AuthorityJournal: filepath.Join(a.cfg.DataDir, "hot-restart", "authority.json"),
	})
	if err != nil {
		return fmt.Errorf("start hot restart child: %w", err)
	}
	abort := func(stageErr error) error {
		return errors.Join(stageErr, process.Abort())
	}
	if err := process.Activate(ctx); err != nil {
		return abort(fmt.Errorf("activate hot restart child: %w", err))
	}
	if a.hotRestartDrain != nil {
		if err := a.hotRestartDrain(ctx, identity); err != nil {
			return abort(fmt.Errorf("drain hot restart parent: %w", err))
		}
	}
	if err := process.TransferAuthority(ctx); err != nil {
		return abort(fmt.Errorf("transfer hot restart authority: %w", err))
	}
	return core.ErrRestartRequested
}

func (a *App) hotRestartLaunchIdentity() (hotrestart.Identity, error) {
	desired, err := a.store.LoadDesiredSnapshot()
	if err != nil {
		return hotrestart.Identity{}, err
	}
	store, ok := a.store.(hotRestartJournalStore)
	if !ok {
		return hotrestart.Identity{}, errors.New("store does not expose the generation journal required for hot restart")
	}
	journal, err := store.LoadGenerationJournal()
	if err != nil {
		return hotrestart.Identity{}, err
	}
	candidate := journal.Candidate
	if candidate == nil || candidate.Phase != model.GenerationPhaseStarted || candidate.Revision != desired.Revision ||
		strings.TrimSpace(candidate.SnapshotDigest) == "" || strings.TrimSpace(candidate.Lease.LeaseID) == "" {
		return hotrestart.Identity{}, errors.New("durable candidate is not ready for hot restart")
	}
	generationID := strings.TrimSpace(candidate.RuntimeGenerationID)
	if generationID == "" {
		generationID = strings.TrimSpace(candidate.GenerationID)
	}
	if generationID == "" {
		return hotrestart.Identity{}, errors.New("durable candidate generation identity is required for hot restart")
	}
	return hotrestart.Identity{
		Revision: candidate.Revision, SnapshotDigest: candidate.SnapshotDigest,
		GenerationID: generationID, LeaseID: candidate.Lease.LeaseID,
	}, nil
}

func (a *App) setRunContext(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	a.runCtxMu.Lock()
	a.runCtx = ctx
	a.runCtxMu.Unlock()
}

func (a *App) hotRestartContext() context.Context {
	a.runCtxMu.RLock()
	ctx := a.runCtx
	a.runCtxMu.RUnlock()
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func (a *App) drainHotRestartParent(ctx context.Context, target hotrestart.Identity) error {
	if a.generations == nil || a.generations.DrainController() == nil {
		return nil
	}
	active := a.generations.ActiveIdentity()
	if active.ID == "" || active.ID == target.GenerationID {
		return nil
	}
	controller := a.generations.DrainController()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(hotRestartDrainTimeout)
	defer timeout.Stop()
	for {
		found := false
		for _, status := range controller.Snapshot().Generations {
			if status.GenerationID != active.ID {
				continue
			}
			found = true
			if status.SessionCount == 0 {
				return nil
			}
		}
		if !found {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeout.C:
			return fmt.Errorf("generation %s did not drain within %s", active.ID, hotRestartDrainTimeout)
		case <-ticker.C:
		}
	}
}
