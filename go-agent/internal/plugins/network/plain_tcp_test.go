package network

import (
	"net"
	"testing"
	"time"

	sdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

func TestManagedTCPListenerStaysPlain(t *testing.T) {
	listenConfig := managedTCPListenConfig()
	if listenConfig.MultipathTCP() {
		t.Fatal("managed TCP listener enables MPTCP")
	}

	manager := NewManager()
	defer manager.Close()
	owner := testOwner(t, manager, "instance", "plain-tcp", false)
	address := endpoint(t)
	listening := call(t, owner, sdk.ManagedNetworkRequest{
		Action:   sdk.ManagedNetworkListen,
		Endpoint: &address,
		Protocol: "tcp",
		MaxFlows: 2,
		IdleMS:   30000,
	})
	owner.Activate()

	var dialer net.Dialer
	dialer.SetMultipathTCP(true)
	peer, err := dialer.Dial("tcp", endpointString(address))
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	peer.SetDeadline(time.Now().Add(2 * time.Second))

	flow := call(t, owner, sdk.ManagedNetworkRequest{
		Action: sdk.ManagedNetworkAccept,
		Handle: listening.Handle,
		WaitMS: 1000,
	})
	if _, err := peer.Write([]byte("plain")); err != nil {
		t.Fatal(err)
	}
	data := call(t, owner, sdk.ManagedNetworkRequest{
		Action:   sdk.ManagedNetworkRead,
		Handle:   flow.Handle,
		MaxBytes: 16,
		WaitMS:   1000,
	})
	if string(data.Data) != "plain" {
		t.Fatalf("payload = %q", data.Data)
	}
	assertPlainTCPListener(t, manager, "tcp://"+endpointString(address))
}
