package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/control"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/core"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/hotrestart"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/ingress"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	agentmodule "github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
	modulecerts "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/certs"
	moduleddns "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/ddns"
	modulediagnostics "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/diagnostics"
	moduleegress "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/egress"
	modulehostmetrics "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/hostmetrics"
	modulehttp "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/http"
	modulel4 "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/l4"
	modulerelay "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/relay"
	moduletraffic "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/traffic"
	modulewireguard "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/wireguard"
	"log"
	"os"
	"reflect"
	stdruntime "runtime"
	"strings"
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
	Activate(context.Context, string, string) error
}

type hotRestartProcess interface {
	Activate(context.Context) error
	TransferAuthority(context.Context) error
	Wait() error
	Signal(os.Signal) error
	Abort() error
}

type hotRestartStartFunc func(context.Context, hotrestart.Launch) (hotRestartProcess, error)
type hotRestartDrainFunc func(context.Context, hotrestart.Identity) error
type hotRestartSuperviseFunc func(context.Context, hotRestartProcess, string, hotrestart.Identity) error

type App struct {
	cfg                    Config
	syncClient             SyncClient
	store                  core.Store
	updater                Updater
	runtime                *core.Runtime
	taskClient             *control.TaskClient
	moduleRegistry         *agentmodule.Registry
	diagnosticModule       *modulediagnostics.Module
	trafficReports         core.TrafficReporter
	hostMetricsReports     core.HostMetricsReporter
	certReports            core.ManagedCertificateReporter
	ddns                   *moduleddns.Module
	generations            *core.GenerationManager
	relayTimeoutReset      func()
	closeOnce              sync.Once
	syncMu                 sync.Mutex
	runCtxMu               sync.RWMutex
	runCtx                 context.Context
	taskRunMu              sync.Mutex
	taskRunCancel          context.CancelFunc
	taskRunDone            <-chan struct{}
	hotRestartStart        hotRestartStartFunc
	hotRestartDrain        hotRestartDrainFunc
	hotRestartSupervise    hotRestartSuperviseFunc
	hotRestartDrainTimeout time.Duration
	hotRestartChild        bool
	processStreams         *ingress.ProcessStreamRegistry
	processPackets         *ingress.ProcessPacketRegistry
}

func advertisedCapabilities(cfg Config) []string {
	return core.CapabilityNames(appCapabilitySource{cfg: cfg})
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
	return modulehttp.NewModule(httpModuleConfigFromAppConfig(cfg))
}

func httpModuleConfigFromAppConfig(cfg Config) modulehttp.Config {
	return modulehttp.Config{
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
	}
}

func newL4ModuleFromConfig(cfg Config) *modulel4.Module {
	return modulel4.NewModule(l4ModuleConfigFromAppConfig(cfg))
}

func l4ModuleConfigFromAppConfig(cfg Config) modulel4.Config {
	return modulel4.Config{
		AgentID:         cfg.AgentID,
		BackendFailures: backendCacheConfigFromAppConfig(cfg),
	}
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
	registry       *agentmodule.Registry
	diagnostics    *modulediagnostics.Module
	traffic        core.TrafficReporter
	hostMetrics    core.HostMetricsReporter
	certReports    core.ManagedCertificateReporter
	ddns           *moduleddns.Module
	generations    *core.GenerationManager
	processStreams *ingress.ProcessStreamRegistry
	processPackets *ingress.ProcessPacketRegistry
}

type processPacketRegistryConsumer interface {
	SetProcessPacketRegistry(*ingress.ProcessPacketRegistry)
}

var (
	_ processPacketRegistryConsumer = (*modulehttp.Module)(nil)
	_ processPacketRegistryConsumer = (*modulel4.Module)(nil)
	_ processPacketRegistryConsumer = (*modulerelay.Module)(nil)
	_ processPacketRegistryConsumer = (*modulewireguard.Module)(nil)
)

func configureProcessPacketRegistry(registry *ingress.ProcessPacketRegistry, consumers ...processPacketRegistryConsumer) {
	for _, consumer := range consumers {
		if consumer != nil {
			consumer.SetProcessPacketRegistry(registry)
		}
	}
}

