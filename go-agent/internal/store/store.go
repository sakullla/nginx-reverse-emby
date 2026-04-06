package store

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
	mu      stdsync.RWMutex
	desired Snapshot
	applied Snapshot
	runtime RuntimeState
}

func NewInMemory() *InMemory {
	return &InMemory{}
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
	s.runtime = state
	return nil
}

func (s *InMemory) LoadRuntimeState() (RuntimeState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.runtime, nil
}

func (s *InMemory) SaveSnapshot(snapshot Snapshot) error {
	return s.SaveDesiredSnapshot(snapshot)
}

func (s *InMemory) LoadSnapshot() (Snapshot, error) {
	return s.LoadDesiredSnapshot()
}
