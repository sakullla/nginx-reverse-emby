package service

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

const (
	pkiLockHelperEnv    = "NRE_PKI_LOCK_HELPER"
	pkiLockHelperDirEnv = "NRE_PKI_LOCK_HELPER_DIR"
)

type pkiCloseErrorLock struct {
	pkiDirectoryLock
	closeErr error
}

func (lock *pkiCloseErrorLock) Close() error {
	return errors.Join(lock.pkiDirectoryLock.Close(), lock.closeErr)
}

func TestVaultCAKeyEncryptionAADAndPermissions(t *testing.T) {
	root := t.TempDir()
	vault, err := OpenPKIVault(PKIVaultConfig{DataRoot: root})
	if err != nil {
		t.Fatalf("OpenPKIVault() error = %v", err)
	}
	plaintext := []byte("test-ca-private-key-material-that-must-not-be-visible")
	reference, err := vault.SealCAKey("domain-1", 1, "ca-signing", plaintext)
	if err != nil {
		t.Fatalf("SealCAKey() error = %v", err)
	}
	recordPath := filepath.Join(root, "pki", "vault", reference)
	record, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatalf("ReadFile(vault record) error = %v", err)
	}
	if bytes.Contains(record, plaintext) {
		t.Fatal("vault record contains plaintext CA key")
	}
	opened, err := vault.OpenCAKey(reference, "domain-1", 1, "ca-signing")
	if err != nil || !bytes.Equal(opened, plaintext) {
		t.Fatalf("OpenCAKey() = %q, %v", opened, err)
	}
	if _, err := vault.OpenCAKey(reference, "domain-2", 1, "ca-signing"); !errors.Is(err, ErrPKIVaultInvalid) {
		t.Fatalf("OpenCAKey(wrong AAD) error = %v, want ErrPKIVaultInvalid", err)
	}
	if repeatedReference, err := vault.SealCAKey("domain-1", 1, "ca-signing", plaintext); err != nil || repeatedReference != reference {
		t.Fatalf("SealCAKey(idempotent retry) = %q, %v", repeatedReference, err)
	}
	if _, err := vault.SealCAKey("domain-1", 1, "ca-signing", []byte("different-key-material")); !errors.Is(err, os.ErrExist) {
		t.Fatalf("SealCAKey(conflicting generation) error = %v, want os.ErrExist", err)
	}

	record[len(record)-1] ^= 0xff
	if err := os.WriteFile(recordPath, record, 0o600); err != nil {
		t.Fatalf("tamper vault record: %v", err)
	}
	if _, err := vault.OpenCAKey(reference, "domain-1", 1, "ca-signing"); !errors.Is(err, ErrPKIVaultInvalid) {
		t.Fatalf("OpenCAKey(tampered) error = %v, want ErrPKIVaultInvalid", err)
	}
	if err := os.Truncate(recordPath, int64(len(record)/2)); err != nil {
		t.Fatalf("truncate vault record: %v", err)
	}
	if _, err := vault.OpenCAKey(reference, "domain-1", 1, "ca-signing"); !errors.Is(err, ErrPKIVaultInvalid) {
		t.Fatalf("OpenCAKey(truncated) error = %v, want ErrPKIVaultInvalid", err)
	}

	masterPath := filepath.Join(root, "pki", "master.key")
	master, err := os.ReadFile(masterPath)
	if err != nil || len(master) != 32 {
		t.Fatalf("master key length = %d, error = %v", len(master), err)
	}
	if runtime.GOOS != "windows" {
		for _, path := range []string{filepath.Join(root, "pki"), filepath.Join(root, "pki", "vault")} {
			info, statErr := os.Stat(path)
			if statErr != nil || info.Mode().Perm() != 0o700 {
				t.Fatalf("directory %s mode = %v, error = %v", path, info.Mode().Perm(), statErr)
			}
		}
		for _, path := range []string{masterPath, recordPath} {
			info, statErr := os.Stat(path)
			if statErr != nil || info.Mode().Perm() != 0o600 {
				t.Fatalf("file %s mode = %v, error = %v", path, info.Mode().Perm(), statErr)
			}
		}
	}
}

