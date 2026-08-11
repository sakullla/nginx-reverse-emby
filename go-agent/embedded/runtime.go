package embedded

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"time"

	agentapp "github.com/sakullla/nginx-reverse-emby/go-agent/internal/app"
	agentcore "github.com/sakullla/nginx-reverse-emby/go-agent/internal/core"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	modulepki "github.com/sakullla/nginx-reverse-emby/go-agent/internal/modules/pki"
)

type Snapshot = model.Snapshot
type PolicyRef = model.PolicyRef
type PluginPolicy = model.PluginPolicy
type PluginGeneration = model.PluginGeneration
type PluginDependencyEdge = model.PluginDependencyEdge
type PluginDependencyConsumer = model.PluginDependencyConsumer
type PluginDependencyTarget = model.PluginDependencyTarget
type PluginRuntimeStatus = model.PluginRuntimeStatus
type PluginRuntimeLogEntry = model.PluginRuntimeLogEntry
type PluginRuntimeLogReport = model.PluginRuntimeLogReport
type PluginGenerationSecretHandle = model.PluginGenerationSecretHandle
type PluginSecretRedemptionRequest = model.PluginSecretRedemptionRequest
type PluginRedeemedSecret = model.PluginRedeemedSecret
type RuntimeState = model.RuntimeState
type AgentConfig = model.AgentConfig
type VersionPackage = model.VersionPackage
type DDNSExtractConfig = model.DDNSExtractConfig
type DDNSFamily = model.DDNSFamily
type GenerationDrainStatus = model.GenerationDrainStatus
type GenerationDrainSnapshot = model.GenerationDrainSnapshot
type HTTPHeader = model.HTTPHeader
type HTTPBackend = model.HTTPBackend
type LoadBalancing = model.LoadBalancing
type HTTPRule = model.HTTPRule
type L4Backend = model.L4Backend
type L4ProxyProtocolTuning = model.L4ProxyProtocolTuning
type L4ProxyEntryAuth = model.L4ProxyEntryAuth
type L4Tuning = model.L4Tuning
type L4Rule = model.L4Rule
type RelayPin = model.RelayPin
type RelayListener = model.RelayListener
type ManagedCertificateBundle = model.ManagedCertificateBundle
type ManagedCertificateACMEInfo = model.ManagedCertificateACMEInfo
type ManagedCertificatePolicy = model.ManagedCertificatePolicy
type PKISecurityAcknowledgement = model.PKISecurityAcknowledgement
type PKITrustRoot = model.PKITrustRoot
type PKISecuritySnapshot = model.PKISecuritySnapshot
type PKITunnelCredential = model.PKITunnelCredential
type PKIEnrollmentRequest = model.PKIEnrollmentRequest
type SyncRequest = agentapp.SyncRequest

const (
	GenerationDrainStateApplied = model.GenerationDrainStateApplied
	GenerationDrainStateDrained = model.GenerationDrainStateDrained
	GenerationDrainStateForced  = model.GenerationDrainStateForced
)

type SyncSource interface {
	Sync(context.Context, SyncRequest) (Snapshot, error)
}

type PluginSecretSource interface {
	RedeemPluginSecrets(context.Context, PluginSecretRedemptionRequest) ([]PluginRedeemedSecret, error)
}

type StateSink interface {
	Save(context.Context, RuntimeState) error
}

type Config struct {
	AgentID                 string
	AgentName               string
	DataDir                 string
	CurrentVersion          string
	HeartbeatInterval       time.Duration
	DDNSIPProbeInterval     time.Duration
	HTTP3Enabled            bool
	TrafficStatsEnabled     bool
	TrafficStatsExplicit    bool
	HTTPTransport           HTTPTransportConfig
	HTTPResilience          HTTPResilienceConfig
	BackendFailures         BackendFailureConfig
	BackendFailuresExplicit bool
	RelayTimeouts           RelayTimeoutConfig
}

type HTTPTransportConfig struct {
	DialTimeout           time.Duration
	TLSHandshakeTimeout   time.Duration
	ResponseHeaderTimeout time.Duration
	IdleConnTimeout       time.Duration
	KeepAlive             time.Duration
	MaxConnsPerHost       int
}

