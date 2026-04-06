package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

const (
	desiredSnapshotFile = "desired_snapshot.json"
	appliedSnapshotFile = "applied_snapshot.json"
	runtimeStateFile    = "runtime_state.json"
)

type Filesystem struct {
	root string
	mu   sync.Mutex
}

func NewFilesystem(root string) (*Filesystem, error) {
	if root == "" {
		root = "."
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		return nil, err
	}
	return &Filesystem{root: root}, nil
}

func (f *Filesystem) SaveDesiredSnapshot(snapshot Snapshot) error {
	return f.save(desiredSnapshotFile, snapshot)
}

func (f *Filesystem) LoadDesiredSnapshot() (Snapshot, error) {
	var snapshot Snapshot
	if err := f.load(desiredSnapshotFile, &snapshot); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

func (f *Filesystem) SaveAppliedSnapshot(snapshot Snapshot) error {
	return f.save(appliedSnapshotFile, snapshot)
}

func (f *Filesystem) LoadAppliedSnapshot() (Snapshot, error) {
	var snapshot Snapshot
	if err := f.load(appliedSnapshotFile, &snapshot); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

func (f *Filesystem) SaveRuntimeState(state RuntimeState) error {
	return f.save(runtimeStateFile, state)
}

func (f *Filesystem) LoadRuntimeState() (RuntimeState, error) {
	var state RuntimeState
	if err := f.load(runtimeStateFile, &state); err != nil {
		return RuntimeState{}, err
	}
	return state, nil
}

func (f *Filesystem) save(filename string, value interface{}) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(f.root, filename), data, 0644)
}

func (f *Filesystem) load(filename string, dest interface{}) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	path := filepath.Join(f.root, filename)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return json.Unmarshal(data, dest)
}
