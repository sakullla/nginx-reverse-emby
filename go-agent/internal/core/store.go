package core

import (
	"context"
	stdsync "sync"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	pluginprocess "github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/process"
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
	mu          stdsync.RWMutex
	desired     Snapshot
	applied     Snapshot
	runtime     RuntimeState
	pluginLogs  pluginLogOutboxState
	logCapacity pluginLogCapacitySignal
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
	_, _, changed, err := s.pluginLogs.acknowledge(sent)
	s.mu.Unlock()
	if changed {
		s.logCapacity.notify()
	}
	return err
}

func (s *InMemory) WaitForPluginLogCapacity(ctx context.Context) error {
	return waitPluginLogCapacity(ctx, &s.logCapacity, func() (int, error) {
		s.mu.RLock()
		defer s.mu.RUnlock()
		return len(s.pluginLogs.pending), nil
	})
}

func (s *InMemory) RetirePluginRuntimeLogFence(identity pluginprocess.RuntimeLogIdentity) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _, err := s.pluginLogs.retire(identity)
	return err
}
