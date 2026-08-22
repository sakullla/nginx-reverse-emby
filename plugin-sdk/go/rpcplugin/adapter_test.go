package rpcplugin

import (
	"testing"
	"time"

	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func TestAdapterOwnsLifecycleForwardingAndHandshakeObservation(t *testing.T) {
	adapter, err := NewAdapter(Config{
		PluginID: "example", PluginVersion: "1.0.0",
		RequiredGrants: []string{"resource.read"},
		Timeouts:       Timeouts{Prepare: time.Second, Activate: time.Second, Stop: time.Second, Drain: time.Second},
	}, HookFuncs{})
	if err != nil {
		t.Fatal(err)
	}
	request := pluginsdk.RPCHandshakeRequest{
		ABI: pluginsdk.RPCABIV1, PluginID: "example", PluginVersion: "1.0.0",
		PackageDigest: "package", ArtifactDigest: "artifact",
		GrantedScopes: []string{"resource.read"}, Generation: "generation",
	}
	if _, err := adapter.Handshake(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	observed, ok := adapter.Request()
	if !ok || observed.Generation != request.Generation {
		t.Fatalf("stored request = %#v/%t", observed, ok)
	}
	if response := adapter.Prepare(t.Context(), pluginsdk.LifecycleRequest{Generation: request.Generation, Config: []byte(`{}`)}); response.Error != nil {
		t.Fatalf("prepare = %#v", response)
	}
	if response := adapter.Activate(t.Context(), pluginsdk.LifecycleRequest{Generation: request.Generation}); response.Error != nil {
		t.Fatalf("activate = %#v", response)
	}
	if response := adapter.Stop(t.Context(), pluginsdk.LifecycleRequest{Generation: request.Generation}); response.Error != nil {
		t.Fatalf("stop = %#v", response)
	}
}
