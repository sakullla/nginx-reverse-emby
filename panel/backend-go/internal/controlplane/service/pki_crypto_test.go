package service

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
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
	if _, err := vault.SealCAKey("domain-1", 1, "ca-signing", plaintext); !errors.Is(err, os.ErrExist) {
		t.Fatalf("SealCAKey(duplicate generation) error = %v, want os.ErrExist", err)
	}

	record[len(record)-1] ^= 0xff
	if err := os.WriteFile(recordPath, record, 0o600); err != nil {
		t.Fatalf("tamper vault record: %v", err)
	}
	if _, err := vault.OpenCAKey(reference, "domain-1", 1, "ca-signing"); !errors.Is(err, ErrPKIVaultInvalid) {
		t.Fatalf("OpenCAKey(tampered) error = %v, want ErrPKIVaultInvalid", err)
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
