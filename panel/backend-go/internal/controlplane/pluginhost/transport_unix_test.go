//go:build !windows

package pluginhost

import (
	"net"
	"os"
	"testing"
	"time"

	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
	"google.golang.org/grpc"
)

func TestPluginHostRealUnixRestartOwnsSocketLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("real Unix socket transport belongs to the full tier")
	}
	runtimeDirectory, err := os.MkdirTemp("/tmp", "nre-pluginhost-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDirectory) })
	var previousAddress string
	for attemptNumber := 0; attemptNumber < 2; attemptNumber++ {
		security, err := provisionControlAttemptSecurity(runtimeDirectory, Endpoint{Network: "unix"})
		if err != nil {
			t.Fatal(err)
		}
		if security.endpoint.Address == previousAddress {
			t.Fatal("restart reused Unix socket path")
		}
		previousAddress = security.endpoint.Address
		listener, err := net.Listen("unix", security.endpoint.Address)
		if err != nil {
			t.Fatal(err)
		}
		server := grpc.NewServer()
		server.RegisterService(controlAttemptServiceDesc(security.endpoint.Cookie), struct{}{})
		go server.Serve(listener)
		client, closer, err := (GRPCDialer{}).Dial(t.Context(), security.endpoint, 2*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		request := pluginsdk.RPCHandshakeRequest{ABI: pluginsdk.RPCABIV1, PluginID: "plugin", PluginVersion: "1", PackageDigest: "package", ArtifactDigest: "artifact", Generation: "g1", GrantedScopes: []string{"relay.read"}}
		response, err := client.Handshake(t.Context(), request)
		if err != nil || validateHandshake(request, response) != nil {
			t.Fatalf("real Unix handshake failed: %v", err)
		}
		_ = closer.Close()
		server.Stop()
		_ = listener.Close()
		if err := security.cleanup(); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(security.endpoint.Address); !os.IsNotExist(err) {
			t.Fatal("Unix socket survived attempt cleanup")
		}
	}
}
