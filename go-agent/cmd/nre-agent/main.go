package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/app"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/config"
)

func main() {
	runtimeApp, err := app.New(config.Default())
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := runtimeApp.Run(ctx); err != nil {
		log.Fatal(err)
	}
}
