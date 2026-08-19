package pluginsdk

import (
	"reflect"
	"strings"
	"testing"
)

func TestNegotiateRPCHandshakeProjectsGrantedCapabilitiesAndRequestedFeatures(t *testing.T) {
	response, err := NegotiateRPCHandshake(RPCPluginDeclaration{
		PluginID:             "example-plugin",
		PluginVersion:        "1.2.3",
		RequiredCapabilities: []string{"secret.use", "event.emit"},
		SupportedFeatures:    []string{RPCFeatureDurableActionsV1},
	}, RPCHandshakeRequest{
		ABI: RPCABIV1, PluginID: "example-plugin", PluginVersion: "1.2.3",
		PackageDigest: "package", ArtifactDigest: "artifact", Generation: "generation",
		GrantedScopes:    []string{"secret.use", "dns.manage", "event.emit"},
		RequiredFeatures: []string{RPCFeatureDurableActionsV1},
	})
	if err != nil {
		t.Fatalf("NegotiateRPCHandshake() error = %v", err)
	}
	if !reflect.DeepEqual(response.Capabilities, []string{"event.emit", "secret.use"}) {
		t.Fatalf("capabilities = %v", response.Capabilities)
	}
	if !reflect.DeepEqual(response.Features, []string{RPCFeatureDurableActionsV1}) || response.ABI != RPCABIV1 {
		t.Fatalf("response = %+v", response)
	}
}

func TestNegotiateRPCHandshakeFailsClosedForMissingGrantOrUnsupportedFeature(t *testing.T) {
	declaration := RPCPluginDeclaration{
		PluginID: "example-plugin", PluginVersion: "1.2.3",
		RequiredCapabilities: []string{"secret.use"},
	}
	request := RPCHandshakeRequest{
		ABI: RPCABIV1, PluginID: "example-plugin", PluginVersion: "1.2.3",
		PackageDigest: "package", ArtifactDigest: "artifact", Generation: "generation",
	}
	if _, err := NegotiateRPCHandshake(declaration, request); err == nil || !strings.Contains(err.Error(), "not granted") {
		t.Fatalf("missing grant error = %v", err)
	}
	request.GrantedScopes = []string{"secret.use"}
	request.RequiredFeatures = []string{RPCFeatureDurableActionsV1}
	if _, err := NegotiateRPCHandshake(declaration, request); err == nil || !strings.Contains(err.Error(), "not acknowledged") {
		t.Fatalf("unsupported feature error = %v", err)
	}
}

func TestNegotiateRPCHandshakeRejectsIdentityAndNonCanonicalLists(t *testing.T) {
	request := RPCHandshakeRequest{ABI: RPCABIV1, PluginID: "example", PluginVersion: "1", PackageDigest: "package", ArtifactDigest: "artifact", Generation: "generation"}
	if _, err := NegotiateRPCHandshake(RPCPluginDeclaration{PluginID: "other", PluginVersion: "1"}, request); err == nil {
		t.Fatal("identity mismatch was accepted")
	}
	request.PluginID = "example"
	request.GrantedScopes = []string{"secret.use", "secret.use"}
	if _, err := NegotiateRPCHandshake(RPCPluginDeclaration{PluginID: "example", PluginVersion: "1"}, request); err == nil {
		t.Fatal("duplicate grant was accepted")
	}
}
