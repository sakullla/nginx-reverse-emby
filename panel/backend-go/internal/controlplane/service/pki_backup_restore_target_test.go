package service

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestPKIBackupRestoreTargetReencryptsVaultAndRecordsTerminalOperation(t *testing.T) {
	sourceRoot := t.TempDir()
	sourceStore := newPKIAuthorityRuntimeTestStore(t, sourceRoot)
	sourceVault, err := OpenPKIVault(PKIVaultConfig{DataRoot: sourceRoot})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sourceVault.Close)
	sourceBootstrap := bootstrapPKIAuthorityRuntimeTest(t, sourceStore, sourceVault)
	sourceKeySource, err := NewPKIVaultBackupKeySource(sourceVault)
	if err != nil {
		t.Fatal(err)
	}
	exporter, err := NewPKIBackupService(PKIBackupServiceOptions{
		LeaseGate: sourceBootstrap.lease, SnapshotSource: sourceStore, AuthorityKeySource: sourceKeySource,
	})
	if err != nil {
		t.Fatal(err)
	}
	passphrase := []byte("production restore target test passphrase")
	exported, err := exporter.ExportProtected(t.Context(), passphrase)
	if err != nil {
		t.Fatalf("ExportProtected() error = %v", err)
	}
	sourceState, err := sourceStore.LoadPKICanonicalState(t.Context())
	if err != nil || sourceState.Settings == nil {
		t.Fatalf("source state = %+v, error = %v", sourceState.Settings, err)
	}
	sourceAuthority, found := activePKIAuthority(sourceState.Authorities)
	if !found || sourceAuthority.EncryptedKeyRef == nil {
		t.Fatalf("source authority = %+v", sourceAuthority)
	}

	t.Run("reopen failure keeps old database and vault serving", func(t *testing.T) {
		targetRoot := t.TempDir()
		targetStore := newPKIAuthorityRuntimeTestStore(t, targetRoot)
		targetVault, err := OpenPKIVault(PKIVaultConfig{DataRoot: targetRoot})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(targetVault.Close)
		targetBootstrap := bootstrapPKIAuthorityRuntimeTest(t, targetStore, targetVault)
		oldState, err := targetStore.LoadPKICanonicalState(t.Context())
		if err != nil || oldState.Settings == nil {
			t.Fatal(err)
		}
		oldAuthority, _ := activePKIAuthority(oldState.Authorities)
		injected := errors.New("injected production reopen failure")
		target, err := NewProductionPKIBackupRestoreTarget(PKIBackupRestoreTargetOptions{
			Store: targetStore, Vault: targetVault, DataRoot: targetRoot,
			ActivationHooks: storage.PKISQLiteRestoreHooks{BeforeReopen: func() error { return injected }},
		})
		if err != nil {
			t.Fatal(err)
		}
		restorer, err := NewPKIBackupService(PKIBackupServiceOptions{
			LeaseGate: targetBootstrap.lease, SnapshotSource: sourceStore,
			AuthorityKeySource: sourceKeySource, RestoreTarget: target,
		})
		if err != nil {
			t.Fatal(err)
		}
		_, err = restorer.RestoreProtected(t.Context(), exported.Envelope, passphrase, PKIBackupRestoreOptions{
			Force: true, OperationID: "restore-reopen-failure",
		})
		if !errors.Is(err, ErrPKIBackupActivation) || !errors.Is(err, injected) {
			t.Fatalf("RestoreProtected(injected) error = %v", err)
		}
		after, err := targetStore.LoadPKICanonicalState(t.Context())
		if err != nil || after.Settings == nil || after.Settings.PKIDomainID != oldState.Settings.PKIDomainID {
			t.Fatalf("target after rollback = %+v, old = %+v, error = %v", after.Settings, oldState.Settings, err)
		}
		if oldAuthority.EncryptedKeyRef == nil {
			t.Fatal("old target authority key reference is missing")
		}
		key, err := targetVault.OpenCAKey(*oldAuthority.EncryptedKeyRef, oldAuthority.PKIDomainID, oldAuthority.Generation, pkiBackupCAPurpose)
		clear(key)
		if err != nil {
			t.Fatalf("old target vault after rollback error = %v", err)
		}
	})

	t.Run("post-commit cleanup failure remains a successful activation", func(t *testing.T) {
		targetRoot := t.TempDir()
		targetStore := newPKIAuthorityRuntimeTestStore(t, targetRoot)
		targetVault, err := OpenPKIVault(PKIVaultConfig{DataRoot: targetRoot})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(targetVault.Close)
		targetBootstrap := bootstrapPKIAuthorityRuntimeTest(t, targetStore, targetVault)
		injected := errors.New("injected post-commit cleanup failure")
		target, err := NewProductionPKIBackupRestoreTarget(PKIBackupRestoreTargetOptions{
			Store: targetStore, Vault: targetVault, DataRoot: targetRoot,
			ActivationHooks: storage.PKISQLiteRestoreHooks{AfterCommit: func() error { return injected }},
		})
		if err != nil {
			t.Fatal(err)
		}
		restorer, err := NewPKIBackupService(PKIBackupServiceOptions{
			LeaseGate: targetBootstrap.lease, SnapshotSource: sourceStore,
			AuthorityKeySource: sourceKeySource, RestoreTarget: target,
		})
		if err != nil {
			t.Fatal(err)
		}
		result, err := restorer.RestoreProtected(t.Context(), exported.Envelope, passphrase, PKIBackupRestoreOptions{
			Force: true, OperationID: "restore-cleanup-pending",
		})
		if err != nil {
			t.Fatalf("RestoreProtected(committed cleanup) error = %v", err)
		}
		if !result.CleanupPending {
			t.Fatalf("RestoreProtected(committed cleanup) result = %+v", result)
		}
		after, err := targetStore.LoadPKICanonicalState(t.Context())
		if err != nil || after.Settings == nil || after.Settings.PKIDomainID != sourceState.Settings.PKIDomainID {
			t.Fatalf("committed cleanup canonical state = %+v, error = %v", after.Settings, err)
		}
		operation, found := findPKILifecycleRow(after, "restore-cleanup-pending")
		if !found || operation.State != storage.PKILifecycleJobStateSucceeded || operation.Phase != "completed" {
			t.Fatalf("committed cleanup operation = %+v", operation)
		}
	})

	t.Run("partial rollback leaves staging under durable journal ownership", func(t *testing.T) {
		targetRoot := t.TempDir()
		targetStore := newPKIAuthorityRuntimeTestStore(t, targetRoot)
		targetVault, err := OpenPKIVault(PKIVaultConfig{DataRoot: targetRoot})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(targetVault.Close)
		targetBootstrap := bootstrapPKIAuthorityRuntimeTest(t, targetStore, targetVault)
		oldState, err := targetStore.LoadPKICanonicalState(t.Context())
		if err != nil || oldState.Settings == nil {
			t.Fatal(err)
		}
		injected := errors.New("injected partial production rollback")
		var obstructionPath string
		target, err := NewProductionPKIBackupRestoreTarget(PKIBackupRestoreTargetOptions{
			Store: targetStore, Vault: targetVault, DataRoot: targetRoot,
			ActivationHooks: storage.PKISQLiteRestoreHooks{BeforeReopen: func() error {
				stageRoots, globErr := filepath.Glob(filepath.Join(targetRoot, ".pki-restore-db-stage-*"))
				if globErr != nil || len(stageRoots) != 1 {
					return errors.Join(injected, globErr)
				}
				obstructionPath = filepath.Join(stageRoots[0], "panel-final.db")
				if writeErr := os.WriteFile(obstructionPath, []byte("rollback obstruction"), 0o600); writeErr != nil {
					return writeErr
				}
				return injected
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		restorer, err := NewPKIBackupService(PKIBackupServiceOptions{
			LeaseGate: targetBootstrap.lease, SnapshotSource: sourceStore,
			AuthorityKeySource: sourceKeySource, RestoreTarget: target,
		})
		if err != nil {
			t.Fatal(err)
		}
		_, err = restorer.RestoreProtected(t.Context(), exported.Envelope, passphrase, PKIBackupRestoreOptions{
			Force: true, OperationID: "restore-partial-rollback",
		})
		if !errors.Is(err, injected) || !errors.Is(err, storage.ErrPKIRestoreJournalOwnsCleanup) {
			t.Fatalf("RestoreProtected(partial rollback) error = %v", err)
		}
		if obstructionPath == "" {
			t.Fatal("partial rollback did not create its staged-path obstruction")
		}
		stageRoot := filepath.Dir(obstructionPath)
		if _, err := os.Stat(stageRoot); err != nil {
			t.Fatalf("production target deleted journal-owned staging root: %v", err)
		}
		activeDatabase, err := targetStore.PKISQLiteDatabasePath()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(obstructionPath); err != nil {
			t.Fatalf("remove production rollback obstruction: %v", err)
		}
		if err := targetStore.Close(); err != nil {
			t.Fatal(err)
		}
		reopened, err := storage.NewStore(storage.StoreConfig{
			Driver: "sqlite", DSN: activeDatabase, DataRoot: targetRoot, LocalAgentID: "local", SkipBootstrapSchema: true,
		})
		if err != nil {
			t.Fatalf("recover production partial rollback: %v", err)
		}
		defer reopened.Close()
		recovered, err := reopened.LoadPKICanonicalState(t.Context())
		if err != nil || recovered.Settings == nil || recovered.Settings.PKIDomainID != oldState.Settings.PKIDomainID {
			t.Fatalf("recovered production target = %+v, old = %+v, error = %v", recovered.Settings, oldState.Settings, err)
		}
		if _, err := os.Stat(stageRoot); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("recovered production staging root still exists: %v", err)
		}
	})

	t.Run("successful force restore uses a fresh local master key", func(t *testing.T) {
		targetRoot := t.TempDir()
		targetStore := newPKIAuthorityRuntimeTestStore(t, targetRoot)
		targetVault, err := OpenPKIVault(PKIVaultConfig{DataRoot: targetRoot})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(targetVault.Close)
		targetBootstrap := bootstrapPKIAuthorityRuntimeTest(t, targetStore, targetVault)
		target, err := NewProductionPKIBackupRestoreTarget(PKIBackupRestoreTargetOptions{
			Store: targetStore, Vault: targetVault, DataRoot: targetRoot,
			Clock: func() time.Time { return time.Now().UTC() },
		})
		if err != nil {
			t.Fatal(err)
		}
		restorer, err := NewPKIBackupService(PKIBackupServiceOptions{
			LeaseGate: targetBootstrap.lease, SnapshotSource: sourceStore,
			AuthorityKeySource: sourceKeySource, RestoreTarget: target,
		})
		if err != nil {
			t.Fatal(err)
		}
		result, err := restorer.RestoreProtected(t.Context(), exported.Envelope, passphrase, PKIBackupRestoreOptions{
			Force: true, OperationID: "restore-success",
		})
		if err != nil {
			t.Fatalf("RestoreProtected() error = %v", err)
		}
		after, err := targetStore.LoadPKICanonicalState(t.Context())
		if err != nil || after.Settings == nil || after.Settings.PKIDomainID != sourceState.Settings.PKIDomainID ||
			after.Settings.PKIEpoch != result.Version.PKIEpoch || after.Settings.SecurityRevision != 0 || after.InstanceLease != nil {
			t.Fatalf("restored canonical state = %+v, lease=%+v, error=%v", after.Settings, after.InstanceLease, err)
		}
		operation, found := findPKILifecycleRow(after, "restore-success")
		if !found || operation.State != storage.PKILifecycleJobStateSucceeded || operation.Phase != "completed" {
			t.Fatalf("restored operation = %+v", operation)
		}
		key, err := targetVault.OpenCAKey(*sourceAuthority.EncryptedKeyRef, sourceAuthority.PKIDomainID,
			sourceAuthority.Generation, pkiBackupCAPurpose)
		clear(key)
		if err != nil {
			t.Fatalf("re-encrypted source authority key error = %v", err)
		}
	})
}
