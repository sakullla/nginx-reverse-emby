package app

import (
	"errors"

	modulecerts "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/certs"
	modulediagnostics "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/diagnostics"
	moduleegress "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/egress"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay"
	moduletraffic "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/traffic"
	agentruntime "github.com/sakullla/nginx-reverse-emby/go-agent/internal/runtime"
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

	certManager, err := modulecerts.NewManager(
		cfg.DataDir,
		modulecerts.WithNodeRole("master"),
		modulecerts.WithLocalAgent(true),
	)
	if err != nil {
		return nil, err
	}

	wireGuardRuntime := newSharedWireGuardRuntime()
	httpModule := newHTTPModuleFromConfig(cfg)
	l4Module := newL4ModuleFromConfig(cfg)
	certModule := modulecerts.NewModule(certManager)
	diagnosticModule := modulediagnostics.NewModule()
	egressModule := moduleegress.NewModule(nil)
	relayModule := relay.NewModule(relay.Config{AgentID: cfg.AgentID, AgentName: cfg.AgentName})
	trafficModule := moduletraffic.NewModule(moduletraffic.Config{
		Interfaces: cfg.TrafficInterfaces,
		Enabled:    cfg.TrafficStatsEnabled,
		EnabledSet: true,
	})
	moduleRegistry, err := newAppModuleRegistry(cfg, certModule, diagnosticModule, egressModule, httpModule, l4Module, relayModule, trafficModule, wireGuardRuntime)
	if err != nil {
		_ = wireGuardRuntime.Close()
		return nil, err
	}
	app := newAppWithAllDeps(
		cfg,
		st,
		client,
		nil,
		certManager,
		nil,
		nil,
		nil,
		nil,
	)
	app.certModule = certModule
	app.setDiagnosticModule(diagnosticModule)
	app.moduleRegistry = moduleRegistry
	app.runtime = agentruntime.NewWithActivator(app.snapshotActivator())
	app.egressModule = egressModule
	app.httpModule = httpModule
	app.l4Module = l4Module
	app.relayModule = relayModule
	app.trafficModule = trafficModule
	app.relayApplier = relayModule
	app.wireGuardRuntime = wireGuardRuntime
	app.relayTimeoutReset = resetRelayTimeouts
	restoreRelayTimeouts = false
	return app, nil
}
