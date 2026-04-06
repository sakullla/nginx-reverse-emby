package app

import (
	"context"
	"runtime"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/config"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/store"
	agentsync "github.com/sakullla/nginx-reverse-emby/go-agent/internal/sync"
)

type Config = config.Config
type Snapshot = store.Snapshot

type SyncClient interface {
	Sync(context.Context, Snapshot) (Snapshot, error)
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

	if snapshot, err := a.syncOnce(ctx, applied); err != nil {
		if applied.DesiredVersion == "" {
			return err
		}
	} else {
		applied = snapshot
	}

	ticker := time.NewTicker(a.cfg.HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if snapshot, err := a.syncOnce(ctx, applied); err == nil {
				applied = snapshot
			}
		}
	}
}

func (a *App) syncOnce(ctx context.Context, applied Snapshot) (Snapshot, error) {
	snapshot, err := a.syncClient.Sync(ctx, applied)
	if err != nil {
		return Snapshot{}, err
	}
	if err := a.store.SaveDesiredSnapshot(snapshot); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}
