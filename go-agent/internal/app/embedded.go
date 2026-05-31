package app

import (
	"errors"

	modulecerts "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/certs"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/store"
)

func NewEmbedded(cfg Config, st store.Store, client SyncClient) (*App, error) {
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
