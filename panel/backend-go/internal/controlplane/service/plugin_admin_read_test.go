package service

import (
	"context"
	"encoding/json"
	"errors"
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

func TestPluginReadsFilterRealMemberAcrossTwoGroups(t *testing.T) {
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
	plugins, err := service.ListForActor(t.Context(), member)
	if err != nil || len(plugins) != 1 || plugins[0].PluginID != "shared" {
		t.Fatalf("member list=%+v error=%v", plugins, err)
	}
	operations, err := service.OperationsForActor(t.Context(), "shared", member)
	if err != nil || len(operations) != 1 {
		t.Fatalf("member operations=%+v error=%v", operations, err)
	}
	var results map[string]any
	if err := json.Unmarshal(operations[0].AgentResults, &results); err != nil || results["edge-a"] == nil || results["edge-b"] != nil || operations[0].ActorID != "" {
		t.Fatalf("member operation projection=%s error=%v", operations[0].AgentResults, err)
	}
	if _, err := service.OperationsForActor(t.Context(), "hidden", member); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("hidden operations error=%v", err)
	}
}
