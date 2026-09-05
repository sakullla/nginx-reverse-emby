package pluginsdk

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestManagedCapabilitiesAgreeWithSchemaAndHandshake(t *testing.T) {
	capabilities := []HostCapability{CapabilityDatasetQuery, CapabilityDatasetManage, CapabilityManagedNetworkListen, CapabilityManagedNetworkDial, CapabilityScopedSecretRead, CapabilityScopedSecretWrite}
	var schema struct {
		Defs map[string]struct {
			Enum []string `json:"enum"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(PluginManifestSchemaV1(), &schema); err != nil {
		t.Fatal(err)
	}
	scopes := make([]string, 0, len(capabilities))
	for _, capability := range capabilities {
		if err := capability.Validate(); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, permission := range schema.Defs["permission_name"].Enum {
			if permission == string(capability) {
				found = true
			}
		}
		if !found {
			t.Fatalf("manifest omits %s", capability)
		}
		scopes = append(scopes, string(capability))
		if ValidateHostCapabilityGrant(capability, scopes, nil) == nil || ValidateHostCapabilityGrant(capability, nil, scopes) == nil {
			t.Fatal("grant or signed declaration alone authorized effect")
		}
		if err := ValidateHostCapabilityGrant(capability, scopes, scopes); err != nil {
			t.Fatal(err)
		}
	}
	features := RequiredRPCFeatures(scopes)
	want := []string{RPCFeatureDatasetsV1, RPCFeatureManagedNetworkV1, RPCFeatureScopedSecretsV1}
	if !reflect.DeepEqual(features, want) {
		t.Fatalf("features = %v", features)
	}
	declaration := RPCPluginDeclaration{PluginID: "routing", PluginVersion: "1.0.0", RequiredCapabilities: scopes, SupportedFeatures: features}
	request := RPCHandshakeRequest{ABI: RPCABIV1, PluginID: "routing", PluginVersion: "1.0.0", PackageDigest: "package", ArtifactDigest: "artifact", Generation: "gen-1", GrantedScopes: scopes, RequiredFeatures: features}
	if _, err := NegotiateRPCHandshake(declaration, request); err != nil {
		t.Fatal(err)
	}
	for _, incomplete := range [][]string{nil, features[:1], features[:2]} {
		old := declaration
		old.SupportedFeatures = incomplete
		if _, err := NegotiateRPCHandshake(old, request); err == nil {
			t.Fatal("guest without required managed extension accepted")
		}
	}
	request.GrantedScopes = scopes[1:]
	if _, err := NegotiateRPCHandshake(declaration, request); err == nil {
		t.Fatal("missing dataset query grant accepted")
	}
	manifest := Manifest{Runtime: Runtime{Kind: RuntimeRPCService}}
	for _, capability := range capabilities {
		manifest.Permissions = append(manifest.Permissions, Permission{Name: string(capability)})
	}
	if err := ValidateManifestManagedCapabilities(manifest, capabilities); err != nil {
		t.Fatal(err)
	}
	if ValidateManifestManagedCapabilities(manifest, capabilities[:len(capabilities)-1]) == nil {
		t.Fatal("old Host accepted managed package")
	}
	manifest.Runtime.Kind = RuntimeWASMPolicy
	if ValidateManifestManagedCapabilities(manifest, capabilities) == nil {
		t.Fatal("WASM acquired network or secret capability")
	}
	manifest.Permissions = []Permission{{Name: string(CapabilityDatasetQuery)}}
	if err := ValidateManifestManagedCapabilities(manifest, capabilities); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{HostRuntimeDatasetOpen, HostRuntimeDatasetQuery, HostRuntimeDatasetControl, HostRuntimeDatasetStatus, HostRuntimeDatasetCatalog} {
		capability, err := DatasetRuntimeCapability(operation)
		if err != nil || capability.Validate() != nil {
			t.Fatalf("unregistered dataset operation %s", operation)
		}
	}
}

func TestPolicySecurityImportNeedsBothGrants(t *testing.T) {
	both := []string{string(CapabilityDatasetQuery), string(CapabilityPolicyTrustedSource)}
	if err := ValidatePolicyV1ImportGrant(PolicyHostDatasetQuery, both, both); err != nil {
		t.Fatal(err)
	}
	for _, missing := range [][]string{nil, both[:1], both[1:]} {
		if ValidatePolicyV1ImportGrant(PolicyHostDatasetQuery, both, missing) == nil || ValidatePolicyV1ImportGrant(PolicyHostDatasetQuery, missing, both) == nil {
			t.Fatal("dataset import accepted incomplete signed/granted capabilities")
		}
	}
	if ValidatePolicyV1ImportGrant(PolicyHostReadTrustedSource, both, nil) == nil {
		t.Fatal("trusted source import accepted absent capability")
	}
	for _, name := range []string{PolicyHostDatasetQuery, PolicyHostReadTrustedSource} {
		if _, mandatory := PolicyV1RequiredHostFunctions()[name]; mandatory {
			t.Fatalf("optional import became mandatory: %s", name)
		}
	}
}
