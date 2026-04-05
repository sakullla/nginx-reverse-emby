package app

import "github.com/sakullla/nginx-reverse-emby/go-agent/internal/config"

type Config = config.Config

type App struct {
	cfg Config
}

func New(cfg Config) (*App, error) {
	return &App{cfg: cfg}, nil
}