func TestVaultAtomicPublicationFailureRetryAndConcurrency(t *testing.T) {
	root := t.TempDir()
	pkiRoot := filepath.Join(root, "pki")
	if err := ensurePKIRestrictedDirectory(pkiRoot); err != nil {
		t.Fatalf("ensurePKIRestrictedDirectory() error = %v", err)
	}
	canonical := filepath.Join(pkiRoot, "master.key")
	if err := writePKIRestrictedFileFromReader(canonical, bytes.NewReader([]byte("truncated")), 32); err == nil {
		t.Fatal("truncated temporary write unexpectedly succeeded")
	}
	if _, err := os.Stat(canonical); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canonical file after failed temporary write error = %v, want os.ErrNotExist", err)
	}
	entries, err := os.ReadDir(pkiRoot)
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	for _, entry := range entries {
		if bytes.Contains([]byte(entry.Name()), []byte(".master.key.tmp-")) {
			t.Fatalf("failed publication left temporary file %q", entry.Name())
		}
	}
	stale := filepath.Join(pkiRoot, ".master.key.tmp-crashed-writer")
	if err := os.WriteFile(stale, bytes.Repeat([]byte{0x11}, 32), 0o600); err != nil {
		t.Fatalf("write stale publication: %v", err)
	}
	payload := bytes.Repeat([]byte{0x5a}, 32)
	if err := writePKIRestrictedFile(canonical, payload); err != nil {
		t.Fatalf("retry writePKIRestrictedFile() error = %v", err)
	}
	if _, err := os.Lstat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale publication after retry error = %v, want os.ErrNotExist", err)
	}
	if err := writePKIRestrictedFile(canonical, bytes.Repeat([]byte{0xa5}, 32)); !errors.Is(err, os.ErrExist) {
		t.Fatalf("no-clobber write error = %v, want os.ErrExist", err)
	}
	stored, err := os.ReadFile(canonical)
	if err != nil || !bytes.Equal(stored, payload) {
		t.Fatalf("canonical payload = %x, error = %v", stored, err)
	}

	concurrentRoot := t.TempDir()
	const workers = 8
	vaults := make(chan *PKIVault, workers)
	errorsByWorker := make(chan error, workers)
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			vault, openErr := OpenPKIVault(PKIVaultConfig{DataRoot: concurrentRoot})
			if openErr != nil {
				errorsByWorker <- openErr
				return
			}
			vaults <- vault
		}()
	}
	wait.Wait()
	close(vaults)
	close(errorsByWorker)
	for workerErr := range errorsByWorker {
		t.Errorf("concurrent OpenPKIVault() error = %v", workerErr)
	}
	var winnerKey []byte
	count := 0
	for vault := range vaults {
		count++
		if winnerKey == nil {
			winnerKey = append([]byte(nil), vault.masterKey...)
			continue
		}
		if !bytes.Equal(vault.masterKey, winnerKey) {
			t.Fatal("concurrent vault creators did not converge on the published master key")
		}
	}
	if count != workers {
		t.Fatalf("successful concurrent vault opens = %d, want %d", count, workers)
	}
}

func TestVaultAtomicNoReplacePreservesNonCooperativeWinner(t *testing.T) {
	root := t.TempDir()
	if err := ensurePKIRestrictedDirectory(root); err != nil {
		t.Fatalf("ensure publication directory: %v", err)
	}
	canonical := filepath.Join(root, "master.key")
	winner := bytes.Repeat([]byte{0x41}, pkiVaultMasterKeySize)
	loser := bytes.Repeat([]byte{0x52}, pkiVaultMasterKeySize)
	ops := defaultPKICryptoFileOps()
	atomicPublish := ops.publish
	ops.publish = func(staging, destination string) error {
		if err := os.WriteFile(destination, winner, 0o600); err != nil {
			return err
		}
		return atomicPublish(staging, destination)
	}
	err := writePKIRestrictedFileWithOps(canonical, loser, ops)
	if !errors.Is(err, os.ErrExist) || !isPurePKIPublishConflict(err) {
		t.Fatalf("non-cooperative publish error = %v, want pure typed os.ErrExist conflict", err)
	}
	stored, err := os.ReadFile(canonical)
	if err != nil || !bytes.Equal(stored, winner) {
		t.Fatalf("canonical winner = %x, %v; want %x", stored, err, winner)
	}
}

