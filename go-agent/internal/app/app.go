package app

import (
	"context"
	"errors"
	"log"
	"os"
	stdruntime "runtime"
	"strings"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/certs"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/config"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/diagnostics"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/hosttraffic"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	platformlinux "github.com/sakullla/nginx-reverse-emby/go-agent/internal/platform/linux"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/relay"
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
	cfg                           Config
	syncClient                    SyncClient
	store                         store.Store
	httpApplier                   HTTPApplier
	certApplier                   CertificateApplier
	l4Applier                     L4Applier
	relayApplier                  RelayApplier
	updater                       Updater
	runtime                       *agentruntime.Runtime
	taskClient                    *agenttask.Client
	diagnosticHandler             *agenttask.DiagnosticHandler
	httpProber                    *diagnostics.HTTPProber
	tcpProber                     *diagnostics.TCPProber
	hostTrafficCollector          hostTrafficCollector
	wireGuardRuntime              *sharedWireGuardRuntime
	relayTimeoutReset             func()
	closeOnce                     sync.Once
	syncMu                        sync.Mutex
	pendingTrafficStatsReportUnix string
}

func advertisedCapabilities(cfg Config) []string {
	capabilities := []string{"http_rules", "cert_install", "local_acme", "l4", "relay_quic"}
	if cfg.HTTP3Enabled {
		capabilities = append(capabilities, "http3_ingress")
	}
	return capabilities
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

	return cfg
}

