package model

import (
	"encoding/json"
	"strings"
	"testing"

	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func TestKnownPluginExtensionPointAllowsResourceGroup(t *testing.T) {
	t.Parallel()
	if !knownPluginExtensionPoint(pluginsdk.ExtensionResourceGroup) {
		t.Fatal("resource.group extension point is not supported")
	}
}

func TestPluginGenerationGrantsAllowDeclaredFullNetwork(t *testing.T) {
	t.Parallel()
	if err := validatePluginGrants([]PluginGrantProjection{{Name: pluginsdk.PermissionNetworkFull}}); err != nil {
		t.Fatalf("network.full generation grant = %v", err)
	}
}

func TestPluginSnapshotAdmitsPublishedSDKRequiredFeatures(t *testing.T) {
	generation := PluginGeneration{
		ID: "provider-generation", InstanceID: "instance", OperationID: "operation", Revision: 7, PluginID: "plugin", PluginVersion: "1.0.0", PackageDigest: strings.Repeat("a", 64),
		Runtime:         PluginRuntimeDescriptor{Kind: PluginRuntimeRPCService, ABI: PluginRPCABIV1, HostScope: "agent", Entry: "artifacts/plugin"},
		Artifact:        PluginArtifactDescriptor{ArtifactID: "artifact", PackageIdentity: "plugin@1.0.0", RelativePath: "artifacts/plugin", SHA256: strings.Repeat("b", 64), SizeBytes: 1, Mode: "executable", GOOS: "linux", GOARCH: "amd64", SignatureVerified: true, SignerKeyID: "key", SignerFingerprint: strings.Repeat("c", 64)},
		ExtensionPoints: []string{"l4.accept"}, ConfigVersion: 1, Config: json.RawMessage(`{}`),
		ResourceBudget: PluginResourceBudget{TimeoutMS: 1000, MemoryBytes: 1 << 20, Concurrency: 2, InputBytes: 4096, OutputBytes: 4096, CPUMillis: 100},
		Target:         PluginTargetBinding{Kind: "agent", ID: "edge", ResourceGroupID: "default", Version: 1}, FailurePolicy: PluginFailurePolicy{OnError: "degraded", OnBudget: "fail-closed", Restart: "never", CoreFallback: "preserve"},
	}
	scopes := []string{string(pluginsdk.CapabilityDatasetQuery), string(pluginsdk.CapabilityDatasetResolve), pluginsdk.PermissionManagedNetworkListen, pluginsdk.PermissionManagedNetworkDial, pluginsdk.PermissionScopedSecretRead, pluginsdk.PermissionScopedSecretWrite}
	for _, scope := range scopes {
		generation.Grants = append(generation.Grants, PluginGrantProjection{Name: scope})
	}
	generation.Grants = append(generation.Grants, PluginGrantProjection{Name: pluginsdk.PermissionManagedNetworkListen, ResourceKind: "network-endpoint", ResourceID: "tcp://[::1]:8443"})
	generation.RequiredFeatures = pluginsdk.RequiredRPCFeaturesForExtensions(scopes, generation.ExtensionPoints)
	snapshot := Snapshot{Revision: 7, PluginGenerations: []PluginGeneration{generation}}
	if err := ValidatePluginGenerations(snapshot, false); err != nil {
		t.Fatalf("actual SDK-projected snapshot rejected: %v", err)
	}
	for _, features := range [][]string{{"rpc.unknown.v1"}, {" rpc.scoped-secrets.v1"}, {pluginsdk.RPCFeatureScopedSecretsV1, pluginsdk.RPCFeatureScopedSecretsV1}} {
		snapshot.PluginGenerations[0].RequiredFeatures = features
		if err := ValidatePluginGenerations(snapshot, false); err == nil {
			t.Fatalf("invalid features accepted: %q", features)
		}
	}
	snapshot.PluginGenerations[0].RequiredFeatures = nil
	if err := ValidatePluginGenerations(snapshot, false); err != nil {
		t.Fatalf("additive compatibility rejected legacy feature set: %v", err)
	}
}

func TestManagedNetworkGrantEndpointSelectors(t *testing.T) {
	for _, permission := range []string{pluginsdk.PermissionManagedNetworkListen, pluginsdk.PermissionManagedNetworkDial} {
		for _, value := range []string{"tcp://127.0.0.1:443", "udp://192.0.2.1:53", "tcp://[::1]:8443", "udp://[2001:db8::1]:53"} {
			if err := validatePluginGrants([]PluginGrantProjection{{Name: permission, ResourceKind: "network-endpoint", ResourceID: value}}); err != nil {
				t.Errorf("valid %s %s: %v", permission, value, err)
			}
		}
		for _, value := range []string{"https://[::1]:443", "tcp://::1:443", "tcp://[::1%lo]:443", "tcp://[::1]:0", "tcp://[::1]:65536", "tcp://[::1]:0443", "tcp://user@[::1]:443", "tcp://[::1]:443/path", "tcp://[::1]:443?x=y", "tcp://[::1]:443#x", "tcp://[ff02::1]:443", "tcp://127.0.0.1:abc", "tcp://127.0.0.1:+443", "tcp://[127.0.0.1]:443"} {
			if err := validatePluginGrants([]PluginGrantProjection{{Name: permission, ResourceKind: "network-endpoint", ResourceID: value}}); err == nil {
				t.Errorf("malformed endpoint accepted: %s", value)
			}
		}
		if err := validatePluginGrants([]PluginGrantProjection{{Name: permission, ResourceKind: "foreign-resource", ResourceID: "tcp://[::1]:443"}}); err == nil {
			t.Error("foreign resource kind accepted")
		}
	}
	if !ValidManagedNetworkEndpointSelector(pluginsdk.PermissionManagedNetworkListen, "tcp://[::]:443") || ValidManagedNetworkEndpointSelector(pluginsdk.PermissionManagedNetworkDial, "tcp://[::]:443") {
		t.Error("listen and outbound wildcard semantics collapsed")
	}
	if !ValidManagedNetworkEndpointSelector(pluginsdk.PermissionManagedNetworkDial, "tcp://example.test:443") || ValidManagedNetworkEndpointSelector(pluginsdk.PermissionManagedNetworkListen, "tcp://example.test:443") {
		t.Error("listen and outbound hostname semantics collapsed")
	}
	if err := validatePluginGrants([]PluginGrantProjection{{Name: "storage.read", ResourceID: "tcp://[::1]:443"}}); err == nil {
		t.Error("non-network resource identity was relaxed")
	}
}