func TestVaultDirectoryLockReleasedWhenHelperProcessExits(t *testing.T) {
	if os.Getenv(pkiLockHelperEnv) == "1" {
		lock, err := acquirePKIOSDirectoryLock(os.Getenv(pkiLockHelperDirEnv))
		if err != nil {
			os.Exit(2)
		}
		if _, err := os.Stdout.WriteString("locked\n"); err != nil {
			os.Exit(3)
		}
		_ = os.Stdout.Sync()
		runtime.KeepAlive(lock)
		os.Exit(0)
	}

	directory := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestVaultDirectoryLockReleasedWhenHelperProcessExits$")
	command.Env = append(os.Environ(), pkiLockHelperEnv+"=1", pkiLockHelperDirEnv+"="+directory)
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatalf("helper StdoutPipe() error = %v", err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatalf("start lock helper: %v", err)
	}
	line, readErr := bufio.NewReader(stdout).ReadString('\n')
	if readErr != nil || line != "locked\n" {
		cancel()
		_ = command.Wait()
		t.Fatalf("lock helper marker = %q, %v; stderr = %q", line, readErr, stderr.String())
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("lock helper exit error = %v; stderr = %q", err, stderr.String())
	}

	reacquired := make(chan error, 1)
	go func() {
		lock, err := acquirePKIOSDirectoryLock(directory)
		if err == nil {
			err = lock.Close()
		}
		reacquired <- err
	}()
	select {
	case err := <-reacquired:
		if err != nil {
			t.Fatalf("reacquire lock after helper exit: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out reacquiring OS lock after helper exited without Close")
	}
}

func TestVaultRestartCleansCompleteStagingAndHardLinkAliases(t *testing.T) {
	t.Run("complete staging leftovers", func(t *testing.T) {
		root := t.TempDir()
		pkiRoot := filepath.Join(root, "pki")
		vaultDir := filepath.Join(pkiRoot, "vault")
		if err := ensurePKIRestrictedDirectory(vaultDir); err != nil {
			t.Fatalf("ensure vault directory: %v", err)
		}
		masterStaging := filepath.Join(pkiRoot, ".master.key.tmp-complete-crash")
		vaultCanonical := pkiVaultReference("domain-1", 1, "ca-signing")
		vaultStaging := filepath.Join(vaultDir, "."+vaultCanonical+".tmp-complete-crash")
		if err := os.WriteFile(masterStaging, bytes.Repeat([]byte{0x11}, pkiVaultMasterKeySize), 0o600); err != nil {
			t.Fatalf("write master staging: %v", err)
		}
		if err := os.WriteFile(vaultStaging, bytes.Repeat([]byte{0x22}, 64), 0o600); err != nil {
			t.Fatalf("write vault staging: %v", err)
		}

		expectedKey := bytes.Repeat([]byte{0x33}, pkiVaultMasterKeySize)
		vault, err := OpenPKIVault(PKIVaultConfig{DataRoot: root, Random: bytes.NewReader(expectedKey)})
		if err != nil {
			t.Fatalf("OpenPKIVault(restart) error = %v", err)
		}
		if !bytes.Equal(vault.masterKey, expectedKey) {
			t.Fatalf("recovered master key = %x, want newly published %x", vault.masterKey, expectedKey)
		}
		for _, path := range []string{masterStaging, vaultStaging} {
			if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("staging path %s after restart error = %v, want os.ErrNotExist", path, err)
			}
		}
	})

	t.Run("old hard-link alias", func(t *testing.T) {
		root := t.TempDir()
		vault, err := OpenPKIVault(PKIVaultConfig{DataRoot: root})
		if err != nil {
			t.Fatalf("OpenPKIVault() error = %v", err)
		}
		masterPath := vault.masterKeyFile
		aliasPath := filepath.Join(filepath.Dir(masterPath), "."+filepath.Base(masterPath)+".tmp-old-hardlink")
		if err := os.Link(masterPath, aliasPath); err != nil {
			t.Skipf("hard links are unavailable on this test filesystem: %v", err)
		}
		canonicalInfo, err := os.Stat(masterPath)
		if err != nil {
			t.Fatalf("stat canonical master key: %v", err)
		}
		aliasInfo, err := os.Stat(aliasPath)
		if err != nil || !os.SameFile(canonicalInfo, aliasInfo) {
			t.Fatalf("staging alias does not share canonical inode: %v", err)
		}
		originalKey := append([]byte(nil), vault.masterKey...)
		reopened, err := OpenPKIVault(PKIVaultConfig{DataRoot: root})
		if err != nil {
			t.Fatalf("OpenPKIVault(restart) error = %v", err)
		}
		if !bytes.Equal(reopened.masterKey, originalKey) {
			t.Fatal("restart changed the canonical master key while removing its old alias")
		}
		if _, err := os.Lstat(aliasPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("old hard-link alias after restart error = %v, want os.ErrNotExist", err)
		}
	})

	t.Run("external master old hard-link alias", func(t *testing.T) {
		root := t.TempDir()
		externalRoot := t.TempDir()
		externalPath := filepath.Join(externalRoot, "master.key")
		expectedKey := bytes.Repeat([]byte{0x61}, pkiVaultMasterKeySize)
		if err := os.WriteFile(externalPath, expectedKey, 0o600); err != nil {
			t.Fatalf("write external master key: %v", err)
		}
		aliasPath := filepath.Join(externalRoot, ".master.key.tmp-old-hardlink")
		if err := os.Link(externalPath, aliasPath); err != nil {
			t.Skipf("hard links are unavailable on this test filesystem: %v", err)
		}
		vault, err := OpenPKIVault(PKIVaultConfig{DataRoot: root, MasterKeyFile: externalPath})
		if err != nil {
			t.Fatalf("OpenPKIVault(external alias upgrade) error = %v", err)
		}
		if !bytes.Equal(vault.masterKey, expectedKey) {
			t.Fatalf("external master key = %x, want %x", vault.masterKey, expectedKey)
		}
		if _, err := os.Lstat(aliasPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("external hard-link alias after upgrade error = %v, want os.ErrNotExist", err)
		}
	})
}

