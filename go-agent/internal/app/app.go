package app

import (
	"context"
	"errors"
	"log"
	"os"
	"reflect"
	stdruntime "runtime"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/config"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/core"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/hosttraffic"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	agentmodule "github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	modulecerts "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/certs"
	modulediagnostics "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/diagnostics"
	moduleegress "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/egress"
	modulehttp "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/http"
	modulel4 "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/l4"
	modulerelay "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay"
	moduletraffic "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/traffic"
	modulewireguard "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/wireguard"
	platformlinux "github.com/sakullla/nginx-reverse-emby/go-agent/internal/platform/linux"
	agentruntime "github.com/sakullla/nginx-reverse-emby/go-agent/internal/runtime"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/store"
	agentsync "github.com/sakullla/nginx-reverse-emby/go-agent/internal/sync"
	agenttask "github.com/sakullla/nginx-reverse-emby/go-agent/internal/task"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/traffic"
	agentupdate "github.com/sakullla/nginx-reverse-emby/go-agent/internal/update"
)

type Config = config.Config
type Snapshot = store.Snapshot
type SyncRequest = agentsync.SyncRequest

type SyncClient interface {
	Sync(context.Context, SyncRequest) (Snapshot, error)
}

type CertificateApplier interface {
	Apply(context.Context, []model.ManagedCertificateBundle, []model.ManagedCertificatePolicy) error
}

type ManagedCertificateReporter interface {
	ManagedCertificateReports(context.Context) ([]model.ManagedCertificateReport, error)
}

type HTTPApplier interface {
	Apply(context.Context, []model.HTTPRule) error
	Close() error
}

type certCloser interface {
	Close() error
}

type HTTPRelayAwareApplier interface {
	ApplyWithRelay(context.Context, []model.HTTPRule, []model.RelayListener) error
}

type HTTPWireGuardAwareApplier interface {
	ApplyWithRelayAndWireGuardProfiles(context.Context, []model.HTTPRule, []model.RelayListener, []model.WireGuardProfile) error
}

type HTTPEgressAwareApplier interface {
	ApplyWithRelayWireGuardAndEgressProfiles(context.Context, []model.HTTPRule, []model.RelayListener, []model.WireGuardProfile, []model.EgressProfile) error
}

type L4RelayAwareApplier interface {
	ApplyWithRelay(context.Context, []model.L4Rule, []model.RelayListener) error
}

type Updater interface {
	Stage(context.Context, model.VersionPackage) (string, error)
	Activate(stagedPath string, desiredVersion string) error
}

type hostTrafficCollector interface {
	Snapshot() (hosttraffic.Snapshot, error)
}

type App struct {
	cfg                  Config
	syncClient           SyncClient
	store                store.Store
	httpApplier          HTTPApplier
	certApplier          CertificateApplier
	l4Applier            L4Applier
	relayApplier         RelayApplier
	updater              Updater
	runtime              *agentruntime.Runtime
	taskClient           *agenttask.Client
	hostTrafficCollector hostTrafficCollector
	moduleRegistry       *agentmodule.Registry
	certModule           *modulecerts.Module
	diagnosticModule     *modulediagnostics.Module
	egressModule         *moduleegress.Module
	httpModule           *modulehttp.Module
	l4Module             *modulel4.Module
	relayModule          *modulerelay.Module
	wireGuardRuntime     *modulewireguard.Runtime
	relayTimeoutReset    func()
	pendingSyncMetadata  map[string]string
	closeOnce            sync.Once
	syncMu               sync.Mutex
}

func advertisedCapabilities(cfg Config) []string {
	registry, err := newAppModuleRegistry(cfg, nil, nil, nil, newHTTPModuleFromConfig(cfg), newL4ModuleFromConfig(cfg), nil, nil)
	if err != nil {
		return nil
	}
	return core.CapabilityNames(appCapabilitySource{cfg: cfg, registry: registry})
}

func newHTTPModuleFromConfigWithTLS(cfg Config, _ modulehttp.TLSMaterialProvider) *modulehttp.Module {
	return newHTTPModuleFromConfig(cfg)
}

