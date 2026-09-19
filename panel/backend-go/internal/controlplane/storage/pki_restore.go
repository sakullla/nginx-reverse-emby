package storage

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gorm.io/gorm"
)

type PKIRestorePathSwap struct {
	ActivePath string
	StagedPath string
}

type PKISQLiteRestoreHooks struct {
	BeforeSwap           func() error
	AfterCleanupRecovery func() error
	BeforeReopen         func() error
	AfterSwap            func() error
	AfterCommit          func() error
	AfterRollback        func() error
}

var (
	ErrPKIRestoreCleanupPending     = errors.New("protected restore committed with cleanup pending")
	ErrPKIRestoreJournalOwnsCleanup = errors.New("protected restore journal owns staging cleanup")
	removeAllPKIRestorePath         = os.RemoveAll
)

// PKIRestoreStagingRegistration is an opaque durable cleanup registration.
// Restore targets create it before writing decrypted database or key material
// into staging roots and pass it back with the activation bundle.
type PKIRestoreStagingRegistration struct {
	manifestPath string
}

type PKISQLiteRestoreBundle struct {
	StagedDatabasePath  string
	AdditionalPaths     []PKIRestorePathSwap
	CleanupPaths        []string
	StagingRegistration PKIRestoreStagingRegistration
	Hooks               PKISQLiteRestoreHooks
	ExpectedLease       PKILeaseFence
}

type pkiRestoreSwapState struct {
	activePath    string
	stagedPath    string
	backupPath    string
	activeExisted bool
	activeMoved   bool
	promoted      bool
}

const pkiRestoreJournalVersion = 1

const pkiRestoreStagingManifestVersion = 1

type pkiRestoreJournal struct {
	Version            int                     `json:"version"`
	ActiveDatabasePath string                  `json:"active_database_path"`
	Swaps              []pkiRestoreJournalSwap `json:"swaps"`
	CleanupPaths       []string                `json:"cleanup_paths,omitempty"`
}

type pkiRestoreJournalSwap struct {
	ActivePath    string `json:"active_path"`
	StagedPath    string `json:"staged_path,omitempty"`
	BackupPath    string `json:"backup_path"`
	ActiveExisted bool   `json:"active_existed"`
}

type pkiRestoreStagingManifest struct {
	Version            int      `json:"version"`
	ActiveDatabasePath string   `json:"active_database_path"`
	CleanupPaths       []string `json:"cleanup_paths"`
}

