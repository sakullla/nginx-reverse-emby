package rpc

import (
	"context"
	"encoding/json"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	managed "github.com/sakullla/nginx-reverse-emby/go-agent/internal/plugins/network"
	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// This child uses only the published SDK; all bytes cross the production private HTTP socket.
func TestManagedRuntimeSDKChild(t *testing.T) {
	if os.Getenv("NRE_TEST_MANAGED_CHILD") != "1" {
		return
	}
	client, err := sdk.NewHostRuntimeClientFromEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	var handle sdk.ManagedNetworkHandle
	if err := json.Unmarshal([]byte(os.Getenv("NRE_TEST_MANAGED_HANDLE")), &handle); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	stream, err := sdk.NewManagedTCPStream(ctx, client, handle)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	data, err := io.ReadAll(stream)
	if err != nil || string(data) != "from-peer" {
		t.Fatalf("read: %q %v", data, err)
	}
	if _, err := stream.Write([]byte("from-plugin")); err != nil {
		t.Fatal(err)
	}
	if err := stream.CloseWrite(ctx); err != nil {
		t.Fatal(err)
	}
	denied := sdk.ManagedNetworkRequest{Action: sdk.ManagedNetworkDial, Binding: handle.Binding, RequestID: "deny-dial", Endpoint: &sdk.ManagedNetworkEndpoint{Host: "127.0.0.1", Port: 9}, Protocol: "tcp", WaitMS: 100}
	if _, err := client.ManagedNetwork(ctx, denied); err == nil {
		t.Fatal("ungranted outbound accepted")
	}
	var udpListener sdk.ManagedNetworkHandle
	if err := json.Unmarshal([]byte(os.Getenv("NRE_TEST_UDP_HANDLE")), &udpListener); err != nil {
		t.Fatal(err)
	}
	udpFlow, err := client.ManagedNetwork(ctx, sdk.ManagedNetworkRequest{Action: sdk.ManagedNetworkAccept, Binding: handle.Binding, RequestID: "accept-udp", Handle: &udpListener, WaitMS: 1000})
	if err != nil {
		t.Fatal(err)
	}
	packet, err := client.ManagedNetwork(ctx, sdk.ManagedNetworkRequest{Action: sdk.ManagedNetworkReceive, Binding: handle.Binding, RequestID: "receive-udp", Handle: udpFlow.Handle, MaxBytes: sdk.ManagedNetworkMaxDatagramBytes, WaitMS: 1000})
	if err != nil || string(packet.Data) != "udp-peer" {
		t.Fatalf("UDP receive: %q %v", packet.Data, err)
	}
	for index, value := range []string{"udp-one", "udp-two"} {
		if _, err := client.ManagedNetwork(ctx, sdk.ManagedNetworkRequest{Action: sdk.ManagedNetworkSend, Binding: handle.Binding, RequestID: "send-udp-" + strconv.Itoa(index), Handle: udpFlow.Handle, Data: []byte(value), WaitMS: 1000}); err != nil {
			t.Fatal(err)
		}
	}
	wrong := handle
	wrong.Binding.Generation = "foreign-generation"
	if _, err := client.ManagedNetwork(ctx, sdk.ManagedNetworkRequest{Action: sdk.ManagedNetworkRead, Binding: wrong.Binding, RequestID: "deny-generation", Handle: &wrong, MaxBytes: 1, WaitMS: 100}); err == nil {
		t.Fatal("foreign generation accepted")
	}
}
func TestManagedRuntimeSDKProcessTransport(t *testing.T) {
	if testing.Short() {
		t.Skip("actual child process")
	}
	directory, err := os.MkdirTemp("", "nre-m-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(directory)
	cookie := "test-private-credential-01234567890123456789"
	cookiePath := filepath.Join(directory, "cookie")
	if err := os.WriteFile(cookiePath, []byte(cookie), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := managed.NewManager()
	defer manager.Close()
	host := &Host{managed: manager, revoked: map[string]model.PluginGenerationRevokeRequest{}}
	candidate := HostCandidate{InstanceID: "instance", Generation: "runtime-generation", Scopes: []string{sdk.PermissionManagedNetworkListen}, Grants: []model.PluginGrantProjection{{Name: sdk.PermissionManagedNetworkListen}}, services: &runtimeServices{}}
	attempt := &hostAttempt{cleanup: func() error { return nil }}
	environment, err := host.startHostRuntime(candidate, attemptSecurity{endpointDirectory: directory, dial: DialConfig{Cookie: cookie}}, attempt)
	if err != nil {
		t.Fatal(err)
	}
	defer attempt.cleanup()
	attempt.network.Activate()
	reservation, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := sdk.ManagedNetworkEndpoint{Host: "127.0.0.1", Port: reservation.Addr().(*net.TCPAddr).Port}
	reservation.Close()
	binding := attempt.network.Binding()
	listener, err := attempt.network.Handle(t.Context(), sdk.ManagedNetworkRequest{Action: sdk.ManagedNetworkListen, Binding: binding, RequestID: "listen", Endpoint: &address, Protocol: "tcp", MaxFlows: 2, IdleMS: 30000})
	if err != nil {
		t.Fatal(err)
	}
	peer, err := net.Dial("tcp", net.JoinHostPort(address.Host, strconv.Itoa(address.Port)))
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	peer.SetDeadline(time.Now().Add(10 * time.Second))
	flow, err := attempt.network.Handle(t.Context(), sdk.ManagedNetworkRequest{Action: sdk.ManagedNetworkAccept, Binding: binding, RequestID: "accept", Handle: listener.Handle, WaitMS: 1000})
	if err != nil {
		t.Fatal(err)
	}
	udpReservation, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	udpAddress := sdk.ManagedNetworkEndpoint{Host: "127.0.0.1", Port: udpReservation.LocalAddr().(*net.UDPAddr).Port}
	udpReservation.Close()
	udpListener, err := attempt.network.Handle(t.Context(), sdk.ManagedNetworkRequest{Action: sdk.ManagedNetworkListen, Binding: binding, RequestID: "listen-udp", Endpoint: &udpAddress, Protocol: "udp", MaxFlows: 2, IdleMS: 30000})
	if err != nil {
		t.Fatal(err)
	}
	udpPeer, err := net.Dial("udp", net.JoinHostPort(udpAddress.Host, strconv.Itoa(udpAddress.Port)))
	if err != nil {
		t.Fatal(err)
	}
	defer udpPeer.Close()
	udpPeer.SetDeadline(time.Now().Add(10 * time.Second))
	udpPeer.Write([]byte("udp-peer"))
	udpEncoded, _ := json.Marshal(udpListener.Handle)
	encoded, _ := json.Marshal(flow.Handle)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), executable, "-test.run=^TestManagedRuntimeSDKChild$")
	command.Env = append(os.Environ(), environment...)
	command.Env = append(command.Env, "NRE_PLUGIN_COOKIE_FILE="+cookiePath, "NRE_TEST_MANAGED_CHILD=1", "NRE_TEST_MANAGED_HANDLE="+string(encoded), "NRE_TEST_UDP_HANDLE="+string(udpEncoded))
	peer.Write([]byte("from-peer"))
	peer.(*net.TCPConn).CloseWrite()
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("SDK child failed: %v\n%s", err, output)
	}
	for _, value := range []string{"udp-one", "udp-two"} {
		buffer := make([]byte, 64)
		n, err := udpPeer.Read(buffer)
		if err != nil || string(buffer[:n]) != value {
			t.Fatalf("process UDP reply %q %v", buffer[:n], err)
		}
	}
	data, err := io.ReadAll(peer)
	if err != nil || string(data) != "from-plugin" {
		t.Fatalf("actual socket response: %q %v", data, err)
	}
}