func TestVaultCleanupFailureIsNotHiddenByReadableCanonical(t *testing.T) {
	root := t.TempDir()
	vault, err := OpenPKIVault(PKIVaultConfig{DataRoot: root})
	if err != nil {
		t.Fatalf("OpenPKIVault() error = %v", err)
	}
	plaintext := []byte("stable-ca-key")
	reference, err := vault.SealCAKey("domain-1", 1, "ca-signing", plaintext)
	if err != nil {
		t.Fatalf("SealCAKey() error = %v", err)
	}
	stagingPath := filepath.Join(vault.vaultDir, "."+reference+".tmp-stale")
	if err := os.WriteFile(stagingPath, []byte("stale"), 0o600); err != nil {
		t.Fatalf("write stale vault staging: %v", err)
	}
	injected := errors.New("injected remove failure")
	defaultRemove := vault.fileOps.remove
	vault.fileOps.remove = func(path string) error {
		if path == stagingPath {
			return injected
		}
		return defaultRemove(path)
	}
	if _, err := vault.SealCAKey("domain-1", 1, "ca-signing", plaintext); !errors.Is(err, errPKIVaultCleanup) || !errors.Is(err, injected) {
		t.Fatalf("SealCAKey(stale cleanup failure) error = %v, want cleanup and injected errors", err)
	}
	opened, err := vault.OpenCAKey(reference, "domain-1", 1, "ca-signing")
	if err != nil || !bytes.Equal(opened, plaintext) {
		t.Fatalf("canonical after cleanup failure = %q, %v", opened, err)
	}
	reopened, err := OpenPKIVault(PKIVaultConfig{DataRoot: root})
	if err != nil {
		t.Fatalf("OpenPKIVault(recovery) error = %v", err)
	}
	if _, err := reopened.SealCAKey("domain-1", 1, "ca-signing", plaintext); err != nil {
		t.Fatalf("SealCAKey(after recovery) error = %v", err)
	}
	if _, err := os.Lstat(stagingPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale vault staging after recovery error = %v, want os.ErrNotExist", err)
	}
}

