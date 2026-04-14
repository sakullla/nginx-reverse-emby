package app

import (
	"errors"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/certs"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/config"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/store"
)

func NewEmbedded(cfg Config, st store.Store, client SyncClient) (*App, error) {
	if st == nil {
		return nil, errors.New("store is required")
	}
	if client == nil {
		return nil, errors.New("sync client is required")
	}

	defaults := config.Default()
	if cfg.AgentID == "" {
		cfg.AgentID = defaults.AgentID
	}
	if cfg.AgentName == "" {
		cfg.AgentName = defaults.AgentName
	}
	if cfg.DataDir == "" {
		cfg.DataDir = defaults.DataDir
	}
	if cfg.CurrentVersion == "" {
		cfg.CurrentVersion = defaults.CurrentVersion
	}

	certManager, err := certs.NewManager(
		cfg.DataDir,
		certs.WithNodeRole("master"),
		certs.WithLocalAgent(true),
	)
	if err != nil {
		return nil, err
	}

	return newAppWithAllDeps(
		cfg,
		st,
		client,
		newHTTPRuntimeManagerWithTLSAndHTTP3(certManager, cfg.HTTP3Enabled),
		certManager,
		newL4RuntimeManagerWithRelay(certManager),
		newRelayRuntimeManager(certManager),
		nil,
		nil,
	), nil
}
