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

var ErrLocalAgentExitedUnexpectedly = errors.New("embedded local agent exited unexpectedly")

type App struct {
	server              *http.Server
	enableLocalAgent    bool
	startLocalAgent     LocalAgentStarter
	startPKIMaintenance LocalAgentStarter
	logger              *log.Logger
	cleanup             func() error
}

func New(cfg config.Config, handler http.Handler, logger *log.Logger, startLocalAgent LocalAgentStarter) *App {
	if logger == nil {
		logger = log.Default()
	}
	return &App{
		enableLocalAgent: cfg.EnableLocalAgent,
		startLocalAgent:  startLocalAgent,
		logger:           logger,
		server: &http.Server{
			Addr:     cfg.ListenAddr,
			Handler:  handler,
			ErrorLog: logger,
		},
	}
}

func (a *App) Run(ctx context.Context) (runErr error) {
	defer func() {
		if a.cleanup != nil {
			runErr = errors.Join(runErr, a.cleanup())
		}
	}()

	if ctx.Err() != nil {
		if a.enableLocalAgent && a.startLocalAgent != nil {
			err := a.startLocalAgent(ctx)
			if err == nil || errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		}
		return nil
	}

	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()

	pkiMaintenanceErrCh := make(chan error, 1)
	if a.startPKIMaintenance != nil {
		go func() {
			pkiMaintenanceErrCh <- a.startPKIMaintenance(runCtx)
		}()
		defer func() {
			cancelRun()
			select {
			case err := <-pkiMaintenanceErrCh:
				if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
					a.logger.Printf("[pki] lease maintenance stopped: %v", err)
				}
			case <-time.After(5 * time.Second):
				a.logger.Printf("[pki] lease maintenance did not stop before cleanup")
			}
		}()
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

	localEnabled := a.enableLocalAgent && a.startLocalAgent != nil
	localAgentErrCh := make(chan error, 1)
	if localEnabled {
		go func() {
			localAgentErrCh <- a.startLocalAgent(runCtx)
		}()
	}

	select {
	case <-ctx.Done():
		cancelRun()
		serverErr := a.shutdownServerAndWait(serverErrCh)
		localErr := waitLocalAgent(localAgentErrCh, localEnabled)
		if serverErr != nil {
			return serverErr
		}
		if localErr == nil || errors.Is(localErr, context.Canceled) {
			return nil
		}
		return localErr
	case err := <-serverErrCh:
		cancelRun()
		localErr := waitLocalAgent(localAgentErrCh, localEnabled)
		if err != nil {
			return err
		}
		if ctx.Err() == nil {
			if localErr != nil && !errors.Is(localErr, context.Canceled) {
				return localErr
			}
			return errors.New("http server exited unexpectedly")
		}
		if localErr == nil || errors.Is(localErr, context.Canceled) {
			return nil
		}
		return localErr
	case err := <-localAgentErrCh:
		cancelRun()
		serverErr := a.shutdownServerAndWait(serverErrCh)
		if serverErr != nil {
			return serverErr
		}
		if err == nil {
			return ErrLocalAgentExitedUnexpectedly
		}
		if errors.Is(err, context.Canceled) && ctx.Err() != nil {
			return nil
		}
		return err
	}
}

func (a *App) SetCleanup(cleanup func() error) {
	a.cleanup = cleanup
}

// SetPKIMaintainer attaches PKI lease maintenance to the existing application
// lifetime. A lease failure is fail-closed for signing but never replaces or
// shuts down the control HTTP listener.
func (a *App) SetPKIMaintainer(maintainer LocalAgentStarter) {
	a.startPKIMaintenance = maintainer
}

func waitLocalAgent(localAgentErrCh <-chan error, enabled bool) error {
	if !enabled {
		return nil
	}
	return <-localAgentErrCh
}

func (a *App) shutdownServerAndWait(serverErrCh <-chan error) error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return <-serverErrCh
}
