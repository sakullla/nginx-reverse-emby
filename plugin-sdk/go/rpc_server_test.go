package pluginsdk

import (
	"context"
	"testing"
)

type rpcLifecycleFixture struct{}

func (rpcLifecycleFixture) Handshake(context.Context, RPCHandshakeRequest) (RPCHandshakeResponse, error) {
	return RPCHandshakeResponse{ABI: RPCABIV1}, nil
}
func (rpcLifecycleFixture) Prepare(context.Context, LifecycleRequest) LifecycleResponse {
	return LifecycleResponse{Success: &LifecycleSuccess{Ready: true}}
}
func (rpcLifecycleFixture) Activate(context.Context, LifecycleRequest) LifecycleResponse {
	return LifecycleResponse{Success: &LifecycleSuccess{Ready: true}}
}
func (rpcLifecycleFixture) Stop(context.Context, LifecycleRequest) LifecycleResponse {
	return LifecycleResponse{Success: &LifecycleSuccess{Ready: true}}
}

func TestServeRPCPluginRejectsMissingTransportBeforeListening(t *testing.T) {
	t.Setenv(EnvPluginEndpoint, "")
	t.Setenv(EnvPluginCookieFile, "")
	if err := ServeRPCPlugin(t.Context(), rpcLifecycleFixture{}); err == nil {
		t.Fatal("missing canonical RPC transport was accepted")
	}
}

func TestRPCPluginServiceDeclaresCanonicalLifecycleMethods(t *testing.T) {
	description := rpcLifecycleServiceDesc("cookie", rpcLifecycleFixture{})
	want := []string{"Handshake", "Prepare", "Activate", "Stop"}
	if description.ServiceName != rpcServiceName || len(description.Methods) != len(want) {
		t.Fatalf("service = %#v", description)
	}
	for index, method := range description.Methods {
		if method.MethodName != want[index] {
			t.Fatalf("method %d = %q, want %q", index, method.MethodName, want[index])
		}
	}
}
