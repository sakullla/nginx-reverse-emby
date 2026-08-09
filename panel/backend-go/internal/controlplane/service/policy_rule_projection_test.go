package service

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

func TestHTTPRulePolicyAndTrustedProxyRoundTrip(t *testing.T) {
	rule := HTTPRule{
		ID: 1, AgentID: "edge-1", FrontendURL: "https://media.example.test",
		Backends: []HTTPRuleBackend{{URL: "http://127.0.0.1:8096"}}, Enabled: true,
		TrustedProxyRanges: []string{"10.0.0.0/8"},
		PolicyRef:          &storage.PolicyRef{ID: "shared-policy", Overlay: json.RawMessage(`{"site":"media"}`)},
	}
	projected := httpRuleFromRow(httpRuleToRow(rule))
	if !reflect.DeepEqual(projected.TrustedProxyRanges, rule.TrustedProxyRanges) {
		t.Fatalf("trusted proxy ranges = %+v", projected.TrustedProxyRanges)
	}
	if projected.PolicyRef == nil || projected.PolicyRef.ID != "shared-policy" || string(projected.PolicyRef.Overlay) != `{"site":"media"}` {
		t.Fatalf("policy ref = %+v", projected.PolicyRef)
	}

	normalized, err := normalizeTrustedPeerRanges([]string{"10.0.0.1", "10.0.0.0/8", "10.0.0.0/8"})
	if err != nil {
		t.Fatalf("normalizeTrustedPeerRanges() error = %v", err)
	}
	if !reflect.DeepEqual(normalized, []string{"10.0.0.0/8", "10.0.0.1/32"}) {
		t.Fatalf("normalized ranges = %+v", normalized)
	}
	if _, err := normalizeTrustedPeerRanges([]string{"not-a-range"}); err == nil {
		t.Fatal("normalizeTrustedPeerRanges() accepted invalid input")
	}
}

func TestL4RulePolicyAndTrustedPROXYRoundTrip(t *testing.T) {
	rule := L4Rule{
		ID: 2, AgentID: "edge-1", Protocol: "tcp", ListenHost: "127.0.0.1", ListenPort: 9000,
		Backends: []L4Backend{{Host: "127.0.0.1", Port: 9001}}, Enabled: true,
		Tuning:    L4Tuning{ProxyProtocol: L4ProxyProtocolTuning{Decode: true, TrustedPeers: []string{"192.0.2.0/24"}}},
		PolicyRef: &storage.PolicyRef{ID: "shared-policy"},
	}
	projected := l4RuleFromRow(l4RuleToRow(rule))
	if peers := projected.Tuning.ProxyProtocol.TrustedPeers; !reflect.DeepEqual(peers, []string{"192.0.2.0/24"}) {
		t.Fatalf("trusted PROXY peers = %+v", peers)
	}
	if projected.PolicyRef == nil || projected.PolicyRef.ID != "shared-policy" {
		t.Fatalf("policy ref = %+v", projected.PolicyRef)
	}

	backup := backupL4RuleFromRule(projected)
	restored := l4RuleInputFromBackup(backup, map[int]int{}, nil)
	if restored.PolicyRef == nil || restored.PolicyRef.ID != "shared-policy" || restored.Tuning == nil || !reflect.DeepEqual(restored.Tuning.ProxyProtocol.TrustedPeers, []string{"192.0.2.0/24"}) {
		t.Fatalf("backup policy projection = %+v", restored)
	}
}

func TestRulePolicyRefValidationSupportsExplicitClear(t *testing.T) {
	fallback := &storage.PolicyRef{ID: "shared-policy", Overlay: json.RawMessage(`{"site":"media"}`)}
	preserved, err := normalizeRulePolicyRef(nil, fallback)
	if err != nil || preserved == fallback || !reflect.DeepEqual(preserved, fallback) {
		t.Fatalf("preserved policy ref = %+v, error = %v", preserved, err)
	}
	cleared, err := normalizeRulePolicyRef(&storage.PolicyRef{}, fallback)
	if err != nil || cleared != nil {
		t.Fatalf("cleared policy ref = %+v, error = %v", cleared, err)
	}
	if _, err := normalizeRulePolicyRef(&storage.PolicyRef{ID: " shared-policy"}, nil); err == nil {
		t.Fatal("normalizeRulePolicyRef() accepted noncanonical id")
	}
}