// ActivatePKISQLiteRestoreBundle is the process-level storage switch used by
// protected restore after its caller has entered maintenance mode. It closes
// SQLite, swaps the database and vault paths, reopens the same GormStore
// pointer, and rolls every path back before returning any error. Hooks let the
// vault reload participate in the same rollback boundary.
func (s *GormStore) ActivatePKISQLiteRestoreBundle(ctx context.Context, bundle PKISQLiteRestoreBundle) error {
	if s == nil || s.driver != "sqlite" || s.transactionScoped {
		return fmt.Errorf("%w: protected restore requires a root SQLite store", ErrPKIInvariant)
	}
	activeDatabasePath, err := s.sqliteDatabasePath()
	if err != nil {
		return err
	}
	stagedDatabasePath, err := cleanAbsoluteRestorePath(bundle.StagedDatabasePath)
	if err != nil {
		return err
	}
	if activeDatabasePath == stagedDatabasePath {
		return pkiInvariant("restore database staging path equals the active database")
	}
	if err := requireRegularRestoreFile(stagedDatabasePath); err != nil {
		return err
	}
	swaps := []PKIRestorePathSwap{{ActivePath: activeDatabasePath, StagedPath: stagedDatabasePath}}
	for _, additional := range bundle.AdditionalPaths {
		active, err := cleanAbsoluteRestorePath(additional.ActivePath)
		if err != nil {
			return err
		}
		staged, err := cleanAbsoluteRestorePath(additional.StagedPath)
		if err != nil {
			return err
		}
		if active == staged || active == activeDatabasePath || staged == stagedDatabasePath {
			return pkiInvariant("restore path swap overlaps the database or itself")
		}
		if _, err := os.Lstat(staged); err != nil {
			return fmt.Errorf("inspect staged restore path: %w", err)
		}
		swaps = append(swaps, PKIRestorePathSwap{ActivePath: active, StagedPath: staged})
	}
	if bundle.Hooks.BeforeSwap != nil {
		if err := bundle.Hooks.BeforeSwap(); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	if s.databaseLifecycle == nil || s.databaseLifecycle.group == nil {
		return pkiInvariant("protected restore database lifecycle gate is unavailable")
	}
	group := s.databaseLifecycle.group
	group.write.Lock()
	defer group.write.Unlock()
	s.sqliteWrite.Lock()
	defer s.sqliteWrite.Unlock()
	group.mu.Lock()
	defer group.mu.Unlock()
	if err := checkpointPKIRestoreGroupLocked(ctx, group); err != nil {
		return fmt.Errorf("checkpoint active SQLite before protected restore: %w", err)
	}
	if err := requirePKISQLiteRestoreLeaseFence(ctx, s.databaseLifecycle.pool, bundle.ExpectedLease); err != nil {
		return fmt.Errorf("authorize protected restore activation: %w", err)
	}

	if group.processLock != nil {
		if err := group.processLock.Close(); err != nil {
			return fmt.Errorf("release shared protected restore lock: %w", err)
		}
		group.processLock = nil
	}
	exclusiveLock, err := acquirePKIRestoreProcessLock(ctx, activeDatabasePath, true)
	if err != nil {
		sharedLock, sharedErr := acquirePKIRestoreProcessLock(context.Background(), activeDatabasePath, false)
		group.processLock = sharedLock
		return errors.Join(fmt.Errorf("acquire exclusive protected restore lock: %w", err), sharedErr)
	}
	group.processLock = exclusiveLock
	// Acquiring the cross-process lock may have blocked behind another live
	// instance. Re-check the lease from the still-open active database so a
	// lease that expired or changed hands while waiting can never authorize a
	// journal or path swap.
	if err := checkpointPKIRestoreGroupLocked(ctx, group); err != nil {
		return errors.Join(fmt.Errorf("checkpoint active SQLite after protected restore lock wait: %w", err),
			downgradePKIRestoreGroupLock(ctx, group))
	}
	if err := requirePKISQLiteRestoreLeaseFence(ctx, s.databaseLifecycle.pool, bundle.ExpectedLease); err != nil {
		return errors.Join(fmt.Errorf("re-authorize protected restore activation: %w", err), downgradePKIRestoreGroupLock(ctx, group))
	}
	if err := recoverPKIRestoreCleanupManifest(activeDatabasePath); err != nil {
		return errors.Join(fmt.Errorf("finish previous protected restore cleanup: %w", err), downgradePKIRestoreGroupLock(ctx, group))
	}
	if bundle.Hooks.AfterCleanupRecovery != nil {
		if err := bundle.Hooks.AfterCleanupRecovery(); err != nil {
			return errors.Join(err, downgradePKIRestoreGroupLock(ctx, group))
		}
	}
	// Cleanup recovery is filesystem work with no bounded duration. Checkpoint
	// and authorize again immediately before closing the live pool so an expired
	// or transferred lease can never cross that gap into journal creation.
	if err := checkpointPKIRestoreGroupLocked(ctx, group); err != nil {
		return errors.Join(fmt.Errorf("checkpoint active SQLite after protected restore cleanup: %w", err),
			downgradePKIRestoreGroupLock(ctx, group))
	}
	if err := requirePKISQLiteRestoreLeaseFence(ctx, s.databaseLifecycle.pool, bundle.ExpectedLease); err != nil {
		return errors.Join(fmt.Errorf("re-authorize protected restore after cleanup: %w", err), downgradePKIRestoreGroupLock(ctx, group))
	}
	if err := closePKIRestoreGroupLocked(group); err != nil {
		reopenErr := reopenPKIRestoreGroupLocked(group)
		return errors.Join(fmt.Errorf("close active SQLite before protected restore: %w", err), reopenErr, downgradePKIRestoreGroupLock(ctx, group))
	}

	states := make([]pkiRestoreSwapState, 0, len(swaps)+2)
	backupSuffix := fmt.Sprintf(".pki-restore-backup-%d-%d", os.Getpid(), time.Now().UTC().UnixNano())
	for _, swap := range swaps {
		state := pkiRestoreSwapState{
			activePath: swap.ActivePath, stagedPath: swap.StagedPath, backupPath: swap.ActivePath + backupSuffix,
		}
		if _, statErr := os.Lstat(state.activePath); statErr == nil {
			state.activeExisted = true
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return errors.Join(statErr, downgradePKIRestoreGroupLock(ctx, group), reopenPKIRestoreGroupLocked(group))
		}
		states = append(states, state)
	}
	for _, sidecar := range []string{"-wal", "-shm"} {
		state := pkiRestoreSwapState{
			activePath: activeDatabasePath + sidecar,
			backupPath: activeDatabasePath + sidecar + backupSuffix,
		}
		if _, statErr := os.Lstat(state.activePath); statErr == nil {
			state.activeExisted = true
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return errors.Join(statErr, downgradePKIRestoreGroupLock(ctx, group), reopenPKIRestoreGroupLocked(group))
		}
		states = append(states, state)
	}
	journal := restoreJournalFromStates(activeDatabasePath, states)
	journal.CleanupPaths = append([]string(nil), bundle.CleanupPaths...)
	if err := validatePKIRestoreStagingRegistration(activeDatabasePath, bundle.StagingRegistration, journal.CleanupPaths); err != nil {
		return errors.Join(err, downgradePKIRestoreGroupLock(ctx, group), reopenPKIRestoreGroupLocked(group))
	}
	journalPath := pkiRestoreJournalPath(activeDatabasePath)
	if err := writePKIRestoreJournal(journalPath, journal); err != nil {
		return errors.Join(err, downgradePKIRestoreGroupLock(ctx, group), reopenPKIRestoreGroupLocked(group))
	}

	rollback := func(cause error) error {
		closeErr := closePKIRestoreGroupLocked(group)
		rollbackErr := rollbackPKIRestoreJournal(journal)
		reopenErr := reopenPKIRestoreGroupLocked(group)
		var hookErr error
		if bundle.Hooks.AfterRollback != nil {
			hookErr = bundle.Hooks.AfterRollback()
		}
		lockErr := downgradePKIRestoreGroupLock(ctx, group)
		var journalErr error
		if closeErr == nil && rollbackErr == nil && reopenErr == nil && hookErr == nil && lockErr == nil {
			journalErr = removePKIRestoreJournalArtifacts(journalPath)
		}
		return errors.Join(ErrPKIRestoreJournalOwnsCleanup, cause, closeErr, rollbackErr, reopenErr, hookErr, lockErr, journalErr)
	}

	if err := promotePKIRestorePaths(states); err != nil {
		return rollback(err)
	}
	if bundle.Hooks.BeforeReopen != nil {
		if err := bundle.Hooks.BeforeReopen(); err != nil {
			return rollback(err)
		}
	}
	if err := reopenPKIRestoreGroupLocked(group); err != nil {
		return rollback(fmt.Errorf("reopen restored SQLite: %w", err))
	}
	if bundle.Hooks.AfterSwap != nil {
		if err := bundle.Hooks.AfterSwap(); err != nil {
			return rollback(err)
		}
	}
	if err := writePKIRestoreCommitMarker(journalPath); err != nil {
		return rollback(err)
	}
	var cleanupErr error
	if bundle.Hooks.AfterCommit != nil {
		cleanupErr = bundle.Hooks.AfterCommit()
	}
	for _, state := range states {
		if !state.activeMoved {
			continue
		}
		if err := removeExactPKIRestoreBackup(state.backupPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			cleanupErr = errors.Join(cleanupErr, err)
		}
		if err := syncPKIRestoreParent(state.activePath); err != nil {
			cleanupErr = errors.Join(cleanupErr, err)
		}
	}
	if cleanupErr == nil {
		cleanupErr = cleanupPKIRestoreStaging(journal)
	}
	if cleanupErr == nil {
		cleanupErr = removePKIRestoreJournalArtifacts(journalPath)
	}
	if cleanupErr == nil {
		cleanupErr = cleanupPKIRestoreStagingRegistration(activeDatabasePath, bundle.StagingRegistration)
	}
	lockErr := downgradePKIRestoreGroupLock(ctx, group)
	committedErr := errors.Join(cleanupErr, lockErr)
	if committedErr != nil {
		return errors.Join(ErrPKIRestoreCleanupPending, ErrPKIRestoreJournalOwnsCleanup, committedErr)
	}
	return nil
}

func preparePKIRestoreLifecycleGroup(ctx context.Context, group *databaseLifecycleGroup) error {
	if group == nil || group.databasePath == "" {
		return nil
	}
	if group.processLock != nil {
		return nil
	}
	sharedLock, err := acquirePKIRestoreProcessLock(ctx, group.databasePath, false)
	if err != nil {
		return err
	}
	journalPath := pkiRestoreJournalPath(group.databasePath)
	journalExists, err := pkiRestoreArtifactExists(journalPath)
	if err != nil {
		_ = sharedLock.Close()
		return err
	}
	markerExists, err := pkiRestoreArtifactExists(pkiRestoreCommitMarkerPath(journalPath))
	if err != nil {
		_ = sharedLock.Close()
		return err
	}
	cleanupExists, err := pkiRestoreArtifactExists(pkiRestoreCleanupManifestPath(journalPath))
	if err != nil {
		_ = sharedLock.Close()
		return err
	}
	stagingManifests, err := listPKIRestoreStagingManifestPaths(group.databasePath)
	if err != nil {
		_ = sharedLock.Close()
		return err
	}
	if !journalExists && !markerExists && !cleanupExists && len(stagingManifests) == 0 {
		group.processLock = sharedLock
		return nil
	}
	if err := sharedLock.Close(); err != nil {
		return err
	}
	exclusiveLock, err := acquirePKIRestoreProcessLock(ctx, group.databasePath, true)
	if err != nil {
		return err
	}
	if err := recoverPKIRestoreJournal(group.databasePath); err != nil {
		_ = exclusiveLock.Close()
		return err
	}
	if err := exclusiveLock.Downgrade(ctx); err != nil {
		_ = exclusiveLock.Close()
		return err
	}
	group.processLock = exclusiveLock
	return nil
}

func checkpointPKIRestoreGroupLocked(ctx context.Context, group *databaseLifecycleGroup) error {
	var result error
	for member := range group.members {
		if member.writeDB != nil {
			if sqlDB, err := member.writeDB.DB(); err == nil {
				_, err = sqlDB.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)")
				result = errors.Join(result, err)
			} else {
				result = errors.Join(result, err)
			}
		}
		if member.databaseLifecycle != nil && !member.databaseLifecycle.closed {
			if connector, ok := member.databaseLifecycle.pool.(gorm.GetDBConnector); ok {
				if sqlDB, err := connector.GetDBConn(); err == nil {
					_, err = sqlDB.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)")
					result = errors.Join(result, err)
				} else {
					result = errors.Join(result, err)
				}
			}
		}
	}
	return result
}

