package certs

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/pkg/acmeflow"
)

func TestLoadPersistedACMEStateStoreWinsOverStaleLegacyAccount(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 25, 14, 0, 0, 0, time.UTC)
	manager := mustNewManager(t, t.TempDir(), withNow(func() time.Time { return now }))
	t.Cleanup(func() { _ = manager.Close() })
	const certificateID = 6201
	lookup := manager.acmeAccountLookup()
	authoritativeKey := mustCreateAccountKeyPEM(t)
	staleLegacyKey := mustCreateAccountKeyPEM(t)
	authoritativeMetadata := acmeflow.AccountMetadata{
		Version:      acmeflow.AccountMetadataVersion,
		DirectoryURL: lookup.DirectoryURL,
		Email:        lookup.Email,
		URI:          "https://acme.example/account/authoritative",
	}
	store, err := acmeflow.OpenStateStore(manager.acmeStateRoot(certificateID), acmeflow.WithStateClock(func() time.Time { return now }))
	if err != nil {
		t.Fatalf("OpenStateStore() error = %v", err)
	}
	if err := store.SaveAccountKey(context.Background(), lookup, authoritativeKey); err != nil {
		t.Fatalf("SaveAccountKey(authoritative) error = %v", err)
	}
	if err := store.SaveAccountMetadata(context.Background(), authoritativeMetadata); err != nil {
		t.Fatalf("SaveAccountMetadata(authoritative) error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close(authoritative store) error = %v", err)
	}

	staleMetadata := authoritativeMetadata
	staleMetadata.URI = "https://acme.example/account/stale-sidecar"
	if err := manager.savePersistedACMEAccountState(certificateID, acmeIssueResult{AccountKeyPEM: staleLegacyKey, Account: staleMetadata}); err != nil {
		t.Fatalf("write stale legacy account fixture: %v", err)
	}
	persisted, err := manager.loadPersistedACMEMaterial(context.Background(), certificateID)
	if err != nil {
		t.Fatalf("loadPersistedACMEMaterial() error = %v", err)
	}
	defer persisted.store.Close()
	if !bytes.Equal(persisted.accountKeyPEM, authoritativeKey) || persisted.account.URI != authoritativeMetadata.URI {
		t.Fatalf("loaded account = key match %t, metadata %#v", bytes.Equal(persisted.accountKeyPEM, authoritativeKey), persisted.account)
	}
	record, err := persisted.store.LoadAccount(context.Background(), lookup)
	if err != nil || !bytes.Equal(record.KeyPEM, authoritativeKey) || record.Metadata.URI != authoritativeMetadata.URI {
		t.Fatalf("authoritative store changed during legacy load: %#v, %v", record, err)
	}
}

func TestLoadPersistedACMEStateDoesNotPairAuthoritativeKeyWithStaleLegacyMetadata(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 25, 14, 15, 0, 0, time.UTC)
	manager := mustNewManager(t, t.TempDir(), withNow(func() time.Time { return now }))
	t.Cleanup(func() { _ = manager.Close() })
	const certificateID = 6203
	lookup := manager.acmeAccountLookup()
	authoritativeKey := mustCreateAccountKeyPEM(t)
	store, err := acmeflow.OpenStateStore(manager.acmeStateRoot(certificateID), acmeflow.WithStateClock(func() time.Time { return now }))
	if err != nil {
		t.Fatalf("OpenStateStore() error = %v", err)
	}
	if err := store.SaveAccountKey(context.Background(), lookup, authoritativeKey); err != nil {
		t.Fatalf("SaveAccountKey(authoritative) error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close(authoritative store) error = %v", err)
	}

	staleMetadata := acmeflow.AccountMetadata{
		Version:      acmeflow.AccountMetadataVersion,
		DirectoryURL: lookup.DirectoryURL,
		Email:        lookup.Email,
		URI:          "https://acme.example/account/stale-sidecar",
	}
	if err := manager.savePersistedACMEAccountState(certificateID, acmeIssueResult{
		AccountKeyPEM: mustCreateAccountKeyPEM(t),
		Account:       staleMetadata,
	}); err != nil {
		t.Fatalf("write stale legacy account fixture: %v", err)
	}

	persisted, err := manager.loadPersistedACMEMaterial(context.Background(), certificateID)
	if err != nil {
		t.Fatalf("loadPersistedACMEMaterial() error = %v", err)
	}
	defer persisted.store.Close()
	if !bytes.Equal(persisted.accountKeyPEM, authoritativeKey) {
		t.Fatal("authoritative account key was replaced by the legacy sidecar")
	}
	if persisted.account.URI != "" {
		t.Fatalf("stale legacy metadata was paired with the authoritative key: %#v", persisted.account)
	}
	record, err := persisted.store.LoadAccount(context.Background(), lookup)
	if err != nil || !bytes.Equal(record.KeyPEM, authoritativeKey) || record.Metadata.URI != "" {
		t.Fatalf("authoritative key-only store changed during legacy load: %#v, %v", record, err)
	}
}

