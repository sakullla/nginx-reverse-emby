package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
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
	identity, drainTimeout, err := a.hotRestartLaunchState()
	if err != nil {
		return err
	}
	if drainTimeout > 0 {
		a.hotRestartDrainTimeout = drainTimeout
	}
	ctx := a.hotRestartContext()
	process, err := a.hotRestartStart(ctx, hotrestart.Launch{
		Binary: binary, Argv: argv, Env: env, Identity: identity,
		AuthorityJournal: filepath.Join(a.cfg.DataDir, "hot-restart", "authority.json"),
		Stdout:           os.Stdout, Stderr: os.Stderr,
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
	if a.hotRestartChild {
		return core.ErrRestartRequested
	}

	// The service manager tracks this original process. Keep it as the sole
	// stable supervisor while every authoritative hot-restart child remains a
	// replaceable worker. Intermediate children exit after their own handoff,
	// preventing parent/child/grandchild chains from accumulating.
	a.stopTaskClient()
	a.closeLocalRuntimes()
	journalPath := filepath.Join(a.cfg.DataDir, "hot-restart", "authority.json")
	supervise := a.hotRestartSupervise
	if supervise == nil {
		supervise = a.superviseHotRestartLineage
	}
	if superviseErr := supervise(ctx, process, journalPath, identity); superviseErr != nil && !errors.Is(superviseErr, context.Canceled) {
		log.Printf("[agent] hot restart authority lineage ended: %v", superviseErr)
	}
	return core.ErrRestartRequested
}

func (a *App) hotRestartLaunchIdentity() (hotrestart.Identity, error) {
	identity, _, err := a.hotRestartLaunchState()
	return identity, err
}

func (a *App) hotRestartLaunchState() (hotrestart.Identity, time.Duration, error) {
	desired, err := a.store.LoadDesiredSnapshot()
	if err != nil {
		return hotrestart.Identity{}, 0, err
	}
	store, ok := a.store.(hotRestartJournalStore)
	if !ok {
		return hotrestart.Identity{}, 0, errors.New("store does not expose the generation journal required for hot restart")
	}
	journal, err := store.LoadGenerationJournal()
	if err != nil {
		return hotrestart.Identity{}, 0, err
	}
	runtimeDigest, err := hotRestartSnapshotDigest(desired)
	if err != nil {
		return hotrestart.Identity{}, 0, err
	}
	record := matchingHotRestartRecord(journal, desired.Revision)
	if record == nil {
		if desired.Revision == 0 {
			return a.bootstrapHotRestartLaunchState(runtimeDigest)
		}
		return hotrestart.Identity{}, 0, errors.New("durable generation is not ready for hot restart")
	}
	if strings.TrimSpace(record.SnapshotDigest) == "" || strings.TrimSpace(record.RuntimeSnapshotHash) == "" ||
		!strings.EqualFold(strings.TrimSpace(record.RuntimeSnapshotHash), runtimeDigest) {
		return hotrestart.Identity{}, 0, errors.New("durable generation does not match the desired runtime snapshot")
	}
	generationID := strings.TrimSpace(record.RuntimeGenerationID)
	if generationID == "" {
		return hotrestart.Identity{}, 0, errors.New("durable generation identity is required for hot restart")
	}
	drainTimeout := time.Duration(record.Lease.DrainTimeoutSeconds) * time.Second
	return hotrestart.Identity{
		Revision: record.Revision, SnapshotDigest: record.SnapshotDigest,
		GenerationID: generationID, LeaseID: record.Lease.LeaseID,
	}, drainTimeout, nil
}

func (a *App) bootstrapHotRestartLaunchState(runtimeDigest string) (hotrestart.Identity, time.Duration, error) {
	if a == nil || a.runtime == nil {
		return hotrestart.Identity{}, 0, errors.New("bootstrap hot restart runtime is required")
	}
	active, managed := a.runtime.ActiveGenerationIdentity()
	if !managed || active.ID == "" || active.Revision != 0 ||
		!strings.EqualFold(strings.TrimSpace(active.SnapshotHash), strings.TrimSpace(runtimeDigest)) {
		return hotrestart.Identity{}, 0, errors.New("bootstrap runtime generation does not match the durable desired snapshot")
	}
	return hotrestart.Identity{
		Revision:       0,
		SnapshotDigest: active.SnapshotHash,
		GenerationID:   active.ID,
		LeaseID:        bootstrapHotRestartLeaseID(active.SnapshotHash),
	}, hotRestartDrainTimeout, nil
}

func bootstrapHotRestartLeaseID(snapshotDigest string) string {
	digest := strings.ToLower(strings.TrimSpace(snapshotDigest))
	if len(digest) > 16 {
		digest = digest[:16]
	}
	return "bootstrap-" + digest
}

func matchingHotRestartRecord(journal model.GenerationJournal, revision int64) *model.GenerationRecord {
	for _, candidate := range []*model.GenerationRecord{journal.Candidate, journal.Active} {
		if candidate == nil || candidate.Revision != revision || strings.TrimSpace(candidate.Lease.LeaseID) == "" {
			continue
		}
		if candidate == journal.Candidate && candidate.Phase == model.GenerationPhaseStarted {
			return candidate
		}
		if candidate == journal.Active && candidate.Phase == model.GenerationPhaseActive {
			return candidate
		}
	}
	return nil
}

func hotRestartSnapshotDigest(snapshot Snapshot) (string, error) {
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
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
