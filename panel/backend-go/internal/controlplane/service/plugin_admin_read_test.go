package service

import (
	"context"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/authz"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
)

type pluginScopedReadStore struct {
	pluginLifecycleStore
	installed  []storage.InstalledPluginRow
	instances  map[string][]storage.PluginInstanceRow
	operations map[string][]storage.PluginOperationRow
}

func (s *pluginScopedReadStore) ListInstalledPlugins(context.Context) ([]storage.InstalledPluginRow, error) {
	return s.installed, nil
}
func (s *pluginScopedReadStore) ListPluginInstances(_ context.Context, pluginID string) ([]storage.PluginInstanceRow, error) {
	return s.instances[pluginID], nil
}
func (s *pluginScopedReadStore) ListPluginOperations(_ context.Context, pluginID string) ([]storage.PluginOperationRow, error) {
	return s.operations[pluginID], nil
}
func (*pluginScopedReadStore) LocalAgentBuild(context.Context) (string, string, bool, error) {
	return "local", "1.0.0", true, nil
}

func TestPluginReadsFailClosedWithoutStableStorageTransaction(t *testing.T) {
	store := &pluginScopedReadStore{
		installed: []storage.InstalledPluginRow{{PluginID: "shared"}, {PluginID: "hidden"}, {PluginID: "empty"}},
		instances: map[string][]storage.PluginInstanceRow{
			"shared": {{ID: "visible-instance", PluginID: "shared", ResourceGroupID: "group-a", TargetJSON: `["edge-a"]`}, {ID: "hidden-instance", PluginID: "shared", ResourceGroupID: "group-b", TargetJSON: `["edge-b"]`}},
			"hidden": {{ID: "hidden-only", PluginID: "hidden", ResourceGroupID: "group-b", TargetJSON: `["edge-b"]`}},
		},
		operations: map[string][]storage.PluginOperationRow{"shared": {{ID: "operation", PluginID: "shared", AgentResultsJSON: `{"edge-a":{"state":"active"},"edge-b":{"state":"failed"}}`}}},
	}
	service := NewPluginService(store, "")
	member := authz.Actor{ID: "member", Permissions: []string{authz.PermissionResourceRead}, VisibleResourceGroups: []string{"group-a"}}
	if plugins, err := service.ListForActor(t.Context(), member); err == nil || plugins != nil {
		t.Fatalf("member list=%+v error=%v, want stable transaction failure", plugins, err)
	}
}
