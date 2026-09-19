//go:build !integration

package service

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestPKIVaultSealsOpensAndFailsClosedOnTamper(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	vault, err := OpenPKIVault(PKIVaultConfig{DataRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	plaintext := []byte("test-ca-private-key-material-that-must-not-be-visible")
	reference, err := vault.SealCAKey("domain-1", 1, "ca-signing", plaintext)
	if err != nil {
		t.Fatal(err)
	}
	recordPath := filepath.Join(root, "pki", "vault", reference)
	record, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(record, plaintext) {
		t.Fatal("vault record contains plaintext CA key")
	}
	opened, err := vault.OpenCAKey(reference, "domain-1", 1, "ca-signing")
	if err != nil || !bytes.Equal(opened, plaintext) {
		t.Fatalf("OpenCAKey() = %q, %v", opened, err)
	}
	if _, err := vault.OpenCAKey(reference, "domain-2", 1, "ca-signing"); !errors.Is(err, ErrPKIVaultInvalid) {
		t.Fatalf("wrong AAD err=%v", err)
	}
	record[len(record)-1] ^= 0xff
	if err := os.WriteFile(recordPath, record, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := vault.OpenCAKey(reference, "domain-1", 1, "ca-signing"); !errors.Is(err, ErrPKIVaultInvalid) {
		t.Fatalf("tampered record err=%v", err)
	}
}
