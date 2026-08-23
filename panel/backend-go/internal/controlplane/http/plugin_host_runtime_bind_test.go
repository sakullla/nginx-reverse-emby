//go:build !integration

package http

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/pluginhost"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func TestProductionPluginCapabilityManagerBindsTaskAndRuleServices(t *testing.T) {
	t.Parallel()
	dataDir, err := os.MkdirTemp("", "nre-plugin-host-bind-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dataDir) })
	hostRoot, err := os.MkdirTemp("", "nre-plugin-host-runtime-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(hostRoot) })
	hostStore, err := storage.NewStore(storage.StoreConfig{Driver: "sqlite", DataRoot: hostRoot, LocalAgentID: "local"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = hostStore.Close() })
	rpcProcessHost, err := pluginhost.New(filepath.Join(dataDir, "plugins", "rpc-runtime"), nil, pluginhost.GRPCDialer{}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	runtimeHost, err := service.NewPluginRuntimeHost(rpcProcessHost, hostStore)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtimeHost.Close(t.Context()) })

	resolved, err := Dependencies{
		Config: config.Config{
			DataDir:      dataDir,
			LocalAgentID: "local",
		},
		PluginRuntimeHost: runtimeHost,
	}.withDefaults()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if resolved.cleanup != nil {
			_ = resolved.cleanup()
		}
	})
	manager, ok := resolved.PluginCapabilityService.(*service.PluginCapabilityManager)
	if !ok || manager == nil {
		t.Fatal("production composition did not install PluginCapabilityManager")
	}
	tasksBound, rulesBound := manager.HostRuntimeServicesBound()
	if !tasksBound || !rulesBound {
		t.Fatal("production composition left TaskService/RuleService unbound")
	}
	candidate := pluginhost.Candidate{
		InstanceID:      "control-1",
		ResourceGroupID: "default",
		Identity:        pluginhost.Identity{PluginID: "example.plugin", Generation: "generation-1"},
		Grants:          []string{pluginsdk.PermissionL4Rule},
	}
	payload, err := json.Marshal(pluginsdk.L4RuleRequest{
		Action: pluginsdk.L4RuleActionCreate, AgentID: "missing-agent", Protocol: pluginsdk.L4RuleProtocolTCP,
		ListenPort: 9000, Backends: []pluginsdk.L4RuleBackend{{Host: "127.0.0.1", Port: 9001}},
	})
	if err != nil {
		t.Fatal(err)
	}
	response := manager.DispatchPluginHostResource(t.Context(), candidate, pluginsdk.HostRuntimeCall{
		Operation:   pluginsdk.HostRuntimeL4Rule,
		OperationID: "l4-rule-bind-probe",
		Payload:     payload,
	})
	if response.Error == nil || response.Error.Code != pluginsdk.ErrorInvalidArgument {
		t.Fatalf("production composition left L4RuleService unbound: %v", response.Error)
	}
}
