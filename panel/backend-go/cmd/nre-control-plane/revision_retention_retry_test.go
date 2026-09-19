package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestRunRevisionRetentionStartupPassRetriesTransientFailure(t *testing.T) {
	originalPass := runRevisionRetentionPass
	originalInterval := revisionRetentionStartupRetryInterval
	originalAttempts := revisionRetentionStartupMaxAttempts
	t.Cleanup(func() {
		runRevisionRetentionPass = originalPass
		revisionRetentionStartupRetryInterval = originalInterval
		revisionRetentionStartupMaxAttempts = originalAttempts
	})

	revisionRetentionStartupRetryInterval = time.Millisecond
	revisionRetentionStartupMaxAttempts = 4
	calls := 0
	runRevisionRetentionPass = func(context.Context, config.Config) error {
		calls++
		if calls < 3 {
			return errors.New("database is locked")
		}
		return nil
	}

	if err := runRevisionRetentionStartupPass(t.Context(), config.Config{}); err != nil {
		t.Fatalf("startup retention pass error = %v", err)
	}
	if calls != 3 {
		t.Fatalf("startup retention calls = %d, want 3", calls)
	}
}

func TestRunRevisionRetentionStartupPassStopsAfterBoundedAttempts(t *testing.T) {
	originalPass := runRevisionRetentionPass
	originalInterval := revisionRetentionStartupRetryInterval
	originalAttempts := revisionRetentionStartupMaxAttempts
	t.Cleanup(func() {
		runRevisionRetentionPass = originalPass
		revisionRetentionStartupRetryInterval = originalInterval
		revisionRetentionStartupMaxAttempts = originalAttempts
	})

	wantErr := errors.New("persistent retention failure")
	revisionRetentionStartupRetryInterval = time.Millisecond
	revisionRetentionStartupMaxAttempts = 3
	calls := 0
	runRevisionRetentionPass = func(context.Context, config.Config) error {
		calls++
		return wantErr
	}

	err := runRevisionRetentionStartupPass(t.Context(), config.Config{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("startup retention pass error = %v, want %v", err, wantErr)
	}
	if calls != 3 {
		t.Fatalf("startup retention calls = %d, want 3", calls)
	}
}

func TestRunRevisionRetentionPassFailsOrphanedPluginOperations(t *testing.T) {
	originalOpen := openRevisionRetentionStore
	t.Cleanup(func() { openRevisionRetentionStore = originalOpen })

	store := &revisionRetentionStoreRecorder{}
	openRevisionRetentionStore = func(config.Config) (revisionRetentionStore, error) {
		return store, nil
	}

	before := time.Now().UTC()
	if err := runRevisionRetentionPass(t.Context(), config.Config{DataDir: t.TempDir()}); err != nil {
		t.Fatalf("run retention pass: %v", err)
	}
	if store.orphanCalls != 1 {
		t.Fatalf("orphan cleanup calls = %d, want 1", store.orphanCalls)
	}
	if store.orphanNow.Before(before) || store.orphanNow.After(time.Now().UTC()) {
		t.Fatalf("orphan cleanup now = %v, want current time", store.orphanNow)
	}
	if want := store.orphanNow.Add(-storage.DefaultOrphanedPluginOperationGrace); !store.orphanCutoff.Equal(want) {
		t.Fatalf("orphan cleanup cutoff = %v, want %v", store.orphanCutoff, want)
	}
	if !store.closed {
		t.Fatal("retention store was not closed")
	}
}

type revisionRetentionStoreRecorder struct {
	orphanCalls  int
	orphanCutoff time.Time
	orphanNow    time.Time
	closed       bool
}

func (*revisionRetentionStoreRecorder) PruneRevisionHistory(context.Context, storage.RevisionRetentionPolicy) (storage.RevisionPruneResult, error) {
	return storage.RevisionPruneResult{}, nil
}

func (*revisionRetentionStoreRecorder) ListPluginRuntimeDirectoryReferences(context.Context) ([]storage.PluginRuntimeDirectoryReference, error) {
	return nil, nil
}

func (s *revisionRetentionStoreRecorder) FailOrphanedPluginOperations(_ context.Context, cutoff, now time.Time) (int64, error) {
	s.orphanCalls++
	s.orphanCutoff = cutoff
	s.orphanNow = now
	return 0, nil
}

func (s *revisionRetentionStoreRecorder) Close() error {
	s.closed = true
	return nil
}