type HTTPResilienceConfig struct {
	ResumeEnabled            bool
	ResumeMaxAttempts        int
	SameBackendRetryAttempts int
}

type BackendFailureConfig struct {
	BackoffBase  time.Duration
	BackoffLimit time.Duration
}

type RelayTimeoutConfig struct {
	DialTimeout      time.Duration
	HandshakeTimeout time.Duration
	FrameTimeout     time.Duration
	IdleTimeout      time.Duration
}

type Runtime struct {
	app         embeddedAppRunner
	ready       <-chan struct{}
	credentials *CredentialStore
	closeMu     sync.Mutex
	closed      bool
	closeErr    error
}

const stateRootDir = "embedded-agent-state"

var newPersistentStore = func(dataDir string, sink StateSink) (agentcore.Store, error) {
	delegate, err := agentcore.NewFilesystem(filepath.Join(dataDir, stateRootDir))
	if err != nil {
		return nil, err
	}
	return &persistentBridgeStore{delegate: delegate, sink: sink}, nil
}

var newEmbeddedApp = func(cfg agentapp.Config, st agentcore.Store, client agentapp.SyncClient) (embeddedAppRunner, error) {
	return agentapp.NewEmbedded(cfg, st, client)
}

type embeddedAppRunner interface {
	Run(context.Context) error
	SyncNow(context.Context) error
	Close() error
}

func New(cfg Config, source SyncSource, sink StateSink) (*Runtime, error) {
	if source == nil {
		return nil, errors.New("sync source is required")
	}
	if sink == nil {
		return nil, errors.New("state sink is required")
	}

	persistentStore, err := newPersistentStore(cfg.DataDir, sink)
	if err != nil {
		return nil, err
	}
	credentialStore, err := modulepki.NewStore(filepath.Join(cfg.DataDir, stateRootDir))
	if err != nil {
		return nil, err
	}

	ready := make(chan struct{})
	var readyOnce sync.Once
	runtimeApp, err := newEmbeddedApp(agentapp.Config{
		AgentID:           cfg.AgentID,
		AgentName:         cfg.AgentName,
		DataDir:           cfg.DataDir,
		HeartbeatInterval: cfg.HeartbeatInterval,
		DDNS: model.DDNSRuntimeConfig{
			IPProbeInterval: cfg.DDNSIPProbeInterval,
		},
		CurrentVersion:       cfg.CurrentVersion,
		HTTP3Enabled:         cfg.HTTP3Enabled,
		TrafficStatsEnabled:  cfg.TrafficStatsEnabled,
		TrafficStatsExplicit: cfg.TrafficStatsExplicit,
		HTTPTransport: model.HTTPTransportConfig{
			DialTimeout:           cfg.HTTPTransport.DialTimeout,
			TLSHandshakeTimeout:   cfg.HTTPTransport.TLSHandshakeTimeout,
			ResponseHeaderTimeout: cfg.HTTPTransport.ResponseHeaderTimeout,
			IdleConnTimeout:       cfg.HTTPTransport.IdleConnTimeout,
			KeepAlive:             cfg.HTTPTransport.KeepAlive,
			MaxConnsPerHost:       cfg.HTTPTransport.MaxConnsPerHost,
		},
		HTTPResilience: model.HTTPResilienceConfig{
			ResumeEnabled:            cfg.HTTPResilience.ResumeEnabled,
			ResumeMaxAttempts:        cfg.HTTPResilience.ResumeMaxAttempts,
			SameBackendRetryAttempts: cfg.HTTPResilience.SameBackendRetryAttempts,
		},
		BackendFailures: model.BackendFailureConfig{
			BackoffBase:  cfg.BackendFailures.BackoffBase,
			BackoffLimit: cfg.BackendFailures.BackoffLimit,
		},
		BackendFailuresExplicit: cfg.BackendFailuresExplicit,
		RelayTimeouts: model.RelayTimeoutConfig{
			DialTimeout:      cfg.RelayTimeouts.DialTimeout,
			HandshakeTimeout: cfg.RelayTimeouts.HandshakeTimeout,
			FrameTimeout:     cfg.RelayTimeouts.FrameTimeout,
			IdleTimeout:      cfg.RelayTimeouts.IdleTimeout,
		},
	}, persistentStore, syncClientAdapter{
		source:   source,
		pkiStore: credentialStore,
		onSync: func() {
			readyOnce.Do(func() { close(ready) })
		},
	})
	if err != nil {
		return nil, err
	}

	return &Runtime{app: runtimeApp, ready: ready, credentials: &CredentialStore{delegate: credentialStore}}, nil
}

