//go:build !integration

package rpc

import (
	"encoding/json"
	"io"
	"path/filepath"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	pluginprocess "github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/process"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func TestNewHostUsesIdentityAwareDefaultProvisioning(t *testing.T) {
	supervisor := pluginprocess.NewSupervisor(hostRunner{}, hostTestSandbox{}, io.Discard)
	host, err := NewHost(pluginprocess.Installer{RuntimeRoot: t.TempDir()}, supervisor, nil)
	if err != nil {
		t.Fatal(err)
	}
	if host.provision != nil {
		t.Fatal("default Host provisioner bypasses candidate storage identity")
	}
}

func TestPluginStorageDirectoryBindingsResolveAuthorizedConfigPath(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "share")
	other := filepath.Join(base, "other")
	config, err := json.Marshal(map[string]any{"storage": map[string]any{"roots": []string{other, root, root}}})
	if err != nil {
		t.Fatal(err)
	}
	grants := []model.PluginGrantProjection{
		{Name: pluginsdk.PermissionStorageRead, ResourceKind: pluginsdk.StorageResourceConfigPath, ResourceID: "/storage/roots"},
		{Name: pluginsdk.PermissionStorageWrite, ResourceKind: pluginsdk.StorageResourceConfigPath, ResourceID: "/storage/roots"},
		{Name: pluginsdk.PermissionStorageWrite, ResourceKind: "resource", ResourceID: "unrelated"},
	}
	bindings, err := pluginStorageDirectoryBindings(grants, config)
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 2 || bindings[0].HostPath != other || bindings[1].HostPath != root || bindings[0].ReadOnly || bindings[1].ReadOnly {
		t.Fatalf("bindings = %#v", bindings)
	}
}

func TestPluginStorageDirectoryBindingsFailClosed(t *testing.T) {
	grant := []model.PluginGrantProjection{{Name: pluginsdk.PermissionStorageWrite, ResourceKind: pluginsdk.StorageResourceConfigPath, ResourceID: "/root_path"}}
	for name, config := range map[string]json.RawMessage{
		"non-string":  []byte(`{"root_path":42}`),
		"relative":    []byte(`{"root_path":"relative"}`),
		"mixed-array": []byte(`{"root_path":["/valid",42]}`),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := pluginStorageDirectoryBindings(grant, config); err == nil {
				t.Fatal("invalid storage binding was accepted")
			}
		})
	}
	bindings, err := pluginStorageDirectoryBindings(grant, []byte(`{}`))
	if err != nil || len(bindings) != 0 {
		t.Fatalf("optional absent binding = %#v, %v", bindings, err)
	}
}