func TestVaultPureConflictWithLockCloseFailureIsNotIdempotentSuccess(t *testing.T) {
	root := t.TempDir()
	vault, err := OpenPKIVault(PKIVaultConfig{DataRoot: root})
	if err != nil {
		t.Fatalf("OpenPKIVault() error = %v", err)
	}
	plaintext := []byte("stable-ca-key")
	if _, err := vault.SealCAKey("domain-1", 1, "ca-signing", plaintext); err != nil {
		t.Fatalf("SealCAKey() error = %v", err)
	}
	injected := errors.New("injected lock close failure")
	defaultAcquire := vault.fileOps.acquireLock
	vault.fileOps.acquireLock = func(directory string) (pkiDirectoryLock, error) {
		lock, err := defaultAcquire(directory)
		if err != nil {
			return nil, err
		}
		return &pkiCloseErrorLock{pkiDirectoryLock: lock, closeErr: injected}, nil
	}
	if _, err := vault.SealCAKey("domain-1", 1, "ca-signing", plaintext); !errors.Is(err, os.ErrExist) || !errors.Is(err, errPKIVaultCleanup) || !errors.Is(err, injected) {
		t.Fatalf("SealCAKey(conflict plus close failure) error = %v, want conflict, cleanup, and injected errors", err)
	}
	vault.fileOps.acquireLock = defaultAcquire
	if _, err := vault.SealCAKey("domain-1", 1, "ca-signing", plaintext); err != nil {
		t.Fatalf("SealCAKey(after close-error recovery) error = %v", err)
	}
}

