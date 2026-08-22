//go:build !integration

package http

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/config"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/pluginhost"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/storage"
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
}
