package app

import (
	"errors"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/certs"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/store"
	agenttask "github.com/sakullla/nginx-reverse-emby/go-agent/internal/task"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/traffic"
)

func NewEmbedded(cfg Config, st store.Store, client SyncClient) (*App, error) {
	if st == nil {
		return nil, errors.New("store is required")
	}
	if client == nil {
		return nil, errors.New("sync client is required")
	}

	cfg = normalizeConstructorConfig(cfg)
	traffic.SetEnabled(cfg.TrafficStatsEnabled)

	resetRelayTimeouts := relay.ConfigureTimeouts(relay.TimeoutConfig{
		DialTimeout:      cfg.RelayTimeouts.DialTimeout,
		HandshakeTimeout: cfg.RelayTimeouts.HandshakeTimeout,
		FrameTimeout:     cfg.RelayTimeouts.FrameTimeout,
		IdleTimeout:      cfg.RelayTimeouts.IdleTimeout,
	})
	restoreRelayTimeouts := true
	defer func() {
		if restoreRelayTimeouts {
			resetRelayTimeouts()
		}
	}()

	certManager, err := certs.NewManager(
		cfg.DataDir,
		certs.WithNodeRole("master"),
		certs.WithLocalAgent(true),
	)
	if err != nil {
		return nil, err
	}

	httpManager := newHTTPRuntimeManagerWithTLSAndHTTP3AndConfig(certManager, cfg.HTTP3Enabled, cfg)
	l4Manager := newL4RuntimeManagerWithRelayAndConfig(certManager, cfg)
	httpProber, tcpProber := newRuntimeDiagnosticProbers(certManager, httpManager, l4Manager)
	diagnosticHandler := agenttask.NewDiagnosticHandler(st, httpProber, tcpProber)
	app := newAppWithAllDeps(
		cfg,
		st,
		client,
		httpManager,
		certManager,
		l4Manager,
		newRelayRuntimeManager(certManager),
		nil,
		nil,
	)
	app.setDiagnostics(diagnosticHandler, httpProber, tcpProber)
	app.relayTimeoutReset = resetRelayTimeouts
	restoreRelayTimeouts = false
	return app, nil
}
