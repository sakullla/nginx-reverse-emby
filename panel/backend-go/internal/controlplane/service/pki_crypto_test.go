package service

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

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

func TestVaultPublicationOperationFallbacksAndExternalMasterRead(t *testing.T) {
	t.Run("rename unsupported leaves no canonical", func(t *testing.T) {
		root := t.TempDir()
		ops := defaultPKICryptoFileOps()
		ops.rename = func(string, string) error { return errors.ErrUnsupported }
		if _, err := OpenPKIVault(PKIVaultConfig{DataRoot: root, fileOps: &ops}); !errors.Is(err, errors.ErrUnsupported) {
			t.Fatalf("OpenPKIVault(rename unsupported) error = %v, want errors.ErrUnsupported", err)
		}
		masterPath := filepath.Join(root, "pki", "master.key")
		if _, err := os.Lstat(masterPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("master key after failed rename error = %v, want os.ErrNotExist", err)
		}
		if _, err := OpenPKIVault(PKIVaultConfig{DataRoot: root}); err != nil {
			t.Fatalf("OpenPKIVault(after rename recovery) error = %v", err)
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
		ops := defaultPKICryptoFileOps()
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
		if !bytes.Equal(vault.masterKey, expectedKey) {
			t.Fatalf("external master key = %x, want %x", vault.masterKey, expectedKey)
		}
	})
}
