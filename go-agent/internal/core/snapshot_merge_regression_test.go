package core

import (
	"context"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
)

func TestMergeSnapshotPayloadPreservesDDNSConfig(t *testing.T) {
	previous := model.Snapshot{DDNSConfig: &model.DDNSExtractConfig{
		Enabled: true,
		Domain:  "media.example.com",
		IPv4:    model.DDNSFamily{Enabled: true, Source: "public_api"},
	}}
	merged := MergeSnapshotPayload(model.Snapshot{Revision: 7}, previous)
	if merged.DDNSConfig == nil || merged.DDNSConfig.Domain != "media.example.com" {
		t.Fatalf("merged DDNS config = %+v", merged.DDNSConfig)
	}

	cloned := cloneSnapshot(merged)
	cloned.DDNSConfig.Domain = "changed.example.com"
	if merged.DDNSConfig.Domain != "media.example.com" {
		t.Fatal("runtime snapshot clone retained the DDNS config pointer")
	}
}

func TestRuntimePublicationDeepClonesTrustedSourceAllowlists(t *testing.T) {
	snapshot := model.Snapshot{
		Revision: 1,
		Rules: []model.HTTPRule{{
			TrustedProxyRanges: []string{"192.0.2.0/24"},
		}},
		L4Rules: []model.L4Rule{{
			Tuning: model.L4Tuning{ProxyProtocol: model.L4ProxyProtocolTuning{
				TrustedPeers: []string{"198.51.100.10"},
			}},
		}},
	}
	runtime := NewRuntime()
	if err := runtime.Apply(context.Background(), model.Snapshot{}, snapshot); err != nil {
		t.Fatal(err)
	}

	snapshot.Rules[0].TrustedProxyRanges[0] = "203.0.113.0/24"
	snapshot.L4Rules[0].Tuning.ProxyProtocol.TrustedPeers[0] = "203.0.113.10"
	first := runtime.ActiveSnapshot()
	first.Rules[0].TrustedProxyRanges[0] = "198.51.100.0/24"
	first.L4Rules[0].Tuning.ProxyProtocol.TrustedPeers[0] = "198.51.100.20"

	second := runtime.ActiveSnapshot()
	if got := second.Rules[0].TrustedProxyRanges[0]; got != "192.0.2.0/24" {
		t.Fatalf("published HTTP trusted proxy range = %q", got)
	}
	if got := second.L4Rules[0].Tuning.ProxyProtocol.TrustedPeers[0]; got != "198.51.100.10" {
		t.Fatalf("published L4 trusted PROXY peer = %q", got)
	}
}

func TestMergeAndCloneSnapshotPreserveIsolatedPluginPolicies(t *testing.T) {
	previous := model.Snapshot{PluginPolicies: []model.PluginPolicy{{
		ID: "edge-policy", Revision: 1,
		Stages: []model.PolicyStage{{
			Kind: model.PolicyKindWAF, PolicyID: "waf", InstanceID: "waf-instance",
			ExtensionPoints: []string{"http.request"}, GrantedScopes: []string{"http.inspect"},
			Config: []byte(`{"mode":"observe"}`),
		}},
	}}}
	merged := MergeSnapshotPayload(model.Snapshot{Revision: 7}, previous)
	if len(merged.PluginPolicies) != 1 || merged.PluginPolicies[0].ID != "edge-policy" {
		t.Fatalf("merged plugin policies = %+v", merged.PluginPolicies)
	}

	cloned := cloneSnapshot(merged)
	cloned.PluginPolicies[0].Stages[0].ExtensionPoints[0] = "changed"
	cloned.PluginPolicies[0].Stages[0].GrantedScopes[0] = "changed"
	cloned.PluginPolicies[0].Stages[0].Config[0] = 'x'
	if got := merged.PluginPolicies[0].Stages[0]; got.ExtensionPoints[0] != "http.request" || got.GrantedScopes[0] != "http.inspect" || string(got.Config) != `{"mode":"observe"}` {
		t.Fatalf("runtime snapshot clone retained plugin policy backing storage: %+v", got)
	}
}

func TestMergeSnapshotPayloadPreservesOmittedPluginGenerations(t *testing.T) {
	previous := model.Snapshot{PluginGenerations: []model.PluginGeneration{{ID: "generation-1", InstanceID: "instance-1"}}}
	merged := MergeSnapshotPayload(model.Snapshot{Revision: 7}, previous)
	if len(merged.PluginGenerations) != 1 || merged.PluginGenerations[0].ID != "generation-1" {
		t.Fatalf("merged plugin generations = %+v", merged.PluginGenerations)
	}

	explicit := MergeSnapshotPayload(model.Snapshot{Revision: 8, PluginGenerations: []model.PluginGeneration{}}, previous)
	if explicit.PluginGenerations == nil || len(explicit.PluginGenerations) != 0 {
		t.Fatalf("explicit empty plugin generations = %#v", explicit.PluginGenerations)
	}
}
