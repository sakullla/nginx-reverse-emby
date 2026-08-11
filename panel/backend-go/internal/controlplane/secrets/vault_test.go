package secrets_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"sync"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/secrets"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func TestKeyringFromEnvironmentFallsBackToPanelToken(t *testing.T) {
	t.Setenv("PANEL_VAULT_MASTER_KEY", "")
	t.Setenv("PANEL_VAULT_KEY_ID", "primary")
	t.Setenv("API_TOKEN", "stable-panel-token-for-vault-derivation")
	t.Setenv("NRE_PANEL_TOKEN", "")

	keyring, err := secrets.KeyringFromEnvironment()
	if err != nil {
		t.Fatalf("KeyringFromEnvironment() error = %v", err)
	}
	want := sha256.Sum256([]byte("nre-panel-vault-v1\x00stable-panel-token-for-vault-derivation"))
	if keyring.CurrentKeyID != "primary" || !bytes.Equal(keyring.Keys["primary"], want[:]) {
		t.Fatalf("derived keyring = %+v", keyring)
	}
}

func TestKeyringFromEnvironmentExplicitKeyTakesPrecedence(t *testing.T) {
	t.Setenv("PANEL_VAULT_MASTER_KEY", "invalid-explicit-key")
	t.Setenv("API_TOKEN", "valid-panel-token-that-must-not-hide-errors")
	if _, err := secrets.KeyringFromEnvironment(); !errors.Is(err, secrets.ErrKeyUnavailable) {
		t.Fatalf("KeyringFromEnvironment() error = %v, want ErrKeyUnavailable", err)
	}
}

func TestVaultMigratesActiveSecretsToReplacementKey(t *testing.T) {
	store, err := storage.NewStore(storage.StoreConfig{Driver: "sqlite", DataRoot: t.TempDir()})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	oldKey := bytes.Repeat([]byte{0x31}, 32)
	newKey := bytes.Repeat([]byte{0x32}, 32)
	oldVault, err := secrets.NewVault(store, secrets.Keyring{CurrentKeyID: "primary", Keys: map[string][]byte{"primary": oldKey}})
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := oldVault.Create(t.Context(), secrets.OperationContext{ActorID: "admin", ResourceGroupID: "default"}, "migration-secret", "test", "keep-me-readable")
	if err != nil {
		t.Fatal(err)
	}

	newVault, err := secrets.NewVault(store, secrets.Keyring{CurrentKeyID: "primary-v2", Keys: map[string][]byte{"primary-v2": newKey, "primary": oldKey}})
	if err != nil {
		t.Fatal(err)
	}
	migrated, err := newVault.MigrateToCurrentKey(t.Context())
	if err != nil || migrated != 1 {
		t.Fatalf("MigrateToCurrentKey() = %d, %v", migrated, err)
	}
	version, err := store.GetSecretVersion(t.Context(), metadata.ID, metadata.ActiveVersion)
	if err != nil || version.KeyID != "primary-v2" {
		t.Fatalf("migrated version = %+v, %v", version, err)
	}
	plaintext, err := newVault.Resolve(t.Context(), secrets.OperationContext{ActorID: "admin", ResourceGroupID: "default"}, metadata.ID)
	if err != nil || string(plaintext) != "keep-me-readable" {
		t.Fatalf("Resolve() = %q, %v", plaintext, err)
	}
	if migrated, err = newVault.MigrateToCurrentKey(t.Context()); err != nil || migrated != 0 {
		t.Fatalf("idempotent MigrateToCurrentKey() = %d, %v", migrated, err)
	}
}

func TestKeyringFromEnvironmentLoadsPreviousDerivedKey(t *testing.T) {
	t.Setenv("PANEL_VAULT_MASTER_KEY", "3232323232323232323232323232323232323232323232323232323232323232")
	t.Setenv("PANEL_VAULT_KEY_ID", "primary-v2")
	t.Setenv("PANEL_VAULT_PREVIOUS_MASTER_KEY", "")
	t.Setenv("PANEL_VAULT_PREVIOUS_API_TOKEN", "old-panel-token")
	t.Setenv("PANEL_VAULT_PREVIOUS_KEY_ID", "primary")

	keyring, err := secrets.KeyringFromEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	wantPrevious := sha256.Sum256([]byte("nre-panel-vault-v1\x00old-panel-token"))
	if keyring.CurrentKeyID != "primary-v2" || !bytes.Equal(keyring.Keys["primary"], wantPrevious[:]) {
		t.Fatalf("keyring = %+v", keyring)
	}
}

type vaultTargetRevoker struct {
	mu      sync.Mutex
	targets []pluginsdk.HostTarget
}

func (revoker *vaultTargetRevoker) RevokeTarget(target pluginsdk.HostTarget) {
	revoker.mu.Lock()
	revoker.targets = append(revoker.targets, target)
	revoker.mu.Unlock()
}

func TestVaultPersistsOnlyEnvelopeCiphertextAndRotationKeepsReference(t *testing.T) {
	t.Parallel()
	store, err := storage.NewStore(storage.StoreConfig{Driver: "sqlite", DataRoot: t.TempDir()})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	key := bytes.Repeat([]byte{0x42}, 32)
	vault, err := secrets.NewVault(store, secrets.Keyring{CurrentKeyID: "test-key", Keys: map[string][]byte{"test-key": key}})
	if err != nil {
		t.Fatalf("NewVault() error = %v", err)
	}
	revoker := &vaultTargetRevoker{}
	vault.SetPluginCapabilityTargetRevoker(revoker)
	ctx := context.Background()
	op := secrets.OperationContext{ActorID: "admin", ResourceGroupID: "default"}
	metadata, err := vault.Create(ctx, op, "cloudflare", "dns", "first-plaintext-token")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	version, err := store.GetSecretVersion(ctx, metadata.ID, 1)
	if err != nil {
		t.Fatalf("GetSecretVersion() error = %v", err)
	}
	if bytes.Contains(version.Ciphertext, []byte("first-plaintext-token")) {
		t.Fatal("ciphertext contains plaintext")
	}
	resolved, err := vault.Resolve(ctx, op, metadata.ID)
	if err != nil || string(resolved) != "first-plaintext-token" {
		t.Fatalf("Resolve() = %q, %v", resolved, err)
	}
	rotated, err := vault.Rotate(ctx, op, metadata.ID, "second-plaintext-token")
	if err != nil {
		t.Fatalf("Rotate() error = %v", err)
	}
	if rotated.ID != metadata.ID || rotated.ActiveVersion != 2 {
		t.Fatalf("Rotate() metadata = %+v", rotated)
	}
	revoker.mu.Lock()
	if len(revoker.targets) != 1 || revoker.targets[0] != (pluginsdk.HostTarget{Kind: "secret", ID: metadata.ID, ResourceGroupID: "default"}) {
		t.Fatalf("rotation revocations = %+v", revoker.targets)
	}
	revoker.mu.Unlock()
	resolved, err = vault.Resolve(ctx, op, metadata.ID)
	if err != nil || string(resolved) != "second-plaintext-token" {
		t.Fatalf("Resolve(rotated) = %q, %v", resolved, err)
	}
	events, err := store.ListAuditEvents(ctx, 20)
	if err != nil {
		t.Fatalf("ListAuditEvents() error = %v", err)
	}
	for _, event := range events {
		if bytes.Contains([]byte(event.MetadataJSON), []byte("plaintext-token")) {
			t.Fatalf("audit leaked plaintext: %s", event.MetadataJSON)
		}
	}
}
