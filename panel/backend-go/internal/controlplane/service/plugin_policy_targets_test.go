//go:build !integration

package service

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/plugins"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func TestNestedPolicyExecutionTargets(t *testing.T) {
	t.Run("nested agent policy", func(t *testing.T) {
		store := newHTTPWAFAttachStore(t)
		for _, id := range []string{"local", "edge-a"} {
			if err := store.SaveAgent(t.Context(), storage.AgentRow{ID: id, Name: id, Version: "1.0.0", CapabilitiesJSON: `["package_manifest_v1"]`}); err != nil {
				t.Fatal(err)
			}
		}
		service := NewPluginService(store, t.TempDir())
		manifest := plugins.Manifest{ID: "waf", Compatibility: plugins.Compatibility{Host: "*", Agent: "*"}, Runtime: pluginsdk.Runtime{
			Kind: pluginsdk.RuntimeRPCService, ABI: pluginsdk.RPCABIV1,
			HostScope: pluginsdk.HostScopeControlPlane, PolicyKind: "waf",
			Policy: &pluginsdk.RuntimePolicy{
				Kind: pluginsdk.RuntimeWASMPolicy, ABI: pluginsdk.PolicyABIV1,
				HostScope: pluginsdk.HostScopeAgent, Entry: "artifacts/waf.wasm",
			},
		}}
		faces, eligibility, err := service.pluginDeploymentProjection(t.Context(), manifest)
		if err != nil {
			t.Fatal(err)
		}
		want := []PluginDeploymentFace{{FaceID: "local-management", HostScope: "control-plane"}, {FaceID: "agent-execution", HostScope: "agent"}}
		if !reflect.DeepEqual(faces, want) || !eligibility.AgentTargetsAllowed {
			t.Errorf("nested policy deployment projection = %+v, %+v", faces, eligibility)
		}
		if err := service.validatePluginTargets(t.Context(), manifest, json.RawMessage(`["edge-a"]`)); err != nil {
			t.Errorf("nested policy must accept a compatible execution target: %v", err)
		}
		if err := service.validatePluginTargets(t.Context(), manifest, json.RawMessage(`["missing-agent"]`)); err == nil {
			t.Error("nested policy accepted a missing execution target")
		}
		if targets, err := pluginConfiguredTargetIDs(manifest, json.RawMessage(`[]`), "local"); err != nil || len(targets) != 0 {
			t.Errorf("empty execution targets became management targets: %v, %v", targets, err)
		}
		if pluginsdk.RuntimeProjectsAgentRPC(manifest.Runtime) {
			t.Fatal("nested policy must not become an Agent RPC process")
		}
	})
}
