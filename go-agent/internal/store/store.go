package store

import (
	stdsync "sync"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

type Snapshot = model.Snapshot

type Store interface {
	SaveSnapshot(snapshot Snapshot) error
	LoadSnapshot() (Snapshot, error)
}

type InMemory struct {
	mu       stdsync.RWMutex
	snapshot Snapshot
}

func NewInMemory() *InMemory {
	return &InMemory{}
}

func (s *InMemory) SaveSnapshot(snapshot Snapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshot = snapshot
	return nil
}

func (s *InMemory) LoadSnapshot() (Snapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshot, nil
}