func normalizeConstructorConfig(cfg Config) Config {
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
	if cfg.HeartbeatInterval <= 0 {
		cfg.HeartbeatInterval = defaults.HeartbeatInterval
	}
	if cfg.HTTPResilience == (config.HTTPResilienceConfig{}) {
		cfg.HTTPResilience = defaults.HTTPResilience
	}
	if !cfg.TrafficStatsExplicit {
		cfg.TrafficStatsEnabled = defaults.TrafficStatsEnabled
	}
	if !cfg.WireGuardExplicit {
		cfg.WireGuardEnabled = defaults.WireGuardEnabled
	}

	return cfg
}

func newHTTPModuleFromConfig(cfg Config) *modulehttp.Module {
	return modulehttp.NewModule(modulehttp.Config{
		AgentID:      cfg.AgentID,
		HTTP3Enabled: cfg.HTTP3Enabled,
		Transport: modulehttp.TransportOptions{
			DialTimeout:           cfg.HTTPTransport.DialTimeout,
			TLSHandshakeTimeout:   cfg.HTTPTransport.TLSHandshakeTimeout,
			ResponseHeaderTimeout: cfg.HTTPTransport.ResponseHeaderTimeout,
			IdleConnTimeout:       cfg.HTTPTransport.IdleConnTimeout,
			KeepAlive:             cfg.HTTPTransport.KeepAlive,
			MaxConnsPerHost:       cfg.HTTPTransport.MaxConnsPerHost,
		},
		Resilience: modulehttp.StreamResilienceOptions{
			ResumeEnabled:            cfg.HTTPResilience.ResumeEnabled,
			ResumeMaxAttempts:        cfg.HTTPResilience.ResumeMaxAttempts,
			SameBackendRetryAttempts: cfg.HTTPResilience.SameBackendRetryAttempts,
		},
		BackendFailures: backendCacheConfigFromAppConfig(cfg),
	})
}

func newL4ModuleFromConfig(cfg Config) *modulel4.Module {
	return modulel4.NewModule(modulel4.Config{
		AgentID:         cfg.AgentID,
		BackendFailures: backendCacheConfigFromAppConfig(cfg),
	})
}

