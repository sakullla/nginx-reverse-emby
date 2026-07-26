package acmeflow

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAccountStateRoundTripAndAtomicMetadataUpdate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	injected := errors.New("injected persistence fault")
	failMetadataSync := false
	store, err := OpenStateStore(root, WithPersistenceFaultInjector(func(point PersistenceFaultPoint) error {
		if failMetadataSync && point == PersistenceFaultPoint("account.metadata.file_synced") {
			return injected
		}
		return nil
	}))
	if err != nil {
		t.Fatalf("OpenStateStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	lookup := AccountLookup{DirectoryURL: "https://ca.example/directory", Email: "ops@example.com"}
	keyPEM := mustRSAKeyPEM(t)
	if err := store.SaveAccountKey(ctx, lookup, keyPEM); err != nil {
		t.Fatalf("SaveAccountKey() error = %v", err)
	}
	metadata := AccountMetadata{
		Version:      AccountMetadataVersion,
		DirectoryURL: lookup.DirectoryURL,
		Email:        lookup.Email,
		URI:          "https://ca.example/acct/7",
		Contact:      []string{"mailto:ops@example.com"},
	}
	if err := store.SaveAccountMetadata(ctx, metadata); err != nil {
		t.Fatalf("SaveAccountMetadata() error = %v", err)
	}

	record, err := store.LoadAccount(ctx, lookup)
	if err != nil {
		t.Fatalf("LoadAccount() error = %v", err)
	}
	if !bytes.Equal(record.KeyPEM, keyPEM) {
		t.Fatal("LoadAccount() returned a different account key")
	}
	if record.Metadata.URI != metadata.URI || len(record.Metadata.Contact) != 1 {
		t.Fatalf("LoadAccount() metadata = %#v", record.Metadata)
	}

	failMetadataSync = true
	updated := metadata
	updated.Contact = []string{"mailto:new@example.com"}
	err = store.SaveAccountMetadata(ctx, updated)
	if !errors.Is(err, injected) {
		t.Fatalf("SaveAccountMetadata() error = %v, want injected fault", err)
	}
	failMetadataSync = false

	record, err = store.LoadAccount(ctx, lookup)
	if err != nil {
		t.Fatalf("LoadAccount() after fault error = %v", err)
	}
	if len(record.Metadata.Contact) != 1 || record.Metadata.Contact[0] != metadata.Contact[0] {
		t.Fatalf("metadata changed before atomic rename: %#v", record.Metadata)
	}

	otherKey := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(mustOtherTestRSAKey(t))})
	if err := store.SaveAccountKey(ctx, lookup, otherKey); err == nil {
		t.Fatal("SaveAccountKey() replaced an immutable account key")
	}
}

func TestAccountStateRequiresVersionedNeutralMetadata(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, err := OpenStateStore(t.TempDir())
	if err != nil {
		t.Fatalf("OpenStateStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	lookup := AccountLookup{DirectoryURL: "https://ca.example/directory", Email: "ops@example.com"}
	if _, err := store.LoadAccount(ctx, lookup); !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("LoadAccount() error = %v, want ErrAccountNotFound", err)
	}
	if err := store.SaveAccountKey(ctx, lookup, mustRSAKeyPEM(t)); err != nil {
		t.Fatalf("SaveAccountKey() error = %v", err)
	}

	invalid := AccountMetadata{
		Version:      AccountMetadataVersion + 1,
		DirectoryURL: lookup.DirectoryURL,
		Email:        lookup.Email,
		URI:          "https://ca.example/acct/7",
	}
	if err := store.SaveAccountMetadata(ctx, invalid); err == nil {
		t.Fatal("SaveAccountMetadata() accepted an unsupported version")
	}
	invalid.Version = AccountMetadataVersion
	invalid.DirectoryURL = "https://user:credential@ca.example/directory"
	if err := store.SaveAccountMetadata(ctx, invalid); err == nil {
		t.Fatal("SaveAccountMetadata() accepted credentials in the directory URL")
	}
}

func TestChallengeIntentPersistsOnlyRecoveryHash(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	store, err := OpenStateStore(root)
	if err != nil {
		t.Fatalf("OpenStateStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	const challengeValue = "dns-token-canary.super-secret.raw-value"
	intent, err := NewChallengeIntent("Example.COM.", "_acme-challenge.Media.Example.COM.", challengeValue)
	if err != nil {
		t.Fatalf("NewChallengeIntent() error = %v", err)
	}
	if intent.Zone != "example.com" || intent.FQDN != "_acme-challenge.media.example.com" {
		t.Fatalf("NewChallengeIntent() names = %q / %q", intent.Zone, intent.FQDN)
	}
	if intent.ValueHash != HashChallengeValue(challengeValue) || strings.Contains(intent.ValueHash, challengeValue) {
		t.Fatalf("NewChallengeIntent() value hash = %q", intent.ValueHash)
	}
	if err := store.SaveChallengeIntent(ctx, intent); err != nil {
		t.Fatalf("SaveChallengeIntent() error = %v", err)
	}
	if err := store.SetChallengeRecordID(ctx, intent.ID, "record-123"); err != nil {
		t.Fatalf("SetChallengeRecordID() error = %v", err)
	}

	intents, err := store.ListChallengeIntents(ctx)
	if err != nil {
		t.Fatalf("ListChallengeIntents() error = %v", err)
	}
	if len(intents) != 1 || intents[0].RecordID != "record-123" || intents[0].Status != ChallengeIntentPending {
		t.Fatalf("ListChallengeIntents() = %#v", intents)
	}
	if err := store.CompleteChallengeIntent(ctx, intent.ID); err != nil {
		t.Fatalf("CompleteChallengeIntent() error = %v", err)
	}
	intents, err = store.ListChallengeIntents(ctx)
	if err != nil {
		t.Fatalf("ListChallengeIntents() after complete error = %v", err)
	}
	if len(intents) != 1 || intents[0].Status != ChallengeIntentCompleted {
		t.Fatalf("completed intents = %#v", intents)
	}

	if tree := readStateTree(t, root); bytes.Contains(tree, []byte(challengeValue)) {
		t.Fatalf("state tree contains raw challenge value: %s", tree)
	}
}

func readStateTree(t *testing.T, root string) []byte {
	t.Helper()
	var result []byte
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		result = append(result, data...)
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir(%q) error = %v", root, err)
	}
	return result
}
