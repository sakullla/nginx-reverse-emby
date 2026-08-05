package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type PKIBackupRestoreTargetOptions struct {
	Store           *storage.GormStore
	Vault           *PKIVault
	DataRoot        string
	MasterKeyFile   string
	Clock           func() time.Time
	ActivationHooks storage.PKISQLiteRestoreHooks
}

type ProductionPKIBackupRestoreTarget struct {
	store           *storage.GormStore
	vault           *PKIVault
	dataRoot        string
	masterKeyFile   string
	clock           func() time.Time
	activationHooks storage.PKISQLiteRestoreHooks
	mutex           sync.Mutex
}

func NewProductionPKIBackupRestoreTarget(options PKIBackupRestoreTargetOptions) (*ProductionPKIBackupRestoreTarget, error) {
	options.DataRoot = strings.TrimSpace(options.DataRoot)
	options.MasterKeyFile = strings.TrimSpace(options.MasterKeyFile)
	if options.Store == nil || options.Vault == nil || options.DataRoot == "" {
		return nil, fmt.Errorf("%w: protected restore target dependencies are required", ErrPKIBackupActivation)
	}
	absoluteRoot, err := filepath.Abs(options.DataRoot)
	if err != nil {
		return nil, err
	}
	if options.MasterKeyFile != "" && !filepath.IsAbs(options.MasterKeyFile) {
		return nil, fmt.Errorf("%w: configured restore master key path must be absolute", ErrPKIBackupActivation)
	}
	if options.Clock == nil {
		options.Clock = time.Now
	}
	return &ProductionPKIBackupRestoreTarget{
		store: options.Store, vault: options.Vault, dataRoot: filepath.Clean(absoluteRoot),
		masterKeyFile: options.MasterKeyFile, clock: options.Clock, activationHooks: options.ActivationHooks,
	}, nil
}

func (t *ProductionPKIBackupRestoreTarget) CurrentPKIBackupTarget(ctx context.Context) (PKIBackupTargetState, error) {
	raw, err := t.store.CaptureConsistentPKISQLite(ctx)
	if err != nil {
		return PKIBackupTargetState{}, err
	}
	stage, err := stagePKIBackupSQLite(ctx, raw, pkiBackupStageOptions{Sanitize: true})
	clear(raw)
	if err != nil {
		return PKIBackupTargetState{}, err
	}
	defer clear(stage.Snapshot)
	target := PKIBackupTargetState{
		SQLiteSchemaVersion: stage.SchemaVersion,
		SQLiteSchemaSHA256:  stage.SchemaSHA256,
	}
	if stage.State.Settings != nil {
		target.Initialized = true
		target.PKIDomainID = stage.State.Settings.PKIDomainID
		target.Version = PKISecurityVersion{
			PKIEpoch:         stage.State.Settings.PKIEpoch,
			SecurityRevision: stage.State.Settings.SecurityRevision,
		}
	}
	return target, nil
}

