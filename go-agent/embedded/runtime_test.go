package embedded

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

type scopedSourceFixture struct {
	received PluginSecretRedemptionRequest
	result   json.RawMessage
}

type capabilityAuditStateSink struct{}

func (capabilityAuditStateSink) Save(context.Context, RuntimeState) error { return nil }

func TestEmbeddedCapabilityAuditDefaultsOffWithoutAuditPath(t *testing.T) {
	dataDir := t.TempDir()
	runtime, err := New(Config{AgentID: "local", AgentName: "local", DataDir: dataDir}, &scopedSourceFixture{}, capabilityAuditStateSink{})
	if err != nil {
		t.Fatal(err)
	}
	if status := runtime.CapabilityAuditStatus(); status.Enabled || status.Queued != 0 || status.Dropped != 0 || status.WriteErrors != 0 || status.LowSpace || status.LastError != "" || !status.LastFlush.IsZero() {
		t.Fatalf("default-off embedded capability audit status = %+v", status)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dataDir, "audit", "plugin-capabilities.jsonl")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("default-off embedded runtime created capability audit: %v", err)
	}
}

func (s *scopedSourceFixture) Sync(context.Context, SyncRequest) (Snapshot, error) {
	return Snapshot{}, nil
}
func (s *scopedSourceFixture) RedeemScopedPluginSecret(_ context.Context, request PluginSecretRedemptionRequest) (json.RawMessage, error) {
	s.received = request
	return s.result, nil
}
func TestEmbeddedScopedRedemptionPreservesRuntimeAndProviderIdentity(t *testing.T) {
	wire, err := sdk.EncodeScopedSecretRequest(sdk.ScopedSecretRequest{Action: sdk.ScopedSecretRead, Binding: sdk.ManagedBinding{InstanceID: "instance", Generation: "runtime-generation", EntryID: "instance"}, Reference: sdk.ScopedSecretReference{InstanceID: "instance", ID: "secret", Scope: "relay", Version: strings.Repeat("a", 32)}})
	if err != nil {
		t.Fatal(err)
	}
	source := &scopedSourceFixture{result: json.RawMessage(`{"reference":"opaque-response"}`)}
	adapter := syncClientAdapter{source: source}
	request := PluginSecretRedemptionRequest{Revision: 7, GenerationID: "provider-generation", RuntimeGenerationID: "runtime-generation", InstanceID: "instance", PluginID: "plugin", OperationID: "operation", PackageDigest: strings.Repeat("a", 64), ArtifactDigest: strings.Repeat("b", 64), Scoped: wire}
	result, err := adapter.RedeemScopedPluginSecret(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if source.received.GenerationID != request.GenerationID || source.received.RuntimeGenerationID != request.RuntimeGenerationID || string(source.received.Scoped) != string(wire) || string(result) != string(source.result) {
		t.Fatal("embedded adapter lost scoped binding or response")
	}
	request.RuntimeGenerationID = "provider-generation"
	if _, err := adapter.RedeemScopedPluginSecret(t.Context(), request); err == nil {
		t.Fatal("inner/outer generation mismatch accepted")
	}
	if err := (&Runtime{}).RevokePluginGeneration(t.Context(), PluginGenerationRevokeRequest{}); err == nil {
		t.Fatal("missing exact generation runtime acknowledged revoke")
	}
}
