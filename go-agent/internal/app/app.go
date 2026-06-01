package app

import (
	"context"
	"errors"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/control"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/core"
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
	"log"
	"os"
	"reflect"
	stdruntime "runtime"
	"sync"
	"time"
)

type Config = model.Config
type Snapshot = core.Snapshot
type SyncRequest = control.SyncRequest

type SyncClient interface {
	Sync(context.Context, SyncRequest) (Snapshot, error)
}

type Updater interface {
	Stage(context.Context, model.VersionPackage) (string, error)
	Activate(stagedPath string, desiredVersion string) error
}

type App struct {
	cfg               Config
	syncClient        SyncClient
	store             core.Store
	updater           Updater
	runtime           *core.Runtime
	taskClient        *control.TaskClient
	moduleRegistry    *agentmodule.Registry
	diagnosticModule  *modulediagnostics.Module
	trafficReports    core.TrafficReporter
	certReports       core.ManagedCertificateReporter
	relayTimeoutReset func()
	closeOnce         sync.Once
	syncMu            sync.Mutex
}

func advertisedCapabilities(cfg Config) []string {
	registry, err := newCapabilityModuleRegistry(cfg)
	if err != nil {
		return nil
	}
	return core.CapabilityNames(appCapabilitySource{cfg: cfg, registry: registry})
}

func newHTTPModuleFromConfigWithTLS(cfg Config, _ modulehttp.TLSMaterialProvider) *modulehttp.Module {
	return newHTTPModuleFromConfig(cfg)
}

