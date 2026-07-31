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
	payload := bytes.Repeat([]byte{0x5a}, 32)
	if err := writePKIRestrictedFile(canonical, payload); err != nil {
		t.Fatalf("retry writePKIRestrictedFile() error = %v", err)
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
