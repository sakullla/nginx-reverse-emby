package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/app"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	httpapi "github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/http"
)

func main() {
	cfg, err := config.LoadFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	handler, err := httpapi.NewRouter(httpapi.Dependencies{Config: cfg})
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := app.New(cfg, handler, nil, nil).Run(ctx); err != nil {
		log.Fatal(err)
	}
}
