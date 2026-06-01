package app

import (
	"errors"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/core"
	modulecerts "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/certs"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay"
)

func NewEmbedded(cfg Config, st core.Store, client SyncClient) (*App, error) {
	if st == nil {
		return nil, errors.New("store is required")
	}
	if client == nil {
		return nil, errors.New("sync client is required")
	}

	cfg = normalizeConstructorConfig(cfg)

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

	modules, err := newConfiguredModules(
		cfg,
		modulecerts.WithNodeRole("master"),
		modulecerts.WithLocalAgent(true),
	)
	if err != nil {
		return nil, err
	}

	app := newAppWithAllDeps(
		cfg,
		st,
		client,
		nil,
		nil,
	)
	app.setConfiguredModules(modules)
	app.relayTimeoutReset = resetRelayTimeouts
	restoreRelayTimeouts = false
	return app, nil
}
