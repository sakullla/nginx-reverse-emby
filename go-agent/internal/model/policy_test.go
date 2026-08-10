package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestHTTPAndL4RulesExposeOnePolicyRef(t *testing.T) {
	snapshot := Snapshot{
		Rules:          []HTTPRule{{ID: 1, PolicyRef: &PolicyRef{ID: "shared", Overlay: json.RawMessage(`{"site":"one"}`)}}},
		L4Rules:        []L4Rule{{ID: 2, PolicyRef: &PolicyRef{ID: "shared"}}},
		PluginPolicies: []PluginPolicy{{ID: "shared", Revision: 3}},
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	wire := string(encoded)
	if strings.Count(wire, `"policy_ref"`) != 2 || !strings.Contains(wire, `"plugin_policies"`) || strings.Contains(wire, `"ip_policy_ref"`) || strings.Contains(wire, `"rate_policy_ref"`) || strings.Contains(wire, `"waf_policy_ref"`) {
		t.Fatalf("snapshot policy wire = %s", wire)
	}
	var decoded Snapshot
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if decoded.Rules[0].PolicyRef == nil || decoded.Rules[0].PolicyRef.ID != "shared" || string(decoded.Rules[0].PolicyRef.Overlay) != `{"site":"one"}` {
		t.Fatalf("decoded HTTP policy ref = %+v", decoded.Rules[0].PolicyRef)
	}
}

func TestFullRevisionRequiresPresentPluginGenerationsDependenciesAndPolicies(t *testing.T) {
	base := `{"desired_version":"v1","desired_revision":1,"agent_config":{},"rules":[],"l4_rules":[],"relay_listeners":[],"egress_profiles":[],"certificates":[],"certificate_policies":[]}`
	var absent Snapshot
	if err := json.Unmarshal([]byte(base), &absent); err != nil {
		t.Fatalf("Unmarshal(absent) error = %v", err)
	}
	if absent.HasFullRevisionPayload() {
		t.Fatal("revision without plugin_policies was accepted as full")
	}

	policyOnlyWire := strings.TrimSuffix(base, "}") + `,"plugin_policies":[]}`
	var policyOnly Snapshot
	if err := json.Unmarshal([]byte(policyOnlyWire), &policyOnly); err != nil {
		t.Fatalf("Unmarshal(policy only) error = %v", err)
	}
	if policyOnly.HasFullRevisionPayload() {
		t.Fatal("revision without plugin_generations was accepted as full")
	}

	generationsAndPoliciesWire := strings.TrimSuffix(base, "}") + `,"plugin_generations":[],"plugin_policies":[]}`
	var generationsAndPolicies Snapshot
	if err := json.Unmarshal([]byte(generationsAndPoliciesWire), &generationsAndPolicies); err != nil {
		t.Fatalf("Unmarshal(generations and policies) error = %v", err)
	}
	if generationsAndPolicies.HasFullRevisionPayload() {
		t.Fatal("revision without plugin_dependencies was accepted as full")
	}

	presentWire := strings.TrimSuffix(base, "}") + `,"plugin_generations":[],"plugin_dependencies":[],"plugin_policies":[]}`
	var present Snapshot
	if err := json.Unmarshal([]byte(presentWire), &present); err != nil {
		t.Fatalf("Unmarshal(present) error = %v", err)
	}
	if !present.HasFullRevisionPayload() {
		t.Fatal("revision with explicit empty plugin_policies was rejected")
	}
	if present.PluginPolicies == nil || len(present.PluginPolicies) != 0 {
		t.Fatalf("explicit empty plugin_policies = %#v, want non-nil empty", present.PluginPolicies)
	}
	if present.PluginGenerations == nil || len(present.PluginGenerations) != 0 {
		t.Fatalf("explicit empty plugin_generations = %#v, want non-nil empty", present.PluginGenerations)
	}
	if present.PluginDependencies == nil || len(present.PluginDependencies) != 0 {
		t.Fatalf("explicit empty plugin_dependencies = %#v, want non-nil empty", present.PluginDependencies)
	}
}

func TestPluginPoliciesWireAlwaysSerializesPresence(t *testing.T) {
	nilWire, err := json.Marshal(Snapshot{})
	if err != nil {
		t.Fatalf("Marshal(nil policies) error = %v", err)
	}
	if !strings.Contains(string(nilWire), `"plugin_policies":null`) {
		t.Fatalf("nil plugin policies wire = %s", nilWire)
	}
	if !strings.Contains(string(nilWire), `"plugin_generations":null`) {
		t.Fatalf("nil plugin generations wire = %s", nilWire)
	}
	if !strings.Contains(string(nilWire), `"plugin_dependencies":null`) {
		t.Fatalf("nil plugin dependencies wire = %s", nilWire)
	}

	emptyWire, err := json.Marshal(Snapshot{PluginPolicies: []PluginPolicy{}, PluginGenerations: []PluginGeneration{}, PluginDependencies: []PluginDependencyEdge{}})
	if err != nil {
		t.Fatalf("Marshal(empty policies) error = %v", err)
	}
	if !strings.Contains(string(emptyWire), `"plugin_policies":[]`) {
		t.Fatalf("empty plugin policies wire = %s", emptyWire)
	}
	if !strings.Contains(string(emptyWire), `"plugin_generations":[]`) {
		t.Fatalf("empty plugin generations wire = %s", emptyWire)
	}
	if !strings.Contains(string(emptyWire), `"plugin_dependencies":[]`) {
		t.Fatalf("empty plugin dependencies wire = %s", emptyWire)
	}
}