func normalizeConstructorConfig(cfg Config) Config {
	defaults := model.Default()

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
	if cfg.HTTPResilience == (model.HTTPResilienceConfig{}) {
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

func backendCacheConfigFromAppConfig(cfg Config) model.BackendCacheConfig {
	if !cfg.HasExplicitBackendFailureOverrides() {
		return model.BackendCacheConfig{}
	}
	return model.BackendCacheConfig{
		FailureBackoffBase:  cfg.BackendFailures.BackoffBase,
		FailureBackoffLimit: cfg.BackendFailures.BackoffLimit,
	}
}

type configuredModules struct {
	registry    *agentmodule.Registry
	diagnostics *modulediagnostics.Module
	traffic     core.TrafficReporter
	certReports core.ManagedCertificateReporter
}

func newConfiguredModules(cfg Config, certOptions ...modulecerts.Option) (configuredModules, error) {
	certModule, err := modulecerts.NewManagedModule(cfg.DataDir, certOptions...)
	if err != nil {
		return configuredModules{}, err
	}
	diagnosticModule := modulediagnostics.NewModule()
	trafficModule := moduletraffic.NewModule(moduletraffic.Config{
		Interfaces: cfg.TrafficInterfaces,
		Enabled:    cfg.TrafficStatsEnabled,
		EnabledSet: true,
	})
	registry, err := newAppModuleRegistry([]agentmodule.Module{
		certModule,
		diagnosticModule,
		moduleegress.NewModule(nil),
		newHTTPModuleFromConfig(cfg),
		configuredWireGuardModule(cfg),
		modulerelay.NewModule(modulerelay.Config{AgentID: cfg.AgentID, AgentName: cfg.AgentName}),
		newL4ModuleFromConfig(cfg),
		trafficModule,
	})
	if err != nil {
		return configuredModules{}, err
	}
	return configuredModules{
		registry:    registry,
		diagnostics: diagnosticModule,
		traffic:     trafficModule,
		certReports: certModule,
	}, nil
}

func newCapabilityModuleRegistry(cfg Config) (*agentmodule.Registry, error) {
	return newAppModuleRegistry([]agentmodule.Module{
		modulecerts.NewModule(nil),
		modulediagnostics.NewModule(),
		moduleegress.NewModule(nil),
		newHTTPModuleFromConfig(cfg),
		configuredWireGuardModule(cfg),
		modulerelay.NewModule(modulerelay.Config{AgentID: cfg.AgentID, AgentName: cfg.AgentName}),
		newL4ModuleFromConfig(cfg),
		moduletraffic.NewModule(),
	})
}

func configuredWireGuardModule(cfg Config) agentmodule.Module {
	if !cfg.WireGuardModuleEnabled() {
		return nil
	}
	return modulewireguard.NewManagedModule(nil)
}

func New(cfg Config) (*App, error) {
	cfg = normalizeConstructorConfig(cfg)

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

	st, err := core.NewFilesystem(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	executablePath, err := os.Executable()
	if err != nil {
		return nil, err
	}
	modules, err := newConfiguredModules(cfg)
	if err != nil {
		return nil, err
	}
	capabilities := core.CapabilityNames(appCapabilitySource{cfg: cfg, registry: modules.registry})
	client := control.NewSyncClient(control.SyncClientConfig{
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
	taskClient := control.NewTaskClient(control.TaskClientConfig{
		MasterURL:     cfg.MasterURL,
		AgentToken:    cfg.AgentToken,
		AgentID:       cfg.AgentID,
		AgentName:     cfg.AgentName,
		Version:       cfg.CurrentVersion,
		Capabilities:  capabilities,
		ReconnectWait: time.Second,
		HTTPTransport: cfg.HTTPTransport,
		Handler:       modules.diagnostics,
	})
	app := newAppWithAllDeps(
		cfg,
		st,
		client,
		core.NewUpdateManager(
			cfg.DataDir,
			executablePath,
			os.Args,
			os.Environ(),
			execReplacement,
			nil,
		),
		taskClient,
	)
	app.setConfiguredModules(modules)
	app.relayTimeoutReset = resetRelayTimeouts
	restoreRelayTimeouts = false
	return app, nil
}

func newAppModuleRegistry(modules []agentmodule.Module) (*agentmodule.Registry, error) {
	registry := agentmodule.NewRegistry()
	for _, mod := range modules {
		if mod == nil {
			continue
		}
		if err := registry.Register(mod); err != nil {
			return nil, err
		}
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

func newAppWithAllDeps(
	cfg Config,
	st core.Store,
	client SyncClient,
	updater Updater,
	taskClient *control.TaskClient,
) *App {
	if cfg.HeartbeatInterval <= 0 {
		cfg.HeartbeatInterval = model.Default().HeartbeatInterval
	}
	app := &App{
		cfg:        cfg,
		store:      st,
		syncClient: client,
		updater:    updater,
		taskClient: taskClient,
	}
	app.runtime = core.NewRuntimeWithActivator(appSnapshotActivator(nil))
	return app
}

func (a *App) setConfiguredModules(modules configuredModules) {
	if a == nil {
		return
	}
	a.moduleRegistry = modules.registry
	a.diagnosticModule = modules.diagnostics
	a.trafficReports = modules.traffic
	a.certReports = modules.certReports
	a.runtime = core.NewRuntimeWithActivator(appSnapshotActivator(modules.registry))
}

func (a *App) ModuleNames() []string {
	if a == nil || a.moduleRegistry == nil {
		return nil
	}
	return a.moduleRegistry.Names()
}

func (a *App) Diagnose(ctx context.Context, taskType string, ruleID int) (map[string]any, error) {
	if a == nil {
		return nil, errors.New("diagnostic handler is not configured")
	}
	msg := control.TaskMessage{
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
	diagnosticModule, err := a.snapshotDiagnosticModule(ctx, snapshot)
	if err != nil {
		return nil, err
	}
	if diagnosticModule == nil {
		return nil, errors.New("diagnostic handler is not configured")
	}
	return diagnosticModule.HandleSnapshotTask(ctx, snapshot, control.TaskMessage{
		TaskType:   taskType,
		RawPayload: map[string]any{"rule_id": ruleID},
	})
}

func (a *App) snapshotDiagnosticModule(ctx context.Context, snapshot Snapshot) (*modulediagnostics.Module, error) {
	if a == nil {
		return nil, nil
	}
	if a.diagnosticModule != nil && a.diagnosticModule.HTTPProber() != nil && a.diagnosticModule.TCPProber() != nil {
		return a.diagnosticModule, nil
	}
	if a.moduleRegistry == nil {
		return a.diagnosticModule, nil
	}
	providers, err := a.moduleRegistry.ProviderResolver()
	if err != nil {
		return nil, err
	}
	diagnosticModule := modulediagnostics.NewModule()
	if err := diagnosticModule.Apply(ctx, agentmodule.ApplyRequest{
		Next:      snapshot,
		Providers: providers,
	}); err != nil {
		return nil, err
	}
	return diagnosticModule, nil
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
		_ = a.syncController().RecordRuntimeErrorWithRevision(err, applied.Revision)
	} else {
		if !reflect.DeepEqual(applied, hydratedApplied) {
			if err := a.store.SaveAppliedSnapshot(hydratedApplied); err != nil {
				log.Printf("[agent] startup applied snapshot hydration save error at revision %d: %v", hydratedApplied.Revision, err)
				_ = a.syncController().RecordRuntimeErrorWithRevision(err, hydratedApplied.Revision)
			}
		}
		if err := a.syncController().PersistTrafficStatsInterval(hydratedApplied.AgentConfig.TrafficStatsInterval); err != nil {
			log.Printf("[agent] startup traffic stats interval hydration error at revision %d: %v", hydratedApplied.Revision, err)
			_ = a.syncController().RecordRuntimeErrorWithRevision(err, hydratedApplied.Revision)
		}
	}

	if err := a.performSync(ctx); err != nil {
		if errors.Is(err, core.ErrRestartRequested) {
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
			if err := a.performSync(ctx); errors.Is(err, core.ErrRestartRequested) {
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
	return core.MergeSnapshotPayload(applied, desired)
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
	if a.moduleRegistry != nil {
		_ = a.moduleRegistry.StopAll(context.Background())
		a.moduleRegistry = nil
	}
	if a.relayTimeoutReset != nil {
		a.relayTimeoutReset()
		a.relayTimeoutReset = nil
	}
}

func (a *App) handlePendingUpdate(ctx context.Context, snapshot Snapshot) error {
	return a.syncController().HandlePendingUpdate(ctx, snapshot)
}