func newConfiguredModules(cfg Config, certOptions ...modulecerts.Option) (configuredModules, error) {
	registry := agentmodule.NewRegistry()
	drain := core.NewGenerationDrain(nil)
	// Revision applies supply their leased drain timeout per cutover. Zero keeps
	// the manager's default only for startup and non-revision activations.
	generations := core.NewManagedGenerationManager(registry, drain, 0)
	certModule, err := modulecerts.NewManagedGenerationModule(cfg.DataDir, generations, certOptions...)
	if err != nil {
		return configuredModules{}, err
	}
	diagnosticModule := modulediagnostics.NewGenerationModule(generations)
	trafficModule := moduletraffic.NewModule(moduletraffic.Config{
		Interfaces:         cfg.TrafficInterfaces,
		Enabled:            cfg.TrafficStatsEnabled,
		EnabledSet:         true,
		GenerationSelector: generations,
	})
	ddnsModule := moduleddns.NewModule(moduleddns.Config{
		IPv4PublicAPIURL:   cfg.DDNS.IPv4PublicAPIURL,
		IPv6PublicAPIURL:   cfg.DDNS.IPv6PublicAPIURL,
		GenerationSelector: generations,
	})
	httpConfig := httpModuleConfigFromAppConfig(cfg)
	httpConfig.GenerationSelector = generations
	httpConfig.SessionRegistrar = generations
	httpConfig.ExternalDrainLifecycle = true
	l4Config := l4ModuleConfigFromAppConfig(cfg)
	l4Config.GenerationSelector = generations
	l4Config.SessionRegistrar = generations
	l4Config.ExternalDrainLifecycle = true
	relayConfig := modulerelay.Config{
		AgentID: cfg.AgentID, AgentName: cfg.AgentName,
		GenerationSelector: generations, SessionRegistrar: generations, ExternalDrainLifecycle: true,
	}
	processStreams := ingress.NewProcessStreamRegistry()
	processPackets := ingress.NewProcessPacketRegistry()
	httpModule := modulehttp.NewModule(httpConfig)
	httpModule.SetProcessStreamRegistry(processStreams)
	relayModule := modulerelay.NewModule(relayConfig)
	relayModule.SetProcessStreamRegistry(processStreams)
	l4Module := modulel4.NewModule(l4Config)
	l4Module.SetProcessStreamRegistry(processStreams)
	wireGuardModule := configuredGenerationWireGuardModule(cfg, generations, generations)
	packetConsumers := []processPacketRegistryConsumer{httpModule, l4Module, relayModule}
	if managedWireGuard, ok := wireGuardModule.(*modulewireguard.Module); ok {
		packetConsumers = append(packetConsumers, managedWireGuard)
	}
	configureProcessPacketRegistry(processPackets, packetConsumers...)
	modules := []agentmodule.Module{
		certModule,
		diagnosticModule,
		moduleegress.NewModule(nil),
		httpModule,
		wireGuardModule,
		relayModule,
		l4Module,
		trafficModule,
		ddnsModule,
	}
	for _, mod := range modules {
		if mod == nil {
			continue
		}
		if err := registry.Register(mod); err != nil {
			return configuredModules{}, err
		}
	}
	if err := registry.ValidateGenerationCompatibility(); err != nil {
		return configuredModules{}, err
	}
	return configuredModules{
		registry:       registry,
		diagnostics:    diagnosticModule,
		traffic:        trafficModule,
		hostMetrics:    modulehostmetrics.NewReporter(modulehostmetrics.ReporterConfig{}),
		certReports:    certModule,
		ddns:           ddnsModule,
		generations:    generations,
		processStreams: processStreams,
		processPackets: processPackets,
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

func configuredGenerationWireGuardModule(cfg Config, selector modulewireguard.WireGuardGenerationSelector, sessions modulewireguard.WireGuardSessionRegistrar) agentmodule.Module {
	if !cfg.WireGuardModuleEnabled() {
		return nil
	}
	return modulewireguard.NewManagedModuleWithConfig(nil, modulewireguard.ModuleConfig{
		GenerationSelector: selector, SessionRegistrar: sessions, ExternalDrainLifecycle: true,
	})
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
		DDNSReporter:  modules.ddns,
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
		nil,
		taskClient,
	)
	app.updater = core.NewUpdateManager(
		cfg.DataDir,
		executablePath,
		os.Args,
		os.Environ(),
		app.hotRestartReplacement,
		nil,
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
	cfg             Config
	registry        *agentmodule.Registry
	platform        string
	arch            string
	hotUpgradeReady bool
}

func (s appCapabilitySource) Capabilities(snapshot agentmodule.SnapshotView) []agentmodule.Capability {
	capabilities := []agentmodule.Capability{
		{Name: "http_rules", Enabled: true},
		{Name: "cert_install", Enabled: true},
		{Name: "local_acme", Enabled: true},
		{Name: "l4", Enabled: true},
		{Name: "relay_quic", Enabled: true},
	}
	if s.cfg.WireGuardModuleEnabled() {
		capabilities = append(capabilities, agentmodule.Capability{Name: "wireguard", Enabled: true})
	}
	capabilities = append(capabilities, agentmodule.Capability{Name: "egress_profiles", Enabled: true})
	if s.cfg.HTTP3Enabled {
		capabilities = append(capabilities, agentmodule.Capability{Name: "http3_ingress", Enabled: true})
	}
	platform, arch := s.platform, s.arch
	if platform == "" {
		platform = stdruntime.GOOS
	}
	if arch == "" {
		arch = stdruntime.GOARCH
	}
	for _, name := range core.HotUpgradeCapabilityNames(platform, arch, s.hotUpgradeReady) {
		capabilities = append(capabilities, agentmodule.Capability{Name: name, Enabled: true})
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
		cfg:            cfg,
		store:          st,
		syncClient:     client,
		updater:        updater,
		taskClient:     taskClient,
		runCtx:         context.Background(),
		processStreams: ingress.NewProcessStreamRegistry(),
		processPackets: ingress.NewProcessPacketRegistry(),
	}
	app.hotRestartStart = app.startHotRestartWithResources
	app.hotRestartDrain = app.drainHotRestartParent
	app.hotRestartDrainTimeout = hotRestartDrainTimeout
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
	a.hostMetricsReports = modules.hostMetrics
	a.certReports = modules.certReports
	a.ddns = modules.ddns
	a.generations = modules.generations
	a.processStreams = modules.processStreams
	a.processPackets = modules.processPackets
	a.runtime = core.NewRuntimeWithGenerationManager(modules.generations)
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
	a.setRunContext(ctx)

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

	return a.runControlLoop(ctx, applied)
}

func (a *App) RunHotRestartChild(ctx context.Context, child *hotrestart.ChildSession) error {
	if a == nil || child == nil {
		return errors.New("hot restart child app and session are required")
	}
	defer func() {
		_ = a.Close()
	}()
	a.hotRestartChild = true
	a.setRunContext(ctx)
	desired, err := a.store.LoadDesiredSnapshot()
	if err != nil {
		return err
	}
	if err := a.validateHotRestartIdentity(child.Identity, desired); err != nil {
		return err
	}
	if a.processStreams == nil {
		return errors.New("hot restart process stream registry is required")
	}
	if a.processPackets == nil {
		return errors.New("hot restart process packet registry is required")
	}
	streamSet, err := a.processStreams.Import(child.StreamDescriptors, child.StreamFiles)
	child.StreamFiles = nil
	if err != nil {
		return fmt.Errorf("import hot restart stream listeners: %w", err)
	}
	defer streamSet.Close()
	packetSet, err := a.processPackets.Import(child.PacketDescriptors, child.PacketFiles)
	child.PacketFiles = nil
	if err != nil {
		return fmt.Errorf("import hot restart packet connections: %w", err)
	}
	defer packetSet.Close()
	if err := a.runtime.Apply(ctx, Snapshot{}, desired); err != nil {
		return fmt.Errorf("prepare hot restart child generation: %w", err)
	}
	if err := a.validateActiveHotRestartRuntime(child.Identity); err != nil {
		return err
	}
	if err := a.processStreams.ValidateImported(); err != nil {
		return fmt.Errorf("validate hot restart stream listeners: %w", err)
	}
	if err := a.processPackets.ValidateImported(); err != nil {
		return fmt.Errorf("validate hot restart packet connections: %w", err)
	}
	if err := child.Ready(); err != nil {
		return err
	}
	if err := child.AwaitActivation(ctx, a.activateHotRestartChildResources); err != nil {
		return err
	}
	if err := child.AwaitAuthority(ctx, a.processPackets.TakeAuthorityImported); err != nil {
		return err
	}
	return a.runControlLoop(ctx, desired)
}

type hotRestartJournalStore interface {
	LoadGenerationJournal() (model.GenerationJournal, error)
}

func (a *App) validateHotRestartIdentity(identity hotrestart.Identity, desired Snapshot) error {
	if err := identity.Validate(); err != nil {
		return err
	}
	if desired.Revision == 0 {
		if a == nil || a.runtime == nil {
			return errors.New("bootstrap hot restart runtime is required")
		}
		if identity.Revision != 0 {
			return errors.New("bootstrap hot restart revision does not match the durable desired snapshot")
		}
		runtimeDigest, err := hotRestartSnapshotDigest(desired)
		if err != nil {
			return err
		}
		candidate, managed, err := a.runtime.CandidateGenerationIdentity(Snapshot{}, desired)
		if err != nil {
			return err
		}
		if !managed || candidate.ID == "" || candidate.Revision != 0 ||
			candidate.ID != identity.GenerationID ||
			!strings.EqualFold(candidate.SnapshotHash, identity.SnapshotDigest) ||
			!strings.EqualFold(runtimeDigest, identity.SnapshotDigest) ||
			identity.LeaseID != bootstrapHotRestartLeaseID(runtimeDigest) {
			return errors.New("bootstrap hot restart identity does not match the durable desired snapshot")
		}
		return nil
	}
	store, ok := a.store.(hotRestartJournalStore)
	if !ok {
		return errors.New("store does not expose the generation journal required for hot restart")
	}
	journal, err := store.LoadGenerationJournal()
	if err != nil {
		return err
	}
	runtimeDigest, err := hotRestartSnapshotDigest(desired)
	if err != nil {
		return err
	}
	record := matchingHotRestartRecord(journal, desired.Revision)
	if record == nil || desired.Revision != identity.Revision || record.Revision != identity.Revision ||
		!strings.EqualFold(strings.TrimSpace(runtimeDigest), strings.TrimSpace(record.RuntimeSnapshotHash)) ||
		!strings.EqualFold(strings.TrimSpace(record.SnapshotDigest), strings.TrimSpace(identity.SnapshotDigest)) ||
		record.Lease.LeaseID != identity.LeaseID {
		return errors.New("hot restart identity does not match the durable desired snapshot and generation journal")
	}
	if record.RuntimeGenerationID == "" || record.RuntimeGenerationID != identity.GenerationID {
		return errors.New("hot restart generation identity does not match the durable generation journal")
	}
	return nil
}

func (a *App) validateActiveHotRestartRuntime(identity hotrestart.Identity) error {
	if a == nil || a.runtime == nil {
		return errors.New("hot restart runtime is required")
	}
	active, managed := a.runtime.ActiveGenerationIdentity()
	if identity.Revision == 0 {
		if !managed || active.ID != identity.GenerationID || active.Revision != 0 ||
			!strings.EqualFold(active.SnapshotHash, identity.SnapshotDigest) {
			return errors.New("bootstrap hot restart runtime generation does not match the launch identity")
		}
		return nil
	}
	store, ok := a.store.(hotRestartJournalStore)
	if !ok {
		return errors.New("store does not expose the generation journal required for hot restart")
	}
	journal, err := store.LoadGenerationJournal()
	if err != nil {
		return err
	}
	record := matchingHotRestartRecord(journal, identity.Revision)
	if record == nil || !managed || active.ID != identity.GenerationID || active.Revision != identity.Revision ||
		!strings.EqualFold(active.SnapshotHash, record.RuntimeSnapshotHash) {
		return errors.New("hot restart runtime generation does not match the durable generation journal")
	}
	return nil
}

func (a *App) runControlLoop(ctx context.Context, startup Snapshot) error {
	if err := a.performSync(ctx); err != nil {
		if errors.Is(err, core.ErrRestartRequested) {
			return nil
		}
		if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
			return nil
		}
		if startup.DesiredVersion == "" && startup.Revision == 0 {
			return err
		}
	}

	if a.taskClient != nil {
		taskCtx, cancelTask := context.WithCancel(ctx)
		taskDone := make(chan struct{})
		a.taskRunMu.Lock()
		a.taskRunCancel = cancelTask
		a.taskRunDone = taskDone
		a.taskRunMu.Unlock()
		defer a.stopTaskClient()
		go func() {
			defer close(taskDone)
			if err := a.taskClient.Run(taskCtx); err != nil && taskCtx.Err() == nil {
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

func (a *App) stopTaskClient() {
	if a == nil {
		return
	}
	a.taskRunMu.Lock()
	cancel := a.taskRunCancel
	done := a.taskRunDone
	a.taskRunCancel = nil
	a.taskRunDone = nil
	a.taskRunMu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		select {
		case <-done:
		case <-timer.C:
			log.Printf("[agent] task client did not stop within 5s")
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

func (a *App) GenerationDrainSnapshot() model.GenerationDrainSnapshot {
	if a == nil || a.runtime == nil {
		return model.GenerationDrainSnapshot{}
	}
	snapshot, _ := a.runtime.GenerationDrainSnapshot()
	return snapshot
}

func (a *App) closeLocalRuntimes() {
	if a.generations != nil {
		_ = a.generations.Close(context.Background())
		a.generations = nil
	}
	if a.moduleRegistry != nil {
		_ = a.moduleRegistry.StopAll(context.Background())
		a.moduleRegistry = nil
	}
	if a.processStreams != nil {
		_ = a.processStreams.Close()
		a.processStreams = nil
	}
	if a.processPackets != nil {
		_ = a.processPackets.Close()
		a.processPackets = nil
	}
	if a.relayTimeoutReset != nil {
		a.relayTimeoutReset()
		a.relayTimeoutReset = nil
	}
}
