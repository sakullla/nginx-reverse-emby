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

func TestPluginScopedOperationAgentResultsUseImmutableInstanceScopes(t *testing.T) {
	actor := authz.Actor{ID: "member", Permissions: []string{authz.PermissionResourceRead}, VisibleResourceGroups: []string{"group-a"}}
	scopes := []PluginOperationScopeDetail{{InstanceID: "visible", ResourceGroupID: "group-a"}, {InstanceID: "hidden", ResourceGroupID: "group-b"}}
	visible, results, err := pluginScopedOperationAgentResults(json.RawMessage(`{"shared-agent/visible":{"state":"active"},"shared-agent/hidden":{"state":"failed"}}`), scopes, actor)
	if err != nil || len(visible) != 1 || visible[0].InstanceID != "visible" || string(results) != `{"shared-agent/visible":{"state":"active"}}` {
		t.Fatalf("visible=%+v results=%s err=%v", visible, results, err)
	}
	for label, raw := range map[string]json.RawMessage{
		"bare agent":       json.RawMessage(`{"shared-agent":{"state":"active"}}`),
		"unknown instance": json.RawMessage(`{"shared-agent/missing":{"state":"active"}}`),
		"extra separator":  json.RawMessage(`{"shared-agent/visible/extra":{"state":"active"}}`),
		"non-object":       json.RawMessage(`[]`),
	} {
		if _, _, err := pluginScopedOperationAgentResults(raw, scopes, actor); !errors.Is(err, ErrPluginReadProjection) {
			t.Fatalf("%s error=%v", label, err)
		}
	}
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
