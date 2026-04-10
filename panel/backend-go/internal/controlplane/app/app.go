package app

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
)

type LocalAgentStarter func(context.Context) error

type App struct {
	server           *http.Server
	enableLocalAgent bool
	startLocalAgent  LocalAgentStarter
}

func New(cfg config.Config, handler http.Handler, logger *log.Logger, startLocalAgent LocalAgentStarter) *App {
	if logger == nil {
		logger = log.Default()
	}
	return &App{
		enableLocalAgent: cfg.EnableLocalAgent,
		startLocalAgent:  startLocalAgent,
		server: &http.Server{
			Addr:     cfg.ListenAddr,
			Handler:  handler,
			ErrorLog: logger,
		},
	}
}

func (a *App) Run(ctx context.Context) error {
	if ctx.Err() != nil {
		if a.enableLocalAgent && a.startLocalAgent != nil {
			return a.startLocalAgent(ctx)
		}
		return nil
	}

	serverErrCh := make(chan error, 1)
	go func() {
		err := a.server.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrCh <- err
			return
		}
		serverErrCh <- nil
	}()

	localAgentErrCh := make(chan error, 1)
	if a.enableLocalAgent && a.startLocalAgent != nil {
		go func() {
			if err := a.startLocalAgent(ctx); err != nil {
				localAgentErrCh <- err
			}
		}()
	}

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := a.server.Shutdown(shutdownCtx); err != nil {
			return err
		}
		return <-serverErrCh
	case err := <-serverErrCh:
		return err
	case err := <-localAgentErrCh:
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if shutdownErr := a.server.Shutdown(shutdownCtx); shutdownErr != nil {
			return shutdownErr
		}
		if serverErr := <-serverErrCh; serverErr != nil {
			return serverErr
		}
		return err
	}
}