func closePKIRestoreGroupLocked(group *databaseLifecycleGroup) error {
	var result error
	for member := range group.members {
		result = errors.Join(result, member.closeDatabaseHandlesLocked())
	}
	return result
}

func reopenPKIRestoreGroupLocked(group *databaseLifecycleGroup) error {
	var result error
	for member := range group.members {
		if member.databaseLifecycle == nil || !member.databaseLifecycle.closed {
			continue
		}
		result = errors.Join(result, member.reopenDatabaseLocked())
	}
	return result
}

func downgradePKIRestoreGroupLock(ctx context.Context, group *databaseLifecycleGroup) error {
	if group == nil || group.databasePath == "" {
		return nil
	}
	if group.processLock == nil {
		sharedLock, err := acquirePKIRestoreProcessLock(ctx, group.databasePath, false)
		if err != nil {
			return err
		}
		group.processLock = sharedLock
		return nil
	}
	return group.processLock.Downgrade(ctx)
}

func requirePKISQLiteRestoreLeaseFence(ctx context.Context, pool gorm.ConnPool, fence PKILeaseFence) error {
	fence.PKIDomainID = strings.TrimSpace(fence.PKIDomainID)
	fence.InstanceID = strings.TrimSpace(fence.InstanceID)
	fence.LeaseTerm = strings.TrimSpace(fence.LeaseTerm)
	if pool == nil || fence.PKIDomainID == "" || fence.PKIEpoch < 0 || fence.InstanceID == "" ||
		fence.LeaseTerm == "" || fence.LeaseDeadline.IsZero() {
		return pkiInvariant("PKI lease fence fields are incomplete")
	}
	var matches int
	err := pool.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM pki_settings AS settings
		JOIN pki_instance_lease AS lease
		  ON lease.id = ?
		 AND lease.pki_domain_id = settings.pki_domain_id
		 AND lease.pki_epoch = settings.pki_epoch
		WHERE settings.id = ?
		  AND settings.pki_domain_id = ?
		  AND settings.pki_epoch = ?
		  AND lease.instance_id = ?
		  AND lease.lease_term = ?
		  AND lease.state = ?
		  AND lease.lease_deadline = ?
		  AND lease.lease_deadline > CURRENT_TIMESTAMP`,
		PKILeaseSingletonID, PKISettingsSingletonID, fence.PKIDomainID, fence.PKIEpoch,
		fence.InstanceID, fence.LeaseTerm, PKIInstanceLeaseStateHeld, fence.LeaseDeadline,
	).Scan(&matches)
	if err != nil {
		return err
	}
	if matches != 1 {
		return ErrPKILeaseFence
	}
	return nil
}

func promotePKIRestorePaths(states []pkiRestoreSwapState) error {
	for index := range states {
		state := &states[index]
		if _, err := os.Lstat(state.backupPath); err == nil {
			return pkiInvariant("protected restore backup path already exists")
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if _, err := os.Lstat(state.activePath); err == nil {
			if err := renamePKIRestorePath(state.activePath, state.backupPath); err != nil {
				return fmt.Errorf("stage active restore path %q: %w", state.activePath, err)
			}
			if err := syncPKIRestoreParent(state.activePath); err != nil {
				return err
			}
			state.activeMoved = true
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if state.stagedPath == "" {
			continue
		}
		if err := renamePKIRestorePath(state.stagedPath, state.activePath); err != nil {
			return fmt.Errorf("promote protected restore path %q: %w", state.activePath, err)
		}
		if err := syncPKIRestoreParent(state.activePath); err != nil {
			return err
		}
		if filepath.Dir(state.stagedPath) != filepath.Dir(state.activePath) {
			if err := syncPKIRestoreParent(state.stagedPath); err != nil {
				return err
			}
		}
		state.promoted = true
	}
	return nil
}

func rollbackPKIRestorePaths(states []pkiRestoreSwapState) error {
	var result error
	for index := len(states) - 1; index >= 0; index-- {
		state := &states[index]
		if state.promoted {
			if _, err := os.Lstat(state.stagedPath); errors.Is(err, os.ErrNotExist) {
				result = errors.Join(result, renamePKIRestorePath(state.activePath, state.stagedPath))
			} else if err != nil {
				result = errors.Join(result, err)
			}
		}
		if state.activeMoved {
			if _, err := os.Lstat(state.activePath); errors.Is(err, os.ErrNotExist) {
				result = errors.Join(result, renamePKIRestorePath(state.backupPath, state.activePath))
			} else if err != nil {
				result = errors.Join(result, err)
			}
		}
	}
	return result
}

func restoreJournalFromStates(activeDatabasePath string, states []pkiRestoreSwapState) pkiRestoreJournal {
	journal := pkiRestoreJournal{
		Version: pkiRestoreJournalVersion, ActiveDatabasePath: activeDatabasePath,
		Swaps: make([]pkiRestoreJournalSwap, 0, len(states)),
	}
	for _, state := range states {
		journal.Swaps = append(journal.Swaps, pkiRestoreJournalSwap{
			ActivePath: state.activePath, StagedPath: state.stagedPath,
			BackupPath: state.backupPath, ActiveExisted: state.activeExisted,
		})
	}
	return journal
}

func pkiRestoreJournalPath(activeDatabasePath string) string {
	return activeDatabasePath + ".pki-restore-journal.json"
}

func pkiRestoreCommitMarkerPath(journalPath string) string {
	return journalPath + ".committed"
}

func pkiRestoreCleanupManifestPath(journalPath string) string {
	return journalPath + ".cleanup-pending"
}

func pkiRestoreStagingManifestPrefix(activeDatabasePath string) string {
	return pkiRestoreJournalPath(activeDatabasePath) + ".staging-"
}

func pkiRestoreDeletionTombstonePath(path string) string {
	return path + ".pki-delete"
}

func writePKIRestoreJournal(path string, journal pkiRestoreJournal) error {
	if err := validatePKIRestoreJournal(journal, journal.ActiveDatabasePath); err != nil {
		return err
	}
	if _, err := os.Lstat(path); err == nil {
		return pkiInvariant("a protected restore journal already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	encoded, err := json.Marshal(journal)
	if err != nil {
		return err
	}
	return writeAtomicPKIRestoreFile(path, encoded)
}

func writePKIRestoreCommitMarker(journalPath string) error {
	return writeAtomicPKIRestoreFile(pkiRestoreCommitMarkerPath(journalPath), []byte("committed\n"))
}

func writeAtomicPKIRestoreFile(path string, payload []byte) error {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".pki-restore-journal-")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(payload); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if _, err := os.Lstat(path); err == nil {
		return pkiInvariant("protected restore journal artifact already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := renamePKIRestorePath(temporaryPath, path); err != nil {
		return err
	}
	return syncPKIRestoreParent(path)
}

func validatePKIRestoreJournal(journal pkiRestoreJournal, expectedDatabasePath string) error {
	expectedDatabasePath, err := cleanAbsoluteRestorePath(expectedDatabasePath)
	if err != nil {
		return err
	}
	if journal.Version != pkiRestoreJournalVersion || journal.ActiveDatabasePath != expectedDatabasePath || len(journal.Swaps) < 1 ||
		journal.Swaps[0].ActivePath != expectedDatabasePath {
		return pkiInvariant("protected restore journal header is invalid")
	}
	seen := make(map[string]struct{}, len(journal.Swaps)*3)
	for _, swap := range journal.Swaps {
		active, err := cleanAbsoluteRestorePath(swap.ActivePath)
		if err != nil || active != swap.ActivePath {
			return pkiInvariant("protected restore journal active path is invalid")
		}
		backup, err := cleanAbsoluteRestorePath(swap.BackupPath)
		if err != nil || backup != swap.BackupPath || !strings.HasPrefix(filepath.Base(backup), filepath.Base(active)+".pki-restore-backup-") {
			return pkiInvariant("protected restore journal backup path is invalid")
		}
		paths := []string{active, backup}
		if swap.StagedPath != "" {
			staged, err := cleanAbsoluteRestorePath(swap.StagedPath)
			if err != nil || staged != swap.StagedPath || staged == active {
				return pkiInvariant("protected restore journal staged path is invalid")
			}
			paths = append(paths, staged)
		}
		for _, path := range paths {
			if _, duplicate := seen[path]; duplicate {
				return pkiInvariant("protected restore journal paths overlap")
			}
			seen[path] = struct{}{}
		}
	}
	cleanupRoots := make([]string, 0, len(journal.CleanupPaths))
	for _, cleanupPath := range journal.CleanupPaths {
		cleaned, err := cleanAbsoluteRestorePath(cleanupPath)
		if err != nil || cleaned != cleanupPath || !strings.HasPrefix(filepath.Base(cleaned), ".pki-restore-") ||
			!strings.Contains(filepath.Base(cleaned), "stage-") {
			return pkiInvariant("protected restore journal cleanup path is invalid")
		}
		if _, duplicate := seen[cleaned]; duplicate {
			return pkiInvariant("protected restore journal cleanup paths overlap restore paths")
		}
		for _, previous := range cleanupRoots {
			if restorePathDescendsFrom(cleaned, previous) || restorePathDescendsFrom(previous, cleaned) {
				return pkiInvariant("protected restore journal cleanup paths overlap")
			}
		}
		containsStagedPath := false
		for _, swap := range journal.Swaps {
			if swap.StagedPath != "" && restorePathDescendsFrom(swap.StagedPath, cleaned) {
				containsStagedPath = true
				break
			}
		}
		if !containsStagedPath {
			return pkiInvariant("protected restore journal cleanup path does not own a staged path")
		}
		cleanupRoots = append(cleanupRoots, cleaned)
	}
	return nil
}

func validatePKIRestoreCleanupRoots(cleanupPaths []string) ([]string, error) {
	cleanedPaths := make([]string, 0, len(cleanupPaths))
	for _, cleanupPath := range cleanupPaths {
		cleaned, err := cleanAbsoluteRestorePath(cleanupPath)
		if err != nil || cleaned != cleanupPath || !strings.HasPrefix(filepath.Base(cleaned), ".pki-restore-") ||
			!strings.Contains(filepath.Base(cleaned), "stage-") {
			return nil, pkiInvariant("protected restore staging cleanup path is invalid")
		}
		for _, previous := range cleanedPaths {
			if cleaned == previous || restorePathDescendsFrom(cleaned, previous) || restorePathDescendsFrom(previous, cleaned) {
				return nil, pkiInvariant("protected restore staging cleanup paths overlap")
			}
		}
		cleanedPaths = append(cleanedPaths, cleaned)
	}
	return cleanedPaths, nil
}

func validatePKIRestoreStagingManifest(
	manifest pkiRestoreStagingManifest,
	expectedDatabasePath string,
) error {
	expectedDatabasePath, err := cleanAbsoluteRestorePath(expectedDatabasePath)
	if err != nil {
		return err
	}
	if manifest.Version != pkiRestoreStagingManifestVersion || manifest.ActiveDatabasePath != expectedDatabasePath ||
		len(manifest.CleanupPaths) == 0 {
		return pkiInvariant("protected restore staging manifest header is invalid")
	}
	_, err = validatePKIRestoreCleanupRoots(manifest.CleanupPaths)
	return err
}

func validatePKIRestoreStagingManifestPath(activeDatabasePath, manifestPath string) error {
	manifestPath, err := cleanAbsoluteRestorePath(manifestPath)
	if err != nil {
		return err
	}
	prefix := pkiRestoreStagingManifestPrefix(activeDatabasePath)
	if filepath.Dir(manifestPath) != filepath.Dir(prefix) || !strings.HasPrefix(filepath.Base(manifestPath), filepath.Base(prefix)) {
		return pkiInvariant("protected restore staging manifest path is invalid")
	}
	suffix := strings.TrimPrefix(filepath.Base(manifestPath), filepath.Base(prefix))
	decoded, err := hex.DecodeString(suffix)
	if err != nil || len(decoded) != 16 {
		return pkiInvariant("protected restore staging manifest identifier is invalid")
	}
	return nil
}

func readPKIRestoreStagingManifest(path, expectedDatabasePath string) (pkiRestoreStagingManifest, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return pkiRestoreStagingManifest{}, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 1<<20 {
		return pkiRestoreStagingManifest{}, pkiInvariant("protected restore staging manifest file is invalid")
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return pkiRestoreStagingManifest{}, err
	}
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	var manifest pkiRestoreStagingManifest
	if err := decoder.Decode(&manifest); err != nil {
		return pkiRestoreStagingManifest{}, pkiInvariant("protected restore staging manifest JSON is invalid")
	}
	if err := validatePKIRestoreStagingManifest(manifest, expectedDatabasePath); err != nil {
		return pkiRestoreStagingManifest{}, err
	}
	return manifest, nil
}

// RegisterPKIRestoreStagingCleanup durably records empty, restricted staging
// roots before a restore target writes plaintext database or key material.
func (s *GormStore) RegisterPKIRestoreStagingCleanup(
	ctx context.Context,
	cleanupPaths []string,
) (PKIRestoreStagingRegistration, error) {
	if err := ctx.Err(); err != nil {
		return PKIRestoreStagingRegistration{}, err
	}
	if s == nil || s.driver != "sqlite" || s.transactionScoped {
		return PKIRestoreStagingRegistration{}, pkiInvariant("staging cleanup registration requires a root SQLite store")
	}
	activeDatabasePath, err := s.sqliteDatabasePath()
	if err != nil {
		return PKIRestoreStagingRegistration{}, err
	}
	cleanedPaths, err := validatePKIRestoreCleanupRoots(cleanupPaths)
	if err != nil || len(cleanedPaths) == 0 {
		if err == nil {
			err = pkiInvariant("staging cleanup registration is empty")
		}
		return PKIRestoreStagingRegistration{}, err
	}
	manifest := pkiRestoreStagingManifest{
		Version: pkiRestoreStagingManifestVersion, ActiveDatabasePath: activeDatabasePath, CleanupPaths: cleanedPaths,
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return PKIRestoreStagingRegistration{}, err
	}
	for attempt := 0; attempt < 4; attempt++ {
		identifier := make([]byte, 16)
		if _, err := rand.Read(identifier); err != nil {
			return PKIRestoreStagingRegistration{}, err
		}
		manifestPath := pkiRestoreStagingManifestPrefix(activeDatabasePath) + hex.EncodeToString(identifier)
		if err := writeAtomicPKIRestoreFile(manifestPath, encoded); err != nil {
			if _, statErr := os.Lstat(manifestPath); statErr == nil {
				continue
			}
			return PKIRestoreStagingRegistration{}, err
		}
		return PKIRestoreStagingRegistration{manifestPath: manifestPath}, nil
	}
	return PKIRestoreStagingRegistration{}, pkiInvariant("allocate protected restore staging manifest")
}

// CleanupPKIRestoreStagingRegistration is idempotent. The manifest remains
// discoverable even after a successful deletion. A later lifecycle open only
// removes it after observing that every registered path and tombstone was
// already absent, so deletion on another volume cannot become untracked after
// a crash on platforms without durable directory flushes.
func (s *GormStore) CleanupPKIRestoreStagingRegistration(registration PKIRestoreStagingRegistration) error {
	if s == nil || s.driver != "sqlite" || s.transactionScoped {
		return pkiInvariant("staging cleanup registration requires a root SQLite store")
	}
	activeDatabasePath, err := s.sqliteDatabasePath()
	if err != nil {
		return err
	}
	return cleanupPKIRestoreStagingRegistration(activeDatabasePath, registration)
}

func validatePKIRestoreStagingRegistration(
	activeDatabasePath string,
	registration PKIRestoreStagingRegistration,
	cleanupPaths []string,
) error {
	if registration.manifestPath == "" {
		return nil
	}
	if err := validatePKIRestoreStagingManifestPath(activeDatabasePath, registration.manifestPath); err != nil {
		return err
	}
	manifest, err := readPKIRestoreStagingManifest(registration.manifestPath, activeDatabasePath)
	if err != nil {
		return err
	}
	if len(manifest.CleanupPaths) != len(cleanupPaths) {
		return pkiInvariant("protected restore staging registration does not match activation cleanup paths")
	}
	for index := range cleanupPaths {
		if manifest.CleanupPaths[index] != cleanupPaths[index] {
			return pkiInvariant("protected restore staging registration does not match activation cleanup paths")
		}
	}
	return nil
}

func listPKIRestoreStagingManifestPaths(activeDatabasePath string) ([]string, error) {
	prefix := pkiRestoreStagingManifestPrefix(activeDatabasePath)
	directory := filepath.Dir(prefix)
	basePrefix := filepath.Base(prefix)
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(entries))
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), basePrefix) {
			continue
		}
		match := filepath.Join(directory, entry.Name())
		semanticPath := strings.TrimSuffix(match, ".pki-delete")
		if err := validatePKIRestoreStagingManifestPath(activeDatabasePath, semanticPath); err != nil {
			return nil, err
		}
		if _, duplicate := seen[semanticPath]; duplicate {
			continue
		}
		seen[semanticPath] = struct{}{}
		paths = append(paths, semanticPath)
	}
	slices.Sort(paths)
	return paths, nil
}

func cleanupPKIRestoreStagingRegistration(
	activeDatabasePath string,
	registration PKIRestoreStagingRegistration,
) error {
	manifest, exists, err := readPKIRestoreStagingRegistration(activeDatabasePath, registration)
	if err != nil || !exists {
		return err
	}
	return cleanupPKIRestoreStagingManifestPaths(manifest)
}

func readPKIRestoreStagingRegistration(
	activeDatabasePath string,
	registration PKIRestoreStagingRegistration,
) (pkiRestoreStagingManifest, bool, error) {
	manifestPath := registration.manifestPath
	if manifestPath == "" {
		return pkiRestoreStagingManifest{}, false, nil
	}
	if err := validatePKIRestoreStagingManifestPath(activeDatabasePath, manifestPath); err != nil {
		return pkiRestoreStagingManifest{}, false, err
	}
	manifestExists, err := pkiRestorePathExists(manifestPath)
	if err != nil {
		return pkiRestoreStagingManifest{}, false, err
	}
	tombstonePath := pkiRestoreDeletionTombstonePath(manifestPath)
	tombstoneExists, err := pkiRestorePathExists(tombstonePath)
	if err != nil {
		return pkiRestoreStagingManifest{}, false, err
	}
	if manifestExists && tombstoneExists {
		return pkiRestoreStagingManifest{}, false, pkiInvariant("protected restore staging manifest is ambiguous")
	}
	if !manifestExists && !tombstoneExists {
		return pkiRestoreStagingManifest{}, false, nil
	}
	readPath := manifestPath
	if tombstoneExists {
		readPath = tombstonePath
	}
	manifest, err := readPKIRestoreStagingManifest(readPath, activeDatabasePath)
	if err != nil {
		return pkiRestoreStagingManifest{}, false, err
	}
	return manifest, true, nil
}

func cleanupPKIRestoreStagingManifestPaths(manifest pkiRestoreStagingManifest) error {
	var result error
	for _, cleanupPath := range manifest.CleanupPaths {
		result = errors.Join(result, CleanupPKIRestoreStagingPath(cleanupPath))
	}
	return result
}

func recoverPKIRestoreStagingRegistration(
	activeDatabasePath string,
	registration PKIRestoreStagingRegistration,
	cleanupWasAlreadyAbsent bool,
) error {
	manifest, exists, err := readPKIRestoreStagingRegistration(activeDatabasePath, registration)
	if err != nil || !exists {
		return err
	}
	if !cleanupWasAlreadyAbsent {
		// Keep the registration for another process lifecycle. A crash after
		// this cleanup can otherwise resurrect a cross-volume tombstone after
		// the only durable record of that path has disappeared.
		return cleanupPKIRestoreStagingManifestPaths(manifest)
	}
	return removeDurablePKIRestorePath(registration.manifestPath)
}

func snapshotPKIRestoreStagingCleanupState(activeDatabasePath string) (map[string]bool, error) {
	paths, err := listPKIRestoreStagingManifestPaths(activeDatabasePath)
	if err != nil {
		return nil, err
	}
	cleanupWasAlreadyAbsent := make(map[string]bool, len(paths))
	for _, path := range paths {
		manifest, exists, err := readPKIRestoreStagingRegistration(activeDatabasePath,
			PKIRestoreStagingRegistration{manifestPath: path})
		if err != nil {
			return nil, err
		}
		if !exists {
			continue
		}
		allAbsent := true
		for _, cleanupPath := range manifest.CleanupPaths {
			pathExists, err := pkiRestoreArtifactExists(cleanupPath)
			if err != nil {
				return nil, err
			}
			allAbsent = allAbsent && !pathExists
		}
		cleanupWasAlreadyAbsent[path] = allAbsent
	}
	return cleanupWasAlreadyAbsent, nil
}

func recoverPKIRestoreStagingManifests(
	activeDatabasePath string,
	cleanupWasAlreadyAbsent map[string]bool,
) error {
	paths, err := listPKIRestoreStagingManifestPaths(activeDatabasePath)
	if err != nil {
		return err
	}
	var result error
	for _, path := range paths {
		result = errors.Join(result, recoverPKIRestoreStagingRegistration(activeDatabasePath,
			PKIRestoreStagingRegistration{manifestPath: path}, cleanupWasAlreadyAbsent[path]))
	}
	return result
}

func restorePathDescendsFrom(path, root string) bool {
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == "." || relative == "" {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func readPKIRestoreJournal(path, expectedDatabasePath string) (pkiRestoreJournal, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return pkiRestoreJournal{}, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 1<<20 {
		return pkiRestoreJournal{}, pkiInvariant("protected restore journal file is invalid")
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return pkiRestoreJournal{}, err
	}
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	var journal pkiRestoreJournal
	if err := decoder.Decode(&journal); err != nil {
		return pkiRestoreJournal{}, pkiInvariant("protected restore journal JSON is invalid")
	}
	if err := validatePKIRestoreJournal(journal, expectedDatabasePath); err != nil {
		return pkiRestoreJournal{}, err
	}
	return journal, nil
}

func recoverPKIRestoreJournal(activeDatabasePath string) error {
	// Snapshot staging state before journal recovery touches any registered
	// path. Main-journal cleanup may remove the same cross-volume tombstones;
	// that must not make them look absent at lifecycle entry and allow their
	// last durable registration to disappear in this process.
	stagingCleanupWasAlreadyAbsent, err := snapshotPKIRestoreStagingCleanupState(activeDatabasePath)
	if err != nil {
		return err
	}
	journalPath := pkiRestoreJournalPath(activeDatabasePath)
	markerPath := pkiRestoreCommitMarkerPath(journalPath)
	journalExists, err := pkiRestorePathExists(journalPath)
	if err != nil {
		return err
	}
	markerExists, err := pkiRestoreArtifactExists(markerPath)
	if err != nil {
		return err
	}
	if !journalExists {
		if markerExists {
			if err := removeDurablePKIRestorePath(markerPath); err != nil {
				return err
			}
		}
		if err := recoverPKIRestoreCleanupManifest(activeDatabasePath); err != nil {
			return err
		}
		return recoverPKIRestoreStagingManifests(activeDatabasePath, stagingCleanupWasAlreadyAbsent)
	}
	cleanupExists, err := pkiRestoreArtifactExists(pkiRestoreCleanupManifestPath(journalPath))
	if err != nil {
		return err
	}
	if cleanupExists {
		return pkiInvariant("protected restore journal overlaps a cleanup manifest")
	}
	journal, err := readPKIRestoreJournal(journalPath, activeDatabasePath)
	if err != nil {
		return err
	}
	if markerExists {
		err = rollForwardPKIRestoreJournal(journal)
	} else {
		err = rollbackPKIRestoreJournal(journal)
	}
	if err != nil {
		return err
	}
	if err := removePKIRestoreJournalArtifacts(journalPath); err != nil {
		return err
	}
	if err := recoverPKIRestoreCleanupManifest(activeDatabasePath); err != nil {
		return err
	}
	return recoverPKIRestoreStagingManifests(activeDatabasePath, stagingCleanupWasAlreadyAbsent)
}

func rollbackPKIRestoreJournal(journal pkiRestoreJournal) error {
	for index := len(journal.Swaps) - 1; index >= 0; index-- {
		swap := journal.Swaps[index]
		activeExists, err := pkiRestorePathExists(swap.ActivePath)
		if err != nil {
			return err
		}
		backupExists, err := pkiRestorePathExists(swap.BackupPath)
		if err != nil {
			return err
		}
		stagedExists := false
		if swap.StagedPath != "" {
			stagedExists, err = pkiRestorePathExists(swap.StagedPath)
			if err != nil {
				return err
			}
		}
		if backupExists {
			if activeExists {
				if swap.StagedPath != "" {
					if stagedExists {
						return pkiInvariant("protected restore rollback paths are ambiguous")
					}
					if err := renamePKIRestorePath(swap.ActivePath, swap.StagedPath); err != nil {
						return err
					}
				} else if err := removeDurablePKIRestorePath(swap.ActivePath); err != nil {
					return err
				}
			}
			if err := renamePKIRestorePath(swap.BackupPath, swap.ActivePath); err != nil {
				return err
			}
		} else if !swap.ActiveExisted && activeExists {
			if swap.StagedPath != "" {
				if stagedExists {
					return pkiInvariant("protected restore rollback staging path already exists")
				}
				if err := renamePKIRestorePath(swap.ActivePath, swap.StagedPath); err != nil {
					return err
				}
			} else if err := removeDurablePKIRestorePath(swap.ActivePath); err != nil {
				return err
			}
		} else if swap.ActiveExisted && !activeExists {
			return pkiInvariant("protected restore rollback lost an active path")
		}
		if err := syncPKIRestoreParent(swap.ActivePath); err != nil {
			return err
		}
		if swap.StagedPath != "" && filepath.Dir(swap.StagedPath) != filepath.Dir(swap.ActivePath) {
			if err := syncPKIRestoreParent(swap.StagedPath); err != nil {
				return err
			}
		}
	}
	return cleanupPKIRestoreStaging(journal)
}

func rollForwardPKIRestoreJournal(journal pkiRestoreJournal) error {
	for index, swap := range journal.Swaps {
		activeExists, err := pkiRestorePathExists(swap.ActivePath)
		if err != nil {
			return err
		}
		if index == 0 && !activeExists {
			return pkiInvariant("committed protected restore database is missing")
		}
		if swap.StagedPath != "" {
			if stagedExists, err := pkiRestorePathExists(swap.StagedPath); err != nil {
				return err
			} else if stagedExists {
				return pkiInvariant("committed protected restore still has a staged path")
			}
		}
		if err := removeExactPKIRestoreBackup(swap.BackupPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := syncPKIRestoreParent(swap.ActivePath); err != nil {
			return err
		}
	}
	return cleanupPKIRestoreStaging(journal)
}

func cleanupPKIRestoreStaging(journal pkiRestoreJournal) error {
	var result error
	for _, path := range journal.CleanupPaths {
		result = errors.Join(result, CleanupPKIRestoreStagingPath(path))
	}
	return result
}

// CleanupPKIRestoreStagingPath applies the same strict path validation and
// durable tombstone deletion used by journal recovery. Restore targets use it
// for failures that happen before storage has accepted a bundle and written
// the main restore journal.
func CleanupPKIRestoreStagingPath(path string) error {
	cleaned, err := cleanAbsoluteRestorePath(path)
	if err != nil || cleaned != path || !strings.HasPrefix(filepath.Base(cleaned), ".pki-restore-") ||
		!strings.Contains(filepath.Base(cleaned), "stage-") {
		return pkiInvariant("refusing to remove an invalid protected restore staging path")
	}
	return removeDurablePKIRestorePath(cleaned)
}

func removePKIRestoreJournalArtifacts(journalPath string) error {
	cleanupPath := pkiRestoreCleanupManifestPath(journalPath)
	cleanupExists, err := pkiRestoreArtifactExists(cleanupPath)
	if err != nil {
		return err
	}
	if cleanupExists {
		return pkiInvariant("a protected restore cleanup manifest already exists")
	}
	if err := renamePKIRestorePath(journalPath, cleanupPath); err != nil {
		return err
	}
	if err := syncPKIRestoreParent(cleanupPath); err != nil {
		return err
	}
	markerPath := pkiRestoreCommitMarkerPath(journalPath)
	return removeDurablePKIRestorePath(markerPath)
}

func recoverPKIRestoreCleanupManifest(activeDatabasePath string) error {
	journalPath := pkiRestoreJournalPath(activeDatabasePath)
	cleanupPath := pkiRestoreCleanupManifestPath(journalPath)
	cleanupExists, err := pkiRestorePathExists(cleanupPath)
	if err != nil {
		return err
	}
	cleanupTombstone := pkiRestoreDeletionTombstonePath(cleanupPath)
	tombstoneExists, err := pkiRestorePathExists(cleanupTombstone)
	if err != nil {
		return err
	}
	if cleanupExists && tombstoneExists {
		return pkiInvariant("protected restore cleanup manifest is ambiguous")
	}
	if !cleanupExists && !tombstoneExists {
		return nil
	}
	manifestPath := cleanupPath
	if tombstoneExists {
		manifestPath = cleanupTombstone
	}
	journal, err := readPKIRestoreJournal(manifestPath, activeDatabasePath)
	if err != nil {
		return err
	}
	var result error
	for _, swap := range journal.Swaps {
		result = errors.Join(result, removePKIRestoreDeletionTombstone(swap.ActivePath))
		result = errors.Join(result, removeExactPKIRestoreBackup(swap.BackupPath))
	}
	result = errors.Join(result, cleanupPKIRestoreStaging(journal))
	result = errors.Join(result, removeDurablePKIRestorePath(pkiRestoreCommitMarkerPath(journalPath)))
	if result != nil {
		return result
	}
	return removeDurablePKIRestorePath(cleanupPath)
}

func pkiRestoreArtifactExists(path string) (bool, error) {
	exists, err := pkiRestorePathExists(path)
	if err != nil || exists {
		return exists, err
	}
	return pkiRestorePathExists(pkiRestoreDeletionTombstonePath(path))
}

func removePKIRestoreDeletionTombstone(path string) error {
	tombstone := pkiRestoreDeletionTombstonePath(path)
	if err := removeAllPKIRestorePath(tombstone); err != nil {
		return err
	}
	return syncPKIRestoreParent(tombstone)
}

// removeDurablePKIRestorePath first moves the semantic path to a deterministic
// tombstone with the platform's durable rename primitive. The cleanup manifest
// remains until a later lifecycle open has retried tombstone deletion, so a
// Windows crash cannot resurrect a sensitive backup or staging directory under
// its active name after the restore journal has disappeared.
func removeDurablePKIRestorePath(path string) error {
	path, err := cleanAbsoluteRestorePath(path)
	if err != nil {
		return err
	}
	tombstone := pkiRestoreDeletionTombstonePath(path)
	pathExists, err := pkiRestorePathExists(path)
	if err != nil {
		return err
	}
	tombstoneExists, err := pkiRestorePathExists(tombstone)
	if err != nil {
		return err
	}
	if tombstoneExists {
		if err := removeAllPKIRestorePath(tombstone); err != nil {
			return err
		}
		if err := syncPKIRestoreParent(tombstone); err != nil {
			return err
		}
	}
	if pathExists {
		if err := renamePKIRestorePath(path, tombstone); err != nil {
			return err
		}
		if err := syncPKIRestoreParent(path); err != nil {
			return err
		}
	}
	if err := removeAllPKIRestorePath(tombstone); err != nil {
		return err
	}
	return syncPKIRestoreParent(tombstone)
}

func pkiRestorePathExists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func syncPKIRestoreParent(path string) error {
	return syncPKIRestoreDirectory(path)
}

func (s *GormStore) closeDatabaseHandlesLocked() error {
	var result error
	if s.writeDB != nil {
		if sqlDB, err := s.writeDB.DB(); err == nil {
			result = errors.Join(result, sqlDB.Close())
		} else {
			result = errors.Join(result, err)
		}
		s.writeDB = nil
	}
	if s.databaseLifecycle != nil && !s.databaseLifecycle.closed {
		result = errors.Join(result, closeGormConnPool(s.databaseLifecycle.pool))
		s.databaseLifecycle.closed = true
	}
	return result
}

func (s *GormStore) reopenDatabaseLocked() error {
	dialector, err := resolveDialector(s.driver, s.storeConfig)
	if err != nil {
		return err
	}
	db, err := gorm.Open(dialector, &gorm.Config{})
	if err != nil {
		return err
	}
	if s.databaseLifecycle == nil {
		if sqlDB, dbErr := db.DB(); dbErr == nil {
			_ = sqlDB.Close()
		}
		return pkiInvariant("database lifecycle gate is unavailable")
	}
	s.databaseLifecycle.pool = db.ConnPool
	s.databaseLifecycle.closed = false
	s.writeDB = nil
	return nil
}

func (s *GormStore) sqliteDatabasePath() (string, error) {
	dsn, err := resolveSQLiteDSN(s.storeConfig)
	if err != nil {
		return "", err
	}
	return sqliteDatabasePathFromDSN(dsn)
}

func (s *GormStore) PKISQLiteDatabasePath() (string, error) {
	if s == nil || s.driver != "sqlite" || s.transactionScoped {
		return "", pkiInvariant("protected restore requires a root SQLite store")
	}
	return s.sqliteDatabasePath()
}

func sqliteDatabasePathFromDSN(dsn string) (string, error) {
	value := strings.TrimSpace(dsn)
	if query := strings.Index(value, "?"); query >= 0 {
		value = value[:query]
	}
	value = strings.TrimPrefix(value, "file:")
	decoded, err := url.PathUnescape(value)
	if err != nil {
		return "", err
	}
	if decoded == "" || decoded == ":memory:" || strings.Contains(strings.ToLower(decoded), "mode=memory") {
		return "", pkiInvariant("protected restore requires a file-backed SQLite database")
	}
	return canonicalSQLiteDatabasePath(decoded)
}

func canonicalSQLiteDatabasePath(path string) (string, error) {
	absolute, err := cleanAbsoluteRestorePath(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if errors.Is(err, os.ErrNotExist) {
		missing := make([]string, 0, 4)
		ancestor := absolute
		for {
			if _, statErr := os.Lstat(ancestor); statErr == nil {
				resolved, err = filepath.EvalSymlinks(ancestor)
				break
			} else if !errors.Is(statErr, os.ErrNotExist) {
				return "", statErr
			}
			parent := filepath.Dir(ancestor)
			if parent == ancestor {
				return "", err
			}
			missing = append(missing, filepath.Base(ancestor))
			ancestor = parent
		}
		if err == nil {
			for index := len(missing) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, missing[index])
			}
		}
	}
	if err != nil {
		return "", err
	}
	resolved, err = cleanAbsoluteRestorePath(resolved)
	if err != nil {
		return "", err
	}
	if err := rejectAliasedPKIRestoreDatabasePath(resolved); err != nil {
		return "", err
	}
	return resolved, nil
}

func cleanAbsoluteRestorePath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", pkiInvariant("protected restore path is empty")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	absolute = filepath.Clean(absolute)
	volume := filepath.VolumeName(absolute)
	if absolute == string(filepath.Separator) || (volume != "" && absolute == volume+string(filepath.Separator)) {
		return "", pkiInvariant("protected restore path cannot be a filesystem root")
	}
	return absolute, nil
}

func requireRegularRestoreFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return pkiInvariant("staged restore database is not a regular file")
	}
	return nil
}

func removeExactPKIRestoreBackup(path string) error {
	path, err := cleanAbsoluteRestorePath(path)
	if err != nil {
		return err
	}
	if !strings.Contains(filepath.Base(path), ".pki-restore-backup-") {
		return pkiInvariant("refusing to remove a non-restore backup path")
	}
	return removeDurablePKIRestorePath(path)
}

// RecordPKIRestoreActivation writes the terminal operation into the staged
// database before it is promoted. This avoids trying to transition an
// operation from the old database after the process has safely reopened the
// restored one.
func (s *GormStore) RecordPKIRestoreActivation(ctx context.Context, operationID string, forced bool, now time.Time) error {
	operationID = strings.TrimSpace(operationID)
	if operationID == "" || now.IsZero() {
		return pkiInvariant("protected restore operation fields are incomplete")
	}
	return s.WithPKITransaction(ctx, func(tx *PKITransaction) error {
		db := tx.db
		settings, found, err := tx.GetPKISettingsForUpdate(ctx)
		if err != nil {
			return err
		}
		if !found {
			return pkiInvariant("protected restore settings are missing")
		}
		if err := db.WithContext(ctx).
			Model(&PKILifecycleJobRow{}).
			Where("kind = ? AND state IN ?", "protected_import", []string{
				PKILifecycleJobStatePending, PKILifecycleJobStateRunning, PKILifecycleJobStateBlocked,
			}).
			Updates(map[string]any{
				"phase": "superseded_by_restore", "state": PKILifecycleJobStateCancelled,
				"last_error": "superseded by a newer protected restore", "active_target_key": nil, "updated_at": now,
			}).Error; err != nil {
			return err
		}
		var existing PKILifecycleJobRow
		err = db.WithContext(ctx).Where("id = ? OR operation_id = ?", operationID, operationID).First(&existing).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			err = tx.CreatePKILifecycleJob(ctx, PKILifecycleJobRow{
				ID: operationID, PKIDomainID: settings.PKIDomainID, TargetType: "backup", TargetID: "pki",
				Kind: "protected_import", Phase: "completed", State: PKILifecycleJobStateSucceeded,
				OperationID: operationID, IdempotencyKey: "protected_import:activation:" + operationID,
				RuntimeJSON: "{}", CreatedAt: now, UpdatedAt: now,
			})
		} else if err == nil {
			err = db.WithContext(ctx).Model(&PKILifecycleJobRow{}).Where("id = ?", existing.ID).Updates(map[string]any{
				"phase": "completed", "state": PKILifecycleJobStateSucceeded, "last_error": "",
				"active_target_key": nil, "updated_at": now,
			}).Error
		}
		if err != nil {
			return err
		}
		digest := sha256.Sum256([]byte("protected-restore\x00" + operationID))
		details, _ := json.Marshal(map[string]any{"forced": forced, "atomic_reopen": true})
		return tx.AppendPKIEvent(ctx, PKIEventRow{
			ID: "pki-restore-" + hex.EncodeToString(digest[:16]), PKIDomainID: settings.PKIDomainID,
			Type: "pki.backup.restored", OccurredAt: now, Source: "restore_target", ObjectType: "pki_domain",
			ObjectID: settings.PKIDomainID, Result: "success", SecurityRevision: settings.SecurityRevision,
			DetailsJSON: string(details),
		})
	})
}
