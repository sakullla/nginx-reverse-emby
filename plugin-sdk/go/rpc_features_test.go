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