func New(cfg Config) (*App, error) {
	cfg = normalizeConstructorConfig(cfg)
	traffic.SetEnabled(cfg.TrafficStatsEnabled)

	resetRelayTimeouts := modulerelay.ConfigureTimeouts(modulerelay.TimeoutConfig{
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

	st, err := store.NewFilesystem(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	certManager, err := modulecerts.NewManager(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	executablePath, err := os.Executable()
	if err != nil {
		return nil, err
	}
	wireGuardRuntime := newSharedWireGuardRuntime()
	httpModule := newHTTPModuleFromConfig(cfg)
	l4Module := newL4ModuleFromConfig(cfg)
	certModule := modulecerts.NewModule(certManager)
	diagnosticModule := modulediagnostics.NewModule()
	egressModule := moduleegress.NewModule(nil)
	relayModule := modulerelay.NewModule(modulerelay.Config{AgentID: cfg.AgentID, AgentName: cfg.AgentName})
	moduleRegistry, err := newAppModuleRegistry(cfg, certModule, diagnosticModule, egressModule, httpModule, l4Module, relayModule, wireGuardRuntime)
	if err != nil {
		_ = wireGuardRuntime.Close()
		return nil, err
	}
	capabilities := core.CapabilityNames(appCapabilitySource{cfg: cfg, registry: moduleRegistry})
	client := agentsync.NewClient(agentsync.ClientConfig{
		MasterURL:      cfg.MasterURL,
		AgentToken:     cfg.AgentToken,
		AgentID:        cfg.AgentID,
		AgentName:      cfg.AgentName,
		Capabilities:   capabilities,
		CurrentVersion: cfg.CurrentVersion,
		Platform:       stdruntime.GOOS + "-" + stdruntime.GOARCH,
		RuntimePackage: model.RuntimePackage{
			Version:  cfg.CurrentVersion,
			Platform: stdruntime.GOOS,
			Arch:     stdruntime.GOARCH,
			SHA256:   cfg.RuntimePackageSHA256,
		},
		HTTPTransport: cfg.HTTPTransport,
	}, nil)
	taskClient := agenttask.NewClient(agenttask.ClientConfig{
		MasterURL:     cfg.MasterURL,
		AgentToken:    cfg.AgentToken,
		AgentID:       cfg.AgentID,
		AgentName:     cfg.AgentName,
		Version:       cfg.CurrentVersion,
		Capabilities:  capabilities,
		ReconnectWait: time.Second,
		HTTPTransport: cfg.HTTPTransport,
		Handler:       diagnosticModule,
	})
	app := newAppWithAllDeps(
		cfg,
		st,
		client,
		nil,
		certManager,
		nil,
		nil,
		agentupdate.NewManager(
			cfg.DataDir,
			executablePath,
			os.Args,
			os.Environ(),
			platformlinux.ExecReplacement,
			nil,
		),
		taskClient,
	)
	app.certModule = certModule
	app.setDiagnosticModule(diagnosticModule)
	app.hostTrafficCollector = hosttraffic.NewCollector(cfg.TrafficInterfaces)
	app.moduleRegistry = moduleRegistry
	app.runtime = agentruntime.NewWithActivator(app.snapshotActivator())
	app.egressModule = egressModule
	app.httpModule = httpModule
	app.l4Module = l4Module
	app.relayModule = relayModule
	app.relayApplier = relayModule
	app.wireGuardRuntime = wireGuardRuntime
	app.relayTimeoutReset = resetRelayTimeouts
	restoreRelayTimeouts = false
	return app, nil
}

func newAppModuleRegistry(
	cfg Config,
	certModule *modulecerts.Module,
	diagnosticModule *modulediagnostics.Module,
	egressModule *moduleegress.Module,
	httpModule *modulehttp.Module,
	l4Module *modulel4.Module,
	relayModule *modulerelay.Module,
	wireGuardRuntime *modulewireguard.Runtime,
) (*agentmodule.Registry, error) {
	registry := agentmodule.NewRegistry()
	if certModule != nil {
		if err := registry.Register(certModule); err != nil {
			return nil, err
		}
	}
	if diagnosticModule != nil {
		if err := registry.Register(diagnosticModule); err != nil {
			return nil, err
		}
	}
	if egressModule != nil {
		if err := registry.Register(egressModule); err != nil {
			return nil, err
		}
	}
	if httpModule != nil {
		if err := registry.Register(httpModule); err != nil {
			return nil, err
		}
	}
	if cfg.WireGuardModuleEnabled() {
		if err := registry.Register(modulewireguard.NewModule(wireGuardRuntime)); err != nil {
			return nil, err
		}
	}
	if relayModule != nil {
		if err := registry.Register(relayModule); err != nil {
			return nil, err
		}
	}
	if l4Module != nil {
		if err := registry.Register(l4Module); err != nil {
			return nil, err
		}
	}
	if err := registry.Register(moduletraffic.NewModule()); err != nil {
		return nil, err
	}
	return registry, nil
}

type appCapabilitySource struct {
	cfg      Config
	registry *agentmodule.Registry
}

func (s appCapabilitySource) Capabilities(snapshot agentmodule.SnapshotView) []agentmodule.Capability {
	capabilities := []agentmodule.Capability{
		{Name: "http_rules", Enabled: true},
		{Name: "cert_install", Enabled: true},
		{Name: "local_acme", Enabled: true},
		{Name: "l4", Enabled: true},
		{Name: "relay_quic", Enabled: true},
	}
	if s.cfg.HTTP3Enabled {
		capabilities = append(capabilities, agentmodule.Capability{Name: "http3_ingress", Enabled: true})
	}
	if s.registry != nil {
		capabilities = append(capabilities, s.registry.Capabilities(snapshot)...)
	}
	return capabilities
}

func newAppWithDeps(
	cfg Config,
	st store.Store,
	client SyncClient,
	certApplier CertificateApplier,
	l4Applier L4Applier,
	relayApplier RelayApplier,
) *App {
	return newAppWithAllDeps(cfg, st, client, nil, certApplier, l4Applier, relayApplier, nil, nil)
}

func newAppWithHTTPDeps(
	cfg Config,
	st store.Store,
	client SyncClient,
	httpApplier HTTPApplier,
	certApplier CertificateApplier,
	l4Applier L4Applier,
	relayApplier RelayApplier,
) *App {
	return newAppWithAllDeps(cfg, st, client, httpApplier, certApplier, l4Applier, relayApplier, nil, nil)
}

func newAppWithAllDeps(
	cfg Config,
	st store.Store,
	client SyncClient,
	httpApplier HTTPApplier,
	certApplier CertificateApplier,
	l4Applier L4Applier,
	relayApplier RelayApplier,
	updater Updater,
	taskClient *agenttask.Client,
) *App {
	if cfg.HeartbeatInterval <= 0 {
		cfg.HeartbeatInterval = config.Default().HeartbeatInterval
	}
	app := &App{
		cfg:          cfg,
		store:        st,
		syncClient:   client,
		httpApplier:  httpApplier,
		certApplier:  certApplier,
		l4Applier:    l4Applier,
		relayApplier: relayApplier,
		updater:      updater,
		taskClient:   taskClient,
	}
	app.runtime = agentruntime.NewWithActivator(app.snapshotActivator())
	return app
}

func (a *App) setDiagnosticModule(diagnosticModule *modulediagnostics.Module) {
	if a == nil {
		return
	}
	a.diagnosticModule = diagnosticModule
}

func (a *App) Diagnose(ctx context.Context, taskType string, ruleID int) (map[string]any, error) {
	if a == nil {
		return nil, errors.New("diagnostic handler is not configured")
	}
	msg := agenttask.TaskMessage{
		TaskType:   taskType,
		RawPayload: map[string]any{"rule_id": ruleID},
	}
	if a.diagnosticModule != nil && a.diagnosticModule.Handler() != nil {
		return a.diagnosticModule.HandleTask(ctx, msg)
	}
	return nil, errors.New("diagnostic handler is not configured")
}

func (a *App) DiagnoseSnapshot(ctx context.Context, snapshot Snapshot, taskType string, ruleID int) (map[string]any, error) {
	if err := a.applyManagedCertificates(ctx, snapshot); err != nil {
		return nil, err
	}
	diagnosticModule, err := a.configuredDiagnosticModule(ctx, snapshot)
	if err != nil {
		return nil, err
	}
	if diagnosticModule == nil {
		return nil, errors.New("diagnostic handler is not configured")
	}
	return diagnosticModule.HandleSnapshotTask(ctx, snapshot, agenttask.TaskMessage{
		TaskType:   taskType,
		RawPayload: map[string]any{"rule_id": ruleID},
	})
}

func (a *App) configuredDiagnosticModule(ctx context.Context, snapshot Snapshot) (*modulediagnostics.Module, error) {
	if a == nil || a.diagnosticModule == nil {
		return nil, nil
	}
	if a.diagnosticModule.HTTPProber() != nil && a.diagnosticModule.TCPProber() != nil {
		return a.diagnosticModule, nil
	}
	if a.moduleRegistry == nil {
		return a.diagnosticModule, nil
	}
	providers, err := a.moduleRegistry.ProviderResolver()
	if err != nil {
		return nil, err
	}
	if err := a.diagnosticModule.Apply(ctx, agentmodule.ApplyRequest{
		Next:      snapshot,
		Providers: providers,
	}); err != nil {
		return nil, err
	}
	return a.diagnosticModule, nil
}

func (a *App) Run(ctx context.Context) error {
	defer func() {
		_ = a.Close()
	}()

	applied, err := a.store.LoadAppliedSnapshot()
	if err != nil {
		return err
	}
	hydratedApplied := a.hydrateAppliedSnapshotFromDesired(applied)
	if err := a.runtime.Apply(ctx, Snapshot{}, hydratedApplied); err != nil {
		log.Printf("[agent] startup runtime hydration error at revision %d: %v", applied.Revision, err)
		_ = a.recordRuntimeErrorWithRevision(err, applied.Revision)
	} else {
		if !reflect.DeepEqual(applied, hydratedApplied) {
			if err := a.store.SaveAppliedSnapshot(hydratedApplied); err != nil {
				log.Printf("[agent] startup applied snapshot hydration save error at revision %d: %v", hydratedApplied.Revision, err)
				_ = a.recordRuntimeErrorWithRevision(err, hydratedApplied.Revision)
			}
		}
		if err := a.persistTrafficStatsInterval(hydratedApplied.AgentConfig.TrafficStatsInterval); err != nil {
			log.Printf("[agent] startup traffic stats interval hydration error at revision %d: %v", hydratedApplied.Revision, err)
			_ = a.recordRuntimeErrorWithRevision(err, hydratedApplied.Revision)
		}
	}

	if err := a.performSync(ctx); err != nil {
		if errors.Is(err, agentupdate.ErrRestartRequested) {
			return nil
		}
		if applied.DesiredVersion == "" && applied.Revision == 0 {
			return err
		}
	}

	if a.taskClient != nil {
		go func() {
			if err := a.taskClient.Run(ctx); err != nil && ctx.Err() == nil {
				log.Printf("[agent] task client error: %v", err)
			}
		}()
	}

	ticker := time.NewTicker(a.cfg.HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := a.performSync(ctx); errors.Is(err, agentupdate.ErrRestartRequested) {
				return nil
			}
		}
	}
}

func (a *App) hydrateAppliedSnapshotFromDesired(applied Snapshot) Snapshot {
	if a == nil || a.store == nil || runtimePayloadComplete(applied) {
		return applied
	}
	desired, err := a.store.LoadDesiredSnapshot()
	if err != nil || !desiredCanHydrateApplied(applied, desired) {
		return applied
	}
	return mergeSnapshotPayload(applied, desired)
}

func desiredCanHydrateApplied(applied, desired Snapshot) bool {
	if desired.Revision == 0 && desired.DesiredVersion == "" {
		return false
	}
	if applied.Revision != desired.Revision {
		return false
	}
	if applied.DesiredVersion != "" && desired.DesiredVersion != "" && applied.DesiredVersion != desired.DesiredVersion {
		return false
	}
	return true
}

func runtimePayloadComplete(snapshot Snapshot) bool {
	return snapshot.Rules != nil &&
		snapshot.L4Rules != nil &&
		snapshot.RelayListeners != nil &&
		snapshot.WireGuardProfiles != nil &&
		snapshot.EgressProfiles != nil &&
		snapshot.Certificates != nil &&
		snapshot.CertificatePolicies != nil
}

func (a *App) Close() error {
	if a == nil {
		return nil
	}
	a.closeOnce.Do(func() {
		a.closeLocalRuntimes()
	})
	return nil
}

func (a *App) performSync(ctx context.Context) error {
	a.syncMu.Lock()
	defer a.syncMu.Unlock()

	applied, err := a.store.LoadAppliedSnapshot()
	if err != nil {
		return err
	}
	controller := a.syncController()
	plan, err := controller.BuildSyncPlan(ctx, applied)
	if err != nil {
		return err
	}
	return controller.PerformSyncPlan(ctx, plan)
}

func (a *App) SyncNow(ctx context.Context) error {
	return a.performSync(ctx)
}

func (a *App) applyModules(ctx context.Context, previous, snapshot Snapshot) error {
	if a == nil || a.moduleRegistry == nil {
		return nil
	}
	return a.moduleRegistry.Apply(ctx, previous, snapshot)
}

func (a *App) closeLocalRuntimes() {
	hasModuleRegistry := a.moduleRegistry != nil
	if !hasModuleRegistry {
		if closer, ok := a.certApplier.(certCloser); ok {
			_ = closer.Close()
		}
	}
	if a.httpApplier != nil {
		_ = a.httpApplier.Close()
	}
	if a.relayApplier != nil {
		_ = a.relayApplier.Close()
	}
	if a.l4Applier != nil {
		_ = a.l4Applier.Close()
	}
	if hasModuleRegistry {
		_ = a.moduleRegistry.StopAll(context.Background())
		a.moduleRegistry = nil
		a.wireGuardRuntime = nil
	} else if a.wireGuardRuntime != nil {
		_ = a.wireGuardRuntime.Close()
		a.wireGuardRuntime = nil
	}
	if a.relayTimeoutReset != nil {
		a.relayTimeoutReset()
		a.relayTimeoutReset = nil
	}
}

func (a *App) handlePendingUpdate(ctx context.Context, snapshot Snapshot) error {
	return a.syncController().HandlePendingUpdate(ctx, snapshot)
}
