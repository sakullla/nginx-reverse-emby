package core

import (
	stdsync "sync"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

type Snapshot = model.Snapshot
type RuntimeState = model.RuntimeState

type Store interface {
	SaveDesiredSnapshot(snapshot Snapshot) error
	LoadDesiredSnapshot() (Snapshot, error)
	SaveAppliedSnapshot(snapshot Snapshot) error
	LoadAppliedSnapshot() (Snapshot, error)
	SaveRuntimeState(state RuntimeState) error
	LoadRuntimeState() (RuntimeState, error)
}

type InMemory struct {
	mu         stdsync.RWMutex
	desired    Snapshot
	applied    Snapshot
	runtime    RuntimeState
	pluginLogs pluginLogOutboxState
}

func NewInMemory() *InMemory {
	return &InMemory{pluginLogs: newPluginLogOutboxState()}
}

func (s *InMemory) SaveDesiredSnapshot(snapshot Snapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.desired = snapshot
	return nil
}

func (s *InMemory) LoadDesiredSnapshot() (Snapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.desired, nil
}

func (s *InMemory) SaveAppliedSnapshot(snapshot Snapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.applied = snapshot
	return nil
}

func (s *InMemory) LoadAppliedSnapshot() (Snapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.applied, nil
}

func (s *InMemory) SaveRuntimeState(state RuntimeState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	copyState := state
	copyState.Metadata = cloneStringMap(state.Metadata)
	copyState.PluginStatuses = clonePluginRuntimeStatuses(state.PluginStatuses)
	copyState.PluginLogReports = model.ClonePluginRuntimeLogReports(state.PluginLogReports)
	s.runtime = copyState
	return nil
}

func (s *InMemory) LoadRuntimeState() (RuntimeState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := s.runtime
	result.Metadata = cloneStringMap(result.Metadata)
	result.PluginStatuses = clonePluginRuntimeStatuses(result.PluginStatuses)
	result.PluginLogReports = model.ClonePluginRuntimeLogReports(result.PluginLogReports)
	return result, nil
}

func (s *InMemory) SaveSnapshot(snapshot Snapshot) error {
	return s.SaveDesiredSnapshot(snapshot)
}

func (s *InMemory) LoadSnapshot() (Snapshot, error) {
	return s.LoadDesiredSnapshot()
}

func (s *InMemory) EnqueuePluginLogReports(batchID string, drafts []model.PluginRuntimeLogReport) ([]model.PluginRuntimeLogReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	assigned, _, err := s.pluginLogs.enqueue(batchID, drafts)
	return model.ClonePluginRuntimeLogReports(assigned), err
}

func (s *InMemory) PendingPluginLogReports() ([]model.PluginRuntimeLogReport, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return model.ClonePluginRuntimeLogReports(s.pluginLogs.pending), nil
}

func (s *InMemory) AcknowledgePluginLogReports(sent []model.PluginRuntimeLogReport) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _, _, err := s.pluginLogs.acknowledge(sent)
	return err
}
