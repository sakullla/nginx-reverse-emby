package acmeflow

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type durableContractFixture struct {
	t        *testing.T
	root     string
	now      time.Time
	fault    PersistenceFaultPoint
	faultErr error
}

func newDurableContractFixture(t *testing.T) *durableContractFixture {
	return &durableContractFixture{
		t: t, root: t.TempDir(), now: time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC),
		faultErr: errors.New("injected durable-state fault"),
	}
}

func (fixture *durableContractFixture) open(name string) *StateStore {
	fixture.t.Helper()
	store, err := OpenStateStore(filepath.Join(fixture.root, name),
		WithStateClock(func() time.Time { return fixture.now }),
		WithPersistenceFaultInjector(func(point PersistenceFaultPoint) error {
			if fixture.fault != "" && point == fixture.fault {
				return fixture.faultErr
			}
			return nil
		}),
	)
	if err != nil {
		fixture.t.Fatalf("OpenStateStore(%q) error = %v", name, err)
	}
	return store
}

func TestDurableStateAtomicityConfinementAndRecovery(t *testing.T) {
	fixture := newDurableContractFixture(t)
	ctx := context.Background()

	t.Run("account atomicity and reopen", func(t *testing.T) {
		store := fixture.open("account")
		lookup := AccountLookup{DirectoryURL: "https://ca.example/directory", Email: "ops@example.com"}
		keyPEM := mustRSAKeyPEM(t)
		metadata := AccountMetadata{
			Version: AccountMetadataVersion, DirectoryURL: lookup.DirectoryURL, Email: lookup.Email,
			URI: "https://ca.example/acct/7", Contact: []string{"mailto:ops@example.com"},
		}
		if err := store.SaveAccountKey(ctx, lookup, keyPEM); err != nil {
			t.Fatal(err)
		}
		if err := store.SaveAccountMetadata(ctx, metadata); err != nil {
			t.Fatal(err)
		}
		fixture.fault = "account.metadata.file_synced"
		updated := metadata
		updated.Contact = []string{"mailto:new@example.com"}
		if err := store.SaveAccountMetadata(ctx, updated); !errors.Is(err, fixture.faultErr) {
			t.Fatalf("SaveAccountMetadata(fault) error = %v", err)
		}
		fixture.fault = ""
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		store = fixture.open("account")
		t.Cleanup(func() { _ = store.Close() })
		record, err := store.LoadAccount(ctx, lookup)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(record.KeyPEM, keyPEM) || len(record.Metadata.Contact) != 1 || record.Metadata.Contact[0] != metadata.Contact[0] {
			t.Fatalf("reopened account = %#v", record)
		}
		if err := store.SaveAccountKey(ctx, lookup, mustOtherRSAKeyPEM(t)); err == nil {
			t.Fatal("SaveAccountKey() replaced immutable account key")
		}
	})

	t.Run("challenge atomicity and secret absence", func(t *testing.T) {
		store := fixture.open("challenge")
		const secret = "dns-token-canary.super-secret.raw-value"
		intent, err := NewChallengeIntent("Example.COM.", "_acme-challenge.Media.Example.COM.", secret)
		if err != nil {
			t.Fatal(err)
		}
		fixture.fault = "challenge.intent.file_synced"
		if err := store.SaveChallengeIntent(ctx, intent); !errors.Is(err, fixture.faultErr) {
			t.Fatalf("SaveChallengeIntent(fault) error = %v", err)
		}
		fixture.fault = ""
		if intents, err := store.ListChallengeIntents(ctx); err != nil || len(intents) != 0 {
			t.Fatalf("intents after failed atomic write = %#v, %v", intents, err)
		}
		if err := store.SaveChallengeIntent(ctx, intent); err != nil {
			t.Fatal(err)
		}
		if err := store.SetChallengeRecordID(ctx, intent.ID, "record-123"); err != nil {
			t.Fatal(err)
		}
		if err := store.CompleteChallengeIntent(ctx, intent.ID); err != nil {
			t.Fatal(err)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		store = fixture.open("challenge")
		t.Cleanup(func() { _ = store.Close() })
		intents, err := store.ListChallengeIntents(ctx)
		if err != nil || len(intents) != 1 || intents[0].Status != ChallengeIntentCompleted || intents[0].RecordID != "record-123" {
			t.Fatalf("reopened intents = %#v, %v", intents, err)
		}
		if tree := readDurableStateTree(t, filepath.Join(fixture.root, "challenge")); bytes.Contains(tree, []byte(secret)) {
			t.Fatalf("durable state leaked challenge secret: %s", tree)
		}
	})

	t.Run("path and symlink confinement", func(t *testing.T) {
		store := fixture.open("confinement")
		t.Cleanup(func() { _ = store.Close() })
		if _, err := store.LoadGeneration(ctx, "../../outside"); err == nil {
			t.Fatal("LoadGeneration() accepted path traversal")
		}
		outside := filepath.Join(fixture.root, "outside")
		if err := os.Mkdir(outside, 0o700); err != nil {
			t.Fatal(err)
		}
		root := filepath.Join(fixture.root, "confinement")
		if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
			t.Skipf("symlink creation unavailable: %v", err)
		}
		if err := store.fs.writeFileAtomic("escape/leak", []byte("secret"), "test.confinement"); err == nil {
			t.Fatal("writeFileAtomic() followed escaping intermediate symlink")
		}
		if _, err := os.Stat(filepath.Join(outside, "leak")); !os.IsNotExist(err) {
			t.Fatalf("outside path was modified: %v", err)
		}
	})

	t.Run("generation fault and reopen", func(t *testing.T) {
		store := fixture.open("generation")
		input := durableGenerationInput(t, fixture.now)
		fixture.fault = "generation.manifest.renamed"
		if _, err := store.StageGeneration(ctx, input); !errors.Is(err, fixture.faultErr) {
			t.Fatalf("StageGeneration(fault) error = %v", err)
		}
		fixture.fault = ""
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		store = fixture.open("generation")
		if _, err := store.Reconcile(ctx); err != nil {
			t.Fatalf("Reconcile() error = %v", err)
		}
		if _, err := store.LoadCurrent(ctx); !errors.Is(err, ErrNoCurrentGeneration) {
			t.Fatalf("LoadCurrent() after failed stage error = %v", err)
		}
		manifest, err := store.StageGeneration(ctx, input)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.PromoteGeneration(ctx, manifest.ID, nil); err != nil {
			t.Fatal(err)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		store = fixture.open("generation")
		t.Cleanup(func() { _ = store.Close() })
		current, err := store.LoadCurrent(ctx)
		if err != nil || current.Manifest.ID != manifest.ID || !bytes.Equal(current.Material.PrivateKeyPEM, input.Material.PrivateKeyPEM) {
			t.Fatalf("reopened generation = %#v, %v", current.Manifest, err)
		}
	})
}

func durableGenerationInput(t *testing.T, now time.Time) GenerationInput {
	t.Helper()
	key := mustTestRSAKey(t)
	keyPEM := mustRSAKeyPEM(t)
	return GenerationInput{
		Material: CertificateMaterial{
			CertificatePEM: issueTestCertificate(t, key, []string{"example.com"}, nil, "example.com", now.Add(-time.Minute), now.Add(time.Hour)),
			PrivateKeyPEM:  keyPEM,
		},
		Policy: MaterialPolicy{Identifiers: []Identifier{{Type: IdentifierDNS, Value: "example.com"}}, Now: now},
		Account: AccountMetadata{
			Version: AccountMetadataVersion, DirectoryURL: "https://ca.example/directory", Email: "ops@example.com", URI: "https://ca.example/acct/7",
		},
	}
}

func mustOtherRSAKeyPEM(t *testing.T) []byte {
	t.Helper()
	return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(mustOtherTestRSAKey(t))})
}

func readDurableStateTree(t *testing.T, root string) []byte {
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