func (t *ProductionPKIBackupRestoreTarget) ActivateProtectedPKIBackup(
	ctx context.Context,
	request PKIBackupActivationRequest,
) (returnErr error) {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	cleanupPaths := make([]string, 0, 3)
	cleanupOwnedByJournal := false
	var stagingRegistration storage.PKIRestoreStagingRegistration
	stagingRegistered := false
	defer func() {
		if cleanupOwnedByJournal {
			return
		}
		if stagingRegistered {
			returnErr = errors.Join(returnErr, t.store.CleanupPKIRestoreStagingRegistration(stagingRegistration))
			return
		}
		if len(cleanupPaths) == 0 {
			return
		}
		for _, path := range cleanupPaths {
			returnErr = errors.Join(returnErr, storage.CleanupPKIRestoreStagingPath(path))
		}
	}()
	request.OperationID = strings.TrimSpace(request.OperationID)
	if request.OperationID == "" || strings.TrimSpace(request.PKIDomainID) == "" || len(request.SQLiteSnapshot) == 0 || !request.Full ||
		strings.TrimSpace(request.Lease.PKIDomainID) == "" || strings.TrimSpace(request.Lease.InstanceID) == "" ||
		strings.TrimSpace(request.Lease.LeaseTerm) == "" || request.Lease.LeaseDeadline.IsZero() {
		return fmt.Errorf("%w: protected restore activation request is incomplete", ErrPKIBackupActivation)
	}
	current, err := t.CurrentPKIBackupTarget(ctx)
	if err != nil {
		return err
	}
	if current != request.ExpectedTarget {
		return fmt.Errorf("%w: protected restore target changed during staging", ErrPKIBackupActivation)
	}
	stageOptions := pkiBackupStageOptions{}
	if request.Forced {
		version := request.Version
		stageOptions.ForceVersion = &version
	}
	staged, err := stagePKIBackupSQLite(ctx, request.SQLiteSnapshot, stageOptions)
	if err != nil {
		return err
	}
	defer clear(staged.Snapshot)
	if staged.State.Settings == nil || staged.State.Settings.PKIDomainID != request.PKIDomainID ||
		staged.State.Settings.PKIEpoch != request.Version.PKIEpoch ||
		staged.State.Settings.SecurityRevision != request.Version.SecurityRevision {
		return fmt.Errorf("%w: staged restore security version is inconsistent", ErrPKIBackupActivation)
	}
	if err := validatePKIBackupAuthorityKeys(staged.State, request.AuthorityKeys); err != nil {
		return err
	}

	activeDatabasePath, err := t.store.PKISQLiteDatabasePath()
	if err != nil {
		return err
	}
	databaseStageRoot, err := os.MkdirTemp(filepath.Dir(activeDatabasePath), ".pki-restore-db-stage-")
	if err != nil {
		return fmt.Errorf("create protected restore database staging root: %w", err)
	}
	cleanupPaths = append(cleanupPaths, databaseStageRoot)
	if err := os.Chmod(databaseStageRoot, 0o700); err != nil {
		return err
	}
	vaultStageRoot, err := os.MkdirTemp(t.dataRoot, ".pki-restore-vault-stage-")
	if err != nil {
		return fmt.Errorf("create protected restore vault staging root: %w", err)
	}
	cleanupPaths = append(cleanupPaths, vaultStageRoot)
	if err := os.Chmod(vaultStageRoot, 0o700); err != nil {
		return err
	}

	stagedMasterKey := ""
	masterStageRoot := ""
	stagingVaultConfig := PKIVaultConfig{DataRoot: vaultStageRoot}
	if t.masterKeyFile != "" {
		masterStageRoot, err = os.MkdirTemp(filepath.Dir(t.masterKeyFile), ".pki-restore-master-stage-")
		if err != nil {
			return fmt.Errorf("create protected restore master-key stage: %w", err)
		}
		cleanupPaths = append(cleanupPaths, masterStageRoot)
		if err := os.Chmod(masterStageRoot, 0o700); err != nil {
			return err
		}
		stagedMasterKey = filepath.Join(masterStageRoot, "master.key")
		stagingVaultConfig.MasterKeyFile = stagedMasterKey
	}
	stagingRegistration, err = t.store.RegisterPKIRestoreStagingCleanup(ctx, cleanupPaths)
	if err != nil {
		return fmt.Errorf("register protected restore staging cleanup: %w", err)
	}
	stagingRegistered = true
	stagedDatabase := filepath.Join(databaseStageRoot, "panel.db")
	if err := writePKIRestrictedFile(stagedDatabase, staged.Snapshot); err != nil {
		return fmt.Errorf("write protected restore SQLite stage: %w", err)
	}
	stagingVault, err := OpenPKIVault(stagingVaultConfig)
	if err != nil {
		return fmt.Errorf("create protected restore vault stage: %w", err)
	}
	stagingVaultClosed := false
	defer func() {
		if !stagingVaultClosed {
			stagingVault.Close()
		}
	}()
	authorities := make(map[string]storage.PKIAuthorityRow, len(staged.State.Authorities))
	for _, authority := range staged.State.Authorities {
		authorities[authority.ID] = authority
	}
	for _, key := range request.AuthorityKeys {
		authority, found := authorities[key.AuthorityID]
		if !found || authority.Generation != key.Generation {
			return fmt.Errorf("%w: restore authority key owner is missing", ErrPKIBackupIntegrity)
		}
		reference, err := stagingVault.SealCAKey(request.PKIDomainID, key.Generation, pkiBackupCAPurpose, key.PKCS8)
		if err != nil {
			return fmt.Errorf("seal restored authority %q: %w", key.AuthorityID, err)
		}
		if authority.EncryptedKeyRef == nil || reference != *authority.EncryptedKeyRef {
			return fmt.Errorf("%w: restored authority key reference is inconsistent", ErrPKIBackupIntegrity)
		}
		opened, err := stagingVault.OpenCAKey(reference, request.PKIDomainID, key.Generation, pkiBackupCAPurpose)
		if err != nil || !slicesEqualBytes(opened, key.PKCS8) {
			clear(opened)
			if err == nil {
				err = ErrPKIBackupIntegrity
			}
			return fmt.Errorf("verify restored authority %q: %w", key.AuthorityID, err)
		}
		clear(opened)
	}
	stagingVault.Close()
	stagingVaultClosed = true

	stagedStore, err := storage.NewStore(storage.StoreConfig{
		Driver: "sqlite", DSN: stagedDatabase, DataRoot: databaseStageRoot,
		LocalAgentID: "pki-restore-stage", SkipBootstrapSchema: true,
	})
	if err != nil {
		return fmt.Errorf("open protected restore operation stage: %w", err)
	}
	if err := stagedStore.RecordPKIRestoreActivation(ctx, request.OperationID, request.Forced, t.clock().UTC()); err != nil {
		_ = stagedStore.Close()
		return fmt.Errorf("record protected restore operation: %w", err)
	}
	finalSnapshot, snapshotErr := stagedStore.CaptureConsistentPKISQLite(ctx)
	closeErr := stagedStore.Close()
	if snapshotErr != nil || closeErr != nil {
		clear(finalSnapshot)
		return errors.Join(snapshotErr, closeErr)
	}
	validated, err := stagePKIBackupSQLite(ctx, finalSnapshot, stageOptions)
	clear(finalSnapshot)
	if err != nil {
		return err
	}
	finalStagedDatabase := filepath.Join(databaseStageRoot, "panel-final.db")
	if err := writePKIRestrictedFile(finalStagedDatabase, validated.Snapshot); err != nil {
		clear(validated.Snapshot)
		return err
	}
	clear(validated.Snapshot)
	stagedDatabase = finalStagedDatabase

	additional := []storage.PKIRestorePathSwap{{
		ActivePath: filepath.Join(t.dataRoot, "pki"),
		StagedPath: filepath.Join(vaultStageRoot, "pki"),
	}}
	if t.masterKeyFile != "" {
		additional = append(additional, storage.PKIRestorePathSwap{
			ActivePath: t.masterKeyFile, StagedPath: stagedMasterKey,
		})
	}
	hooks := t.activationHooks
	afterSwap := hooks.AfterSwap
	afterRollback := hooks.AfterRollback
	hooks.AfterRollback = func() error {
		reloadErr := t.vault.reloadLocked()
		if afterRollback != nil {
			reloadErr = errors.Join(reloadErr, afterRollback())
		}
		return reloadErr
	}
	t.vault.mutex.Lock()
	defer t.vault.mutex.Unlock()
	hooks.AfterSwap = func() error {
		if err := t.vault.reloadLocked(); err != nil {
			return err
		}
		if afterSwap != nil {
			return afterSwap()
		}
		return nil
	}
	activationErr := t.store.ActivatePKISQLiteRestoreBundle(ctx, storage.PKISQLiteRestoreBundle{
		StagedDatabasePath: stagedDatabase, AdditionalPaths: additional, CleanupPaths: cleanupPaths,
		StagingRegistration: stagingRegistration, Hooks: hooks,
		ExpectedLease: storage.PKILeaseFence{
			PKIDomainID: request.Lease.PKIDomainID, PKIEpoch: request.Lease.PKIEpoch,
			InstanceID: request.Lease.InstanceID, LeaseTerm: request.Lease.LeaseTerm, LeaseDeadline: request.Lease.LeaseDeadline,
		},
	})
	if activationErr == nil || errors.Is(activationErr, storage.ErrPKIRestoreCleanupPending) ||
		errors.Is(activationErr, storage.ErrPKIRestoreJournalOwnsCleanup) {
		cleanupOwnedByJournal = true
	}
	return activationErr
}

func slicesEqualBytes(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	var mismatch byte
	for index := range left {
		mismatch |= left[index] ^ right[index]
	}
	return mismatch == 0
}

var _ PKIBackupRestoreTarget = (*ProductionPKIBackupRestoreTarget)(nil)
