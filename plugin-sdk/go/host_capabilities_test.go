package pluginsdk

import (
	"strings"
	"testing"
)

func TestCanonicalPluginCapabilitiesAndCalls(t *testing.T) {
	capabilities := []HostCapability{
		CapabilityPolicyAtomicState, CapabilityPolicyMonotonicClock, CapabilityPolicyTrustedSource,
		CapabilityServiceRevocableResourceHandle, CapabilityUIDynamicActions, CapabilityHTTPOutbound,
		CapabilityHTTPRule, CapabilityUIDynamic,
	}
	for _, capability := range capabilities {
		if err := capability.Validate(); err != nil {
			t.Fatalf("Validate(%q): %v", capability, err)
		}
	}
	call := HostCapabilityCall{
		PluginID: "official.waf", InstanceID: "instance-1", Generation: "generation-1",
		Capability:  CapabilityPolicyTrustedSource,
		Actor:       HostActor{ID: "official.waf", ResourceGroupID: "group-1"},
		Target:      HostTarget{Kind: "http.rule", ID: "rule-1", ResourceGroupID: "group-1"},
		QuotaMetric: "host.call", QuotaUnits: 1,
	}
	if err := call.Validate(); err != nil {
		t.Fatal(err)
	}
	call.Target.ResourceGroupID = "group-2"
	if err := call.Validate(); err == nil {
		t.Fatal("cross-resource-group host call was accepted")
	}
	if err := (HostCapability("policy.clock.wall")).Validate(); err == nil {
		t.Fatal("unknown host capability was accepted")
	}
	if err := (HostCapability("container.compose")).Validate(); err == nil {
		t.Fatal("removed container.compose host capability was accepted")
	}
	schema := string(PluginManifestSchemaV1())
	for _, name := range []string{"container.compose", "container.read", "container.manage", "container.provider"} {
		if strings.Contains(schema, `"`+name+`"`) {
			t.Fatalf("manifest schema still declares %q", name)
		}
	}
	for _, name := range []string{"http.rule", "ui.dynamic"} {
		if !strings.Contains(schema, `"`+name+`"`) {
			t.Fatalf("manifest schema omits permission %q", name)
		}
	}
}

func TestDynamicActionIsTypedAndNonRecursive(t *testing.T) {
	action := DynamicAction{ID: "rotate", Label: "Rotate", Capability: CapabilityServiceRevocableResourceHandle, TargetKind: "secret", Confirm: "Rotate this credential?"}
	if err := action.Validate(); err != nil {
		t.Fatal(err)
	}
	action.Capability = CapabilityUIDynamicActions
	if err := action.Validate(); err == nil {
		t.Fatal("recursive dynamic action was accepted")
	}
}
