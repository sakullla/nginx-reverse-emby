package app

import (
	"errors"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/hosttraffic"
	modulecerts "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/certs"
	modulediagnostics "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/diagnostics"
	moduleegress "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/egress"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay"
	agentruntime "github.com/sakullla/nginx-reverse-emby/go-agent/internal/runtime"
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
	l4Manager := newL4RuntimeManagerWithRelayConfigAndWireGuard(certManager, cfg, wireGuardRuntime)
	httpProber, tcpProber := newRuntimeDiagnosticProbers(certManager, httpModule, l4Manager)
	diagnosticHandler := agenttask.NewDiagnosticHandler(st, httpProber, tcpProber)
	certModule := modulecerts.NewModule(certManager)
	diagnosticModule := modulediagnostics.NewModule(diagnosticHandler, httpProber, tcpProber)
	egressModule := moduleegress.NewModule(nil)
	relayModule := relay.NewModule(relay.Config{AgentID: cfg.AgentID, AgentName: cfg.AgentName})
	moduleRegistry, err := newAppModuleRegistry(cfg, certModule, diagnosticModule, egressModule, httpModule, relayModule, wireGuardRuntime)
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
		l4Manager,
		nil,
		nil,
		nil,
	)
	app.certModule = certModule
	app.setDiagnosticModule(diagnosticModule)
	app.hostTrafficCollector = hosttraffic.NewCollector(cfg.TrafficInterfaces)
	app.moduleRegistry = moduleRegistry
	app.runtime = agentruntime.NewWithActivator(app.snapshotActivator())
	app.egressModule = egressModule
	app.httpModule = httpModule
	app.relayModule = relayModule
	app.relayApplier = relayModule
	app.wireGuardRuntime = wireGuardRuntime
	app.relayTimeoutReset = resetRelayTimeouts
	restoreRelayTimeouts = false
	return app, nil
}
