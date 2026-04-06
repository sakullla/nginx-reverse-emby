package app

import (
	"context"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/config"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/store"
	agentsync "github.com/sakullla/nginx-reverse-emby/go-agent/internal/sync"
)

type Config = config.Config

type App struct {
	cfg        Config
	syncClient *agentsync.Client
	store      store.Store
}

func New(cfg Config) (*App, error) {
	return &App{
		cfg:        cfg,
		syncClient: agentsync.NewClient(),
		store:      store.NewInMemory(),
	}, nil
}

func (a *App) Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}
