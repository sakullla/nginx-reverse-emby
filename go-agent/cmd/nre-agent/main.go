package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/app"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/hotrestart"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/dockerproxy"
)

func main() {
	if filepath.Base(os.Args[0]) == "docker" && os.Getenv(dockerproxy.EndpointEnv) != "" {
		os.Exit(dockerproxy.RunCLI(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
	}
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