func (r *Runtime) Run(ctx context.Context) error {
	return r.app.Run(ctx)
}

func (r *Runtime) SyncNow(ctx context.Context) error {
	return r.app.SyncNow(ctx)
}

func (r *Runtime) GenerationDrainSnapshot() GenerationDrainSnapshot {
	if r == nil || r.app == nil {
		return GenerationDrainSnapshot{}
	}
	reader, ok := r.app.(interface {
		GenerationDrainSnapshot() model.GenerationDrainSnapshot
	})
	if !ok {
		return GenerationDrainSnapshot{}
	}
	return reader.GenerationDrainSnapshot()
}

// ApplyRevision is the only embedded sync path allowed to advance runtime
// configuration. Periodic syncs still publish telemetry, but replay the
// currently applied revision until the coordinator supplies an approved snapshot.
func (r *Runtime) ApplyRevision(ctx context.Context, snapshot Snapshot) error {
	return r.ApplyRevisionWithDrainTimeout(ctx, snapshot, 0)
}

func (r *Runtime) ApplyRevisionWithDrainTimeout(ctx context.Context, snapshot Snapshot, drainTimeout time.Duration) error {
	if r == nil || r.app == nil {
		return errors.New("embedded runtime is not initialized")
	}
	if snapshot.Revision <= 0 {
		return errors.New("approved snapshot revision must be positive")
	}
	if r.ready != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-r.ready:
		}
	}
	applyCtx := context.WithValue(ctx, approvedRevisionContextKey{}, sanitizeSnapshot(snapshot))
	applyCtx = agentcore.WithRevisionDrainTimeout(applyCtx, drainTimeout)
	return r.app.SyncNow(applyCtx)
}

func (r *Runtime) DiagnoseSnapshot(ctx context.Context, snapshot Snapshot, req DiagnosticRequest) (map[string]any, error) {
	if r == nil || r.app == nil {
		return nil, errors.New("embedded runtime is not initialized")
	}
	diagnoser, ok := r.app.(interface {
		DiagnoseSnapshot(context.Context, agentapp.Snapshot, string, int) (map[string]any, error)
	})
	if !ok {
		return nil, errors.New("embedded runtime diagnostics are not available")
	}
	return diagnoser.DiagnoseSnapshot(ctx, sanitizeSnapshot(snapshot), req.TaskType, req.RuleID)
}

func (r *Runtime) Close() error {
	if r == nil || r.app == nil {
		return nil
	}
	r.closeMu.Lock()
	defer r.closeMu.Unlock()
	if r.closed {
		return r.closeErr
	}

	r.closeErr = r.app.Close()
	r.closed = true
	return r.closeErr
}

type syncClientAdapter struct {
	source   SyncSource
	onSync   func()
	pkiStore *modulepki.Store
}

func (a syncClientAdapter) EmbeddedTunnelPKIStore() *modulepki.Store {
	return a.pkiStore
}

func (a syncClientAdapter) Sync(ctx context.Context, request agentapp.SyncRequest) (agentapp.Snapshot, error) {
	_, err := a.source.Sync(ctx, SyncRequest(request))
	if a.onSync != nil {
		a.onSync()
	}
	if err != nil {
		return Snapshot{}, err
	}
	if snapshot, ok := ctx.Value(approvedRevisionContextKey{}).(Snapshot); ok {
		return sanitizeSnapshot(snapshot), nil
	}
	return Snapshot{Revision: int64(request.CurrentRevision)}, nil
}

