package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/app"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/hotrestart"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func main() {
	cfg, err := model.LoadFromEnv()
	if err != nil {
		log.Fatal(err)
	}

	child, isChild, err := hotrestart.OpenChildSessionFromEnvironment()
	if err != nil {
		log.Fatal(err)
	}
	if !isChild {
		startPprofServer()
	}

	runtimeApp, err := app.New(cfg)
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if isChild {
		defer child.Close()
		if err := runtimeApp.RunHotRestartChild(ctx, child); err != nil {
			log.Fatal(err)
		}
		return
	}

	if err := runtimeApp.Run(ctx); err != nil {
		log.Fatal(err)
	}
}
