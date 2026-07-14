package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/core"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/hotrestart"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

const (
	hotRestartDrainTimeout          = 10 * time.Minute
	hotRestartSupervisorStopTimeout = 10 * time.Second
)

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
	return superviseHotRestartChild(ctx, process)
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
	desiredDigest, err := hotRestartSnapshotDigest(desired)
	if err != nil {
		return hotrestart.Identity{}, err
	}
	if candidate == nil || candidate.Phase != model.GenerationPhaseStarted || candidate.Revision != desired.Revision ||
		!strings.EqualFold(strings.TrimSpace(candidate.SnapshotDigest), desiredDigest) || strings.TrimSpace(candidate.Lease.LeaseID) == "" {
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

func hotRestartSnapshotDigest(snapshot Snapshot) (string, error) {
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func superviseHotRestartChild(ctx context.Context, process hotRestartProcess) error {
	waitResult := make(chan error, 1)
	go func() { waitResult <- process.Wait() }()
	select {
	case err := <-waitResult:
		return errors.Join(core.ErrRestartRequested, err)
	case <-ctx.Done():
	}
	signalErr := process.Signal(os.Interrupt)
	timer := time.NewTimer(hotRestartSupervisorStopTimeout)
	defer timer.Stop()
	select {
	case err := <-waitResult:
		return errors.Join(core.ErrRestartRequested, signalErr, err)
	case <-timer.C:
		abortErr := process.Abort()
		return errors.Join(core.ErrRestartRequested, signalErr, abortErr, <-waitResult)
	}
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
	if active.ID == "" {
		return nil
	}
	controller := a.generations.DrainController()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	drainTimeout := a.hotRestartDrainTimeout
	if drainTimeout <= 0 {
		drainTimeout = hotRestartDrainTimeout
	}
	timeout := time.NewTimer(drainTimeout)
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
			return fmt.Errorf("generation %s did not drain within %s", active.ID, drainTimeout)
		case <-ticker.C:
		}
	}
}
