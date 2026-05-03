package app

import (
	"context"
	"errors"
	"log"
	"os"
	"reflect"
	stdruntime "runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/certs"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/config"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/diagnostics"
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

const (
	runtimeMetaTrafficStatsInterval       = "traffic_stats_interval"
	runtimeMetaLastTrafficStatsReportUnix = "last_traffic_stats_report_unix"
)

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

type App struct {
	cfg               Config
	syncClient        SyncClient
	store             store.Store
	httpApplier       HTTPApplier
	certApplier       CertificateApplier
	l4Applier         L4Applier
	relayApplier      RelayApplier
	updater           Updater
	runtime           *agentruntime.Runtime
	taskClient        *agenttask.Client
	diagnosticHandler *agenttask.DiagnosticHandler
	httpProber        *diagnostics.HTTPProber
	tcpProber         *diagnostics.TCPProber
	relayTimeoutReset func()
	closeOnce         sync.Once
	syncMu            sync.Mutex
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
	l4Manager := newL4RuntimeManagerWithRelayAndConfig(certManager, cfg)
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
		newRelayRuntimeManager(certManager),
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

func (a *App) syncRequest(ctx context.Context, applied Snapshot) (SyncRequest, error) {
	req := SyncRequest{CurrentRevision: int(applied.Revision)}

	state, err := a.store.LoadRuntimeState()
	if err != nil {
		return SyncRequest{}, err
	}
	meta := ensureMetadata(state.Metadata)
	req.LastApplyRevision = int(parseInt64(meta["last_apply_revision"], applied.Revision))
	req.LastApplyStatus = strings.TrimSpace(meta["last_apply_status"])
	req.LastApplyMessage = meta["last_apply_message"]
	if req.LastApplyStatus == "" {
		req.LastApplyStatus = "success"
	}
	if !traffic.Enabled() {
		req.Stats = map[string]any{}
		req.StatsPresent = true
	} else if now := time.Now(); shouldReportTrafficStats(meta, now) {
		stats := traffic.SnapshotNonZero()
		if stats != nil {
			req.Stats = stats
			meta[runtimeMetaLastTrafficStatsReportUnix] = strconv.FormatInt(now.Unix(), 10)
			state.Metadata = meta
			if err := a.store.SaveRuntimeState(state); err != nil {
				return SyncRequest{}, err
			}
		}
	}

	if reporter, ok := a.certApplier.(ManagedCertificateReporter); ok {
		reports, err := reporter.ManagedCertificateReports(ctx)
		if err != nil {
			return SyncRequest{}, err
		}
		req.ManagedCertificateReports = reports
	}

	return req, nil
}

func shouldReportTrafficStats(meta map[string]string, now time.Time) bool {
	interval, err := time.ParseDuration(strings.TrimSpace(meta[runtimeMetaTrafficStatsInterval]))
	if err != nil || interval <= 0 {
		return true
	}
	lastReportUnix, err := strconv.ParseInt(strings.TrimSpace(meta[runtimeMetaLastTrafficStatsReportUnix]), 10, 64)
	if err != nil || lastReportUnix <= 0 {
		return true
	}
	return !now.Before(time.Unix(lastReportUnix, 0).Add(interval))
}

func (a *App) persistTrafficStatsInterval(raw string) error {
	interval := strings.TrimSpace(raw)
	state, err := a.store.LoadRuntimeState()
	if err != nil {
		return err
	}
	state.Metadata = ensureMetadata(state.Metadata)
	if interval == "" {
		delete(state.Metadata, runtimeMetaTrafficStatsInterval)
		return a.store.SaveRuntimeState(state)
	}
	if _, err := time.ParseDuration(interval); err != nil {
		return err
	}
	state.Metadata[runtimeMetaTrafficStatsInterval] = interval
	return a.store.SaveRuntimeState(state)
}

func (a *App) syncOnce(ctx context.Context, req SyncRequest) error {
	snapshot, err := a.syncClient.Sync(ctx, req)
	if err != nil {
		log.Printf("[agent] sync error: %v", err)
		return a.recordRuntimeError(err)
	}
	existingDesired, err := a.store.LoadDesiredSnapshot()
	if err != nil {
		return a.recordRuntimeError(err)
	}
	persistedSnapshot := mergeSnapshotPayload(snapshot, existingDesired)
	if err := a.store.SaveDesiredSnapshot(persistedSnapshot); err != nil {
		return a.recordRuntimeError(err)
	}
	if err := a.handlePendingUpdate(ctx, persistedSnapshot); err != nil {
		return err
	}
	previousApplied := a.runtime.ActiveSnapshot()
	candidateApplied := mergeSnapshotPayload(snapshot, previousApplied)
	if err := a.runtime.Apply(ctx, previousApplied, candidateApplied); err != nil {
		log.Printf("[agent] runtime apply error at revision %d: %v", candidateApplied.Revision, err)
		a.rollbackRuntime(ctx, candidateApplied, previousApplied)
		return a.recordRuntimeErrorWithRevision(err, candidateApplied.Revision)
	}
	if err := a.store.SaveAppliedSnapshot(candidateApplied); err != nil {
		log.Printf("[agent] save applied snapshot error at revision %d: %v", candidateApplied.Revision, err)
		a.rollbackRuntime(ctx, candidateApplied, previousApplied)
		return a.recordPersistedRuntimeErrorWithRevision(err, candidateApplied.Revision)
	}
	if err := a.persistRuntimeState(true); err != nil {
		a.rollbackRuntime(ctx, candidateApplied, previousApplied)
		_ = a.store.SaveAppliedSnapshot(previousApplied)
		return a.recordPersistedRuntimeErrorWithRevision(err, candidateApplied.Revision)
	}
	return nil
}

func (a *App) recordRuntimeError(syncErr error) error {
	return a.recordRuntimeErrorWithRevision(syncErr, a.runtime.ActiveSnapshot().Revision)
}

func (a *App) recordRuntimeErrorWithRevision(syncErr error, revision int64) error {
	state, err := a.runtimeStateForPersistence()
	if err != nil {
		return syncErr
	}
	state.Metadata = ensureMetadata(state.Metadata)
	state.Metadata["last_sync_error"] = syncErr.Error()
	setApplyMetadata(state.Metadata, revision, "error", syncErr.Error())
	if err := a.store.SaveRuntimeState(state); err != nil {
		return syncErr
	}
	return syncErr
}

func (a *App) persistRuntimeState(clearLastSyncError bool) error {
	state, err := a.runtimeStateForPersistence()
	if err != nil {
		return err
	}
	state.Metadata = ensureMetadata(state.Metadata)
	setApplyMetadata(state.Metadata, a.runtime.ActiveSnapshot().Revision, "success", "")
	if clearLastSyncError {
		delete(state.Metadata, "last_sync_error")
	}
	if err := a.store.SaveRuntimeState(state); err != nil {
		return err
	}
	return nil
}

func (a *App) recordPersistedRuntimeError(syncErr error) error {
	state, err := a.store.LoadRuntimeState()
	if err != nil {
		return syncErr
	}
	currentRevision := parseInt64(state.Metadata["current_revision"], state.CurrentRevision)
	return a.recordPersistedRuntimeErrorWithRevision(syncErr, currentRevision)
}

func (a *App) recordPersistedRuntimeErrorWithRevision(syncErr error, revision int64) error {
	state, err := a.store.LoadRuntimeState()
	if err != nil {
		return syncErr
	}
	state.Metadata = ensureMetadata(state.Metadata)
	state.Metadata["last_sync_error"] = syncErr.Error()
	setApplyMetadata(state.Metadata, revision, "error", syncErr.Error())
	if err := a.store.SaveRuntimeState(state); err != nil {
		return syncErr
	}
	return syncErr
}

func ensureMetadata(meta map[string]string) map[string]string {
	if meta == nil {
		return make(map[string]string)
	}
	return meta
}

func setApplyMetadata(meta map[string]string, revision int64, status string, message string) {
	meta["last_apply_revision"] = strconv.FormatInt(revision, 10)
	meta["last_apply_status"] = status
	meta["last_apply_message"] = message
}

func parseInt64(raw string, fallback int64) int64 {
	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return fallback
	}
	return value
}

func (a *App) runtimeStateForPersistence() (store.RuntimeState, error) {
	existing, err := a.store.LoadRuntimeState()
	if err != nil {
		return store.RuntimeState{}, err
	}

	current := a.runtime.State()
	state := existing
	state.Status = current.Status
	state.CurrentRevision = current.CurrentRevision
	state.Metadata = ensureMetadata(existing.Metadata)
	for key, value := range current.Metadata {
		state.Metadata[key] = value
	}
	return state, nil
}

func (a *App) applyManagedCertificates(ctx context.Context, snapshot Snapshot) error {
	if a.certApplier == nil {
		return nil
	}
	if snapshot.Certificates == nil && snapshot.CertificatePolicies == nil {
		return nil
	}
	return a.certApplier.Apply(ctx, snapshot.Certificates, snapshot.CertificatePolicies)
}

func (a *App) applyHTTPRules(ctx context.Context, snapshot Snapshot) error {
	if a.httpApplier == nil || snapshot.Rules == nil {
		return nil
	}
	if relayAware, ok := a.httpApplier.(HTTPRelayAwareApplier); ok {
		return relayAware.ApplyWithRelay(ctx, snapshot.Rules, snapshot.RelayListeners)
	}
	return a.httpApplier.Apply(ctx, snapshot.Rules)
}

func mergeSnapshotPayload(next, previous Snapshot) Snapshot {
	merged := next
	if next.VersionPackage == nil {
		merged.VersionPackage = previous.VersionPackage
	}
	if !next.HasAgentConfig() {
		merged.AgentConfig = previous.AgentConfig
	}
	if next.Rules == nil {
		merged.Rules = previous.Rules
	}
	if next.L4Rules == nil {
		merged.L4Rules = previous.L4Rules
	}
	if next.RelayListeners == nil {
		merged.RelayListeners = previous.RelayListeners
	}
	if next.Certificates == nil {
		merged.Certificates = previous.Certificates
	}
	if next.CertificatePolicies == nil {
		merged.CertificatePolicies = previous.CertificatePolicies
	}
	return merged
}

func (a *App) rollbackRuntime(ctx context.Context, previousApplied, targetApplied Snapshot) {
	if reflect.DeepEqual(previousApplied, targetApplied) {
		return
	}
	_ = a.runtime.Rollback(ctx, previousApplied, targetApplied)
}

func (a *App) applyL4Rules(ctx context.Context, snapshot Snapshot) error {
	if a.l4Applier == nil || snapshot.L4Rules == nil {
		return nil
	}
	if relayAware, ok := a.l4Applier.(L4RelayAwareApplier); ok {
		return relayAware.ApplyWithRelay(ctx, snapshot.L4Rules, snapshot.RelayListeners)
	}
	return a.l4Applier.Apply(ctx, snapshot.L4Rules)
}

func (a *App) applyRelayListeners(ctx context.Context, snapshot Snapshot) error {
	if a.relayApplier == nil || snapshot.RelayListeners == nil {
		return nil
	}
	return a.relayApplier.Apply(ctx, localRelayListeners(snapshot.RelayListeners, a.cfg.AgentID, a.cfg.AgentName))
}

func (a *App) snapshotActivator() agentruntime.Activator {
	handlers := a.snapshotActivationHandlers()
	certActivator := agentruntime.NewSnapshotActivator(agentruntime.SnapshotActivationHandlers{
		ActivateManagedCertificates: handlers.ActivateManagedCertificates,
	})
	configActivator := agentruntime.NewSnapshotActivator(agentruntime.SnapshotActivationHandlers{
		ActivateAgentConfig: handlers.ActivateAgentConfig,
	})
	rulesActivator := agentruntime.NewSnapshotActivator(agentruntime.SnapshotActivationHandlers{
		ActivateHTTPRules: handlers.ActivateHTTPRules,
		ActivateL4Rules:   handlers.ActivateL4Rules,
	})
	relayActivator := agentruntime.NewSnapshotActivator(agentruntime.SnapshotActivationHandlers{
		ActivateRelayListeners: handlers.ActivateRelayListeners,
	})

	return func(ctx context.Context, previous, next model.Snapshot) error {
		if err := certActivator(ctx, previous, next); err != nil {
			return err
		}
		if err := configActivator(ctx, previous, next); err != nil {
			return err
		}

		localPrevious := previous
		localPrevious.RelayListeners = localRelayListeners(previous.RelayListeners, a.cfg.AgentID, a.cfg.AgentName)
		localNext := next
		localNext.RelayListeners = localRelayListeners(next.RelayListeners, a.cfg.AgentID, a.cfg.AgentName)

		if err := relayActivator(ctx, localPrevious, localNext); err != nil {
			return err
		}

		return rulesActivator(ctx, previous, next)
	}
}

func (a *App) snapshotActivationHandlers() agentruntime.SnapshotActivationHandlers {
	return agentruntime.SnapshotActivationHandlers{
		ActivateAgentConfig: func(_ context.Context, cfg model.AgentConfig) error {
			relay.SetOutboundProxyURL(cfg.OutboundProxyURL)
			return a.persistTrafficStatsInterval(cfg.TrafficStatsInterval)
		},
		ActivateManagedCertificates: func(ctx context.Context, bundles []model.ManagedCertificateBundle, policies []model.ManagedCertificatePolicy) error {
			return a.applyManagedCertificates(ctx, Snapshot{
				Certificates:        bundles,
				CertificatePolicies: policies,
			})
		},
		ActivateHTTPRules: func(ctx context.Context, rules []model.HTTPRule, relayListeners []model.RelayListener) error {
			return a.applyHTTPRules(ctx, Snapshot{
				Rules:          rules,
				RelayListeners: relayListeners,
			})
		},
		ActivateRelayListeners: func(ctx context.Context, relayListeners []model.RelayListener) error {
			return a.applyRelayListeners(ctx, Snapshot{
				RelayListeners: relayListeners,
			})
		},
		ActivateL4Rules: func(ctx context.Context, rules []model.L4Rule, relayListeners []model.RelayListener) error {
			return a.applyL4Rules(ctx, Snapshot{
				L4Rules:        rules,
				RelayListeners: relayListeners,
			})
		},
	}
}

func localRelayListeners(listeners []model.RelayListener, agentID, agentName string) []model.RelayListener {
	if listeners == nil {
		return nil
	}
	identity := strings.TrimSpace(agentID)
	fallback := strings.TrimSpace(agentName)
	if identity == "" && fallback == "" {
		return listeners
	}
	filtered := make([]model.RelayListener, 0, len(listeners))
	for _, listener := range listeners {
		if listener.AgentID == identity || (identity == "" && listener.AgentID == fallback) || listener.AgentID == fallback {
			filtered = append(filtered, listener)
		}
	}
	return filtered
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
