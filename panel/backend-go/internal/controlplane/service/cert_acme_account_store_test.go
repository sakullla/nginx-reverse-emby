package service

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/pkg/acmeflow"
)

func TestACMEAccountStorePersistsCrashWindowAndSeparatesIdentity(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	store, err := openMasterACMEAccountStore(dataDir)
	if err != nil {
		t.Fatalf("openMasterACMEAccountStore() error = %v", err)
	}
	lookup := acmeflow.AccountLookup{DirectoryURL: "https://ca.example/directory", Email: "ops@example.com"}
	keyPEM := mustTestACMEAccountKeyPEM(t)
	if err := store.SaveAccountKey(ctx, lookup, keyPEM); err != nil {
		t.Fatalf("SaveAccountKey() error = %v", err)
	}
	root := store.root
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	store, err = openMasterACMEAccountStore(dataDir)
	if err != nil {
		t.Fatalf("reopen account store: %v", err)
	}
	record, err := store.LoadAccount(ctx, lookup)
	if err != nil {
		t.Fatalf("LoadAccount() after key-only crash window error = %v", err)
	}
	if !bytes.Equal(record.KeyPEM, keyPEM) || record.Metadata.URI != "" {
		t.Fatalf("key-only crash record = %#v", record.Metadata)
	}
	metadata := acmeflow.AccountMetadata{
		Version:      acmeflow.AccountMetadataVersion,
		DirectoryURL: lookup.DirectoryURL,
		Email:        lookup.Email,
		URI:          "https://ca.example/account/7",
		Contact:      []string{"mailto:ops@example.com"},
	}
	if err := store.SaveAccountMetadata(ctx, metadata); err != nil {
		t.Fatalf("SaveAccountMetadata() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close(reopened) error = %v", err)
	}

	store, err = openMasterACMEAccountStore(dataDir)
	if err != nil {
		t.Fatalf("reopen completed account store: %v", err)
	}
	defer store.Close()
	record, err = store.LoadAccount(ctx, lookup)
	if err != nil {
		t.Fatalf("LoadAccount() completed record error = %v", err)
	}
	if !bytes.Equal(record.KeyPEM, keyPEM) || record.Metadata.URI != metadata.URI {
		t.Fatalf("completed account record did not round-trip: %#v", record.Metadata)
	}
	for _, other := range []acmeflow.AccountLookup{
		{DirectoryURL: lookup.DirectoryURL, Email: "other@example.com"},
		{DirectoryURL: "https://other-ca.example/directory", Email: lookup.Email},
	} {
		if _, err := store.LoadAccount(ctx, other); !errors.Is(err, acmeflow.ErrAccountNotFound) {
			t.Fatalf("LoadAccount(%#v) error = %v, want ErrAccountNotFound", other, err)
		}
	}
	if want := filepath.Join(dataDir, "acme", "master"); root != want {
		t.Fatalf("account root = %q, want %q", root, want)
	}
	assertMasterACMEStatePermissions(t, root)
}

func TestACMEAccountStoreAtomicMetadataAndCorruptionErrorsAreSafe(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	injected := errors.New("persistence-fault-canary")
	failMetadataSync := false
	store, err := openMasterACMEAccountStore(dataDir, acmeflow.WithPersistenceFaultInjector(func(point acmeflow.PersistenceFaultPoint) error {
		if failMetadataSync && point == acmeflow.PersistenceFaultPoint("account.metadata.file_synced") {
			return injected
		}
		return nil
	}))
	if err != nil {
		t.Fatalf("openMasterACMEAccountStore() error = %v", err)
	}
	lookup := acmeflow.AccountLookup{DirectoryURL: "https://ca.example/directory", Email: "ops@example.com"}
	if err := store.SaveAccountKey(ctx, lookup, mustTestACMEAccountKeyPEM(t)); err != nil {
		t.Fatalf("SaveAccountKey() error = %v", err)
	}
	metadata := acmeflow.AccountMetadata{
		Version:      acmeflow.AccountMetadataVersion,
		DirectoryURL: lookup.DirectoryURL,
		Email:        lookup.Email,
		URI:          "https://ca.example/account/7",
		Contact:      []string{"mailto:ops@example.com"},
	}
	if err := store.SaveAccountMetadata(ctx, metadata); err != nil {
		t.Fatalf("SaveAccountMetadata() error = %v", err)
	}
	failMetadataSync = true
	updated := metadata
	updated.Contact = []string{"mailto:changed@example.com"}
	if err := store.SaveAccountMetadata(ctx, updated); !errors.Is(err, injected) {
		t.Fatalf("SaveAccountMetadata(fault) error = %v, want injected fault", err)
	}
	root := store.root
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	store, err = openMasterACMEAccountStore(dataDir)
	if err != nil {
		t.Fatalf("reopen account store: %v", err)
	}
	record, err := store.LoadAccount(ctx, lookup)
	if err != nil {
		t.Fatalf("LoadAccount() after fault error = %v", err)
	}
	if len(record.Metadata.Contact) != 1 || record.Metadata.Contact[0] != metadata.Contact[0] {
		t.Fatalf("metadata changed before atomic rename: %#v", record.Metadata)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close(recovery) error = %v", err)
	}

	accounts, err := os.ReadDir(filepath.Join(root, "accounts"))
	if err != nil || len(accounts) != 1 {
		t.Fatalf("ReadDir(accounts) entries = %d, error = %v", len(accounts), err)
	}
	metadataPath := filepath.Join(root, "accounts", accounts[0].Name(), "metadata.json")
	const corruptCanary = "persisted-provider-token-canary"
	if err := os.WriteFile(metadataPath, []byte(`{"provider_token":"`+corruptCanary+`"}`), 0o600); err != nil {
		t.Fatalf("corrupt metadata fixture: %v", err)
	}
	store, err = openMasterACMEAccountStore(dataDir)
	if err != nil {
		t.Fatalf("reopen corrupt account store: %v", err)
	}
	defer store.Close()
	_, err = store.LoadAccount(ctx, lookup)
	if err == nil {
		t.Fatal("LoadAccount() error = nil for corrupt metadata")
	}
	if category := acmeflow.ErrorCategoryOf(err); category != acmeflow.CategoryAccount {
		t.Fatalf("corrupt metadata category = %q, want %q", category, acmeflow.CategoryAccount)
	}
	if strings.Contains(err.Error(), corruptCanary) || strings.Contains(err.Error(), "provider_token") {
		t.Fatalf("corrupt metadata error exposed persisted contents: %v", err)
	}
}

func mustTestACMEAccountKeyPEM(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey() error = %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

func assertMasterACMEStatePermissions(t *testing.T, root string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Log("Windows does not expose POSIX permission bits; Linux full-tier verifies chmod semantics")
		return
	}
	if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		want := os.FileMode(0o600)
		if info.IsDir() {
			want = 0o700
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("mode(%s) = %o, want %o", path, got, want)
		}
		return nil
	}); err != nil {
		t.Fatalf("Walk(account state) error = %v", err)
	}
}
