package storage

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPolicySnapshotProjectionPreservesAttachmentsAndExplicitEmptyDefinitions(t *testing.T) {
	httpRules := snapshotHTTPRules([]HTTPRuleRow{{
		ID: 1, AgentID: "edge-1", FrontendURL: "https://media.example.test",
		BackendsJSON: `[{"url":"http://127.0.0.1:8096"}]`, Enabled: true,
		TrustedProxyRangesJSON: `["10.0.0.0/8"]`, PolicyRefJSON: `{"id":"shared-policy","overlay":{"site":"media"}}`,
	}}, false)
	l4Rules := snapshotL4Rules([]L4RuleRow{{
		ID: 2, AgentID: "edge-1", Protocol: "udp", ListenHost: "127.0.0.1", ListenPort: 9000,
		BackendsJSON: `[{"host":"127.0.0.1","port":9001}]`, Enabled: true,
		TuningJSON:    `{"proxy_protocol":{"decode":true,"trusted_peers":["192.0.2.0/24"]}}`,
		PolicyRefJSON: `{"id":"shared-policy"}`,
	}}, false)
	if len(httpRules) != 1 || httpRules[0].PolicyRef == nil || httpRules[0].PolicyRef.ID != "shared-policy" || string(httpRules[0].PolicyRef.Overlay) != `{"site":"media"}` {
		t.Fatalf("HTTP policy projection = %+v", httpRules)
	}
	if len(httpRules[0].TrustedProxyRanges) != 1 || httpRules[0].TrustedProxyRanges[0] != "10.0.0.0/8" {
		t.Fatalf("HTTP trusted proxy projection = %+v", httpRules[0].TrustedProxyRanges)
	}
	if len(l4Rules) != 1 || l4Rules[0].PolicyRef == nil || l4Rules[0].PolicyRef.ID != "shared-policy" {
		t.Fatalf("L4 policy projection = %+v", l4Rules)
	}
	if peers := l4Rules[0].Tuning.ProxyProtocol.TrustedPeers; len(peers) != 1 || peers[0] != "192.0.2.0/24" {
		t.Fatalf("L4 trusted peer projection = %+v", peers)
	}

	payload, err := json.Marshal(Snapshot{Rules: httpRules, L4Rules: l4Rules, PluginPolicies: []PluginPolicy{}})
	if err != nil {
		t.Fatalf("json.Marshal(Snapshot) error = %v", err)
	}
	if !strings.Contains(string(payload), `"plugin_policies":[]`) {
		t.Fatalf("snapshot does not carry explicit empty plugin_policies: %s", payload)
	}
}

func TestMalformedPersistedPolicyRefFailsClosedInSnapshot(t *testing.T) {
	rules := snapshotHTTPRules([]HTTPRuleRow{{
		ID: 1, FrontendURL: "https://media.example.test", BackendsJSON: `[{"url":"http://127.0.0.1:8096"}]`,
		Enabled: true, PolicyRefJSON: `{"id":`,
	}}, false)
	if len(rules) != 1 || rules[0].PolicyRef == nil || !strings.Contains(rules[0].PolicyRef.ID, "invalid") {
		t.Fatalf("malformed policy ref was silently dropped: %+v", rules)
	}
}
