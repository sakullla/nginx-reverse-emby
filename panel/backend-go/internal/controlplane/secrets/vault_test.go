package secrets_test

import (
	"bytes"
	"context"
	"sync"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/secrets"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

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
