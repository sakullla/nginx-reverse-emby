package core

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

const (
	desiredSnapshotFile       = "desired-snapshot.json"
	appliedSnapshotFile       = "applied-snapshot.json"
	runtimeStateFile          = "runtime-state.json"
	generationJournalFile     = "generation-journal.json"
	lastKnownGoodSnapshotFile = "last-known-good-snapshot.json"
)

type Filesystem struct {
	root                   string
	mu                     sync.Mutex
	syncDirectory          func(string) error
	pluginLogAppendFailure func(string) error
	pluginLogSessionID     string
	logCapacity            pluginLogCapacitySignal
}

func NewFilesystem(root string) (*Filesystem, error) {
	if root == "" {
		root = "."
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		return nil, err
	}
	sessionBytes := make([]byte, 32)
	if _, err := rand.Read(sessionBytes); err != nil {
		return nil, err
	}
	return &Filesystem{root: root, syncDirectory: syncFilesystemDirectory, pluginLogSessionID: hex.EncodeToString(sessionBytes)}, nil
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
		var lastKnownGood Snapshot
		if fallbackErr := f.load(lastKnownGoodSnapshotFile, &lastKnownGood); fallbackErr == nil && lastKnownGood.Revision > 0 {
			return lastKnownGood, nil
		} else if fallbackErr != nil {
			return Snapshot{}, errors.Join(err, fallbackErr)
		}
		return Snapshot{}, err
	}
	return snapshot, nil
}

func (f *Filesystem) SaveLastKnownGoodSnapshot(snapshot Snapshot) error {
	return f.save(lastKnownGoodSnapshotFile, snapshot)
}

func (f *Filesystem) LoadLastKnownGoodSnapshot() (Snapshot, error) {
	var snapshot Snapshot
	if err := f.load(lastKnownGoodSnapshotFile, &snapshot); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

func (f *Filesystem) SaveGenerationJournal(journal model.GenerationJournal) error {
	return f.save(generationJournalFile, journal)
}

func (f *Filesystem) LoadGenerationJournal() (model.GenerationJournal, error) {
	var journal model.GenerationJournal
	if err := f.load(generationJournalFile, &journal); err != nil {
		return model.GenerationJournal{}, err
	}
	return journal, nil
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
	dstPath := filepath.Join(f.root, filename)
	tmpFile, err := os.CreateTemp(f.root, filename+".tmp")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()

	removeTemp := func() {
		os.Remove(tmpPath)
	}
	if err := tmpFile.Chmod(0600); err != nil {
		tmpFile.Close()
		removeTemp()
		return err
	}

	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		removeTemp()
		return err
	}

	if err := tmpFile.Sync(); err != nil {
		tmpFile.Close()
		removeTemp()
		return err
	}

	if err := tmpFile.Close(); err != nil {
		removeTemp()
		return err
	}

	if err := os.Rename(tmpPath, dstPath); err != nil {
		removeTemp()
		return err
	}
	if err := f.syncDirectory(f.root); err != nil {
		return &filesystemCommitUncertainError{err: err}
	}
	return nil
}

type filesystemCommitUncertainError struct {
	err error
}

func (e *filesystemCommitUncertainError) Error() string {
	return "filesystem rename committed but directory sync failed: " + e.err.Error()
}

func (e *filesystemCommitUncertainError) Unwrap() error {
	return e.err
}

func isFilesystemCommitUncertain(err error) bool {
	var target *filesystemCommitUncertainError
	return errors.As(err, &target)
}

func syncFilesystemDirectory(root string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	dir, err := os.Open(root)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
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
