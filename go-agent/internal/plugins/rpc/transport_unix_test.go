//go:build !windows

package rpc

import (
	"net"
	"os"
	"testing"
	"time"

	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
	"google.golang.org/grpc"
)

func TestRPCRealUnixRestartOwnsSocketLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("real Unix socket transport belongs to the full tier")
	}
	runtimeDirectory, err := os.MkdirTemp("/tmp", "nre-rpc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDirectory) })
	var previousAddress string
	for attemptNumber := 0; attemptNumber < 2; attemptNumber++ {
		security, err := provisionAttemptSecurity(runtimeDirectory, DialConfig{Network: "unix", Deadline: 2 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		if security.dial.Address == previousAddress {
			t.Fatal("restart reused Unix socket path")
		}
		previousAddress = security.dial.Address
		listener, err := net.Listen("unix", security.dial.Address)
		if err != nil {
			t.Fatal(err)
		}
		server := grpc.NewServer()
		server.RegisterService(agentAttemptServiceDesc(security.dial.Cookie), struct{}{})
		go server.Serve(listener)
		client, closeClient, err := Dial(t.Context(), security.dial)
		if err != nil {
			t.Fatal(err)
		}
		request := pluginsdk.RPCHandshakeRequest{ABI: pluginsdk.RPCABIV1, PluginID: "plugin", PluginVersion: "1", PackageDigest: "package", ArtifactDigest: "artifact", Generation: "g1", GrantedScopes: []string{"relay.read"}}
		if _, err := client.Handshake(t.Context(), request); err != nil {
			t.Fatal(err)
		}
		_ = closeClient()
		server.Stop()
		_ = listener.Close()
		if err := security.cleanup(); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(security.dial.Address); !os.IsNotExist(err) {
			t.Fatal("Unix socket survived attempt cleanup")
		}
	}
}
