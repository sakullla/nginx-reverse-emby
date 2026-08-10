package storage

import (
	"net"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestPluginCapabilityTargetVersionTracksSecretAndRelayMutation(t *testing.T) {
	store := newStorageMigrationTestStore(t, "local")
	now := time.Now().UTC().Truncate(time.Second)
	secret := SecretRow{ID: "secret-1", Name: "credential", ResourceGroupID: "group-a", ActiveVersion: 1, Fingerprint: "fingerprint-1", CreatedAt: now, RotatedAt: now}
	if err := store.db.Create(&secret).Error; err != nil {
		t.Fatal(err)
	}
	first, ok, err := store.PluginCapabilityTargetVersion(t.Context(), "secret", secret.ID)
	if err != nil || !ok || first == "" {
		t.Fatalf("initial secret version=%q found=%t error=%v", first, ok, err)
	}
	if err := store.db.Model(&SecretRow{}).Where("id = ?", secret.ID).Updates(map[string]any{"active_version": 2, "fingerprint": "fingerprint-2", "rotated_at": now.Add(time.Second)}).Error; err != nil {
		t.Fatal(err)
	}
	second, ok, err := store.PluginCapabilityTargetVersion(t.Context(), "vault.secret", secret.ID)
	if err != nil || !ok || second == "" || second == first {
		t.Fatalf("rotated secret version=%q initial=%q found=%t error=%v", second, first, ok, err)
	}
	if err := store.db.Delete(&SecretRow{}, "id = ?", secret.ID).Error; err != nil {
		t.Fatal(err)
	}
	if version, exists, versionErr := store.PluginCapabilityTargetVersion(t.Context(), "secret", secret.ID); versionErr != nil || exists || version != "" {
		t.Fatalf("deleted secret version=%q found=%t error=%v", version, exists, versionErr)
	}

	relay := RelayListenerRow{ID: 7, AgentID: "local", Name: "relay", BindHostsJSON: "[]", TagsJSON: "[]", Revision: 1}
	if err := store.db.Create(&relay).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.BindResource(t.Context(), ResourceBindingRow{ID: "local-binding", ResourceKind: "agent", ResourceID: "local", ResourceGroupID: "group-a", UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	relayFirst, ok, err := store.PluginCapabilityTargetVersion(t.Context(), "relay", "local:7")
	if err != nil || !ok || relayFirst == "" {
		t.Fatalf("initial relay version=%q found=%t error=%v", relayFirst, ok, err)
	}
	if err := store.db.Model(&RelayListenerRow{}).Where("agent_id = ? AND id = ?", "local", 7).Update("revision", 2).Error; err != nil {
		t.Fatal(err)
	}
	relaySecond, ok, err := store.PluginCapabilityTargetVersion(t.Context(), "relay.listener", "local:7")
	if err != nil || !ok || relaySecond == relayFirst {
		t.Fatalf("updated relay version=%q initial=%q found=%t error=%v", relaySecond, relayFirst, ok, err)
	}
	binding, ok, err := store.PluginCapabilityTargetBinding(t.Context(), "relay", "local:7")
	if err != nil || !ok || binding.ResourceGroupID != "group-a" || binding.Version != relaySecond {
		t.Fatalf("relay binding=%+v found=%t error=%v", binding, ok, err)
	}
}

func TestEnsurePluginCapabilityDockerSocketBindingOwnsLiveEndpointAndRevokesRotation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix Docker socket ownership is unavailable on Windows")
	}
	store := newStorageMigrationTestStore(t, "local")
	socketPath := filepath.Join(t.TempDir(), "docker.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsurePluginCapabilityDockerSocketBinding(t.Context(), "default", socketPath); err != nil {
		t.Fatal(err)
	}
	first, ok, err := store.PluginCapabilityTargetBinding(t.Context(), PluginCapabilityDockerSocketKind, PluginCapabilityDockerSocketID)
	if err != nil || !ok || first.ResourceGroupID != "default" || first.Version == "" {
		t.Fatalf("initial Docker binding=%+v found=%t error=%v", first, ok, err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if binding, exists, bindingErr := store.PluginCapabilityTargetBinding(t.Context(), PluginCapabilityDockerSocketKind, PluginCapabilityDockerSocketID); bindingErr != nil || exists || binding.Version != "" {
		t.Fatalf("closed Docker binding=%+v found=%t error=%v", binding, exists, bindingErr)
	}
	replacement, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
	second, ok, err := store.PluginCapabilityTargetBinding(t.Context(), PluginCapabilityDockerSocketKind, PluginCapabilityDockerSocketID)
	if err != nil || !ok || second.Version == "" || second.Version == first.Version {
		t.Fatalf("replacement Docker binding=%+v initial=%+v found=%t error=%v", second, first, ok, err)
	}
}
