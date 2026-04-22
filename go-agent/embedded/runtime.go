package embedded

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"time"

	agentapp "github.com/sakullla/nginx-reverse-emby/go-agent/internal/app"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/config"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	agentstore "github.com/sakullla/nginx-reverse-emby/go-agent/internal/store"
)

type Snapshot = model.Snapshot
type RuntimeState = model.RuntimeState
type VersionPackage = model.VersionPackage
type HTTPHeader = model.HTTPHeader
type HTTPBackend = model.HTTPBackend
type LoadBalancing = model.LoadBalancing
type HTTPRule = model.HTTPRule
type L4Backend = model.L4Backend
type L4ProxyProtocolTuning = model.L4ProxyProtocolTuning
type L4Tuning = model.L4Tuning
type L4Rule = model.L4Rule
type RelayPin = model.RelayPin
type RelayListener = model.RelayListener
type ManagedCertificateBundle = model.ManagedCertificateBundle
type ManagedCertificateACMEInfo = model.ManagedCertificateACMEInfo
type ManagedCertificatePolicy = model.ManagedCertificatePolicy
type SyncRequest = agentapp.SyncRequest

type SyncSource interface {
	Sync(context.Context, SyncRequest) (Snapshot, error)
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
	HTTP3Enabled            bool
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
	app      embeddedAppRunner
	closeMu  sync.Mutex
	closed   bool
	closeErr error
}

const stateRootDir = "embedded-agent-state"

var newPersistentStore = func(dataDir string, sink StateSink) (agentstore.Store, error) {
	delegate, err := agentstore.NewFilesystem(filepath.Join(dataDir, stateRootDir))
	if err != nil {
		return nil, err
	}
	return &persistentBridgeStore{delegate: delegate, sink: sink}, nil
}

var newEmbeddedApp = func(cfg agentapp.Config, st agentstore.Store, client agentapp.SyncClient) (embeddedAppRunner, error) {
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

	runtimeApp, err := newEmbeddedApp(agentapp.Config{
		AgentID:           cfg.AgentID,
		AgentName:         cfg.AgentName,
		DataDir:           cfg.DataDir,
		HeartbeatInterval: cfg.HeartbeatInterval,
		CurrentVersion:    cfg.CurrentVersion,
		HTTP3Enabled:      cfg.HTTP3Enabled,
		HTTPTransport: config.HTTPTransportConfig{
			DialTimeout:           cfg.HTTPTransport.DialTimeout,
			TLSHandshakeTimeout:   cfg.HTTPTransport.TLSHandshakeTimeout,
			ResponseHeaderTimeout: cfg.HTTPTransport.ResponseHeaderTimeout,
			IdleConnTimeout:       cfg.HTTPTransport.IdleConnTimeout,
			KeepAlive:             cfg.HTTPTransport.KeepAlive,
			MaxConnsPerHost:       cfg.HTTPTransport.MaxConnsPerHost,
		},
		HTTPResilience: config.HTTPResilienceConfig{
			ResumeEnabled:            cfg.HTTPResilience.ResumeEnabled,
			ResumeMaxAttempts:        cfg.HTTPResilience.ResumeMaxAttempts,
			SameBackendRetryAttempts: cfg.HTTPResilience.SameBackendRetryAttempts,
		},
		BackendFailures: config.BackendFailureConfig{
			BackoffBase:  cfg.BackendFailures.BackoffBase,
			BackoffLimit: cfg.BackendFailures.BackoffLimit,
		},
		BackendFailuresExplicit: cfg.BackendFailuresExplicit,
		RelayTimeouts: config.RelayTimeoutConfig{
			DialTimeout:      cfg.RelayTimeouts.DialTimeout,
			HandshakeTimeout: cfg.RelayTimeouts.HandshakeTimeout,
			FrameTimeout:     cfg.RelayTimeouts.FrameTimeout,
			IdleTimeout:      cfg.RelayTimeouts.IdleTimeout,
		},
	}, persistentStore, syncClientAdapter{source: source})
	if err != nil {
		return nil, err
	}

	return &Runtime{app: runtimeApp}, nil
}

func (r *Runtime) Run(ctx context.Context) error {
	return r.app.Run(ctx)
}

func (r *Runtime) SyncNow(ctx context.Context) error {
	return r.app.SyncNow(ctx)
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
	source SyncSource
}

func (a syncClientAdapter) Sync(ctx context.Context, request agentapp.SyncRequest) (agentapp.Snapshot, error) {
	snapshot, err := a.source.Sync(ctx, SyncRequest(request))
	if err != nil {
		return Snapshot{}, err
	}
	return sanitizeSnapshot(snapshot), nil
}

type persistentBridgeStore struct {
	delegate agentstore.Store
	sink     StateSink
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
	if state.Metadata == nil {
		return copyValue
	}

	copyValue.Metadata = make(map[string]string, len(state.Metadata))
	for key, value := range state.Metadata {
		copyValue.Metadata[key] = value
	}
	return copyValue
}
