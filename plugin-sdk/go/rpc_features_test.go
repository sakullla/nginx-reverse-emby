package pluginsdk

import "testing"

func TestRequiredRPCFeaturesGateDurableActionsWithoutBreakingLegacyGuests(t *testing.T) {
	if features := RequiredRPCFeatures([]string{"relay.read"}); len(features) != 0 {
		t.Fatalf("legacy features = %v", features)
	}
	features := RequiredRPCFeatures([]string{string(CapabilityUIDynamicActions)})
	if len(features) != 1 || features[0] != RPCFeatureDurableActionsV1 {
		t.Fatalf("dynamic action features = %v", features)
	}
	if err := ValidateRPCFeatures(features, nil); err == nil {
		t.Fatal("expected an old action guest without feature acknowledgement to be rejected")
	}
	if err := ValidateRPCFeatures(features, features); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRPCFeatures(nil, features); err == nil {
		t.Fatal("expected an unrequested feature to be rejected")
	}
}

func TestHTTPOutboundOnlyRequiresProviderFeatureForProviderExtension(t *testing.T) {
	if features := RequiredRPCFeatures([]string{PermissionHTTPOutbound}); len(features) != 0 {
		t.Fatalf("general outbound features = %#v", features)
	}
	features := RequiredRPCFeaturesForExtensions([]string{PermissionHTTPOutbound}, []string{ExtensionHTTPBackendProvider})
	if len(features) != 1 || features[0] != RPCFeatureHTTPBackendProviderV1 {
		t.Fatalf("provider features = %#v", features)
	}
}

func TestPolicyEntryOverlayFeaturePreservesV010ModeOnlyNegotiation(t *testing.T) {
	legacy := RequiredRPCFeatures([]string{string(CapabilityPolicyControl)})
	if len(legacy) != 1 || legacy[0] != RPCFeaturePolicyControlsV1 {
		t.Fatalf("v0.10 policy-control features = %v", legacy)
	}
	required := RequiredRPCFeatures([]string{string(CapabilityPolicyControl), string(CapabilityPolicyEntryOverlays)})
	if len(required) != 2 || required[0] != RPCFeaturePolicyControlsV1 || required[1] != RPCFeaturePolicyEntryOverlaysV1 {
		t.Fatalf("entry-overlay features = %v", required)
	}
	if err := ValidateRPCFeatures(required, legacy); err == nil {
		t.Fatal("v0.10 Host acknowledgement accepted for entry overlays")
	}
	if err := ValidateRPCFeatures(required, required); err != nil {
		t.Fatal(err)
	}
	if features := RequiredRPCFeatures([]string{string(CapabilityPolicyEntryOverlays)}); len(features) != 2 || features[0] != RPCFeaturePolicyControlsV1 || features[1] != RPCFeaturePolicyEntryOverlaysV1 {
		t.Fatalf("entry-overlay capability features = %v", features)
	}
}