func TestAgentACMEStateStorePersistsAccountCrashWindowsAndPreservesLegacyRegistration(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 25, 14, 30, 0, 0, time.UTC)
	manager := mustNewManager(t, t.TempDir(), withNow(func() time.Time { return now }))
	t.Cleanup(func() { _ = manager.Close() })
	const certificateID = 6202
	legacyRegistration := []byte(`{"uri":"https://acme.example/account/legacy"}`)
	if err := manager.saveManagedCertificateState(certificateID, managedCertificateState{
		ACME: &model.ManagedCertificateACMEState{
			Account: model.ManagedCertificateACMEAccountState{Registration: legacyRegistration},
		},
	}); err != nil {
		t.Fatalf("save legacy managed state: %v", err)
	}
	rawStore, err := acmeflow.OpenStateStore(manager.acmeStateRoot(certificateID), acmeflow.WithStateClock(func() time.Time { return now }))
	if err != nil {
		t.Fatalf("OpenStateStore() error = %v", err)
	}
	defer rawStore.Close()
	store := &agentACMEStateStore{StateStore: rawStore, manager: manager, certificateID: certificateID}
	lookup := manager.acmeAccountLookup()
	keyPEM := mustCreateAccountKeyPEM(t)
	if err := store.SaveAccountKey(context.Background(), lookup, keyPEM); err != nil {
		t.Fatalf("SaveAccountKey() error = %v", err)
	}
	if sidecar, err := os.ReadFile(filepath.Join(manager.materialDir(certificateID), "acme_account_key.pem")); err != nil || !bytes.Equal(sidecar, keyPEM) {
		t.Fatalf("account key sidecar after crash window = match %t, %v", bytes.Equal(sidecar, keyPEM), err)
	}
	metadata := acmeflow.AccountMetadata{
		Version:      acmeflow.AccountMetadataVersion,
		DirectoryURL: lookup.DirectoryURL,
		Email:        lookup.Email,
		URI:          "https://acme.example/account/recovered",
		Contact:      []string{"mailto:ops@example.com"},
	}
	if err := store.SaveAccountMetadata(context.Background(), metadata); err != nil {
		t.Fatalf("SaveAccountMetadata() error = %v", err)
	}
	state, ok, err := manager.loadManagedCertificateState(certificateID)
	if err != nil || !ok || state.ACME == nil || state.ACME.Account.Metadata == nil {
		t.Fatalf("managed account state = %#v, %v", state, err)
	}
	if !bytes.Equal(state.ACME.Account.KeyPEM, keyPEM) || state.ACME.Account.Metadata.URI != metadata.URI {
		t.Fatalf("managed neutral account state mismatch: %#v", state.ACME.Account)
	}
	if !bytes.Equal(state.ACME.Account.Registration, legacyRegistration) {
		t.Fatalf("legacy registration was not preserved: %s", state.ACME.Account.Registration)
	}
}
