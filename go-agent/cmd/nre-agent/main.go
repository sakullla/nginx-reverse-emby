package main

import (
	"context"
	"log"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"syscall"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/app"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/config"
)

func main() {
	cfg, err := config.LoadFromEnv()
	if err != nil {
		log.Fatal(err)
	}

	startPprofServer()

	runtimeApp, err := app.New(cfg)
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := runtimeApp.Run(ctx); err != nil {
		log.Fatal(err)
	}
}

func startPprofServer() {
	addr := os.Getenv("NRE_PPROF_ADDR")
	if addr == "" {
		return
	}

	go func() {
		log.Printf("[agent] pprof listening on %s", addr)
		if err := http.ListenAndServe(addr, nil); err != nil {
			log.Printf("[agent] pprof server stopped: %v", err)
		}
	}()
}
