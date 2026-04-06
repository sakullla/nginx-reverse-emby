package app

import (
	"context"
	"runtime"
	"strconv"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/config"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/store"
	agentsync "github.com/sakullla/nginx-reverse-emby/go-agent/internal/sync"
)

type Config = config.Config
type Snapshot = store.Snapshot
type SyncRequest = agentsync.SyncRequest

type SyncClient interface {
	Sync(context.Context, SyncRequest) (Snapshot, error)
}

type App struct {
	cfg        Config
	syncClient SyncClient
	store      store.Store
}

func New(cfg Config) (*App, error) {
	st, err := store.NewFilesystem(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	client := agentsync.NewClient(agentsync.ClientConfig{
		MasterURL:      cfg.MasterURL,
		AgentToken:     cfg.AgentToken,
		AgentID:        cfg.AgentID,
		AgentName:      cfg.AgentName,
		CurrentVersion: cfg.CurrentVersion,
		Platform:       runtime.GOOS + "-" + runtime.GOARCH,
	}, nil)
	return newAppWithDeps(cfg, st, client), nil
}

func newAppWithDeps(cfg Config, st store.Store, client SyncClient) *App {
	if cfg.HeartbeatInterval <= 0 {
		cfg.HeartbeatInterval = config.Default().HeartbeatInterval
	}
	return &App{
		cfg:        cfg,
		store:      st,
		syncClient: client,
	}
}

func (a *App) Run(ctx context.Context) error {
	applied, err := a.store.LoadAppliedSnapshot()
	if err != nil {
		return err
	}
	runtimeState, err := a.store.LoadRuntimeState()
	if err != nil {
		return err
	}
	req := SyncRequest{CurrentRevision: currentRevisionFromMetadata(runtimeState.Metadata)}

	if err := a.syncOnce(ctx, req, &runtimeState); err != nil {
		if applied.DesiredVersion == "" {
			return err
		}
	}

	ticker := time.NewTicker(a.cfg.HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			a.syncOnce(ctx, req, &runtimeState)
		}
	}
}

func (a *App) syncOnce(ctx context.Context, req SyncRequest, runtimeState *store.RuntimeState) error {
	snapshot, err := a.syncClient.Sync(ctx, req)
	if err != nil {
		return a.recordRuntimeError(runtimeState, err)
	}
	if err := a.clearRuntimeError(runtimeState); err != nil {
		return err
	}
	if err := a.store.SaveDesiredSnapshot(snapshot); err != nil {
		return err
	}
	return nil
}

func (a *App) recordRuntimeError(state *store.RuntimeState, syncErr error) error {
	state.Metadata = ensureMetadata(state.Metadata)
	state.Metadata["last_sync_error"] = syncErr.Error()
	if err := a.store.SaveRuntimeState(*state); err != nil {
		return err
	}
	return syncErr
}

func (a *App) clearRuntimeError(state *store.RuntimeState) error {
	delete(state.Metadata, "last_sync_error")
	if err := a.store.SaveRuntimeState(*state); err != nil {
		return err
	}
	return nil
}

func ensureMetadata(meta map[string]string) map[string]string {
	if meta == nil {
		return make(map[string]string)
	}
	return meta
}

func currentRevisionFromMetadata(meta map[string]string) int {
	if meta == nil {
		return 0
	}
	if raw, ok := meta["current_revision"]; ok {
		if val, err := strconv.Atoi(raw); err == nil {
			return val
		}
	}
	return 0
}