func (a syncClientAdapter) RedeemPluginSecrets(ctx context.Context, request model.PluginSecretRedemptionRequest) ([]model.PluginRedeemedSecret, error) {
	source, ok := a.source.(PluginSecretSource)
	if !ok {
		return nil, errors.New("embedded plugin secret redemption is unavailable")
	}
	return source.RedeemPluginSecrets(ctx, request)
}

type approvedRevisionContextKey struct{}

type persistentBridgeStore struct {
	delegate agentcore.Store
	sink     StateSink
}

func (s *persistentBridgeStore) EnqueuePluginLogReports(batchID string, reports []model.PluginRuntimeLogReport) ([]model.PluginRuntimeLogReport, error) {
	outbox, ok := s.delegate.(agentcore.PluginLogOutboxStore)
	if !ok {
		return nil, errors.New("embedded plugin log outbox is unavailable")
	}
	return outbox.EnqueuePluginLogReports(batchID, reports)
}

func (s *persistentBridgeStore) PendingPluginLogReports() ([]model.PluginRuntimeLogReport, error) {
	outbox, ok := s.delegate.(agentcore.PluginLogOutboxStore)
	if !ok {
		return nil, errors.New("embedded plugin log outbox is unavailable")
	}
	return outbox.PendingPluginLogReports()
}

func (s *persistentBridgeStore) AcknowledgePluginLogReports(reports []model.PluginRuntimeLogReport) error {
	outbox, ok := s.delegate.(agentcore.PluginLogOutboxStore)
	if !ok {
		return errors.New("embedded plugin log outbox is unavailable")
	}
	return outbox.AcknowledgePluginLogReports(reports)
}

func (s *persistentBridgeStore) SaveDesiredSnapshot(snapshot Snapshot) error {
	return s.delegate.SaveDesiredSnapshot(sanitizeSnapshot(snapshot))
}

func (s *persistentBridgeStore) LoadDesiredSnapshot() (Snapshot, error) {
	snapshot, err := s.delegate.LoadDesiredSnapshot()
	if err != nil {
		return Snapshot{}, err
	}
	return sanitizeSnapshot(snapshot), nil
}

func (s *persistentBridgeStore) SaveAppliedSnapshot(snapshot Snapshot) error {
	return s.delegate.SaveAppliedSnapshot(sanitizeSnapshot(snapshot))
}

func (s *persistentBridgeStore) LoadAppliedSnapshot() (Snapshot, error) {
	snapshot, err := s.delegate.LoadAppliedSnapshot()
	if err != nil {
		return Snapshot{}, err
	}
	return sanitizeSnapshot(snapshot), nil
}

func (s *persistentBridgeStore) SaveRuntimeState(state RuntimeState) error {
	persisted := copyRuntimeState(state)
	if err := s.delegate.SaveRuntimeState(persisted); err != nil {
		return err
	}
	return s.sink.Save(context.Background(), persisted)
}

func (s *persistentBridgeStore) LoadRuntimeState() (RuntimeState, error) {
	state, err := s.delegate.LoadRuntimeState()
	if err != nil {
		return RuntimeState{}, err
	}
	return copyRuntimeState(state), nil
}

func sanitizeSnapshot(snapshot Snapshot) Snapshot {
	copyValue := snapshot
	copyValue.DesiredVersion = ""
	copyValue.VersionPackage = nil
	return copyValue
}

func copyRuntimeState(state RuntimeState) RuntimeState {
	copyValue := state
	copyValue.Metadata = cloneRuntimeMetadata(state.Metadata)
	copyValue.PluginStatuses = append([]model.PluginRuntimeStatus(nil), state.PluginStatuses...)
	for index := range copyValue.PluginStatuses {
		copyValue.PluginStatuses[index].Details = append([]byte(nil), state.PluginStatuses[index].Details...)
		copyValue.PluginStatuses[index].Budget = append([]byte(nil), state.PluginStatuses[index].Budget...)
	}
	return copyValue
}

func cloneRuntimeMetadata(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}
