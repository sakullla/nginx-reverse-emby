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
