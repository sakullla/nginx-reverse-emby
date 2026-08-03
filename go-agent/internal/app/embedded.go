package app

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/core"
	modulecerts "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/certs"
	modulepki "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/pki"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay"
)

const embeddedAgentStateRootDir = "embedded-agent-state"

type embeddedTunnelPKIStoreSource interface {
	EmbeddedTunnelPKIStore() *modulepki.Store
}

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
	pkiStore := (*modulepki.Store)(nil)
	if source, ok := client.(embeddedTunnelPKIStoreSource); ok {
		pkiStore = source.EmbeddedTunnelPKIStore()
	}
	if pkiStore == nil {
		pkiStore, err = modulepki.NewStore(filepath.Join(cfg.DataDir, embeddedAgentStateRootDir))
		if err != nil {
			return nil, fmt.Errorf("open embedded tunnel PKI store: %w", err)
		}
	}

	app := newAppWithAllDeps(
		cfg,
		st,
		client,
		nil,
		nil,
	)
	app.setConfiguredModules(modules)
	app.pkiStore = pkiStore
	app.relayTimeoutReset = resetRelayTimeouts
	restoreRelayTimeouts = false
	return app, nil
}
