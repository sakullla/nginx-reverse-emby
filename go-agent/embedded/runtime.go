package embedded

import (
	"context"
	"errors"
	"sync"
	"time"

	agentapp "github.com/sakullla/nginx-reverse-emby/go-agent/internal/app"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
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
	AgentID           string
	AgentName         string
	DataDir           string
	CurrentVersion    string
	HeartbeatInterval time.Duration
}

type Runtime struct {
	app *agentapp.App
}

func New(cfg Config, source SyncSource, sink StateSink) (*Runtime, error) {
	if source == nil {
		return nil, errors.New("sync source is required")
	}
	if sink == nil {
		return nil, errors.New("state sink is required")
	}

	runtimeApp, err := agentapp.NewEmbedded(agentapp.Config{
		AgentID:           cfg.AgentID,
		AgentName:         cfg.AgentName,
		DataDir:           cfg.DataDir,
		HeartbeatInterval: cfg.HeartbeatInterval,
		CurrentVersion:    cfg.CurrentVersion,
	}, newBridgeStore(sink), syncClientAdapter{source: source})
	if err != nil {
		return nil, err
	}

	return &Runtime{app: runtimeApp}, nil
}

func (r *Runtime) Run(ctx context.Context) error {
	return r.app.Run(ctx)
}

type syncClientAdapter struct {
	source SyncSource
}

func (a syncClientAdapter) Sync(ctx context.Context, request agentapp.SyncRequest) (agentapp.Snapshot, error) {
	return a.source.Sync(ctx, SyncRequest(request))
}

type bridgeStore struct {
	mu      sync.RWMutex
	desired Snapshot
	applied Snapshot
	runtime RuntimeState
	sink    StateSink
}

func newBridgeStore(sink StateSink) *bridgeStore {
	return &bridgeStore{sink: sink}
}

func (s *bridgeStore) SaveDesiredSnapshot(snapshot Snapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.desired = snapshot
	return nil
}

func (s *bridgeStore) LoadDesiredSnapshot() (Snapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.desired, nil
}

func (s *bridgeStore) SaveAppliedSnapshot(snapshot Snapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.applied = snapshot
	return nil
}

func (s *bridgeStore) LoadAppliedSnapshot() (Snapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.applied, nil
}

func (s *bridgeStore) SaveRuntimeState(state RuntimeState) error {
	if err := s.sink.Save(context.Background(), copyRuntimeState(state)); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.runtime = copyRuntimeState(state)
	return nil
}

func (s *bridgeStore) LoadRuntimeState() (RuntimeState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return copyRuntimeState(s.runtime), nil
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
