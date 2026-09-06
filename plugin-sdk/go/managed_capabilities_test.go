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

func TestManifestPolicyEntryOverlayCapabilityProjectsHandshakeFeatures(t *testing.T) {
	legacyScopes := []string{string(CapabilityPolicyControl)}
	legacyFeatures := RequiredRPCFeatures(legacyScopes)
	if !reflect.DeepEqual(legacyFeatures, []string{RPCFeaturePolicyControlsV1}) {
		t.Fatalf("legacy policy features = %v", legacyFeatures)
	}

	scopes := []string{string(CapabilityPolicyControl), string(CapabilityPolicyEntryOverlays)}
	features := RequiredRPCFeatures(scopes)
	if !reflect.DeepEqual(features, []string{RPCFeaturePolicyControlsV1, RPCFeaturePolicyEntryOverlaysV1}) {
		t.Fatalf("entry overlay features = %v", features)
	}
	manifest := Manifest{Runtime: Runtime{Kind: RuntimeRPCService}, Permissions: []Permission{{Name: string(CapabilityPolicyControl)}, {Name: string(CapabilityPolicyEntryOverlays)}}}
	supported := []HostCapability{CapabilityPolicyControl, CapabilityPolicyEntryOverlays}
	if err := ValidateManifestManagedCapabilities(manifest, supported); err != nil {
		t.Fatal(err)
	}
	if ValidateManifestManagedCapabilities(manifest, supported[:1]) == nil {
		t.Fatal("Host lacking entry-overlay capability accepted the signed manifest")
	}
	manifest.Permissions = manifest.Permissions[1:]
	if ValidateManifestManagedCapabilities(manifest, supported) == nil {
		t.Fatal("entry-overlay capability without policy.control was accepted")
	}

	declaration := RPCPluginDeclaration{PluginID: "ip-policy", PluginVersion: "1.0.0", RequiredCapabilities: scopes, SupportedFeatures: features, RequiredFeatures: features}
	request := RPCHandshakeRequest{ABI: RPCABIV1, PluginID: declaration.PluginID, PluginVersion: declaration.PluginVersion, PackageDigest: "package", ArtifactDigest: "artifact", Generation: "generation", GrantedScopes: scopes, RequiredFeatures: legacyFeatures}
	if _, err := NegotiateRPCHandshake(declaration, request); err == nil {
		t.Fatal("manifest projection missing the new feature passed handshake")
	}
	request.RequiredFeatures = features
	if _, err := NegotiateRPCHandshake(declaration, request); err != nil {
		t.Fatal(err)
	}
}