func TestVaultPublicationOperationFallbacksAndExternalMasterRead(t *testing.T) {
	t.Run("atomic publish unsupported leaves no canonical", func(t *testing.T) {
		root := t.TempDir()
		ops := defaultPKICryptoFileOps()
		ops.publish = func(string, string) error { return errors.ErrUnsupported }
		if _, err := OpenPKIVault(PKIVaultConfig{DataRoot: root, fileOps: &ops}); !errors.Is(err, errors.ErrUnsupported) {
			t.Fatalf("OpenPKIVault(publish unsupported) error = %v, want errors.ErrUnsupported", err)
		}
		masterPath := filepath.Join(root, "pki", "master.key")
		if _, err := os.Lstat(masterPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("master key after failed rename error = %v, want os.ErrNotExist", err)
		}
		if _, err := OpenPKIVault(PKIVaultConfig{DataRoot: root}); err != nil {
			t.Fatalf("OpenPKIVault(after publish recovery) error = %v", err)
		}
	})

	t.Run("unsupported directory sync uses safe fallback", func(t *testing.T) {
		root := t.TempDir()
		ops := defaultPKICryptoFileOps()
		ops.syncDirectory = func(string) error { return errors.ErrUnsupported }
		if _, err := OpenPKIVault(PKIVaultConfig{DataRoot: root, fileOps: &ops}); err != nil {
			t.Fatalf("OpenPKIVault(unsupported directory sync) error = %v", err)
		}
		if key, err := readPKIMasterKey(filepath.Join(root, "pki", "master.key")); err != nil || len(key) != pkiVaultMasterKeySize {
			t.Fatalf("read published master key = %x, %v", key, err)
		}
	})

	t.Run("directory sync failure rolls back publication", func(t *testing.T) {
		root := t.TempDir()
		injected := errors.New("injected directory sync failure")
		ops := defaultPKICryptoFileOps()
		ops.syncDirectory = func(string) error { return injected }
		if _, err := OpenPKIVault(PKIVaultConfig{DataRoot: root, fileOps: &ops}); !errors.Is(err, injected) {
			t.Fatalf("OpenPKIVault(directory sync failure) error = %v, want injected error", err)
		}
		masterPath := filepath.Join(root, "pki", "master.key")
		if _, err := os.Lstat(masterPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("master key after sync rollback error = %v, want os.ErrNotExist", err)
		}
		if _, err := OpenPKIVault(PKIVaultConfig{DataRoot: root}); err != nil {
			t.Fatalf("OpenPKIVault(after sync recovery) error = %v", err)
		}
	})

	t.Run("existing external master key does not sync its parent", func(t *testing.T) {
		root := t.TempDir()
		externalRoot := t.TempDir()
		externalPath := filepath.Join(externalRoot, "master.key")
		expectedKey := bytes.Repeat([]byte{0x7a}, pkiVaultMasterKeySize)
		if err := os.WriteFile(externalPath, expectedKey, 0o600); err != nil {
			t.Fatalf("write external master key: %v", err)
		}
		syncCalls := 0
		externalLockCalls := 0
		ops := defaultPKICryptoFileOps()
		defaultAcquire := ops.acquireLock
		ops.acquireLock = func(directory string) (pkiDirectoryLock, error) {
			if directory == externalRoot {
				externalLockCalls++
			}
			return defaultAcquire(directory)
		}
		ops.syncDirectory = func(string) error {
			syncCalls++
			return errors.New("unexpected directory sync")
		}
		vault, err := OpenPKIVault(PKIVaultConfig{DataRoot: root, MasterKeyFile: externalPath, fileOps: &ops})
		if err != nil {
			t.Fatalf("OpenPKIVault(existing external master key) error = %v", err)
		}
		if syncCalls != 0 {
			t.Fatalf("existing external master key triggered %d directory sync calls", syncCalls)
		}
		if externalLockCalls != 0 {
			t.Fatalf("existing external master key triggered %d external directory lock calls", externalLockCalls)
		}
		if !bytes.Equal(vault.masterKey, expectedKey) {
			t.Fatalf("external master key = %x, want %x", vault.masterKey, expectedKey)
		}
	})

	t.Run("external staging cleanup failure is explicit", func(t *testing.T) {
		root := t.TempDir()
		externalRoot := t.TempDir()
		externalPath := filepath.Join(externalRoot, "master.key")
		stagingPath := filepath.Join(externalRoot, ".master.key.tmp-stale")
		if err := os.WriteFile(externalPath, bytes.Repeat([]byte{0x37}, pkiVaultMasterKeySize), 0o600); err != nil {
			t.Fatalf("write external master key: %v", err)
		}
		if err := os.WriteFile(stagingPath, []byte("stale"), 0o600); err != nil {
			t.Fatalf("write external staging: %v", err)
		}
		injected := errors.New("injected external cleanup failure")
		ops := defaultPKICryptoFileOps()
		defaultRemove := ops.remove
		ops.remove = func(path string) error {
			if path == stagingPath {
				return injected
			}
			return defaultRemove(path)
		}
		if _, err := OpenPKIVault(PKIVaultConfig{DataRoot: root, MasterKeyFile: externalPath, fileOps: &ops}); !errors.Is(err, errPKIVaultCleanup) || !errors.Is(err, injected) {
			t.Fatalf("OpenPKIVault(external cleanup failure) error = %v, want cleanup and injected errors", err)
		}
		if _, err := OpenPKIVault(PKIVaultConfig{DataRoot: root, MasterKeyFile: externalPath}); err != nil {
			t.Fatalf("OpenPKIVault(after external cleanup recovery) error = %v", err)
		}
	})
}
