package embedded

import (
	"context"
	"encoding/json"
	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
	"strings"
	"testing"
)

type scopedSourceFixture struct {
	received PluginSecretRedemptionRequest
	result   json.RawMessage
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
