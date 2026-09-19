//go:build linux && integration

package network

import (
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/model"
	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
	"io"
	"net"
	"testing"
	"time"
)

func TestIntegrationManagedIPv6SocketGrant(t *testing.T) {
	reservation, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Fatal("required Linux IPv6 listener unavailable:", err)
	}
	endpoint := sdk.ManagedNetworkEndpoint{Host: "::1", Port: reservation.Addr().(*net.TCPAddr).Port}
	reservation.Close()
	selector := "tcp://" + endpointString(endpoint)
	if !model.ValidManagedNetworkEndpointSelector(sdk.PermissionManagedNetworkListen, selector) {
		t.Fatal("model rejected IPv6 endpoint")
	}
	manager := NewManager()
	defer manager.Close()
	owner := testOwner(t, manager, "instance", "ipv6-generation", false)
	owner.authority.Grants = []model.PluginGrantProjection{{Name: sdk.PermissionManagedNetworkListen, ResourceKind: "network-endpoint", ResourceID: selector}}
	owner.Activate()
	listening := call(t, owner, sdk.ManagedNetworkRequest{Action: sdk.ManagedNetworkListen, Endpoint: &endpoint, Protocol: "tcp", MaxFlows: 2, IdleMS: 1000})
	peer, err := net.Dial("tcp6", endpointString(endpoint))
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	peer.SetDeadline(time.Now().Add(time.Second))
	peer.Write([]byte("ipv6"))
	flow := call(t, owner, sdk.ManagedNetworkRequest{Action: sdk.ManagedNetworkAccept, Handle: listening.Handle, WaitMS: 1000})
	if flow.Source.Source.Host != "::1" || flow.Source.Peer.Host != "::1" {
		t.Fatal("lost actual IPv6 socket source")
	}
	data := call(t, owner, sdk.ManagedNetworkRequest{Action: sdk.ManagedNetworkRead, Handle: flow.Handle, MaxBytes: 4, WaitMS: 1000})
	if string(data.Data) != "ipv6" {
		t.Fatal("IPv6 payload differs")
	}
	call(t, owner, sdk.ManagedNetworkRequest{Action: sdk.ManagedNetworkWrite, Handle: flow.Handle, Data: data.Data, WaitMS: 1000})
	buffer := make([]byte, 4)
	if _, err := io.ReadFull(peer, buffer); err != nil || string(buffer) != "ipv6" {
		t.Fatal("IPv6 reply failed:", err)
	}
	foreign := endpoint
	foreign.Host = "127.0.0.1"
	_, err = owner.Handle(t.Context(), sdk.ManagedNetworkRequest{Action: sdk.ManagedNetworkListen, Binding: owner.Binding(), RequestID: "foreign-endpoint", Endpoint: &foreign, Protocol: "tcp", MaxFlows: 2, IdleMS: 1000})
	if err == nil {
		t.Fatal("IPv6 endpoint grant authorized unrelated IPv4 bind")
	}
}
