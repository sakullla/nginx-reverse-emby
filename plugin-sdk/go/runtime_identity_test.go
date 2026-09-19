package pluginsdk

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestPluginInstanceIdentityEnvironmentIsStrict(t *testing.T) {
	for _, test := range []struct {
		name    string
		value   string
		present bool
		valid   bool
	}{
		{name: "valid", value: "ss-primary", present: true, valid: true},
		{name: "missing"},
		{name: "empty", present: true},
		{name: "leading whitespace", value: " ss-primary", present: true},
		{name: "wildcard", value: "*", present: true},
		{name: "control delimiter", value: "ss\nprimary", present: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := ResolvePluginInstanceID(test.value, test.present)
			if (err == nil) != test.valid {
				t.Fatalf("ResolvePluginInstanceID(%q, %v) = %q, %v", test.value, test.present, got, err)
			}
			if test.valid && got != test.value {
				t.Fatalf("resolved identity = %q", got)
			}
		})
	}

	if err := os.Unsetenv(EnvPluginInstanceID); err != nil {
		t.Fatal(err)
	}
	if _, err := PluginInstanceIDFromEnvironment(); err == nil {
		t.Fatal("missing Host environment identity was accepted")
	}
	t.Setenv(EnvPluginInstanceID, "user\nsupplied")
	if _, err := PluginInstanceIDFromEnvironment(); err == nil {
		t.Fatal("invalid environment identity was accepted")
	}
	t.Setenv(EnvPluginInstanceID, "ss-primary")
	if got, err := PluginInstanceIDFromEnvironment(); err != nil || got != "ss-primary" {
		t.Fatalf("Host environment identity = %q, %v", got, err)
	}
}

func TestRuntimeIdentityCapabilityNegotiatesWithoutChangingLegacy(t *testing.T) {
	if features := RequiredRPCFeatures(nil); len(features) != 0 {
		t.Fatalf("legacy manifest acquired runtime identity: %v", features)
	}
	legacy := RequiredRPCFeatures([]string{string(CapabilityManagedNetworkListen)})
	if !reflect.DeepEqual(legacy, []string{RPCFeatureManagedNetworkV1}) {
		t.Fatalf("legacy managed features changed: %v", legacy)
	}

	scopes := []string{string(CapabilityManagedNetworkListen), string(CapabilityRuntimeIdentity)}
	features := RequiredRPCFeatures(scopes)
	if !reflect.DeepEqual(features, []string{RPCFeatureManagedNetworkV1, RPCFeatureRuntimeIdentityV1}) {
		t.Fatalf("runtime identity features = %v", features)
	}
	manifest := Manifest{Runtime: Runtime{Kind: RuntimeRPCService}, Permissions: []Permission{{Name: string(CapabilityRuntimeIdentity)}}}
	if err := ValidateManifestManagedCapabilities(manifest, []HostCapability{CapabilityRuntimeIdentity}); err != nil {
		t.Fatal(err)
	}
	if ValidateManifestManagedCapabilities(manifest, nil) == nil {
		t.Fatal("Host without runtime identity accepted opted-in manifest")
	}
	manifest.Runtime.Kind = RuntimeWASMPolicy
	if ValidateManifestManagedCapabilities(manifest, []HostCapability{CapabilityRuntimeIdentity}) == nil {
		t.Fatal("WASM policy acquired process runtime identity")
	}
	if !strings.Contains(string(PluginManifestSchemaV1()), `"runtime.identity"`) {
		t.Fatal("manifest schema omits runtime.identity")
	}

	declaration := RPCPluginDeclaration{
		PluginID: "ss-plugin", PluginVersion: "1.0.0",
		RequiredCapabilities: scopes,
		SupportedFeatures:    features,
		RequiredFeatures:     features,
	}
	request := RPCHandshakeRequest{
		ABI: RPCABIV1, PluginID: declaration.PluginID, PluginVersion: declaration.PluginVersion,
		PackageDigest: "package", ArtifactDigest: "artifact", Generation: "generation",
		GrantedScopes: scopes, RequiredFeatures: legacy,
	}
	if _, err := NegotiateRPCHandshake(declaration, request); err == nil || !strings.Contains(err.Error(), RPCFeatureRuntimeIdentityV1) {
		t.Fatalf("Host missing runtime identity feature was accepted: %v", err)
	}
	request.RequiredFeatures = features
	response, err := NegotiateRPCHandshake(declaration, request)
	if err != nil || !reflect.DeepEqual(response.Features, features) {
		t.Fatalf("runtime identity handshake = %+v, %v", response, err)
	}
}
