package storage

import (
	"strings"
	"testing"
	"time"

	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func TestPluginCapabilityResourceAdapterRevalidatesOpaqueBindingAndReturnsOnlyRedactedData(t *testing.T) {
	store := newStorageMigrationTestStore(t, "local")
	now := time.Now().UTC()
	secret := SecretRow{ID: "secret-1", Name: "credential", ResourceGroupID: "group-a", ActiveVersion: 1, Fingerprint: "sensitive-fingerprint", CreatedAt: now, RotatedAt: now}
	if err := store.db.Create(&secret).Error; err != nil {
		t.Fatal(err)
	}
	binding, ok, err := store.PluginCapabilityTargetBinding(t.Context(), "secret", secret.ID)
	if err != nil || !ok {
		t.Fatalf("binding=%+v found=%t error=%v", binding, ok, err)
	}
	value, err := store.ExecutePluginCapabilityResourceCall(t.Context(), binding, pluginsdk.RPCResourceCall{RequestID: "call-1", ResourceHandle: "handle-1", Operation: pluginsdk.RPCResourceInspect})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(value), secret.ID) || strings.Contains(string(value), secret.Name) || strings.Contains(string(value), secret.Fingerprint) || !strings.Contains(string(value), binding.Version) {
		t.Fatalf("redacted resource projection=%s", value)
	}
	if err := store.db.Model(&SecretRow{}).Where("id = ?", secret.ID).Update("active_version", 2).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := store.ExecutePluginCapabilityResourceCall(t.Context(), binding, pluginsdk.RPCResourceCall{RequestID: "call-2", ResourceHandle: "handle-1", Operation: pluginsdk.RPCResourceInspect}); err == nil {
		t.Fatal("stale resource binding was accepted")
	}
}
