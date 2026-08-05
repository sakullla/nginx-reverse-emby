package storage

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPKIBackupRestoreTargetRollsBackSwapAndReopenFailure(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	activeDatabase := filepath.Join(root, "panel.db")
	store, err := NewStore(StoreConfig{Driver: "sqlite", DSN: activeDatabase, DataRoot: root, LocalAgentID: "local"})
	if err != nil {
		t.Fatalf("NewStore(active) error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.SaveAgent(t.Context(), AgentRow{ID: "old-agent", Name: "old-agent"}); err != nil {
		t.Fatalf("seed active database: %v", err)
	}
	expectedLease := seedPKIRestoreLease(t, store)
	activeBundle := filepath.Join(root, "pki")
	if err := os.MkdirAll(activeBundle, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(activeBundle, "marker"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}

	newStage := func(name string) (string, string) {
		t.Helper()
		stageRoot := filepath.Join(root, name)
		if err := os.MkdirAll(stageRoot, 0o700); err != nil {
			t.Fatal(err)
		}
		stagedDatabase := filepath.Join(stageRoot, "panel.db")
		stagedStore, err := NewStore(StoreConfig{
			Driver: "sqlite", DSN: stagedDatabase, DataRoot: stageRoot, LocalAgentID: "local",
		})
		if err != nil {
			t.Fatalf("NewStore(stage) error = %v", err)
		}
		if err := stagedStore.SaveAgent(t.Context(), AgentRow{ID: "new-agent", Name: "new-agent"}); err != nil {
			_ = stagedStore.Close()
			t.Fatalf("seed staged database: %v", err)
		}
		if err := stagedStore.Close(); err != nil {
			t.Fatalf("close staged database: %v", err)
		}
		stagedBundle := filepath.Join(stageRoot, "pki")
		if err := os.MkdirAll(stagedBundle, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(stagedBundle, "marker"), []byte("new"), 0o600); err != nil {
			t.Fatal(err)
		}
		return stagedDatabase, stagedBundle
	}

	staleDatabase, staleBundle := newStage("stale-lease-stage")
	staleLease := expectedLease
	staleLease.LeaseTerm = strings.Repeat("b", 64)
	err = store.ActivatePKISQLiteRestoreBundle(t.Context(), PKISQLiteRestoreBundle{
		StagedDatabasePath: staleDatabase,
		AdditionalPaths:    []PKIRestorePathSwap{{ActivePath: activeBundle, StagedPath: staleBundle}},
		ExpectedLease:      staleLease,
	})
	if !errors.Is(err, ErrPKILeaseFence) {
		t.Fatalf("ActivatePKISQLiteRestoreBundle(stale lease) error = %v", err)
	}
	if _, err := os.Stat(staleDatabase); err != nil {
		t.Fatalf("stale-lease rejection consumed staged database: %v", err)
	}

	injected := errors.New("injected reopen failure")
	failedDatabase, failedBundle := newStage("failed-stage")
	err = store.ActivatePKISQLiteRestoreBundle(t.Context(), PKISQLiteRestoreBundle{
		StagedDatabasePath: failedDatabase,
		AdditionalPaths:    []PKIRestorePathSwap{{ActivePath: activeBundle, StagedPath: failedBundle}},
		Hooks:              PKISQLiteRestoreHooks{BeforeReopen: func() error { return injected }},
		ExpectedLease:      expectedLease,
	})
	if !errors.Is(err, injected) {
		t.Fatalf("ActivatePKISQLiteRestoreBundle(injected) error = %v", err)
	}
	agents, err := store.ListAgents(t.Context())
	if err != nil || len(agents) != 1 || agents[0].ID != "old-agent" {
		t.Fatalf("old database after rollback = %+v, error = %v", agents, err)
	}
	marker, err := os.ReadFile(filepath.Join(activeBundle, "marker"))
	if err != nil || string(marker) != "old" {
		t.Fatalf("old bundle after rollback = %q, error = %v", marker, err)
	}
	if _, err := os.Stat(failedDatabase); err != nil {
		t.Fatalf("failed staged database was not restored: %v", err)
	}

	successDatabase, successBundle := newStage("success-stage")
	if err := store.ActivatePKISQLiteRestoreBundle(t.Context(), PKISQLiteRestoreBundle{
		StagedDatabasePath: successDatabase,
		AdditionalPaths:    []PKIRestorePathSwap{{ActivePath: activeBundle, StagedPath: successBundle}},
		ExpectedLease:      expectedLease,
	}); err != nil {
		t.Fatalf("ActivatePKISQLiteRestoreBundle(success) error = %v", err)
	}
	agents, err = store.ListAgents(t.Context())
	if err != nil || len(agents) != 1 || agents[0].ID != "new-agent" {
		t.Fatalf("restored database = %+v, error = %v", agents, err)
	}
	marker, err = os.ReadFile(filepath.Join(activeBundle, "marker"))
	if err != nil || string(marker) != "new" {
		t.Fatalf("restored bundle = %q, error = %v", marker, err)
	}
}

func TestPKIBackupRestoreTargetQuiescesReadersDuringSwap(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	activeDatabase := filepath.Join(root, "panel.db")
	store, err := NewStore(StoreConfig{Driver: "sqlite", DSN: activeDatabase, DataRoot: root, LocalAgentID: "local"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.SaveAgent(t.Context(), AgentRow{ID: "old-agent", Name: "old-agent"}); err != nil {
		t.Fatal(err)
	}
	expectedLease := seedPKIRestoreLease(t, store)
	stagedDatabase := createPKIRestoreAgentDatabase(t, filepath.Join(root, "stage"), "new-agent")

	swapEntered := make(chan struct{})
	releaseSwap := make(chan struct{})
	activationDone := make(chan error, 1)
	go func() {
		activationDone <- store.ActivatePKISQLiteRestoreBundle(t.Context(), PKISQLiteRestoreBundle{
			StagedDatabasePath: stagedDatabase,
			ExpectedLease:      expectedLease,
			Hooks: PKISQLiteRestoreHooks{BeforeReopen: func() error {
				close(swapEntered)
				<-releaseSwap
				return nil
			}},
		})
	}()
	select {
	case <-swapEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("protected restore did not enter the exclusive swap window")
	}

	type readerResult struct {
		agents []AgentRow
		err    error
	}
	readerDone := make(chan readerResult, 1)
	go func() {
		agents, readErr := store.ListAgents(t.Context())
		readerDone <- readerResult{agents: agents, err: readErr}
	}()
	select {
	case result := <-readerDone:
		t.Fatalf("reader crossed the exclusive restore window: agents=%+v error=%v", result.agents, result.err)
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseSwap)
	if err := <-activationDone; err != nil {
		t.Fatalf("ActivatePKISQLiteRestoreBundle() error = %v", err)
	}
	select {
	case result := <-readerDone:
		if result.err != nil || len(result.agents) != 1 || result.agents[0].ID != "new-agent" {
			t.Fatalf("reader after restore = %+v, error = %v", result.agents, result.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reader did not resume after protected restore")
	}
}

func TestPKIBackupRestoreTargetReopensEverySamePathStoreAndBlocksNewOpen(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	activeDatabase := filepath.Join(root, "panel.db")
	storeA, err := NewStore(StoreConfig{Driver: "sqlite", DSN: activeDatabase, DataRoot: root, LocalAgentID: "local"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storeA.Close() })
	if err := storeA.SaveAgent(t.Context(), AgentRow{ID: "old-agent", Name: "old-agent"}); err != nil {
		t.Fatal(err)
	}
	expectedLease := seedPKIRestoreLease(t, storeA)
	storeB, err := NewStore(StoreConfig{Driver: "sqlite", DSN: activeDatabase, DataRoot: root, LocalAgentID: "local"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storeB.Close() })
	stagedDatabase := createPKIRestoreAgentDatabase(t, filepath.Join(root, "stage"), "new-agent")

	swapEntered := make(chan struct{})
	releaseSwap := make(chan struct{})
	activationDone := make(chan error, 1)
	go func() {
		activationDone <- storeA.ActivatePKISQLiteRestoreBundle(t.Context(), PKISQLiteRestoreBundle{
			StagedDatabasePath: stagedDatabase,
			ExpectedLease:      expectedLease,
			Hooks: PKISQLiteRestoreHooks{BeforeReopen: func() error {
				close(swapEntered)
				<-releaseSwap
				return nil
			}},
		})
	}()
	select {
	case <-swapEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("protected restore did not enter the multi-store swap window")
	}

	type openResult struct {
		store *GormStore
		err   error
	}
	openDone := make(chan openResult, 1)
	go func() {
		opened, openErr := NewStore(StoreConfig{
			Driver: "sqlite", DSN: activeDatabase, DataRoot: root, LocalAgentID: "local", SkipBootstrapSchema: true,
		})
		openDone <- openResult{store: opened, err: openErr}
	}()
	select {
	case result := <-openDone:
		if result.store != nil {
			_ = result.store.Close()
		}
		t.Fatalf("same-path store opened during restore: %v", result.err)
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseSwap)
	if err := <-activationDone; err != nil {
		t.Fatalf("ActivatePKISQLiteRestoreBundle() error = %v", err)
	}
	assertPKIRestoreAgent(t, storeA, "new-agent")
	assertPKIRestoreAgent(t, storeB, "new-agent")
	select {
	case result := <-openDone:
		if result.err != nil {
			t.Fatalf("same-path store open after restore: %v", result.err)
		}
		assertPKIRestoreAgent(t, result.store, "new-agent")
		_ = result.store.Close()
	case <-time.After(5 * time.Second):
		t.Fatal("same-path store did not open after restore")
	}
}

func TestPKIRestoreJournalRecoversPreCommitAndCommittedCrashes(t *testing.T) {
	t.Parallel()
	t.Run("pre-commit rolls back", func(t *testing.T) {
		root := t.TempDir()
		activeDatabase := createPKIRestoreAgentDatabase(t, filepath.Join(root, "active"), "old-agent")
		stagedDatabase := createPKIRestoreAgentDatabase(t, filepath.Join(root, "stage"), "new-agent")
		backupPath := activeDatabase + ".pki-restore-backup-test-precommit"
		states := []pkiRestoreSwapState{
			{activePath: activeDatabase, stagedPath: stagedDatabase, backupPath: backupPath, activeExisted: true},
			{activePath: activeDatabase + "-wal", backupPath: activeDatabase + "-wal.pki-restore-backup-test-precommit"},
		}
		journal := restoreJournalFromStates(activeDatabase, states)
		journalPath := pkiRestoreJournalPath(activeDatabase)
		if err := writePKIRestoreJournal(journalPath, journal); err != nil {
			t.Fatal(err)
		}
		if err := promotePKIRestorePaths(states); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(activeDatabase+"-wal", []byte("interrupted restored WAL"), 0o600); err != nil {
			t.Fatal(err)
		}

		store, err := NewStore(StoreConfig{
			Driver: "sqlite", DSN: activeDatabase, DataRoot: filepath.Dir(activeDatabase),
			LocalAgentID: "local", SkipBootstrapSchema: true,
		})
		if err != nil {
			t.Fatalf("NewStore(recover pre-commit) error = %v", err)
		}
		assertPKIRestoreAgent(t, store, "old-agent")
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		assertPKIRestoreArtifactsRemoved(t, journalPath, backupPath)
		stagedStore, err := NewStore(StoreConfig{
			Driver: "sqlite", DSN: stagedDatabase, DataRoot: filepath.Dir(stagedDatabase),
			LocalAgentID: "local", SkipBootstrapSchema: true,
		})
		if err != nil {
			t.Fatalf("NewStore(restored stage) error = %v", err)
		}
		assertPKIRestoreAgent(t, stagedStore, "new-agent")
		_ = stagedStore.Close()
	})

	t.Run("committed rolls forward", func(t *testing.T) {
		root := t.TempDir()
		activeDatabase := createPKIRestoreAgentDatabase(t, filepath.Join(root, "active"), "old-agent")
		stagedDatabase := createPKIRestoreAgentDatabase(t, filepath.Join(root, "stage"), "new-agent")
		backupPath := activeDatabase + ".pki-restore-backup-test-committed"
		states := []pkiRestoreSwapState{{
			activePath: activeDatabase, stagedPath: stagedDatabase, backupPath: backupPath, activeExisted: true,
		}}
		journal := restoreJournalFromStates(activeDatabase, states)
		journalPath := pkiRestoreJournalPath(activeDatabase)
		if err := writePKIRestoreJournal(journalPath, journal); err != nil {
			t.Fatal(err)
		}
		if err := promotePKIRestorePaths(states); err != nil {
			t.Fatal(err)
		}
		if err := writePKIRestoreCommitMarker(journalPath); err != nil {
			t.Fatal(err)
		}

		store, err := NewStore(StoreConfig{
			Driver: "sqlite", DSN: activeDatabase, DataRoot: filepath.Dir(activeDatabase),
			LocalAgentID: "local", SkipBootstrapSchema: true,
		})
		if err != nil {
			t.Fatalf("NewStore(recover committed) error = %v", err)
		}
		assertPKIRestoreAgent(t, store, "new-agent")
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		assertPKIRestoreArtifactsRemoved(t, journalPath, backupPath)
		if _, err := os.Stat(stagedDatabase); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("committed staged database unexpectedly exists: %v", err)
		}
	})
}

func TestPKIRestoreProcessLockSerializesExclusiveActivation(t *testing.T) {
	t.Parallel()
	activeDatabase := filepath.Join(t.TempDir(), "panel.db")
	shared, err := acquirePKIRestoreProcessLock(t.Context(), activeDatabase, false)
	if err != nil {
		t.Fatal(err)
	}
	defer shared.Close()
	blockedCtx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if exclusive, err := acquirePKIRestoreProcessLock(blockedCtx, activeDatabase, true); !errors.Is(err, context.DeadlineExceeded) {
		if exclusive != nil {
			_ = exclusive.Close()
		}
		t.Fatalf("exclusive restore lock while shared holder is live = %v, want deadline", err)
	}
	if err := shared.Close(); err != nil {
		t.Fatal(err)
	}
	exclusive, err := acquirePKIRestoreProcessLock(t.Context(), activeDatabase, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := exclusive.Downgrade(t.Context()); err != nil {
		_ = exclusive.Close()
		t.Fatal(err)
	}
	secondShared, err := acquirePKIRestoreProcessLock(t.Context(), activeDatabase, false)
	if err != nil {
		_ = exclusive.Close()
		t.Fatal(err)
	}
	_ = secondShared.Close()
	_ = exclusive.Close()
}

func TestPKIBackupRestoreTargetRechecksLeaseAfterProcessLockWait(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	activeDatabase := filepath.Join(root, "panel.db")
	store, err := NewStore(StoreConfig{Driver: "sqlite", DSN: activeDatabase, DataRoot: root, LocalAgentID: "local"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.SaveAgent(t.Context(), AgentRow{ID: "old-agent", Name: "old-agent"}); err != nil {
		t.Fatal(err)
	}
	expectedLease := seedPKIRestoreLease(t, store)
	stagedDatabase := createPKIRestoreAgentDatabase(t, filepath.Join(root, "stage"), "new-agent")

	blocker, err := acquirePKIRestoreProcessLock(t.Context(), activeDatabase, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if blocker != nil {
			_ = blocker.Close()
		}
	})
	activationDone := make(chan error, 1)
	go func() {
		activationDone <- store.ActivatePKISQLiteRestoreBundle(t.Context(), PKISQLiteRestoreBundle{
			StagedDatabasePath: stagedDatabase,
			ExpectedLease:      expectedLease,
		})
	}()
	select {
	case activationErr := <-activationDone:
		t.Fatalf("protected restore did not wait for the process lock: %v", activationErr)
	case <-time.After(100 * time.Millisecond):
	}

	if _, err := store.databaseLifecycle.pool.ExecContext(t.Context(),
		"UPDATE pki_instance_lease SET lease_term = ? WHERE id = ?", strings.Repeat("c", 64), PKILeaseSingletonID); err != nil {
		t.Fatalf("replace lease while restore waits for process lock: %v", err)
	}
	if err := blocker.Close(); err != nil {
		t.Fatal(err)
	}
	blocker = nil
	select {
	case activationErr := <-activationDone:
		if !errors.Is(activationErr, ErrPKILeaseFence) {
			t.Fatalf("protected restore after lease handoff error = %v", activationErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("protected restore did not finish after releasing the process lock")
	}
	if _, err := os.Stat(stagedDatabase); err != nil {
		t.Fatalf("lease fencing consumed the staged database: %v", err)
	}
	assertPKIRestoreAgent(t, store, "old-agent")
}

func TestPKIBackupRestoreTargetRechecksLeaseAfterCleanupRecovery(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	activeDatabase := filepath.Join(root, "panel.db")
	store, err := NewStore(StoreConfig{Driver: "sqlite", DSN: activeDatabase, DataRoot: root, LocalAgentID: "local"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.SaveAgent(t.Context(), AgentRow{ID: "old-agent", Name: "old-agent"}); err != nil {
		t.Fatal(err)
	}
	expectedLease := seedPKIRestoreLease(t, store)
	stagedDatabase := createPKIRestoreAgentDatabase(t, filepath.Join(root, "stage"), "new-agent")

	err = store.ActivatePKISQLiteRestoreBundle(t.Context(), PKISQLiteRestoreBundle{
		StagedDatabasePath: stagedDatabase,
		ExpectedLease:      expectedLease,
		Hooks: PKISQLiteRestoreHooks{AfterCleanupRecovery: func() error {
			_, hookErr := store.databaseLifecycle.pool.ExecContext(t.Context(),
				"UPDATE pki_instance_lease SET lease_deadline = datetime('now', '-1 second') WHERE id = ?", PKILeaseSingletonID)
			return hookErr
		}},
	})
	if !errors.Is(err, ErrPKILeaseFence) {
		t.Fatalf("protected restore after cleanup crossed lease deadline: %v", err)
	}
	if _, err := os.Stat(stagedDatabase); err != nil {
		t.Fatalf("post-cleanup lease fence consumed staged database: %v", err)
	}
	assertPKIRestoreAgent(t, store, "old-agent")
}

func TestPKIBackupRestoreTargetReportsCommittedCleanupAndRecoversIt(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	activeDatabase := filepath.Join(root, "panel.db")
	store, err := NewStore(StoreConfig{Driver: "sqlite", DSN: activeDatabase, DataRoot: root, LocalAgentID: "local"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAgent(t.Context(), AgentRow{ID: "old-agent", Name: "old-agent"}); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	expectedLease := seedPKIRestoreLease(t, store)
	stageRoot := filepath.Join(root, ".pki-restore-stage-committed-cleanup")
	stagedDatabase := createPKIRestoreAgentDatabase(t, stageRoot, "new-agent")
	injected := errors.New("injected post-commit cleanup failure")

	err = store.ActivatePKISQLiteRestoreBundle(t.Context(), PKISQLiteRestoreBundle{
		StagedDatabasePath: stagedDatabase,
		CleanupPaths:       []string{stageRoot},
		ExpectedLease:      expectedLease,
		Hooks:              PKISQLiteRestoreHooks{AfterCommit: func() error { return injected }},
	})
	if !errors.Is(err, ErrPKIRestoreCleanupPending) || !errors.Is(err, injected) {
		_ = store.Close()
		t.Fatalf("committed cleanup error = %v", err)
	}
	assertPKIRestoreAgent(t, store, "new-agent")
	journalPath := pkiRestoreJournalPath(activeDatabase)
	if exists, statErr := pkiRestorePathExists(journalPath); statErr != nil || !exists {
		_ = store.Close()
		t.Fatalf("committed restore journal exists = %v, error = %v", exists, statErr)
	}
	if exists, statErr := pkiRestorePathExists(pkiRestoreCommitMarkerPath(journalPath)); statErr != nil || !exists {
		_ = store.Close()
		t.Fatalf("committed restore marker exists = %v, error = %v", exists, statErr)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewStore(StoreConfig{
		Driver: "sqlite", DSN: activeDatabase, DataRoot: root, LocalAgentID: "local", SkipBootstrapSchema: true,
	})
	if err != nil {
		t.Fatalf("NewStore(recover committed cleanup) error = %v", err)
	}
	defer reopened.Close()
	assertPKIRestoreAgent(t, reopened, "new-agent")
	assertPKIRestoreArtifactsRemoved(t, journalPath, activeDatabase+".unused-backup")
	if _, err := os.Stat(stageRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("committed staging root still exists: %v", err)
	}
}

func TestPKIRestoreCleanupManifestRetriesDeletionTombstones(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	activeDatabase := createPKIRestoreAgentDatabase(t, root, "new-agent")
	stageRoot := filepath.Join(root, ".pki-restore-stage-tombstone")
	stagedDatabase := filepath.Join(stageRoot, "panel.db")
	backupPath := activeDatabase + ".pki-restore-backup-tombstone"
	journal := restoreJournalFromStates(activeDatabase, []pkiRestoreSwapState{{
		activePath: activeDatabase, stagedPath: stagedDatabase, backupPath: backupPath, activeExisted: true,
	}})
	journal.CleanupPaths = []string{stageRoot}
	journalPath := pkiRestoreJournalPath(activeDatabase)
	cleanupPath := pkiRestoreCleanupManifestPath(journalPath)
	encoded, err := json.Marshal(journal)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeAtomicPKIRestoreFile(cleanupPath, encoded); err != nil {
		t.Fatal(err)
	}
	backupTombstone := pkiRestoreDeletionTombstonePath(backupPath)
	if err := os.WriteFile(backupTombstone, []byte("retired database material"), 0o600); err != nil {
		t.Fatal(err)
	}
	stageTombstone := pkiRestoreDeletionTombstonePath(stageRoot)
	if err := os.MkdirAll(stageTombstone, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stageTombstone, "private-key"), []byte("sensitive"), 0o600); err != nil {
		t.Fatal(err)
	}

	store, err := NewStore(StoreConfig{
		Driver: "sqlite", DSN: activeDatabase, DataRoot: root, LocalAgentID: "local", SkipBootstrapSchema: true,
	})
	if err != nil {
		t.Fatalf("NewStore(retry deletion tombstones) error = %v", err)
	}
	defer store.Close()
	assertPKIRestoreAgent(t, store, "new-agent")
	for _, path := range []string{backupTombstone, stageTombstone, cleanupPath, pkiRestoreDeletionTombstonePath(cleanupPath)} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("cleanup artifact %q still exists: %v", path, err)
		}
	}
}

func TestPKIRestoreStagingRegistrationRetriesSensitiveTombstoneDeletion(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	activeDatabase := filepath.Join(root, "panel.db")
	store, err := NewStore(StoreConfig{Driver: "sqlite", DSN: activeDatabase, DataRoot: root, LocalAgentID: "local"})
	if err != nil {
		t.Fatal(err)
	}
	stageRoot := filepath.Join(root, ".pki-restore-vault-stage-registration")
	if err := os.MkdirAll(stageRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	registration, err := store.RegisterPKIRestoreStagingCleanup(t.Context(), []string{stageRoot})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stageRoot, "master.key"), []byte("sensitive"), 0o600); err != nil {
		t.Fatal(err)
	}

	injected := errors.New("injected staging tombstone deletion failure")
	tombstone := pkiRestoreDeletionTombstonePath(stageRoot)
	originalRemoveAll := removeAllPKIRestorePath
	removeAllPKIRestorePath = func(path string) error {
		if path == tombstone {
			return injected
		}
		return originalRemoveAll(path)
	}
	err = store.CleanupPKIRestoreStagingRegistration(registration)
	removeAllPKIRestorePath = originalRemoveAll
	if !errors.Is(err, injected) {
		_ = store.Close()
		t.Fatalf("staging cleanup injected failure = %v", err)
	}
	if _, err := os.Lstat(tombstone); err != nil {
		_ = store.Close()
		t.Fatalf("sensitive deletion tombstone is not retained: %v", err)
	}
	if _, err := os.Lstat(registration.manifestPath); err != nil {
		_ = store.Close()
		t.Fatalf("staging cleanup manifest is not retained: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewStore(StoreConfig{
		Driver: "sqlite", DSN: activeDatabase, DataRoot: root, LocalAgentID: "local", SkipBootstrapSchema: true,
	})
	if err != nil {
		t.Fatalf("NewStore(recover staging registration) error = %v", err)
	}
	for _, path := range []string{stageRoot, tombstone} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("registered staging artifact %q still exists: %v", path, err)
		}
	}
	if _, err := os.Lstat(registration.manifestPath); err != nil {
		t.Fatalf("staging registration was not retained for clean-open confirmation: %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}

	confirmed, err := NewStore(StoreConfig{
		Driver: "sqlite", DSN: activeDatabase, DataRoot: root, LocalAgentID: "local", SkipBootstrapSchema: true,
	})
	if err != nil {
		t.Fatalf("NewStore(confirm staging deletion) error = %v", err)
	}
	defer confirmed.Close()
	for _, path := range []string{stageRoot, tombstone, registration.manifestPath,
		pkiRestoreDeletionTombstonePath(registration.manifestPath)} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("confirmed staging artifact %q still exists: %v", path, err)
		}
	}
}

func TestPKIRestoreStagingRegistrationTracksResurrectedCrossVolumeTombstone(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	activeDatabase := filepath.Join(root, "panel.db")
	store, err := NewStore(StoreConfig{Driver: "sqlite", DSN: activeDatabase, DataRoot: root, LocalAgentID: "local"})
	if err != nil {
		t.Fatal(err)
	}
	stageRoot := filepath.Join(root, ".pki-restore-vault-stage-resurrected")
	if err := os.MkdirAll(stageRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	registration, err := store.RegisterPKIRestoreStagingCleanup(t.Context(), []string{stageRoot})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stageRoot, "master.key"), []byte("sensitive"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.CleanupPKIRestoreStagingRegistration(registration); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(registration.manifestPath); err != nil {
		t.Fatalf("staging registration disappeared in the deletion lifecycle: %v", err)
	}

	// Model a power loss on a volume whose directory deletion was not durable:
	// the sensitive tombstone returns while the registration on the DB volume
	// remains. The first open must clean it but retain the record for one more
	// clean-open confirmation.
	tombstone := pkiRestoreDeletionTombstonePath(stageRoot)
	if err := os.MkdirAll(tombstone, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tombstone, "master.key"), []byte("resurrected-sensitive"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	recovered, err := NewStore(StoreConfig{
		Driver: "sqlite", DSN: activeDatabase, DataRoot: root, LocalAgentID: "local", SkipBootstrapSchema: true,
	})
	if err != nil {
		t.Fatalf("NewStore(clean resurrected staging tombstone) error = %v", err)
	}
	if _, err := os.Lstat(tombstone); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("resurrected staging tombstone still exists: %v", err)
	}
	if _, err := os.Lstat(registration.manifestPath); err != nil {
		t.Fatalf("registration was removed in the same lifecycle as resurrected data: %v", err)
	}
	if err := recovered.Close(); err != nil {
		t.Fatal(err)
	}

	confirmed, err := NewStore(StoreConfig{
		Driver: "sqlite", DSN: activeDatabase, DataRoot: root, LocalAgentID: "local", SkipBootstrapSchema: true,
	})
	if err != nil {
		t.Fatalf("NewStore(confirm resurrected staging deletion) error = %v", err)
	}
	defer confirmed.Close()
	for _, path := range []string{stageRoot, tombstone, registration.manifestPath,
		pkiRestoreDeletionTombstonePath(registration.manifestPath)} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("confirmed resurrected staging artifact %q still exists: %v", path, err)
		}
	}
}

func TestPKIRestoreStagingRegistrationRecoveryTreatsDatabasePathLiterally(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "data[restore]")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	activeDatabase := filepath.Join(root, "panel.db")
	store, err := NewStore(StoreConfig{Driver: "sqlite", DSN: activeDatabase, DataRoot: root, LocalAgentID: "local"})
	if err != nil {
		t.Fatal(err)
	}
	stageRoot := filepath.Join(root, ".pki-restore-vault-stage-literal-scan")
	if err := os.MkdirAll(stageRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	registration, err := store.RegisterPKIRestoreStagingCleanup(t.Context(), []string{stageRoot})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CleanupPKIRestoreStagingRegistration(registration); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewStore(StoreConfig{
		Driver: "sqlite", DSN: activeDatabase, DataRoot: root, LocalAgentID: "local", SkipBootstrapSchema: true,
	})
	if err != nil {
		t.Fatalf("NewStore(recover literal staging registration path) error = %v", err)
	}
	defer reopened.Close()
	for _, path := range []string{stageRoot, pkiRestoreDeletionTombstonePath(stageRoot), registration.manifestPath,
		pkiRestoreDeletionTombstonePath(registration.manifestPath)} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("literal-path staging artifact %q still exists: %v", path, err)
		}
	}
}

func TestPKIRestoreJournalRetainsCleanupOwnershipAcrossPartialRollback(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	activeDatabase := filepath.Join(root, "panel.db")
	store, err := NewStore(StoreConfig{Driver: "sqlite", DSN: activeDatabase, DataRoot: root, LocalAgentID: "local"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAgent(t.Context(), AgentRow{ID: "old-agent", Name: "old-agent"}); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	expectedLease := seedPKIRestoreLease(t, store)
	activeBundle := filepath.Join(root, "pki")
	if err := os.MkdirAll(activeBundle, 0o700); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(activeBundle, "marker"), []byte("old"), 0o600); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	stageRoot := filepath.Join(root, ".pki-restore-multi-stage-rollback")
	stagedDatabase := createPKIRestoreAgentDatabase(t, stageRoot, "new-agent")
	stagedBundle := filepath.Join(stageRoot, "pki")
	if err := os.MkdirAll(stagedBundle, 0o700); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stagedBundle, "marker"), []byte("new"), 0o600); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	injected := errors.New("injected activation failure after promotion")
	err = store.ActivatePKISQLiteRestoreBundle(t.Context(), PKISQLiteRestoreBundle{
		StagedDatabasePath: stagedDatabase,
		AdditionalPaths:    []PKIRestorePathSwap{{ActivePath: activeBundle, StagedPath: stagedBundle}},
		CleanupPaths:       []string{stageRoot},
		ExpectedLease:      expectedLease,
		Hooks: PKISQLiteRestoreHooks{BeforeReopen: func() error {
			if writeErr := os.WriteFile(stagedDatabase, []byte("rollback obstruction"), 0o600); writeErr != nil {
				return writeErr
			}
			return injected
		}},
	})
	if !errors.Is(err, injected) || !errors.Is(err, ErrPKIRestoreJournalOwnsCleanup) {
		_ = store.Close()
		t.Fatalf("partial rollback ownership error = %v", err)
	}
	journalPath := pkiRestoreJournalPath(activeDatabase)
	if exists, statErr := pkiRestorePathExists(journalPath); statErr != nil || !exists {
		_ = store.Close()
		t.Fatalf("partial rollback journal exists = %v, error = %v", exists, statErr)
	}
	if _, err := os.Stat(stageRoot); err != nil {
		_ = store.Close()
		t.Fatalf("journal-owned staging root was removed: %v", err)
	}
	if err := os.Remove(stagedDatabase); err != nil {
		_ = store.Close()
		t.Fatalf("remove rollback obstruction: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewStore(StoreConfig{
		Driver: "sqlite", DSN: activeDatabase, DataRoot: root, LocalAgentID: "local", SkipBootstrapSchema: true,
	})
	if err != nil {
		t.Fatalf("NewStore(recover partial rollback) error = %v", err)
	}
	defer reopened.Close()
	assertPKIRestoreAgent(t, reopened, "old-agent")
	marker, err := os.ReadFile(filepath.Join(activeBundle, "marker"))
	if err != nil || string(marker) != "old" {
		t.Fatalf("recovered active bundle = %q, error = %v", marker, err)
	}
	if _, err := os.Stat(stageRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("recovered journal staging root still exists: %v", err)
	}
}

func TestSQLiteRestoreLifecycleCanonicalizesSymlinksAndRejectsHardlinks(t *testing.T) {
	t.Parallel()
	t.Run("symlink aliases share one lifecycle group", func(t *testing.T) {
		root := t.TempDir()
		realRoot := filepath.Join(root, "real")
		if err := os.MkdirAll(realRoot, 0o700); err != nil {
			t.Fatal(err)
		}
		aliasRoot := filepath.Join(root, "alias")
		if err := os.Symlink(realRoot, aliasRoot); err != nil {
			t.Skipf("filesystem symlinks unavailable: %v", err)
		}
		realDatabase := filepath.Join(realRoot, "panel.db")
		aliasDatabase := filepath.Join(aliasRoot, "panel.db")
		realStore, err := NewStore(StoreConfig{Driver: "sqlite", DSN: realDatabase, DataRoot: realRoot, LocalAgentID: "local"})
		if err != nil {
			t.Fatal(err)
		}
		defer realStore.Close()
		aliasStore, err := NewStore(StoreConfig{
			Driver: "sqlite", DSN: aliasDatabase, DataRoot: aliasRoot, LocalAgentID: "local", SkipBootstrapSchema: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		defer aliasStore.Close()
		if realStore.databaseLifecycle.group != aliasStore.databaseLifecycle.group {
			t.Fatal("SQLite symlink aliases installed distinct lifecycle groups")
		}
	})

	t.Run("hardlink aliases are rejected", func(t *testing.T) {
		root := t.TempDir()
		databasePath := createPKIRestoreAgentDatabase(t, root, "agent")
		aliasPath := filepath.Join(root, "panel-hardlink.db")
		if err := os.Link(databasePath, aliasPath); err != nil {
			t.Skipf("filesystem hard links unavailable: %v", err)
		}
		store, err := NewStore(StoreConfig{
			Driver: "sqlite", DSN: aliasPath, DataRoot: root, LocalAgentID: "local", SkipBootstrapSchema: true,
		})
		if store != nil {
			_ = store.Close()
		}
		if !errors.Is(err, ErrPKIInvariant) {
			t.Fatalf("NewStore(hardlink alias) error = %v", err)
		}
	})
}

func seedPKIRestoreLease(t *testing.T, store *GormStore) PKILeaseFence {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	deadline := now.Add(time.Hour)
	settings := pkiTestSettings(now)
	settings.ID = PKISettingsSingletonID
	settings.PKIDomainID = "restore-domain"
	settings.PKIEpoch = 1
	lease := PKIInstanceLeaseRow{
		ID: PKILeaseSingletonID, PKIDomainID: settings.PKIDomainID, PKIEpoch: settings.PKIEpoch,
		InstanceID: "restore-instance", LeaseTerm: strings.Repeat("a", 64), LeaseDeadline: deadline,
		State: PKIInstanceLeaseStateHeld, UpdatedAt: now,
	}
	if err := store.db.WithContext(t.Context()).Create(&settings).Error; err != nil {
		t.Fatalf("seed restore settings: %v", err)
	}
	if err := store.db.WithContext(t.Context()).Create(&lease).Error; err != nil {
		t.Fatalf("seed restore lease: %v", err)
	}
	return PKILeaseFence{
		PKIDomainID: settings.PKIDomainID, PKIEpoch: settings.PKIEpoch,
		InstanceID: lease.InstanceID, LeaseTerm: lease.LeaseTerm, LeaseDeadline: deadline,
	}
}

func createPKIRestoreAgentDatabase(t *testing.T, root, agentID string) string {
	t.Helper()
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join(root, "panel.db")
	store, err := NewStore(StoreConfig{Driver: "sqlite", DSN: databasePath, DataRoot: root, LocalAgentID: "local"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAgent(t.Context(), AgentRow{ID: agentID, Name: agentID}); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	return databasePath
}

func assertPKIRestoreAgent(t *testing.T, store *GormStore, expectedID string) {
	t.Helper()
	agents, err := store.ListAgents(t.Context())
	if err != nil || len(agents) != 1 || agents[0].ID != expectedID {
		t.Fatalf("restored agents = %+v, want %q, error = %v", agents, expectedID, err)
	}
}

func assertPKIRestoreArtifactsRemoved(t *testing.T, journalPath, backupPath string) {
	t.Helper()
	for _, path := range []string{
		journalPath, pkiRestoreCommitMarkerPath(journalPath), pkiRestoreCleanupManifestPath(journalPath),
		pkiRestoreDeletionTombstonePath(pkiRestoreCleanupManifestPath(journalPath)), backupPath,
	} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("restore artifact %q still exists: %v", path, err)
		}
	}
}
