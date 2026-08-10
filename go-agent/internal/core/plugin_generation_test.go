package core_test

import (
	"context"
	"strings"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/core"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/module"
)

type pluginStatusModule struct{}

func (*pluginStatusModule) Name() string { return "plugin-status" }
func (*pluginStatusModule) Descriptor() module.ModuleDescriptor {
	return module.ModuleDescriptor{Name: "plugin-status"}
}
func (*pluginStatusModule) RegisterProviders(module.ProviderRegistry) error      { return nil }
func (*pluginStatusModule) Capabilities(module.SnapshotView) []module.Capability { return nil }
func (*pluginStatusModule) Apply(context.Context, module.ApplyRequest) error     { return nil }
func (*pluginStatusModule) Stop(context.Context) error                           { return nil }
func (*pluginStatusModule) Prepare(_ context.Context, request module.ApplyRequest) (module.ModuleTransaction, error) {
	generation := request.Next.PluginGenerations[0]
	return &pluginStatusTransaction{status: model.PluginRuntimeStatus{
		InstanceID: generation.InstanceID, PluginID: generation.PluginID, OperationID: generation.OperationID, Revision: generation.Revision,
		GenerationID: generation.ID, PackageDigest: generation.PackageDigest, ArtifactDigest: generation.Artifact.SHA256,
		ConfigVersion: generation.ConfigVersion, RuntimeKind: generation.Runtime.Kind, State: "degraded", Sequence: 1, ErrorCode: "rpc_runtime_failed",
	}}, nil
}

type pluginStatusTransaction struct{ status model.PluginRuntimeStatus }

func (*pluginStatusTransaction) Ready(context.Context) error   { return nil }
func (*pluginStatusTransaction) Destroy(context.Context) error { return nil }
func (*pluginStatusTransaction) Commit() error                 { return nil }
func (*pluginStatusTransaction) Rollback() error               { return nil }
func (t *pluginStatusTransaction) PluginRuntimeStatuses() []model.PluginRuntimeStatus {
	return []model.PluginRuntimeStatus{t.status}
}

func TestRuntimeStateCarriesFencedPluginGenerationStatus(t *testing.T) {
	registry := module.NewRegistry()
	if err := registry.Register(&pluginStatusModule{}); err != nil {
		t.Fatal(err)
	}
	runtime := core.NewRuntimeWithGenerationManager(core.NewGenerationManager(registry))
	snapshot := model.Snapshot{Revision: 9, PluginGenerations: []model.PluginGeneration{{
		ID: strings.Repeat("b", 64), InstanceID: "rpc-9", PluginID: "example.rpc", OperationID: "operation-9", Revision: 9,
		PackageDigest: strings.Repeat("a", 64), Artifact: model.PluginArtifactDescriptor{SHA256: strings.Repeat("c", 64)}, ConfigVersion: 3,
		Runtime: model.PluginRuntimeDescriptor{Kind: model.PluginRuntimeRPCService},
	}}}
	if err := runtime.Apply(t.Context(), model.Snapshot{}, snapshot); err != nil {
		t.Fatal(err)
	}
	state := runtime.State()
	if len(state.PluginStatuses) != 1 {
		t.Fatalf("plugin statuses = %+v", state.PluginStatuses)
	}
	status := state.PluginStatuses[0]
	if status.InstanceID != "rpc-9" || status.PluginID != "example.rpc" || status.OperationID != "operation-9" || status.Revision != 9 || status.GenerationID != strings.Repeat("b", 64) || status.PackageDigest != strings.Repeat("a", 64) || status.ArtifactDigest != strings.Repeat("c", 64) || status.ConfigVersion != 3 || status.Sequence != 1 || status.State != "degraded" {
		t.Fatalf("plugin status identity = %+v", status)
	}
}

var (
	_ module.TransactionalModule   = (*pluginStatusModule)(nil)
	_ module.GenerationTransaction = (*pluginStatusTransaction)(nil)
)
