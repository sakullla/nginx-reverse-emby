package core

import (
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
