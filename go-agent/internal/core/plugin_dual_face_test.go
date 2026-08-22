//go:build !integration

package core

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func TestRuntimeApplyKeepsDualFaceExecutionGenerationWithoutHTTPBackend(t *testing.T) {
	generation := model.PluginGeneration{
		ID:              "generation-1",
		InstanceID:      "instance-1",
		PluginID:        "dual-face",
		PluginVersion:   "1.0.0",
		Runtime:         model.PluginRuntimeDescriptor{Kind: model.PluginRuntimeRPCService, ABI: model.PluginRPCABIV1, HostScope: "agent", Entry: "artifacts/plugin"},
		Config:          json.RawMessage(`{}`),
		ExtensionPoints: []string{"ui.route"},
		Target:          model.PluginTargetBinding{Kind: "agent", ID: "edge-a"},
	}
	next := model.Snapshot{
		DesiredVersion:     "v1",
		Revision:           1,
		PluginGenerations:  []model.PluginGeneration{generation},
		PluginDependencies: []model.PluginDependencyEdge{},
	}
	runtime := NewRuntime()
	if err := runtime.Apply(context.Background(), model.Snapshot{}, next); err != nil {
		t.Fatal(err)
	}
	got := runtime.ActiveSnapshot()
	if len(got.PluginGenerations) != 1 || got.PluginGenerations[0].PluginID != "dual-face" || got.PluginGenerations[0].Runtime.HostScope != "agent" {
		t.Fatalf("active generations = %+v", got.PluginGenerations)
	}
	if len(got.PluginGenerations[0].HTTPBackendProviders) != 0 {
		t.Fatal("execution generation required an HTTP backend-provider")
	}
	if len(got.PluginDependencies) != 0 {
		t.Fatalf("distribution created HTTP backend dependencies: %+v", got.PluginDependencies)
	}
	merged := MergeSnapshotPayload(model.Snapshot{Revision: 1}, got)
	if len(merged.PluginGenerations) != 1 || merged.PluginGenerations[0].PluginID != "dual-face" {
		t.Fatalf("merged generations = %+v", merged.PluginGenerations)
	}
}

func TestDualFaceAgentArtifactHostScopeMatchesGeneration(t *testing.T) {
	runtime := pluginsdk.Runtime{
		Kind:       pluginsdk.RuntimeRPCService,
		ABI:        pluginsdk.RPCABIV1,
		HostScope:  pluginsdk.HostScopeControlPlane,
		HostScopes: []string{pluginsdk.HostScopeControlPlane, pluginsdk.HostScopeAgent},
		Entry:      "artifacts/plugin",
	}
	generationHostScope := pluginsdk.RuntimeAgentFaceHostScope(runtime)
	if generationHostScope != pluginsdk.HostScopeAgent {
		t.Fatalf("agent snapshot host_scope = %q", generationHostScope)
	}
	if !pluginsdk.RuntimeDurableArtifactHostScopeMatches(generationHostScope, pluginsdk.HostScopeAgent) {
		t.Fatal("agent-face artifact host_scope must equal generation host_scope for issuance")
	}
	if pluginsdk.RuntimeDurableArtifactHostScopeMatches(generationHostScope, runtime.HostScope) {
		t.Fatal("primary control-plane artifact host_scope must not satisfy agent generation equality")
	}
}