func New(cfg Config) (*App, error) {
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

	st, err := store.NewFilesystem(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	client := agentsync.NewClient(agentsync.ClientConfig{
		MasterURL:      cfg.MasterURL,
		AgentToken:     cfg.AgentToken,
		AgentID:        cfg.AgentID,
		AgentName:      cfg.AgentName,
		Capabilities:   advertisedCapabilities(cfg),
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
	certManager, err := certs.NewManager(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	executablePath, err := os.Executable()
	if err != nil {
		return nil, err
	}
	httpManager := newHTTPRuntimeManagerWithTLSAndHTTP3AndConfig(certManager, cfg.HTTP3Enabled, cfg)
	wireGuardRuntime := newSharedWireGuardRuntime()
	l4Manager := newL4RuntimeManagerWithRelayConfigAndWireGuard(certManager, cfg, wireGuardRuntime)
	httpProber, tcpProber := newRuntimeDiagnosticProbers(certManager, httpManager, l4Manager)
	diagnosticHandler := agenttask.NewDiagnosticHandler(st, httpProber, tcpProber)
	taskClient := agenttask.NewClient(agenttask.ClientConfig{
		MasterURL:     cfg.MasterURL,
		AgentToken:    cfg.AgentToken,
		AgentID:       cfg.AgentID,
		AgentName:     cfg.AgentName,
		Version:       cfg.CurrentVersion,
		Capabilities:  advertisedCapabilities(cfg),
		ReconnectWait: time.Second,
		HTTPTransport: cfg.HTTPTransport,
		Handler:       diagnosticHandler,
	})
	app := newAppWithAllDeps(
		cfg,
		st,
		client,
		httpManager,
		certManager,
		l4Manager,
		newRelayRuntimeManagerWithWireGuard(certManager, wireGuardRuntime),
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
	app.setDiagnostics(diagnosticHandler, httpProber, tcpProber)
	app.hostTrafficCollector = hosttraffic.NewCollector(cfg.TrafficInterfaces)
	app.wireGuardRuntime = wireGuardRuntime
	app.relayTimeoutReset = resetRelayTimeouts
	restoreRelayTimeouts = false
	return app, nil
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

func newRuntimeDiagnosticProbers(relayProvider relay.TLSMaterialProvider, httpApplier HTTPApplier, l4Applier L4Applier) (*diagnostics.HTTPProber, *diagnostics.TCPProber) {
	httpCfg := diagnostics.HTTPProberConfig{Attempts: 5, RelayProvider: relayProvider}
	if manager, ok := httpApplier.(*httpRuntimeManager); ok {
		httpCfg.Cache = manager.cache
	}
	tcpCfg := diagnostics.TCPProberConfig{Attempts: 5, RelayProvider: relayProvider}
	if manager, ok := l4Applier.(*l4RuntimeManager); ok {
		tcpCfg.Cache = manager.cache
	}
	return diagnostics.NewHTTPProber(httpCfg), diagnostics.NewTCPProber(tcpCfg)
}

func (a *App) setDiagnostics(handler *agenttask.DiagnosticHandler, httpProber *diagnostics.HTTPProber, tcpProber *diagnostics.TCPProber) {
	a.diagnosticHandler = handler
	a.httpProber = httpProber
	a.tcpProber = tcpProber
}

func (a *App) Diagnose(ctx context.Context, taskType string, ruleID int) (map[string]any, error) {
	if a == nil || a.diagnosticHandler == nil {
		return nil, errors.New("diagnostic handler is not configured")
	}
	return a.diagnosticHandler.HandleTask(ctx, agenttask.TaskMessage{
		TaskType:   taskType,
		RawPayload: map[string]any{"rule_id": ruleID},
	})
}

func (a *App) DiagnoseSnapshot(ctx context.Context, snapshot Snapshot, taskType string, ruleID int) (map[string]any, error) {
	if a == nil || a.httpProber == nil || a.tcpProber == nil {
		return nil, errors.New("diagnostic handler is not configured")
	}
	if err := a.applyManagedCertificates(ctx, snapshot); err != nil {
		return nil, err
	}
	mem := store.NewInMemory()
	if err := mem.SaveAppliedSnapshot(snapshot); err != nil {
		return nil, err
	}
	handler := agenttask.NewDiagnosticHandler(mem, a.httpProber, a.tcpProber)
	return handler.HandleTask(ctx, agenttask.TaskMessage{
		TaskType:   taskType,
		RawPayload: map[string]any{"rule_id": ruleID},
	})
}

func (a *App) Run(ctx context.Context) error {
	defer func() {
		_ = a.Close()
	}()

	applied, err := a.store.LoadAppliedSnapshot()
	if err != nil {
		return err
	}
	if err := a.runtime.Apply(ctx, Snapshot{}, applied); err != nil {
		log.Printf("[agent] startup runtime hydration error at revision %d: %v", applied.Revision, err)
		_ = a.recordRuntimeErrorWithRevision(err, applied.Revision)
	} else if err := a.persistTrafficStatsInterval(applied.AgentConfig.TrafficStatsInterval); err != nil {
		log.Printf("[agent] startup traffic stats interval hydration error at revision %d: %v", applied.Revision, err)
		_ = a.recordRuntimeErrorWithRevision(err, applied.Revision)
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
	req, err := a.syncRequest(ctx, applied)
	if err != nil {
		return err
	}
	return a.syncOnce(ctx, req)
}

func (a *App) SyncNow(ctx context.Context) error {
	return a.performSync(ctx)
}

func (a *App) closeLocalRuntimes() {
	if closer, ok := a.certApplier.(certCloser); ok {
		_ = closer.Close()
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
	if a.wireGuardRuntime != nil {
		_ = a.wireGuardRuntime.Close()
		a.wireGuardRuntime = nil
	}
	if a.relayTimeoutReset != nil {
		a.relayTimeoutReset()
		a.relayTimeoutReset = nil
	}
}

func (a *App) handlePendingUpdate(ctx context.Context, snapshot Snapshot) error {
	if !agentupdate.HasValidPackage(snapshot.VersionPackage) {
		return nil
	}
	desiredSHA := strings.TrimSpace(snapshot.VersionPackage.SHA256)
	if desiredSHA == "" {
		return nil
	}
	currentSHA := strings.TrimSpace(a.cfg.RuntimePackageSHA256)
	if currentSHA != "" && strings.EqualFold(currentSHA, desiredSHA) {
		return nil
	}
	if a.updater == nil {
		return a.recordRuntimeError(errors.New("updater unavailable"))
	}

	stagedPath, err := a.updater.Stage(ctx, *snapshot.VersionPackage)
	if err != nil {
		return a.recordRuntimeError(err)
	}
	if err := a.updater.Activate(stagedPath, snapshot.DesiredVersion); err != nil {
		if errors.Is(err, agentupdate.ErrRestartRequested) {
			return err
		}
		return a.recordRuntimeError(err)
	}
	return agentupdate.ErrRestartRequested
}
