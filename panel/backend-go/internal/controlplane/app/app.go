package app

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
)

type App struct {
	server *http.Server
}

func New(cfg config.Config, handler http.Handler, logger *log.Logger) *App {
	if logger == nil {
		logger = log.Default()
	}
	return &App{
		server: &http.Server{
			Addr:     cfg.ListenAddr,
			Handler:  handler,
			ErrorLog: logger,
		},
	}
}

func (a *App) Run(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		err := a.server.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := a.server.Shutdown(shutdownCtx); err != nil {
			return err
		}
		return <-errCh
	case err := <-errCh:
		return err
	}
}
